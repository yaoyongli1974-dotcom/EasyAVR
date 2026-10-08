package ehome

import (
	"encoding/hex"
	"fmt"
	"log"
	"net"
	"regexp"
	"time"

	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/access"
	"github.com/easyavr/easyavr/internal/config"
	"github.com/easyavr/easyavr/internal/model"
)

// Backend is a device-access engine for Hikvision EHOME/ISUP. The default build
// uses the pure-Go UDP listener below; an optional cgo backend that drives
// Hikvision's native EHOME/ISUP SDK is enabled with the `ehome_sdk` build tag.
type Backend interface {
	Start() error
	Stop()
	Running() bool
}

var deviceIDPattern = regexp.MustCompile(`\d{12,20}`)

// udpBackend binds the CMS UDP port, logs datagrams and records any device id it
// can identify. It does NOT implement the proprietary EHOME binary protocol; it
// exists so the integration point works without the vendor SDK.
type udpBackend struct {
	cfg  config.EHOMEConfig
	db   *gorm.DB
	conn *net.UDPConn
}

func newUDPBackend(cfg config.EHOMEConfig, db *gorm.DB) *udpBackend {
	return &udpBackend{cfg: cfg, db: db}
}

// Start binds the CMS UDP port and begins reading.
func (s *udpBackend) Start() error {
	addr, err := net.ResolveUDPAddr("udp", s.cfg.CMSListen)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}
	s.conn = conn
	go s.readLoop()
	log.Printf("[ehome] ISUP CMS listening on %s (sms=%s public=%s)", s.cfg.CMSListen, s.cfg.SMSListen, s.cfg.PublicIP)
	return nil
}

func (s *udpBackend) Stop() {
	if s.conn != nil {
		s.conn.Close()
		s.conn = nil
	}
}

func (s *udpBackend) Running() bool { return s.conn != nil }

func (s *udpBackend) readLoop() {
	buf := make([]byte, 64*1024)
	for {
		n, addr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		payload := make([]byte, n)
		copy(payload, buf[:n])
		log.Printf("[ehome] packet from %s (%d bytes): %s", addr, n, hex.EncodeToString(payload[:min(n, 32)]))
		s.identify(payload, addr)
	}
}

// identify best-effort records a device when an ASCII device id is present.
func (s *udpBackend) identify(payload []byte, addr *net.UDPAddr) {
	id := string(deviceIDPattern.Find(payload))
	if id == "" {
		return
	}
	if allowed, reason := access.Check(s.db, "EHOME", id, "", addr.IP.String(), addr.Port); !allowed {
		log.Printf("[ehome] device %s from %s denied: %s", id, addr, reason)
		return
	}
	var dev model.Device
	err := s.db.Where("protocol = ? AND name = ?", "ehome", id).First(&dev).Error
	if err == gorm.ErrRecordNotFound {
		dev = model.Device{
			Name: id, Protocol: "ehome", AccessMode: "register",
			IP: addr.IP.String(), Status: "online",
		}
		s.db.Create(&dev)
		s.db.Create(&model.Channel{
			DeviceID: dev.ID, Name: id + "-main", StreamType: "main",
			StreamKey: "eh_" + fmt.Sprintf("%d", time.Now().UnixNano()%1e9), Enabled: true,
		})
		log.Printf("[ehome] discovered device %s from %s", id, addr)
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
