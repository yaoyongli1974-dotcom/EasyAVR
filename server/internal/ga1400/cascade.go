package ga1400

import (
	"bytes"
	"context"
	"encoding/xml"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/config"
	"github.com/easyavr/easyavr/internal/model"
)

// CascadeService manages GA/T1400 cascades: registering to upper view
// libraries and subscribing to lower ones.
type CascadeService struct {
	db     *gorm.DB
	cfg    config.GA1400Config
	client *http.Client

	mu      sync.Mutex
	cancels map[uint]context.CancelFunc
}

// Subscription lifecycle constants. GA/T1400 subscriptions expire; we renew
// before the deadline and periodically push a full sync to upper libraries.
const (
	subscribeTTL     = 5 * time.Minute
	renewWindow      = 90 * time.Second
	fullSyncInterval = 10 * time.Minute
)

func NewCascadeService(db *gorm.DB, cfg config.GA1400Config) *CascadeService {
	return &CascadeService{
		db: db, cfg: cfg,
		client:  &http.Client{Timeout: 10 * time.Second},
		cancels: map[uint]context.CancelFunc{},
	}
}

// Start launches every enabled cascade.
func (s *CascadeService) Start(ctx context.Context) {
	var items []model.GA1400Cascade
	s.db.Where("enabled = ?", true).Find(&items)
	for _, item := range items {
		s.Ensure(item)
	}
}

// Ensure starts (or restarts) the loop for one cascade.
func (s *CascadeService) Ensure(item model.GA1400Cascade) {
	s.mu.Lock()
	if cancel, ok := s.cancels[item.ID]; ok {
		cancel()
		delete(s.cancels, item.ID)
	}
	ctx, cancel := context.WithCancel(context.Background())
	s.cancels[item.ID] = cancel
	s.mu.Unlock()
	go s.loop(ctx, item)
}

// Remove stops a cascade.
func (s *CascadeService) Remove(id uint) {
	s.mu.Lock()
	cancel, ok := s.cancels[id]
	if ok {
		delete(s.cancels, id)
	}
	s.mu.Unlock()
	if ok {
		cancel()
	}
}

func (s *CascadeService) loop(ctx context.Context, item model.GA1400Cascade) {
	sync := func() {
		var online bool
		if item.Direction == "down" {
			online = s.ensureSubscription(item)
		} else {
			online = s.registerAndHeartbeat(item)
			if online && s.dueForFullSync(item) {
				s.FullSync(item)
			}
		}
		s.db.Model(&model.GA1400Cascade{}).Where("id = ?", item.ID).
			Updates(map[string]any{"online": online, "last_heartbeat": time.Now()})
	}
	sync()
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sync()
		}
	}
}

// dueForFullSync reports whether the up cascade has not synced recently.
func (s *CascadeService) dueForFullSync(item model.GA1400Cascade) bool {
	if item.LastSyncAt.IsZero() {
		return true
	}
	return time.Since(item.LastSyncAt) >= fullSyncInterval
}

// ensureSubscription renews the lower-library subscription before it expires.
func (s *CascadeService) ensureSubscription(item model.GA1400Cascade) bool {
	var sub model.GA1400Subscription
	err := s.db.Where("cascade_id = ?", item.ID).First(&sub).Error
	if err != nil || sub.ExpiresAt.IsZero() || time.Until(sub.ExpiresAt) < renewWindow {
		return s.subscribe(item)
	}
	return true
}

func (s *CascadeService) registerAndHeartbeat(item model.GA1400Cascade) bool {
	if !s.post(item, "/VIID/System/Register", s.registerBody()) {
		return false
	}
	return s.post(item, "/VIID/System/Keepalive", s.keepaliveBody())
}

func (s *CascadeService) registerBody() []byte {
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><Register><PlatformID>%s</PlatformID><PlatformName>EasyAVR</PlatformName></Register>`, s.cfg.PlatformID))
}

func (s *CascadeService) keepaliveBody() []byte {
	return []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?><Keepalive><PlatformID>%s</PlatformID></Keepalive>`, s.cfg.PlatformID))
}

// subscribe registers a subscription on a lower view library.
func (s *CascadeService) subscribe(item model.GA1400Cascade) bool {
	subID := fmt.Sprintf("easyavr-%d", item.ID)
	notifyURL := strings.TrimRight(s.cfg.NotifyURL, "/")
	if notifyURL == "" {
		notifyURL = strings.TrimRight(item.URL, "/")
	}
	body := []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>`+
		`<SubscribeNotifications><SubscribeID>%s</SubscribeID><Title>EasyAVR</Title>`+
		`<EventTypes>1,2,3</EventTypes><NotifyURL>%s/VIID/Notification</NotifyURL>`+
		`</SubscribeNotifications>`, subID, notifyURL))
	ok := s.post(item, "/VIID/SubscribeNotifications", body)
	if ok {
		now := time.Now()
		expires := now.Add(subscribeTTL)
		var sub model.GA1400Subscription
		err := s.db.Where("cascade_id = ?", item.ID).First(&sub).Error
		if err == gorm.ErrRecordNotFound {
			s.db.Create(&model.GA1400Subscription{
				CascadeID: item.ID, SubscribeID: subID, Title: "EasyAVR",
				EventTypes: "1,2,3", Status: "active",
				ExpiresAt: expires, LastRenewAt: now, RenewCount: 1,
			})
		} else {
			s.db.Model(&sub).Updates(map[string]any{
				"status": "active", "expires_at": expires,
				"last_renew_at": now, "renew_count": sub.RenewCount + 1,
			})
		}
	}
	return ok
}

// FullSync pushes all local events (or those created since the last sync) to an
// upper view library. For a down cascade it re-subscribes the lower library so
// it replays its current data, and returns the number of records pushed.
func (s *CascadeService) FullSync(item model.GA1400Cascade) int {
	if item.Direction == "down" {
		if s.subscribe(item) {
			return 1
		}
		return 0
	}
	q := s.db.Order("id ASC")
	if !item.LastSyncAt.IsZero() {
		q = q.Where("created_at > ?", item.LastSyncAt)
	}
	var events []model.AIEvent
	q.Find(&events)
	count := 0
	for _, ev := range events {
		if s.pushEvent(item, ev) {
			count++
		}
	}
	s.db.Model(&model.GA1400Cascade{}).Where("id = ?", item.ID).
		Updates(map[string]any{"last_sync_at": time.Now(), "sync_count": count})
	return count
}

// PushEvent forwards an event to every enabled upper view library.
func (s *CascadeService) PushEvent(ev model.AIEvent) {
	var items []model.GA1400Cascade
	s.db.Where("enabled = ? AND direction = ?", true, "up").Find(&items)
	for _, item := range items {
		if !s.pushEvent(item, ev) {
			log.Printf("[ga1400] push to %s failed", item.URL)
		}
	}
}

// pushEvent posts a single event notification to one upper view library.
func (s *CascadeService) pushEvent(item model.GA1400Cascade, ev model.AIEvent) bool {
	level := "3"
	switch ev.Level {
	case "critical":
		level = "1"
	case "warning":
		level = "2"
	}
	body := []byte(fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>`+
		`<Notification><DeviceID>%d</DeviceID><AlarmLevel>%s</AlarmLevel>`+
		`<AlarmDescription>%s</AlarmDescription><EventType>%s</EventType></Notification>`,
		ev.ChannelID, level, xmlEscape(ev.Summary), xmlEscape(ev.EventType)))
	return s.post(item, "/VIID/Notification", body)
}

// Test posts to a cascade and reports success.
func (s *CascadeService) Test(item model.GA1400Cascade) error {
	if item.Direction == "down" {
		if !s.subscribe(item) {
			return fmt.Errorf("subscribe failed")
		}
		return nil
	}
	if !s.registerAndHeartbeat(item) {
		return fmt.Errorf("register/heartbeat failed")
	}
	return nil
}

// post sends an XML body with optional HTTP digest authentication.
func (s *CascadeService) post(item model.GA1400Cascade, path string, body []byte) bool {
	url := strings.TrimRight(item.URL, "/") + path
	resp, err := s.do(http.MethodPost, url, body, "")
	if err != nil {
		return false
	}
	if resp.StatusCode == http.StatusUnauthorized {
		realm, nonce := parseDigestChallenge(resp.Header.Get("WWW-Authenticate"))
		resp.Body.Close()
		if realm == "" {
			return false
		}
		auth := buildDigestAuthorization(item.Username, realm, item.Password, http.MethodPost, requestURI(url), nonce)
		resp, err = s.do(http.MethodPost, url, body, auth)
		if err != nil {
			return false
		}
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
	return resp.StatusCode < 300
}

func (s *CascadeService) do(method, url string, body []byte, auth string) (*http.Response, error) {
	req, err := http.NewRequest(method, url, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/xml; charset=utf-8")
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	return s.client.Do(req)
}

func requestURI(raw string) string {
	if i := strings.Index(raw, "://"); i >= 0 {
		rest := raw[i+3:]
		if j := strings.IndexAny(rest, "/?#"); j >= 0 {
			return rest[j:]
		}
		return "/"
	}
	return raw
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}
