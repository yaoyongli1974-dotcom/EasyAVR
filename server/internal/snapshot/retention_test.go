package snapshot

import (
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/store"
)

var testSnapSeq atomic.Int64

func newRetentionService(t *testing.T) (*Service, *gorm.DB, string) {
	t.Helper()
	dir := t.TempDir()
	db, err := store.Open(filepath.Join(t.TempDir(), "snap.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	return NewService(db, nil, dir), db, dir
}

// addSnapshot writes a real file under dir and catalogs it.
func addSnapshot(t *testing.T, db *gorm.DB, dir string, takenAt time.Time, size int) model.Snapshot {
	t.Helper()
	sub := filepath.Join(dir, "1")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	name := fmt.Sprintf("%d-%d.jpg", time.Now().UnixNano(), testSnapSeq.Add(1))
	if err := os.WriteFile(filepath.Join(sub, name), make([]byte, size), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	snap := model.Snapshot{
		ChannelID: 1,
		Path:      filepath.Join("1", name),
		URL:       "/snapshots/1/" + name,
		SizeBytes: int64(size),
		TakenAt:   takenAt,
	}
	if err := db.Create(&snap).Error; err != nil {
		t.Fatalf("create snapshot: %v", err)
	}
	return snap
}

func countSnapshots(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	if err := db.Model(&model.Snapshot{}).Count(&n).Error; err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestCleanupDeletesExpiredOnly(t *testing.T) {
	s, db, dir := newRetentionService(t)
	s.SetRetention(RetentionConfig{Days: 7})
	now := time.Now()
	old := addSnapshot(t, db, dir, now.AddDate(0, 0, -10), 100)
	fresh := addSnapshot(t, db, dir, now.AddDate(0, 0, -1), 50)

	deleted, freed, err := s.Cleanup()
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if deleted != 1 || freed != 100 {
		t.Fatalf("deleted=%d bytes=%d, want 1/100", deleted, freed)
	}
	if countSnapshots(t, db) != 1 {
		t.Fatalf("rows = %d, want 1", countSnapshots(t, db))
	}
	var left model.Snapshot
	if err := db.First(&left).Error; err != nil || left.ID != fresh.ID {
		t.Fatalf("remaining snapshot = %+v (%v), want fresh %d", left, err, fresh.ID)
	}
	if _, err := os.Stat(filepath.Join(dir, old.Path)); !os.IsNotExist(err) {
		t.Fatalf("expired file still present: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, fresh.Path)); err != nil {
		t.Fatalf("fresh file removed: %v", err)
	}
}

func TestCleanupSkipsReferencedSnapshots(t *testing.T) {
	s, db, dir := newRetentionService(t)
	s.SetRetention(RetentionConfig{Days: 7})
	past := time.Now().AddDate(0, 0, -10)
	byEvent := addSnapshot(t, db, dir, past, 10)
	bySample := addSnapshot(t, db, dir, past, 10)
	free := addSnapshot(t, db, dir, past, 10)

	if err := db.Create(&model.AIEvent{Snapshot: byEvent.URL, Summary: "ref"}).Error; err != nil {
		t.Fatalf("event: %v", err)
	}
	if err := db.Create(&model.DatasetSample{ImageURL: bySample.URL}).Error; err != nil {
		t.Fatalf("sample: %v", err)
	}
	// A non-exact match must not protect the free snapshot.
	if err := db.Create(&model.AIEvent{Snapshot: free.URL + "-other"}).Error; err != nil {
		t.Fatalf("event near-match: %v", err)
	}

	deleted, _, err := s.Cleanup()
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1", deleted)
	}
	if n := countSnapshots(t, db); n != 2 {
		t.Fatalf("rows = %d, want 2 (referenced ones kept)", n)
	}
	var gone model.Snapshot
	if err := db.First(&gone, free.ID).Error; err == nil {
		t.Fatalf("unreferenced snapshot %d should be deleted", free.ID)
	}
	for _, id := range []uint{byEvent.ID, bySample.ID} {
		var kept model.Snapshot
		if err := db.First(&kept, id).Error; err != nil {
			t.Fatalf("referenced snapshot %d should be kept: %v", id, err)
		}
	}
}

func TestCleanupOrphanRowMissingFile(t *testing.T) {
	s, db, dir := newRetentionService(t)
	s.SetRetention(RetentionConfig{Days: 7})
	snap := addSnapshot(t, db, dir, time.Now().AddDate(0, 0, -10), 33)
	if err := os.Remove(filepath.Join(dir, snap.Path)); err != nil {
		t.Fatalf("remove file: %v", err)
	}

	deleted, freed, err := s.Cleanup()
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if deleted != 1 || freed != 33 {
		t.Fatalf("deleted=%d bytes=%d, want 1/33", deleted, freed)
	}
	if countSnapshots(t, db) != 0 {
		t.Fatalf("orphan row not cleared")
	}
}

func TestCleanupFailureContinuesThenRetries(t *testing.T) {
	s, db, dir := newRetentionService(t)
	s.SetRetention(RetentionConfig{Days: 7})
	past := time.Now().AddDate(0, 0, -10)

	// A non-empty directory cannot be removed by os.Remove: this row fails.
	blocked := addSnapshot(t, db, dir, past, 10)
	blockedAbs, err := s.ResolvePath(blocked.Path)
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	if err := os.Remove(blockedAbs); err != nil {
		t.Fatalf("remove file: %v", err)
	}
	if err := os.Mkdir(blockedAbs, 0o755); err != nil {
		t.Fatalf("mkdir blocker: %v", err)
	}
	child := filepath.Join(blockedAbs, "lock")
	if err := os.WriteFile(child, []byte("x"), 0o644); err != nil {
		t.Fatalf("write child: %v", err)
	}
	good := addSnapshot(t, db, dir, past, 20)

	deleted, _, err := s.Cleanup()
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("deleted = %d, want 1 (failed row must be skipped)", deleted)
	}
	var left model.Snapshot
	if err := db.First(&left).Error; err != nil || left.ID != blocked.ID {
		t.Fatalf("remaining = %+v (%v), want blocked=%d (good=%d deleted)", left, err, blocked.ID, good.ID)
	}

	// Retry after clearing the transient failure.
	if err := os.Remove(child); err != nil {
		t.Fatalf("remove child: %v", err)
	}
	if err := os.Remove(blockedAbs); err != nil {
		t.Fatalf("remove blocker dir: %v", err)
	}
	deleted, _, err = s.Cleanup()
	if err != nil {
		t.Fatalf("retry cleanup: %v", err)
	}
	if deleted != 1 || countSnapshots(t, db) != 0 {
		t.Fatalf("retry deleted=%d rows=%d, want 1/0", deleted, countSnapshots(t, db))
	}
}

func TestCleanupMaxPerRun(t *testing.T) {
	s, db, dir := newRetentionService(t)
	s.SetRetention(RetentionConfig{Days: 7, MaxPerRun: 2})
	past := time.Now().AddDate(0, 0, -10)
	for i := 0; i < 5; i++ {
		addSnapshot(t, db, dir, past.Add(time.Duration(i)*time.Minute), 1)
	}

	deleted, _, err := s.Cleanup()
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if deleted != 2 {
		t.Fatalf("deleted = %d, want 2", deleted)
	}
	if got := countSnapshots(t, db); got != 3 {
		t.Fatalf("rows = %d, want 3", got)
	}
	deleted, _, err = s.Cleanup()
	if err != nil {
		t.Fatalf("second cleanup: %v", err)
	}
	if deleted != 2 || countSnapshots(t, db) != 1 {
		t.Fatalf("second pass deleted=%d rows=%d, want 2/1", deleted, countSnapshots(t, db))
	}
}

func TestRetentionStatsAndSetter(t *testing.T) {
	s, db, _ := newRetentionService(t)
	cfg := RetentionConfig{Days: 3, MaxDiskUsageMB: 10, CleanupInterval: 5 * time.Minute, MaxPerRun: 7}
	s.SetRetention(cfg)
	if got := s.Retention(); got != cfg {
		t.Fatalf("Retention() = %+v, want %+v", got, cfg)
	}

	if _, _, err := s.Cleanup(); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	lastAt, lastDeleted, lastErr := s.RetentionStats()
	if lastAt.IsZero() || lastDeleted != 0 || lastErr != "" {
		t.Fatalf("stats = %v/%d/%q", lastAt, lastDeleted, lastErr)
	}

	// A full failure is recorded in lastErr.
	if err := db.Migrator().DropTable(&model.Snapshot{}); err != nil {
		t.Fatalf("drop table: %v", err)
	}
	if _, _, err := s.Cleanup(); err == nil {
		t.Fatal("cleanup with missing table should fail")
	}
	_, _, lastErr = s.RetentionStats()
	if lastErr == "" {
		t.Fatal("lastErr should record the fatal error")
	}
}

func TestDiskCapNeverDeletesFreshSnapshots(t *testing.T) {
	s, db, dir := newRetentionService(t)
	// 1 MB cap with a 2 MB recent snapshot: disk pressure must not override
	// the retention window.
	s.SetRetention(RetentionConfig{Days: 365, MaxDiskUsageMB: 1})
	snap := addSnapshot(t, db, dir, time.Now(), 2*1024*1024)

	deleted, _, err := s.Cleanup()
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if deleted != 0 {
		t.Fatalf("deleted = %d, want 0 (snapshot is not expired)", deleted)
	}
	if _, err := os.Stat(filepath.Join(dir, snap.Path)); err != nil {
		t.Fatalf("fresh file removed under disk pressure: %v", err)
	}
}
