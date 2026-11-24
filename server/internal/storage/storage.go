// Package storage reports disk capacity and the size of the directories that
// hold platform data (snapshots, database, GB35114 certificates, recordings).
// It is pure Go: on Linux the capacity comes from syscall.Statfs, with no CGO
// dependency. A broken or unreadable directory is skipped instead of failing
// the whole health check.
package storage

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"gorm.io/gorm"
)

// Alert levels used in Alert.Level.
const (
	LevelInfo     = "info"
	LevelWarning  = "warning"
	LevelCritical = "critical"
)

// Config controls the capacity thresholds (percent) and the maximum number of
// entries visited in one directory walk. Zero values select the documented
// defaults; MaxWalkEntries is bounded so a huge snapshot directory cannot stall
// a health check.
type Config struct {
	InfoPercent     int
	WarnPercent     int
	CriticalPercent int
	MaxWalkEntries  int
}

// DirUsage is the disk usage of one monitored directory.
type DirUsage struct {
	Name  string
	Path  string
	Bytes int64
	Files int
}

// Alert is a capacity threshold crossing or a brief note about a directory
// that had to be skipped.
type Alert struct {
	Level       string
	Dir         string
	UsedPercent float64
	Message     string
}

// Health is a point-in-time view of disk capacity, directory sizes, alerts and
// the last cleanup reported by the caller.
type Health struct {
	TotalBytes        int64
	UsedBytes         int64
	FreeBytes         int64
	UsedPercent       float64
	Dirs              []DirUsage
	Alerts            []Alert
	LastCleanupAt     time.Time
	LastCleanupResult string
	CheckedAt         time.Time
}

// Service computes storage health. It is safe for concurrent use.
type Service struct {
	mu       sync.RWMutex
	db       *gorm.DB
	cfg      Config
	dirs     map[string]string
	dirsSet  map[string]bool // directories explicitly provided through SetDirs
	checked  bool
	lastDirs []DirUsage
	lastNote []Alert
	cleanup  time.Time
	result   string
}

// New creates a storage service. db is optional: it is only used to infer the
// recordings directory from persisted platform settings. cfg supplies the
// threshold percentages (defaults 70/85/95) and the walk entry cap (default
// 10000). Directory paths can be supplied later with SetDirs; until then the
// defaults from config.go are used (./data, ./data/snapshots,
// ./data/gb35114-certs, ./data/recordings).
func New(db *gorm.DB, cfg Config) *Service {
	return &Service{
		db:      db,
		cfg:     normalizeConfig(cfg),
		dirs:    defaultDirs(),
		dirsSet: make(map[string]bool),
	}
}

func normalizeConfig(cfg Config) Config {
	if cfg.InfoPercent == 0 && cfg.WarnPercent == 0 && cfg.CriticalPercent == 0 {
		cfg.InfoPercent, cfg.WarnPercent, cfg.CriticalPercent = 70, 85, 95
	}
	if cfg.MaxWalkEntries <= 0 {
		cfg.MaxWalkEntries = 10000
	}
	return cfg
}

func defaultDirs() map[string]string {
	return map[string]string{
		"snapshots":  "./data/snapshots",
		"data":       "./data",
		"certs":      "./data/gb35114-certs",
		"recordings": "./data/recordings",
	}
}

// SetDirs overrides one or more monitored directories. Names are canonicalized
// (e.g. "snapshot", "db", "gb35114", "recording") and empty values are ignored,
// so a caller can patch just the snapshot directory it read from config.
func (s *Service) SetDirs(dirs map[string]string) {
	if len(dirs) == 0 {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for name, path := range dirs {
		name = canonicalName(name)
		path = strings.TrimSpace(path)
		if name == "" || path == "" {
			continue
		}
		s.dirs[name] = path
		s.dirsSet[name] = true
	}
}

func canonicalName(name string) string {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "snapshot", "snapshots", "snapshotdir", "snapshot_dir":
		return "snapshots"
	case "data", "db", "dbpath", "db_dir":
		return "data"
	case "cert", "certs", "certdir", "gb35114", "gb35114_certs":
		return "certs"
	case "recording", "recordings", "recorddir", "record_dir":
		return "recordings"
	default:
		return strings.TrimSpace(name)
	}
}

// NoteCleanup records the outcome of the latest retention/cleanup run for the
// next Health report.
func (s *Service) NoteCleanup(at time.Time, result string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cleanup = at
	s.result = result
}

// Health measures the filesystem and all monitored directories. It never
// panics because of a bad directory; such directories are omitted from Dirs
// and described by an info alert.
func (s *Service) Health() Health {
	s.mu.RLock()
	cfg := s.cfg
	dirs := make(map[string]string, len(s.dirs))
	for name, path := range s.dirs {
		dirs[name] = path
	}
	_, recordingsSet := s.dirsSet["recordings"]
	cleanupAt, cleanupResult := s.cleanup, s.result
	s.mu.RUnlock()

	h := Health{
		LastCleanupAt:     cleanupAt,
		LastCleanupResult: cleanupResult,
		CheckedAt:         time.Now(),
	}

	// capacity of the volume that holds the data directory, falling back to /
	target := dirs["data"]
	if strings.TrimSpace(target) == "" {
		target = "/"
	}
	total, used, free, pct, ok := diskUsage(target)
	if !ok && target != "/" {
		target = "/"
		total, used, free, pct, ok = diskUsage(target)
	}
	if ok {
		h.TotalBytes, h.UsedBytes, h.FreeBytes, h.UsedPercent = total, used, free, pct
		if a, hit := thresholdAlert(cfg, target, pct); hit {
			h.Alerts = append(h.Alerts, a)
		}
	} else {
		h.Alerts = append(h.Alerts, Alert{
			Level:   LevelInfo,
			Dir:     target,
			Message: fmt.Sprintf("无法读取磁盘容量，已跳过: %s", target),
		})
	}

	// The recordings path is not a platform directory by default (ZLM writes
	// it); infer it from persisted settings unless the caller supplied one.
	if !recordingsSet {
		if path := s.recordingDirFromDB(); path != "" {
			dirs["recordings"] = path
		}
	}
	for _, name := range orderedNames(dirs) {
		path := dirs[name]
		u, err := measureDir(name, path, cfg.MaxWalkEntries)
		if err != nil {
			h.Alerts = append(h.Alerts, Alert{
				Level:       LevelInfo,
				Dir:         path,
				UsedPercent: pct,
				Message:     fmt.Sprintf("目录 %s 不可用，已跳过: %v", name, err),
			})
			continue
		}
		h.Dirs = append(h.Dirs, u)
	}

	s.mu.Lock()
	s.checked = true
	s.lastDirs = append([]DirUsage(nil), h.Dirs...)
	s.lastNote = append([]Alert(nil), h.Alerts...)
	s.mu.Unlock()
	return h
}

// Usage returns the directory sizes from the most recent Health call.
func (s *Service) Usage() []DirUsage {
	s.ensureChecked()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]DirUsage(nil), s.lastDirs...)
}

// Alerts returns the alerts from the most recent Health call.
func (s *Service) Alerts() []Alert {
	s.ensureChecked()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return append([]Alert(nil), s.lastNote...)
}

func (s *Service) ensureChecked() {
	s.mu.RLock()
	checked := s.checked
	s.mu.RUnlock()
	if !checked {
		s.Health()
	}
}

// recordingDirFromDB looks for a persisted recordings directory setting. A
// missing table or a closed database is treated as "not configured".
func (s *Service) recordingDirFromDB() string {
	if s.db == nil {
		return ""
	}
	keys := []string{"recordings_dir", "recording_dir", "record_dir", "zlm_mp4_save_path"}
	var rows []struct {
		Key   string `gorm:"column:key"`
		Value string `gorm:"column:value"`
	}
	if err := s.db.Table("platform_settings").Where("key IN ?", keys).Find(&rows).Error; err != nil {
		return ""
	}
	for _, key := range keys {
		for _, row := range rows {
			if row.Key == key && strings.TrimSpace(row.Value) != "" {
				return strings.TrimSpace(row.Value)
			}
		}
	}
	return ""
}

func diskUsage(path string) (total, used, free int64, pct float64, ok bool) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return 0, 0, 0, 0, false
	}
	blockSize := int64(st.Bsize)
	if blockSize <= 0 {
		blockSize = 1
	}
	total = int64(st.Blocks) * blockSize
	free = int64(st.Bavail) * blockSize // available to an unprivileged writer
	used = total - int64(st.Bfree)*blockSize
	if used < 0 {
		used = 0
	}
	if total > 0 {
		pct = float64(used) / float64(total) * 100
	}
	return total, used, free, pct, true
}

// thresholdAlert reports only the highest threshold that has been crossed.
func thresholdAlert(cfg Config, dir string, pct float64) (Alert, bool) {
	level, threshold := "", 0
	switch {
	case cfg.CriticalPercent > 0 && pct >= float64(cfg.CriticalPercent):
		level, threshold = LevelCritical, cfg.CriticalPercent
	case cfg.WarnPercent > 0 && pct >= float64(cfg.WarnPercent):
		level, threshold = LevelWarning, cfg.WarnPercent
	case cfg.InfoPercent > 0 && pct >= float64(cfg.InfoPercent):
		level, threshold = LevelInfo, cfg.InfoPercent
	default:
		return Alert{}, false
	}
	label := map[string]string{LevelInfo: "提示", LevelWarning: "警告", LevelCritical: "严重"}[level]
	return Alert{
		Level:       level,
		Dir:         dir,
		UsedPercent: pct,
		Message:     fmt.Sprintf("磁盘 %s 使用率 %.1f%%，达到%s阈值 %d%%", dir, pct, label, threshold),
	}, true
}

func orderedNames(dirs map[string]string) []string {
	preferred := []string{"snapshots", "data", "certs", "recordings"}
	seen := make(map[string]bool, len(dirs))
	names := make([]string, 0, len(dirs))
	for _, name := range preferred {
		if _, ok := dirs[name]; ok && !seen[name] {
			seen[name] = true
			names = append(names, name)
		}
	}
	var extra []string
	for name := range dirs {
		if !seen[name] {
			extra = append(extra, name)
		}
	}
	sort.Strings(extra)
	return append(names, extra...)
}

// measureDir walks a directory (or a single file) with a hard entry cap.
// Unreadable entries are skipped; a panic from the walk is converted to an
// error so one bad directory cannot take down the caller.
func measureDir(name, path string, maxEntries int) (u DirUsage, err error) {
	u = DirUsage{Name: name, Path: path}
	if _, statErr := os.Stat(path); statErr != nil {
		return u, statErr
	}
	if maxEntries <= 0 {
		maxEntries = 10000
	}
	visited := 0
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("walk panic: %v", r)
		}
	}()
	err = filepath.WalkDir(path, func(_ string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return nil // skip the unreadable entry and keep going
		}
		visited++
		if !d.IsDir() {
			if info, infoErr := d.Info(); infoErr == nil {
				u.Bytes += info.Size()
				u.Files++
			}
		}
		if visited >= maxEntries {
			return filepath.SkipAll
		}
		return nil
	})
	return u, err
}
