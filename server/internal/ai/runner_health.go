package ai

import (
	"errors"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/model"
)

// Health status values exposed by Runner.HealthStats.
const (
	HealthAvailable   = "available"
	HealthDegraded    = "degraded"
	HealthUnavailable = "unavailable"
)

const (
	// healthRunSampleLimit bounds the read used to count consecutive failures.
	healthRunSampleLimit = 100
	// consecutiveFailureAlertLimit is the number of back-to-back failed/timeout
	// runs that turns the runner health from available to degraded.
	consecutiveFailureAlertLimit = 3
	// staleHeartbeatFactor multiplies the heartbeat period to decide that an
	// active run is no longer making progress.
	staleHeartbeatFactor = 3
)

// HealthStats is a sanitized point-in-time view of the AI runner used by the
// health probes. It contains only counters, timestamps and short category
// strings: prompts, provider keys, DSNs and configuration values are never
// read, so the whole struct is safe to serialize into a public response.
type HealthStats struct {
	Status              string     `json:"status"`
	Available           bool       `json:"available"`
	Closed              bool       `json:"closed"`
	RunningTasks        int        `json:"runningTasks"`
	WaitingTasks        int        `json:"waitingTasks"`
	ActiveRuns          int64      `json:"activeRuns"`
	QueuedRuns          int64      `json:"queuedRuns"`
	ConsecutiveFailures int        `json:"consecutiveFailures"`
	LongRunningRuns     int64      `json:"longRunningRuns"`
	StaleHeartbeats     int64      `json:"staleHeartbeats"`
	LastRunAt           *time.Time `json:"lastRunAt,omitempty"`
	LastHeartbeatAt     *time.Time `json:"lastHeartbeatAt,omitempty"`
	LastRecoverAt       *time.Time `json:"lastRecoverAt,omitempty"`
	RecoverAttempted    bool       `json:"recoverAttempted"`
	LastRecoverOK       bool       `json:"lastRecoverOK"`
	LastRecoverError    string     `json:"lastRecoverError,omitempty"`
	DBAvailable         bool       `json:"dbAvailable"`
	RunCreateFailures   int64      `json:"runCreateFailures"`
	RunUpdateFailures   int64      `json:"runUpdateFailures"`
	TaskStatusFailures  int64      `json:"taskStatusFailures"`
	// Alerts holds short sanitized explanations for a degraded/unavailable
	// status. They are safe to copy verbatim into a public probe.
	Alerts []string `json:"alerts,omitempty"`
}

// Healthy reports whether the runner is fully operational.
func (s HealthStats) Healthy() bool { return s.Status == HealthAvailable }

// HealthStats returns a read-only snapshot of scheduler/worker health. In-memory
// state is snapshotted under the runner lock; run history is read from the
// AITaskRun table with plain SELECTs. A missing or broken database degrades the
// AI check safely instead of failing the probe.
func (r *Runner) HealthStats() HealthStats {
	s := HealthStats{Status: HealthAvailable, Available: true}

	// In-memory state: live loops and threads parked on concurrency slots.
	r.mu.Lock()
	s.Closed = r.closed
	for _, state := range r.tasks {
		select {
		case <-state.done:
		default:
			s.RunningTasks++
		}
	}
	waiting := 0
	if r.global != nil {
		waiting += r.global.waiting()
	}
	for _, sem := range r.chSems {
		waiting += sem.waiting()
	}
	r.mu.Unlock()
	s.WaitingTasks = waiting

	s.RunCreateFailures = atomic.LoadInt64(&r.runCreateFailures)
	s.RunUpdateFailures = atomic.LoadInt64(&r.runUpdateFailures)
	s.TaskStatusFailures = atomic.LoadInt64(&r.taskStatusFailures)

	// Recovery is a single-writer field; copy it under its own lock. The
	// pointers are replaced rather than mutated, so the snapshot is stable.
	r.recoverStatusMu.RLock()
	s.RecoverAttempted = r.recoverAttempted
	s.LastRecoverAt = r.lastRecoverAt
	s.LastRecoverOK = r.lastRecoverOK
	s.LastRecoverError = r.lastRecoverErr
	r.recoverStatusMu.RUnlock()

	if s.Closed {
		s.Status = HealthUnavailable
		s.Alerts = append(s.Alerts, "ai runner closed")
	}
	if r.db == nil {
		s.degrade("ai run statistics unavailable: database unavailable")
		return s
	}

	dbUnavailable := func() HealthStats {
		s.DBAvailable = false
		s.degrade("ai run statistics unavailable: database unavailable")
		return s
	}
	s.DBAvailable = true
	activeStatuses := nonTerminalRunStatuses
	if err := r.db.Model(&model.AITaskRun{}).Where("status IN ?", activeStatuses).Count(&s.ActiveRuns).Error; err != nil {
		return dbUnavailable()
	}
	if err := r.db.Model(&model.AITaskRun{}).Where("status = ?", RunPending).Count(&s.QueuedRuns).Error; err != nil {
		return dbUnavailable()
	}

	// Most recent schedule/heartbeat across this worker's run history. Pluck
	// keeps the driver's native time type: SQLite returns MAX() expressions as
	// strings, which cannot be scanned into *time.Time.
	var lastRuns, lastHeartbeats []time.Time
	if err := r.db.Model(&model.AITaskRun{}).Where("last_run_at IS NOT NULL").
		Order("last_run_at DESC").Limit(1).Pluck("last_run_at", &lastRuns).Error; err != nil {
		return dbUnavailable()
	}
	if err := r.db.Model(&model.AITaskRun{}).Where("heartbeat_at IS NOT NULL").
		Order("heartbeat_at DESC").Limit(1).Pluck("heartbeat_at", &lastHeartbeats).Error; err != nil {
		return dbUnavailable()
	}
	if len(lastRuns) > 0 {
		s.LastRunAt = &lastRuns[0]
	}
	if len(lastHeartbeats) > 0 {
		s.LastHeartbeatAt = &lastHeartbeats[0]
	}

	// Consecutive terminal failures, ignoring in-flight attempts.
	var statuses []string
	if err := r.db.Model(&model.AITaskRun{}).
		Where("status IN ?", []string{RunFailed, RunTimeout, RunSucceeded, RunStopped}).
		Order("id DESC").Limit(healthRunSampleLimit).Pluck("status", &statuses).Error; err != nil {
		return dbUnavailable()
	}
	for _, status := range statuses {
		if status != RunFailed && status != RunTimeout {
			break
		}
		s.ConsecutiveFailures++
	}

	opts := r.currentOptions()
	now := time.Now()
	timeoutCutoff := now.Add(-opts.TaskTimeout)
	staleCutoff := now.Add(-staleHeartbeatFactor * heartbeatPeriod(opts.TaskTimeout))
	if err := r.db.Model(&model.AITaskRun{}).
		Where("status IN ? AND started_at < ?", activeStatuses, timeoutCutoff).
		Count(&s.LongRunningRuns).Error; err != nil {
		return dbUnavailable()
	}
	if err := r.db.Model(&model.AITaskRun{}).
		Where("status IN ? AND COALESCE(heartbeat_at, last_run_at, started_at) < ?", activeStatuses, staleCutoff).
		Count(&s.StaleHeartbeats).Error; err != nil {
		return dbUnavailable()
	}

	if s.Status != HealthUnavailable {
		if s.StaleHeartbeats > 0 {
			s.degrade("ai scheduler heartbeat stale")
		}
		if s.LongRunningRuns > 0 {
			s.degrade("ai runs exceeding task timeout")
		}
		if s.ConsecutiveFailures >= consecutiveFailureAlertLimit {
			s.degrade("ai runs failing consecutively")
		}
		if s.RecoverAttempted && !s.LastRecoverOK {
			s.degrade("ai recovery failed")
		}
		if s.RunCreateFailures > 0 || s.RunUpdateFailures > 0 || s.TaskStatusFailures > 0 {
			s.degrade("ai run state persistence failures")
		}
	}
	return s
}

// degrade downgrades the status unless the runner is already unavailable and
// appends a sanitized alert (without duplicating identical messages).
func (s *HealthStats) degrade(message string) {
	if s.Status != HealthUnavailable {
		s.Status = HealthDegraded
	}
	for _, existing := range s.Alerts {
		if existing == message {
			return
		}
	}
	s.Alerts = append(s.Alerts, message)
}

// setRecoverStatus records the outcome of a full recovery scan. Only category
// strings are kept: raw DB errors can embed a DSN and must never reach a probe.
func (r *Runner) setRecoverStatus(err error) {
	r.recoverStatusMu.Lock()
	defer r.recoverStatusMu.Unlock()
	now := time.Now()
	r.recoverAttempted = true
	r.lastRecoverAt = &now
	r.lastRecoverOK = err == nil
	r.lastRecoverErr = sanitizeRecoverError(err)
}

func sanitizeRecoverError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, ErrRunnerClosed) {
		return "runner_closed"
	}
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "record_not_found"
	}
	return "database_error"
}
