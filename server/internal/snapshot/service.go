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
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/media"
	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/video"
)

// Service captures and catalogs snapshots.
type Service struct {
	db  *gorm.DB
	zlm *video.Client
	dir string
}

func NewService(db *gorm.DB, zlm *video.Client, dir string) *Service {
	return &Service{db: db, zlm: zlm, dir: dir}
}

// Dir returns the root directory where snapshots are stored.
func (s *Service) Dir() string { return s.dir }

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

// ResolvePath converts a catalog-relative snapshot path to an absolute path,
// rejecting absolute paths, empty paths and any traversal outside the storage
// directory (siblings such as <dir>-evil are rejected too).
func (s *Service) ResolvePath(rel string) (string, error) {
	if rel == "" || filepath.IsAbs(rel) {
		return "", ErrPathOutside
	}
	root, err := filepath.Abs(s.dir)
	if err != nil {
		return "", err
	}
	root = filepath.Clean(root)
	target := filepath.Clean(filepath.Join(root, rel))
	if !strings.HasPrefix(target, root+string(os.PathSeparator)) {
		return "", ErrPathOutside
	}
	return target, nil
}
