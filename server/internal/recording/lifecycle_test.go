package recording

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/store"
	"github.com/easyavr/easyavr/internal/video"
)

// lifecycleZLM is a per-stream/per-day fake media core. It answers the file
// listing and directory deletion APIs plus HEAD on the playback URL so Sync's
// best-effort size probe can be exercised.
type lifecycleZLM struct {
	srv     *httptest.Server
	mu      sync.Mutex
	files   map[string]map[string][]string // stream -> date -> paths
	deleted []string
}

func newLifecycleZLM(t *testing.T, files map[string]map[string][]string) *lifecycleZLM {
	t.Helper()
	f := &lifecycleZLM{files: files}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMp4RecordFile"):
			stream, period := r.URL.Query().Get("stream"), r.URL.Query().Get("period")
			paths := []string{}
			f.mu.Lock()
			if byDate := f.files[stream]; byDate != nil {
				paths = append(paths, byDate[period]...)
			}
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{"rootPath": "/record/", "paths": paths},
			})
		case strings.HasSuffix(r.URL.Path, "/deleteRecordDirectory"):
			f.mu.Lock()
			f.deleted = append(f.deleted, r.FormValue("period"))
			f.mu.Unlock()
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0})
		case strings.HasPrefix(r.URL.Path, "/record/live/"):
			// /record/live/<stream>/<date>/<file>
			parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/record/live/"), "/")
			found := false
			if len(parts) == 3 {
				f.mu.Lock()
				for _, p := range f.files[parts[0]][parts[1]] {
					if normRecordFile(p) == parts[2] {
						found = true
						break
					}
				}
				f.mu.Unlock()
			}
			if !found {
				w.WriteHeader(http.StatusNotFound)
				return
			}
			payload := []byte("fake-mp4-payload")
			w.Header().Set("Content-Length", strconv.Itoa(len(payload)))
			w.WriteHeader(http.StatusOK)
			if r.Method != http.MethodHead {
				_, _ = w.Write(payload)
			}
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "success", "data": nil})
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *lifecycleZLM) deletions() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.deleted...)
}

func newLifecycleService(t *testing.T, fake *lifecycleZLM) (*Service, *model.Channel) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "lifecycle.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	dev := model.Device{Name: "dev", Protocol: "rtsp", AccessMode: "pull"}
	if err := db.Create(&dev).Error; err != nil {
		t.Fatalf("device: %v", err)
	}
	ch := model.Channel{DeviceID: dev.ID, Name: "ch", StreamKey: "ch_test", Enabled: true}
	if err := db.Create(&ch).Error; err != nil {
		t.Fatalf("channel: %v", err)
	}
	u, err := url.Parse(fake.srv.URL)
	if err != nil {
		t.Fatalf("parse fake url: %v", err)
	}
	port, _ := strconv.Atoi(u.Port())
	client := video.NewClient(fake.srv.URL, "secret", u.Hostname(), port, 8080, 554, 1935)
	return NewService(db, client), &ch
}

func oldRecording(ch *model.Channel, file string) model.Recording {
	return model.Recording{
		ChannelID: ch.ID, StreamKey: ch.StreamKey,
		Date: time.Now().AddDate(0, 0, -10).Format("2006-01-02"),
		File: file, URL: "http://x/" + file,
	}
}

func uploadedArchive(recID, chID uint, size int64) *model.RecordingArchive {
	return &model.RecordingArchive{
		RecordingID: recID, ChannelID: chID, StreamKey: "ch_test",
		Status: archiveStatusUploaded, Size: size,
	}
}

func TestCleanupSkipsMarkedRecording(t *testing.T) {
	fake := newLifecycleZLM(t, nil)
	svc, ch := newLifecycleService(t, fake)
	rec := oldRecording(ch, "marked.mp4")
	rec.Marked = true
	if err := svc.db.Create(&rec).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := svc.db.Create(uploadedArchive(rec.ID, ch.ID, 10)).Error; err != nil {
		t.Fatalf("archive: %v", err)
	}

	removed, err := svc.Cleanup(3, true)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if removed != 0 || svc.LastCleanup().Skipped.Marked != 1 {
		t.Fatalf("removed=%d skipped=%+v, want marked protected", removed, svc.LastCleanup())
	}
	if got := fake.deletions(); len(got) != 0 {
		t.Fatalf("deleted folders = %v, want none", got)
	}
}

func TestCleanupSkipsActiveChannel(t *testing.T) {
	fake := newLifecycleZLM(t, nil)
	svc, ch := newLifecycleService(t, fake)
	if err := svc.db.Model(ch).Update("recording", true).Error; err != nil {
		t.Fatalf("mark recording: %v", err)
	}
	rec := oldRecording(ch, "active.mp4")
	if err := svc.db.Create(&rec).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	removed, err := svc.Cleanup(3, true)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if removed != 0 || svc.LastCleanup().Skipped.Active != 1 {
		t.Fatalf("removed=%d skipped=%+v, want active protected", removed, svc.LastCleanup())
	}
	if got := fake.deletions(); len(got) != 0 {
		t.Fatalf("deleted folders = %v, want none", got)
	}
}

func TestCleanupSkipsPlanManagedChannel(t *testing.T) {
	fake := newLifecycleZLM(t, nil)
	svc, ch := newLifecycleService(t, fake)
	svc.mu.Lock()
	svc.planManaged[ch.ID] = true
	svc.mu.Unlock()
	rec := oldRecording(ch, "plan.mp4")
	if err := svc.db.Create(&rec).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}

	removed, err := svc.Cleanup(3, true)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if removed != 0 || svc.LastCleanup().Skipped.Active != 1 {
		t.Fatalf("removed=%d skipped=%+v, want plan-managed protected", removed, svc.LastCleanup())
	}
}

// With archiving in use, a sole copy that has no uploaded archive must survive
// a file-deleting cleanup.
func TestCleanupSkipsUnarchivedSoleCopy(t *testing.T) {
	fake := newLifecycleZLM(t, nil)
	svc, ch := newLifecycleService(t, fake)
	rec := oldRecording(ch, "sole.mp4")
	if err := svc.db.Create(&rec).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	// A different recording's archive row enables the lifecycle protection.
	other := oldRecording(ch, "other.mp4")
	other.Date = time.Now().Format("2006-01-02") // keep it out of the prune window
	if err := svc.db.Create(&other).Error; err != nil {
		t.Fatalf("seed other: %v", err)
	}
	if err := svc.db.Create(uploadedArchive(other.ID, ch.ID, 10)).Error; err != nil {
		t.Fatalf("archive: %v", err)
	}

	removed, err := svc.Cleanup(3, true)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if removed != 0 || svc.LastCleanup().Skipped.Unarchived != 1 {
		t.Fatalf("removed=%d skipped=%+v, want unarchived protected", removed, svc.LastCleanup())
	}
	if got := fake.deletions(); len(got) != 0 {
		t.Fatalf("deleted folders = %v, want none (protected file present)", got)
	}
}

func TestCleanupAllowsUploadedArchive(t *testing.T) {
	fake := newLifecycleZLM(t, nil)
	svc, ch := newLifecycleService(t, fake)
	rec := oldRecording(ch, "archived.mp4")
	if err := svc.db.Create(&rec).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := svc.db.Create(uploadedArchive(rec.ID, ch.ID, 10)).Error; err != nil {
		t.Fatalf("archive: %v", err)
	}

	removed, err := svc.Cleanup(3, true)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if removed != 1 || svc.LastCleanup().Skipped != (CleanupSkipped{}) {
		t.Fatalf("removed=%d skipped=%+v, want deletion", removed, svc.LastCleanup())
	}
	if got := fake.deletions(); len(got) != 1 {
		t.Fatalf("deleted folders = %v, want one", got)
	}
}

func TestCleanupFilesZeroIgnoresUnarchived(t *testing.T) {
	fake := newLifecycleZLM(t, nil)
	svc, ch := newLifecycleService(t, fake)
	rec := oldRecording(ch, "catalog-only.mp4")
	if err := svc.db.Create(&rec).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	other := oldRecording(ch, "other.mp4")
	other.Date = time.Now().Format("2006-01-02") // keep it out of the prune window
	if err := svc.db.Create(&other).Error; err != nil {
		t.Fatalf("seed other: %v", err)
	}
	if err := svc.db.Create(uploadedArchive(other.ID, ch.ID, 10)).Error; err != nil {
		t.Fatalf("archive: %v", err)
	}

	removed, err := svc.Cleanup(3, false)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if removed != 1 || svc.LastCleanup().Skipped.Unarchived != 0 {
		t.Fatalf("removed=%d skipped=%+v, want catalog-only prune", removed, svc.LastCleanup())
	}
	if got := fake.deletions(); len(got) != 0 {
		t.Fatalf("deleted folders = %v, want none for files=0", got)
	}
}

func TestConsistencyReportsMissingAndOrphan(t *testing.T) {
	today := time.Now().Format("2006-01-02")
	fake := newLifecycleZLM(t, map[string]map[string][]string{
		"ch_test": {today: {"present.mp4", "orphan.mp4"}},
	})
	svc, ch := newLifecycleService(t, fake)
	present := model.Recording{ChannelID: ch.ID, StreamKey: ch.StreamKey, Date: today, File: "present.mp4"}
	gone := model.Recording{ChannelID: ch.ID, StreamKey: ch.StreamKey, Date: today, File: "gone.mp4"}
	for _, rec := range []*model.Recording{&present, &gone} {
		if err := svc.db.Create(rec).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	missing, orphan, err := svc.Consistency()
	if err != nil {
		t.Fatalf("consistency: %v", err)
	}
	if len(missing) != 1 || missing[0] != gone.ID {
		t.Fatalf("missing = %v, want [%d]", missing, gone.ID)
	}
	if len(orphan) != 1 || !strings.HasSuffix(orphan[0], "/orphan.mp4") {
		t.Fatalf("orphan = %v, want the ZLM-only file", orphan)
	}
}

func TestConsistencyIsReadOnly(t *testing.T) {
	today := time.Now().Format("2006-01-02")
	fake := newLifecycleZLM(t, map[string]map[string][]string{
		"ch_test": {today: {"present.mp4"}},
	})
	svc, ch := newLifecycleService(t, fake)
	rec := model.Recording{ChannelID: ch.ID, StreamKey: ch.StreamKey, Date: today, File: "gone.mp4"}
	if err := svc.db.Create(&rec).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, _, err := svc.Consistency(); err != nil {
		t.Fatalf("consistency: %v", err)
	}
	var count int64
	svc.db.Model(&model.Recording{}).Count(&count)
	if count != 1 {
		t.Fatalf("catalog rows after consistency = %d, want 1", count)
	}
	if got := fake.deletions(); len(got) != 0 {
		t.Fatalf("deleted folders = %v, consistency must not mutate", got)
	}
}

func TestCapacitySumsLocalAndArchivedBytes(t *testing.T) {
	fake := newLifecycleZLM(t, nil)
	svc, ch := newLifecycleService(t, fake)
	r1 := oldRecording(ch, "a.mp4")
	r1.SizeBytes = 100
	r2 := oldRecording(ch, "b.mp4")
	r2.SizeBytes = 200
	for _, rec := range []*model.Recording{&r1, &r2} {
		if err := svc.db.Create(rec).Error; err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	for _, arch := range []*model.RecordingArchive{
		uploadedArchive(r1.ID, ch.ID, 50),
		uploadedArchive(r2.ID, ch.ID, 70),
	} {
		if err := svc.db.Create(arch).Error; err != nil {
			t.Fatalf("archive: %v", err)
		}
	}

	total, local, archived, err := svc.Capacity()
	if err != nil {
		t.Fatalf("capacity: %v", err)
	}
	if local != 300 || archived != 120 || total != 420 {
		t.Fatalf("capacity = (%d,%d,%d), want (420,300,120)", total, local, archived)
	}
}

func TestSyncFillsSizeBytesWhenMetadataAvailable(t *testing.T) {
	today := time.Now().Format("2006-01-02")
	fake := newLifecycleZLM(t, map[string]map[string][]string{
		"ch_test": {today: {"sized.mp4"}},
	})
	svc, ch := newLifecycleService(t, fake)

	items, err := svc.Sync(ch.ID, today)
	if err != nil {
		t.Fatalf("sync: %v", err)
	}
	if len(items) != 1 || items[0].SizeBytes != int64(len("fake-mp4-payload")) {
		t.Fatalf("synced items = %+v, want probed SizeBytes", items)
	}
}
