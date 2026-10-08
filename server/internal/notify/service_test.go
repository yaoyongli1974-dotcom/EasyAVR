package notify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/store"
)

func itoa(n uint) string { return strconv.FormatUint(uint64(n), 10) }

func TestWebhookDeliveryAndLevelFilter(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "n.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	got := make(chan model.AIEvent, 4)
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Event model.AIEvent `json:"event"`
		}
		json.NewDecoder(r.Body).Decode(&payload)
		got <- payload.Event
	}))
	defer hook.Close()

	ch := model.NotificationChannel{Name: "wh", Type: "webhook", URL: hook.URL, Enabled: true}
	db.Create(&ch)
	db.Create(&model.NotificationRule{
		Name: "alerts", Enabled: true, TargetIDs: itoa(ch.ID), MinLevel: "warning",
	})

	svc := NewService(db)
	svc.Notify(model.AIEvent{Kind: "cv", EventType: "person", Level: "warning", Summary: "x"})

	select {
	case ev := <-got:
		if ev.EventType != "person" {
			t.Fatalf("wrong event delivered: %+v", ev)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("warning event was not delivered")
	}

	// Below the min level -> must be filtered out.
	svc.Notify(model.AIEvent{Kind: "cv", EventType: "noise", Level: "info"})
	select {
	case ev := <-got:
		t.Fatalf("info event should have been filtered, got %+v", ev)
	case <-time.After(500 * time.Millisecond):
	}
}

func TestEventTypeFilter(t *testing.T) {
	db, _ := store.Open(filepath.Join(t.TempDir(), "n2.db"))
	svc := NewService(db)
	r := model.NotificationRule{Enabled: true, EventType: "fire"}
	if svc.match(r, model.AIEvent{EventType: "fire"}) != true {
		t.Fatal("expected fire to match")
	}
	if svc.match(r, model.AIEvent{EventType: "person"}) {
		t.Fatal("expected person not to match")
	}
}
