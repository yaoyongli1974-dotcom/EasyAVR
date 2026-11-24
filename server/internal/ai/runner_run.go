package ai

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/easyavr/easyavr/internal/model"
)

// semaphore is a counting semaphore whose limit can change while tasks are
// waiting. Waiters are queued so fairness is preserved and context
// cancellation releases a granted slot to the next waiter.
type semaphore struct {
	mu      sync.Mutex
	limit   int
	active  int
	waiters []chan struct{}
}

func newSemaphore(limit int) *semaphore {
	if limit < 1 {
		limit = 1
	}
	return &semaphore{limit: limit}
}

func (s *semaphore) setLimit(limit int) {
	if limit < 1 {
		limit = 1
	}
	s.mu.Lock()
	s.limit = limit
	s.grantLocked()
	s.mu.Unlock()
}

// grantLocked hands free slots to queued waiters. The caller must hold s.mu.
func (s *semaphore) grantLocked() {
	for s.active < s.limit && len(s.waiters) > 0 {
		ch := s.waiters[0]
		s.waiters = s.waiters[1:]
		s.active++
		close(ch)
	}
}

func (s *semaphore) acquire(ctx context.Context) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	s.mu.Lock()
	if s.active < s.limit && len(s.waiters) == 0 {
		s.active++
		s.mu.Unlock()
		return nil
	}
	ch := make(chan struct{})
	s.waiters = append(s.waiters, ch)
	s.mu.Unlock()

	select {
	case <-ch:
		return nil
	case <-ctx.Done():
		s.mu.Lock()
		found := false
		for i, waiter := range s.waiters {
			if waiter == ch {
				s.waiters = append(s.waiters[:i], s.waiters[i+1:]...)
				found = true
				break
			}
		}
		if !found {
			// A slot was granted concurrently; return it to the semaphore.
			if s.active > 0 {
				s.active--
			}
			s.grantLocked()
		}
		s.mu.Unlock()
		return ctx.Err()
	}
}

func (s *semaphore) release() {
	s.mu.Lock()
	if s.active > 0 {
		s.active--
	}
	s.grantLocked()
	s.mu.Unlock()
}

// taskLoop runs one immediate execution followed by periodic executions until
// ctx is canceled. The ticker stops with the context.
func (r *Runner) taskLoop(ctx context.Context, taskID, channelID uint) {
	var task model.AITask
	if err := r.db.First(&task, taskID).Error; err != nil {
		log.Printf("[ai] task_id=%d load failed: %v", taskID, err)
		return
	}
	cfg := decodeConfig(task.Config)
	intervalSec := cfg.IntervalSec
	if intervalSec < 5 {
		intervalSec = 30
	}
	period := time.Duration(intervalSec) * time.Second
	if period < minTaskInterval {
		period = minTaskInterval
	}
	ticker := time.NewTicker(period)
	defer ticker.Stop()

	r.doWork(ctx, taskID, channelID)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.doWork(ctx, taskID, channelID)
		}
	}
}

// doWork is one scheduled execution: it creates a run record, waits for a
// global and per-channel slot (without holding any Runner lock), executes with
// retries and finally writes the run/task terminal state.
func (r *Runner) doWork(ctx context.Context, taskID, channelID uint) {
	now := time.Now()
	run := &model.AITaskRun{
		TaskID:    taskID,
		Status:    RunPending,
		StartedAt: &now,
		LastRunAt: &now,
		WorkerID:  r.workerID,
	}
	runID := uint(0)
	if err := r.db.Create(run).Error; err != nil {
		log.Printf("[ai] task_id=%d create run failed: %v", taskID, err)
	} else {
		runID = run.ID
	}

	finalized := false
	finish := func(status string, cerr *classifiedError, retries int) {
		if finalized {
			return
		}
		finalized = true
		if runID != 0 {
			r.finalizeRun(taskID, runID, status, cerr, retries)
		}
		code := ""
		if cerr != nil {
			code = string(cerr.code)
		}
		log.Printf("[ai] task_id=%d run_id=%d status=%s error_code=%s retry_count=%d", taskID, runID, status, code, retries)

		taskStatus := "running"
		switch status {
		case RunFailed:
			taskStatus = "failed"
		case RunStopped:
			taskStatus = "stopped"
		}
		errCode, errMsg := "", ""
		if cerr != nil {
			errCode, errMsg = string(cerr.code), cerr.publicMessage()
		}
		r.setTaskStatus(taskID, taskStatus, errCode, errMsg)
	}
	defer func() {
		if finalized {
			return
		}
		if ctx.Err() != nil {
			finish(RunStopped, nil, 0)
		} else {
			finish(RunFailed, classify(ErrorInternal, false, "task execution aborted", nil), 0)
		}
	}()

	if err := r.global.acquire(ctx); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			finish(RunStopped, nil, 0)
		} else {
			finish(RunFailed, classify(ErrorResourceLimit, false, "global concurrency limit", err), 0)
		}
		return
	}
	defer r.global.release()

	sem := r.channelSem(channelID)
	if err := sem.acquire(ctx); err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			finish(RunStopped, nil, 0)
		} else {
			finish(RunFailed, classify(ErrorResourceLimit, false, "channel concurrency limit", err), 0)
		}
		return
	}
	defer sem.release()

	if runID != 0 {
		r.updateRun(runID, map[string]any{"status": RunStarting})
	}
	opts := r.currentOptions()
	attemptCtx, cancel := context.WithTimeout(ctx, opts.TaskTimeout)
	defer cancel()

	// A heartbeat keeps long-running attempts observable after a restart.
	hbStop, hbDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(hbDone)
		ticker := time.NewTicker(heartbeatPeriod(opts.TaskTimeout))
		defer ticker.Stop()
		for {
			select {
			case <-hbStop:
				return
			case <-ticker.C:
				if runID != 0 {
					r.updateRun(runID, map[string]any{"heartbeat_at": time.Now()})
				}
			}
		}
	}()
	defer func() { close(hbStop); <-hbDone }()

	if runID != 0 {
		r.updateRun(runID, map[string]any{"status": RunRunning})
	}
	cerr, retries := r.attemptWithRetry(attemptCtx, taskID, runID)
	if ctx.Err() != nil {
		// A caller-initiated cancel (Stop/Close) is a clean stop, not a failure.
		finish(RunStopped, nil, retries)
		return
	}
	if cerr != nil {
		finish(RunFailed, cerr, retries)
		return
	}
	finish(RunSucceeded, nil, retries)
}

func heartbeatPeriod(timeout time.Duration) time.Duration {
	p := timeout / 4
	if p > 10*time.Second {
		p = 10 * time.Second
	}
	if p < 50*time.Millisecond {
		p = 50 * time.Millisecond
	}
	return p
}

// attemptWithRetry retries only transient provider/stream/frame failures, and
// never after an event has been created (which would duplicate findings and
// notifications). Backoff is base*2^n with a cap.
func (r *Runner) attemptWithRetry(ctx context.Context, taskID, runID uint) (*classifiedError, int) {
	opts := r.currentOptions()
	retries := 0
	var cerr *classifiedError
	for attempt := 0; attempt <= opts.MaxRetries; attempt++ {
		if attempt > 0 {
			delay := backoffDelay(opts.BackoffBase, attempt-1)
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return classify(ErrorInternal, false, "task execution canceled", ctx.Err()), retries
			}
			timer.Stop()
			retries = attempt
			if runID != 0 {
				r.updateRun(runID, map[string]any{
					"status": RunRunning, "retry_count": retries, "last_run_at": time.Now(),
				})
			}
		}
		created, attemptErr := r.executeAttempt(ctx, taskID)
		cerr = attemptErr
		if cerr == nil {
			return nil, retries
		}
		log.Printf("[ai] task_id=%d run_id=%d error_code=%s attempt=%d", taskID, runID, cerr.code, attempt+1)
		if created > 0 || !isRetryable(cerr, ctx) {
			return cerr, retries
		}
	}
	return cerr, retries
}

func backoffDelay(base time.Duration, n int) time.Duration {
	delay := base
	for i := 0; i < n; i++ {
		if delay > maxBackoff/2 {
			return maxBackoff
		}
		delay *= 2
	}
	if delay > maxBackoff {
		return maxBackoff
	}
	return delay
}

func (r *Runner) executeAttempt(ctx context.Context, taskID uint) (int, *classifiedError) {
	var task model.AITask
	if err := r.db.First(&task, taskID).Error; err != nil {
		return 0, classify(ErrorInternal, false, fmt.Sprintf("failed to load task (%T)", err), err)
	}
	events, cerr := r.analyzeTask(ctx, task)
	return len(events), cerr
}

func (r *Runner) finalizeRun(taskID, runID uint, status string, cerr *classifiedError, retries int) {
	now := time.Now()
	fields := map[string]any{
		"status":      status,
		"finished_at": now,
		"retry_count": retries,
	}
	if cerr != nil {
		fields["error_code"] = string(cerr.code)
		fields["error_msg"] = cerr.publicMessage()
	}
	r.updateRun(runID, fields)
}
