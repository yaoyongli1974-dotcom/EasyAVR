// Package recording implements the recording & playback capability of the
// Video Middle Platform: server-side recording control (delegated to
// ZLMediaKit), a playback catalog, and schedule-based automatic recording.
package recording

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/video"
)

const (
	archiveStatusUploaded = "uploaded"
	// How many days of ZLM directories are scanned for orphan files when the
	// catalog has no row for them. Keep the bound so a report stays cheap.
	orphanLookbackDays = 30
	sizeProbeTimeout   = 2 * time.Second
)

// CleanupSkipped counts the catalog entries a cleanup run deliberately kept.
type CleanupSkipped struct {
	Marked     int64 `json:"marked"`
	Active     int64 `json:"active"`
	Unarchived int64 `json:"unarchived"`
}

// CleanupResult is the outcome of one cleanup pass.
type CleanupResult struct {
	Deleted int64          `json:"deleted"`
	Skipped CleanupSkipped `json:"skipped"`
}

// Service manages recordings and recording plans.
type Service struct {
	db   *gorm.DB
	zlm  *video.Client
	http *http.Client

	mu          sync.Mutex
	planManaged map[uint]bool // channelID -> recording was started by a plan

	statsMu     sync.Mutex
	lastCleanup CleanupResult
}

func NewService(db *gorm.DB, zlm *video.Client) *Service {
	return &Service{
		db: db, zlm: zlm, planManaged: map[uint]bool{},
		http: &http.Client{Timeout: sizeProbeTimeout},
	}
}

// LastCleanup returns the statistics of the most recent Cleanup call.
func (s *Service) LastCleanup() CleanupResult {
	s.statsMu.Lock()
	defer s.statsMu.Unlock()
	return s.lastCleanup
}

// probeSize best-effort reads a recording's size from ZLM's HTTP headers.
// getMp4RecordFile itself reports paths only, so the metadata is frequently
// unavailable; 0 tells the caller to leave SizeBytes at zero.
func (s *Service) probeSize(rawURL string) int64 {
	client := s.http
	if client == nil {
		client = &http.Client{Timeout: sizeProbeTimeout}
	}
	resp, err := client.Head(rawURL)
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK || resp.ContentLength <= 0 {
		return 0
	}
	return resp.ContentLength
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
	if err := s.db.Model(&ch).Update("recording", false).Error; err != nil {
		return err
	}
	// The finished segments only become visible in the catalog once ZLM closes
	// the file, so sync now instead of waiting for the manual sync button.
	s.syncRecent(channelID)
	return nil
}

// syncRecent catalogs the segments ZLM has just finished. Segments are named
// after their start time, so a recording that began before midnight lives in
// the previous day's folder.
func (s *Service) syncRecent(channelID uint) {
	now := time.Now()
	dates := []string{now.Format("2006-01-02")}
	if now.Hour() < 2 {
		dates = append(dates, now.AddDate(0, 0, -1).Format("2006-01-02"))
	}
	for _, d := range dates {
		if _, err := s.Sync(channelID, d); err != nil {
			log.Printf("[recording] catalog sync channel %d %s: %v", channelID, d, err)
		}
	}
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
		s.db.Model(&model.Recording{}).Where("channel_id = ? AND date = ? AND file = ?", channelID, date, p).Count(&count)
		if count > 0 {
			continue
		}
		rec := model.Recording{
			ChannelID: channelID,
			StreamKey: ch.StreamKey,
			Date:      date,
			File:      p,
			URL:       s.zlm.RecordURL(ch.StreamKey, date, p),
			StartTime: parseRecordTime(date, p),
		}
		// ZLM's file listing carries no byte count. A HEAD on the media URL
		// sometimes exposes Content-Length; when it does not, SizeBytes stays 0
		// so capacity reporting never invents data.
		if size := s.probeSize(rec.URL); size > 0 {
			rec.SizeBytes = size
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
			cutoff := now.AddDate(0, 0, -plan.RetentionDays).Format("2006-01-02")
			if r := s.pruneBefore(plan.ChannelID, s.channelStreamKey(plan.ChannelID), cutoff, true, now); r.Deleted > 0 {
				log.Printf("[recording] retention pruned %d entries for channel %d", r.Deleted, plan.ChannelID)
			}
		}
	}
}

// channelStreamKey looks up a channel's stream key, "" when unknown.
func (s *Service) channelStreamKey(channelID uint) string {
	var ch model.Channel
	if err := s.db.Select("stream_key").First(&ch, channelID).Error; err != nil {
		return ""
	}
	return ch.StreamKey
}

// channelActive reports whether a channel is still writing recordings, either
// through an explicit start or a running plan.
func (s *Service) channelActive(channelID uint) bool {
	var ch model.Channel
	if s.db.Select("recording").First(&ch, channelID).Error == nil && ch.Recording {
		return true
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.planManaged[channelID]
}

// hasArchives tells whether the platform uses cloud archiving at all. When no
// archive row has ever been written there is no "remote copy" concept to
// protect, so retention keeps its historical behaviour.
func (s *Service) hasArchives() bool {
	var n int64
	s.db.Model(&model.RecordingArchive{}).Count(&n)
	return n > 0
}

func (s *Service) hasUploadedArchive(recordingID uint) bool {
	var n int64
	s.db.Model(&model.RecordingArchive{}).
		Where("recording_id = ? AND status = ?", recordingID, archiveStatusUploaded).Count(&n)
	return n > 0
}

// hasOtherCopy is true when another catalog row points at the same media file;
// dropping one row then does not destroy the only reference to the file.
func (s *Service) hasOtherCopy(rec model.Recording) bool {
	var n int64
	s.db.Model(&model.Recording{}).
		Where("stream_key = ? AND date = ? AND file = ? AND id <> ?", rec.StreamKey, rec.Date, rec.File, rec.ID).
		Count(&n)
	return n > 0
}

// pruneBefore drops a channel's expired catalog entries and (optionally) the
// media files whose recording date is before cutoff. Protected entries (marked,
// live channel, sole unarchived copy) survive; a day folder is only removed
// when none of its recordings is protected and no catalog row still points at
// it. Segments are keyed by day, so file deletion granularity is the date
// folder on the media core.
func (s *Service) pruneBefore(channelID uint, streamKey, cutoff string, deleteFiles bool, now time.Time) CleanupResult {
	var res CleanupResult
	var recs []model.Recording
	if err := s.db.Where("channel_id = ? AND date < ?", channelID, cutoff).Find(&recs).Error; err != nil {
		return res
	}
	if len(recs) == 0 {
		return res
	}

	active := s.channelActive(channelID)
	// files=0 only prunes the catalog: the media stays put, so the sole-copy
	// protection is deliberately relaxed for that mode (录像存储阈值 files=0).
	protectUnarchived := deleteFiles && s.hasArchives()

	var deletable []uint
	protectedDates := map[string]bool{}
	for _, rec := range recs {
		switch {
		case rec.Marked:
			res.Skipped.Marked++
			protectedDates[rec.Date] = true
		case active:
			res.Skipped.Active++
			protectedDates[rec.Date] = true
		case protectUnarchived && !s.hasUploadedArchive(rec.ID) && !s.hasOtherCopy(rec):
			res.Skipped.Unarchived++
			protectedDates[rec.Date] = true
		default:
			deletable = append(deletable, rec.ID)
		}
	}

	if len(deletable) > 0 {
		del := s.db.Where("id IN ?", deletable).Delete(&model.Recording{})
		res.Deleted = del.RowsAffected
	}

	if deleteFiles && streamKey != "" {
		var remaining []string
		s.db.Model(&model.Recording{}).Where("stream_key = ?", streamKey).Distinct().Pluck("date", &remaining)
		stillUsed := map[string]bool{}
		for _, d := range remaining {
			stillUsed[d] = true
		}
		dates := map[string]bool{}
		for _, rec := range recs {
			if !protectedDates[rec.Date] && !stillUsed[rec.Date] {
				dates[rec.Date] = true
			}
		}
		for d := range dates {
			if err := s.zlm.DeleteRecordDir(streamKey, d); err != nil {
				log.Printf("[recording] delete files channel %d %s: %v", channelID, d, err)
			}
		}
	}
	return res
}

// Cleanup prunes expired recordings. days > 0 applies a window to every
// channel; otherwise each channel plan's RetentionDays decides. deleteFiles
// also removes the date folders from the media core (录像存储阈值自动删除).
// The skip breakdown is kept in memory and readable via LastCleanup.
func (s *Service) Cleanup(days int, deleteFiles bool) (int64, error) {
	now := time.Now()
	total := CleanupResult{}
	if days > 0 {
		cutoff := now.AddDate(0, 0, -days).Format("2006-01-02")
		var channels []model.Channel
		s.db.Find(&channels)
		for _, ch := range channels {
			total.add(s.pruneBefore(ch.ID, ch.StreamKey, cutoff, deleteFiles, now))
		}
	} else {
		var plans []model.RecordingPlan
		s.db.Where("retention_days > 0").Find(&plans)
		for _, p := range plans {
			cutoff := now.AddDate(0, 0, -p.RetentionDays).Format("2006-01-02")
			total.add(s.pruneBefore(p.ChannelID, s.channelStreamKey(p.ChannelID), cutoff, deleteFiles, now))
		}
	}
	s.statsMu.Lock()
	s.lastCleanup = total
	s.statsMu.Unlock()
	return total.Deleted, nil
}

func (r *CleanupResult) add(other CleanupResult) {
	r.Deleted += other.Deleted
	r.Skipped.Marked += other.Skipped.Marked
	r.Skipped.Active += other.Skipped.Active
	r.Skipped.Unarchived += other.Skipped.Unarchived
}

// normRecordFile reduces ZLM's two path spellings (bare file name or dated
// relative path) to a comparable base name.
func normRecordFile(p string) string {
	return filepath.Base(strings.ReplaceAll(p, "\\", "/"))
}

// Consistency compares the catalog with ZLM without changing either side.
// missing holds IDs of catalog rows whose file is no longer on the media core;
// orphan holds playback URLs of files ZLM reports that have no catalog row.
func (s *Service) Consistency() (missing []uint, orphan []string, err error) {
	missing = []uint{}
	orphan = []string{}

	var recs []model.Recording
	if err := s.db.Find(&recs).Error; err != nil {
		return missing, orphan, err
	}

	catalog := map[string]bool{}
	pairs := map[[2]string]bool{}
	streams := map[string]bool{}
	for _, rec := range recs {
		streams[rec.StreamKey] = true
		catalog[rec.StreamKey+"\x00"+rec.Date+"\x00"+normRecordFile(rec.File)] = true
		pairs[[2]string{rec.StreamKey, rec.Date}] = true
	}
	// Also scan a recent window per stream, otherwise an orphan in a date with
	// no catalog row at all could never be discovered.
	today := time.Now()
	for key := range streams {
		for i := 0; i < orphanLookbackDays; i++ {
			pairs[[2]string{key, today.AddDate(0, 0, -i).Format("2006-01-02")}] = true
		}
	}

	onZLM := map[string]bool{}
	queried := map[[2]string]bool{}
	var firstErr error
	for pair := range pairs {
		files, qerr := s.zlm.GetMp4RecordFile(pair[0], pair[1])
		if qerr != nil {
			// An empty/old day folder can be reported as an error by some ZLM
			// builds; only fail the report when no day could be inspected.
			if firstErr == nil {
				firstErr = qerr
			}
			continue
		}
		queried[pair] = true
		for _, p := range files.Paths {
			key := pair[0] + "\x00" + pair[1] + "\x00" + normRecordFile(p)
			onZLM[key] = true
			if !catalog[key] {
				orphan = append(orphan, s.zlm.RecordURL(pair[0], pair[1], p))
			}
		}
	}
	for _, rec := range recs {
		pair := [2]string{rec.StreamKey, rec.Date}
		if queried[pair] && !onZLM[rec.StreamKey+"\x00"+rec.Date+"\x00"+normRecordFile(rec.File)] {
			missing = append(missing, rec.ID)
		}
	}
	if len(queried) == 0 {
		err = firstErr
	}
	return missing, orphan, err
}

// Capacity sums locally cataloged bytes and archived object bytes. A recording
// with an unavailable ZLM size simply contributes 0 until the next Sync.
func (s *Service) Capacity() (totalBytes int64, localBytes int64, archivedBytes int64, err error) {
	if err = s.db.Model(&model.Recording{}).Select("COALESCE(SUM(size_bytes), 0)").Row().Scan(&localBytes); err != nil {
		return 0, 0, 0, err
	}
	if err = s.db.Model(&model.RecordingArchive{}).Select("COALESCE(SUM(size), 0)").Row().Scan(&archivedBytes); err != nil {
		return 0, 0, 0, err
	}
	return localBytes + archivedBytes, localBytes, archivedBytes, nil
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

// parseRecordTime parses the names ZLM reports for a segment. Depending on the
// build it returns a bare "2024-01-02-14-30-00-0.mp4" or a dated path such as
// "2024-01-02/14-30-00.mp4"; both must land on the real start time.
func parseRecordTime(date, path string) time.Time {
	name := strings.TrimSuffix(filepath.Base(path), ".mp4")
	candidates := []string{name}
	if len(name) > 19 {
		candidates = append(candidates, name[:19])
	}
	for _, cand := range candidates {
		if t, err := time.ParseInLocation("2006-01-02-15-04-05", cand, time.Local); err == nil {
			return t
		}
		if t, err := time.ParseInLocation("2006-01-02 15-04-05", date+" "+cand, time.Local); err == nil {
			return t
		}
	}
	return time.Now()
}
