package store

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/glebarez/sqlite"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"

	"github.com/easyavr/easyavr/internal/model"
)

// Open opens a SQLite database at path with production pragmas (WAL,
// busy_timeout, foreign_keys) and runs migrations. It is the default and
// requires no external services.
func Open(path string) (*gorm.DB, error) {
	return OpenWith("sqlite", path)
}

// OpenWith opens a database for the given driver ("sqlite" or "postgres") and
// DSN, runs migrations and returns the handle. For sqlite the DSN is a file
// path; for postgres it is a connection string.
func OpenWith(driver, dsn string) (*gorm.DB, error) {
	var (
		db  *gorm.DB
		err error
	)
	switch strings.ToLower(strings.TrimSpace(driver)) {
	case "", "sqlite", "sqlite3":
		db, err = openSQLite(dsn)
	case "postgres", "postgresql", "pgx":
		db, err = openPostgres(dsn)
	default:
		return nil, fmt.Errorf("unsupported database driver %q (use sqlite or postgres)", driver)
	}
	if err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		return nil, err
	}
	return db, nil
}

func openSQLite(path string) (*gorm.DB, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." && !strings.HasPrefix(path, "file:") {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
	}
	return gorm.Open(sqlite.Open(sqliteDSN(path)), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
}

// sqliteDSN appends the pragmas used in production: WAL journaling for
// concurrent readers, a busy timeout to avoid "database is locked", and
// enforced foreign keys.
func sqliteDSN(path string) string {
	pragmas := "_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)"
	if strings.Contains(path, "?") {
		return path + "&" + pragmas
	}
	if strings.HasPrefix(path, "file:") {
		return path + "?" + pragmas
	}
	if path == ":memory:" {
		return "file::memory:?cache=shared&" + pragmas
	}
	return "file:" + path + "?" + pragmas
}

func openPostgres(dsn string) (*gorm.DB, error) {
	if dsn == "" {
		return nil, fmt.Errorf("postgres DSN is empty (set EASYAVR_DB_DSN)")
	}
	return gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
}

// migrate runs the automatic schema migration for all models.
func migrate(db *gorm.DB) error {
	return db.AutoMigrate(
		&model.User{},
		&model.Role{},
		&model.Device{},
		&model.Channel{},
		&model.VideoResource{},
		&model.AIProvider{},
		&model.AITask{},
		&model.AIEvent{},
		&model.Recording{},
		&model.RecordingPlan{},
		&model.Snapshot{},
		&model.GBDevice{},
		&model.NotificationChannel{},
		&model.NotificationRule{},
		&model.ClusterNode{},
		&model.GBCascade{},
		&model.GBWhiteList{},
		&model.GA1400Cascade{},
		&model.GA1400Subscription{},
		&model.GB35114Cert{},
		&model.APIKey{},
		&model.APIRequestLog{},
		&model.DeviceGroup{},
		&model.DeviceGroupDevice{},
		&model.ChannelGroupChannel{},
		&model.UserGroup{},
		&model.Track{},
		&model.AuditLog{},
	)
}

// Seed creates the initial admin account and built-in roles when empty.
func Seed(db *gorm.DB, username, password string) error {
	if err := seedRoles(db); err != nil {
		return err
	}
	var count int64
	if err := db.Model(&model.User{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	hash, err := HashPassword(password)
	if err != nil {
		return err
	}
	admin := model.User{
		Username:     username,
		PasswordHash: hash,
		Nickname:     "Administrator",
		Role:         "admin",
		Enabled:      true,
	}
	if err := db.Create(&admin).Error; err != nil {
		return err
	}
	log.Printf("[seed] created default admin user %q", username)
	return nil
}

// seedRoles creates the built-in roles when the role table is empty.
func seedRoles(db *gorm.DB) error {
	var count int64
	if err := db.Model(&model.Role{}).Count(&count).Error; err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	roles := []model.Role{
		{Name: "admin", Description: "系统管理员，拥有全部权限", Permissions: "*", Builtin: true},
		{Name: "operator", Description: "运维人员，可管理设备/视频/AI，不能管理用户与平台配置",
			Permissions: "device,video,recording,snapshot,ai,event,search", Builtin: true},
		{Name: "viewer", Description: "访客，仅可观看视频与查看事件",
			Permissions: "video,event,search", Builtin: true},
	}
	if err := db.Create(&roles).Error; err != nil {
		return err
	}
	log.Printf("[seed] created %d built-in roles", len(roles))
	return nil
}
