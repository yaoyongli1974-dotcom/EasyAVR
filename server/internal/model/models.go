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

// Role groups permissions assigned to users. Permissions is a comma-separated
// list of feature keys (e.g. "device,video,ai,config"); the built-in "admin"
// role always has full access.
type Role struct {
	Base
	Name        string `gorm:"uniqueIndex;size:32" json:"name"`
	Description string `gorm:"size:255" json:"description"`
	Permissions string `gorm:"size:512" json:"permissions"`
	Builtin     bool   `gorm:"default:false" json:"builtin"`
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

	// GPS position for electronic map (手册 3.3.4).
	Longitude float64    `json:"longitude"`
	Latitude  float64    `json:"latitude"`
	Altitude  float64    `json:"altitude"`
	Heading   float64    `json:"heading"`           // degrees, 0-360
	Speed     float64    `json:"speed"`             // km/h
	GPSTime   *time.Time `json:"gpsTime,omitempty"` // last GPS update time
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

	// GPS position for electronic map (手册 3.3.4).
	Longitude float64    `json:"longitude"`
	Latitude  float64    `json:"latitude"`
	Altitude  float64    `json:"altitude"`
	Heading   float64    `json:"heading"`           // degrees, 0-360
	Speed     float64    `json:"speed"`             // km/h
	GPSTime   *time.Time `json:"gpsTime,omitempty"` // last GPS update time
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
	Name        string `gorm:"size:128" json:"name"`
	ChannelID   uint   `gorm:"index" json:"channelId"`
	ProviderID  uint   `gorm:"index" json:"providerId"`
	TaskType    string `gorm:"size:32;index" json:"taskType"`
	Config      string `gorm:"type:text" json:"config"` // JSON: roi, interval, labels, prompt
	ROI         string `gorm:"type:text" json:"roi"`    // JSON array of normalized polygon points
	Sensitivity int    `gorm:"default:50" json:"sensitivity"`
	Schedule    string `gorm:"type:text" json:"schedule"` // JSON: {days, start, end}
	Enabled     bool   `gorm:"default:true" json:"enabled"`
	Status      string `gorm:"size:16;default:stopped" json:"status"`
}

// AIModel is a registry entry for an AI model, stable across versions.
// Kind mirrors AIProvider.Kind (cv, vlm, llm, embedding). Task is the concrete
// capability (detection, classification, ocr, chat, embedding, ...).
type AIModel struct {
	Base
	Name        string `gorm:"size:128;index" json:"name"`
	Kind        string `gorm:"size:16;index" json:"kind"`
	Task        string `gorm:"size:64;index" json:"task"`
	Framework   string `gorm:"size:32" json:"framework"` // onnx, pytorch, tensorrt, openvino, api
	Source      string `gorm:"size:16;default:local" json:"source"`
	Description string `gorm:"size:512" json:"description"`
	Tags        string `gorm:"size:255" json:"tags"`
	Enabled     bool   `gorm:"default:true" json:"enabled"`

	Versions []AIModelVersion `json:"versions,omitempty" gorm:"-"`
}

// AIModelVersion is an immutable artifact revision of a model.
// Status: registered, available, deployed, archived.
type AIModelVersion struct {
	Base
	ModelID   uint   `gorm:"index:idx_model_version,unique" json:"modelId"`
	Version   string `gorm:"size:32;index:idx_model_version,unique" json:"version"`
	Status    string `gorm:"size:16;default:registered" json:"status"`
	Format    string `gorm:"size:32" json:"format"`
	SizeBytes int64  `json:"sizeBytes"`
	Checksum  string `gorm:"size:128" json:"checksum"`
	Path      string `gorm:"size:512" json:"path"`
	URL       string `gorm:"size:512" json:"url"`
	Metrics   string `gorm:"type:text" json:"metrics"` // JSON: mAP, accuracy, latencyMs, fps
	Labels    string `gorm:"type:text" json:"labels"`  // JSON array of class labels
	Params    string `gorm:"type:text" json:"params"`  // JSON default inference params
	Notes     string `gorm:"size:512" json:"notes"`
}

// AIModelDeployment binds a model version to a runtime AIProvider.
// Status: pending, active, stopped, failed. Health: unknown, healthy, unhealthy.
type AIModelDeployment struct {
	Base
	Name         string     `gorm:"size:128" json:"name"`
	ModelID      uint       `gorm:"index" json:"modelId"`
	VersionID    uint       `gorm:"index" json:"versionId"`
	ProviderID   uint       `gorm:"index" json:"providerId"`
	Status       string     `gorm:"size:16;default:pending" json:"status"`
	Replicas     int        `gorm:"default:1" json:"replicas"`
	Config       string     `gorm:"type:text" json:"config"` // JSON deploy config
	Health       string     `gorm:"size:16;default:unknown" json:"health"`
	LastHealthAt *time.Time `json:"lastHealthAt,omitempty"`
	DeployedAt   *time.Time `json:"deployedAt,omitempty"`
}

// Dataset is a labeled sample collection used to train models.
// Kind mirrors AIModel.Kind (detection, classification, embedding, ...).
// Status: draft, ready, archived.
type Dataset struct {
	Base
	Name         string `gorm:"size:128;index" json:"name"`
	Description  string `gorm:"size:512" json:"description"`
	Kind         string `gorm:"size:16;index" json:"kind"`
	Source       string `gorm:"size:16;default:manual" json:"source"` // events, snapshots, manual, mixed
	Labels       string `gorm:"type:text" json:"labels"`              // JSON array of class labels
	Status       string `gorm:"size:16;default:draft" json:"status"`
	SampleCount  int    `json:"sampleCount"`
	LabeledCount int    `json:"labeledCount"`
	CreatedBy    string `gorm:"size:64" json:"createdBy"`
}

// DatasetSample is one item of a dataset, usually sourced from an AI event
// snapshot. Labels is JSON (class/bbox annotations). Split: train, val, test.
// Status: unlabeled, labeled, reviewed.
type DatasetSample struct {
	Base
	DatasetID uint   `gorm:"index" json:"datasetId"`
	EventID   uint   `gorm:"index" json:"eventId"`
	ChannelID uint   `gorm:"index" json:"channelId"`
	ImageURL  string `gorm:"size:512" json:"imageUrl"`
	Labels    string `gorm:"type:text" json:"labels"`
	Split     string `gorm:"size:8;default:train" json:"split"`
	Status    string `gorm:"size:16;default:unlabeled" json:"status"`
	Note      string `gorm:"size:255" json:"note"`
}

// AnnotationTask is a labeling job over a dataset.
// Status: pending, in_progress, completed.
type AnnotationTask struct {
	Base
	Name         string `gorm:"size:128;index" json:"name"`
	DatasetID    uint   `gorm:"index" json:"datasetId"`
	Assignee     string `gorm:"size:64" json:"assignee"`
	Status       string `gorm:"size:16;default:pending" json:"status"`
	Instructions string `gorm:"size:512" json:"instructions"`
	Total        int    `json:"total"`
	Labeled      int    `json:"labeled"`
}

// TrainingJob is a model training run over a dataset. On success it registers
// an AIModel (if needed) and an AIModelVersion with the job metrics.
// Status: queued, running, succeeded, failed, canceled.
type TrainingJob struct {
	Base
	Name        string     `gorm:"size:128;index" json:"name"`
	DatasetID   uint       `gorm:"index" json:"datasetId"`
	ModelID     uint       `gorm:"index" json:"modelId"`
	BaseModelID uint       `json:"baseModelId"`
	Framework   string     `gorm:"size:32" json:"framework"`
	HyperParams string     `gorm:"type:text" json:"hyperParams"` // JSON
	Status      string     `gorm:"size:16;default:queued" json:"status"`
	Metrics     string     `gorm:"type:text" json:"metrics"` // JSON
	VersionID   uint       `json:"versionId"`                // produced AIModelVersion
	Log         string     `gorm:"type:text" json:"log"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
}

// PTZPreset catalogs a pan/tilt/zoom preset on a channel. The preset itself is
// stored on the device; this table keeps the platform-side name/label.
type PTZPreset struct {
	Base
	ChannelID uint   `gorm:"index:idx_ptz_channel_preset,unique" json:"channelId"`
	Preset    int    `gorm:"index:idx_ptz_channel_preset,unique" json:"preset"`
	Name      string `gorm:"size:64" json:"name"`
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

	// Acknowledgement for alert escalation (unacked events can escalate to
	// higher tiers of an AlertPolicy).
	Acked   bool       `gorm:"default:false;index" json:"acked"`
	AckedAt *time.Time `json:"ackedAt,omitempty"`
	AckedBy string     `gorm:"size:64" json:"ackedBy"`
}

// AlertPolicy replaces the traditional alert plan: a matching rule with ordered
// escalation tiers dispatched by AI event severity. Tiers with DelaySec>0 are
// delivered by the escalation scheduler only while the event is unacknowledged.
type AlertPolicy struct {
	Base
	Name        string `gorm:"size:128;index" json:"name"`
	Description string `gorm:"size:512" json:"description"`
	Enabled     bool   `gorm:"default:true" json:"enabled"`
	Priority    int    `gorm:"default:0" json:"priority"`
	Kind        string `gorm:"size:16" json:"kind"`      // filter by provider kind
	EventType   string `gorm:"size:64" json:"eventType"` // filter by event type
	MinLevel    string `gorm:"size:16;default:info" json:"minLevel"`
	ChannelID   uint   `gorm:"index" json:"channelId"` // 0 = any channel
	Keywords    string `gorm:"size:255" json:"keywords"`
	CooldownSec int    `gorm:"default:0" json:"cooldownSec"`
	AckRequired bool   `gorm:"default:false" json:"ackRequired"`

	Tiers []AlertPolicyTier `gorm:"-" json:"tiers,omitempty"`
}

// AlertPolicyTier is one escalation step of an AlertPolicy. Tier 0 is the
// immediate notification; higher tiers escalate until acknowledgement.
type AlertPolicyTier struct {
	Base
	PolicyID  uint   `gorm:"index" json:"policyId"`
	Tier      int    `gorm:"default:0" json:"tier"`
	MinLevel  string `gorm:"size:16" json:"minLevel"` // tier applies only if event level >= this
	DelaySec  int    `gorm:"default:0" json:"delaySec"`
	TargetIDs string `gorm:"size:255" json:"targetIds"` // comma-separated NotificationChannel IDs
	Template  string `gorm:"size:512" json:"template"`
}

// AlertDelivery audits every tier dispatch of an alert policy.
// Reason: immediate, escalation, test. Status: success, failed.
type AlertDelivery struct {
	Base
	PolicyID    uint   `gorm:"index" json:"policyId"`
	PolicyName  string `gorm:"size:128" json:"policyName"`
	Tier        int    `json:"tier"`
	EventID     uint   `gorm:"index" json:"eventId"`
	ChannelID   uint   `gorm:"index" json:"channelId"` // NotificationChannel ID
	ChannelName string `gorm:"size:64" json:"channelName"`
	Reason      string `gorm:"size:32" json:"reason"`
	Status      string `gorm:"size:16" json:"status"`
	Error       string `gorm:"size:512" json:"error"`
}

// Track stores a GPS position point for device/channel trajectory (手册 3.3.5 轨迹跟踪).
// Source: device (device-level GPS), channel (channel-level GPS), or manual.
type Track struct {
	Base
	DeviceID  uint      `gorm:"index" json:"deviceId"`
	ChannelID uint      `gorm:"index" json:"channelId"`
	Longitude float64   `json:"longitude"`
	Latitude  float64   `json:"latitude"`
	Altitude  float64   `json:"altitude"`
	Heading   float64   `json:"heading"`               // degrees
	Speed     float64   `json:"speed"`                 // km/h
	Accuracy  float64   `json:"accuracy"`              // meters, GPS accuracy
	Source    string    `gorm:"size:16" json:"source"` // device, channel, manual, gb28181
	TrackTime time.Time `gorm:"index" json:"trackTime"`
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

	// Emergency mark for quick retrieval (手册 3.3.3 紧急标记).
	Marked bool   `gorm:"default:false;index" json:"marked"`
	Mark   string `gorm:"size:255" json:"mark"`
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

// PlatformSetting is a persisted runtime platform setting (key/value), editable
// from the platform-config page. Values override environment defaults for
// features that can be started/stopped at runtime (手册 3.7 平台配置).
type PlatformSetting struct {
	Base
	Key   string `gorm:"uniqueIndex;size:64" json:"key"`
	Value string `gorm:"size:255" json:"value"`
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

// GBBlackList blocks malicious GB28181/EHOME registrations. A rule matches when
// all of its non-empty fields equal the registering device; any match blocks the
// device (手册 3.7.4.3 黑名单).
type GBBlackList struct {
	Base
	DeviceID    string `gorm:"size:64;index" json:"deviceId"`
	UA          string `gorm:"size:128" json:"ua"`
	IP          string `gorm:"size:64" json:"ip"`
	Port        int    `json:"port"`
	Protocol    string `gorm:"size:16;default:GB28181" json:"protocol"`
	Enabled     bool   `gorm:"default:true" json:"enabled"`
	Description string `gorm:"size:255" json:"description"`
}

// StatusLog records device/channel online/offline transitions and health check
// results, powering the channel "消息/状态记录" view (手册 3.2.3).
type StatusLog struct {
	Base
	DeviceID  uint      `gorm:"index" json:"deviceId"`
	ChannelID uint      `gorm:"index" json:"channelId"`
	Target    string    `gorm:"size:16;index" json:"target"` // device | channel
	Online    bool      `json:"online"`
	Source    string    `gorm:"size:16" json:"source"` // poll, check, register, gb28181
	Message   string    `gorm:"size:512" json:"message"`
	IP        string    `gorm:"size:64" json:"ip"`
	LoggedAt  time.Time `gorm:"index" json:"loggedAt"`
}

// ChannelTraffic is the latest accumulated ingress bytes for a channel, sampled
// from the media kernel (手册 3.2.3 流量).
type ChannelTraffic struct {
	Base
	ChannelID  uint      `gorm:"uniqueIndex" json:"channelId"`
	StreamKey  string    `gorm:"size:64" json:"streamKey"`
	Online     bool      `json:"online"`
	Bytes      int64     `json:"bytes"` // cumulative ingress bytes reported by ZLM
	LastSample time.Time `json:"lastSample"`
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

// DeviceGroup organizes devices/channels in a hierarchy (multi-level).
// Path stores ancestry like "/1/3/7/" for fast subtree queries.
type DeviceGroup struct {
	Base
	Name        string `gorm:"size:64;index" json:"name"`
	Description string `gorm:"size:255" json:"description"`
	ParentID    uint   `gorm:"index" json:"parentId"`
	Path        string `gorm:"size:255;index" json:"path"` // e.g. "/1/3/7/"
	Sort        int    `gorm:"default:0" json:"sort"`

	// Base coordinate: devices/channels in this group without their own GPS are
	// shown here on the map (手册 3.3.4 group base point).
	Longitude float64 `json:"longitude"`
	Latitude  float64 `json:"latitude"`
}

// DeviceGroupDevice binds a device to a group (many-to-many).
type DeviceGroupDevice struct {
	DeviceID uint `gorm:"primaryKey;index" json:"deviceId"`
	GroupID  uint `gorm:"primaryKey;index" json:"groupId"`
}

// ChannelGroupChannel binds a channel to a group (many-to-many).
type ChannelGroupChannel struct {
	ChannelID uint `gorm:"primaryKey;index" json:"channelId"`
	GroupID   uint `gorm:"primaryKey;index" json:"groupId"`
}

// UserGroup binds a user to a group with optional per-group permissions.
// Permissions is a comma-separated list (same keys as Role.Permissions).
// Empty means inherit from role.
type UserGroup struct {
	Base
	UserID      uint   `gorm:"uniqueIndex:uq_user_group" json:"userId"`
	GroupID     uint   `gorm:"uniqueIndex:uq_user_group" json:"groupId"`
	Permissions string `gorm:"size:512" json:"permissions"`
}

// AuditLog records an operation for compliance and troubleshooting (手册 3.7.5 运维审计).
// Action: create, update, delete, login, logout, export, import, config_change, etc.
// Resource: devices, channels, users, roles, groups, ai_tasks, etc.
// Result: success, failed.
type AuditLog struct {
	Base
	UserID      uint   `gorm:"index" json:"userId"`
	Username    string `gorm:"size:64;index" json:"username"`
	IP          string `gorm:"size:64" json:"ip"`
	Method      string `gorm:"size:8;index" json:"method"`      // GET, POST, PUT, DELETE
	Path        string `gorm:"size:255;index" json:"path"`      // API path
	Action      string `gorm:"size:32;index" json:"action"`     // create, update, delete, login...
	Resource    string `gorm:"size:64;index" json:"resource"`   // devices, users, channels...
	ResourceID  string `gorm:"size:64;index" json:"resourceId"` // target ID
	Result      string `gorm:"size:16;index" json:"result"`     // success, failed
	ErrorMsg    string `gorm:"size:512" json:"errorMsg"`        // error detail if failed
	RequestBody string `gorm:"type:text" json:"requestBody"`    // request payload (JSON)
	LatencyMs   int64  `json:"latencyMs"`                       // request latency
}
