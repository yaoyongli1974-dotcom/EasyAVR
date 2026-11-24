package audittool

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

func newDB(t *testing.T, path string) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.AutoMigrate(&auditRow{}); err != nil {
		t.Fatal(err)
	}
	return db
}

func TestDryRunDoesNotModifyRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")
	db := newDB(t, path)
	db.Create(&auditRow{RequestBody: `{"password":"hunter2"}`})
	db.Create(&auditRow{RequestBody: `{"path":"/devices","status":200}`})

	rep, err := Run(Options{Driver: "sqlite", DSN: path})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Suspicious != 1 || len(rep.SampleIDs) != 1 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	var body string
	if err := db.Model(&auditRow{}).Where("id = ?", rep.SampleIDs[0]).Pluck("request_body", &body).Error; err != nil {
		t.Fatal(err)
	}
	if body != `{"password":"hunter2"}` {
		t.Fatalf("dry run modified data: %q", body)
	}
}

func TestApplyRedactsOnlySensitiveRowsWithBackup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "audit.db")
	db := newDB(t, path)
	db.Create(&auditRow{RequestBody: `{"password":"hunter2"}`})
	db.Create(&auditRow{RequestBody: `{"apiKey":"abc"}`})
	db.Create(&auditRow{RequestBody: `{"path":"/devices","status":200}`})

	if _, err := Run(Options{Driver: "sqlite", DSN: path, Apply: true, Confirm: true}); err == nil {
		t.Fatal("expected refusal to redact without backup")
	}
	backup := filepath.Join(dir, "backup.db")
	rep, err := Run(Options{Driver: "sqlite", DSN: path, Apply: true, Confirm: true, BackupPath: backup})
	if err != nil {
		t.Fatal(err)
	}
	if rep.Suspicious != 2 || rep.Redacted != 2 {
		t.Fatalf("unexpected report: %+v", rep)
	}
	if info, err := os.Stat(backup); err != nil || info.Size() == 0 {
		t.Fatalf("backup missing or empty: %v", err)
	}
	var bodies []string
	db.Model(&auditRow{}).Order("id").Pluck("request_body", &bodies)
	if len(bodies) != 3 || bodies[0] != "" || bodies[1] != "" || bodies[2] != `{"path":"/devices","status":200}` {
		t.Fatalf("unexpected rows after redaction: %q", bodies)
	}
	rep2, err := Run(Options{Driver: "sqlite", DSN: path})
	if err != nil {
		t.Fatal(err)
	}
	if rep2.Suspicious != 0 {
		t.Fatalf("redaction was not idempotent: %+v", rep2)
	}
}

func TestApplyRequiresConfirm(t *testing.T) {
	path := filepath.Join(t.TempDir(), "audit.db")
	db := newDB(t, path)
	db.Create(&auditRow{RequestBody: `{"password":"hunter2"}`})
	if _, err := Run(Options{Driver: "sqlite", DSN: path, Apply: true}); err == nil {
		t.Fatal("expected refusal to redact without --yes")
	}
}
