package models

import "time"

type Job struct {
	ID                      uint       `gorm:"primaryKey" json:"id"`
	Name                    string     `gorm:"uniqueIndex;size:255;not null" json:"name"`
	NotifyOnStart           bool       `gorm:"not null;default:false" json:"notify_on_start"`
	NotifyOnSuccess         bool       `gorm:"not null;default:false" json:"notify_on_success"`
	NotifyOnFailure         bool       `gorm:"not null;default:true" json:"notify_on_failure"`
	NotifyOnHeartbeatMissed bool       `gorm:"not null;default:true" json:"notify_on_heartbeat_missed"`
	HeartbeatStaleAfterSec  int        `gorm:"not null;default:300" json:"heartbeat_stale_after_sec"`
	LastMissAlertAt         *time.Time `json:"last_miss_alert_at,omitempty"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
}

type Run struct {
	ID             uint       `gorm:"primaryKey" json:"id"`
	JobID          uint       `gorm:"index;not null" json:"job_id"`
	RunID          string     `gorm:"uniqueIndex;size:64;not null" json:"run_id"`
	Status         string     `gorm:"size:32;not null;index" json:"status"`
	StartTime      *time.Time `json:"start_time,omitempty"`
	EndTime        *time.Time `json:"end_time,omitempty"`
	MetadataJSON   string     `gorm:"type:text" json:"metadata,omitempty"`
	TagsJSON       string     `gorm:"type:text" json:"tags,omitempty"`
	Logs           string     `gorm:"type:text" json:"logs,omitempty"`
	ErrorDetails   string     `gorm:"type:text" json:"error_details,omitempty"`
	IdempotencyKey string     `gorm:"size:128;index" json:"idempotency_key,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
	Job            Job        `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
}

type Heartbeat struct {
	ID           uint      `gorm:"primaryKey" json:"id"`
	JobID        uint      `gorm:"uniqueIndex;not null" json:"job_id"`
	SeenAt       time.Time `gorm:"index;not null" json:"seen_at"`
	MetadataJSON string    `gorm:"type:text" json:"metadata,omitempty"`
	TagsJSON     string    `gorm:"type:text" json:"tags,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
	Job          Job       `gorm:"constraint:OnUpdate:CASCADE,OnDelete:CASCADE;" json:"-"`
}

type AlertChannel struct {
	ID         uint      `gorm:"primaryKey" json:"id"`
	Type       string    `gorm:"size:32;not null;index" json:"type"` // webhook | email
	ConfigJSON string    `gorm:"type:text" json:"config"`
	Enabled    bool      `gorm:"not null;default:true" json:"enabled"`
	CreatedAt  time.Time `json:"created_at"`
	UpdatedAt  time.Time `json:"updated_at"`
}
