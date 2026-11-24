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
}

const (
	defaultMaxConcurrency  = 4
	defaultPerChannelLimit = 1
	defaultTaskTimeout     = 120 * time.Second
	defaultMaxRetries      = 2
	defaultBackoffBase     = 2 * time.Second
	maxBackoff             = 60 * time.Second
)

// run statuses persisted in AITaskRun.
const (
	RunPending   = "pending"
	RunStarting  = "starting"
	RunRunning   = "running"
	RunStopping  = "stopping"
	RunFailed    = "failed"
	RunStopped   = "stopped"
	RunSucceeded = "succeeded"
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

	optMu            sync.RWMutex
	opts             RunnerOptions
	taskHasErrorCols bool

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
	r := &Runner{
		db: db, zlm: zlm,
		tasks:           map[uint]*runnerState{},
		chSems:          map[uint]*semaphore{},
		opts:            opts,
		global:          newSemaphore(opts.MaxConcurrency),
		workerID:        newWorkerID(),
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
	return opts
}

// Configure updates bounds at runtime. Changed limits apply to tasks started
// afterwards and to already-waiting acquisitions immediately.
func (r *Runner) Configure(opts RunnerOptions) {
	opts = normalizeOptions(opts)
	r.optMu.Lock()
	r.opts = opts
	r.optMu.Unlock()
	r.global.setLimit(opts.MaxConcurrency)
	r.mu.Lock()
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
func (r *Runner) Start(taskID uint) error {
	r.mu.Lock()
	if existing, ok := r.tasks[taskID]; ok {
		select {
		case <-existing.done: // stale entry from a loop that just exited
		default:
			r.mu.Unlock()
			return nil
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	state := &runnerState{id: taskID, cancel: cancel, done: make(chan struct{})}
	r.tasks[taskID] = state
	r.mu.Unlock()

	var task model.AITask
	if err := r.db.First(&task, taskID).Error; err != nil {
		cancel()
		r.dropState(state)
		return err
	}
	// Keep the historical side effect of Start: starting a task also marks it
	// enabled in the UI. Recovery never relies on enabled to decide what to run.
	if err := r.db.Model(&model.AITask{}).Where("id = ?", taskID).
		Updates(map[string]any{"status": "running", "enabled": true}).Error; err != nil {
		log.Printf("[ai] task_id=%d start status update failed: %v", taskID, err)
	}
	go func() {
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
	r.mu.Lock()
	state, ok := r.tasks[taskID]
	r.mu.Unlock()
	if ok && state != nil {
		state.cancel()
		select {
		case <-state.done:
		case <-time.After(5 * time.Second):
			log.Printf("[ai] task_id=%d stop wait timed out", taskID)
		}
	}
	r.setTaskStatus(taskID, "stopped", "", "")
}

// Close cancels every task and waits for their loops to exit. Shutdown is an
// alias for graceful process shutdown.
func (r *Runner) Close() {
	r.mu.Lock()
	states := make([]*runnerState, 0, len(r.tasks))
	for _, state := range r.tasks {
		states = append(states, state)
	}
	r.mu.Unlock()
	for _, state := range states {
		state.cancel()
	}
	for _, state := range states {
		select {
		case <-state.done:
		case <-time.After(5 * time.Second):
			log.Printf("[ai] task_id=%d close wait timed out", state.id)
		}
	}
}

func (r *Runner) Shutdown() { r.Close() }

// Recover reconciles task state after a process restart. Tasks left in
// status=running are restarted only when their config sets autoRecover=true;
// the rest are reset to stopped. Lingering non-terminal runs are failed and
// closed out. Recovery never uses enabled as a signal: enabled means the task
// is configured, not that it was started.
func (r *Runner) Recover() {
	var tasks []model.AITask
	if err := r.db.Where("status = ?", "running").Find(&tasks).Error; err != nil {
		log.Printf("[ai] recover scan failed: %v", err)
		return
	}
	for i := range tasks {
		if err := r.RecoverTask(tasks[i].ID); err != nil {
			log.Printf("[ai] task_id=%d recover failed: %v", tasks[i].ID, err)
		}
	}
}

// RecoverTask recovers a single task. A failure is returned to the caller but
// is never fatal to the service (Recover only logs it).
func (r *Runner) RecoverTask(taskID uint) error {
	var task model.AITask
	if err := r.db.First(&task, taskID).Error; err != nil {
		return err
	}
	now := time.Now()
	if err := r.db.Model(&model.AITaskRun{}).
		Where("task_id = ? AND status IN ?", taskID, nonTerminalRunStatuses).
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
		r.setTaskStatus(taskID, "running", "", "")
		return r.Start(taskID)
	}
	r.Stop(taskID)
	return nil
}

// RunOnce executes a task a single time and returns the generated events. Its
// signature and response shape are kept stable for the /ai/tasks/:id/run API.
func (r *Runner) RunOnce(ctx context.Context, task model.AITask) ([]model.AIEvent, error) {
	events, cerr := r.analyzeTask(ctx, task)
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
			go r.sink.OnEvent(ev)
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
	if sem, ok := r.chSems[key]; ok {
		return sem
	}
	sem := newSemaphore(r.currentOptions().PerChannelLimit)
	r.chSems[key] = sem
	return sem
}

// updateRun writes run fields with a structured log on failure. Values are
// limited to IDs/codes; no prompt, key or payload is ever logged.
func (r *Runner) updateRun(runID uint, fields map[string]any) {
	if err := r.db.Model(&model.AITaskRun{}).Where("id = ?", runID).Updates(fields).Error; err != nil {
		log.Printf("[ai] run_id=%d update failed: %v", runID, err)
	}
}

func (r *Runner) setTaskStatus(taskID uint, status, errCode, errMsg string) {
	fields := map[string]any{"status": status}
	if errCode != "" && r.taskHasErrorCols {
		fields["last_error_code"] = errCode
		fields["last_error_msg"] = errMsg
	}
	if err := r.db.Model(&model.AITask{}).Where("id = ?", taskID).Updates(fields).Error; err != nil {
		log.Printf("[ai] task_id=%d status update failed: %v", taskID, err)
	}
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
