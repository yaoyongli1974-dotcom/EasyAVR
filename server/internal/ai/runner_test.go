package ai

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/easyavr/easyavr/internal/model"
)

func newRunnerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "runner.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.Channel{}, &model.AIProvider{}, &model.AITask{}, &model.AITaskRun{}, &model.AIEvent{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func seedRunnerTask(t *testing.T, db *gorm.DB, config string) (taskID, channelID uint) {
	t.Helper()
	ch := model.Channel{Name: "cam", SourceURL: "rtsp://test/source", StreamKey: "test-" + fmt.Sprint(time.Now().UnixNano())}
	if err := db.Create(&ch).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	return seedTaskOnChannel(t, db, ch.ID, config), ch.ID
}

func seedTaskOnChannel(t *testing.T, db *gorm.DB, channelID uint, config string) uint {
	t.Helper()
	p := model.AIProvider{Name: "cv", Kind: KindCV, Endpoint: "http://127.0.0.1:1/detect", Enabled: true}
	if err := db.Create(&p).Error; err != nil {
		t.Fatalf("create provider: %v", err)
	}
	task := model.AITask{
		Name: "task", ChannelID: channelID, ProviderID: p.ID, TaskType: "cv_detect",
		Config: config, Enabled: true, Status: "stopped",
	}
	if err := db.Create(&task).Error; err != nil {
		t.Fatalf("create task: %v", err)
	}
	return task.ID
}

type fakeAnalyzer struct {
	calls int32
	fn    func(context.Context, AnalyzeInput) ([]Result, error)
}

func (f *fakeAnalyzer) Kind() string { return KindCV }

func (f *fakeAnalyzer) Analyze(ctx context.Context, in AnalyzeInput) ([]Result, error) {
	atomic.AddInt32(&f.calls, 1)
	if f.fn != nil {
		return f.fn(ctx, in)
	}
	return nil, nil
}

func (f *fakeAnalyzer) callCount() int { return int(atomic.LoadInt32(&f.calls)) }

func runWithInterval(t *testing.T, interval time.Duration) {
	t.Helper()
	old := minTaskInterval
	minTaskInterval = interval
	t.Cleanup(func() { minTaskInterval = old })
}

func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}

func runsFor(t *testing.T, db *gorm.DB, taskID uint) []model.AITaskRun {
	t.Helper()
	var runs []model.AITaskRun
	if err := db.Where("task_id = ?", taskID).Order("id ASC").Find(&runs).Error; err != nil {
		t.Fatalf("list runs: %v", err)
	}
	return runs
}

// Start must not launch a second loop for the same task, and Stop must cancel
// the in-flight analyzer call and close out the run as stopped.
func TestRunnerStartStopAndNoDoubleInstance(t *testing.T) {
	runWithInterval(t, 20*time.Millisecond)
	db := newRunnerTestDB(t)
	taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0,"grabFrame":false}`)

	entered := make(chan struct{}, 4)
	analyzer := &fakeAnalyzer{fn: func(ctx context.Context, _ AnalyzeInput) ([]Result, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	r := NewRunnerWithOptions(db, nil, RunnerOptions{TaskTimeout: 5 * time.Second})
	r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })

	if err := r.Start(taskID); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := r.Start(taskID); err != nil {
		t.Fatalf("second start: %v", err)
	}
	select {
	case <-entered:
	case <-time.After(2 * time.Second):
		t.Fatal("analyzer was never called")
	}
	time.Sleep(120 * time.Millisecond) // several ticker periods
	if got := analyzer.callCount(); got != 1 {
		t.Fatalf("duplicate execution instances: analyzer called %d times", got)
	}

	start := time.Now()
	r.Stop(taskID)
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("Stop blocked for %v", elapsed)
	}
	callsAfterStop := analyzer.callCount()
	time.Sleep(80 * time.Millisecond)
	if got := analyzer.callCount(); got != callsAfterStop {
		t.Fatalf("task kept running after Stop: %d -> %d", callsAfterStop, got)
	}
	var task model.AITask
	db.First(&task, taskID)
	if task.Status != "stopped" {
		t.Fatalf("task status = %q, want stopped", task.Status)
	}
	runs := runsFor(t, db, taskID)
	if len(runs) == 0 {
		t.Fatal("no run recorded")
	}
	last := runs[len(runs)-1]
	if last.Status != RunStopped || last.FinishedAt == nil {
		t.Fatalf("last run = %+v, want stopped with finish time", last)
	}
	r.Close()
}

// An attempt exceeding TaskTimeout ends as failed/provider_timeout and the
// analyzer's context is canceled.
func TestRunnerTaskTimeout(t *testing.T) {
	runWithInterval(t, time.Hour)
	db := newRunnerTestDB(t)
	taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)

	analyzer := &fakeAnalyzer{fn: func(ctx context.Context, _ AnalyzeInput) ([]Result, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	r := NewRunnerWithOptions(db, nil, RunnerOptions{TaskTimeout: 90 * time.Millisecond})
	r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
	if err := r.Start(taskID); err != nil {
		t.Fatalf("start: %v", err)
	}
	defer r.Close()

	waitFor(t, 3*time.Second, func() bool {
		runs := runsFor(t, db, taskID)
		return len(runs) > 0 && runs[0].FinishedAt != nil
	})
	run := runsFor(t, db, taskID)[0]
	if run.Status != RunTimeout || run.ErrorCode != string(ErrorProviderTimeout) || run.FinishedAt == nil {
		t.Fatalf("run = %+v, want timeout/provider_timeout with a finish time", run)
	}
	var task model.AITask
	db.First(&task, taskID)
	if task.Status != "failed" {
		t.Fatalf("task status = %q, want failed", task.Status)
	}
}

// Global and per-channel limits gate concurrent executions without blocking
// the Runner lock (Start returns immediately for all tasks).
func TestRunnerConcurrencyLimits(t *testing.T) {
	runWithInterval(t, time.Hour)

	t.Run("global", func(t *testing.T) {
		db := newRunnerTestDB(t)
		var active, peak int32
		release := make(chan struct{})
		analyzer := &fakeAnalyzer{fn: func(ctx context.Context, _ AnalyzeInput) ([]Result, error) {
			n := atomic.AddInt32(&active, 1)
			for {
				old := atomic.LoadInt32(&peak)
				if n <= old || atomic.CompareAndSwapInt32(&peak, old, n) {
					break
				}
			}
			select {
			case <-release:
			case <-ctx.Done():
			}
			atomic.AddInt32(&active, -1)
			return nil, nil
		}}
		r := NewRunnerWithOptions(db, nil, RunnerOptions{
			MaxConcurrency: 2, PerChannelLimit: 2, TaskTimeout: 10 * time.Second,
		})
		r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
		defer r.Close()

		ids := make([]uint, 0, 4)
		for i := 0; i < 4; i++ {
			id, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
			ids = append(ids, id)
			if err := r.Start(id); err != nil {
				t.Fatalf("start: %v", err)
			}
		}
		waitFor(t, 3*time.Second, func() bool { return atomic.LoadInt32(&active) == 2 })
		time.Sleep(60 * time.Millisecond)
		if got := atomic.LoadInt32(&active); got > 2 {
			t.Fatalf("global concurrency = %d, want <= 2", got)
		}
		close(release)
		waitFor(t, 3*time.Second, func() bool {
			for _, id := range ids {
				runs := runsFor(t, db, id)
				if len(runs) == 0 || runs[0].Status != RunSucceeded {
					return false
				}
			}
			return true
		})
		if got := atomic.LoadInt32(&peak); got > 2 {
			t.Fatalf("observed peak concurrency %d, want <= 2", got)
		}
	})

	t.Run("per-channel", func(t *testing.T) {
		db := newRunnerTestDB(t)
		var active, peak int32
		release := make(chan struct{})
		analyzer := &fakeAnalyzer{fn: func(ctx context.Context, _ AnalyzeInput) ([]Result, error) {
			n := atomic.AddInt32(&active, 1)
			for {
				old := atomic.LoadInt32(&peak)
				if n <= old || atomic.CompareAndSwapInt32(&peak, old, n) {
					break
				}
			}
			select {
			case <-release:
			case <-ctx.Done():
			}
			atomic.AddInt32(&active, -1)
			return nil, nil
		}}
		r := NewRunnerWithOptions(db, nil, RunnerOptions{
			MaxConcurrency: 4, PerChannelLimit: 1, TaskTimeout: 10 * time.Second,
		})
		r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
		defer r.Close()

		_, channelID := seedRunnerTask(t, db, `{"intervalSec":0}`)
		for i := 0; i < 2; i++ {
			id := seedTaskOnChannel(t, db, channelID, `{"intervalSec":0}`)
			if err := r.Start(id); err != nil {
				t.Fatalf("start: %v", err)
			}
		}
		waitFor(t, 3*time.Second, func() bool { return atomic.LoadInt32(&active) == 1 })
		time.Sleep(60 * time.Millisecond)
		if got := atomic.LoadInt32(&peak); got > 1 {
			t.Fatalf("per-channel concurrency = %d, want <= 1", got)
		}
		close(release)
	})
}

// Transient provider errors are retried with exponential backoff; the run
// records the retry count. Non-retryable classifications are attempted once.
func TestRunnerRetryAndClassification(t *testing.T) {
	runWithInterval(t, time.Hour)

	t.Run("retry", func(t *testing.T) {
		db := newRunnerTestDB(t)
		taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
		analyzer := &fakeAnalyzer{}
		analyzer.fn = func(_ context.Context, _ AnalyzeInput) ([]Result, error) {
			if analyzer.callCount() < 3 {
				return nil, errors.New("dial tcp 127.0.0.1:1: connect: connection refused")
			}
			return []Result{{EventType: "person", Summary: "detected"}}, nil
		}
		r := NewRunnerWithOptions(db, nil, RunnerOptions{
			MaxConcurrency: 1, PerChannelLimit: 1, TaskTimeout: 5 * time.Second,
			MaxRetries: 2, BackoffBase: 10 * time.Millisecond,
		})
		r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
		defer r.Close()
		if err := r.Start(taskID); err != nil {
			t.Fatalf("start: %v", err)
		}
		waitFor(t, 3*time.Second, func() bool {
			runs := runsFor(t, db, taskID)
			return len(runs) > 0 && runs[len(runs)-1].Status == RunSucceeded
		})
		runs := runsFor(t, db, taskID)
		run := runs[len(runs)-1]
		if got := analyzer.callCount(); got != 3 {
			t.Fatalf("analyzer calls = %d, want 3 (1 + 2 retries)", got)
		}
		if run.RetryCount != 2 {
			t.Fatalf("retry count = %d, want 2", run.RetryCount)
		}
		var events int64
		db.Model(&model.AIEvent{}).Where("task_id = ?", taskID).Count(&events)
		if events != 1 {
			t.Fatalf("events = %d, want 1 (no duplicates from retries)", events)
		}
	})

	t.Run("non-retryable inference error", func(t *testing.T) {
		db := newRunnerTestDB(t)
		taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
		analyzer := &fakeAnalyzer{fn: func(context.Context, AnalyzeInput) ([]Result, error) {
			return nil, errors.New("cv worker returned invalid json: {...}")
		}}
		r := NewRunnerWithOptions(db, nil, RunnerOptions{
			TaskTimeout: 5 * time.Second, MaxRetries: 3, BackoffBase: time.Millisecond,
		})
		r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
		defer r.Close()
		if err := r.Start(taskID); err != nil {
			t.Fatalf("start: %v", err)
		}
		waitFor(t, 3*time.Second, func() bool {
			runs := runsFor(t, db, taskID)
			return len(runs) > 0 && runs[0].FinishedAt != nil
		})
		run := runsFor(t, db, taskID)[0]
		if run.ErrorCode != string(ErrorInferenceFailed) || analyzer.callCount() != 1 {
			t.Fatalf("run code=%q calls=%d, want inference_failed/1", run.ErrorCode, analyzer.callCount())
		}
	})

	t.Run("event db failure is internal", func(t *testing.T) {
		db := newRunnerTestDB(t)
		taskID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
		analyzer := &fakeAnalyzer{fn: func(context.Context, AnalyzeInput) ([]Result, error) {
			return []Result{{EventType: "person", Summary: "detected"}}, nil
		}}
		r := NewRunnerWithOptions(db, nil, RunnerOptions{
			TaskTimeout: 5 * time.Second, MaxRetries: 3, BackoffBase: time.Millisecond,
		})
		r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
		defer r.Close()

		const cb = "test_fail_event_create"
		db.Callback().Create().Before("gorm:create").Register(cb, func(tx *gorm.DB) {
			if _, ok := tx.Statement.Dest.(*model.AIEvent); ok {
				tx.AddError(errors.New("event store unavailable"))
			}
		})
		defer db.Callback().Create().Remove(cb)

		if err := r.Start(taskID); err != nil {
			t.Fatalf("start: %v", err)
		}
		waitFor(t, 3*time.Second, func() bool {
			runs := runsFor(t, db, taskID)
			return len(runs) > 0 && runs[0].FinishedAt != nil
		})
		run := runsFor(t, db, taskID)[0]
		if run.ErrorCode != string(ErrorInternal) || analyzer.callCount() != 1 {
			t.Fatalf("run code=%q calls=%d, want internal_error/1", run.ErrorCode, analyzer.callCount())
		}
	})
}

func TestErrorClassification(t *testing.T) {
	cases := []struct {
		name string
		err  error
		code ErrorCode
	}{
		{"deadline", context.DeadlineExceeded, ErrorProviderTimeout},
		{"canceled", context.Canceled, ErrorInternal},
		{"refused", errors.New("dial tcp: connect: connection refused"), ErrorProviderUnavailable},
		{"bad json", errors.New("cv worker returned invalid json"), ErrorInferenceFailed},
		{"unknown", errors.New("something odd"), ErrorInferenceFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := classifyProviderErr(context.Background(), tc.err)
			if got.code != tc.code {
				t.Fatalf("code = %s, want %s", got.code, tc.code)
			}
		})
	}
	if got := classifyFrameErr(context.Background(), errors.New("ffmpeg: no frames")).code; got != ErrorFrameCaptureFailed {
		t.Fatalf("frame code = %s, want frame_capture_failed", got)
	}
	if got := backoffDelay(2*time.Second, 2); got != 8*time.Second {
		t.Fatalf("backoff = %v, want 8s", got)
	}
	if got := backoffDelay(60*time.Second, 10); got != maxBackoff {
		t.Fatalf("backoff cap = %v, want %v", got, maxBackoff)
	}
}

// Recover only restarts autoRecover tasks and closes out every lingering run.
func TestRunnerRecover(t *testing.T) {
	runWithInterval(t, time.Hour)
	db := newRunnerTestDB(t)
	analyzer := &fakeAnalyzer{}
	mkRunner := func() *Runner {
		r := NewRunnerWithOptions(db, nil, RunnerOptions{TaskTimeout: 5 * time.Second})
		r.SetAnalyzerFactory(func(model.AIProvider) (Analyzer, error) { return analyzer, nil })
		return r
	}

	autoID, _ := seedRunnerTask(t, db, `{"intervalSec":0,"autoRecover":true}`)
	manualID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
	stoppedID, _ := seedRunnerTask(t, db, `{"intervalSec":0,"autoRecover":true}`)
	db.Model(&model.AITask{}).Where("id IN ?", []uint{autoID, manualID}).Update("status", "running")
	for _, id := range []uint{autoID, manualID} {
		run := model.AITaskRun{TaskID: id, Status: RunRunning, WorkerID: "old-worker"}
		db.Create(&run)
	}

	r := mkRunner()
	r.Recover()

	var lingering []model.AITaskRun
	db.Where("worker_id = ?", "old-worker").Find(&lingering)
	if len(lingering) != 2 {
		t.Fatalf("lingering runs = %d, want 2", len(lingering))
	}
	for _, run := range lingering {
		if run.Status != RunFailed || run.ErrorCode != string(ErrorInternal) || run.FinishedAt == nil {
			t.Fatalf("lingering run not closed out: %+v", run)
		}
	}
	var tasks []model.AITask
	db.Where("id IN ?", []uint{autoID, manualID, stoppedID}).Order("id ASC").Find(&tasks)
	statusByID := map[uint]string{}
	for _, task := range tasks {
		statusByID[task.ID] = task.Status
	}
	if statusByID[autoID] != "running" {
		t.Fatalf("autoRecover task status = %q, want running", statusByID[autoID])
	}
	if statusByID[manualID] != "stopped" {
		t.Fatalf("manual task status = %q, want stopped", statusByID[manualID])
	}
	if statusByID[stoppedID] != "stopped" {
		t.Fatalf("stopped task changed to %q", statusByID[stoppedID])
	}
	waitFor(t, 3*time.Second, func() bool { return len(runsFor(t, db, autoID)) > 1 })
	if len(runsFor(t, db, manualID)) != 1 || len(runsFor(t, db, stoppedID)) != 0 {
		t.Fatal("only the autoRecover task should be restarted")
	}
	r.Close()

	// A second Recover on a fresh runner must not resurrect the manual task.
	r2 := mkRunner()
	db.Model(&model.AITask{}).Where("id = ?", manualID).Update("status", "running")
	r2.Recover()
	var manual model.AITask
	db.First(&manual, manualID)
	if manual.Status != "stopped" {
		t.Fatalf("manual task status after second recover = %q", manual.Status)
	}
	r2.Close()
}

// A task waiting for a slot must not block Start or Stop while holding a lock.
func TestRunnerPendingStartsWhenSlotReleased(t *testing.T) {
	runWithInterval(t, time.Hour)
	db := newRunnerTestDB(t)
	release := make(chan struct{})
	started := make(chan struct{}, 2)
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

	firstID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
	secondID, _ := seedRunnerTask(t, db, `{"intervalSec":0}`)
	if err := r.Start(firstID); err != nil {
		t.Fatalf("start first: %v", err)
	}
	<-started // first task now holds the only global slot
	if err := r.Start(secondID); err != nil {
		t.Fatalf("start second: %v", err)
	}
	// The second task has a pending run but cannot execute until a slot frees.
	waitFor(t, 2*time.Second, func() bool { return len(runsFor(t, db, secondID)) > 0 })
	if run := runsFor(t, db, secondID)[0]; run.Status == RunRunning || run.Status == RunSucceeded {
		t.Fatalf("second task should be pending, got %s", run.Status)
	}
	close(release)
	select {
	case <-started:
	case <-time.After(3 * time.Second):
		t.Fatal("pending task never acquired a slot")
	}
	waitFor(t, 2*time.Second, func() bool {
		runs := runsFor(t, db, secondID)
		return len(runs) > 0 && runs[0].FinishedAt != nil
	})
}
