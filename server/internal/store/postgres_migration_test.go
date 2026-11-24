package store

import (
	"os"
	"strings"
	"testing"

	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/security"
)

// TestPostgresMigration runs only when EASYAVR_TEST_PG_DSN points at a
// disposable PostgreSQL database. It verifies AutoMigrate on a real PG server
// and that re-running migrations is safe.
func TestPostgresMigration(t *testing.T) {
	dsn := os.Getenv("EASYAVR_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("EASYAVR_TEST_PG_DSN not set; PostgreSQL migration verification skipped")
	}
	db, err := OpenWith("postgres", dsn)
	if err != nil {
		t.Fatalf("migrate postgres: %v", err)
	}
	if !db.Migrator().HasTable("audit_logs") {
		t.Fatal("audit_logs table missing after AutoMigrate")
	}
	for _, column := range []string{"id", "request_body", "user_id", "username", "ip", "method", "path", "action", "resource", "resource_id", "result", "error_msg", "latency_ms"} {
		if !db.Migrator().HasColumn("audit_logs", column) {
			t.Errorf("audit_logs.%s missing after AutoMigrate", column)
		}
	}
	if _, err := OpenWith("postgres", dsn); err != nil {
		t.Fatalf("re-running migrations on an existing schema: %v", err)
	}
}

// TestPostgresCredentialEncryption verifies that credential fields are stored
// as ciphertext on a real PostgreSQL server, including legacy-row migration.
func TestPostgresCredentialEncryption(t *testing.T) {
	dsn := os.Getenv("EASYAVR_TEST_PG_DSN")
	if dsn == "" {
		t.Skip("EASYAVR_TEST_PG_DSN not set; PostgreSQL credential encryption verification skipped")
	}
	e, err := security.New("pg-credential-key", "v1", nil)
	if err != nil {
		t.Fatal(err)
	}
	security.SetDefault(e)
	t.Cleanup(func() { security.SetDefault(nil) })
	db, err := OpenWith("postgres", dsn)
	if err != nil {
		t.Fatal(err)
	}
	dev := model.Device{Name: "pg-cam", Password: "pg-camera-secret"}
	if err := db.Create(&dev).Error; err != nil {
		t.Fatal(err)
	}
	var raw string
	if err := db.Table(tableName(&model.Device{})).Where("id = ?", dev.ID).
		Pluck("password", &raw).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(raw, "enc:v1:") || strings.Contains(raw, "pg-camera-secret") {
		t.Fatalf("device password stored in plaintext on PG: %q", raw)
	}
	var read model.Device
	if err := db.First(&read, dev.ID).Error; err != nil || read.Password != "pg-camera-secret" {
		t.Fatalf("PG decrypt on read: %q, %v", read.Password, err)
	}
	// Legacy plaintext row bypassing hooks must be migrated on reopen.
	if err := db.Table(tableName(&model.Device{})).
		Create(map[string]any{"name": "legacy", "password": "legacy-pg-secret"}).Error; err != nil {
		t.Fatal(err)
	}
	if _, err := OpenWith("postgres", dsn); err != nil {
		t.Fatal(err)
	}
	var legacy string
	if err := db.Table(tableName(&model.Device{})).Where("name = ?", "legacy").
		Pluck("password", &legacy).Error; err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(legacy, "enc:v1:") || strings.Contains(legacy, "legacy-pg-secret") {
		t.Fatalf("legacy PG password not migrated: %q", legacy)
	}
}
