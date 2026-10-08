package notify

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/store"
)

// alertFixture builds a db with a webhook channel that records deliveries.
func alertFixture(t *testing.T) (*Service, model.NotificationChannel, *[]model.AIEvent, *sync.Mutex) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "alert.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	var mu sync.Mutex
	var got []model.AIEvent
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var payload struct {
			Event model.AIEvent `json:"event"`
		}
		json.NewDecoder(r.Body).Decode(&payload)
		mu.Lock()
		got = append(got, payload.Event)
		mu.Unlock()
	}))
	t.Cleanup(hook.Close)

	ch := model.NotificationChannel{Name: "wh", Type: "webhook", URL: hook.URL, Enabled: true}
	if err := db.Create(&ch).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	return NewService(db), ch, &got, &mu
}

func count(got *[]model.AIEvent, mu *sync.Mutex) int {
	mu.Lock()
	defer mu.Unlock()
	return len(*got)
}

func TestAlertImmediateTierAndEscalation(t *testing.T) {
	svc, ch, got, mu := alertFixture(t)

	p := model.AlertPolicy{Name: "critical-people", Enabled: true, MinLevel: "warning", AckRequired: true}
	if err := svc.db.Create(&p).Error; err != nil {
		t.Fatalf("create policy: %v", err)
	}
	t0 := model.AlertPolicyTier{PolicyID: p.ID, Tier: 0, DelaySec: 0, TargetIDs: itoa(ch.ID)}
	t1 := model.AlertPolicyTier{PolicyID: p.ID, Tier: 1, DelaySec: 1, TargetIDs: itoa(ch.ID)}
	svc.db.Create(&t0)
	svc.db.Create(&t1)

	now := time.Now()
	ev := model.AIEvent{Kind: "cv", EventType: "person_intrusion", Level: "warning", Summary: "person", OccurredAt: now}
	svc.db.Create(&ev)

	// Immediate tier fires synchronously.
	svc.Alert(ev)
	if count(got, mu) != 1 {
		t.Fatalf("expected 1 immediate delivery, got %d", count(got, mu))
	}

	// Escalation not due yet.
	if n := svc.Escalate(now); n != 0 {
		t.Fatalf("expected no escalation before delay, got %d", n)
	}

	// After the delay the unacknowledged event escalates once.
	if n := svc.Escalate(now.Add(2 * time.Second)); n != 1 {
		t.Fatalf("expected 1 escalation, got %d", n)
	}
	if count(got, mu) != 2 {
		t.Fatalf("expected 2 total deliveries, got %d", count(got, mu))
	}
	// Re-running must not duplicate (idempotent per policy/tier/event).
	if n := svc.Escalate(now.Add(2 * time.Second)); n != 0 {
		t.Fatalf("expected no duplicate escalation, got %d", n)
	}
}

func TestAlertAckStopsEscalation(t *testing.T) {
	svc, ch, got, mu := alertFixture(t)

	p := model.AlertPolicy{Name: "acked", Enabled: true, MinLevel: "info", AckRequired: true}
	svc.db.Create(&p)
	svc.db.Create(&model.AlertPolicyTier{PolicyID: p.ID, Tier: 1, DelaySec: 1, TargetIDs: itoa(ch.ID)})

	now := time.Now()
	acked := model.AIEvent{Level: "critical", EventType: "fire", OccurredAt: now.Add(-5 * time.Second), Acked: true}
	svc.db.Create(&acked)

	// Acked event must not escalate.
	if n := svc.Escalate(now); n != 0 {
		t.Fatalf("acked event should not escalate, got %d", n)
	}
	if count(got, mu) != 0 {
		t.Fatalf("no delivery expected for acked event, got %d", count(got, mu))
	}
}

func TestAlertCooldownSuppresses(t *testing.T) {
	svc, ch, got, mu := alertFixture(t)

	p := model.AlertPolicy{Name: "cooldown", Enabled: true, MinLevel: "info", CooldownSec: 60}
	svc.db.Create(&p)
	svc.db.Create(&model.AlertPolicyTier{PolicyID: p.ID, Tier: 0, DelaySec: 0, TargetIDs: itoa(ch.ID)})

	svc.Alert(model.AIEvent{Level: "warning", EventType: "motion", Summary: "a"})
	svc.Alert(model.AIEvent{Level: "warning", EventType: "motion", Summary: "b"})
	if count(got, mu) != 1 {
		t.Fatalf("cooldown should suppress the second alert, got %d", count(got, mu))
	}
}

func TestAlertFilterAndDeliveryAudit(t *testing.T) {
	svc, ch, _, _ := alertFixture(t)

	p := model.AlertPolicy{Name: "only-fire", Enabled: true, EventType: "fire", MinLevel: "warning"}
	svc.db.Create(&p)
	svc.db.Create(&model.AlertPolicyTier{PolicyID: p.ID, Tier: 0, DelaySec: 0, TargetIDs: itoa(ch.ID)})

	svc.Alert(model.AIEvent{EventType: "person", Level: "critical"}) // wrong type
	svc.Alert(model.AIEvent{EventType: "fire", Level: "info"})       // below min level
	svc.Alert(model.AIEvent{EventType: "fire", Level: "warning"})    // matches

	var records []model.AlertDelivery
	svc.db.Find(&records)
	if len(records) != 1 || records[0].Status != "success" || records[0].Reason != "immediate" {
		t.Fatalf("unexpected delivery audit: %+v", records)
	}
	if records[0].ChannelID != ch.ID {
		t.Fatalf("delivery should reference the channel: %+v", records[0])
	}
}
