package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
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
}

// EventSink receives events produced by tasks (used to index and notify).
type EventSink interface {
	OnEvent(model.AIEvent)
}

// Runner executes AI tasks and writes findings into the AI Event Center.
type Runner struct {
	db  *gorm.DB
	zlm *video.Client

	sink EventSink

	mu      sync.Mutex
	running map[uint]context.CancelFunc
}

func NewRunner(db *gorm.DB, zlm *video.Client) *Runner {
	return &Runner{db: db, zlm: zlm, running: map[uint]context.CancelFunc{}}
}

// SetSink attaches an event sink (notifications, semantic indexing).
func (r *Runner) SetSink(sink EventSink) { r.sink = sink }

// Start begins periodic execution of a task until Stop is called.
func (r *Runner) Start(taskID uint) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.running[taskID]; ok {
		return nil
	}
	var task model.AITask
	if err := r.db.First(&task, taskID).Error; err != nil {
		return err
	}
	cfg := decodeConfig(task.Config)
	interval := cfg.IntervalSec
	if interval < 5 {
		interval = 30
	}
	ctx, cancel := context.WithCancel(context.Background())
	r.running[taskID] = cancel
	r.db.Model(&task).Updates(map[string]any{"status": "running", "enabled": true})

	go func() {
		ticker := time.NewTicker(time.Duration(interval) * time.Second)
		defer ticker.Stop()
		r.executeOnce(ctx, taskID)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.executeOnce(ctx, taskID)
			}
		}
	}()
	return nil
}

// Stop cancels a running task.
func (r *Runner) Stop(taskID uint) {
	r.mu.Lock()
	cancel, ok := r.running[taskID]
	if ok {
		delete(r.running, taskID)
	}
	r.mu.Unlock()
	if ok {
		cancel()
	}
	r.db.Model(&model.AITask{}).Where("id = ?", taskID).Update("status", "stopped")
}

// RunOnce executes a task a single time and returns the generated events.
func (r *Runner) RunOnce(ctx context.Context, task model.AITask) ([]model.AIEvent, error) {
	var provider model.AIProvider
	if err := r.db.First(&provider, task.ProviderID).Error; err != nil {
		return nil, fmt.Errorf("provider not found: %w", err)
	}
	analyzer, err := NewAnalyzer(provider)
	if err != nil {
		return nil, err
	}
	cfg := decodeConfig(task.Config)
	in := AnalyzeInput{Prompt: cfg.Prompt, Config: json.RawMessage(task.Config)}

	source := r.sourceURL(task.ChannelID)
	in.MediaURL = source
	if cfg.GrabFrame || provider.Kind == KindVLM {
		frame, err := media.GrabFrame(ctx, source)
		if err != nil {
			return nil, fmt.Errorf("grab frame: %w", err)
		}
		in.Frame = frame
	}

	results, err := analyzer.Analyze(ctx, in)
	if err != nil {
		return nil, err
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
			return events, err
		}
		if r.sink != nil {
			ev := ev
			go r.sink.OnEvent(ev)
		}
		events = append(events, ev)
	}
	return events, nil
}

func (r *Runner) executeOnce(ctx context.Context, taskID uint) {
	var task model.AITask
	if err := r.db.First(&task, taskID).Error; err != nil {
		return
	}
	if _, err := r.RunOnce(ctx, task); err != nil {
		log.Printf("[ai] task %d (%s) failed: %v", task.ID, task.Name, err)
	}
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
