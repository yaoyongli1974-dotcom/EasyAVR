// Package event implements the AI Event Center: unified storage, querying and
// statistics over events produced by CV/VLM/LLM capabilities.
package event

import (
	"time"

	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/model"
)

// Service provides queries over AI events.
type Service struct {
	db *gorm.DB
}

func NewService(db *gorm.DB) *Service {
	return &Service{db: db}
}

// Query describes an event search request.
type Query struct {
	ChannelID uint
	Kind      string
	Level     string
	EventType string
	Keyword   string
	From      *time.Time
	To        *time.Time
	Page      int
	PageSize  int
}

// List returns events matching the query plus the total count.
func (s *Service) List(q Query) ([]model.AIEvent, int64, error) {
	tx := s.db.Model(&model.AIEvent{})
	if q.ChannelID != 0 {
		tx = tx.Where("channel_id = ?", q.ChannelID)
	}
	if q.Kind != "" {
		tx = tx.Where("kind = ?", q.Kind)
	}
	if q.Level != "" {
		tx = tx.Where("level = ?", q.Level)
	}
	if q.EventType != "" {
		tx = tx.Where("event_type = ?", q.EventType)
	}
	if q.Keyword != "" {
		like := "%" + q.Keyword + "%"
		tx = tx.Where("summary LIKE ? OR payload LIKE ?", like, like)
	}
	if q.From != nil {
		tx = tx.Where("occurred_at >= ?", *q.From)
	}
	if q.To != nil {
		tx = tx.Where("occurred_at <= ?", *q.To)
	}
	var total int64
	if err := tx.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	if q.PageSize <= 0 {
		q.PageSize = 20
	}
	if q.Page <= 0 {
		q.Page = 1
	}
	var events []model.AIEvent
	err := tx.Order("occurred_at DESC").
		Offset((q.Page - 1) * q.PageSize).
		Limit(q.PageSize).
		Find(&events).Error
	return events, total, err
}

// Stats summarizes event counts by level and type.
type Stats struct {
	Total   int64            `json:"total"`
	Today   int64            `json:"today"`
	ByLevel map[string]int64 `json:"byLevel"`
	ByType  map[string]int64 `json:"byType"`
	ByKind  map[string]int64 `json:"byKind"`
}

func (s *Service) Stats() (*Stats, error) {
	st := &Stats{ByLevel: map[string]int64{}, ByType: map[string]int64{}, ByKind: map[string]int64{}}
	if err := s.db.Model(&model.AIEvent{}).Count(&st.Total).Error; err != nil {
		return nil, err
	}
	startOfDay := time.Now().Truncate(24 * time.Hour)
	s.db.Model(&model.AIEvent{}).Where("occurred_at >= ?", startOfDay).Count(&st.Today)
	countGroup := func(col string, dst map[string]int64) {
		type row struct {
			K string
			N int64
		}
		var rows []row
		s.db.Model(&model.AIEvent{}).Select(col + " as k, count(*) as n").Group(col).Scan(&rows)
		for _, r := range rows {
			dst[r.K] = r.N
		}
	}
	countGroup("level", st.ByLevel)
	countGroup("event_type", st.ByType)
	countGroup("kind", st.ByKind)
	return st, nil
}
