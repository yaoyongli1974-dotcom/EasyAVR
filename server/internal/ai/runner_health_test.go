package ai

import (
	"errors"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/model"
)

func insertHealthRun(t *testing.T, db *gorm.DB, taskID uint, status string, started, last, heartbeat *time.Time) {
	t.Helper()
	run := model.AITaskRun{
		TaskID: taskID, Status: status,
		StartedAt: started, LastRunAt: last, HeartbeatAt: heartbeat,
	}
	if err := db.Create(&run).Error; err != nil {
		t.Fatalf("seed run: %v", err)
	}
}

func timePtr(t time.Time) *time.Time { return &t }

// A runner without a database must degrade safely instead of panicking.
func TestHealthStatsWithoutDatabase(t *testing.T) {
	r := NewRunner(nil, nil)
	stats := r.HealthStats()
	if stats.Status != HealthDegraded || stats.Healthy() {
		t.Fatalf("status=%q stats=%+v", stats.Status, stats)
	}
	if stats.DBAvailable {
		t.Fatal("DBAvailable=true with nil database")
	}
	if stats.Closed || stats.RunningTasks != 0 || stats.ActiveRuns != 0 {
		t.Fatalf("unexpected stats=%+v", stats)
	}
	found := false
	for _, alert := range stats.Alerts {
		if alert == "ai run statistics unavailable: database unavailable" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing database alert: %+v", stats.Alerts)
	}
}

// Stats must reflect real run history, not merely runner presence.
func TestHealthStatsReflectsRunHistoryAndRecovery(t *testing.T) {
	db := newRunnerTestDB(t)
	taskID, _ := seedRunnerTask(t, db, `{"intervalSec":30}`)
	r := NewRunnerWithOptions(db, nil, RunnerOptions{TaskTimeout: time.Second})
	defer r.Close()

	// A full recovery with no running tasks is a successful baseline.
	if err := r.Recover(); err != nil {
		t.Fatalf("recover: %v", err)
	}
	stats := r.HealthStats()
	if !stats.LastRecoverOK || stats.LastRecoverAt == nil || stats.LastRecoverError != "" {
		t.Fatalf("recovery baseline=%+v", stats)
	}
	if stats.Status != HealthAvailable || !stats.Healthy() {
		t.Fatalf("healthy runner status=%q stats=%+v", stats.Status, stats)
	}

	old := time.Now().Add(-time.Hour)
	insertHealthRun(t, db, taskID, RunSucceeded, timePtr(old), timePtr(old), nil)
	for range []int{0, 1, 2} {
		insertHealthRun(t, db, taskID, RunFailed, timePtr(old), timePtr(old), nil)
	}
	// An active run whose heartbeat stopped an hour ago.
	insertHealthRun(t, db, taskID, RunRunning, timePtr(old), timePtr(old), timePtr(old))
	insertHealthRun(t, db, taskID, RunPending, nil, nil, nil)

	// A failed recovery must be reported as a sanitized category, never as a
	// raw DB error (which could embed a DSN).
	r.setRecoverStatus(errors.New("dial tcp: secret-dsn@db:5432 failed"))

	stats = r.HealthStats()
	if stats.Status != HealthDegraded || stats.Healthy() {
		t.Fatalf("status=%q stats=%+v", stats.Status, stats)
	}
	if stats.ConsecutiveFailures != 3 {
		t.Errorf("consecutive failures=%d want 3", stats.ConsecutiveFailures)
	}
	if stats.ActiveRuns != 2 || stats.QueuedRuns != 1 {
		t.Errorf("active=%d queued=%d want 2/1", stats.ActiveRuns, stats.QueuedRuns)
	}
	if stats.LongRunningRuns < 1 || stats.StaleHeartbeats < 1 {
		t.Errorf("long=%d stale=%d want >=1 each", stats.LongRunningRuns, stats.StaleHeartbeats)
	}
	if stats.LastRunAt == nil || stats.LastHeartbeatAt == nil {
		t.Errorf("missing timestamps: %+v", stats)
	}
	if stats.LastRecoverOK || stats.LastRecoverError != "database_error" {
		t.Errorf("recovery=%v/%q", stats.LastRecoverOK, stats.LastRecoverError)
	}
	wantAlerts := []string{
		"ai scheduler heartbeat stale", "ai recovery failed",
	}
	for _, want := range wantAlerts {
		found := false
		for _, alert := range stats.Alerts {
			if alert == want {
				found = true
			}
		}
		if !found {
			t.Errorf("missing alert %q in %+v", want, stats.Alerts)
		}
	}

	// A newer success and a healthy recovery clear the degraded signals.
	now := time.Now()
	insertHealthRun(t, db, taskID, RunSucceeded, timePtr(now), timePtr(now), nil)
	if err := db.Model(&model.AITaskRun{}).
		Where("status IN ?", []string{RunRunning, RunPending}).
		Update("status", RunSucceeded).Error; err != nil {
		t.Fatalf("finalize runs: %v", err)
	}
	r.setRecoverStatus(nil)
	stats = r.HealthStats()
	if stats.Status != HealthAvailable {
		t.Fatalf("recovered status=%q alerts=%+v", stats.Status, stats.Alerts)
	}
	if stats.ConsecutiveFailures != 0 {
		t.Errorf("consecutive failures=%d want 0", stats.ConsecutiveFailures)
	}
}

// A closed runner is unavailable and says so in the alert list.
func TestHealthStatsClosedRunner(t *testing.T) {
	r := NewRunner(nil, nil)
	r.Close()
	stats := r.HealthStats()
	if stats.Status != HealthUnavailable || !stats.Closed || stats.Healthy() {
		t.Fatalf("status=%q closed=%v stats=%+v", stats.Status, stats.Closed, stats)
	}
	found := false
	for _, alert := range stats.Alerts {
		if alert == "ai runner closed" {
			found = true
		}
	}
	if !found {
		t.Fatalf("missing closed alert: %+v", stats.Alerts)
	}
}

// HealthStats is read-only and safe to call while the scheduler mutates state
// (verified with -race by the package test suite).
func TestHealthStatsConcurrentReads(t *testing.T) {
	db := newRunnerTestDB(t)
	taskID, _ := seedRunnerTask(t, db, `{"intervalSec":30}`)
	r := NewRunnerWithOptions(db, nil, RunnerOptions{TaskTimeout: time.Second})
	defer r.Close()

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 20 {
				_ = r.HealthStats()
			}
		}()
	}
	for range 20 {
		insertHealthRun(t, db, taskID, RunSucceeded, nil, nil, nil)
	}
	wg.Wait()
}
