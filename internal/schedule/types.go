package schedule

import (
	"encoding/json"
	"time"
)

// Task is the definition of a scheduled task
type Task struct {
	ID           string             `json:"id"`
	Name         string             `json:"name"`
	Schedule     string             `json:"schedule"`         // cron / ISO8601 / Go duration
	Status       string             `json:"status,omitempty"` // active / disabled / archive (populated by repo)
	MissedPolicy string             `json:"missed_policy"`    // run_once / skip
	TaskDef      TaskDef            `json:"task"`
	Notification NotificationConfig `json:"notification"`
	CreatedAt    time.Time          `json:"created_at"`
	UpdatedAt    time.Time          `json:"updated_at"`
	Version      int64              `json:"version,omitempty"`
}

// TaskDef is the task execution definition
type TaskDef struct {
	Instruction  string `json:"instruction"`
	Model        string `json:"model"`
	SystemPrompt string `json:"system_prompt"`
}

// NotificationConfig holds natural-language notification requirements.
// After a task finishes, the matching requirement is handed to the LLM,
// which fulfils it with the configured MCP tools. Empty means no notification.
type NotificationConfig struct {
	OnSuccess string `json:"on_success"`
	OnFailure string `json:"on_failure"`
}

// UnmarshalJSON accepts string requirements; any other JSON value
// (e.g. legacy channel arrays like ["webhook"], or null) decodes to empty.
func (n *NotificationConfig) UnmarshalJSON(data []byte) error {
	var raw struct {
		OnSuccess json.RawMessage `json:"on_success"`
		OnFailure json.RawMessage `json:"on_failure"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	n.OnSuccess = rawToString(raw.OnSuccess)
	n.OnFailure = rawToString(raw.OnFailure)
	return nil
}

func rawToString(raw json.RawMessage) string {
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return ""
	}
	return s
}

// ExecutionRecord records a single task execution
type ExecutionRecord struct {
	ExecutionID string     `json:"execution_id"`
	TaskID      string     `json:"task_id"`
	StartedAt   time.Time  `json:"started_at"`  // renamed from ExecTime
	FinishedAt  *time.Time `json:"finished_at"` // nil while running
	TriggerType string     `json:"trigger_type"`
	SessionID   string     `json:"session_id"`
	ChatID      string     `json:"chat_id"`
	Status      string     `json:"status"`
	DurationMs  int64      `json:"duration_ms"`
	StepCount   int        `json:"step_count"`
	Error       string     `json:"error"`
}

// ScheduleType represents the type of schedule expression
type ScheduleType int

const (
	ScheduleTypeCron     ScheduleType = iota // cron expression
	ScheduleTypeOnce                         // ISO8601 timestamp
	ScheduleTypeInterval                     // Go duration
)

// ParseScheduleType detects the schedule type from the expression
func ParseScheduleType(schedule string) ScheduleType {
	// Try parsing as ISO8601 first (contains T or has exact date format)
	if t, err := time.Parse(time.RFC3339, schedule); err == nil {
		_ = t
		return ScheduleTypeOnce
	}

	// Try parsing as Go duration
	if _, err := time.ParseDuration(schedule); err == nil {
		return ScheduleTypeInterval
	}

	// Default to cron
	return ScheduleTypeCron
}
