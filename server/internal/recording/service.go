// Package recording implements the recording & playback capability of the
// Video Middle Platform: server-side recording control (delegated to
// ZLMediaKit), a playback catalog, and schedule-based automatic recording.
package recording

import (
	"context"
	"fmt"
	"log"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/video"
)

// Service manages recordings and recording plans.
type Service struct {
	db  *gorm.DB
	zlm *video.Client

	mu          sync.Mutex
	planManaged map[uint]bool // channelID -> recording was started by a plan
}

func NewService(db *gorm.DB, zlm *video.Client) *Service {
	return &Service{db: db, zlm: zlm, planManaged: map[uint]bool{}}
}

// Start begins server-side recording for a channel.
func (s *Service) Start(channelID uint) error {
	var ch model.Channel
	if err := s.db.First(&ch, channelID).Error; err != nil {
		return err
	}
	if err := s.zlm.StartRecord(ch.StreamKey); err != nil {
		return err
	}
	return s.db.Model(&ch).Update("recording", true).Error
}

// Stop ends server-side recording for a channel.
func (s *Service) Stop(channelID uint) error {
	var ch model.Channel
	if err := s.db.First(&ch, channelID).Error; err != nil {
		return err
	}
	if err := s.zlm.StopRecord(ch.StreamKey); err != nil {
		return err
	}
	s.mu.Lock()
	delete(s.planManaged, channelID)
	s.mu.Unlock()
	return s.db.Model(&ch).Update("recording", false).Error
}

// Sync pulls the recording file list for a channel/day from ZLM into the catalog.
func (s *Service) Sync(channelID uint, date string) ([]model.Recording, error) {
	var ch model.Channel
	if err := s.db.First(&ch, channelID).Error; err != nil {
		return nil, err
	}
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	files, err := s.zlm.GetMp4RecordFile(ch.StreamKey, date)
	if err != nil {
		return nil, err
	}
	for _, p := range files.Paths {
		var count int64
		s.db.Model(&model.Recording{}).Where("channel_id = ? AND file = ?", channelID, p).Count(&count)
		if count > 0 {
			continue
		}
		rec := model.Recording{
			ChannelID: channelID,
			StreamKey: ch.StreamKey,
			Date:      date,
			File:      p,
			URL:       s.zlm.RecordURL(ch.StreamKey, p),
			StartTime: parseRecordTime(date, p),
		}
		s.db.Create(&rec)
	}
	return s.List(channelID, date)
}

// List returns cataloged recordings for a channel, newest first.
func (s *Service) List(channelID uint, date string) ([]model.Recording, error) {
	tx := s.db.Order("start_time DESC")
	if channelID != 0 {
		tx = tx.Where("channel_id = ?", channelID)
	}
	if date != "" {
		tx = tx.Where("date = ?", date)
	}
	var recs []model.Recording
	err := tx.Find(&recs).Error
	return recs, err
}

// Delete removes a catalog entry. Note: the media file itself lives on the
// streaming core and is subject to ZLM-side retention.
func (s *Service) Delete(id uint) error {
	return s.db.Delete(&model.Recording{}, id).Error
}

// ---- Recording plans ----

// UpsertPlan creates or updates the recording plan of a channel.
func (s *Service) UpsertPlan(plan model.RecordingPlan) (*model.RecordingPlan, error) {
	if plan.ChannelID == 0 {
		return nil, fmt.Errorf("channelId is required")
	}
	if plan.Days == "" {
		plan.Days = "daily"
	}
	if plan.StartTime == "" {
		plan.StartTime = "00:00"
	}
	if plan.EndTime == "" {
		plan.EndTime = "23:59"
	}
	var existing model.RecordingPlan
	err := s.db.Where("channel_id = ?", plan.ChannelID).First(&existing).Error
	if err == gorm.ErrRecordNotFound {
		if err := s.db.Create(&plan).Error; err != nil {
			return nil, err
		}
		return &plan, nil
	}
	if err != nil {
		return nil, err
	}
	existing.Enabled = plan.Enabled
	existing.Days = plan.Days
	existing.StartTime = plan.StartTime
	existing.EndTime = plan.EndTime
	existing.RetentionDays = plan.RetentionDays
	existing.StreamType = plan.StreamType
	if err := s.db.Save(&existing).Error; err != nil {
		return nil, err
	}
	return &existing, nil
}

func (s *Service) GetPlan(channelID uint) (*model.RecordingPlan, error) {
	var plan model.RecordingPlan
	if err := s.db.Where("channel_id = ?", channelID).First(&plan).Error; err != nil {
		return nil, err
	}
	return &plan, nil
}

// StartScheduler evaluates recording plans on a fixed interval until ctx ends.
func (s *Service) StartScheduler(ctx context.Context, interval time.Duration) {
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		s.evaluate()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				s.evaluate()
			}
		}
	}()
}

func (s *Service) evaluate() {
	var plans []model.RecordingPlan
	s.db.Where("enabled = ?", true).Find(&plans)
	now := time.Now()
	for _, plan := range plans {
		desired := planMatches(plan, now)
		s.mu.Lock()
		active := s.planManaged[plan.ChannelID]
		s.mu.Unlock()
		if desired && !active {
			if err := s.Start(plan.ChannelID); err != nil {
				log.Printf("[recording] plan start channel %d: %v", plan.ChannelID, err)
				continue
			}
			s.mu.Lock()
			s.planManaged[plan.ChannelID] = true
			s.mu.Unlock()
			log.Printf("[recording] plan started channel %d", plan.ChannelID)
		} else if !desired && active {
			if err := s.Stop(plan.ChannelID); err != nil {
				log.Printf("[recording] plan stop channel %d: %v", plan.ChannelID, err)
				continue
			}
			log.Printf("[recording] plan stopped channel %d", plan.ChannelID)
		}
		if desired && plan.RetentionDays > 0 {
			s.cleanup(plan, now)
		}
	}
}

// cleanup drops catalog entries older than the plan retention window.
func (s *Service) cleanup(plan model.RecordingPlan, now time.Time) {
	cutoff := now.AddDate(0, 0, -plan.RetentionDays).Format("2006-01-02")
	s.db.Where("channel_id = ? AND date < ?", plan.ChannelID, cutoff).Delete(&model.Recording{})
}

func planMatches(plan model.RecordingPlan, now time.Time) bool {
	if !dayMatches(plan.Days, now.Weekday()) {
		return false
	}
	cur := now.Format("15:04")
	if plan.StartTime <= plan.EndTime {
		return cur >= plan.StartTime && cur <= plan.EndTime
	}
	// Overnight window, e.g. 22:00 -> 06:00.
	return cur >= plan.StartTime || cur <= plan.EndTime
}

func dayMatches(days string, wd time.Weekday) bool {
	switch strings.ToLower(days) {
	case "", "daily", "all":
		return true
	case "workday", "weekday":
		return wd >= time.Monday && wd <= time.Friday
	case "weekend":
		return wd == time.Saturday || wd == time.Sunday
	}
	for _, part := range strings.Split(days, ",") {
		n, err := strconv.Atoi(strings.TrimSpace(part))
		if err != nil {
			continue
		}
		// Accept both 1(Mon)..7(Sun) and 0(Sun)..6(Sat).
		if n == int(wd) || (n == 7 && wd == time.Sunday) || (n == 0 && wd == time.Sunday) {
			return true
		}
	}
	return false
}

// parseRecordTime parses a ZLM record path like "2024-01-02/14-30-00.mp4".
func parseRecordTime(date, path string) time.Time {
	base := strings.TrimSuffix(path, ".mp4")
	parts := strings.Split(base, "/")
	name := parts[len(parts)-1]
	t, err := time.ParseInLocation("2006-01-02 15-04-05", date+" "+name, time.Local)
	if err != nil {
		return time.Now()
	}
	return t
}
