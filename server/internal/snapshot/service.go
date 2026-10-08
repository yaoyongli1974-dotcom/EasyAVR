// Package snapshot implements snapshot capture for the Video Resource Center.
// Frames are grabbed from the channel stream (via ffmpeg) and stored on disk,
// then cataloged and served by the API.
package snapshot

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
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
	tx := s.db.Model(&model.Snapshot{})
	if channelID != 0 {
		tx = tx.Where("channel_id = ?", channelID)
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
