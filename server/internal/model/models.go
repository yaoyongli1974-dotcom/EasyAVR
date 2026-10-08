package model

import "time"

// Base is embedded by all persisted entities.
type Base struct {
	ID        uint      `gorm:"primaryKey" json:"id"`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// User is a platform account. Roles: admin, operator, viewer.
type User struct {
	Base
	Username     string `gorm:"uniqueIndex;size:64" json:"username"`
	PasswordHash string `gorm:"size:128" json:"-"`
	Nickname     string `gorm:"size:64" json:"nickname"`
	Role         string `gorm:"size:32;default:viewer" json:"role"`
	Enabled      bool   `gorm:"default:true" json:"enabled"`
}

// Device is a logical/physical video source endpoint.
// Protocol examples: rtsp, rtmp, onvif, gb28181, ehome, hikvision, dahua.
// AccessMode: pull (platform pulls stream), push (device pushes), register (device registers to platform).
type Device struct {
	Base
	Name         string    `gorm:"size:128;index" json:"name"`
	Protocol     string    `gorm:"size:32;index" json:"protocol"`
	AccessMode   string    `gorm:"size:16;default:pull" json:"accessMode"`
	Manufacturer string    `gorm:"size:64" json:"manufacturer"`
	IP           string    `gorm:"size:64" json:"ip"`
	Port         int       `json:"port"`
	Username     string    `gorm:"size:64" json:"username"`
	Password     string    `gorm:"size:128" json:"-"`
	Status       string    `gorm:"size:16;default:offline" json:"status"`
	GroupID      uint      `gorm:"index" json:"groupId"`
	NodeID       string    `gorm:"size:64;index" json:"nodeId"` // cluster node assignment
	Channels     []Channel `json:"channels,omitempty"`
}

// Channel is a playable stream belonging to a device (main/sub stream etc).
type Channel struct {
	Base
	DeviceID    uint   `gorm:"index" json:"deviceId"`
	Name        string `gorm:"size:128" json:"name"`
	StreamType  string `gorm:"size:16;default:main" json:"streamType"` // main, sub
	SourceURL   string `gorm:"size:512" json:"sourceUrl"`
	StreamKey   string `gorm:"uniqueIndex;size:64" json:"streamKey"` // ZLM stream id
	Online      bool   `gorm:"default:false" json:"online"`
	Enabled     bool   `gorm:"default:true" json:"enabled"`
	Recording   bool   `gorm:"default:false" json:"recording"`
	Description string `gorm:"size:255" json:"description"`

	// Snapshot configuration (Video Resource Center).
	SnapshotEnabled  bool `gorm:"default:false" json:"snapshotEnabled"`
	SnapshotInterval int  `gorm:"default:300" json:"snapshotInterval"` // seconds

	// GB28181 identity when the channel belongs to a registered GB device.
	GBDeviceID  string `gorm:"size:64;index" json:"gbDeviceId"`
	GBChannelID string `gorm:"size:64" json:"gbChannelId"`
}

// VideoResource is the unified catalog entry of the Video Resource Center.
// Kind: live, recording, snapshot.
type VideoResource struct {
	Base
	ChannelID uint       `gorm:"index" json:"channelId"`
	Kind      string     `gorm:"size:16;index" json:"kind"`
	Protocol  string     `gorm:"size:16" json:"protocol"`
	URL       string     `gorm:"size:512" json:"url"`
	Path      string     `gorm:"size:512" json:"path"`
	StartTime *time.Time `json:"startTime,omitempty"`
	EndTime   *time.Time `json:"endTime,omitempty"`
	SizeBytes int64      `json:"sizeBytes"`
	Duration  int        `json:"duration"`
	Tags      string     `gorm:"size:255" json:"tags"`
}

// AIProvider is a registered AI capability backend.
// Kind: cv (detection/structured), vlm (vision-language), llm (text/semantic).
type AIProvider struct {
	Base
	Name     string `gorm:"size:64;index" json:"name"`
	Kind     string `gorm:"size:16;index" json:"kind"`
	Vendor   string `gorm:"size:32" json:"vendor"`
	Endpoint string `gorm:"size:255" json:"endpoint"`
	APIKey   string `gorm:"size:255" json:"-"`
	Model    string `gorm:"size:64" json:"model"`
	Enabled  bool   `gorm:"default:true" json:"enabled"`
}

// AITask binds a channel to an AI capability with task-specific config.
// TaskType: cv_detect, vlm_understand, llm_analyze, vqd (video quality diagnosis).
type AITask struct {
	Base
	Name       string `gorm:"size:128" json:"name"`
	ChannelID  uint   `gorm:"index" json:"channelId"`
	ProviderID uint   `gorm:"index" json:"providerId"`
	TaskType   string `gorm:"size:32;index" json:"taskType"`
	Config     string `gorm:"type:text" json:"config"` // JSON: roi, interval, labels, prompt
	Enabled    bool   `gorm:"default:true" json:"enabled"`
	Status     string `gorm:"size:16;default:stopped" json:"status"`
}

// AIEvent is an entry in the AI Event Center. Events may be produced by
// platform tasks or ingested from external AI workers.
type AIEvent struct {
	Base
	ChannelID  uint      `gorm:"index" json:"channelId"`
	TaskID     uint      `gorm:"index" json:"taskId"`
	ProviderID uint      `gorm:"index" json:"providerId"`
	Kind       string    `gorm:"size:16;index" json:"kind"`      // cv, vlm, llm
	EventType  string    `gorm:"size:64;index" json:"eventType"` // person_intrusion, fire, ...
	Level      string    `gorm:"size:16;index" json:"level"`     // info, warning, critical
	Confidence float64   `json:"confidence"`
	Summary    string    `gorm:"size:512" json:"summary"`
	Payload    string    `gorm:"type:text" json:"payload"` // JSON detail
	Snapshot   string    `gorm:"size:512" json:"snapshot"`
	OccurredAt time.Time `gorm:"index" json:"occurredAt"`

	// Semantic search vector (JSON float array) produced by an embedding model.
	Embedding      string `gorm:"type:text" json:"-"`
	EmbeddingModel string `gorm:"size:64" json:"-"`
}

// Recording catalogs a recorded video file from the Video Resource Center.
type Recording struct {
	Base
	ChannelID uint      `gorm:"index" json:"channelId"`
	StreamKey string    `gorm:"size:64;index" json:"streamKey"`
	Date      string    `gorm:"size:10;index" json:"date"` // YYYY-MM-DD
	File      string    `gorm:"size:255" json:"file"`      // relative path from ZLM
	URL       string    `gorm:"size:512" json:"url"`
	SizeBytes int64     `json:"sizeBytes"`
	Duration  int       `json:"duration"` // seconds
	StartTime time.Time `json:"startTime"`
}

// RecordingPlan schedules automatic recording for a channel.
type RecordingPlan struct {
	Base
	ChannelID     uint   `gorm:"uniqueIndex" json:"channelId"`
	Enabled       bool   `gorm:"default:true" json:"enabled"`
	Days          string `gorm:"size:64;default:daily" json:"days"` // daily | workday | weekend | "1,2,3"
	StartTime     string `gorm:"size:8;default:00:00" json:"startTime"`
	EndTime       string `gorm:"size:8;default:23:59" json:"endTime"`
	RetentionDays int    `gorm:"default:7" json:"retentionDays"`
	StreamType    string `gorm:"size:16;default:main" json:"streamType"`
}

// Snapshot is a captured still image from a channel.
type Snapshot struct {
	Base
	ChannelID uint      `gorm:"index" json:"channelId"`
	Path      string    `gorm:"size:512" json:"path"`
	URL       string    `gorm:"size:512" json:"url"`
	SizeBytes int64     `json:"sizeBytes"`
	TakenAt   time.Time `gorm:"index" json:"takenAt"`
}

// GBDevice is a GB28181 device that registered to the platform over SIP.
type GBDevice struct {
	Base
	DeviceID      string    `gorm:"uniqueIndex;size:64" json:"deviceId"`
	Name          string    `gorm:"size:128" json:"name"`
	Manufacturer  string    `gorm:"size:128" json:"manufacturer"`
	Model         string    `gorm:"size:128" json:"model"`
	Firmware      string    `gorm:"size:128" json:"firmware"`
	IP            string    `gorm:"size:64" json:"ip"`
	Port          int       `json:"port"`
	Transport     string    `gorm:"size:8;default:UDP" json:"transport"`
	ChannelCount  int       `json:"channelCount"`
	Online        bool      `gorm:"default:false" json:"online"`
	RegisteredAt  time.Time `json:"registeredAt"`
	LastKeepalive time.Time `json:"lastKeepalive"`
	Expires       int       `json:"expires"`
}

// NotificationChannel is a delivery endpoint for alert notifications.
// Type: webhook, email.
type NotificationChannel struct {
	Base
	Name    string `gorm:"size:64;index" json:"name"`
	Type    string `gorm:"size:16;index" json:"type"`
	Enabled bool   `gorm:"default:true" json:"enabled"`
	// Webhook
	URL    string `gorm:"size:512" json:"url"`
	Secret string `gorm:"size:255" json:"-"`
	// Email
	SMTPHost     string `gorm:"size:128" json:"smtpHost"`
	SMTPPort     int    `json:"smtpPort"`
	SMTPUser     string `gorm:"size:128" json:"smtpUser"`
	SMTPPassword string `gorm:"size:255" json:"-"`
	From         string `gorm:"size:128" json:"from"`
	To           string `gorm:"size:255" json:"to"`
	UseTLS       bool   `json:"useTls"`
}

// NotificationRule decides which events are pushed to which channels.
type NotificationRule struct {
	Base
	Name      string `gorm:"size:64" json:"name"`
	Enabled   bool   `gorm:"default:true" json:"enabled"`
	TargetIDs string `gorm:"size:255" json:"targetIds"` // comma-separated NotificationChannel IDs
	MinLevel  string `gorm:"size:16" json:"minLevel"`   // info | warning | critical
	Kind      string `gorm:"size:16" json:"kind"`       // filter by provider kind (optional)
	EventType string `gorm:"size:64" json:"eventType"`  // filter by event type (optional)
}

// ClusterNode is a member of the EasyAVR cluster.
type ClusterNode struct {
	Base
	NodeID        string    `gorm:"uniqueIndex;size:64" json:"nodeId"`
	Name          string    `gorm:"size:128" json:"name"`
	APIBase       string    `gorm:"size:255" json:"apiBase"`
	Status        string    `gorm:"size:16;default:online" json:"status"`
	IsSelf        bool      `json:"isSelf"`
	LastHeartbeat time.Time `json:"lastHeartbeat"`
}

// GBCascade is a GB28181 upper platform this platform cascades to.
type GBCascade struct {
	Base
	Name          string    `gorm:"size:128" json:"name"`
	TargetID      string    `gorm:"size:64" json:"targetId"` // upper platform SIP id
	LocalID       string    `gorm:"size:64" json:"localId"`  // our id presented to the upper platform
	TargetIP      string    `gorm:"size:64" json:"targetIp"`
	TargetPort    int       `json:"targetPort"`
	Password      string    `gorm:"size:128" json:"-"`
	Transport     string    `gorm:"size:8;default:UDP" json:"transport"`
	Enabled       bool      `gorm:"default:true" json:"enabled"`
	Online        bool      `gorm:"default:false" json:"online"`
	LastHeartbeat time.Time `json:"lastHeartbeat"`
}

// GBWhiteList restricts which GB28181/EHOME devices may register.
type GBWhiteList struct {
	Base
	DeviceID    string `gorm:"size:64;index" json:"deviceId"`
	IP          string `gorm:"size:64" json:"ip"`
	Port        int    `json:"port"`
	Protocol    string `gorm:"size:16;default:GB28181" json:"protocol"`
	Password    string `gorm:"size:128" json:"-"`
	Enabled     bool   `gorm:"default:true" json:"enabled"`
	Description string `gorm:"size:255" json:"description"`
}

// GB35114Cert is a device certificate issued by the platform's SM2 CA.
// Status: active, revoked.
type GB35114Cert struct {
	Base
	DeviceID  string     `gorm:"size:64;index" json:"deviceId"`
	Serial    string     `gorm:"size:64;index" json:"serial"`
	Subject   string     `gorm:"size:255" json:"subject"`
	CertPEM   string     `gorm:"type:text" json:"-"`
	Status    string     `gorm:"size:16;default:active" json:"status"`
	NotBefore time.Time  `json:"notBefore"`
	NotAfter  time.Time  `json:"notAfter"`
	RevokedAt *time.Time `json:"revokedAt,omitempty"`
}

// APIKey is a credential for third-party / APP access to the open API.
// Scopes is a comma-separated subset of: read, write. Only the hash of the
// secret is stored; the plaintext is shown once at creation time.
// RateLimit (requests/min) and QuotaPerDay (0 = unlimited) bound usage.
type APIKey struct {
	Base
	Name         string     `gorm:"size:64" json:"name"`
	Prefix       string     `gorm:"size:16;index" json:"prefix"`
	KeyHash      string     `gorm:"size:64;uniqueIndex" json:"-"`
	Scopes       string     `gorm:"size:128;default:read" json:"scopes"`
	Enabled      bool       `gorm:"default:true" json:"enabled"`
	ExpiresAt    *time.Time `json:"expiresAt,omitempty"`
	LastUsedAt   *time.Time `json:"lastUsedAt,omitempty"`
	LastUsedIP   string     `gorm:"size:64" json:"lastUsedIp"`
	RateLimit    int        `json:"rateLimit"`
	QuotaPerDay  int        `json:"quotaPerDay"`
	UsedToday    int        `json:"usedToday"`
	QuotaResetAt time.Time  `json:"quotaResetAt"`
}

// APIRequestLog is one audited call against the open API.
type APIRequestLog struct {
	Base
	KeyID     uint   `gorm:"index" json:"keyId"`
	KeyName   string `gorm:"size:64" json:"keyName"`
	Method    string `gorm:"size:8" json:"method"`
	Path      string `gorm:"size:255" json:"path"`
	Status    int    `json:"status"`
	LatencyMs int64  `json:"latencyMs"`
	IP        string `gorm:"size:64" json:"ip"`
}

// GA1400Cascade links the platform to another GA/T1400 view library.
// Direction: up (this platform pushes to an upper library) or down (a lower
// library pushes to us; we subscribe).
type GA1400Cascade struct {
	Base
	Name          string    `gorm:"size:128" json:"name"`
	Direction     string    `gorm:"size:8;default:up" json:"direction"`
	PlatformID    string    `gorm:"size:64" json:"platformId"`
	URL           string    `gorm:"size:255" json:"url"`
	Username      string    `gorm:"size:64" json:"username"`
	Password      string    `gorm:"size:128" json:"-"`
	Enabled       bool      `gorm:"default:true" json:"enabled"`
	Online        bool      `gorm:"default:false" json:"online"`
	LastHeartbeat time.Time `json:"lastHeartbeat"`
	LastSyncAt    time.Time `json:"lastSyncAt"` // last full sync pushed to an upper library
	SyncCount     int       `json:"syncCount"`  // records pushed by the last full sync
}

// GA1400Subscription is a subscription we hold on a lower view library.
type GA1400Subscription struct {
	Base
	CascadeID   uint      `gorm:"index" json:"cascadeId"`
	SubscribeID string    `gorm:"size:64" json:"subscribeId"`
	Title       string    `gorm:"size:128" json:"title"`
	EventTypes  string    `gorm:"size:255" json:"eventTypes"`
	Status      string    `gorm:"size:16;default:active" json:"status"`
	ExpiresAt   time.Time `json:"expiresAt"`   // subscribe TTL deadline
	LastRenewAt time.Time `json:"lastRenewAt"` // last successful renew
	RenewCount  int       `json:"renewCount"`
}
