package ai

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/easyavr/easyavr/internal/model"
)

// waitClose waits for Close/Shutdown without hanging the whole test suite.
func waitClose(t *testing.T, timeout time.Duration, fn func()) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		fn()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(timeout):
		t.Fatal("shutdown did not return in time")
	}
}

func assertQuietRunner(t *testing.T, r *Runner) {
	t.Helper()
	r.mu.Lock()
	inflight, tasks, sems := r.inflight, len(r.tasks), len(r.chSems)
	r.mu.Unlock()
	if inflight != 0 || tasks != 0 || sems != 0 {
		t.Fatalf("shutdown residue: inflight=%d tasks=%d semaphores=%d", inflight, tasks, sems)
	}
}

func assertClosedRejectsWork(t *testing.T, r *Runner) {
	t.Helper()
	if !r.IsClosed() {
		t.Fatal("runner is not marked closed")
	}
	select {
	case <-r.Done():
	default:
		t.Fatal("Done channel was not closed")
	}
	begin := time.Now()
	errs := []error{r.Start(1), r.Recover(), r.RecoverTask(1)}
	if _, err := r.RunOnce(context.Background(), model.AITask{}); err != nil {
		errs = append(errs, err)
	} else {
		t.Fatal("RunOnce succeeded after shutdown")
	}
	if elapsed := time.Since(begin); elapsed > time.Second {
		t.Fatalf("post-close calls blocked for %v", elapsed)
	}
	for i, err := range errs {
		if !errors.Is(err, ErrRunnerClosed) {
			t.Fatalf("call[%d] after close = %v, want ErrRunnerClosed", i, err)
		}
	}
}

// Normal start/close: the loop exits, run/task state is finalized and all
// resources are released. Repeated and concurrent Close are no-ops.
func TestLifecycleNormalStartAndClose(t *testing.T) {
	runWithInterval(t, time.Hour)
	db := newRunnerTestDB(t)
	taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
	analyzer := &fakeAnalyzer{fn: func(context.Context, AnalyzeInput) ([]Result, error) {
		return []Result{{EventType: "person"}}, nil
	}}
	r := NewRunnerWithOptions(db, nil, RunnerOptions{TaskTimeout: time.Second})
	r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })

	if err := r.Start(taskID); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		runs := runsFor(t, db, taskID)
		return len(runs) > 0 && runs[0].Status == RunSucceeded
	})
	waitClose(t, 2*time.Second, r.Close)
	assertQuietRunner(t, r)
	assertClosedRejectsWork(t, r)

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); r.Close(); r.Shutdown() }()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("repeated/concurrent Close blocked")
	}
	if runs := runsFor(t, db, taskID); len(runs) != 1 {
		t.Fatalf("post-close calls created runs: %d", len(runs))
	}
}

// Closing while an analyzer is in flight cancels it and waits for the final DB
// write. No execution or DB write may happen after Close returns.
func TestLifecycleCloseWhileRunning(t *testing.T) {
	runWithInterval(t, 20*time.Millisecond)
	db := newRunnerTestDB(t)
	taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
	entered := make(chan struct{}, 4)
	var canceled atomic.Bool
	analyzer := &fakeAnalyzer{fn: func(ctx context.Context, _ AnalyzeInput) ([]Result, error) {
		entered <- struct{}{}
		<-ctx.Done()
		canceled.Store(true)
		return nil, ctx.Err()
	}}
	r := NewRunnerWithOptions(db, nil, RunnerOptions{TaskTimeout: 5 * time.Second})
	r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
	if err := r.Start(taskID); err != nil {
		t.Fatalf("start: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("analyzer never started")
	}

	waitClose(t, 3*time.Second, r.Close)
	if !canceled.Load() {
		t.Fatal("in-flight analyzer was not canceled")
	}
	calls := analyzer.callCount()
	runCount := len(runsFor(t, db, taskID))
	time.Sleep(120 * time.Millisecond)
	if got := analyzer.callCount(); got != calls {
		t.Fatalf("execution started after Close: %d -> %d", calls, got)
	}
	if got := len(runsFor(t, db, taskID)); got != runCount {
		t.Fatalf("DB write after Close: runs %d -> %d", runCount, got)
	}
	runs := runsFor(t, db, taskID)
	if len(runs) == 0 || runs[len(runs)-1].Status != RunStopped || runs[len(runs)-1].FinishedAt == nil {
		t.Fatalf("in-flight run was not stopped: %+v", runs)
	}
	assertQuietRunner(t, r)
	assertClosedRejectsWork(t, r)
}

// A task waiting for a global slot must be released on shutdown instead of
// parking on the semaphore until its context happens to expire.
func TestLifecycleCloseReleasesPendingSlot(t *testing.T) {
	runWithInterval(t, time.Hour)
	db := newRunnerTestDB(t)
	release := make(chan struct{})
	entered := make(chan struct{}, 2)
	analyzer := &fakeAnalyzer{fn: func(ctx context.Context, _ AnalyzeInput) ([]Result, error) {
		entered <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil, nil
	}}
	r := NewRunnerWithOptions(db, nil, RunnerOptions{
		MaxConcurrency: 1, PerChannelLimit: 1, TaskTimeout: 5 * time.Second,
	})
	r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
	ids := []uint{}
	for i := 0; i < 2; i++ {
		id, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
		ids = append(ids, id)
		if err := r.Start(id); err != nil {
			t.Fatalf("start[%d]: %v", i, err)
		}
	}
	<-entered
	waitFor(t, 2*time.Second, func() bool {
		for _, id := range ids {
			if len(runsFor(t, db, id)) == 0 {
				return false
			}
		}
		return true
	})
	waitClose(t, 3*time.Second, r.Close)
	for _, id := range ids {
		runs := runsFor(t, db, id)
		if len(runs) == 0 || runs[0].FinishedAt == nil || runs[0].Status == RunRunning {
			t.Fatalf("task_id=%d pending run not finalized: %+v", id, runs)
		}
	}
	assertQuietRunner(t, r)
}

// Stop cancels the current attempt and no ticker tick may start another one.
func TestLifecycleTaskCancelStopsScheduling(t *testing.T) {
	runWithInterval(t, 20*time.Millisecond)
	db := newRunnerTestDB(t)
	taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
	entered := make(chan struct{}, 4)
	analyzer := &fakeAnalyzer{fn: func(ctx context.Context, _ AnalyzeInput) ([]Result, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	r := NewRunnerWithOptions(db, nil, RunnerOptions{TaskTimeout: 5 * time.Second})
	r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
	defer r.Close()
	if err := r.Start(taskID); err != nil {
		t.Fatalf("start: %v", err)
	}
	<-entered
	r.Stop(taskID)
	calls := analyzer.callCount()
	time.Sleep(150 * time.Millisecond)
	if got := analyzer.callCount(); got != calls {
		t.Fatalf("task kept executing after Stop: %d -> %d", calls, got)
	}
	runs := runsFor(t, db, taskID)
	if len(runs) == 0 || runs[0].Status != RunStopped {
		t.Fatalf("run after Stop = %+v", runs)
	}
}

// A stuck provider is bounded by TaskTimeout; the run is terminal and no retry
// starts a new execution.
func TestLifecycleTaskTimeoutStopsExecution(t *testing.T) {
	runWithInterval(t, time.Hour)
	db := newRunnerTestDB(t)
	taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
	analyzer := &fakeAnalyzer{fn: func(ctx context.Context, _ AnalyzeInput) ([]Result, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	r := NewRunnerWithOptions(db, nil, RunnerOptions{
		TaskTimeout: 60 * time.Millisecond, MaxRetries: 2, BackoffBase: time.Hour,
	})
	r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
	defer r.Close()
	if err := r.Start(taskID); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool {
		runs := runsFor(t, db, taskID)
		return len(runs) > 0 && runs[0].FinishedAt != nil
	})
	run := runsFor(t, db, taskID)[0]
	if run.Status != RunTimeout || run.ErrorCode != string(ErrorProviderTimeout) {
		t.Fatalf("timed out run = %+v", run)
	}
	time.Sleep(80 * time.Millisecond)
	if got := analyzer.callCount(); got != 1 {
		t.Fatalf("timeout triggered %d executions, want 1", got)
	}
}

// Recovery racing with Close: neither side leaks goroutines, and no execution
// may start after shutdown completes.
func TestLifecycleCloseDuringRecover(t *testing.T) {
	runWithInterval(t, time.Hour)
	db := newRunnerTestDB(t)
	entered := make(chan struct{}, 64)
	analyzer := &fakeAnalyzer{fn: func(ctx context.Context, _ AnalyzeInput) ([]Result, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	r := NewRunnerWithOptions(db, nil, RunnerOptions{TaskTimeout: 5 * time.Second})
	r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })

	const n = 12
	for i := 0; i < n; i++ {
		id, _ := seedRunnerTask(t, db, `{"intervalSec":0,"autoRecover":true}`)
		db.Model(&model.AITask{}).Where("id = ?", id).Update("status", "running")
		db.Create(&model.AITaskRun{TaskID: id, Status: RunRunning, WorkerID: "old-worker"})
	}

	recoverDone, closeDone := make(chan error, 1), make(chan struct{})
	go func() { recoverDone <- r.Recover() }()
	go func() { r.Close(); close(closeDone) }()
	select {
	case <-closeDone:
	case <-time.After(3 * time.Second):
		t.Fatal("Close blocked during Recover")
	}
	select {
	case err := <-recoverDone:
		if err != nil && !errors.Is(err, ErrRunnerClosed) {
			t.Fatalf("Recover during shutdown: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Recover blocked during Close")
	}
	calls := analyzer.callCount()
	time.Sleep(120 * time.Millisecond)
	if got := analyzer.callCount(); got != calls {
		t.Fatalf("execution started after Close: %d -> %d", calls, got)
	}
	assertQuietRunner(t, r)
	assertClosedRejectsWork(t, r)
}

// Concurrent Start/Recover for the same tasks must yield exactly one loop and
// one run per task, even when repeated recovery is attempted.
func TestLifecycleConcurrentStartRecoverSingleInstance(t *testing.T) {
	runWithInterval(t, time.Hour)
	db := newRunnerTestDB(t)
	analyzer := &fakeAnalyzer{}
	r := NewRunnerWithOptions(db, nil, RunnerOptions{TaskTimeout: time.Second})
	r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
	defer r.Close()

	const n = 8
	ids := make([]uint, 0, n)
	for i := 0; i < n; i++ {
		id, _ := seedRunnerTask(t, db, `{"intervalSec":0,"autoRecover":true}`)
		db.Model(&model.AITask{}).Where("id = ?", id).Update("status", "running")
		ids = append(ids, id)
	}

	var wg sync.WaitGroup
	errCh := make(chan error, n*3+4) // 3 calls per task + 3 full recover scans
	for _, id := range ids {
		for _, fn := range []func() error{
			func() error { return r.Start(id) },
			func() error { return r.RecoverTask(id) },
			func() error { return r.RecoverTask(id) },
		} {
			wg.Add(1)
			go func(fn func() error) { defer wg.Done(); errCh <- fn() }(fn)
		}
	}
	for i := 0; i < 3; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); errCh <- r.Recover() }()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent start/recover: %v", err)
		}
	}
	waitFor(t, 3*time.Second, func() bool { return analyzer.callCount() == n })
	time.Sleep(150 * time.Millisecond)
	if got := analyzer.callCount(); got != n {
		t.Fatalf("analyzer called %d times, want %d", got, n)
	}
	for _, id := range ids {
		if runs := runsFor(t, db, id); len(runs) != 1 {
			t.Fatalf("task_id=%d has %d runs, want 1", id, len(runs))
		}
	}
	r.mu.Lock()
	live := len(r.tasks)
	r.mu.Unlock()
	if live != n {
		t.Fatalf("live loops = %d, want %d", live, n)
	}
}

// Repeated start/close cycles on fresh runners must not accumulate goroutines
// or leave semaphores/maps behind.
func TestLifecycleRepeatedStartCloseCycles(t *testing.T) {
	runWithInterval(t, time.Hour)
	for i := 0; i < 6; i++ {
		db := newRunnerTestDB(t)
		taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
		analyzer := &fakeAnalyzer{}
		r := NewRunnerWithOptions(db, nil, RunnerOptions{TaskTimeout: time.Second})
		r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
		if err := r.Start(taskID); err != nil {
			t.Fatalf("cycle %d start: %v", i, err)
		}
		waitFor(t, 2*time.Second, func() bool { return analyzer.callCount() == 1 })
		waitClose(t, 2*time.Second, r.Close)
		assertQuietRunner(t, r)
	}
}

// With the backing SQL database already closed, entry points return errors
// quickly and never launch an execution; shutdown remains safe and idempotent.
func TestLifecycleDBClosedAfterRunnerCreated(t *testing.T) {
	runWithInterval(t, time.Hour)
	db := newRunnerTestDB(t)
	taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
	r := NewRunnerWithOptions(db, nil, RunnerOptions{TaskTimeout: time.Second})
	analyzer := &fakeAnalyzer{}
	r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })

	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("sql db: %v", err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatalf("close sql db: %v", err)
	}

	begin := time.Now()
	if err := r.Start(taskID); err == nil {
		t.Fatal("Start succeeded with a closed database")
	}
	if err := r.Recover(); err == nil {
		t.Fatal("Recover succeeded with a closed database")
	}
	if err := r.RecoverTask(taskID); err == nil {
		t.Fatal("RecoverTask succeeded with a closed database")
	}
	if _, err := r.RunOnce(context.Background(), model.AITask{TaskType: "cv_detect"}); err == nil {
		t.Fatal("RunOnce succeeded with a closed database")
	}
	if elapsed := time.Since(begin); elapsed > 2*time.Second {
		t.Fatalf("closed-DB calls blocked for %v", elapsed)
	}
	if analyzer.callCount() != 0 {
		t.Fatal("execution launched with a closed database")
	}
	assertQuietRunner(t, r)
	waitClose(t, 2*time.Second, r.Close)
}

// Closing with a live attempt whose DB has just been closed must still return;
// failed finalizer writes are surfaced as counters, not panics or hangs.
// A provider that ignores cancellation cannot block Close forever: shutdown
// is bounded by ShutdownTimeout and finalizer writes stop once Close returns.
func TestLifecycleShutdownTimeoutAbandonsStuckAttempt(t *testing.T) {
	runWithInterval(t, time.Hour)
	db := newRunnerTestDB(t)
	taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
	entered := make(chan struct{})
	release := make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	analyzer := &fakeAnalyzer{fn: func(context.Context, AnalyzeInput) ([]Result, error) {
		close(entered)
		<-release // deliberately ignores cancellation
		return nil, nil
	}}
	r := NewRunnerWithOptions(db, nil, RunnerOptions{
		TaskTimeout: 5 * time.Second, ShutdownTimeout: 60 * time.Millisecond,
	})
	r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
	defer unblock()
	if err := r.Start(taskID); err != nil {
		t.Fatalf("start: %v", err)
	}
	<-entered
	begin := time.Now()
	waitClose(t, 2*time.Second, r.Close)
	if elapsed := time.Since(begin); elapsed > time.Second {
		t.Fatalf("Close ignored its timeout: %v", elapsed)
	}
	runCount := len(runsFor(t, db, taskID))
	unblock()
	time.Sleep(120 * time.Millisecond)
	if got := len(runsFor(t, db, taskID)); got != runCount {
		t.Fatalf("DB write after Close returned: runs %d -> %d", runCount, got)
	}
}

func TestLifecycleDBClosedWhileTaskRunning(t *testing.T) {
	runWithInterval(t, time.Hour)
	db := newRunnerTestDB(t)
	taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
	entered := make(chan struct{})
	analyzer := &fakeAnalyzer{fn: func(ctx context.Context, _ AnalyzeInput) ([]Result, error) {
		close(entered)
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	r := NewRunnerWithOptions(db, nil, RunnerOptions{TaskTimeout: 5 * time.Second})
	r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
	if err := r.Start(taskID); err != nil {
		t.Fatalf("start: %v", err)
	}
	<-entered
	sqlDB, _ := db.DB()
	_ = sqlDB.Close()
	waitClose(t, 3*time.Second, r.Close)
	assertQuietRunner(t, r)
	if r.RunUpdateFailures() == 0 {
		t.Fatal("failed finalizer writes were not surfaced")
	}
}

// Split shutdown signals: Done fires as soon as shutdown starts, while a
// concurrent Close must wait until cleanup has actually completed.
func TestLifecycleConcurrentCloseWaitsForCompletion(t *testing.T) {
	runWithInterval(t, time.Hour)
	db := newRunnerTestDB(t)
	taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	analyzer := &fakeAnalyzer{fn: func(ctx context.Context, _ AnalyzeInput) ([]Result, error) {
		select {
		case entered <- struct{}{}:
		default:
		}
		<-release
		return nil, ctx.Err()
	}}
	r := NewRunnerWithOptions(db, nil, RunnerOptions{TaskTimeout: 5 * time.Second, ShutdownTimeout: 2 * time.Second})
	r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
	if err := r.Start(taskID); err != nil {
		t.Fatalf("start: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("analyzer never started")
	}

	firstDone := make(chan struct{})
	go func() { r.Close(); close(firstDone) }()
	select {
	case <-r.Done():
	case <-time.After(time.Second):
		t.Fatal("Done was not closed when shutdown started")
	}
	secondDone := make(chan struct{})
	go func() { r.Close(); close(secondDone) }()
	select {
	case <-secondDone:
		t.Fatal("second Close returned before cleanup completed")
	case <-time.After(120 * time.Millisecond):
	}
	select {
	case <-firstDone:
		t.Fatal("first Close returned before cleanup completed")
	default:
	}
	close(release)
	waitClose(t, 3*time.Second, func() {
		<-firstDone
		<-secondDone
	})
	assertQuietRunner(t, r)
}

// A Start interrupted between persisting "running" and launching its loop
// must roll that status back conditionally, leaving no false running task.
func TestLifecycleStartCloseDBStateConsistency(t *testing.T) {
	runWithInterval(t, time.Hour)
	db := newRunnerTestDB(t)
	taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
	updated := make(chan struct{})
	release := make(chan struct{})
	r := NewRunnerWithOptions(db, nil, RunnerOptions{TaskTimeout: time.Second, ShutdownTimeout: 2 * time.Second})
	r.startDBUpdatedHook = func() {
		select {
		case <-updated:
		default:
			close(updated)
		}
		<-release
	}
	r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) {
		return &fakeAnalyzer{fn: func(context.Context, AnalyzeInput) ([]Result, error) {
			t.Error("analysis must not run for a Start interrupted by Close")
			return nil, nil
		}}, nil
	})

	startErr := make(chan error, 1)
	go func() { startErr <- r.Start(taskID) }()
	select {
	case <-updated:
	case <-time.After(2 * time.Second):
		t.Fatal("Start never persisted the running status")
	}
	closeDone := make(chan struct{})
	go func() { r.Close(); close(closeDone) }()
	select {
	case <-r.Done():
	case <-time.After(time.Second):
		t.Fatal("Close did not enter shutdown")
	}
	close(release)
	if err := <-startErr; !errors.Is(err, ErrRunnerClosed) {
		t.Fatalf("interrupted Start error = %v, want ErrRunnerClosed", err)
	}
	<-closeDone

	var task model.AITask
	if err := db.First(&task, taskID).Error; err != nil {
		t.Fatalf("reload task: %v", err)
	}
	if task.Status == "running" {
		t.Fatal("interrupted Start left a false running status in the database")
	}
	if task.Status != "stopped" {
		t.Fatalf("task status = %q, want stopped", task.Status)
	}
	if !task.Enabled {
		t.Fatal("interrupted Start lost the task's previous enabled flag")
	}
	if runs := runsFor(t, db, taskID); len(runs) != 0 {
		t.Fatalf("interrupted Start created %d run records", len(runs))
	}
	assertQuietRunner(t, r)
}

// Concurrent Start/Close stress: no false running status, no duplicate runs,
// no leftover state or goroutines across 100 rounds.
func TestLifecycleConcurrentStartCloseStress(t *testing.T) {
	runWithInterval(t, time.Hour)
	db := newRunnerTestDB(t)
	for i := 0; i < 100; i++ {
		taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
		r := NewRunnerWithOptions(db, nil, RunnerOptions{TaskTimeout: time.Second, ShutdownTimeout: 2 * time.Second})
		r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) {
			return &fakeAnalyzer{fn: func(context.Context, AnalyzeInput) ([]Result, error) {
				return []Result{{EventType: "person"}}, nil
			}}, nil
		})
		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); _ = r.Start(taskID) }()
		go func() { defer wg.Done(); r.Close() }()
		wg.Wait()
		assertQuietRunner(t, r)
		var task model.AITask
		if err := db.First(&task, taskID).Error; err != nil {
			t.Fatalf("round %d reload task: %v", i, err)
		}
		if task.Status == "running" {
			t.Fatalf("round %d left a false running status", i)
		}
		if runs := runsFor(t, db, taskID); len(runs) > 1 {
			t.Fatalf("round %d created %d run records", i, len(runs))
		}
	}
}

// Three-way Start/Stop/Close stress: across 100 independent rounds the task
// row must agree with the real execution state, no round may launch a second
// loop or run, and shutdown must leave no tracked work or goroutines behind.
func TestLifecycleConcurrentStartStopCloseStress(t *testing.T) {
	runWithInterval(t, time.Hour)
	const rounds = 100
	const (
		settleWindow = 20 * time.Millisecond
		pollStep     = 2 * time.Millisecond
		goroutineTol = 32
	)

	baseline := runtime.NumGoroutine()
	for round := 0; round < rounds; round++ {
		t.Run(fmt.Sprintf("round-%03d", round), func(t *testing.T) {
			db := newRunnerTestDB(t)
			sqlDB, err := db.DB()
			if err != nil {
				t.Fatalf("sql db: %v", err)
			}
			// Cleanups run LIFO: the runner closes first, then the SQL pool so
			// its connectionOpener goroutine cannot skew the leak sample.
			t.Cleanup(func() { _ = sqlDB.Close() })

			taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0,"grabFrame":false}`)
			entered := make(chan struct{}, 1)
			release := make(chan struct{})
			var releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			// Hold the attempt open until the three-way call has been issued,
			// then let cancellation drive it to a terminal state.
			analyzer := &fakeAnalyzer{fn: func(ctx context.Context, _ AnalyzeInput) ([]Result, error) {
				select {
				case entered <- struct{}{}:
				default:
				}
				<-release
				<-ctx.Done()
				return nil, ctx.Err()
			}}
			r := NewRunnerWithOptions(db, nil, RunnerOptions{
				TaskTimeout: 5 * time.Second, ShutdownTimeout: 5 * time.Second, MaxRetries: 0,
			})
			r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
			t.Cleanup(func() { unblock(); r.Close() }) // idempotent safety net on failure

			// Establish a live loop and an in-flight run first; otherwise a
			// shutdown winning the initial race would leave nothing to check.
			if err := r.Start(taskID); err != nil {
				t.Fatalf("initial start: %v", err)
			}
			select {
			case <-entered:
			case <-time.After(2 * time.Second):
				unblock()
				t.Fatal("initial analyzer never entered")
			}

			// Fixed interleaving: duplicate Start, Stop and Close are released
			// together while that attempt is in flight. The analyzer stays
			// parked until duplicate Start reports, so Stop cannot retire the
			// loop before Start's idempotency check runs.
			ready := make(chan struct{})
			var armed, done sync.WaitGroup
			armed.Add(3)
			done.Add(3)
			errCh := make(chan error, 1)
			go func() { defer done.Done(); armed.Done(); <-ready; errCh <- r.Start(taskID) }()
			go func() { defer done.Done(); armed.Done(); <-ready; r.Stop(taskID) }()
			go func() { defer done.Done(); armed.Done(); <-ready; r.Close() }()
			armed.Wait()
			close(ready)
			select {
			case err := <-errCh:
				if err != nil && !errors.Is(err, ErrRunnerClosed) {
					unblock()
					t.Fatalf("concurrent Start: %v", err)
				}
			case <-time.After(2 * time.Second):
				unblock()
				t.Fatal("concurrent Start did not return")
			}
			unblock()
			done.Wait()

			assertQuietRunner(t, r)
			if !r.IsClosed() {
				t.Fatal("runner is not closed after the three-way call")
			}

			// A short bounded observation window catches work that tries to
			// start or write after every caller has returned.
			calls := analyzer.callCount()
			runs := runsFor(t, db, taskID)
			deadline := time.Now().Add(settleWindow)
			for time.Now().Before(deadline) {
				time.Sleep(pollStep)
			}
			if got := analyzer.callCount(); got != calls {
				t.Fatalf("execution started after Close: %d -> %d", calls, got)
			}
			after := runsFor(t, db, taskID)
			if len(after) != len(runs) {
				t.Fatalf("DB write after Close: runs %d -> %d", len(runs), len(after))
			}
			if len(after) > 1 {
				t.Fatalf("round produced %d runs for one task, want at most 1", len(after))
			}
			if calls > 1 {
				t.Fatalf("round produced %d analyzer calls, want at most 1", calls)
			}
			for _, run := range after {
				switch run.Status {
				case RunStopped, RunSucceeded, RunFailed, RunTimeout:
				default:
					t.Fatalf("run %d has non-terminal status %q", run.ID, run.Status)
				}
				if run.FinishedAt == nil {
					t.Fatalf("run %d has no finished_at (status=%q)", run.ID, run.Status)
				}
			}

			var task model.AITask
			if err := db.First(&task, taskID).Error; err != nil {
				t.Fatalf("reload task: %v", err)
			}
			if !task.Enabled {
				t.Fatal("task lost its enabled flag after the three-way call")
			}
			if task.Status == "running" {
				t.Fatal("closed runner left a task in status=running with no live loop")
			}
		})
	}

	// Relative goroutine sample: every per-round runner and database is gone,
	// so the count must settle close to the pre-stress baseline.
	deadline := time.Now().Add(2 * time.Second)
	for {
		got := runtime.NumGoroutine()
		if got <= baseline+goroutineTol {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("goroutine leak after %d rounds: baseline=%d current=%d tolerance=%d",
				rounds, baseline, got, goroutineTol)
		}
		time.Sleep(5 * time.Millisecond)
	}
}
