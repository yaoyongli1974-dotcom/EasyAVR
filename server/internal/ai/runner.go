package ai

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"

	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/media"
	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/video"
)

// TaskConfig is the decoded AITask.Config JSON.
type TaskConfig struct {
	IntervalSec int      `json:"intervalSec"`
	GrabFrame   bool     `json:"grabFrame"`
	Prompt      string   `json:"prompt"`
	Labels      []string `json:"labels"`
	ROI         []any    `json:"roi"`
	// AutoRecover is opt-in: only tasks that explicitly set autoRecover=true are
	// restarted by Runner.Recover after an unclean shutdown.
	AutoRecover bool `json:"autoRecover"`
}

// EventSink receives events produced by tasks (used to index and notify).
type EventSink interface {
	OnEvent(model.AIEvent)
}

// RunnerOptions bounds task execution so a busy provider cannot exhaust the
// host. Zero/negative values are replaced by the documented defaults.
type RunnerOptions struct {
	MaxConcurrency  int           // global parallel executions, default 4
	PerChannelLimit int           // parallel executions per channel, default 1
	TaskTimeout     time.Duration // hard timeout for one attempt, default 120s
	MaxRetries      int           // retries for transient provider/stream/frame errors, default 2
	BackoffBase     time.Duration // exponential backoff base, default 2s
	// ShutdownTimeout bounds how long Close waits for in-flight work after the
	// runner context has been canceled. Running attempts should react to the
	// cancellation immediately; the timeout only guards stuck providers.
	ShutdownTimeout time.Duration // default 10s
}

const (
	defaultMaxConcurrency  = 4
	defaultPerChannelLimit = 1
	defaultTaskTimeout     = 120 * time.Second
	defaultMaxRetries      = 2
	defaultBackoffBase     = 2 * time.Second
	defaultShutdownTimeout = 10 * time.Second
	maxBackoff             = 60 * time.Second
)

// ErrRunnerClosed is returned by Start/Recover/RecoverTask/RunOnce once the
// runner has been shut down. Callers can fail fast without touching the DB.
var ErrRunnerClosed = errors.New("ai runner is closed")

// run statuses persisted in AITaskRun.
const (
	RunPending   = "pending"
	RunStarting  = "starting"
	RunRunning   = "running"
	RunStopping  = "stopping"
	RunFailed    = "failed"
	RunStopped   = "stopped"
	RunSucceeded = "succeeded"
	// RunTimeout is a terminal run status: the attempt hit TaskTimeout (or the
	// provider reported a deadline) and produced no findings. Recovery must not
	// treat it as a leftover from a crashed process.
	RunTimeout = "timeout"
)

// nonTerminalRunStatuses are cleared by Recover (leftovers from a crash).
var nonTerminalRunStatuses = []string{RunPending, RunStarting, RunRunning, RunStopping}

// minTaskInterval is the shortest allowed ticker period. Tests in this package
// lower it so scheduling behaviour can be verified without slow sleeps.
var minTaskInterval = 5 * time.Second

// runnerState tracks one started task instance. done is closed when its loop
// has fully exited (and all concurrency slots have been released).
type runnerState struct {
	id     uint
	cancel context.CancelFunc
	done   chan struct{}
	// started is closed once the pre-launch DB work has succeeded or failed.
	// A concurrent Start waits on it instead of returning nil for an instance
	// that never actually launched.
	started chan struct{}
	initErr error // written before started is closed, read after it is closed
}

// Runner executes AI tasks and writes findings into the AI Event Center.
type Runner struct {
	db  *gorm.DB
	zlm *video.Client

	sink EventSink

	mu     sync.Mutex
	tasks  map[uint]*runnerState
	global *semaphore
	chSems map[uint]*semaphore

	// Lifecycle. baseCtx is canceled by Close/Shutdown; every execution,
	// recovery scan and one-shot run derives from it so shutdown stops new
	// work while in-flight attempts wind down. inflight/idle let Close wait
	// without spawning a dedicated waiter goroutine.
	baseCtx    context.Context
	baseCancel context.CancelFunc
	closed     bool          // guarded by mu
	abandoned  bool          // guarded by mu; set when Close exceeds its grace period
	closedDone chan struct{} // closed once shutdown begins; idempotent Close waits on it
	inflight   int           // guarded by mu
	idle       chan struct{} // closed whenever inflight reaches zero
	recoverMu  sync.Mutex    // serializes recover scans/work per runner

	optMu            sync.RWMutex
	opts             RunnerOptions
	taskHasErrorCols bool

	// Persistence failures are surfaced as alerts/counters instead of being
	// silently logged and forgotten. They are safe for tests and health checks
	// to observe without exposing task prompts or provider credentials.
	runCreateFailures  int64
	runUpdateFailures  int64
	taskStatusFailures int64

	workerID string

	// Test seams. Defaults are the production implementations.
	analyzerFactory func(model.AIProvider) (Analyzer, error)
	frameGrabber    func(context.Context, string) ([]byte, error)
}

// NewRunner builds a Runner with production defaults.
func NewRunner(db *gorm.DB, zlm *video.Client) *Runner {
	return NewRunnerWithOptions(db, zlm, RunnerOptions{})
}

// NewRunnerWithOptions builds a Runner with explicit bounds.
func NewRunnerWithOptions(db *gorm.DB, zlm *video.Client, opts RunnerOptions) *Runner {
	opts = normalizeOptions(opts)
	baseCtx, baseCancel := context.WithCancel(context.Background())
	idle := make(chan struct{})
	close(idle)
	r := &Runner{
		db: db, zlm: zlm,
		tasks:           map[uint]*runnerState{},
		chSems:          map[uint]*semaphore{},
		opts:            opts,
		global:          newSemaphore(opts.MaxConcurrency),
		workerID:        newWorkerID(),
		baseCtx:         baseCtx,
		baseCancel:      baseCancel,
		closedDone:      make(chan struct{}),
		idle:            idle,
		analyzerFactory: NewAnalyzer,
		frameGrabber:    media.GrabFrame,
	}
	if db != nil && db.Migrator().HasColumn(&model.AITask{}, "last_error_code") {
		r.taskHasErrorCols = true
	}
	return r
}

func normalizeOptions(opts RunnerOptions) RunnerOptions {
	if opts.MaxConcurrency < 1 {
		opts.MaxConcurrency = defaultMaxConcurrency
	}
	if opts.PerChannelLimit < 1 {
		opts.PerChannelLimit = defaultPerChannelLimit
	}
	if opts.TaskTimeout <= 0 {
		opts.TaskTimeout = defaultTaskTimeout
	}
	if opts.MaxRetries < 0 {
		opts.MaxRetries = defaultMaxRetries
	}
	if opts.BackoffBase <= 0 {
		opts.BackoffBase = defaultBackoffBase
	}
	if opts.ShutdownTimeout <= 0 {
		opts.ShutdownTimeout = defaultShutdownTimeout
	}
	return opts
}

// Configure updates bounds at runtime. Changed limits apply to tasks started
// afterwards and to already-waiting acquisitions immediately. It is a no-op
// after shutdown so a closed runner cannot be resurrected.
func (r *Runner) Configure(opts RunnerOptions) {
	opts = normalizeOptions(opts)
	r.optMu.Lock()
	r.opts = opts
	r.optMu.Unlock()
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return
	}
	r.global.setLimit(opts.MaxConcurrency)
	for _, sem := range r.chSems {
		sem.setLimit(opts.PerChannelLimit)
	}
	r.mu.Unlock()
}

func (r *Runner) currentOptions() RunnerOptions {
	r.optMu.RLock()
	defer r.optMu.RUnlock()
	return r.opts
}

// track reserves n in-flight slots. A failed reservation means the runner is
// closing and no new work may start. Callers must balance it with finishTrack.
func (r *Runner) track(n int) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.trackLocked(n)
}

// trackLocked is track with r.mu already held.
func (r *Runner) trackLocked(n int) bool {
	if r.closed {
		return false
	}
	if r.inflight == 0 {
		r.idle = make(chan struct{})
	}
	r.inflight += n
	return true
}

func (r *Runner) finishTrack() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.inflight--
	if r.inflight <= 0 {
		r.inflight = 0
		close(r.idle)
	}
}

// idleLocked returns a channel closed when all tracked work has exited.
// Caller holds r.mu.
func (r *Runner) idleLocked() <-chan struct{} {
	if r.inflight == 0 {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return r.idle
}

// deriveCtx binds a task/caller context to the runner lifetime: Close cancels
// every derived context even when the original caller stays alive.
func (r *Runner) deriveCtx(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	ctx, cancel := context.WithCancel(parent)
	stop := context.AfterFunc(r.baseCtx, cancel)
	return ctx, func() { stop(); cancel() }
}

// IsClosed reports whether Close/Shutdown has begun.
func (r *Runner) IsClosed() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.closed
}

// Done is closed when shutdown begins (not when it finishes). Close itself is
// idempotent and blocks until in-flight work has exited.
func (r *Runner) Done() <-chan struct{} { return r.closedDone }

// canWriteDB guards finalizer writes from work abandoned by a shutdown timeout.
func (r *Runner) canWriteDB() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return !r.abandoned
}

// isLiveLocked reports whether a task still has a live execution loop.
// Caller holds r.mu.
func (r *Runner) isLiveLocked(taskID uint) bool {
	state := r.tasks[taskID]
	if state == nil {
		return false
	}
	select {
	case <-state.done:
		return false
	default:
		return true
	}
}

func newWorkerID() string {
	host, _ := os.Hostname()
	buf := make([]byte, 4)
	_, _ = rand.Read(buf)
	return fmt.Sprintf("%s-%d-%s", host, os.Getpid(), hex.EncodeToString(buf))
}

// SetSink attaches an event sink (notifications, semantic indexing).
func (r *Runner) SetSink(sink EventSink) { r.sink = sink }

// SetAnalyzerFactory and SetFrameGrabber are test seams; production code does
// not call them. They let tests exercise the scheduler without a real detector
// or ffmpeg.
func (r *Runner) SetAnalyzerFactory(fn func(model.AIProvider) (Analyzer, error)) {
	if fn != nil {
		r.analyzerFactory = fn
	}
}

func (r *Runner) SetFrameGrabber(fn func(context.Context, string) ([]byte, error)) {
	if fn != nil {
		r.frameGrabber = fn
	}
}

// Start begins periodic execution of a task until Stop is called. It is
// idempotent: a task never has two live execution loops. DB work is performed
// outside the lock so a slow database cannot block Stop/Close/other Starts.
// If any pre-launch DB step fails, no goroutine is launched and every resource
// reserved by this call (context, registered state) is released before the
// error is returned.
func (r *Runner) Start(taskID uint) error {
	// Tracking the caller covers the DB work done before the loop launches, so
	// Close waits for it instead of returning while a Start is still writing.
	if !r.track(1) {
		return ErrRunnerClosed
	}
	defer r.finishTrack()

	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return ErrRunnerClosed
	}
	if existing, ok := r.tasks[taskID]; ok {
		select {
		case <-existing.done: // stale entry from a loop that just exited
			delete(r.tasks, taskID)
		default:
			r.mu.Unlock()
			// The first caller owns the launch. Wait for its DB work so a
			// concurrent Start never reports success for a failed launch.
			<-existing.started
			if r.IsClosed() || errors.Is(existing.initErr, ErrRunnerClosed) {
				return ErrRunnerClosed
			}
			return existing.initErr
		}
	}
	ctx, cancel := r.deriveCtx(r.baseCtx)
	state := &runnerState{
		id: taskID, cancel: cancel,
		done: make(chan struct{}), started: make(chan struct{}),
	}
	r.tasks[taskID] = state
	r.mu.Unlock()

	var task model.AITask
	var initErr error
	if err := r.db.First(&task, taskID).Error; err != nil {
		initErr = fmt.Errorf("ai task_id=%d load failed: %w", taskID, err)
	}
	// Keep the historical side effect of Start: starting a task also marks it
	// enabled in the UI. Recovery never relies on enabled to decide what to run.
	if initErr == nil {
		if err := r.db.Model(&model.AITask{}).Where("id = ?", taskID).
			Updates(map[string]any{"status": "running", "enabled": true}).Error; err != nil {
			initErr = fmt.Errorf("ai task_id=%d running status update failed: %w", taskID, err)
		}
	}

	if initErr != nil {
		state.initErr = initErr
		close(state.started)
		cancel()
		// Close done as well: a concurrent Stop must not wait five seconds for
		// a loop that was never launched.
		close(state.done)
		r.dropState(state)
		log.Printf("[ai] ALERT task_id=%d start aborted, no execution launched: %v", taskID, initErr)
		return initErr
	}

	// Reserve the loop slot and check the closed flag atomically: a shutdown
	// between the DB work and this point must not launch an execution.
	r.mu.Lock()
	if r.closed || r.tasks[taskID] != state || !r.trackLocked(1) {
		r.mu.Unlock()
		state.initErr = ErrRunnerClosed
		close(state.started)
		cancel()
		close(state.done)
		r.dropState(state)
		log.Printf("[ai] task_id=%d start aborted: runner is shutting down", taskID)
		return ErrRunnerClosed
	}
	r.mu.Unlock()
	close(state.started)
	go func() {
		defer r.finishTrack()
		defer close(state.done)
		defer r.dropState(state)
		r.taskLoop(ctx, taskID, task.ChannelID)
	}()
	return nil
}

func (r *Runner) dropState(state *runnerState) {
	r.mu.Lock()
	if r.tasks[state.id] == state {
		delete(r.tasks, state.id)
	}
	r.mu.Unlock()
}

// Stop cancels a running task, waits for its loop to exit, and records
// terminal state. It is safe to call for a task that is not running.
func (r *Runner) Stop(taskID uint) {
	if !r.track(1) {
		return // closed: no DB write, no new work
	}
	defer r.finishTrack()

	r.mu.Lock()
	state := r.tasks[taskID]
	if state != nil {
		select {
		case <-state.done:
			state = nil
		default:
		}
	}
	r.mu.Unlock()
	if state != nil {
		state.cancel()
		select {
		case <-state.done:
		case <-time.After(5 * time.Second):
			log.Printf("[ai] task_id=%d stop wait timed out", taskID)
		}
	}
	r.setTaskStatus(taskID, "stopped", "", "")
}

// Close cancels every task and waits for in-flight work to exit. It is
// idempotent (concurrent/repeated calls all wait for the same shutdown), and
// no Start/Recover/RunOnce may launch work once it begins. Shutdown is an
// alias for graceful process shutdown.
func (r *Runner) Close() {
	r.mu.Lock()
	if r.closed {
		done := r.closedDone
		r.mu.Unlock()
		<-done
		return
	}
	r.closed = true
	close(r.closedDone)
	idle := r.idleLocked()
	states := make([]*runnerState, 0, len(r.tasks))
	for _, state := range r.tasks {
		states = append(states, state)
	}
	// Release every concurrency resource: waiters are woken with ErrRunnerClosed
	// by the closed semaphores instead of lingering until their context expires.
	r.global.close()
	for _, sem := range r.chSems {
		sem.close()
	}
	r.chSems = map[uint]*semaphore{}
	r.tasks = map[uint]*runnerState{}
	grace := r.currentOptions().ShutdownTimeout
	r.mu.Unlock()

	r.baseCancel()
	begin := time.Now()
	log.Printf("[ai] runner shutdown: canceling %d active task(s)", len(states))
	for _, state := range states {
		state.cancel()
		log.Printf("[ai] task_id=%d shutdown: execution canceled", state.id)
	}
	select {
	case <-idle:
		log.Printf("[ai] runner shutdown complete in %v", time.Since(begin))
	case <-time.After(grace):
		// A provider that ignores cancellation must not write to the DB after
		// Close has returned: skip the remaining finalizer updates.
		r.mu.Lock()
		r.abandoned = true
		r.mu.Unlock()
		log.Printf("[ai] ALERT runner shutdown exceeded %v; in-flight work abandoned", grace)
	}
}

func (r *Runner) Shutdown() { r.Close() }

// Recover reconciles task state after a process restart. Tasks left in
// status=running are restarted only when their config sets autoRecover=true;
// the rest are reset to stopped. Lingering non-terminal runs are failed and
// closed out. Recovery never uses enabled as a signal: enabled means the task
// is configured, not that it was started.
func (r *Runner) Recover() error {
	if !r.track(1) {
		return ErrRunnerClosed
	}
	defer r.finishTrack()
	if r.baseCtx.Err() != nil {
		return ErrRunnerClosed
	}

	r.recoverMu.Lock()
	defer r.recoverMu.Unlock()
	var tasks []model.AITask
	if err := r.db.Where("status = ?", "running").Find(&tasks).Error; err != nil {
		log.Printf("[ai] recover scan failed: %v", err)
		return err
	}
	for i := range tasks {
		if r.IsClosed() || r.baseCtx.Err() != nil {
			log.Printf("[ai] recover aborted during shutdown")
			return ErrRunnerClosed
		}
		if err := r.recoverTaskLocked(tasks[i].ID); err != nil && !errors.Is(err, ErrRunnerClosed) {
			log.Printf("[ai] task_id=%d recover failed: %v", tasks[i].ID, err)
		}
	}
	return nil
}

// RecoverTask recovers a single task. A failure is returned to the caller but
// is never fatal to the service (Recover only logs it).
func (r *Runner) RecoverTask(taskID uint) error {
	if !r.track(1) {
		return ErrRunnerClosed
	}
	defer r.finishTrack()

	r.recoverMu.Lock()
	defer r.recoverMu.Unlock()
	return r.recoverTaskLocked(taskID)
}

// recoverTaskLocked implements recovery with recoverMu held. Runs owned by
// this worker are left alone: repeated/concurrent recovery must not kill a
// live execution, and Start is idempotent so only one loop exists per task.
func (r *Runner) recoverTaskLocked(taskID uint) error {
	if r.IsClosed() {
		return ErrRunnerClosed
	}
	var task model.AITask
	if err := r.db.First(&task, taskID).Error; err != nil {
		return err
	}
	now := time.Now()
	if err := r.db.Model(&model.AITaskRun{}).
		Where("task_id = ? AND status IN ? AND (worker_id IS NULL OR worker_id <> ?)",
			taskID, nonTerminalRunStatuses, r.workerID).
		Updates(map[string]any{
			"status":      RunFailed,
			"error_code":  string(ErrorInternal),
			"error_msg":   "runner restarted before the run finished",
			"finished_at": now,
		}).Error; err != nil {
		return err
	}
	cfg := decodeConfig(task.Config)
	if cfg.AutoRecover {
		if err := r.setTaskStatus(taskID, "running", "", ""); err != nil {
			return err
		}
		return r.Start(taskID)
	}
	// Another caller may have started this task already; recovery must not
	// stop a live loop that was launched outside Recover.
	r.mu.Lock()
	live := r.isLiveLocked(taskID)
	r.mu.Unlock()
	if !live {
		r.Stop(taskID)
	}
	return nil
}

// RunOnce executes a task a single time and returns the generated events. Its
// signature and response shape are kept stable for the /ai/tasks/:id/run API.
// The execution is bound to the runner lifetime: Close cancels it as well.
func (r *Runner) RunOnce(ctx context.Context, task model.AITask) ([]model.AIEvent, error) {
	if !r.track(1) {
		return nil, ErrRunnerClosed
	}
	defer r.finishTrack()
	runCtx, cancel := r.deriveCtx(ctx)
	defer cancel()
	events, cerr := r.analyzeTask(runCtx, task)
	if cerr != nil {
		return events, cerr
	}
	return events, nil
}

func (r *Runner) analyzeTask(ctx context.Context, task model.AITask) ([]model.AIEvent, *classifiedError) {
	if task.ProviderID == 0 || task.ChannelID == 0 {
		return nil, classify(ErrorInvalidTaskConfig, false, "task is missing channel or provider", nil)
	}
	var provider model.AIProvider
	if err := r.db.First(&provider, task.ProviderID).Error; err != nil {
		return nil, classify(ErrorProviderUnavailable, false, fmt.Sprintf("ai provider not found (%T)", err), err)
	}
	analyzer, err := r.analyzerFactory(provider)
	if err != nil {
		return nil, classify(ErrorInvalidTaskConfig, false, fmt.Sprintf("cannot create analyzer (%T)", err), err)
	}
	cfg := decodeConfig(task.Config)
	in := AnalyzeInput{Prompt: cfg.Prompt, Config: json.RawMessage(task.Config)}

	source := r.sourceURL(task.ChannelID)
	if source == "" {
		return nil, classify(ErrorStreamUnavailable, true, "task channel has no available stream source", nil)
	}
	in.MediaURL = source
	if cfg.GrabFrame || provider.Kind == KindVLM {
		frame, err := r.frameGrabber(ctx, source)
		if err != nil {
			return nil, classifyFrameErr(ctx, err)
		}
		in.Frame = frame
	}

	results, err := analyzer.Analyze(ctx, in)
	if err != nil {
		return nil, classifyProviderErr(ctx, err)
	}
	events := make([]model.AIEvent, 0, len(results))
	for _, res := range results {
		payload, _ := json.Marshal(res.Payload)
		ev := model.AIEvent{
			ChannelID:  task.ChannelID,
			TaskID:     task.ID,
			ProviderID: provider.ID,
			Kind:       provider.Kind,
			EventType:  res.EventType,
			Level:      defaultLevel(res.Level),
			Confidence: res.Confidence,
			Summary:    res.Summary,
			Payload:    string(payload),
			OccurredAt: time.Now(),
		}
		if err := r.db.Create(&ev).Error; err != nil {
			// An event-store failure is internal: never classify it as a
			// provider failure and never retry (would duplicate findings).
			return events, classify(ErrorInternal, false, fmt.Sprintf("failed to persist AI event (%T)", err), err)
		}
		if r.sink != nil {
			if r.track(1) {
				go func(ev model.AIEvent) {
					defer r.finishTrack()
					r.sink.OnEvent(ev)
				}(ev)
			} else {
				log.Printf("[ai] task_id=%d event sink dropped during shutdown", task.ID)
			}
		}
		events = append(events, ev)
	}
	return events, nil
}

// sourceURL prefers the channel's configured source, falling back to the
// RTSP distribution URL served by ZLMediaKit.
func (r *Runner) sourceURL(channelID uint) string {
	var ch model.Channel
	if err := r.db.First(&ch, channelID).Error; err != nil {
		return ""
	}
	if ch.SourceURL != "" {
		return ch.SourceURL
	}
	if r.zlm != nil {
		return r.zlm.PlayURLs(ch.StreamKey)["rtsp"]
	}
	return ""
}

func (r *Runner) channelSem(channelID uint) *semaphore {
	key := channelID
	if key == 0 {
		key = ^uint(0) // isolate misconfigured tasks from real channels
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		// A stray in-flight attempt must not resurrect a per-channel slot.
		closed := newSemaphore(1)
		closed.close()
		return closed
	}
	if sem, ok := r.chSems[key]; ok {
		return sem
	}
	sem := newSemaphore(r.currentOptions().PerChannelLimit)
	r.chSems[key] = sem
	return sem
}

// updateRun writes run fields. Failures are returned, counted and logged as
// alerts so a dropped status transition is never silent. Values are limited to
// IDs/codes; no prompt, key or payload is ever logged.
func (r *Runner) updateRun(runID uint, fields map[string]any) error {
	if runID == 0 || !r.canWriteDB() {
		return nil
	}
	if err := r.db.Model(&model.AITaskRun{}).Where("id = ?", runID).Updates(fields).Error; err != nil {
		atomic.AddInt64(&r.runUpdateFailures, 1)
		log.Printf("[ai] ALERT run_id=%d update failed fields=%v: %v", runID, fields, err)
		return err
	}
	return nil
}

// RunCreateFailures reports how many run records could not be persisted.
func (r *Runner) RunCreateFailures() int64 { return atomic.LoadInt64(&r.runCreateFailures) }

// RunUpdateFailures reports how many run status transitions could not be
// persisted (including terminal statuses and heartbeats).
func (r *Runner) RunUpdateFailures() int64 { return atomic.LoadInt64(&r.runUpdateFailures) }

// TaskStatusFailures reports how many task status transitions could not be
// persisted.
func (r *Runner) TaskStatusFailures() int64 { return atomic.LoadInt64(&r.taskStatusFailures) }

func (r *Runner) setTaskStatus(taskID uint, status, errCode, errMsg string) error {
	if !r.canWriteDB() {
		return nil
	}
	fields := map[string]any{"status": status}
	if errCode != "" && r.taskHasErrorCols {
		fields["last_error_code"] = errCode
		fields["last_error_msg"] = errMsg
	}
	if err := r.db.Model(&model.AITask{}).Where("id = ?", taskID).Updates(fields).Error; err != nil {
		atomic.AddInt64(&r.taskStatusFailures, 1)
		log.Printf("[ai] ALERT task_id=%d status=%s update failed: %v", taskID, status, err)
		return err
	}
	return nil
}

func decodeConfig(raw string) TaskConfig {
	var cfg TaskConfig
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &cfg)
	}
	return cfg
}

func defaultLevel(l string) string {
	if l == "" {
		return "info"
	}
	return l
}

// ErrNoProvider is returned when a task references a missing provider.
var ErrNoProvider = errors.New("ai provider not found")
