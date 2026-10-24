package model

// CloudStore is an S3-compatible object storage credential (MinIO/OSS/COS/...).
type CloudStore struct {
	Base
	Name      string `gorm:"size:64" json:"name"`
	Type      string `gorm:"size:16,default:s3" json:"type"` // s3 today; oss/cos map to the same API
	Endpoint  string `gorm:"size:255" json:"endpoint"`       // e.g. http://127.0.0.1:9000
	Bucket    string `gorm:"size:64" json:"bucket"`
	AccessKey string `gorm:"size:255" json:"accessKey"`
	SecretKey string `gorm:"size:255" json:"-"`
	Region    string `gorm:"size:32" json:"region"`
	PathStyle bool   `gorm:"default:true" json:"pathStyle"`
	Enabled   bool   `gorm:"default:true" json:"enabled"`

	// HasSecret tells the UI whether a secret is stored; the secret itself is
	// never serialized.
	HasSecret bool `gorm:"-" json:"hasSecret"`
}

// RecordingArchive links a catalog recording to an uploaded object.
type RecordingArchive struct {
	Base
	RecordingID uint   `gorm:"uniqueIndex" json:"recordId"`
	ChannelID   uint   `gorm:"index" json:"channelId"`
	StreamKey   string `gorm:"size:64;index" json:"streamKey"`
	StoreID     uint   `gorm:"index" json:"storeId"`
	Bucket      string `gorm:"size:64" json:"bucket"`
	Object      string `gorm:"size:512" json:"object"`
	Size        int64  `json:"size"`
	Status      string `gorm:"size:16;default:uploaded" json:"status"` // uploaded | failed
	Error       string `gorm:"size:512" json:"error"`
}
