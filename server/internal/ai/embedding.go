package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"strings"
	"time"
)

// KindEmbedding is the provider kind for text embedding models.
const KindEmbedding = "embedding"

// Embedder produces vector embeddings for text.
type Embedder interface {
	Embed(ctx context.Context, texts []string) ([][]float32, error)
	Model() string
}

// EmbeddingClient talks to an OpenAI-compatible /v1/embeddings endpoint.
type EmbeddingClient struct {
	Name      string
	Endpoint  string
	APIKey    string
	ModelName string
	client    *http.Client
}

func NewEmbeddingClient(name, endpoint, apiKey, model string) *EmbeddingClient {
	return &EmbeddingClient{Name: name, Endpoint: endpoint, APIKey: apiKey, ModelName: model}
}

func (e *EmbeddingClient) Model() string { return e.ModelName }

func (e *EmbeddingClient) httpClient() *http.Client {
	if e.client == nil {
		e.client = &http.Client{Timeout: 30 * time.Second}
	}
	return e.client
}

type embedRequest struct {
	Model string   `json:"model"`
	Input []string `json:"input"`
}

type embedResponse struct {
	Data []struct {
		Embedding []float32 `json:"embedding"`
	} `json:"data"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error"`
}

func (e *EmbeddingClient) Embed(ctx context.Context, texts []string) ([][]float32, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	body, _ := json.Marshal(embedRequest{Model: e.ModelName, Input: texts})
	endpoint := strings.TrimRight(e.Endpoint, "/")
	if !strings.HasSuffix(endpoint, "/embeddings") {
		endpoint += "/v1/embeddings"
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	if e.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+e.APIKey)
	}
	resp, err := e.httpClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	var parsed embedResponse
	if err := json.Unmarshal(raw, &parsed); err != nil {
		return nil, fmt.Errorf("embedding: invalid response: %s", string(raw))
	}
	if parsed.Error != nil {
		return nil, fmt.Errorf("embedding: %s", parsed.Error.Message)
	}
	out := make([][]float32, 0, len(parsed.Data))
	for _, d := range parsed.Data {
		out = append(out, d.Embedding)
	}
	return out, nil
}

// Cosine returns the cosine similarity of two equal-length vectors.
func Cosine(a, b []float32) float64 {
	if len(a) == 0 || len(a) != len(b) {
		return 0
	}
	var dot, na, nb float64
	for i := range a {
		dot += float64(a[i]) * float64(b[i])
		na += float64(a[i]) * float64(a[i])
		nb += float64(b[i]) * float64(b[i])
	}
	if na == 0 || nb == 0 {
		return 0
	}
	return dot / (math.Sqrt(na) * math.Sqrt(nb))
}
