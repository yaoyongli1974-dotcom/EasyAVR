package ai

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/model"
)

// failAITaskUpdates makes every AITask UPDATE on this DB fail until the
// returned function is called. Start must treat that as a fatal launch error.
func failAITaskUpdates(db *gorm.DB, name string) func() {
	db.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
		// Model(...).Updates(map) stores the map in Dest, not the model.
		if tx.Statement.Table == "ai_tasks" {
			tx.AddError(errors.New("task store unavailable"))
		}
	})
	return func() { _ = db.Callback().Update().Remove(name) }
}

// failRunUpdates makes every AITaskRun UPDATE on this DB fail.
func failRunUpdates(db *gorm.DB, name string) func() {
	db.Callback().Update().Before("gorm:update").Register(name, func(tx *gorm.DB) {
		if tx.Statement.Table == "ai_task_runs" {
			tx.AddError(errors.New("run store unavailable"))
		}
	})
	return func() { _ = db.Callback().Update().Remove(name) }
}

// failRunCreates makes every AITaskRun INSERT on this DB fail.
func failRunCreates(db *gorm.DB, name string) func() {
	db.Callback().Create().Before("gorm:create").Register(name, func(tx *gorm.DB) {
		if _, ok := tx.Statement.Dest.(*model.AITaskRun); ok {
			tx.AddError(errors.New("run store unavailable"))
		}
	})
	return func() { _ = db.Callback().Create().Remove(name) }
}

func taskStatus(t *testing.T, db *gorm.DB, taskID uint) model.AITask {
	t.Helper()
	var task model.AITask
	if err := db.First(&task, taskID).Error; err != nil {
		t.Fatalf("load task: %v", err)
	}
	return task
}

// Start: happy path, duplicate start, missing task, and no residue on error.
func TestStartLifecycleAndMissingTask(t *testing.T) {
	runWithInterval(t, time.Hour)
	db := newRunnerTestDB(t)
	taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
	analyzer := &fakeAnalyzer{}
	r := NewRunnerWithOptions(db, nil, RunnerOptions{TaskTimeout: time.Second})
	r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
	defer r.Close()

	if err := r.Start(taskID); err != nil {
		t.Fatalf("start: %v", err)
	}
	if len(r.tasks) != 1 {
		t.Fatalf("live tasks after start = %d, want 1", len(r.tasks))
	}
	// Duplicate start is a no-op and must not create a second loop.
	if err := r.Start(taskID); err != nil {
		t.Fatalf("duplicate start: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return analyzer.callCount() == 1 })
	time.Sleep(60 * time.Millisecond)
	if got := analyzer.callCount(); got != 1 {
		t.Fatalf("duplicate start launched %d executions, want 1", got)
	}
	if len(r.tasks) != 1 {
		t.Fatalf("live tasks after duplicate start = %d, want 1", len(r.tasks))
	}

	const missing = uint(999999)
	err := r.Start(missing)
	if err == nil || !errors.Is(err, gorm.ErrRecordNotFound) || !strings.Contains(err.Error(), fmt.Sprint(missing)) {
		t.Fatalf("missing task error = %v, want explicit record-not-found error with task id", err)
	}
	if len(r.tasks) != 1 || r.tasks[missing] != nil {
		t.Fatalf("missing task left state behind: %+v", r.tasks)
	}
}

// Start: a failed status update aborts the launch, releases the reserved state
// and lets a later Start succeed once the store is healthy again.
func TestStartStatusUpdateFailureAbortsAndReleases(t *testing.T) {
	runWithInterval(t, time.Hour)
	db := newRunnerTestDB(t)
	taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
	analyzer := &fakeAnalyzer{}
	r := NewRunnerWithOptions(db, nil, RunnerOptions{TaskTimeout: time.Second})
	r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
	defer r.Close()

	undo := failAITaskUpdates(db, "test_start_update_failure")
	// Two concurrent starters must both observe the failure instead of one
	// caller returning nil for a loop that never launched.
	var wg sync.WaitGroup
	errs := make([]error, 2)
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = r.Start(taskID)
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err == nil || !strings.Contains(err.Error(), "status update failed") {
			t.Fatalf("start[%d] error = %v, want explicit status update failure", i, err)
		}
	}
	if len(r.tasks) != 0 {
		t.Fatalf("failed start left %d live states, want 0", len(r.tasks))
	}
	time.Sleep(60 * time.Millisecond)
	if got := analyzer.callCount(); got != 0 {
		t.Fatalf("failed start executed analyzer %d times, want 0", got)
	}
	undo()

	if err := r.Start(taskID); err != nil {
		t.Fatalf("restart after failure: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return analyzer.callCount() > 0 })
	if len(r.tasks) != 1 {
		t.Fatalf("live tasks after restart = %d, want 1", len(r.tasks))
	}
}

// Start: concurrent Starts of one task keep exactly one live instance.
func TestStartConcurrentSameTaskSingleInstance(t *testing.T) {
	runWithInterval(t, time.Hour)
	db := newRunnerTestDB(t)
	taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
	analyzer := &fakeAnalyzer{}
	r := NewRunnerWithOptions(db, nil, RunnerOptions{TaskTimeout: time.Second})
	r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
	defer r.Close()

	const starters = 16
	var wg sync.WaitGroup
	errs := make([]error, starters)
	start := make(chan struct{})
	for i := range errs {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			errs[i] = r.Start(taskID)
		}(i)
	}
	close(start)
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Fatalf("concurrent start[%d]: %v", i, err)
		}
	}
	waitFor(t, 2*time.Second, func() bool { return analyzer.callCount() == 1 })
	time.Sleep(120 * time.Millisecond)
	if got := analyzer.callCount(); got != 1 {
		t.Fatalf("concurrent starts produced %d executions, want 1", got)
	}
	if len(r.tasks) != 1 {
		t.Fatalf("concurrent starts produced %d live states, want 1", len(r.tasks))
	}
}

// Start: exceeding global/per-channel limits must not block callers; the extra
// runs stay pending until a slot is released.
func TestStartBeyondConcurrencyLimitsDoesNotBlock(t *testing.T) {
	runWithInterval(t, time.Hour)
	db := newRunnerTestDB(t)
	release := make(chan struct{})
	started := make(chan struct{}, 3)
	analyzer := &fakeAnalyzer{fn: func(ctx context.Context, _ AnalyzeInput) ([]Result, error) {
		started <- struct{}{}
		select {
		case <-release:
		case <-ctx.Done():
		}
		return nil, nil
	}}
	r := NewRunnerWithOptions(db, nil, RunnerOptions{
		MaxConcurrency: 1, PerChannelLimit: 1, TaskTimeout: 10 * time.Second,
	})
	r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
	defer r.Close()

	ids := make([]uint, 3)
	for i := range ids {
		ids[i], _ = seedRunnerTask(t, db, `{"intervalSec":0}`)
	}
	for i, id := range ids {
		begin := time.Now()
		if err := r.Start(id); err != nil {
			t.Fatalf("start[%d]: %v", i, err)
		}
		if elapsed := time.Since(begin); elapsed > time.Second {
			t.Fatalf("start[%d] blocked for %v", i, elapsed)
		}
	}
	<-started // only the first task can hold the single slot
	waitFor(t, 2*time.Second, func() bool {
		for _, id := range ids {
			if len(runsFor(t, db, id)) == 0 {
				return false
			}
		}
		return true
	})
	// Which task wins the single slot is scheduler-dependent; what matters is
	// that at most one run is executing and the rest stay pending.
	running, pending := 0, 0
	for _, id := range ids {
		switch run := runsFor(t, db, id)[0]; run.Status {
		case RunRunning:
			running++
		case RunPending, RunStarting:
			pending++
		}
	}
	if running != 1 || pending != len(ids)-1 {
		t.Fatalf("slot accounting: running=%d pending=%d, want 1/%d", running, pending, len(ids)-1)
	}
	close(release)
	waitFor(t, 3*time.Second, func() bool {
		for _, id := range ids {
			runs := runsFor(t, db, id)
			if len(runs) == 0 || runs[0].FinishedAt == nil {
				return false
			}
		}
		return true
	})
}

// Run records: a failed INSERT must stop the execution before any analyzer call.
func TestRunCreateFailureSkipsAnalysis(t *testing.T) {
	runWithInterval(t, time.Hour)
	db := newRunnerTestDB(t)
	taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
	analyzer := &fakeAnalyzer{}
	r := NewRunnerWithOptions(db, nil, RunnerOptions{TaskTimeout: time.Second})
	r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
	defer r.Close()

	undo := failRunCreates(db, "test_run_create_failure")
	defer undo()
	if err := r.Start(taskID); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return taskStatus(t, db, taskID).Status == "failed" })
	time.Sleep(60 * time.Millisecond)
	if got := analyzer.callCount(); got != 0 {
		t.Fatalf("analysis ran %d times without a run record, want 0", got)
	}
	if r.RunCreateFailures() == 0 {
		t.Fatal("run create failure was not reported")
	}
	if runs := runsFor(t, db, taskID); len(runs) != 0 {
		t.Fatalf("unexpected run rows: %+v", runs)
	}
	task := taskStatus(t, db, taskID)
	if task.LastErrorCode != string(ErrorInternal) {
		t.Fatalf("task error code = %q, want internal_error", task.LastErrorCode)
	}
}

// Run records: success, classified failure and timeout all reach a terminal
// run status with an explicit error code where applicable.
func TestRunTerminalStatuses(t *testing.T) {
	runWithInterval(t, time.Hour)

	t.Run("succeeded", func(t *testing.T) {
		db := newRunnerTestDB(t)
		taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
		analyzer := &fakeAnalyzer{fn: func(context.Context, AnalyzeInput) ([]Result, error) {
			return []Result{{EventType: "person", Summary: "ok"}}, nil
		}}
		r := NewRunnerWithOptions(db, nil, RunnerOptions{TaskTimeout: time.Second})
		r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
		defer r.Close()
		if err := r.Start(taskID); err != nil {
			t.Fatalf("start: %v", err)
		}
		waitFor(t, 2*time.Second, func() bool {
			runs := runsFor(t, db, taskID)
			return len(runs) > 0 && runs[0].Status == RunSucceeded
		})
		run := runsFor(t, db, taskID)[0]
		if run.FinishedAt == nil {
			t.Fatalf("succeeded run has no finish time: %+v", run)
		}
	})

	t.Run("failed with classification", func(t *testing.T) {
		db := newRunnerTestDB(t)
		taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
		analyzer := &fakeAnalyzer{fn: func(context.Context, AnalyzeInput) ([]Result, error) {
			return nil, errors.New("invalid json from worker")
		}}
		r := NewRunnerWithOptions(db, nil, RunnerOptions{TaskTimeout: time.Second})
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
		if run.Status != RunFailed || run.ErrorCode != string(ErrorInferenceFailed) {
			t.Fatalf("run = %+v, want failed/inference_failed", run)
		}
	})

	t.Run("timeout", func(t *testing.T) {
		db := newRunnerTestDB(t)
		taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
		analyzer := &fakeAnalyzer{fn: func(ctx context.Context, _ AnalyzeInput) ([]Result, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		}}
		r := NewRunnerWithOptions(db, nil, RunnerOptions{TaskTimeout: 70 * time.Millisecond, MaxRetries: 0})
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
			t.Fatalf("run = %+v, want timeout/provider_timeout", run)
		}
	})
}

// Run records: a failed status transition is counted and alerted, and the task
// is marked failed instead of looking healthy with a stuck run row.
func TestRunUpdateFailureIsSurfaced(t *testing.T) {
	runWithInterval(t, time.Hour)
	db := newRunnerTestDB(t)
	taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
	analyzer := &fakeAnalyzer{fn: func(context.Context, AnalyzeInput) ([]Result, error) {
		return []Result{{EventType: "person"}}, nil
	}}
	r := NewRunnerWithOptions(db, nil, RunnerOptions{TaskTimeout: time.Second})
	r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
	defer r.Close()

	undo := failRunUpdates(db, "test_run_update_failure")
	defer undo()
	if err := r.Start(taskID); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitFor(t, 2*time.Second, func() bool { return taskStatus(t, db, taskID).Status == "failed" })
	if r.RunUpdateFailures() == 0 {
		t.Fatal("failed run updates were not surfaced")
	}
	task := taskStatus(t, db, taskID)
	if task.LastErrorCode != string(ErrorInternal) {
		t.Fatalf("task error code = %q, want internal_error", task.LastErrorCode)
	}
}

// Run records: each retry is an independent run row with its own ID and
// classification; the earlier failed attempts are never rewritten.
func TestRetryCreatesIndependentRunRecords(t *testing.T) {
	runWithInterval(t, time.Hour)
	db := newRunnerTestDB(t)
	taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
	analyzer := &fakeAnalyzer{}
	analyzer.fn = func(context.Context, AnalyzeInput) ([]Result, error) {
		if analyzer.callCount() < 3 {
			return nil, errors.New("dial tcp 127.0.0.1:1: connect: connection refused")
		}
		return []Result{{EventType: "person", Summary: "ok"}}, nil
	}
	r := NewRunnerWithOptions(db, nil, RunnerOptions{
		MaxRetries: 2, BackoffBase: time.Millisecond, TaskTimeout: 2 * time.Second,
	})
	r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
	defer r.Close()
	if err := r.Start(taskID); err != nil {
		t.Fatalf("start: %v", err)
	}
	waitFor(t, 3*time.Second, func() bool {
		runs := runsFor(t, db, taskID)
		return len(runs) == 3 && runs[2].Status == RunSucceeded
	})
	runs := runsFor(t, db, taskID)
	wantStatus := []string{RunFailed, RunFailed, RunSucceeded}
	wantRetry := []int{0, 1, 2}
	seen := map[uint]bool{}
	for i, run := range runs {
		if !seen[run.ID] {
			seen[run.ID] = true
		} else {
			t.Fatalf("run id %d reused on attempt %d", run.ID, i)
		}
		if len(seen) != i+1 {
			t.Fatalf("attempt %d reused a previous run id", i)
		}
		if run.Status != wantStatus[i] || run.RetryCount != wantRetry[i] || run.FinishedAt == nil {
			t.Fatalf("run[%d] = %+v, want status=%s retry=%d with finish time", i, run, wantStatus[i], wantRetry[i])
		}
	}
	if runs[0].ErrorCode != string(ErrorProviderUnavailable) || runs[1].ErrorCode != string(ErrorProviderUnavailable) {
		t.Fatalf("failed attempts are not classified: %q / %q", runs[0].ErrorCode, runs[1].ErrorCode)
	}
	var events int64
	db.Model(&model.AIEvent{}).Where("task_id = ?", taskID).Count(&events)
	if events != 1 {
		t.Fatalf("events = %d, want 1 (no replay of earlier attempts)", events)
	}
}

// Recovery: historical successful runs are left untouched, non-terminal runs
// are closed out, and only the autoRecover task is started on a fresh runner.
func TestRecoverDoesNotReplaySucceededRuns(t *testing.T) {
	runWithInterval(t, time.Hour)
	db := newRunnerTestDB(t)
	analyzer := &fakeAnalyzer{}
	taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0,"autoRecover":true}`)
	finished := time.Now().Add(-time.Hour)
	oldSucceeded := model.AITaskRun{
		TaskID: taskID, Status: RunSucceeded, WorkerID: "old-worker",
		FinishedAt: &finished, ErrorCode: "",
	}
	oldRunning := model.AITaskRun{TaskID: taskID, Status: RunRunning, WorkerID: "old-worker"}
	if err := db.Create(&oldSucceeded).Error; err != nil {
		t.Fatalf("seed succeeded run: %v", err)
	}
	if err := db.Create(&oldRunning).Error; err != nil {
		t.Fatalf("seed running run: %v", err)
	}
	db.Model(&model.AITask{}).Where("id = ?", taskID).Update("status", "running")

	r := NewRunnerWithOptions(db, nil, RunnerOptions{TaskTimeout: time.Second})
	r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
	r.Recover() // a fresh process: no in-memory state is carried over
	defer r.Close()

	var succeeded model.AITaskRun
	db.First(&succeeded, oldSucceeded.ID)
	if succeeded.Status != RunSucceeded || succeeded.WorkerID != "old-worker" ||
		succeeded.ErrorCode != "" || succeeded.FinishedAt == nil || !succeeded.FinishedAt.Equal(finished) {
		t.Fatalf("historical success was rewritten: %+v", succeeded)
	}
	var running model.AITaskRun
	db.First(&running, oldRunning.ID)
	if running.Status != RunFailed || running.ErrorCode != string(ErrorInternal) || running.FinishedAt == nil {
		t.Fatalf("lingering run not closed out: %+v", running)
	}
	waitFor(t, 3*time.Second, func() bool { return len(runsFor(t, db, taskID)) > 2 })
	newRuns := runsFor(t, db, taskID)
	if newRuns[2].WorkerID != r.workerID || newRuns[2].TaskID != taskID {
		t.Fatalf("new run after recovery = %+v", newRuns[2])
	}
	waitFor(t, 2*time.Second, func() bool { return analyzer.callCount() > 0 })
	time.Sleep(60 * time.Millisecond)
	if got := analyzer.callCount(); got != 1 {
		t.Fatalf("recovery replayed history: analyzer called %d times, want 1", got)
	}
}
