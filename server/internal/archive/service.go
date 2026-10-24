// Package archive uploads recorded videos to S3-compatible object storage
// (MinIO, Aliyun OSS, Tencent COS, ...) and tracks the resulting objects.
package archive

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/video"
)

// uploadPartSize is the multipart chunk used when the source size is unknown
// (minio-go requires an explicit part size for size -1 streams).
const uploadPartSize = 16 << 20

// Uploader stores one object. The interface keeps the service testable without
// a live object storage server.
type Uploader interface {
	Put(ctx context.Context, bucket, object string, r io.Reader, size int64) error
}

// UploaderFactory builds an Uploader for a store configuration.
type UploaderFactory func(store model.CloudStore) (Uploader, error)

// bucketChecker is implemented by the real S3 uploader; Test uses it to verify
// connectivity and credentials.
type bucketChecker interface {
	BucketExists(ctx context.Context, bucket string) (bool, error)
	EnsureBucket(ctx context.Context, bucket string) error
}

// Service archives recordings to object storage.
type Service struct {
	db   *gorm.DB
	zlm  *video.Client
	http *http.Client
	up   UploaderFactory

	mu    sync.Mutex
	cache map[uint]Uploader // storeID -> client, invalidated on update/delete
}

// NewService builds the service with the real S3 uploader.
func NewService(db *gorm.DB, zlm *video.Client) *Service {
	return NewServiceWithFactory(db, zlm, NewS3Uploader)
}

// NewServiceWithFactory builds the service with a custom uploader factory
// (tests).
func NewServiceWithFactory(db *gorm.DB, zlm *video.Client, factory UploaderFactory) *Service {
	return &Service{
		db:    db,
		zlm:   zlm,
		up:    factory,
		cache: map[uint]Uploader{},
		http: &http.Client{
			// Recordings can be large; only cap the wait for response headers.
			Transport: &http.Transport{ResponseHeaderTimeout: 30 * time.Second},
		},
	}
}

// Invalidate drops the cached client of a store, so the next upload picks up
// edited credentials.
func (s *Service) Invalidate(storeID uint) {
	s.mu.Lock()
	delete(s.cache, storeID)
	s.mu.Unlock()
}

// ObjectPath is the object key layout used in every bucket.
func ObjectPath(streamKey, date, file string) string {
	return path.Join("easyavr", "recordings", streamKey, date, file)
}

func (s *Service) uploader(store model.CloudStore) (Uploader, error) {
	if s.up == nil {
		return nil, errors.New("archive: no uploader configured")
	}
	if store.ID == 0 {
		return s.up(store)
	}
	s.mu.Lock()
	cached, found := s.cache[store.ID]
	s.mu.Unlock()
	if found {
		return cached, nil
	}
	up, err := s.up(store)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	s.cache[store.ID] = up
	s.mu.Unlock()
	return up, nil
}

// Upload copies a catalog recording into a store and records the object. The
// previous archive row of the recording is replaced, so re-archiving retries
// cleanly.
func (s *Service) Upload(recordingID, storeID uint) (model.RecordingArchive, error) {
	var rec model.Recording
	if err := s.db.First(&rec, recordingID).Error; err != nil {
		return model.RecordingArchive{}, fmt.Errorf("recording %d: %w", recordingID, err)
	}
	var store model.CloudStore
	if err := s.db.First(&store, storeID).Error; err != nil {
		return model.RecordingArchive{}, fmt.Errorf("storage %d: %w", storeID, err)
	}
	if !store.Enabled {
		return model.RecordingArchive{}, fmt.Errorf("storage %q is disabled", store.Name)
	}
	if store.Bucket == "" {
		return model.RecordingArchive{}, fmt.Errorf("storage %q has no bucket", store.Name)
	}

	object := ObjectPath(rec.StreamKey, rec.Date, path.Base(strings.ReplaceAll(rec.File, "\\", "/")))
	s.db.Where("recording_id = ?", rec.ID).Delete(&model.RecordingArchive{})

	arch := model.RecordingArchive{
		RecordingID: rec.ID,
		ChannelID:   rec.ChannelID,
		StreamKey:   rec.StreamKey,
		StoreID:     store.ID,
		Bucket:      store.Bucket,
		Object:      object,
	}
	size, err := s.copyFromZLM(store, object, rec)
	if err != nil {
		arch.Status = "failed"
		arch.Error = truncate(err.Error(), 500)
		// Keep the failed attempt visible; the handler still reports the error.
		_ = s.db.Create(&arch).Error
		return arch, err
	}
	arch.Size = size
	arch.Status = "uploaded"
	if err := s.db.Create(&arch).Error; err != nil {
		return arch, err
	}
	return arch, nil
}

func (s *Service) copyFromZLM(store model.CloudStore, object string, rec model.Recording) (int64, error) {
	if s.zlm == nil {
		return 0, errors.New("archive: media client is not configured")
	}
	up, err := s.uploader(store)
	if err != nil {
		return 0, err
	}
	src := s.zlm.RecordURL(rec.StreamKey, rec.Date, rec.File)
	ctx := context.Background()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, src, nil)
	if err != nil {
		return 0, err
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return 0, fmt.Errorf("download %s: %w", src, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("download %s: %s", src, resp.Status)
	}

	// Stream straight into the upload; never buffer the whole recording.
	body := io.Reader(resp.Body)
	size := resp.ContentLength
	var counter *countingReader
	if size < 0 {
		counter = &countingReader{r: resp.Body}
		body = counter
	}
	if err := up.Put(ctx, store.Bucket, object, body, size); err != nil {
		return 0, err
	}
	if counter != nil {
		return counter.n, nil
	}
	return size, nil
}

// List returns the archive rows of one recording.
func (s *Service) List(recordingID uint) ([]model.RecordingArchive, error) {
	var items []model.RecordingArchive
	err := s.db.Where("recording_id = ?", recordingID).Order("id DESC").Find(&items).Error
	return items, err
}

// ListByStatus returns a page of archive rows, optionally filtered by status.
func (s *Service) ListByStatus(status string, page, pageSize int) ([]model.RecordingArchive, int64, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 20
	}
	tx := s.db.Model(&model.RecordingArchive{})
	if status != "" {
		tx = tx.Where("status = ?", status)
	}
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var items []model.RecordingArchive
	err := tx.Order("id DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&items).Error
	return items, total, err
}

// Test verifies that the store is reachable and the bucket exists. It always
// builds a fresh client so unsaved credentials can be tested before saving.
func (s *Service) Test(ctx context.Context, store model.CloudStore) error {
	if s.up == nil {
		return errors.New("archive: no uploader configured")
	}
	if store.Endpoint == "" || store.Bucket == "" {
		return errors.New("endpoint and bucket are required")
	}
	up, err := s.up(store)
	if err != nil {
		return err
	}
	checker, ok := up.(bucketChecker)
	if !ok {
		return nil
	}
	// Test = connectivity + bucket reachable; a missing bucket is created
	// right away so the store is usable immediately after the test passes.
	return checker.EnsureBucket(ctx, store.Bucket)
}

// S3Uploader is the minio-go backed Uploader.
type S3Uploader struct {
	client *minio.Client
}

// NewS3Uploader builds an Uploader for an S3-compatible endpoint. MinIO, OSS
// and COS all speak the same API; only the endpoint, region and addressing
// style differ.
func NewS3Uploader(store model.CloudStore) (Uploader, error) {
	host, secure, err := parseEndpoint(store.Endpoint)
	if err != nil {
		return nil, err
	}
	lookup := minio.BucketLookupPath
	if !store.PathStyle {
		lookup = minio.BucketLookupDNS
	}
	client, err := minio.New(host, &minio.Options{
		Creds:        credentials.NewStaticV4(store.AccessKey, store.SecretKey, ""),
		Secure:       secure,
		Region:       store.Region,
		BucketLookup: lookup,
	})
	if err != nil {
		return nil, err
	}
	return &S3Uploader{client: client}, nil
}

func (u *S3Uploader) Put(ctx context.Context, bucket, object string, r io.Reader, size int64) error {
	_, err := u.client.PutObject(ctx, bucket, object, r, size, minio.PutObjectOptions{
		ContentType: "video/mp4",
		PartSize:    uploadPartSize,
	})
	return err
}

func (u *S3Uploader) BucketExists(ctx context.Context, bucket string) (bool, error) {
	return u.client.BucketExists(ctx, bucket)
}

// EnsureBucket creates the bucket when missing (region honored for AWS S3;
// MinIO-style endpoints ignore it).
func (u *S3Uploader) EnsureBucket(ctx context.Context, bucket string) error {
	exists, err := u.client.BucketExists(ctx, bucket)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}
	return u.client.MakeBucket(ctx, bucket, minio.MakeBucketOptions{Region: ""})
}

// parseEndpoint accepts "http://host:9000", "https://host" or a bare
// "host:9000" (treated as http, the usual MinIO deployment).
func parseEndpoint(raw string) (string, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", false, errors.New("endpoint is required")
	}
	if !strings.Contains(raw, "://") {
		raw = "http://" + raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", false, fmt.Errorf("invalid endpoint %q", raw)
	}
	return u.Host, u.Scheme == "https", nil
}

type countingReader struct {
	r io.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
