package store

import (
	"os"
	"testing"
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
