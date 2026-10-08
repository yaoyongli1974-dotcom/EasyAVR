package ai

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPDetector adapts an external CV detection worker. The worker receives a
// JSON payload and must return either a single Result object or an array of
// them. This keeps the platform vendor-neutral for CV backends (YOLO, Paddle,
// TensorRT, RKNN boxes, ...).
//
// Request:  {"mediaUrl":string,"frame":"<base64 jpeg>","model":string,"config":{...}}
// Response: {"eventType","level","confidence","summary","payload"} or [ ... ]
type HTTPDetector struct {
	Name     string
	Endpoint string
	APIKey   string
	Model    string
	client   *http.Client
}

func (d *HTTPDetector) Kind() string { return KindCV }

func (d *HTTPDetector) httpClient() *http.Client {
	if d.client == nil {
		d.client = &http.Client{Timeout: 30 * time.Second}
	}
	return d.client
}

func (d *HTTPDetector) Analyze(ctx context.Context, in AnalyzeInput) ([]Result, error) {
	payload := map[string]any{
		"mediaUrl": in.MediaURL,
		"model":    d.Model,
		"prompt":   in.Prompt,
	}
	if len(in.Frame) > 0 {
		payload["frame"] = base64.StdEncoding.EncodeToString(in.Frame)
	}
	if len(in.Config) > 0 {
		payload["config"] = json.RawMessage(in.Config)
	}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if d.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+d.APIKey)
	}
	resp, err := d.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	trimmed := strings.TrimSpace(string(raw))
	if strings.HasPrefix(trimmed, "[") {
		var many []Result
		if err := json.Unmarshal(raw, &many); err != nil {
			return nil, err
		}
		return many, nil
	}
	var one Result
	if err := json.Unmarshal(raw, &one); err != nil {
		return nil, fmt.Errorf("cv worker returned invalid json: %s", trimmed)
	}
	return []Result{one}, nil
}
