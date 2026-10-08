package gb28181

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"log"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"github.com/tjfoc/gmsm/gmtls"
	gmx509 "github.com/tjfoc/gmsm/x509"

	"github.com/easyavr/easyavr/internal/config"
	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/video"
)

// Server is a minimal GB28181 SIP signaling server supporting UDP and
// GB35114 secure SIP over GM/T 0024 TLS.
type Server struct {
	cfg config.GBConfig
	db  *gorm.DB
	zlm *video.Client

	conn    *net.UDPConn
	tlsLn   net.Listener
	tlsAddr string
	sn      atomic.Int64
	cseq    atomic.Int64

	mu       sync.Mutex
	sessions map[string]*session      // deviceID -> registration
	pending  map[string]chan *Message // callID -> outgoing request responses
	streams  map[uint]*liveStream     // channelID -> active live call

	cascadeMu sync.Mutex
	cascades  map[uint]context.CancelFunc // cascadeID -> stop func

	sink EventSink
}

// EventSink receives events produced by signaling (alarms). Implemented by the
// server layer to drive notifications and semantic indexing.
type EventSink interface {
	OnEvent(model.AIEvent)
}

// SetSink attaches an event sink.
func (s *Server) SetSink(sink EventSink) { s.sink = sink }

// Peer identifies the transport endpoint a SIP message arrived from and can be
// replied to. Exactly one of UDP or Conn is set.
type Peer struct {
	Transport string // UDP | TLS
	UDP       *net.UDPAddr
	Conn      net.Conn
	wmu       sync.Mutex
}

// IP returns the peer's remote IP.
func (p *Peer) IP() string {
	if p.Conn != nil {
		if host, _, err := net.SplitHostPort(p.Conn.RemoteAddr().String()); err == nil {
			return host
		}
		return p.Conn.RemoteAddr().String()
	}
	if p.UDP != nil {
		return p.UDP.IP.String()
	}
	return ""
}

// Port returns the peer's remote port.
func (p *Peer) Port() int {
	if p.Conn != nil {
		if _, port, err := net.SplitHostPort(p.Conn.RemoteAddr().String()); err == nil {
			n, _ := strconv.Atoi(port)
			return n
		}
		return 0
	}
	if p.UDP != nil {
		return p.UDP.Port
	}
	return 0
}

func (p *Peer) String() string { return fmt.Sprintf("%s:%d", p.IP(), p.Port()) }

type session struct {
	DeviceID      string
	Peer          *Peer
	Transport     string
	Expires       int
	LastKeepalive time.Time
}

type liveStream struct {
	ChannelID uint
	DeviceID  string
	StreamKey string
	SSRC      string
	RTPPort   int
	CallID    string
	FromTag   string
	ToTag     string
	CSeq      int
}

// TLSConfig configures the GB35114 secure SIP listener.
type TLSConfig struct {
	Listen            string
	SignCertPEM       []byte
	SignKeyPEM        []byte
	EncCertPEM        []byte
	EncKeyPEM         []byte
	CAPool            *gmx509.CertPool
	RequireClientCert bool
}

func NewServer(cfg config.GBConfig, db *gorm.DB, zlm *video.Client) *Server {
	return &Server{
		cfg:      cfg,
		db:       db,
		zlm:      zlm,
		sessions: map[string]*session{},
		pending:  map[string]chan *Message{},
		streams:  map[uint]*liveStream{},
		cascades: map[uint]context.CancelFunc{},
	}
}

// Start binds the UDP socket and begins serving. It is non-blocking.
func (s *Server) Start() error {
	addr, err := net.ResolveUDPAddr("udp", s.cfg.Listen)
	if err != nil {
		return err
	}
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		return err
	}
	s.conn = conn
	go s.readLoop()
	go s.offlineChecker()
	log.Printf("[gb28181] SIP signaling listening on %s (id=%s realm=%s)", s.cfg.Listen, s.cfg.ID, s.cfg.Realm)
	return nil
}

// StartTLS binds a GM/T 0024 secure SIP listener for GB35114 devices.
func (s *Server) StartTLS(cfg TLSConfig) error {
	signCert, err := gmtls.X509KeyPair(cfg.SignCertPEM, cfg.SignKeyPEM)
	if err != nil {
		return fmt.Errorf("load sign cert: %w", err)
	}
	encCert, err := gmtls.X509KeyPair(cfg.EncCertPEM, cfg.EncKeyPEM)
	if err != nil {
		return fmt.Errorf("load enc cert: %w", err)
	}
	tlsCfg := &gmtls.Config{
		GMSupport:    &gmtls.GMSupport{},
		Certificates: []gmtls.Certificate{signCert, encCert},
		ClientAuth:   gmtls.NoClientCert,
	}
	if cfg.CAPool != nil {
		tlsCfg.ClientCAs = cfg.CAPool
		if cfg.RequireClientCert {
			tlsCfg.ClientAuth = gmtls.RequireAndVerifyClientCert
		} else {
			tlsCfg.ClientAuth = gmtls.VerifyClientCertIfGiven
		}
	}
	ln, err := gmtls.Listen("tcp", cfg.Listen, tlsCfg)
	if err != nil {
		return err
	}
	s.tlsLn = ln
	s.tlsAddr = ln.Addr().String()
	go s.acceptLoop()
	log.Printf("[gb28181] GB35114 secure SIP (GM/T 0024 TLS) listening on %s", s.tlsAddr)
	return nil
}

func (s *Server) Stop() {
	if s.conn != nil {
		s.conn.Close()
	}
	if s.tlsLn != nil {
		s.tlsLn.Close()
	}
}

// Addr returns the bound UDP address (useful for tests).
func (s *Server) Addr() string {
	if s.conn == nil {
		return ""
	}
	return s.conn.LocalAddr().String()
}

// TLSAddr returns the bound secure SIP address (useful for tests).
func (s *Server) TLSAddr() string { return s.tlsAddr }

func (s *Server) readLoop() {
	buf := make([]byte, 64*1024)
	for {
		n, addr, err := s.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		data := make([]byte, n)
		copy(data, buf[:n])
		peer := &Peer{Transport: "UDP", UDP: addr}
		go s.handle(data, peer)
	}
}

func (s *Server) acceptLoop() {
	for {
		conn, err := s.tlsLn.Accept()
		if err != nil {
			return
		}
		go s.serveConn(conn)
	}
}

func (s *Server) serveConn(conn net.Conn) {
	defer conn.Close()
	peer := &Peer{Transport: "TLS", Conn: conn}
	r := bufio.NewReader(conn)
	for {
		data, err := readSIPMessage(r)
		if err != nil {
			return
		}
		s.handle(data, peer)
	}
}

// readSIPMessage reads one framed SIP message from a stream (SIP over TCP/TLS
// uses Content-Length framing).
func readSIPMessage(r *bufio.Reader) ([]byte, error) {
	var head bytes.Buffer
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return nil, err
		}
		head.WriteString(line)
		if line == "\r\n" || line == "\n" {
			break
		}
	}
	contentLen := 0
	for _, line := range strings.Split(head.String(), "\n") {
		l := strings.TrimRight(line, "\r")
		if i := strings.Index(strings.ToLower(l), "content-length:"); i == 0 {
			fmt.Sscanf(strings.TrimSpace(l[len("content-length:"):]), "%d", &contentLen)
		}
	}
	if contentLen > 0 {
		body := make([]byte, contentLen)
		if _, err := io.ReadFull(r, body); err != nil {
			return nil, err
		}
		head.Write(body)
	}
	return head.Bytes(), nil
}

func (s *Server) handle(data []byte, peer *Peer) {
	msg, err := ParseMessage(data)
	if err != nil {
		return
	}
	if !msg.IsRequest {
		s.routeResponse(msg)
		return
	}
	switch msg.Method {
	case "REGISTER":
		s.handleRegister(msg, peer)
	case "MESSAGE":
		s.handleMessage(msg, peer)
	case "INVITE":
		s.reply(msg, peer, 200, "OK")
	case "BYE":
		s.reply(msg, peer, 200, "OK")
	case "ACK":
		// no-op
	case "OPTIONS":
		s.reply(msg, peer, 200, "OK")
	default:
		s.reply(msg, peer, 200, "OK")
	}
}

// ---- REGISTER ----

func (s *Server) handleRegister(msg *Message, peer *Peer) {
	deviceID := userPart(msg.Get("To"))
	if deviceID == "" {
		deviceID = userPart(msg.Get("From"))
	}
	auth := msg.Get("Authorization")
	if auth == "" || !VerifyDigest(auth, "REGISTER", s.cfg.Realm, s.cfg.Password) {
		resp := msg.Response(401, "Unauthorized")
		nonce := fmt.Sprintf("%d", time.Now().UnixNano())
		resp.Add("WWW-Authenticate", fmt.Sprintf(
			`Digest realm="%s", nonce="%s", algorithm=MD5`, s.cfg.Realm, nonce))
		s.send(resp, peer)
		return
	}
	expires := 3600
	if v := msg.Get("Expires"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			expires = n
		}
	}
	if v := msg.Get("Contact"); v != "" {
		if i := strings.Index(strings.ToLower(v), "expires="); i >= 0 {
			rest := v[i+len("expires="):]
			rest = strings.TrimRight(strings.TrimSpace(strings.Split(rest, ";")[0]), ">")
			if n, err := strconv.Atoi(rest); err == nil {
				expires = n
			}
		}
	}

	s.mu.Lock()
	s.sessions[deviceID] = &session{DeviceID: deviceID, Peer: peer, Transport: peer.Transport, Expires: expires, LastKeepalive: time.Now()}
	s.mu.Unlock()

	s.upsertDevice(deviceID, peer, expires)
	s.send(msg.Response(200, "OK"), peer)
	log.Printf("[gb28181] device %s registered from %s over %s (expires=%d)", deviceID, peer, peer.Transport, expires)

	// Ask the device for its channel catalog.
	go s.sendCatalogQuery(deviceID, peer)
}

func (s *Server) upsertDevice(deviceID string, peer *Peer, expires int) {
	var dev model.GBDevice
	err := s.db.Where("device_id = ?", deviceID).First(&dev).Error
	if err == gorm.ErrRecordNotFound {
		dev = model.GBDevice{DeviceID: deviceID, IP: peer.IP(), Port: peer.Port(), Transport: peer.Transport}
	}
	dev.IP = peer.IP()
	dev.Port = peer.Port()
	dev.Transport = peer.Transport
	dev.Online = true
	dev.Expires = expires
	dev.RegisteredAt = time.Now()
	if dev.LastKeepalive.IsZero() {
		dev.LastKeepalive = time.Now()
	}
	if dev.ID == 0 {
		s.db.Create(&dev)
	} else {
		s.db.Save(&dev)
	}
}

// ---- MESSAGE ----

func (s *Server) handleMessage(msg *Message, peer *Peer) {
	ct := msg.Get("Content-Type")
	if !strings.Contains(strings.ToLower(ct), "manscdp") {
		s.reply(msg, peer, 200, "OK")
		return
	}
	body := decodeBody([]byte(msg.Body))
	cmd := cmdType(body)
	switch cmd {
	case "Keepalive":
		var k Keepalive
		if xmlUnmarshal(body, &k) == nil {
			s.touch(k.DeviceID)
			log.Printf("[gb28181] keepalive %s status=%s", k.DeviceID, k.Status)
		}
	case "Catalog":
		s.handleCatalog(body, peer)
	case "DeviceInfo":
		if info, err := parseDeviceInfo(body); err == nil {
			s.updateDeviceInfo(info)
		}
	case "Alarm":
		var a Alarm
		if xmlUnmarshal(body, &a) == nil {
			s.handleAlarm(a)
		}
	}
	s.reply(msg, peer, 200, "OK")
}

func (s *Server) touch(deviceID string) {
	now := time.Now()
	s.mu.Lock()
	if sess, ok := s.sessions[deviceID]; ok {
		sess.LastKeepalive = now
	}
	s.mu.Unlock()
	s.db.Model(&model.GBDevice{}).Where("device_id = ?", deviceID).
		Updates(map[string]any{"online": true, "last_keepalive": now})
}

func (s *Server) handleCatalog(body string, peer *Peer) {
	resp, err := parseCatalog(body)
	if err != nil {
		log.Printf("[gb28181] parse catalog: %v", err)
		return
	}
	// Find or create the parent device row.
	var gbDev model.GBDevice
	if err := s.db.Where("device_id = ?", resp.DeviceID).First(&gbDev).Error; err != nil {
		gbDev = model.GBDevice{DeviceID: resp.DeviceID, Online: true, RegisteredAt: time.Now()}
		s.db.Create(&gbDev)
	}
	// Find or create the platform-side Device used by the video plane.
	var dev model.Device
	err = s.db.Where("protocol = ? AND name = ?", "gb28181", resp.DeviceID).First(&dev).Error
	if err == gorm.ErrRecordNotFound {
		dev = model.Device{
			Name: resp.DeviceID, Protocol: "gb28181", AccessMode: "register",
			IP: gbDev.IP, Status: "online", Manufacturer: gbDev.Manufacturer,
		}
		s.db.Create(&dev)
	} else {
		s.db.Model(&dev).Updates(map[string]any{"status": "online", "ip": gbDev.IP})
	}
	for _, item := range resp.DeviceList.Items {
		var ch model.Channel
		e := s.db.Where("gb_device_id = ? AND gb_channel_id = ?", resp.DeviceID, item.DeviceID).First(&ch).Error
		online := strings.EqualFold(item.Status, "ON")
		if e == gorm.ErrRecordNotFound {
			ch = model.Channel{
				DeviceID: dev.ID, Name: item.Name, StreamType: "main",
				StreamKey:  "gb_" + randomHex(8),
				GBDeviceID: resp.DeviceID, GBChannelID: item.DeviceID,
				Online: online, Enabled: true,
			}
			s.db.Create(&ch)
		} else {
			s.db.Model(&ch).Updates(map[string]any{"online": online, "name": item.Name})
		}
	}
	s.db.Model(&gbDev).Update("channel_count", resp.SumNum)
	log.Printf("[gb28181] catalog %s: %d channels (sum=%d)", resp.DeviceID, len(resp.DeviceList.Items), resp.SumNum)
}

func (s *Server) updateDeviceInfo(info *DeviceInfoResponse) {
	s.db.Model(&model.GBDevice{}).Where("device_id = ?", info.DeviceID).Updates(map[string]any{
		"name": info.Name, "manufacturer": info.Manufacturer,
		"model": info.Model, "firmware": info.Firmware, "channel_count": info.Channel,
	})
}

func (s *Server) handleAlarm(a Alarm) {
	level := "warning"
	switch strings.TrimSpace(a.AlarmPriority) {
	case "1":
		level = "critical"
	case "2":
		level = "warning"
	case "3", "4":
		level = "info"
	}
	eventType := "gb_alarm"
	if a.AlarmDescription != "" {
		eventType = "gb_" + strings.ReplaceAll(strings.TrimSpace(a.AlarmDescription), " ", "_")
	}
	ev := model.AIEvent{
		Kind: "gb28181", EventType: eventType, Level: level,
		Summary: a.AlarmDescription, OccurredAt: time.Now(),
		Payload: fmt.Sprintf(`{"deviceId":%q,"method":%q,"priority":%q}`, a.DeviceID, a.AlarmMethod, a.AlarmPriority),
	}
	s.db.Create(&ev)
	if s.sink != nil {
		s.sink.OnEvent(ev)
	}
}

// ---- outgoing requests ----

func (s *Server) sendCatalogQuery(deviceID string, peer *Peer) {
	body, _ := buildQuery("Catalog", deviceID, int(s.sn.Add(1)))
	m := s.newRequest("MESSAGE", fmt.Sprintf("sip:%s@%s", deviceID, s.cfg.Realm), peer)
	m.Add("Content-Type", "Application/MANSCDP+xml")
	m.Body = body
	s.send(m, peer)
}

// RefreshCatalog re-queries a registered device's channel catalog.
func (s *Server) RefreshCatalog(deviceID string) error {
	peer := s.sessionPeer(deviceID)
	if peer == nil {
		return fmt.Errorf("gb device %s is not registered", deviceID)
	}
	s.sendCatalogQuery(deviceID, peer)
	return nil
}

// RegisteredCount returns the number of currently registered devices.
func (s *Server) RegisteredCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.sessions)
}

func (s *Server) newRequest(method, uri string, peer *Peer) *Message {
	callID := fmt.Sprintf("%d@%s", time.Now().UnixNano(), s.cfg.RTPIP)
	fromTag := randomHex(4)
	m := &Message{IsRequest: true, Method: method, URI: uri}
	m.Add("Via", s.via(peer))
	m.Add("From", fmt.Sprintf("<sip:%s@%s>;tag=%s", s.cfg.ID, s.cfg.Realm, fromTag))
	m.Add("To", fmt.Sprintf("<%s>", uri))
	m.Add("Call-ID", callID)
	m.Add("CSeq", fmt.Sprintf("%d %s", s.cseq.Add(1), method))
	m.Add("Max-Forwards", "70")
	m.Add("Contact", s.contact(peer))
	m.Add("User-Agent", "EasyAVR")
	return m
}

// via builds a Via header matching the transport the request leaves on.
func (s *Server) via(peer *Peer) string {
	if peer != nil && peer.Transport == "TLS" {
		return fmt.Sprintf("SIP/2.0/TLS %s:%d;rport;branch=z9hG4bK%s", s.cfg.RTPIP, s.tlsPort(), randomHex(6))
	}
	return fmt.Sprintf("SIP/2.0/UDP %s:%d;rport;branch=z9hG4bK%s", s.cfg.RTPIP, s.sipPort(), randomHex(6))
}

func (s *Server) contact(peer *Peer) string {
	if peer != nil && peer.Transport == "TLS" {
		return fmt.Sprintf("<sip:%s@%s:%d;transport=tls>", s.cfg.ID, s.cfg.RTPIP, s.tlsPort())
	}
	return fmt.Sprintf("<sip:%s@%s:%d>", s.cfg.ID, s.cfg.RTPIP, s.sipPort())
}

func (s *Server) send(msg *Message, peer *Peer) {
	if peer == nil {
		return
	}
	if peer.Conn != nil {
		peer.wmu.Lock()
		_, err := peer.Conn.Write(msg.Bytes())
		peer.wmu.Unlock()
		if err != nil {
			log.Printf("[gb28181] send tls: %v", err)
		}
		return
	}
	if s.conn == nil || peer.UDP == nil {
		return
	}
	if _, err := s.conn.WriteToUDP(msg.Bytes(), peer.UDP); err != nil {
		log.Printf("[gb28181] send: %v", err)
	}
}

func (s *Server) reply(req *Message, peer *Peer, code int, reason string) {
	s.send(req.Response(code, reason), peer)
}

func (s *Server) routeResponse(msg *Message) {
	callID := msg.Get("Call-ID")
	s.mu.Lock()
	ch := s.pending[callID]
	s.mu.Unlock()
	if ch != nil {
		select {
		case ch <- msg:
		default:
		}
	}
}

func (s *Server) waitResponse(callID string, timeout time.Duration) *Message {
	deadline := time.After(timeout)
	for {
		select {
		case m := <-s.responseChan(callID):
			if m.StatusCode >= 200 {
				return m
			}
		case <-deadline:
			return nil
		}
	}
}

func (s *Server) responseChan(callID string) chan *Message {
	s.mu.Lock()
	defer s.mu.Unlock()
	ch, ok := s.pending[callID]
	if !ok {
		ch = make(chan *Message, 16)
		s.pending[callID] = ch
	}
	return ch
}

func (s *Server) clearPending(callID string) {
	s.mu.Lock()
	delete(s.pending, callID)
	s.mu.Unlock()
}

func (s *Server) sipPort() int {
	_, portStr, err := net.SplitHostPort(s.cfg.Listen)
	if err != nil {
		return 5060
	}
	p, _ := strconv.Atoi(portStr)
	return p
}

func (s *Server) tlsPort() int {
	if s.tlsAddr == "" {
		return 5061
	}
	_, portStr, err := net.SplitHostPort(s.tlsAddr)
	if err != nil {
		return 5061
	}
	p, _ := strconv.Atoi(portStr)
	return p
}

func (s *Server) offlineChecker() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for range ticker.C {
		now := time.Now()
		s.mu.Lock()
		var offline []string
		for id, sess := range s.sessions {
			if now.Sub(sess.LastKeepalive) > time.Duration(sess.Expires)*time.Second {
				offline = append(offline, id)
				delete(s.sessions, id)
			}
		}
		s.mu.Unlock()
		for _, id := range offline {
			s.db.Model(&model.GBDevice{}).Where("device_id = ?", id).Update("online", false)
			s.db.Model(&model.Channel{}).Where("gb_device_id = ?", id).Update("online", false)
			log.Printf("[gb28181] device %s offline (keepalive timeout)", id)
		}
	}
}

func randomHex(n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ---- live INVITE ----

// InvitePlay asks a registered GB device to push the channel as PS/RTP into
// ZLMediaKit, then returns nothing until the call is answered.
func (s *Server) InvitePlay(ctx context.Context, channelID uint) (map[string]string, error) {
	var ch model.Channel
	if err := s.db.First(&ch, channelID).Error; err != nil {
		return nil, err
	}
	if ch.GBDeviceID == "" || ch.GBChannelID == "" {
		return nil, fmt.Errorf("channel %d is not a GB28181 channel", channelID)
	}
	peer := s.sessionPeer(ch.GBDeviceID)
	if peer == nil {
		return nil, fmt.Errorf("gb device %s is not registered", ch.GBDeviceID)
	}

	ssrc := randomSSRC()
	port, err := s.zlm.OpenRtpServer(ch.StreamKey, 0, false)
	if err != nil {
		return nil, err
	}
	uri := fmt.Sprintf("sip:%s@%s", ch.GBChannelID, peer.String())
	m := s.newRequest("INVITE", uri, peer)
	m.Add("Content-Type", "application/SDP")
	callID := m.Get("Call-ID")
	cseq := m.Get("CSeq")
	m.Body = buildPlaySDP(s.cfg.RTPIP, port, ssrc)

	s.responseChan(callID) // register before sending
	s.send(m, peer)
	resp := s.waitResponse(callID, 8*time.Second)
	if resp == nil || resp.StatusCode != 200 {
		s.clearPending(callID)
		_ = s.zlm.CloseRtpServer(ch.StreamKey)
		if resp == nil {
			return nil, fmt.Errorf("device did not answer INVITE")
		}
		return nil, fmt.Errorf("device rejected INVITE: %d %s", resp.StatusCode, resp.StatusText)
	}
	// ACK the 2xx.
	ack := &Message{IsRequest: true, Method: "ACK", URI: uri}
	ack.Add("Via", s.via(peer))
	ack.Add("From", m.Get("From"))
	ack.Add("To", resp.Get("To"))
	ack.Add("Call-ID", callID)
	ack.Add("CSeq", cseq)
	ack.Add("Max-Forwards", "70")
	s.send(ack, peer)

	s.mu.Lock()
	s.streams[channelID] = &liveStream{
		ChannelID: channelID, DeviceID: ch.GBDeviceID, StreamKey: ch.StreamKey,
		SSRC: ssrc, RTPPort: port, CallID: callID, ToTag: tagFrom(resp.Get("To")), CSeq: cseqNumber(cseq),
	}
	s.mu.Unlock()
	s.db.Model(&ch).Update("online", true)
	log.Printf("[gb28181] INVITE accepted for channel %d (stream=%s rtp=%d ssrc=%s)", channelID, ch.StreamKey, port, ssrc)
	return s.zlm.PlayURLs(ch.StreamKey), nil
}

// StopPlay tears down an active GB live call.
func (s *Server) StopPlay(channelID uint) error {
	s.mu.Lock()
	st := s.streams[channelID]
	delete(s.streams, channelID)
	s.mu.Unlock()
	if st == nil {
		return nil
	}
	_ = s.zlm.CloseRtpServer(st.StreamKey)
	_ = s.zlm.CloseStreams(st.StreamKey)
	peer := s.sessionPeer(st.DeviceID)
	if peer != nil {
		uri := fmt.Sprintf("sip:%s@%s", st.DeviceID, peer.String())
		bye := &Message{IsRequest: true, Method: "BYE", URI: uri}
		bye.Add("Via", s.via(peer))
		bye.Add("From", fmt.Sprintf("<sip:%s@%s>;tag=%s", s.cfg.ID, s.cfg.Realm, randomHex(4)))
		bye.Add("To", fmt.Sprintf("<sip:%s@%s>", st.DeviceID, s.cfg.Realm))
		bye.Add("Call-ID", st.CallID)
		bye.Add("CSeq", fmt.Sprintf("%d BYE", st.CSeq+1))
		bye.Add("Max-Forwards", "70")
		s.send(bye, peer)
	}
	return nil
}

func (s *Server) sessionPeer(deviceID string) *Peer {
	s.mu.Lock()
	defer s.mu.Unlock()
	if sess, ok := s.sessions[deviceID]; ok {
		return sess.Peer
	}
	return nil
}

func buildPlaySDP(ip string, port int, ssrc string) string {
	return fmt.Sprintf(
		"v=0\r\no=%s 0 0 IN IP4 %s\r\ns=Play\r\nc=IN IP4 %s\r\nt=0 0\r\n"+
			"m=video %d RTP/AVP 96 98 97\r\na=recvonly\r\na=rtpmap:96 PS/90000\r\n"+
			"a=rtpmap:98 H264/90000\r\na=rtpmap:97 MPEG4/90000\r\ny=%s\r\n",
		ssrc, ip, ip, port, ssrc)
}

func randomSSRC() string {
	var b [8]byte
	rand.Read(b[:])
	var n uint64
	for _, x := range b {
		n = n<<8 | uint64(x)
	}
	return fmt.Sprintf("%010d", n%9000000000+1000000000)
}

func tagFrom(to string) string {
	if i := strings.Index(strings.ToLower(to), ";tag="); i >= 0 {
		return strings.TrimSpace(to[i+5:])
	}
	return ""
}

func cseqNumber(cseq string) int {
	parts := strings.Fields(cseq)
	if len(parts) > 0 {
		if n, err := strconv.Atoi(parts[0]); err == nil {
			return n
		}
	}
	return 1
}

func userPart(uri string) string {
	uri = strings.TrimSpace(uri)
	start := strings.Index(uri, "sip:")
	offset := len("sip:")
	if start < 0 {
		start = strings.Index(uri, ":")
		offset = 1
		if start < 0 {
			return ""
		}
	}
	rest := uri[start+offset:]
	// strip < >
	rest = strings.Trim(rest, "<>")
	if end := strings.IndexAny(rest, "@>;"); end >= 0 {
		return rest[:end]
	}
	return rest
}
