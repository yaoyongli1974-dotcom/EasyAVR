package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func newTestService(t *testing.T, cfg Config) (*Service, map[string]string) {
	t.Helper()
	dirs := map[string]string{
		"snapshots":  t.TempDir(),
		"data":       t.TempDir(),
		"certs":      t.TempDir(),
		"recordings": t.TempDir(),
	}
	s := New(nil, cfg)
	s.SetDirs(dirs)
	return s, dirs
}

func TestHealthStatfsAndThresholds(t *testing.T) {
	// Derive the thresholds from the actual filesystem usage so the test is
	// independent of the host disk fill level (0 means "use defaults").
	probe := t.TempDir()
	var st syscall.Statfs_t
	if err := syscall.Statfs(probe, &st); err != nil {
		t.Fatalf("statfs: %v", err)
	}
	total := int64(st.Blocks) * int64(st.Bsize)
	used := total - int64(st.Bfree)*int64(st.Bsize)
	base := int(float64(used) / float64(total) * 100)
	if base < 1 {
		base = 1
	}
	cases := []struct {
		name   string
		cfg    Config
		wantLv string
	}{
		{"info", Config{InfoPercent: base, WarnPercent: 101, CriticalPercent: 102}, LevelInfo},
		{"warning", Config{InfoPercent: base, WarnPercent: base, CriticalPercent: 101}, LevelWarning},
		{"critical", Config{InfoPercent: base, WarnPercent: base, CriticalPercent: base}, LevelCritical},
		{"below thresholds", Config{InfoPercent: 101, WarnPercent: 102, CriticalPercent: 103}, ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			s, dirs := newTestService(t, tc.cfg)
			h := s.Health()

			var st syscall.Statfs_t
			if err := syscall.Statfs(dirs["data"], &st); err != nil {
				t.Fatalf("statfs: %v", err)
			}
			bs := int64(st.Bsize)
			wantTotal := int64(st.Blocks) * bs
			wantFree := int64(st.Bavail) * bs
			wantUsed := wantTotal - int64(st.Bfree)*bs
			if h.TotalBytes != wantTotal || h.FreeBytes != wantFree || h.UsedBytes != wantUsed {
				t.Fatalf("capacity = (%d,%d,%d), want (%d,%d,%d)",
					h.TotalBytes, h.UsedBytes, h.FreeBytes, wantTotal, wantUsed, wantFree)
			}
			wantPct := float64(wantUsed) / float64(wantTotal) * 100
			if wantTotal > 0 && (h.UsedPercent < wantPct-0.001 || h.UsedPercent > wantPct+0.001) {
				t.Fatalf("used percent = %.3f, want %.3f", h.UsedPercent, wantPct)
			}
			if h.CheckedAt.IsZero() {
				t.Fatal("CheckedAt is zero")
			}
			if len(h.Dirs) != 4 {
				t.Fatalf("dirs = %d, want 4: %+v", len(h.Dirs), h.Dirs)
			}
			if tc.wantLv == "" {
				if len(h.Alerts) != 0 {
					t.Fatalf("alerts = %+v, want none", h.Alerts)
				}
				return
			}
			if len(h.Alerts) != 1 || h.Alerts[0].Level != tc.wantLv {
				t.Fatalf("alerts = %+v, want one %s alert", h.Alerts, tc.wantLv)
			}
			if h.Alerts[0].Dir != dirs["data"] {
				t.Fatalf("alert dir = %q, want %q", h.Alerts[0].Dir, dirs["data"])
			}
		})
	}
}

func TestHealthDirectoryUsageAndEntryLimit(t *testing.T) {
	root := t.TempDir()
	for i := 0; i < 24; i++ {
		name := filepath.Join(root, "sub", fmt.Sprintf("file%02d", i))
		if i%4 == 0 {
			name = filepath.Join(root, fmt.Sprintf("file%02d", i))
		}
		if err := os.MkdirAll(filepath.Dir(name), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(name, []byte("0123456789"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	other := t.TempDir()
	s := New(nil, Config{MaxWalkEntries: 6})
	s.SetDirs(map[string]string{
		"snapshot":  root, // alias must be canonicalized
		"data":      other,
		"gb35114":   other,
		"recording": other,
	})

	h := s.Health()
	if len(h.Dirs) != 4 {
		t.Fatalf("dirs = %+v, want 4 entries", h.Dirs)
	}
	if h.Dirs[0].Name != "snapshots" || h.Dirs[0].Path != root {
		t.Fatalf("first dir = %+v, want snapshots", h.Dirs[0])
	}
	if h.Dirs[0].Files == 0 || h.Dirs[0].Files > 6 {
		t.Fatalf("files = %d, want 1..6 (entry cap)", h.Dirs[0].Files)
	}
	if h.Dirs[0].Bytes != int64(h.Dirs[0].Files)*10 {
		t.Fatalf("bytes = %d, want %d", h.Dirs[0].Bytes, h.Dirs[0].Files*10)
	}
	if usage := s.Usage(); len(usage) != 4 || usage[0].Files != h.Dirs[0].Files {
		t.Fatalf("Usage() = %+v, want same as Health", usage)
	}

	// A larger cap must see more files than the truncated walk.
	s.SetDirs(map[string]string{"snapshots": root})
	s2 := New(nil, Config{MaxWalkEntries: 1000})
	s2.SetDirs(map[string]string{"snapshots": root, "data": other, "certs": other, "recordings": other})
	h2 := s2.Health()
	if h2.Dirs[0].Files != 24 {
		t.Fatalf("uncapped files = %d, want 24", h2.Dirs[0].Files)
	}
}

func TestHealthBadDirectoryIsSkipped(t *testing.T) {
	s, dirs := newTestService(t, Config{InfoPercent: 101, WarnPercent: 102, CriticalPercent: 103})
	missing := filepath.Join(t.TempDir(), "does-not-exist")
	s.SetDirs(map[string]string{"snapshots": missing})

	h := s.Health()
	if len(h.Dirs) != 3 {
		t.Fatalf("dirs = %+v, want 3 (bad directory skipped)", h.Dirs)
	}
	for _, u := range h.Dirs {
		if u.Path == missing {
			t.Fatalf("bad directory still present: %+v", u)
		}
	}
	if len(h.Alerts) != 1 || h.Alerts[0].Level != LevelInfo || h.Alerts[0].Dir != missing {
		t.Fatalf("alerts = %+v, want one info note about %s", h.Alerts, missing)
	}
	if dirs["data"] == "" {
		t.Fatal("test setup: empty data dir")
	}
	if got := s.Alerts(); len(got) != 1 || got[0].Dir != missing {
		t.Fatalf("Alerts() = %+v, want cached note", got)
	}
}

func TestHealthWithNilDBAndUsageBeforeHealth(t *testing.T) {
	// Defaults from config.go do not exist in the test package directory;
	// lazily initialized Usage/Alerts and Health must all work without a DB.
	s := New(nil, Config{})
	if got := s.Usage(); len(got) != 0 {
		t.Fatalf("Usage() with missing default dirs = %+v, want empty", got)
	}
	if got := s.Alerts(); len(got) == 0 {
		t.Fatal("Alerts() should report skipped missing directories")
	}
	h := s.Health()
	if h.CheckedAt.IsZero() {
		t.Fatal("CheckedAt is zero")
	}
	if len(s.Usage()) != len(h.Dirs) {
		t.Fatal("Usage() must mirror Health().Dirs")
	}
	if len(s.Alerts()) != len(h.Alerts) {
		t.Fatal("Alerts() must mirror Health().Alerts")
	}
}

func TestNoteCleanup(t *testing.T) {
	s, _ := newTestService(t, Config{})
	at := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	s.NoteCleanup(at, "删除 12 个过期目录")
	h := s.Health()
	if !h.LastCleanupAt.Equal(at) {
		t.Fatalf("LastCleanupAt = %v, want %v", h.LastCleanupAt, at)
	}
	if h.LastCleanupResult != "删除 12 个过期目录" {
		t.Fatalf("LastCleanupResult = %q", h.LastCleanupResult)
	}
	s.NoteCleanup(time.Time{}, "")
	h2 := s.Health()
	if !h2.LastCleanupAt.IsZero() || h2.LastCleanupResult != "" {
		t.Fatalf("cleanup not replaced: %+v", h2)
	}
}

func TestRecordingDirFromPlatformSetting(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	sqlDB.SetMaxOpenConns(1)
	if err := db.Exec("CREATE TABLE platform_settings (id integer primary key autoincrement, `key` text, value text)").Error; err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	if err := db.Exec("INSERT INTO platform_settings (`key`, value) VALUES (?, ?)", "recordings_dir", dir).Error; err != nil {
		t.Fatal(err)
	}

	other := t.TempDir()
	s := New(db, Config{InfoPercent: 101, WarnPercent: 102, CriticalPercent: 103})
	s.SetDirs(map[string]string{"snapshots": other, "data": other, "certs": other})
	h := s.Health()
	var found bool
	for _, u := range h.Dirs {
		if u.Name == "recordings" && u.Path == dir {
			found = true
		}
	}
	if !found {
		t.Fatalf("recordings directory not inferred from db: %+v", h.Dirs)
	}

	// An explicit SetDirs value wins over the persisted setting.
	explicit := t.TempDir()
	s.SetDirs(map[string]string{"recordings": explicit})
	h = s.Health()
	for _, u := range h.Dirs {
		if u.Name == "recordings" && u.Path != explicit {
			t.Fatalf("explicit recordings dir overridden: %+v", u)
		}
	}
}
