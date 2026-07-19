package models

import (
	"time"
)

type Machine struct {
	ID           uint       `json:"id" gorm:"primaryKey"`
	Name         string     `json:"name" gorm:"not null"`
	Token        string     `json:"token" gorm:"uniqueIndex;not null"`
	LastSeenAt   *time.Time `json:"lastSeenAt"`
	AgentVersion string     `json:"agentVersion"`
	OS           string     `json:"os"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

type SyncTask struct {
	ID                 uint      `json:"id" gorm:"primaryKey"`
	MachineID          uint      `json:"machineId" gorm:"not null;index"`
	Name               string    `json:"name" gorm:"not null"`
	Alpha              string    `json:"alpha" gorm:"not null"`
	Beta               string    `json:"beta" gorm:"not null"`
	Mode               string    `json:"mode" gorm:"default:two-way-resolved"`
	IgnoreVCS          bool      `json:"ignoreVcs"`
	SymlinkMode        string    `json:"symlinkMode" gorm:"default:ignore"`
	IgnorePaths        []string  `json:"ignorePaths,omitempty"`
	MutagenSessionName string    `json:"mutagenSessionName"`
	Status             string    `json:"status" gorm:"default:"`
	LastError          string    `json:"lastError"`
	CreatedAt          time.Time `json:"createdAt"`
	UpdatedAt          time.Time `json:"updatedAt"`
}

type MachineConfig struct {
	ID        uint      `json:"id" gorm:"primaryKey"`
	MachineID uint      `json:"machineId" gorm:"uniqueIndex;not null"`
	Type      string    `json:"type" gorm:"not null"` // global | ssh
	Content   string    `json:"content"`
	UpdatedAt time.Time `json:"updatedAt"`
}
