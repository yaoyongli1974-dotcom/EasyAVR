package recording

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/store"
	"github.com/easyavr/easyavr/internal/video"
)

// fakeZLM answers just the endpoints the recording service needs and records
// the periods requested from the file-deletion API.
func fakeZLM(t *testing.T, paths []string) (*httptest.Server, *[]string) {
	t.Helper()
	var deleted []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/getMp4RecordFile"):
			_ = json.NewEncoder(w).Encode(map[string]any{
				"code": 0,
				"data": map[string]any{"rootPath": "/record/2026-10-24/", "paths": paths},
			})
		case strings.HasSuffix(r.URL.Path, "/deleteRecordDirectory"):
			deleted = append(deleted, r.FormValue("period"))
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0})
		default:
			_ = json.NewEncoder(w).Encode(map[string]any{"code": 0, "msg": "success", "data": nil})
		}
	}))
	t.Cleanup(srv.Close)
	return srv, &deleted
}

func newTestService(t *testing.T, zlmURL string) (*Service, *model.Channel) {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "rec.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	dev := model.Device{Name: "dev", Protocol: "rtsp", AccessMode: "pull"}
	if err := db.Create(&dev).Error; err != nil {
		t.Fatalf("create device: %v", err)
	}
	ch := model.Channel{DeviceID: dev.ID, Name: "ch", StreamKey: "ch_test", Enabled: true}
	if err := db.Create(&ch).Error; err != nil {
		t.Fatalf("create channel: %v", err)
	}
	client := video.NewClient(zlmURL, "secret", "127.0.0.1", 80, 8080, 554, 1935)
	return NewService(db, client), &ch
}

// Stopping a recording must publish the finished segment into the catalog;
// previously the file existed only on the media core until a manual sync.
func TestStopSyncsCatalog(t *testing.T) {
	zlmSrv, _ := fakeZLM(t, []string{"2026-10-24-15-57-03-0.mp4"})
	svc, ch := newTestService(t, zlmSrv.URL)

	if err := svc.Stop(ch.ID); err != nil {
		t.Fatalf("stop: %v", err)
	}
	recs, err := svc.List(ch.ID, "")
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if len(recs) != 1 {
		t.Fatalf("catalog entries = %d, want 1", len(recs))
	}
	rec := recs[0]
	today := time.Now().Format("2006-01-02")
	wantURL := "/record/live/ch_test/" + today + "/2026-10-24-15-57-03-0.mp4"
	if !strings.HasSuffix(rec.URL, wantURL) {
		t.Errorf("url = %q, want suffix %q", rec.URL, wantURL)
	}
	want := time.Date(2026, 10, 24, 15, 57, 3, 0, time.Local)
	if !rec.StartTime.Equal(want) {
		t.Errorf("start time = %v, want %v", rec.StartTime, want)
	}
}

// A second stop must not duplicate catalog entries.
func TestSyncIsIdempotent(t *testing.T) {
	zlmSrv, _ := fakeZLM(t, []string{"2026-10-24-15-57-03-0.mp4"})
	svc, ch := newTestService(t, zlmSrv.URL)

	for i := 0; i < 3; i++ {
		if _, err := svc.Sync(ch.ID, "2026-10-24"); err != nil {
			t.Fatalf("sync %d: %v", i, err)
		}
	}
	recs, _ := svc.List(ch.ID, "")
	if len(recs) != 1 {
		t.Fatalf("catalog entries = %d, want 1", len(recs))
	}
}

func TestParseRecordTimeFormats(t *testing.T) {
	want := time.Date(2024, 1, 2, 14, 30, 5, 0, time.Local)
	cases := []struct{ name, date, path string }{
		{"dated path", "2024-01-02", "2024-01-02/14-30-05.mp4"},
		{"bare zlm name", "2024-01-02", "2024-01-02-14-30-05-0.mp4"},
		{"time only", "2024-01-02", "14-30-05.mp4"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseRecordTime(tc.date, tc.path); !got.Equal(want) {
				t.Fatalf("parseRecordTime(%q) = %v, want %v", tc.path, got, want)
			}
		})
	}
}

// Cleanup with deleteFiles must remove both the catalog rows and the day
// folders on the media core; recent recordings must survive.
func TestCleanupDeletesExpiredFiles(t *testing.T) {
	zlmSrv, deleted := fakeZLM(t, nil)
	svc, ch := newTestService(t, zlmSrv.URL)

	now := time.Now()
	oldDate := now.AddDate(0, 0, -10).Format("2006-01-02")
	today := now.Format("2006-01-02")
	rows := []model.Recording{
		{ChannelID: ch.ID, StreamKey: ch.StreamKey, Date: oldDate, File: "a.mp4", URL: "http://x"},
		{ChannelID: ch.ID, StreamKey: ch.StreamKey, Date: today, File: "b.mp4", URL: "http://x"},
	}
	for i := range rows {
		if err := svc.db.Create(&rows[i]).Error; err != nil {
			t.Fatalf("seed row %d: %v", i, err)
		}
	}

	days := 3
	removed, err := svc.Cleanup(days, true)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if len(*deleted) != 1 || (*deleted)[0] != oldDate {
		t.Fatalf("deleted periods = %v, want [%s]", *deleted, oldDate)
	}
	recs, _ := svc.List(ch.ID, "")
	if len(recs) != 1 || recs[0].Date != today {
		t.Fatalf("surviving catalog = %+v", recs)
	}
}

// files=0 must only prune the catalog, leaving media files untouched.
func TestCleanupCatalogOnly(t *testing.T) {
	zlmSrv, deleted := fakeZLM(t, nil)
	svc, ch := newTestService(t, zlmSrv.URL)

	now := time.Now()
	rec := model.Recording{ChannelID: ch.ID, StreamKey: ch.StreamKey, Date: now.AddDate(0, 0, -30).Format("2006-01-02"), File: "old.mp4"}
	if err := svc.db.Create(&rec).Error; err != nil {
		t.Fatalf("seed: %v", err)
	}
	removed, err := svc.Cleanup(7, false)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if removed != 1 || len(*deleted) != 0 {
		t.Fatalf("removed=%d deleted=%v, want catalog-only prune", removed, *deleted)
	}
}

// A plan retention window should clear only that channel's recordings.
func TestCleanupPlanScoped(t *testing.T) {
	zlmSrv, deleted := fakeZLM(t, nil)
	db, err := store.Open(filepath.Join(t.TempDir(), "plan.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	dev := model.Device{Name: "dev", Protocol: "rtsp", AccessMode: "pull"}
	if err := db.Create(&dev).Error; err != nil {
		t.Fatalf("device: %v", err)
	}
	chs := []model.Channel{{DeviceID: dev.ID, Name: "a", StreamKey: "ch_a"}, {DeviceID: dev.ID, Name: "b", StreamKey: "ch_b"}}
	for i := range chs {
		if err := db.Create(&chs[i]).Error; err != nil {
			t.Fatalf("channel %d: %v", i, err)
		}
	}
	oldDate := time.Now().AddDate(0, 0, -10).Format("2006-01-02")
	plan := model.RecordingPlan{ChannelID: chs[0].ID, Enabled: true, RetentionDays: 5}
	if err := db.Create(&plan).Error; err != nil {
		t.Fatalf("plan: %v", err)
	}
	for i, ch := range chs {
		rec := model.Recording{ChannelID: ch.ID, StreamKey: ch.StreamKey, Date: oldDate, File: "old.mp4"}
		if err := db.Create(&rec).Error; err != nil {
			t.Fatalf("rec %d: %v", i, err)
		}
	}

	client := video.NewClient(zlmSrv.URL, "secret", "127.0.0.1", 80, 8080, 554, 1935)
	svc := NewService(db, client)
	removed, err := svc.Cleanup(0, true)
	if err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if removed != 1 {
		t.Fatalf("removed = %d, want 1", removed)
	}
	if len(*deleted) != 1 || !strings.HasSuffix((*deleted)[0], oldDate) {
		t.Fatalf("deleted periods = %v, want only ch_a's %s", *deleted, oldDate)
	}
}
