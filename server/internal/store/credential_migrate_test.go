package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/security"
)

func TestCredentialMigrationEncryptsLegacyRows(t *testing.T) {
	e, err := security.New("migration-key", "v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	security.SetDefault(e)
	t.Cleanup(func() { security.SetDefault(nil) })

	db, err := Open(filepath.Join(t.TempDir(), "migrate.db"))
	if err != nil {
		t.Fatal(err)
	}
	// Bypass model hooks to simulate rows written by an older version.
	for _, row := range []struct {
		table, column, value string
	}{
		{tableName(&model.Device{}), "password", "legacy-camera-pass"},
		{tableName(&model.AIProvider{}), "api_key", "legacy-api-key"},
	} {
		if err := db.Exec("INSERT INTO "+row.table+" ("+row.column+") VALUES (?)", row.value).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := migrateCredentials(db); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		table, column, plain string
	}{
		{tableName(&model.Device{}), "password", "legacy-camera-pass"},
		{tableName(&model.AIProvider{}), "api_key", "legacy-api-key"},
	} {
		var raw string
		if err := db.Table(tc.table).Pluck(tc.column, &raw).Error; err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(raw, "enc:v1:") || strings.Contains(raw, tc.plain) {
			t.Fatalf("%s.%s not encrypted: %q", tc.table, tc.column, raw)
		}
		plain, err := e.Decrypt(raw)
		if err != nil || plain != tc.plain {
			t.Fatalf("%s.%s decrypt: %q, %v", tc.table, tc.column, plain, err)
		}
	}
	// Re-running must be a no-op.
	var before string
	db.Table(tableName(&model.Device{})).Pluck("password", &before)
	if err := migrateCredentials(db); err != nil {
		t.Fatal(err)
	}
	var after string
	db.Table(tableName(&model.Device{})).Pluck("password", &after)
	if before != after {
		t.Fatal("migration was not idempotent")
	}
}

func TestCredentialMigrationRotatesOldKeyID(t *testing.T) {
	old, _ := security.New("old-key", "v1", nil)
	cipher, err := old.Encrypt("rotate-me")
	if err != nil {
		t.Fatal(err)
	}
	current, err := security.New("current-key", "v2", []string{"v1:old-key"})
	if err != nil {
		t.Fatal(err)
	}
	security.SetDefault(current)
	t.Cleanup(func() { security.SetDefault(nil) })

	db, err := Open(filepath.Join(t.TempDir(), "rotate.db"))
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO devices (password) VALUES (?)", cipher).Error; err != nil {
		t.Fatal(err)
	}
	if err := migrateCredentials(db); err != nil {
		t.Fatal(err)
	}
	var raw string
	db.Table(tableName(&model.Device{})).Pluck("password", &raw)
	if !strings.HasPrefix(raw, "enc:v2:") {
		t.Fatalf("row was not re-encrypted: %q", raw)
	}
	plain, err := current.Decrypt(raw)
	if err != nil || plain != "rotate-me" {
		t.Fatalf("rotated decrypt: %q, %v", plain, err)
	}
}

// TestCredentialMigrationOnCopiedDatabase is an opt-in dry run against a COPY
// of a production/development database: EASYAVR_TEST_MIGRATION_DB=<path>.
// It verifies every credential column ends up encrypted and decryptable.
func TestCredentialMigrationOnCopiedDatabase(t *testing.T) {
	path := os.Getenv("EASYAVR_TEST_MIGRATION_DB")
	if path == "" {
		t.Skip("EASYAVR_TEST_MIGRATION_DB not set")
	}
	e, err := security.New("copy-migration-key", "v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	security.SetDefault(e)
	t.Cleanup(func() { security.SetDefault(nil) })
	db, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, spec := range credentialColumns {
		table := tableName(spec.model)
		for _, column := range spec.columns {
			var rows []map[string]any
			if err := db.Table(table).Select("id, " + column).Find(&rows).Error; err != nil {
				t.Fatal(err)
			}
			for _, row := range rows {
				value := valueString(row[column])
				if value == "" {
					continue
				}
				if _, err := e.Decrypt(value); err != nil {
					t.Fatalf("%s.%s row %v is not decryptable: %v", table, column, row["id"], err)
				}
			}
		}
	}
}
