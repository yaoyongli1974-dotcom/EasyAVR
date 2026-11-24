// Package snapshot implements snapshot capture for the Video Resource Center.
// Frames are grabbed from the channel stream (via ffmpeg) and stored on disk,
// then cataloged and served by the API.
package snapshot

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/media"
	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/video"
)

// RetentionConfig governs automatic snapshot cleanup. Days <= 0 disables
// age-based expiry; MaxDiskUsageMB <= 0 disables the disk usage cap; a
// non-positive CleanupInterval disables the scheduler.
type RetentionConfig struct {
	Days            int
	MaxDiskUsageMB  int
	CleanupInterval time.Duration
	MaxPerRun       int
}

const (
	defaultRetentionDays    = 30
	defaultCleanupInterval  = time.Hour
	defaultMaxCleanupPerRun = 200
)

// Service captures and catalogs snapshots.
type Service struct {
	db  *gorm.DB
	zlm *video.Client
	dir string

	// retentionMu guards the runtime retention config and the last-run stats.
	retentionMu   sync.RWMutex
	retention     RetentionConfig
	lastCleanupAt time.Time
	lastDeleted   int
	lastErr       string

	// cleanupMu makes Cleanup runs mutually exclusive (scheduler ticks and
	// manual API calls must never delete the same row twice).
	cleanupMu sync.Mutex
}

func NewService(db *gorm.DB, zlm *video.Client, dir string) *Service {
	return &Service{
		db:  db,
		zlm: zlm,
		dir: dir,
		retention: RetentionConfig{
			Days:            defaultRetentionDays,
			CleanupInterval: defaultCleanupInterval,
			MaxPerRun:       defaultMaxCleanupPerRun,
		},
	}
}

// Dir returns the root directory where snapshots are stored.
func (s *Service) Dir() string { return s.dir }

// SetRetention replaces the runtime retention config. A non-positive
// MaxPerRun is normalized to the default batch size so that the value exposed
// to the API matches the value actually enforced by Cleanup.
func (s *Service) SetRetention(cfg RetentionConfig) {
	if cfg.MaxPerRun <= 0 {
		cfg.MaxPerRun = defaultMaxCleanupPerRun
	}
	s.retentionMu.Lock()
	defer s.retentionMu.Unlock()
	s.retention = cfg
}

// Retention returns the current runtime retention config.
func (s *Service) Retention() RetentionConfig {
	s.retentionMu.RLock()
	defer s.retentionMu.RUnlock()
	return s.retention
}

// RetentionStats reports the outcome of the most recent cleanup run.
func (s *Service) RetentionStats() (lastCleanupAt time.Time, lastDeleted int, lastErr string) {
	s.retentionMu.RLock()
	defer s.retentionMu.RUnlock()
	return s.lastCleanupAt, s.lastDeleted, s.lastErr
}

// Capture grabs a frame from the channel and stores it as a snapshot.
func (s *Service) Capture(ctx context.Context, channelID uint) (*model.Snapshot, error) {
	var ch model.Channel
	if err := s.db.First(&ch, channelID).Error; err != nil {
		return nil, err
	}
	source := ch.SourceURL
	if source == "" {
		source = s.zlm.PlayURLs(ch.StreamKey)["rtsp"]
	}
	frame, err := media.GrabFrame(ctx, source)
	if err != nil {
		return nil, err
	}
	sub := filepath.Join(s.dir, fmt.Sprintf("%d", channelID))
	if err := os.MkdirAll(sub, 0o755); err != nil {
		return nil, err
	}
	name := fmt.Sprintf("%d.jpg", time.Now().UnixMilli())
	abs := filepath.Join(sub, name)
	if err := os.WriteFile(abs, frame, 0o644); err != nil {
		return nil, err
	}
	snap := model.Snapshot{
		ChannelID: channelID,
		Path:      filepath.Join(fmt.Sprintf("%d", channelID), name),
		URL:       fmt.Sprintf("/snapshots/%d/%s", channelID, name),
		SizeBytes: int64(len(frame)),
		TakenAt:   time.Now(),
	}
	if err := s.db.Create(&snap).Error; err != nil {
		return nil, err
	}
	return &snap, nil
}

// List returns snapshots for a channel (or all when channelID is 0).
func (s *Service) List(channelID uint, page, pageSize int) ([]model.Snapshot, int64, error) {
	return s.ListForChannels(channelID, nil, page, pageSize)
}

// ListForChannels returns snapshots restricted to allowed channels. A nil
// allowed slice means every channel is visible; a non-nil empty slice means
// none are visible. channelID further narrows the result when non-zero.
func (s *Service) ListForChannels(channelID uint, allowed []uint, page, pageSize int) ([]model.Snapshot, int64, error) {
	if allowed != nil && len(allowed) == 0 {
		return []model.Snapshot{}, 0, nil
	}
	tx := s.db.Model(&model.Snapshot{})
	if channelID != 0 {
		tx = tx.Where("channel_id = ?", channelID)
	}
	if allowed != nil {
		tx = tx.Where("channel_id IN ?", allowed)
	}
	var total int64
	tx.Count(&total)
	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 {
		pageSize = 24
	}
	var snaps []model.Snapshot
	err := tx.Order("taken_at DESC").Offset((page - 1) * pageSize).Limit(pageSize).Find(&snaps).Error
	return snaps, total, err
}

// Delete removes a snapshot catalog entry and its file.
func (s *Service) Delete(id uint) error {
	var snap model.Snapshot
	if err := s.db.First(&snap, id).Error; err != nil {
		return err
	}
	if snap.Path != "" {
		_ = os.Remove(filepath.Join(s.dir, snap.Path))
	}
	return s.db.Delete(&model.Snapshot{}, id).Error
}

// Cleanup prunes expired snapshots. A snapshot is expired when its TakenAt is
// older than the configured retention window; when a disk usage cap is set the
// oldest expired snapshots are evicted until the cap is met. Snapshots that
// are still within the window are NEVER deleted, even under disk pressure.
// Snapshots still referenced by an AI event or a dataset sample are skipped.
// Files are removed before catalog rows; a missing file is treated as success
// so orphan rows can be cleared, while other failures are logged and skipped.
func (s *Service) Cleanup() (deleted int, bytes int64, err error) {
	s.cleanupMu.Lock()
	defer s.cleanupMu.Unlock()

	s.retentionMu.RLock()
	cfg := s.retention
	s.retentionMu.RUnlock()

	now := time.Now()
	defer func() {
		s.retentionMu.Lock()
		s.lastCleanupAt = now
		s.lastDeleted = deleted
		s.lastErr = ""
		if err != nil {
			s.lastErr = err.Error()
		}
		s.retentionMu.Unlock()
	}()

	// Snapshot URLs still in use by the AI event center or by dataset samples.
	// Exact matches only: a longer/shorter URL must not protect a snapshot.
	referenced := make(map[string]bool)
	var eventURLs []string
	if err := s.db.Model(&model.AIEvent{}).Where("snapshot <> ?", "").Pluck("snapshot", &eventURLs).Error; err != nil {
		return 0, 0, err
	}
	for _, u := range eventURLs {
		referenced[u] = true
	}
	var sampleURLs []string
	if err := s.db.Model(&model.DatasetSample{}).Where("image_url <> ?", "").Pluck("image_url", &sampleURLs).Error; err != nil {
		return 0, 0, err
	}
	for _, u := range sampleURLs {
		referenced[u] = true
	}

	// Only age-expired snapshots are eligible, oldest first. Days <= 0 means
	// retention is disabled and nothing is eligible.
	var eligible []model.Snapshot
	if cfg.Days > 0 {
		cutoff := now.AddDate(0, 0, -cfg.Days)
		if err := s.db.Where("taken_at < ?", cutoff).Order("taken_at ASC").Find(&eligible).Error; err != nil {
			return 0, 0, err
		}
	}

	if cfg.MaxDiskUsageMB > 0 {
		var usage int64
		s.db.Model(&model.Snapshot{}).Select("COALESCE(SUM(size_bytes),0)").Scan(&usage)
		limit := int64(cfg.MaxDiskUsageMB) * 1024 * 1024
		if usage > limit {
			log.Printf("[snapshot] retention: usage %d bytes exceeds cap %d MB, evicting oldest expired snapshots", usage, cfg.MaxDiskUsageMB)
		}
	}

	maxPerRun := cfg.MaxPerRun
	if maxPerRun <= 0 {
		maxPerRun = defaultMaxCleanupPerRun
	}
	for _, snap := range eligible {
		if deleted >= maxPerRun {
			break
		}
		if referenced[snap.URL] {
			continue
		}
		abs, err := s.resolve(snap.Path, false)
		if err != nil {
			log.Printf("[snapshot] retention: skip snapshot %d: %v", snap.ID, err)
			continue
		}
		if err := os.Remove(abs); err != nil && !os.IsNotExist(err) {
			log.Printf("[snapshot] retention: remove file for snapshot %d: %v", snap.ID, err)
			continue
		}
		if err := s.db.Delete(&model.Snapshot{}, snap.ID).Error; err != nil {
			log.Printf("[snapshot] retention: delete row %d: %v", snap.ID, err)
			continue
		}
		deleted++
		bytes += snap.SizeBytes
	}
	return deleted, bytes, nil
}

// StartCleanupScheduler runs Cleanup on the configured interval until ctx is
// cancelled. A non-positive interval disables the scheduler. Overlapping runs
// are prevented by cleanupMu (which also guards manual API-triggered runs).
func (s *Service) StartCleanupScheduler(ctx context.Context) {
	go func() {
		s.retentionMu.RLock()
		interval := s.retention.CleanupInterval
		s.retentionMu.RUnlock()
		if interval <= 0 {
			return
		}
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if _, _, err := s.Cleanup(); err != nil {
					log.Printf("[snapshot] scheduled cleanup: %v", err)
				}
			}
		}
	}()
}

// StartScheduler periodically captures snapshots for channels with
// snapshotEnabled set, respecting each channel's interval.
func (s *Service) StartScheduler(ctx context.Context, tick time.Duration) {
	go func() {
		t := time.NewTicker(tick)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				s.runDue(ctx)
			}
		}
	}()
}

func (s *Service) runDue(ctx context.Context) {
	var channels []model.Channel
	s.db.Where("snapshot_enabled = ?", true).Find(&channels)
	for _, ch := range channels {
		interval := ch.SnapshotInterval
		if interval <= 0 {
			interval = 300
		}
		var last model.Snapshot
		err := s.db.Where("channel_id = ?", ch.ID).Order("taken_at DESC").First(&last).Error
		if err == nil && time.Since(last.TakenAt) < time.Duration(interval)*time.Second {
			continue
		}
		if _, err := s.Capture(ctx, ch.ID); err != nil {
			log.Printf("[snapshot] channel %d: %v", ch.ID, err)
		}
	}
}

// DefaultTokenTTL is the lifetime of a snapshot access token when none is
// supplied.
const DefaultTokenTTL = 5 * time.Minute

const tokenPrefix = "s"

var (
	ErrEmptySecret  = errors.New("snapshot token secret is empty")
	ErrInvalidToken = errors.New("invalid snapshot access token")
	ErrExpiredToken = errors.New("snapshot access token expired")
	ErrPathOutside  = errors.New("snapshot path outside storage directory")
	// ErrPathNotFound wraps fs.ErrNotExist so callers can distinguish a
	// missing file (404) from an escaping path (403) without leaking a path.
	ErrPathNotFound = fmt.Errorf("snapshot file does not exist: %w", fs.ErrNotExist)
)

type accessPayload struct {
	ID        uint  `json:"id"`
	UserID    uint  `json:"uid"`
	ExpiresAt int64 `json:"exp"` // Unix milliseconds
}

// SnapshotAccessToken signs a short-lived token bound to a snapshot and user.
// The token is "s" + base64url(payload) + "." + base64url(HMAC-SHA256 sig).
// A zero ttl uses DefaultTokenTTL; a negative ttl issues an already-expired
// token (used to exercise expiry handling).
func SnapshotAccessToken(id, user uint, ttl time.Duration, secret string) (string, time.Time, error) {
	if secret == "" {
		return "", time.Time{}, ErrEmptySecret
	}
	if ttl == 0 {
		ttl = DefaultTokenTTL
	}
	expires := time.Now().Add(ttl)
	raw, err := json.Marshal(accessPayload{ID: id, UserID: user, ExpiresAt: expires.UnixMilli()})
	if err != nil {
		return "", time.Time{}, err
	}
	encoded := base64.RawURLEncoding.EncodeToString(raw)
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(encoded))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return tokenPrefix + encoded + "." + sig, expires, nil
}

// ParseSnapshotAccessToken verifies the signature and expiry and returns the
// bound snapshot ID, user ID and expiry. Any payload tampering (including a
// changed snapshot ID) invalidates the signature.
func ParseSnapshotAccessToken(token, secret string) (uint, uint, time.Time, error) {
	if secret == "" {
		return 0, 0, time.Time{}, ErrEmptySecret
	}
	if !strings.HasPrefix(token, tokenPrefix) {
		return 0, 0, time.Time{}, ErrInvalidToken
	}
	parts := strings.Split(token[len(tokenPrefix):], ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return 0, 0, time.Time{}, ErrInvalidToken
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return 0, 0, time.Time{}, ErrInvalidToken
	}
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(parts[0]))
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return 0, 0, time.Time{}, ErrInvalidToken
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return 0, 0, time.Time{}, ErrInvalidToken
	}
	var p accessPayload
	if err := json.Unmarshal(raw, &p); err != nil || p.ID == 0 {
		return 0, 0, time.Time{}, ErrInvalidToken
	}
	expires := time.UnixMilli(p.ExpiresAt)
	if !time.Now().Before(expires) {
		return 0, 0, time.Time{}, ErrExpiredToken
	}
	return p.ID, p.UserID, expires, nil
}

// OpenSnapshot opens a catalog-relative snapshot path for reading and returns
// the already-opened file. Unlike ResolvePath, it never hands a path back to
// the caller: the platform layer resolves the name from a pinned root
// directory fd in one operation, so replacing a parent directory (or the final
// component) after the check cannot redirect the read outside the snapshot
// root. Only regular files are returned; all errors use the fixed
// ErrPathOutside / ErrPathNotFound sentinels and never contain local paths.
func (s *Service) OpenSnapshot(rel string) (*os.File, error) {
	if !validSnapshotRelativePath(rel) {
		return nil, ErrPathOutside
	}
	root, err := newSnapshotRoot(s.dir)
	if err != nil {
		return nil, ErrPathOutside
	}
	defer func() { _ = root.close() }()

	f, err := root.open(rel)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, ErrPathOutside
	}
	if !info.Mode().IsRegular() {
		_ = f.Close()
		return nil, ErrPathOutside
	}
	return f, nil
}

// validSnapshotRelativePath performs the lexical checks that both the strict
// Linux path and the portable fallback rely on. A path that leaves the root
// syntactically is never passed to the platform layer.
func validSnapshotRelativePath(rel string) bool {
	if rel == "" || filepath.IsAbs(rel) {
		return false
	}
	clean := filepath.Clean(rel)
	return clean != "." && clean != ".." && !strings.HasPrefix(clean, ".."+string(os.PathSeparator))
}

// snapshotOpenError maps platform open failures onto the two public
// sentinels. Only a genuinely missing file is ErrPathNotFound; escapes,
// symlinks, magic links, permissions and all other failures are ErrPathOutside
// so the HTTP layer answers 403 rather than pretending the file vanished.
func snapshotOpenError(err error) error {
	if errors.Is(err, fs.ErrNotExist) {
		return ErrPathNotFound
	}
	return ErrPathOutside
}

// ResolvePath converts a catalog-relative snapshot path to its canonical,
// symlink-free absolute path inside the storage directory. It is retained for
// non-read paths such as retention cleanup; reads must use OpenSnapshot so the
// check and the open cannot be separated. It rejects empty
// and absolute paths, traversal (including sibling directories with a
// matching prefix) and any symlink — including multi-level chains — whose
// resolved target escapes the snapshot root. A missing path returns
// ErrPathNotFound; all errors are fixed strings that never include a local
// path.
func (s *Service) ResolvePath(rel string) (string, error) {
	return s.resolve(rel, true)
}

// resolve implements ResolvePath. requireExists=false is used by retention
// cleanup, where an orphan row's missing file must still be removable; the
// lexical checks and symlink validation stay identical in both modes.
func (s *Service) resolve(rel string, requireExists bool) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", ErrPathOutside
	}
	root, err := filepath.Abs(s.dir)
	if err != nil {
		return "", ErrPathOutside
	}
	root = filepath.Clean(root)
	// Resolve the root itself so a symlinked storage directory compares
	// against its real location rather than the symlink path.
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", ErrPathOutside
	}
	realRoot = filepath.Clean(realRoot)

	target := filepath.Clean(filepath.Join(root, rel))
	if !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", ErrPathOutside
	}

	// EvalSymlinks follows every symlink component, so a link that points
	// outside the root (directly or through a chain) is detected here.
	resolved, err := filepath.EvalSymlinks(target)
	if err == nil {
		if !pathWithin(resolved, realRoot) {
			return "", ErrPathOutside
		}
		return resolved, nil
	}
	if !errors.Is(err, fs.ErrNotExist) {
		return "", ErrPathOutside
	}
	// A dangling symlink at the terminal component cannot be proven to be
	// inside the root, so reject it rather than letting a later open follow it.
	if info, lerr := os.Lstat(target); lerr == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", ErrPathOutside
	}
	if requireExists {
		return "", ErrPathNotFound
	}

	// Missing terminal path (cleanup only): validate each existing ancestor
	// so a symlinked parent cannot redirect the removal outside the root.
	for p := target; p != root && p != filepath.Dir(p); p = filepath.Dir(p) {
		info, lerr := os.Lstat(p)
		if lerr != nil {
			if errors.Is(lerr, fs.ErrNotExist) {
				continue
			}
			return "", ErrPathOutside
		}
		if info.Mode()&os.ModeSymlink != 0 {
			r, rerr := filepath.EvalSymlinks(p)
			if rerr != nil || !pathWithin(r, realRoot) {
				return "", ErrPathOutside
			}
		}
	}
	return target, nil
}

func pathWithin(path, root string) bool {
	return path == root || strings.HasPrefix(path, root+string(os.PathSeparator))
}
