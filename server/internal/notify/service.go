// Package notify implements alert notifications for the AI Event Center:
// rules match incoming events and deliver them to webhook or email channels.
package notify

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/model"
)

var levelRank = map[string]int{"info": 0, "warning": 1, "critical": 2}

// Service matches events against rules and delivers notifications.
type Service struct {
	db     *gorm.DB
	client *http.Client
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db, client: &http.Client{Timeout: 10 * time.Second}}
}

// Notify dispatches an event to every matching rule target. Delivery is async
// so it never blocks the event ingestion path.
func (s *Service) Notify(ev model.AIEvent) {
	var rules []model.NotificationRule
	s.db.Where("enabled = ?", true).Find(&rules)
	for _, r := range rules {
		if !s.match(r, ev) {
			continue
		}
		for _, id := range parseIDs(r.TargetIDs) {
			var ch model.NotificationChannel
			if err := s.db.First(&ch, id).Error; err != nil || !ch.Enabled {
				continue
			}
			go func(c model.NotificationChannel) {
				if err := s.Deliver(c, ev); err != nil {
					log.Printf("[notify] %s -> %s failed: %v", c.Type, c.Name, err)
				}
			}(ch)
		}
	}
}

// Test sends a synthetic event to a single channel.
func (s *Service) Test(channelID uint) error {
	var ch model.NotificationChannel
	if err := s.db.First(&ch, channelID).Error; err != nil {
		return err
	}
	return s.Deliver(ch, model.AIEvent{
		Kind: "test", EventType: "test_notification", Level: "info",
		Summary: "EasyAVR 通知测试", OccurredAt: time.Now(),
	})
}

func (s *Service) match(r model.NotificationRule, ev model.AIEvent) bool {
	if r.Kind != "" && r.Kind != ev.Kind {
		return false
	}
	if r.EventType != "" && r.EventType != ev.EventType {
		return false
	}
	if r.MinLevel != "" && levelRank[ev.Level] < levelRank[r.MinLevel] {
		return false
	}
	return true
}

// Deliver sends an event to one channel.
func (s *Service) Deliver(ch model.NotificationChannel, ev model.AIEvent) error {
	switch ch.Type {
	case "webhook":
		return s.webhook(ch, ev)
	case "email":
		return s.email(ch, ev)
	default:
		return fmt.Errorf("unsupported channel type %q", ch.Type)
	}
}

func (s *Service) webhook(ch model.NotificationChannel, ev model.AIEvent) error {
	if ch.URL == "" {
		return fmt.Errorf("webhook url is empty")
	}
	body, _ := json.Marshal(map[string]any{
		"source": "easyavr",
		"event":  ev,
	})
	req, err := http.NewRequest(http.MethodPost, ch.URL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if ch.Secret != "" {
		req.Header.Set("X-EasyAVR-Secret", ch.Secret)
	}
	resp, err := s.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("webhook returned status %d", resp.StatusCode)
	}
	return nil
}

func (s *Service) email(ch model.NotificationChannel, ev model.AIEvent) error {
	if ch.SMTPHost == "" || ch.From == "" || ch.To == "" {
		return fmt.Errorf("email channel is incomplete")
	}
	port := ch.SMTPPort
	if port == 0 {
		port = 587
	}
	addr := fmt.Sprintf("%s:%d", ch.SMTPHost, port)
	recipients := splitList(ch.To)
	subject := fmt.Sprintf("[EasyAVR][%s] %s", strings.ToUpper(ev.Level), ev.EventType)
	body := fmt.Sprintf("时间: %s\n类型: %s\n级别: %s\n通道: %d\n摘要: %s\n",
		ev.OccurredAt.Format(time.RFC3339), ev.EventType, ev.Level, ev.ChannelID, ev.Summary)
	msg := buildMIME(ch.From, recipients, subject, body)
	auth := smtp.PlainAuth("", ch.SMTPUser, ch.SMTPPassword, ch.SMTPHost)

	if ch.UseTLS {
		return sendMailTLS(addr, ch.SMTPHost, auth, ch.From, recipients, msg)
	}
	return smtp.SendMail(addr, auth, ch.From, recipients, msg)
}

func sendMailTLS(addr, host string, auth smtp.Auth, from string, to []string, msg []byte) error {
	conn, err := tls.Dial("tcp", addr, &tls.Config{ServerName: host})
	if err != nil {
		return err
	}
	c, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer c.Close()
	if auth != nil {
		if err := c.Auth(auth); err != nil {
			return err
		}
	}
	if err := c.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := c.Rcpt(rcpt); err != nil {
			return err
		}
	}
	w, err := c.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return c.Quit()
}

func buildMIME(from string, to []string, subject, body string) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", strings.Join(to, ","))
	fmt.Fprintf(&b, "Subject: %s\r\n", subject)
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=UTF-8\r\n\r\n")
	b.WriteString(body)
	return []byte(b.String())
}

func parseIDs(s string) []uint {
	var ids []uint
	for _, part := range strings.Split(s, ",") {
		if n, err := strconv.Atoi(strings.TrimSpace(part)); err == nil && n > 0 {
			ids = append(ids, uint(n))
		}
	}
	return ids
}

func splitList(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if v := strings.TrimSpace(part); v != "" {
			out = append(out, v)
		}
	}
	return out
}
