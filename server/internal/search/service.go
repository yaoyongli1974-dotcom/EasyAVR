// Package search implements the intelligent retrieval layer: it embeds AI
// events into vectors and answers natural-language queries by cosine
// similarity, falling back to keyword search when no embedding provider exists.
package search

import (
	"context"
	"strings"

	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/ai"
	"github.com/easyavr/easyavr/internal/model"
)

// Service provides indexing and semantic search over AI events.
type Service struct {
	db      *gorm.DB
	vectors vectorStore
}

// NewService builds the search service. When pgvector is true and the database
// is PostgreSQL with the vector extension available, embeddings are stored and
// searched with pgvector; otherwise a portable JSON + cosine store is used.
func NewService(db *gorm.DB, pgvector bool) *Service {
	store := vectorStore(&jsonVectorStore{db: db})
	if pgvector {
		if pg := newPGVectorStore(db); pg.Enabled() {
			store = pg
		}
	}
	return &Service{db: db, vectors: store}
}

// VectorBackend reports the active embedding store ("json" or "pgvector").
func (s *Service) VectorBackend() string { return s.vectors.Name() }

// HasEmbedder reports whether an embedding provider is configured.
func (s *Service) HasEmbedder() bool { return s.embedder() != nil }

func (s *Service) embedder() ai.Embedder {
	var p model.AIProvider
	if err := s.db.Where("kind = ? AND enabled = ?", ai.KindEmbedding, true).First(&p).Error; err != nil {
		return nil
	}
	return ai.NewEmbeddingClient(p.Name, p.Endpoint, p.APIKey, p.Model)
}

// IndexEvent computes and stores the embedding for an event. Best-effort: it
// silently no-ops when no embedding provider is configured.
func (s *Service) IndexEvent(ev model.AIEvent) {
	emb := s.embedder()
	if emb == nil || ev.ID == 0 {
		return
	}
	text := eventText(ev)
	if text == "" {
		return
	}
	vecs, err := emb.Embed(context.Background(), []string{text})
	if err != nil || len(vecs) == 0 {
		return
	}
	_ = s.vectors.Index(ev.ID, emb.Model(), vecs[0])
}

// Hit is a search result with its similarity score.
type Hit struct {
	Event model.AIEvent `json:"event"`
	Score float64       `json:"score"`
}

// Search returns the topK events most similar to the query.
func (s *Service) Search(query string, topK int) []Hit {
	if topK <= 0 {
		topK = 10
	}
	emb := s.embedder()
	if emb == nil {
		return s.keyword(query, topK)
	}
	qv, err := emb.Embed(context.Background(), []string{query})
	if err != nil || len(qv) == 0 {
		return s.keyword(query, topK)
	}
	hits, err := s.vectors.Search(qv[0], topK)
	if err != nil {
		return s.keyword(query, topK)
	}
	return hits
}

func (s *Service) keyword(query string, topK int) []Hit {
	var events []model.AIEvent
	s.db.Where("summary LIKE ? OR event_type LIKE ?", "%"+query+"%", "%"+query+"%").
		Order("occurred_at DESC").Limit(topK).Find(&events)
	hits := make([]Hit, 0, len(events))
	for _, e := range events {
		hits = append(hits, Hit{Event: e, Score: 0})
	}
	return hits
}

func eventText(ev model.AIEvent) string {
	parts := []string{ev.EventType, ev.Summary}
	if strings.TrimSpace(ev.Payload) != "" {
		parts = append(parts, ev.Payload)
	}
	return strings.TrimSpace(strings.Join(parts, " "))
}
