package store

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/easyavr/easyavr/internal/security"
)

func TestGBPasswordPersistsEncryptedAcrossRestart(t *testing.T) {
	e, err := security.New("gb-password-key", "v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	security.SetDefault(e)
	t.Cleanup(func() { security.SetDefault(nil) })

	path := filepath.Join(t.TempDir(), "gb.db")
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, found, err := PersistedGBPassword(db); err != nil || found {
		t.Fatalf("unexpected initial state: found=%v err=%v", found, err)
	}
	if err := SaveGBPassword(db, "RotatedSipPass123"); err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := db.Table("platform_settings").Where("key = ?", GBPasswordSettingKey).
		Pluck("value", &raw).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "enc:v1:") || strings.Contains(raw, "RotatedSipPass123") {
		t.Fatalf("GB password stored in plaintext: %q", raw)
	}
	// Simulate a process restart with a fresh DB handle.
	db2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	got, found, err := PersistedGBPassword(db2)
	if err != nil || !found || got != "RotatedSipPass123" {
		t.Fatalf("restart load: found=%v password=%q err=%v", found, got, err)
	}
	if err := SaveGBPassword(db2, "SecondRotatedPass"); err != nil {
		t.Fatal(err)
	}
	got, _, err = PersistedGBPassword(db2)
	if err != nil || got != "SecondRotatedPass" {
		t.Fatalf("second rotation: %q, %v", got, err)
	}
}
