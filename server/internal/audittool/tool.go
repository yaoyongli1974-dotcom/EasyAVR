// Package audittool provides the manual, opt-in triage/redaction tool for
// historical AuditLog.RequestBody rows written before TASK-001.
package audittool

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Options controls a single triage/redaction run.
type Options struct {
	Driver     string // sqlite or postgres
	DSN        string // sqlite file path or postgres DSN
	BackupPath string // sqlite: created here; postgres: pre-existing file from pg_dump
	Apply      bool
	Confirm    bool
	Limit      int // max rows to redact in one run, 0 => 500
}

// Report summarizes what the run found and changed.
type Report struct {
	Suspicious int64
	Redacted   int64
	Backup     string
	SampleIDs  []uint
}

var sensitivePatterns = []string{
	"%password%", "%passwd%", "%secret%", "%apikey%", "%api_key%",
	"%token%", "%authorization%", "%accesskey%", "%access_key%",
	"%privatekey%", "%credential%",
}

func open(opts Options) (*gorm.DB, error) {
	cfg := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
	switch strings.ToLower(strings.TrimSpace(opts.Driver)) {
	case "", "sqlite", "sqlite3":
		return gorm.Open(sqlite.Open(opts.DSN), cfg)
	case "postgres", "postgresql", "pgx":
		return gorm.Open(postgres.Open(opts.DSN), cfg)
	default:
		return nil, fmt.Errorf("unsupported driver %q", opts.Driver)
	}
}

func where() (string, []any) {
	clauses := make([]string, 0, len(sensitivePatterns))
	args := make([]any, 0, len(sensitivePatterns))
	for _, p := range sensitivePatterns {
		clauses = append(clauses, "LOWER(COALESCE(request_body, '')) LIKE ?")
		args = append(args, p)
	}
	return strings.Join(clauses, " OR "), args
}

// Run scans matching audit rows. It never writes unless Apply and Confirm are
// both true, and it refuses to apply without a verified backup.
func Run(opts Options) (*Report, error) {
	if strings.TrimSpace(opts.DSN) == "" {
		return nil, errors.New("DSN/path is required")
	}
	db, err := open(opts)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		return nil, err
	}
	defer sqlDB.Close()

	if !db.Migrator().HasTable(&auditRow{}) {
		return nil, errors.New("audit_logs table not found; nothing to do")
	}
	cond, args := where()
	rep := &Report{}
	if err := db.Model(&auditRow{}).Where(cond, args...).Count(&rep.Suspicious).Error; err != nil {
		return nil, fmt.Errorf("count suspicious rows: %w", err)
	}
	if err := db.Model(&auditRow{}).Where(cond, args...).Order("id DESC").Limit(5).
		Pluck("id", &rep.SampleIDs).Error; err != nil {
		return nil, fmt.Errorf("sample suspicious rows: %w", err)
	}
	if !opts.Apply {
		return rep, nil
	}
	if !opts.Confirm {
		return nil, errors.New("refusing to redact without --yes")
	}
	backup, err := ensureBackup(db, opts)
	if err != nil {
		return nil, err
	}
	rep.Backup = backup

	limit := opts.Limit
	if limit <= 0 {
		limit = 500
	}
	tx := db.Begin()
	if tx.Error != nil {
		return nil, tx.Error
	}
	var ids []uint
	if err := tx.Model(&auditRow{}).Where(cond, args...).Order("id DESC").Limit(limit).
		Pluck("id", &ids).Error; err != nil {
		tx.Rollback()
		return nil, fmt.Errorf("select rows to redact: %w", err)
	}
	for _, id := range ids {
		res := tx.Model(&auditRow{}).Where("id = ? AND ("+cond+")", append([]any{id}, args...)...).
			Update("request_body", "")
		if res.Error != nil {
			tx.Rollback()
			return nil, fmt.Errorf("redact row %d: %w", id, res.Error)
		}
		rep.Redacted += res.RowsAffected
	}
	if err := tx.Commit().Error; err != nil {
		return nil, fmt.Errorf("commit redaction: %w", err)
	}
	return rep, nil
}

type auditRow struct {
	ID          uint   `gorm:"column:id;primaryKey"`
	RequestBody string `gorm:"column:request_body"`
}

func (auditRow) TableName() string { return "audit_logs" }

func ensureBackup(db *gorm.DB, opts Options) (string, error) {
	path := strings.TrimSpace(opts.BackupPath)
	if path == "" {
		return "", errors.New("refusing to redact without --backup (SQLite: target path; PostgreSQL: existing pg_dump file)")
	}
	if strings.Contains(strings.ToLower(opts.Driver), "postgres") {
		info, err := os.Stat(path)
		if err != nil || info.Size() == 0 {
			return "", fmt.Errorf("backup file %q must exist and be non-empty (create it with pg_dump first)", path)
		}
		return path, nil
	}
	if _, err := os.Stat(path); err == nil {
		return "", fmt.Errorf("backup path %q already exists; refusing to overwrite", path)
	}
	if err := db.Exec("VACUUM INTO ?", path).Error; err != nil {
		return "", fmt.Errorf("create sqlite backup: %w", err)
	}
	return path, nil
}
