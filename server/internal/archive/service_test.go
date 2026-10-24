package archive

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/store"
	"github.com/easyavr/easyavr/internal/video"
)

type stubUploader struct {
	bucket string
	object string
	size   int64
	data   []byte
	err    error
}

func (u *stubUploader) Put(ctx context.Context, bucket, object string, r io.Reader, size int64) error {
	u.bucket, u.object, u.size = bucket, object, size
	body, readErr := io.ReadAll(r)
	u.data = body
	if readErr != nil {
		return readErr
	}
	return u.err
}

func newTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := store.Open(filepath.Join(t.TempDir(), "archive.db"))
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	if err := db.AutoMigrate(&model.CloudStore{}, &model.RecordingArchive{}); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}

func seedRecording(t *testing.T, db *gorm.DB) (model.Recording, model.CloudStore) {
	t.Helper()
	rec := model.Recording{ChannelID: 7, StreamKey: "ch_abc", Date: "2024-01-02", File: "14-30-00.mp4"}
	if err := db.Create(&rec).Error; err != nil {
		t.Fatalf("create recording: %v", err)
	}
	st := model.CloudStore{
		Name: "minio", Type: "s3", Endpoint: "http://127.0.0.1:9000",
		Bucket: "videos", AccessKey: "ak", SecretKey: "sk", PathStyle: true, Enabled: true,
	}
	if err := db.Create(&st).Error; err != nil {
		t.Fatalf("create store: %v", err)
	}
	return rec, st
}

func testZLM(t *testing.T, h http.Handler) *video.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatalf("parse test server url: %v", err)
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil {
		t.Fatalf("parse test server port: %v", err)
	}
	return video.NewClient("http://"+u.Host, "secret", u.Hostname(), port, 0, 554, 1935)
}

func TestObjectPath(t *testing.T) {
	got := ObjectPath("ch_abc", "2024-01-02", "14-30-00.mp4")
	want := "easyavr/recordings/ch_abc/2024-01-02/14-30-00.mp4"
	if got != want {
		t.Fatalf("ObjectPath = %q, want %q", got, want)
	}
}

func TestParseEndpoint(t *testing.T) {
	cases := []struct {
		in     string
		host   string
		secure bool
	}{
		{"http://127.0.0.1:9000", "127.0.0.1:9000", false},
		{"https://oss-cn-hangzhou.aliyuncs.com", "oss-cn-hangzhou.aliyuncs.com", true},
		{"minio.local:9000", "minio.local:9000", false},
	}
	for _, tc := range cases {
		host, secure, err := parseEndpoint(tc.in)
		if err != nil {
			t.Fatalf("parseEndpoint(%q): %v", tc.in, err)
		}
		if host != tc.host || secure != tc.secure {
			t.Errorf("parseEndpoint(%q) = %q,%v want %q,%v", tc.in, host, secure, tc.host, tc.secure)
		}
	}
	if _, _, err := parseEndpoint("  "); err == nil {
		t.Error("empty endpoint should fail")
	}
}

func TestUploadKnownSize(t *testing.T) {
	db := newTestDB(t)
	rec, st := seedRecording(t, db)
	payload := []byte("fake-mp4-bytes")
	zlm := testZLM(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write(payload)
	}))
	up := &stubUploader{}
	svc := NewServiceWithFactory(db, zlm, func(model.CloudStore) (Uploader, error) { return up, nil })

	arch, err := svc.Upload(rec.ID, st.ID)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if arch.Status != "uploaded" {
		t.Errorf("status = %q, want uploaded", arch.Status)
	}
	if want := "easyavr/recordings/ch_abc/2024-01-02/14-30-00.mp4"; arch.Object != want {
		t.Errorf("object = %q, want %q", arch.Object, want)
	}
	if arch.StoreID != st.ID || arch.Bucket != "videos" || arch.ChannelID != rec.ChannelID {
		t.Errorf("archive row = %+v", arch)
	}
	if arch.Size != int64(len(payload)) {
		t.Errorf("size = %d, want %d", arch.Size, len(payload))
	}
	if up.size != int64(len(payload)) {
		t.Errorf("upload size = %d, want %d", up.size, len(payload))
	}
	if up.bucket != "videos" || up.object != arch.Object {
		t.Errorf("put args = %q/%q", up.bucket, up.object)
	}
	if !bytes.Equal(up.data, payload) {
		t.Errorf("uploaded data = %q, want %q", up.data, payload)
	}

	var saved model.RecordingArchive
	if err := db.First(&saved, arch.ID).Error; err != nil {
		t.Fatalf("reload archive: %v", err)
	}
	if saved.Status != "uploaded" || saved.Size != int64(len(payload)) {
		t.Errorf("saved row = %+v", saved)
	}
}

func TestUploadUnknownSize(t *testing.T) {
	db := newTestDB(t)
	rec, st := seedRecording(t, db)
	payload := []byte("chunked-body")
	zlm := testZLM(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Flushing before the handler returns forces a chunked response, so
		// Content-Length is absent and the size has to be counted.
		w.Write(payload)
		w.(http.Flusher).Flush()
		w.Write(payload)
	}))
	up := &stubUploader{}
	svc := NewServiceWithFactory(db, zlm, func(model.CloudStore) (Uploader, error) { return up, nil })

	arch, err := svc.Upload(rec.ID, st.ID)
	if err != nil {
		t.Fatalf("Upload: %v", err)
	}
	if up.size != -1 {
		t.Errorf("upload size = %d, want -1 for unknown length", up.size)
	}
	want := int64(len(payload) * 2)
	if arch.Size != want || int64(len(up.data)) != want {
		t.Errorf("size = %d (streamed %d), want %d", arch.Size, len(up.data), want)
	}
	if !bytes.Equal(up.data, append(append([]byte{}, payload...), payload...)) {
		t.Errorf("uploaded data = %q", up.data)
	}
}

func TestUploadReplacesPreviousRow(t *testing.T) {
	db := newTestDB(t)
	rec, st := seedRecording(t, db)
	zlm := testZLM(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("abc"))
	}))
	svc := NewServiceWithFactory(db, zlm, func(model.CloudStore) (Uploader, error) {
		return &stubUploader{}, nil
	})

	for i := 0; i < 2; i++ {
		if _, err := svc.Upload(rec.ID, st.ID); err != nil {
			t.Fatalf("Upload #%d: %v", i+1, err)
		}
	}
	var count int64
	db.Model(&model.RecordingArchive{}).Where("recording_id = ?", rec.ID).Count(&count)
	if count != 1 {
		t.Fatalf("archive rows = %d, want 1", count)
	}
}

func TestUploadFailureRecordsError(t *testing.T) {
	cases := []struct {
		name     string
		uploader Uploader
		status   int
	}{
		{name: "upload error", uploader: &stubUploader{err: errors.New("access denied")}, status: http.StatusOK},
		{name: "download 404", uploader: &stubUploader{}, status: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := newTestDB(t)
			rec, st := seedRecording(t, db)
			zlm := testZLM(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tc.status)
				w.Write([]byte("nope"))
			}))
			svc := NewServiceWithFactory(db, zlm, func(model.CloudStore) (Uploader, error) {
				return tc.uploader, nil
			})

			arch, err := svc.Upload(rec.ID, st.ID)
			if err == nil {
				t.Fatal("Upload should fail")
			}
			if arch.Status != "failed" {
				t.Errorf("status = %q, want failed", arch.Status)
			}
			if arch.Error == "" || arch.Error != truncate(arch.Error, 500) {
				t.Errorf("error = %q", arch.Error)
			}

			var rows []model.RecordingArchive
			db.Where("recording_id = ?", rec.ID).Find(&rows)
			if len(rows) != 1 || rows[0].Status != "failed" || rows[0].Error == "" {
				t.Fatalf("failed rows = %+v", rows)
			}
		})
	}
}

func TestTestStoreChecksBucket(t *testing.T) {
	db := newTestDB(t)
	_, st := seedRecording(t, db)
	factory := func(up Uploader) func(model.CloudStore) (Uploader, error) {
		return func(model.CloudStore) (Uploader, error) { return up, nil }
	}
	// Missing bucket is fine now: the test auto-creates it.
	up := &checkerUploader{present: boolFalse()}
	svc := NewServiceWithFactory(db, nil, factory(up))
	if err := svc.Test(context.Background(), st); err != nil {
		t.Fatalf("ensure-missing bucket should pass: %v", err)
	}
	if up.ensured != 1 {
		t.Fatalf("EnsureBucket calls = %d, want 1", up.ensured)
	}
	// EnsureBucket failure surfaces as Bad Gateway text.
	up2 := &checkerUploader{present: boolFalse(), ensureErr: errors.New("access denied")}
	svc2 := NewServiceWithFactory(db, nil, factory(up2))
	if err := svc2.Test(context.Background(), st); err == nil || !strings.Contains(err.Error(), "access denied") {
		t.Fatalf("ensure failure should surface, got %v", err)
	}
	if err := svc2.Test(context.Background(), model.CloudStore{Bucket: "b"}); err == nil {
		t.Fatal("missing endpoint should fail")
	}
}

func boolFalse() *bool {
	f := false
	return &f
}

type checkerUploader struct {
	present   *bool
	ensureErr error
	ensured   int
}

func (u *checkerUploader) Put(ctx context.Context, bucket, object string, r io.Reader, size int64) error {
	return nil
}

func (u *checkerUploader) BucketExists(ctx context.Context, bucket string) (bool, error) {
	return *u.present, nil
}

func (u *checkerUploader) EnsureBucket(ctx context.Context, bucket string) error {
	u.ensured++
	return u.ensureErr
}
