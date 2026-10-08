package gb28181

import (
	"context"
	"fmt"
	"log"
	"net"
	"time"

	"github.com/easyavr/easyavr/internal/model"
)

// EnsureCascade starts (or restarts) outbound registration to an upper platform.
func (s *Server) EnsureCascade(item model.GBCascade) {
	s.cascadeMu.Lock()
	if cancel, ok := s.cascades[item.ID]; ok {
		cancel()
		delete(s.cascades, item.ID)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cascades[item.ID] = cancel
	s.cascadeMu.Unlock()
	go s.cascadeLoop(ctx, item)
}

// RemoveCascade stops outbound registration for a cascade.
func (s *Server) RemoveCascade(id uint) {
	s.cascadeMu.Lock()
	cancel, ok := s.cascades[id]
	if ok {
		delete(s.cascades, id)
	}
	s.cascadeMu.Unlock()
	if ok {
		cancel()
	}
}

// StartCascades launches every enabled cascade (called at startup).
func (s *Server) StartCascades(items []model.GBCascade) {
	for _, item := range items {
		if item.Enabled {
			s.EnsureCascade(item)
		}
	}
}

func (s *Server) cascadeLoop(ctx context.Context, item model.GBCascade) {
	addr := &net.UDPAddr{IP: net.ParseIP(item.TargetIP), Port: item.TargetPort}
	if addr.IP == nil {
		log.Printf("[gb28181] cascade %d: invalid target ip %q", item.ID, item.TargetIP)
		return
	}
	peer := &Peer{Transport: "UDP", UDP: addr}
	register := func() bool {
		ok := s.registerToUpper(item, peer)
		s.db.Model(&model.GBCascade{}).Where("id = ?", item.ID).Updates(map[string]any{
			"online": ok, "last_heartbeat": time.Now(),
		})
		return ok
	}
	if !register() {
		log.Printf("[gb28181] cascade %d: initial register to %s failed", item.ID, item.TargetIP)
	}
	ticker := time.NewTicker(60 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			register()
		}
	}
}

func (s *Server) registerToUpper(item model.GBCascade, peer *Peer) bool {
	resp := s.transact(s.buildRegister(item, peer, ""), peer, 5*time.Second)
	if resp == nil {
		return false
	}
	if resp.StatusCode == 401 {
		challenge := resp.Get("WWW-Authenticate")
		resp = s.transact(s.buildRegister(item, peer, challenge), peer, 5*time.Second)
	}
	return resp != nil && resp.StatusCode == 200
}

func (s *Server) buildRegister(item model.GBCascade, peer *Peer, challenge string) *Message {
	uri := fmt.Sprintf("sip:%s@%s", item.TargetID, item.TargetID)
	m := s.newRequest("REGISTER", uri, peer)
	m.Set("From", fmt.Sprintf("<sip:%s@%s>;tag=%s", item.LocalID, item.TargetID, randomHex(4)))
	m.Set("To", fmt.Sprintf("<sip:%s@%s>", item.LocalID, item.TargetID))
	m.Set("Contact", fmt.Sprintf("<sip:%s@%s:%d>", item.LocalID, s.cfg.RTPIP, s.sipPort()))
	m.Set("Expires", "3600")
	if challenge != "" {
		realm, nonce := ParseChallenge(challenge)
		if realm == "" {
			realm = item.TargetID
		}
		m.Set("Authorization", BuildAuthorization(item.LocalID, realm, item.Password, "REGISTER", uri, nonce))
	}
	return m
}

// transact sends a request and waits for the first non-100 response.
func (s *Server) transact(m *Message, peer *Peer, timeout time.Duration) *Message {
	callID := m.Get("Call-ID")
	s.responseChan(callID)
	s.send(m, peer)
	resp := s.waitResponse(callID, timeout)
	s.clearPending(callID)
	return resp
}
