package search

import (
	"os"
	"testing"

	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/store"
)

// TestPGVectorStore runs against a real PostgreSQL with the pgvector extension.
// Enable with:
//
//	EASYAVR_TEST_PG_DSN='postgres://easyavr:easyavr@127.0.0.1:5432/easyavr?sslmode=disable' \
//	go test ./internal/search -run TestPGVectorStore -v
func TestPGVectorStore(t *testing.T) {
	dsn := os.Getenv("EASYAVR_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("set EASYAVR_TEST_PG_DSN to run the pgvector integration test")
	}
	db, err := store.OpenWith("postgres", dsn)
	if err != nil {
		t.Fatalf("db: %v", err)
	}
	vs := newPGVectorStore(db)
	if !vs.Enabled() {
		t.Fatal("pgvector not enabled (is the vector extension installed?)")
	}
	if err := db.Exec("DELETE FROM event_vectors").Error; err != nil {
		t.Fatalf("clean: %v", err)
	}

	e1 := model.AIEvent{Kind: "cv", EventType: "fire", Level: "critical", Summary: "smoke and fire"}
	e2 := model.AIEvent{Kind: "cv", EventType: "person", Level: "info", Summary: "person walking"}
	db.Create(&e1)
	db.Create(&e2)

	if err := vs.Index(e1.ID, "m", []float32{1, 0, 0}); err != nil {
		t.Fatalf("index e1: %v", err)
	}
	if err := vs.Index(e2.ID, "m", []float32{0, 1, 0}); err != nil {
		t.Fatalf("index e2: %v", err)
	}

	hits, err := vs.Search([]float32{0.9, 0.1, 0}, 1)
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(hits) != 1 || hits[0].Event.EventType != "fire" {
		t.Fatalf("unexpected hits: %+v", hits)
	}
}
