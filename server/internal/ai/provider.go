// Package ai implements the AI Intelligence Middle Platform: a unified
// abstraction over CV (detection/structured), VLM (vision-language) and LLM
// (text/semantic) providers, plus the task runner that feeds events into the
// AI Event Center.
package ai

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/easyavr/easyavr/internal/model"
)

// Kind values for providers and tasks.
const (
	KindCV  = "cv"
	KindVLM = "vlm"
	KindLLM = "llm"
)

// AnalyzeInput is the common request passed to any analyzer.
type AnalyzeInput struct {
	MediaURL string          // stream URL or image URL
	Frame    []byte          // optional JPEG frame grabbed from a stream
	Prompt   string          // instruction for VLM/LLM
	Config   json.RawMessage // task-specific config (roi, labels, ...)
}

// Result is a normalized finding produced by any AI capability.
type Result struct {
	EventType  string         `json:"eventType"`
	Level      string         `json:"level"` // info, warning, critical
	Confidence float64        `json:"confidence"`
	Summary    string         `json:"summary"`
	Payload    map[string]any `json:"payload"`
}

// Analyzer is the unified capability interface for CV/VLM/LLM backends.
type Analyzer interface {
	Kind() string
	Analyze(ctx context.Context, in AnalyzeInput) ([]Result, error)
}

// NewAnalyzer builds an Analyzer from a persisted provider definition.
// Unknown vendors fall back to the generic HTTP detector for CV and the
// OpenAI-compatible client for VLM/LLM.
func NewAnalyzer(p model.AIProvider) (Analyzer, error) {
	switch p.Kind {
	case KindCV:
		return &HTTPDetector{Name: p.Name, Endpoint: p.Endpoint, APIKey: p.APIKey, Model: p.Model}, nil
	case KindVLM, KindLLM:
		return &OpenAICompatible{Name: p.Name, Provider: p.Kind, Endpoint: p.Endpoint, APIKey: p.APIKey, Model: p.Model}, nil
	default:
		return nil, fmt.Errorf("unsupported provider kind %q", p.Kind)
	}
}
