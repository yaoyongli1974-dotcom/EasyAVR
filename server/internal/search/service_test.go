package search

import (
	"path/filepath"
	"testing"

	"github.com/easyavr/easyavr/internal/ai"
	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/store"
)

func TestCosine(t *testing.T) {
	if s := ai.Cosine([]float32{1, 0}, []float32{1, 0}); s < 0.999 {
		t.Fatalf("identical vectors cosine = %v", s)
	}
	if s := ai.Cosine([]float32{1, 0}, []float32{0, 1}); s != 0 {
		t.Fatalf("orthogonal vectors cosine = %v", s)
	}
	if s := ai.Cosine([]float32{1, 0}, []float32{1, 0, 0}); s != 0 {
		t.Fatalf("mismatched length should be 0, got %v", s)
	}
}

func TestKeywordFallback(t *testing.T) {
	db, err := store.Open(filepath.Join(t.TempDir(), "s.db"))
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	db.Create(&model.AIEvent{Kind: "cv", EventType: "fire", Level: "critical", Summary: "smoke and fire detected"})
	db.Create(&model.AIEvent{Kind: "cv", EventType: "person", Level: "info", Summary: "person walking"})

	svc := NewService(db, false)
	if svc.HasEmbedder() {
		t.Fatal("no embedding provider expected")
	}
	hits := svc.Search("fire", 10)
	if len(hits) != 1 || hits[0].Event.EventType != "fire" {
		t.Fatalf("keyword search failed: %+v", hits)
	}
}
