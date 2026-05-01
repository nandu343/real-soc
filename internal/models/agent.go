package models

import "time"

// AgentRecord represents a persistently registered agent in the orchestrator
// registry. This differs from AgentCard (which is embedded in WorkflowState)
// by being managed independently and indexed for quick lookup by the UI.
type AgentRecord struct {
	AgentID      string         `json:"agent_id" gorm:"primaryKey;column:agent_id"`
	Image        string         `json:"image,omitempty" gorm:"column:image"`
	Endpoint     string         `json:"endpoint,omitempty" gorm:"column:endpoint"`
	Auth         map[string]any `json:"auth,omitempty" gorm:"column:auth;type:jsonb"`
	Type         string         `json:"type,omitempty" gorm:"column:type"`
	Capabilities []string       `json:"capabilities,omitempty" gorm:"column:capabilities;type:jsonb"`
	Outbound     string         `json:"outbound,omitempty" gorm:"column:outbound"`
	CreatedAt    time.Time      `json:"created_at" gorm:"column:created_at;autoCreateTime"`
	UpdatedAt    time.Time      `json:"updated_at" gorm:"column:updated_at;autoUpdateTime"`
}

// TableName sets a friendly table name for GORM migrations.
func (AgentRecord) TableName() string { return "agents" }
