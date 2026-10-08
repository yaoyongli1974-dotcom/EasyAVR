package search

import (
	"encoding/json"
	"errors"
	"sort"
	"strconv"
	"strings"

	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/ai"
	"github.com/easyavr/easyavr/internal/model"
)

// vectorStore abstracts where event embeddings live and how similarity search
// is executed. The default JSON store is portable (SQLite); the pgvector store
// is used automatically on PostgreSQL when the extension is available.
type vectorStore interface {
	Name() string
	Enabled() bool
	Index(eventID uint, modelName string, vec []float32) error
	Search(qv []float32, topK int) ([]Hit, error)
}

// ---- portable JSON store (AIEvent.embedding column + in-process cosine) ----

type jsonVectorStore struct{ db *gorm.DB }

func (s *jsonVectorStore) Name() string  { return "json" }
func (s *jsonVectorStore) Enabled() bool { return true }

func (s *jsonVectorStore) Index(eventID uint, _ string, vec []float32) error {
	b, err := json.Marshal(vec)
	if err != nil {
		return err
	}
	return s.db.Model(&model.AIEvent{}).Where("id = ?", eventID).Update("embedding", string(b)).Error
}

func (s *jsonVectorStore) Search(qv []float32, topK int) ([]Hit, error) {
	var events []model.AIEvent
	s.db.Where("embedding IS NOT NULL AND embedding <> ''").Find(&events)
	hits := make([]Hit, 0, len(events))
	for _, e := range events {
		var v []float32
		if json.Unmarshal([]byte(e.Embedding), &v) != nil {
			continue
		}
		hits = append(hits, Hit{Event: e, Score: ai.Cosine(qv, v)})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].Score > hits[j].Score })
	if len(hits) > topK {
		hits = hits[:topK]
	}
	return hits, nil
}

// ---- PostgreSQL pgvector store ----

type pgVectorStore struct {
	db      *gorm.DB
	enabled bool
}

// newPGVectorStore initializes the extension and side table; it stays disabled
// (caller falls back to JSON) when the extension is not installed.
func newPGVectorStore(db *gorm.DB) *pgVectorStore {
	s := &pgVectorStore{db: db}
	if db.Dialector.Name() != "postgres" {
		return s
	}
	if err := db.Exec("CREATE EXTENSION IF NOT EXISTS vector").Error; err != nil {
		return s
	}
	ddl := `CREATE TABLE IF NOT EXISTS event_vectors (
		event_id bigint PRIMARY KEY,
		model text,
		embedding vector,
		updated_at timestamptz NOT NULL DEFAULT now()
	)`
	if err := db.Exec(ddl).Error; err != nil {
		return s
	}
	// Best-effort ANN index (HNSW needs a fixed dimension, so skip for the
	// unbounded vector type). Exact scan via <=> is used instead.
	s.enabled = true
	return s
}

func (s *pgVectorStore) Name() string  { return "pgvector" }
func (s *pgVectorStore) Enabled() bool { return s.enabled }

func (s *pgVectorStore) Index(eventID uint, modelName string, vec []float32) error {
	if !s.enabled {
		return errors.New("pgvector disabled")
	}
	return s.db.Exec(`INSERT INTO event_vectors (event_id, model, embedding, updated_at)
		VALUES (?, ?, ?::vector, now())
		ON CONFLICT (event_id) DO UPDATE SET embedding = EXCLUDED.embedding, model = EXCLUDED.model, updated_at = now()`,
		eventID, modelName, formatVector(vec)).Error
}

func (s *pgVectorStore) Search(qv []float32, topK int) ([]Hit, error) {
	if !s.enabled {
		return nil, errors.New("pgvector disabled")
	}
	lit := formatVector(qv)
	var rows []struct {
		EventID uint
		Score   float64
	}
	err := s.db.Raw(`SELECT event_id, 1 - (embedding <=> ?::vector) AS score
		FROM event_vectors WHERE embedding IS NOT NULL
		ORDER BY embedding <=> ?::vector LIMIT ?`, lit, lit, topK).Scan(&rows).Error
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, nil
	}
	order := make(map[uint]int, len(rows))
	ids := make([]uint, 0, len(rows))
	score := make(map[uint]float64, len(rows))
	for i, r := range rows {
		order[r.EventID] = i
		ids = append(ids, r.EventID)
		score[r.EventID] = r.Score
	}
	var events []model.AIEvent
	s.db.Where("id IN ?", ids).Find(&events)
	sort.Slice(events, func(i, j int) bool { return order[events[i].ID] < order[events[j].ID] })
	hits := make([]Hit, 0, len(events))
	for _, e := range events {
		hits = append(hits, Hit{Event: e, Score: score[e.ID]})
	}
	return hits, nil
}

// formatVector renders a float slice as a pgvector literal: [1,2,3].
func formatVector(vec []float32) string {
	var b strings.Builder
	b.WriteByte('[')
	for i, f := range vec {
		if i > 0 {
			b.WriteByte(',')
		}
		b.WriteString(strconv.FormatFloat(float64(f), 'f', -1, 32))
	}
	b.WriteByte(']')
	return b.String()
}
