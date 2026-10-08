package device

import (
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/easyavr/easyavr/internal/model"
	"github.com/easyavr/easyavr/internal/video"
)

// Service drives channel lifecycle against the streaming core.
type Service struct {
	db  *gorm.DB
	zlm *video.Client
}

func NewService(db *gorm.DB, zlm *video.Client) *Service {
	return &Service{db: db, zlm: zlm}
}

// StartChannel brings a channel online.
//   - pull/onvif/rtsp/rtmp: create a ZLM pull proxy from the resolved source URL
//   - push: open an RTP server (GB28181/EHOME) or expose an RTMP push URL
func (s *Service) StartChannel(channelID uint) (map[string]string, error) {
	var ch model.Channel
	if err := s.db.First(&ch, channelID).Error; err != nil {
		return nil, err
	}
	var dev model.Device
	if err := s.db.First(&dev, ch.DeviceID).Error; err != nil {
		return nil, err
	}
	out := map[string]string{}
	switch strings.ToLower(dev.AccessMode) {
	case "push":
		if strings.EqualFold(dev.Protocol, "gb28181") || strings.EqualFold(dev.Protocol, "ehome") {
			port, err := s.zlm.OpenRtpServer(ch.StreamKey, 0, true)
			if err != nil {
				return nil, err
			}
			out["rtpPort"] = fmt.Sprintf("%d", port)
		}
		out["pushUrl"] = s.zlm.PushURL(ch.StreamKey)
	default:
		source := BuildSourceURL(dev, ch)
		if source == "" {
			return nil, fmt.Errorf("no source url for channel %d", ch.ID)
		}
		if _, err := s.zlm.AddStreamProxy(ch.StreamKey, source); err != nil {
			return nil, err
		}
		out["source"] = source
	}
	if err := s.db.Model(&ch).Updates(map[string]any{"online": true, "enabled": true}).Error; err != nil {
		return nil, err
	}
	return out, nil
}

// StopChannel takes a channel offline and closes the underlying stream.
func (s *Service) StopChannel(channelID uint) error {
	var ch model.Channel
	if err := s.db.First(&ch, channelID).Error; err != nil {
		return err
	}
	_ = s.zlm.CloseRtpServer(ch.StreamKey)
	_ = s.zlm.CloseStreams(ch.StreamKey)
	return s.db.Model(&ch).Update("online", false).Error
}

// SyncStatus reconciles channel online flags with the live streams reported by
// the streaming core. Returns the number of channels updated.
func (s *Service) SyncStatus() (int, error) {
	streams, err := s.zlm.MediaList()
	if err != nil {
		return 0, err
	}
	active := map[string]bool{}
	for _, st := range streams {
		active[st.Stream] = true
	}
	var channels []model.Channel
	if err := s.db.Find(&channels).Error; err != nil {
		return 0, err
	}
	updated := 0
	for _, ch := range channels {
		online := active[ch.StreamKey]
		if ch.Online != online {
			s.db.Model(&ch).Update("online", online)
			updated++
		}
	}
	return updated, nil
}
