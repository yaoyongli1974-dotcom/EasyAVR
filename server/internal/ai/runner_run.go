package ai

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
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
	closed  bool
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
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	s.limit = limit
	s.grantLocked()
}

// close aborts every waiter and blocks new acquisitions. Called by Runner.Close
// so shutdown does not leave goroutines parked on a semaphore.
func (s *semaphore) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	for _, ch := range s.waiters {
		close(ch)
	}
	s.waiters = nil
	s.active = 0
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
	if s.closed {
		s.mu.Unlock()
		return ErrRunnerClosed
	}
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
		s.mu.Lock()
		closed := s.closed
		s.mu.Unlock()
		if closed {
			return ErrRunnerClosed
		}
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
		if !found && !s.closed {
			// A slot was granted concurrently; return it to the semaphore.
			if s.active > 0 {
				s.active--
			}
			s.grantLocked()
		}
		err := ctx.Err()
		s.mu.Unlock()
		if err == nil {
			return ErrRunnerClosed // woken by close(), not by a granted slot
		}
		return err
	}
}

func (s *semaphore) release() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return
	}
	if s.active > 0 {
		s.active--
	}
	s.grantLocked()
}

// stopRequested reports whether this execution was canceled by Stop/Close or
// hit its own deadline. Both are clean stops, not provider failures.
func stopRequested(ctx context.Context, err error) bool {
	return errors.Is(err, ErrRunnerClosed) || errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) || ctx.Err() != nil
}

// taskLoop runs one immediate execution followed by periodic executions until
// ctx is canceled. The ticker stops with the context.
func (r *Runner) taskLoop(ctx context.Context, taskID, channelID uint) {
	if ctx.Err() != nil {
		return
	}
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
			if ctx.Err() != nil {
				return
			}
			r.doWork(ctx, taskID, channelID)
		}
	}
}

// doWork is one scheduled execution: it creates a run record BEFORE any AI
// analysis, waits for a global and per-channel slot (without holding any
// Runner lock), executes with retries and finally writes run/task terminal
// state. Every retry opens a fresh run record; a runID is never reused.
func (r *Runner) doWork(ctx context.Context, taskID, channelID uint) {
	// Never open a new run (or start a new execution) after cancellation.
	if ctx.Err() != nil || r.IsClosed() {
		return
	}
	// current is the run row for the attempt in flight. It is atomic because
	// the heartbeat goroutine reads it while the main goroutine swaps it on a
	// retry.
	var current atomic.Uint64
	getRunID := func() uint { return uint(current.Load()) }
	finalized := false

	// newRun either opens a fresh run row or aborts the whole execution. A
	// create failure is a hard stop: no analyzer call may follow it.
	newRun := func(attempt int, prev *classifiedError) bool {
		if prevID := getRunID(); prevID != 0 && prev != nil {
			// Close out the failed attempt before opening its successor.
			if err := r.finalizeRun(taskID, prevID, RunFailed, prev, attempt-1); err != nil {
				log.Printf("[ai] ALERT task_id=%d run_id=%d previous attempt status not persisted: %v", taskID, prevID, err)
			}
		}
		now := time.Now()
		run := &model.AITaskRun{
			TaskID:     taskID,
			Status:     RunPending,
			StartedAt:  &now,
			LastRunAt:  &now,
			WorkerID:   r.workerID,
			RetryCount: attempt,
		}
		if err := r.db.Create(run).Error; err != nil {
			atomic.AddInt64(&r.runCreateFailures, 1)
			log.Printf("[ai] ALERT task_id=%d create run failed attempt=%d code=%s err=%v; analysis skipped",
				taskID, attempt, ErrorInternal, err)
			r.setTaskStatus(taskID, "failed", string(ErrorInternal), "failed to create task run record")
			finalized = true // the deferred finalizer must not overwrite the alert above
			return false
		}
		current.Store(uint64(run.ID))
		finalized = false
		return true
	}

	// finish is the finalizer for the attempt currently in flight. The runID
	// guard is preserved: with no persisted run there is no run to finalize.
	finish := func(status string, cerr *classifiedError, retries int) {
		if finalized {
			return
		}
		finalized = true
		if id := getRunID(); id != 0 {
			if err := r.finalizeRun(taskID, id, status, cerr, retries); err != nil {
				log.Printf("[ai] ALERT task_id=%d run_id=%d terminal status=%s not persisted: %v", taskID, id, status, err)
				r.setTaskStatus(taskID, "failed", string(ErrorInternal), "failed to persist run terminal status")
				return
			}
		}
		code := ""
		if cerr != nil {
			code = string(cerr.code)
		}
		log.Printf("[ai] task_id=%d run_id=%d status=%s error_code=%s retry_count=%d", taskID, getRunID(), status, code, retries)

		taskStatus := "running"
		switch status {
		case RunFailed, RunTimeout:
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

	// No analysis before the run record exists, and none after cancellation.
	if ctx.Err() != nil || !newRun(0, nil) {
		return
	}

	if err := r.global.acquire(ctx); err != nil {
		if stopRequested(ctx, err) {
			finish(RunStopped, nil, 0)
		} else {
			finish(RunFailed, classify(ErrorResourceLimit, false, "global concurrency limit", err), 0)
		}
		return
	}
	defer r.global.release()

	sem := r.channelSem(channelID)
	if err := sem.acquire(ctx); err != nil {
		if stopRequested(ctx, err) {
			finish(RunStopped, nil, 0)
		} else {
			finish(RunFailed, classify(ErrorResourceLimit, false, "channel concurrency limit", err), 0)
		}
		return
	}
	defer sem.release()

	opts := r.currentOptions()

	// A heartbeat keeps long-running attempts observable after a restart. It is
	// tracked so Close does not return while a heartbeat write is in flight.
	hbStop, hbDone := make(chan struct{}), make(chan struct{})
	tracked := r.track(1)
	go func() {
		defer close(hbDone)
		if tracked {
			defer r.finishTrack()
		}
		ticker := time.NewTicker(heartbeatPeriod(opts.TaskTimeout))
		defer ticker.Stop()
		for {
			select {
			case <-hbStop:
				return
			case <-ticker.C:
				if r.IsClosed() {
					return
				}
				if id := getRunID(); id != 0 {
					_ = r.updateRun(id, map[string]any{"heartbeat_at": time.Now()}) // updateRun alerts on failure
				}
			}
		}
	}()
	defer func() { close(hbStop); <-hbDone }()

	cerr, retries, timedOut := r.attemptWithRetry(ctx, taskID, getRunID, newRun)
	if ctx.Err() != nil {
		// A caller-initiated cancel (Stop/Close) is a clean stop, not a failure.
		finish(RunStopped, nil, retries)
		return
	}
	switch {
	case timedOut:
		if cerr == nil {
			cerr = classify(ErrorProviderTimeout, false, "task execution timed out", nil)
		}
		finish(RunTimeout, cerr, retries)
	case cerr != nil:
		finish(RunFailed, cerr, retries)
	default:
		finish(RunSucceeded, nil, retries)
	}
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
// notifications). Backoff is base*2^n with a cap. Each retry gets a brand new
// run record, so a failed attempt can never be silently rewritten as a later
// successful one. The returned flag reports a hard timeout.
func (r *Runner) attemptWithRetry(
	ctx context.Context,
	taskID uint,
	getRunID func() uint,
	newRun func(int, *classifiedError) bool,
) (*classifiedError, int, bool) {
	opts := r.currentOptions()
	retries := 0
	var cerr *classifiedError
	for attempt := 0; attempt <= opts.MaxRetries; attempt++ {
		if ctx.Err() != nil {
			return classify(ErrorInternal, false, "task execution canceled", ctx.Err()), retries, false
		}
		if attempt > 0 {
			delay := backoffDelay(opts.BackoffBase, attempt-1)
			timer := time.NewTimer(delay)
			select {
			case <-timer.C:
			case <-ctx.Done():
				timer.Stop()
				return classify(ErrorInternal, false, "task execution canceled", ctx.Err()), retries, false
			}
			timer.Stop()
			retries = attempt
			if !newRun(attempt, cerr) {
				return classify(ErrorInternal, false, "failed to create retry run record", nil), retries, false
			}
		}
		stepCtx, cancelStep := context.WithTimeout(ctx, opts.TaskTimeout)
		created, stepErr, timedOut := r.runSingleAttempt(stepCtx, taskID, getRunID(), retries)
		cancelStep()
		cerr = stepErr
		if cerr == nil {
			return nil, retries, false
		}
		log.Printf("[ai] task_id=%d run_id=%d error_code=%s attempt=%d", taskID, getRunID(), cerr.code, attempt+1)
		if timedOut || created > 0 || !isRetryable(cerr, ctx) {
			return cerr, retries, timedOut
		}
	}
	return cerr, retries, false
}

// runSingleAttempt performs one analysis with its own timeout window and
// records the starting/running transitions. Every update failure is surfaced
// by updateRun (ALERT log + counter) instead of being swallowed.
func (r *Runner) runSingleAttempt(ctx context.Context, taskID, runID uint, retries int) (int, *classifiedError, bool) {
	if err := r.updateRun(runID, map[string]any{"status": RunStarting}); err != nil {
		log.Printf("[ai] ALERT task_id=%d run_id=%d starting status not persisted: %v", taskID, runID, err)
	}
	if err := r.updateRun(runID, map[string]any{"status": RunRunning, "retry_count": retries}); err != nil {
		log.Printf("[ai] ALERT task_id=%d run_id=%d running status not persisted: %v", taskID, runID, err)
	}

	created, cerr := r.executeAttempt(ctx, taskID)
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
	if timedOut && (cerr == nil || cerr.code != ErrorProviderTimeout) {
		cerr = classify(ErrorProviderTimeout, false, "task execution timed out", ctx.Err())
	}
	return created, cerr, timedOut
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

// finalizeRun writes a terminal run status. The returned error must be handled
// by the caller: a dropped terminal update leaves the run looking live.
func (r *Runner) finalizeRun(taskID, runID uint, status string, cerr *classifiedError, retries int) error {
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
	return r.updateRun(runID, fields)
}
