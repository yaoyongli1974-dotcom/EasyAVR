package model

// TranscodeJob is a derived stream produced by a platform-side ffmpeg
// pipeline: "transcode" re-encodes the channel stream (scale/bitrate), "osd"
// overlays a watermark caption with drawtext, "both" does both in one encode
// pass. The result is pushed back into the media core as live/<StreamKey>.
type TranscodeJob struct {
	Base
	ChannelID uint   `gorm:"index" json:"channelId"`
	Name      string `gorm:"size:128" json:"name"`
	Mode      string `gorm:"size:16;default:transcode" json:"mode"` // transcode | osd | both
	StreamKey string `gorm:"size:64;uniqueIndex" json:"streamKey"`  // derived <channel>_t
	Text      string `gorm:"size:255" json:"text"`
	Position  string `gorm:"size:16;default:top-left" json:"position"` // top-left | top-right | bottom-left | bottom-right
	FontSize  int    `gorm:"default:24" json:"fontSize"`
	Width     int    `json:"width"`                       // 0 keeps source width
	Height    int    `json:"height"`                      // 0 keeps source height
	Bitrate   int    `gorm:"default:2000" json:"bitrate"` // kbps
	Enabled   bool   `gorm:"default:true" json:"enabled"`
	Status    string `gorm:"size:16;default:stopped" json:"status"` // stopped | running | failed
	LastError string `gorm:"size:512" json:"lastError"`
	PID       int    `json:"pid"`
}
