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

// OpenAICompatible talks to any OpenAI-compatible /v1/chat/completions
// endpoint (OpenAI, vLLM, Ollama, LM Studio, Qwen, DeepSeek, ...). It is used
// for VLM (image+text) and LLM (text) capabilities.
type OpenAICompatible struct {
	Name     string
	Provider string // "vlm" or "llm"
	Endpoint string
	APIKey   string
	Model    string
	client   *http.Client
}

func (o *OpenAICompatible) Kind() string { return o.Provider }

func (o *OpenAICompatible) httpClient() *http.Client {
	if o.client == nil {
		o.client = &http.Client{Timeout: 60 * time.Second}
	}
	return o.client
}

type chatMessage struct {
	Role    string `json:"role"`
	Content any    `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model"`
	Messages    []chatMessage `json:"messages"`
	Temperature float64       `json:"temperature"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

// Analyze sends the input to the model. When a frame/image is present it is
// attached as a base64 data URL for vision models.
func (o *OpenAICompatible) Analyze(ctx context.Context, in AnalyzeInput) ([]Result, error) {
	prompt := in.Prompt
	if prompt == "" {
		prompt = "Describe any security-relevant events in this image. " +
			"Respond ONLY as JSON: {\"eventType\":string,\"level\":\"info|warning|critical\",\"confidence\":number,\"summary\":string}."
	}
	content := []map[string]any{{"type": "text", "text": prompt}}
	if len(in.Frame) > 0 {
		content = append(content, map[string]any{
			"type": "image_url",
			"image_url": map[string]string{
				"url": "data:image/jpeg;base64," + base64.StdEncoding.EncodeToString(in.Frame),
			},
		})
	}
	body, _ := json.Marshal(chatRequest{
		Model:       o.Model,
		Messages:    []chatMessage{{Role: "user", Content: content}},
		Temperature: 0.1,
	})
	endpoint := strings.TrimRight(o.Endpoint, "/")
	if !strings.HasSuffix(endpoint, "/chat/completions") {
		endpoint += "/v1/chat/completions"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if o.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+o.APIKey)
	}
	resp, err := o.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var parsed chatResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("ai: invalid response: %s", string(raw))
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("ai: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return nil, fmt.Errorf("ai: empty choices")
	}
	return parseResults(parsed.Choices[0].Message.Content), nil
}

// parseResults tolerantly extracts structured results from model text,
// falling back to a single summary result when the model did not return JSON.
func parseResults(text string) []Result {
	trimmed := strings.TrimSpace(text)
	if i := strings.Index(trimmed, "{"); i >= 0 {
		if j := strings.LastIndex(trimmed, "}"); j > i {
			var single Result
			if err := json.Unmarshal([]byte(trimmed[i:j+1]), &single); err == nil && single.Summary != "" {
				if single.Level == "" {
					single.Level = "info"
				}
				return []Result{single}
			}
			var many []Result
			if err := json.Unmarshal([]byte(trimmed[i:j+1]), &many); err == nil && len(many) > 0 {
				return many
			}
		}
	}
	return []Result{{Level: "info", Summary: trimmed}}
}
