package model

import "time"

// AITaskRun is one scheduled execution of an AI task. Status values:
// pending, starting, running, stopping, failed, stopped, succeeded.
// ErrorCode is one of the ai package error codes, e.g. provider_timeout,
// stream_unavailable, frame_capture_failed, inference_failed,
// invalid_task_config, resource_limit, internal_error.
type AITaskRun struct {
	Base
	TaskID      uint       `gorm:"index" json:"taskId"`
	Status      string     `gorm:"size:16;default:pending;index" json:"status"`
	StartedAt   *time.Time `json:"startedAt,omitempty"`
	FinishedAt  *time.Time `json:"finishedAt,omitempty"`
	LastRunAt   *time.Time `json:"lastRunAt,omitempty"`
	RetryCount  int        `json:"retryCount"`
	ErrorCode   string     `gorm:"size:32" json:"errorCode"`
	ErrorMsg    string     `gorm:"size:512" json:"errorMsg"`
	WorkerID    string     `gorm:"size:64" json:"workerId"`
	HeartbeatAt *time.Time `json:"heartbeatAt,omitempty"`
}
