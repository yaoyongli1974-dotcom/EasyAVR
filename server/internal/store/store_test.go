package store

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestOpenWithUnsupportedDriver(t *testing.T) {
	if _, err := OpenWith("mysql", "dsn"); err == nil {
		t.Fatal("expected unsupported driver error")
	}
}

func TestOpenSQLiteAppliesPragmas(t *testing.T) {
	db, err := Open(filepath.Join(t.TempDir(), "pragmas.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	var mode string
	if err := db.Raw("PRAGMA journal_mode").Scan(&mode).Error; err != nil {
		t.Fatalf("pragma journal_mode: %v", err)
	}
	if strings.ToLower(mode) != "wal" {
		t.Fatalf("journal_mode = %q, want wal", mode)
	}
	var fk int
	if err := db.Raw("PRAGMA foreign_keys").Scan(&fk).Error; err != nil {
		t.Fatalf("pragma foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Fatalf("foreign_keys = %d, want 1", fk)
	}
}
