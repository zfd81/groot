package schedule

import "time"

// Task is the definition of a scheduled task
type Task struct {
	ID             string             `json:"id"`
	Name           string             `json:"name"`
	Schedule       string             `json:"schedule"`        // cron / ISO8601 / Go duration
	MissedPolicy   string             `json:"missed_policy"`   // run_once / skip
	TaskDef        TaskDef            `json:"task"`
	Notification   NotificationConfig `json:"notification"`
	CreatedAt      time.Time          `json:"created_at"`
	UpdatedAt      time.Time          `json:"updated_at"`
}

// TaskDef is the task execution definition
type TaskDef struct {
	Instruction  string `json:"instruction"`
	Model        string `json:"model"`
	SystemPrompt string `json:"system_prompt"`
}

// NotificationConfig specifies notification channels
type NotificationConfig struct {
	OnSuccess []string `json:"on_success"`
	OnFailure []string `json:"on_failure"`
}

// ExecutionRecord records a single task execution
type ExecutionRecord struct {
	TaskID        string             `json:"task_id"`
	ExecTime      time.Time          `json:"exec_time"`
	TriggerType   string             `json:"trigger_type"` // cron / once / interval / manual
	SessionID     string             `json:"session_id"`
	ChatID        string             `json:"chat_id"`
	Status        string             `json:"status"` // completed / failed / cancelled
	DurationMs    int64              `json:"duration_ms"`
	StepCount     int                `json:"step_count"`
	Error         string             `json:"error"`
	Notifications []NotificationResult `json:"notifications"`
}

// NotificationResult records the result of a notification send
type NotificationResult struct {
	Channel   string    `json:"channel"`
	Sent      bool      `json:"sent"`
	Message   string    `json:"message"`
	Timestamp time.Time `json:"timestamp"`
}

// ScheduleType represents the type of schedule expression
type ScheduleType int

const (
	ScheduleTypeCron    ScheduleType = iota // cron expression
	ScheduleTypeOnce                        // ISO8601 timestamp
	ScheduleTypeInterval                    // Go duration
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
