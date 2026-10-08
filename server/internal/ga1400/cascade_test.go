package ga1400

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/easyavr/easyavr/internal/config"
	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/store"
)

func TestCascadeRegisterAndPush(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "c.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	var mu sync.Mutex
	seen := map[string]int{}
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen[r.URL.Path]++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer peer.Close()

	svc := NewCascadeService(db, config.GA1400Config{NotifyURL: peer.URL})
	item := model.GA1400Cascade{Name: "up", Direction: "up", URL: peer.URL, Enabled: true}
	db.Create(&item)

	if !svc.registerAndHeartbeat(item) {
		t.Fatal("register/heartbeat failed")
	}
	svc.PushEvent(model.AIEvent{Level: "warning", EventType: "intrusion", Summary: "x"})

	mu.Lock()
	defer mu.Unlock()
	if seen["/VIID/System/Register"] != 1 || seen["/VIID/System/Keepalive"] != 1 || seen["/VIID/Notification"] != 1 {
		t.Fatalf("unexpected peer calls: %+v", seen)
	}
}

func TestCascadeDigestAuth(t *testing.T) {
	db, _ := store.Open(filepath.Join(t.TempDir(), "d.db"))
	var authorized bool
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "" {
			w.Header().Set("WWW-Authenticate", `Digest realm="easyavr", nonce="n1"`)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		authorized = true
		w.WriteHeader(http.StatusOK)
	}))
	defer peer.Close()

	svc := NewCascadeService(db, config.GA1400Config{})
	item := model.GA1400Cascade{Name: "up", Direction: "up", URL: peer.URL, Username: "easyavr", Password: "easyavr123"}
	if !svc.registerAndHeartbeat(item) {
		t.Fatal("digest register should succeed on retry")
	}
	if !authorized {
		t.Fatal("peer never saw a digest Authorization header")
	}
}

func TestSubscriptionAutoRenew(t *testing.T) {
	db, _ := store.Open(filepath.Join(t.TempDir(), "r.db"))
	var calls int
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusOK)
	}))
	defer peer.Close()

	svc := NewCascadeService(db, config.GA1400Config{NotifyURL: "http://easyavr.local"})
	item := model.GA1400Cascade{Name: "down", Direction: "down", URL: peer.URL, Enabled: true}
	db.Create(&item)

	// A fresh subscription must not trigger a renew round-trip.
	db.Create(&model.GA1400Subscription{
		CascadeID: item.ID, SubscribeID: "easyavr-1", Status: "active",
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if !svc.ensureSubscription(item) {
		t.Fatal("ensureSubscription reported offline")
	}
	if calls != 0 {
		t.Fatalf("expected no renew for a fresh subscription, got %d calls", calls)
	}

	// An expiring subscription is renewed and its counters advance.
	db.Model(&model.GA1400Subscription{}).Where("cascade_id = ?", item.ID).
		Update("expires_at", time.Now().Add(10*time.Second))
	if !svc.ensureSubscription(item) {
		t.Fatal("renew failed")
	}
	if calls != 1 {
		t.Fatalf("expected 1 renew call, got %d", calls)
	}
	var sub model.GA1400Subscription
	db.Where("cascade_id = ?", item.ID).First(&sub)
	if sub.RenewCount != 1 || !sub.ExpiresAt.After(time.Now().Add(time.Minute)) {
		t.Fatalf("subscription not renewed: %+v", sub)
	}
}

func TestFullSyncPushesEvents(t *testing.T) {
	db, _ := store.Open(filepath.Join(t.TempDir(), "f.db"))
	var notifications int
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/VIID/Notification" {
			notifications++
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer peer.Close()

	svc := NewCascadeService(db, config.GA1400Config{NotifyURL: peer.URL})
	item := model.GA1400Cascade{Name: "up", Direction: "up", URL: peer.URL, Enabled: true}
	db.Create(&item)
	for i := 0; i < 3; i++ {
		db.Create(&model.AIEvent{Kind: "cv", EventType: "intrusion", Level: "warning", Summary: "x"})
	}
	if n := svc.FullSync(item); n != 3 {
		t.Fatalf("expected 3 records synced, got %d", n)
	}
	if notifications != 3 {
		t.Fatalf("expected 3 notifications, got %d", notifications)
	}
	var fresh model.GA1400Cascade
	db.First(&fresh, item.ID)
	if fresh.SyncCount != 3 || fresh.LastSyncAt.IsZero() {
		t.Fatalf("sync not recorded: %+v", fresh)
	}

	// A second sync only pushes events created since the first one.
	if n := svc.FullSync(fresh); n != 0 {
		t.Fatalf("expected incremental sync to push 0, got %d", n)
	}
}

func TestCascadeSubscribe(t *testing.T) {
	db, _ := store.Open(filepath.Join(t.TempDir(), "s.db"))
	peer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer peer.Close()

	svc := NewCascadeService(db, config.GA1400Config{NotifyURL: "http://easyavr.local"})
	item := model.GA1400Cascade{Name: "down", Direction: "down", URL: peer.URL, Enabled: true}
	if !svc.subscribe(item) {
		t.Fatal("subscribe failed")
	}
	var count int64
	db.Model(&model.GA1400Subscription{}).Count(&count)
	if count != 1 {
		t.Fatalf("expected 1 subscription, got %d", count)
	}
}
