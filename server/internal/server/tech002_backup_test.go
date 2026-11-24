package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// Backup/restore regression coverage. All shell drills run against temporary
// directories only. No production database, DSN or credential is ever read or
// written: the PostgreSQL live-restore case is explicitly skipped.

func tech002RepoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate test source")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", "..", ".."))
}

func tech002Script(t *testing.T, name string) string {
	t.Helper()
	p := filepath.Join(tech002RepoRoot(t), "deploy", "scripts", name)
	if _, err := os.Stat(p); err != nil {
		t.Fatalf("script %s missing: %v", name, err)
	}
	return p
}

func tech002RequireTools(t *testing.T) {
	t.Helper()
	for _, tool := range []string{"bash", "python3", "gzip", "flock", "date", "find"} {
		if _, err := exec.LookPath(tool); err != nil {
			t.Skipf("required tool %q unavailable: %v", tool, err)
		}
	}
}

func tech002CreateDB(t *testing.T, path, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("CREATE TABLE reg_rows (id INTEGER PRIMARY KEY, val TEXT)").Error; err != nil {
		t.Fatal(err)
	}
	if err := db.Exec("INSERT INTO reg_rows(val) VALUES (?)", value).Error; err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	if err := sqlDB.Close(); err != nil {
		t.Fatal(err)
	}
}

func tech002DBValue(t *testing.T, path string) string {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(path), &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		sqlDB, _ := db.DB()
		if sqlDB != nil {
			_ = sqlDB.Close()
		}
	}()
	var val string
	if err := db.Table("reg_rows").Pluck("val", &val).Error; err != nil {
		t.Fatal(err)
	}
	return val
}

func tech002Gunzip(t *testing.T, src, dst string) {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	zr, err := gzip.NewReader(in)
	if err != nil {
		t.Fatal(err)
	}
	defer zr.Close()
	data, err := io.ReadAll(zr)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func tech002GlobOne(t *testing.T, pattern string) string {
	t.Helper()
	matches, err := filepath.Glob(pattern)
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 1 {
		t.Fatalf("expected exactly one match for %q, got %d: %v", pattern, len(matches), matches)
	}
	return matches[0]
}

func tech002Read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestTech002RegressionSQLiteBackupRestore(t *testing.T) {
	tech002RequireTools(t)
	backup := tech002Script(t, "backup.sh")
	restore := tech002Script(t, "restore.sh")

	tmp := t.TempDir()
	dbPath := filepath.Join(tmp, "source", "easyavr.db")
	const payload = "SQLITE-REGRESSION-PAYLOAD"
	tech002CreateDB(t, dbPath, payload)

	envPath := filepath.Join(tmp, "env")
	const (
		masterKey = "MASTER-KEY-DO-NOT-BACKUP-123"
		jwtSecret = "JWT-SECRET-DO-NOT-BACKUP-456"
		adminPass = "ADMIN-PASS-DO-NOT-BACKUP-789"
	)
	envText := strings.Join([]string{
		"EASYAVR_ENV=production",
		"EASYAVR_LISTEN=:18000",
		"EASYAVR_ALLOW_ORIGIN=https://example.com",
		"EASYAVR_CREDENTIAL_KEY=" + masterKey,
		"EASYAVR_JWT_SECRET=" + jwtSecret,
		"EASYAVR_ADMIN_PASS=" + adminPass,
		"UNKNOWN_SECRET=should-never-be-backed-up",
		"",
	}, "\n")
	if err := os.WriteFile(envPath, []byte(envText), 0o600); err != nil {
		t.Fatal(err)
	}

	backupDir := filepath.Join(tmp, "backups")
	invokeBackup := func(keep string) string {
		cmd := exec.Command("bash", backup)
		cmd.Env = []string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + os.Getenv("HOME"),
			"LC_ALL=C",
			"DB=" + dbPath,
			"BACKUP_DIR=" + backupDir,
			"ENV_FILE=" + envPath,
			"KEEP=" + keep,
		}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("backup.sh failed: %v\n%s", err, out)
		}
		return string(out)
	}

	invokeBackup("2")
	dbGz := tech002GlobOne(t, filepath.Join(backupDir, "db", "easyavr-*.db.gz"))
	cfgGz := tech002GlobOne(t, filepath.Join(backupDir, "config", "config-*.gz"))

	// No whole-env dump, and no secret value may appear in any backup artifact.
	for _, pattern := range []string{
		filepath.Join(backupDir, "**", "env-*.gz"),
		filepath.Join(backupDir, "db", "env-*.gz"),
		filepath.Join(backupDir, "config", "env-*.gz"),
	} {
		matches, _ := filepath.Glob(pattern)
		if len(matches) != 0 {
			t.Fatalf("backup produced a whole-env artifact: %v", matches)
		}
	}
	cfgPlain := filepath.Join(tmp, "config-plain")
	tech002Gunzip(t, cfgGz, cfgPlain)
	cfgContent := tech002Read(t, cfgPlain)
	if !strings.Contains(cfgContent, "EASYAVR_ENV=production") {
		t.Fatalf("config backup lost whitelisted keys: %q", cfgContent)
	}
	for _, secret := range []string{masterKey, jwtSecret, adminPass, "should-never-be-backed-up"} {
		if strings.Contains(cfgContent, secret) {
			t.Fatalf("config backup leaked a secret: %q", secret)
		}
	}
	dbPlain := filepath.Join(tmp, "db-plain.db")
	tech002Gunzip(t, dbGz, dbPlain)
	if got := tech002DBValue(t, dbPlain); got != payload {
		t.Fatalf("backed-up row = %q, want %q", got, payload)
	}

	// Rotation: KEEP=1 leaves a single published DB backup.
	time.Sleep(1100 * time.Millisecond)
	invokeBackup("1")
	remaining, err := filepath.Glob(filepath.Join(backupDir, "db", "easyavr-*.db.gz"))
	if err != nil {
		t.Fatal(err)
	}
	if len(remaining) != 1 {
		t.Fatalf("KEEP=1 left %d db backups: %v", len(remaining), remaining)
	}
	dbGz = remaining[0]

	// Restore dry-run must not touch the existing target.
	target := filepath.Join(tmp, "restore", "target.db")
	const oldValue = "OLD-TARGET-VALUE"
	tech002CreateDB(t, target, oldValue)
	invoke := func(args ...string) (string, error) {
		cmd := exec.Command("bash", append([]string{restore}, args...)...)
		cmd.Env = []string{
			"PATH=" + os.Getenv("PATH"),
			"HOME=" + os.Getenv("HOME"),
			"LC_ALL=C",
		}
		out, err := cmd.CombinedOutput()
		if err == nil && !strings.Contains(string(out), "dry-run: no changes made") && !strings.Contains(string(out), "done: restore verified") {
			t.Fatalf("unexpected restore output: %s", out)
		}
		return string(out), err
	}
	if _, err := invoke("-f", dbGz, "-d", target, "--dry-run"); err != nil {
		t.Fatalf("restore dry-run failed: %v", err)
	}
	if got := tech002DBValue(t, target); got != oldValue {
		t.Fatalf("dry-run modified target: %q", got)
	}
	if _, err := invoke("-f", dbGz, "-d", target, "--yes"); err != nil {
		t.Fatalf("restore --yes failed: %v", err)
	}
	if got := tech002DBValue(t, target); got != payload {
		t.Fatalf("restored row = %q, want %q", got, payload)
	}

	// A corrupt archive is rejected without changing the target.
	corrupt := filepath.Join(tmp, "corrupt.db.gz")
	if err := os.WriteFile(corrupt, []byte("not a gzip archive"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := invoke("-f", corrupt, "-d", target, "--yes"); err == nil {
		t.Fatal("restore accepted a corrupt backup")
	}
	if got := tech002DBValue(t, target); got != payload {
		t.Fatalf("corrupt restore modified target: %q", got)
	}

	// Neither script may generate or persist a master key or rewrite env.
	if got := tech002Read(t, envPath); got != envText {
		t.Fatal("backup/restore modified the env file")
	}
	for _, script := range []string{backup, restore} {
		body := tech002Read(t, script)
		tech002AssertNoKeyGeneration(t, filepath.Base(script), body)
		if strings.Contains(body, "/etc/easyavr/env") && strings.Contains(body, ">") {
			// Comments mention the path; an actual write would use redirection.
			for _, line := range strings.Split(body, "\n") {
				trimmed := strings.TrimSpace(line)
				if strings.HasPrefix(trimmed, "#") {
					continue
				}
				if strings.Contains(trimmed, "/etc/easyavr/env") && (strings.Contains(trimmed, ">") || strings.Contains(trimmed, "tee")) {
					t.Fatalf("%s writes to the env file: %s", filepath.Base(script), trimmed)
				}
			}
		}
	}
}

func tech002AssertNoKeyGeneration(t *testing.T, name, body string) {
	t.Helper()
	for _, forbidden := range []string{"openssl rand", "dd if=/dev/urandom", "dd if=/dev/random"} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("%s generates a key via %q", name, forbidden)
		}
	}
	for _, line := range strings.Split(body, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			continue
		}
		if strings.HasPrefix(trimmed, "EASYAVR_CREDENTIAL_KEY=") ||
			strings.HasPrefix(trimmed, "export EASYAVR_CREDENTIAL_KEY=") {
			t.Fatalf("%s assigns the master key: %s", name, trimmed)
		}
	}
}

func TestTech002RegressionPostgreSQLBackupRestore(t *testing.T) {
	t.Run("static script guards", func(t *testing.T) {
		pgBackup := tech002Read(t, tech002Script(t, "pg_backup.sh"))
		pgRestore := tech002Read(t, tech002Script(t, "pg_restore.sh"))
		for _, needle := range []string{"pg_dump -Fc", "pg_restore -l", "PG_KEEP", "DSN redacted"} {
			if !strings.Contains(pgBackup, needle) {
				t.Fatalf("pg_backup.sh missing guard %q", needle)
			}
		}
		for _, needle := range []string{"pg_restore -j", "--clean --if-exists", "<dsn-redacted>", "dry-run"} {
			if !strings.Contains(pgRestore, needle) {
				t.Fatalf("pg_restore.sh missing guard %q", needle)
			}
		}
		tech002AssertNoKeyGeneration(t, "pg_backup.sh", pgBackup)
		tech002AssertNoKeyGeneration(t, "pg_restore.sh", pgRestore)
	})

	t.Run("live restore", func(t *testing.T) {
		t.Skip("SKIP: no writable PostgreSQL target is available; the local easyavr_ro role is SELECT-only and this suite must not connect to or restore the business database")
	})

	t.Run("optional readonly probe", func(t *testing.T) {
		dsn := os.Getenv("EASYAVR_TEST_PG_DSN")
		if dsn == "" {
			t.Skip("SKIP: EASYAVR_TEST_PG_DSN not set; no database connection attempted by default")
		}
		if _, err := exec.LookPath("psql"); err != nil {
			t.Skipf("SKIP: psql unavailable: %v", err)
		}
		cmd := exec.Command("psql", dsn, "-Atc", "SELECT 1")
		cmd.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + os.Getenv("HOME"), "LC_ALL=C"}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("readonly probe failed (DSN redacted): %v", err)
		}
		if !bytes.Equal(bytes.TrimSpace(out), []byte("1")) {
			t.Fatalf("unexpected readonly probe result: %q", out)
		}
	})
}
