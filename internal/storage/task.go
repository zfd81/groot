package storage

import (
	"time"
)

// TaskStatus represents task status
type TaskStatus string

const (
	StatusRunning   TaskStatus = "running"
	StatusCompleted TaskStatus = "completed"
	StatusFailed    TaskStatus = "failed"
	StatusCancelled TaskStatus = "cancelled"
)

// Task represents a task record
type Task struct {
	ID          string        `json:"id"`
	Instruction string        `json:"instruction"`
	Prompt      string        `json:"prompt,omitempty"`
	Attachments []Attachment  `json:"attachments,omitempty"`
	Status      TaskStatus    `json:"status"`
	Progress    *TaskProgress `json:"progress,omitempty"`
	Result      interface{}   `json:"result,omitempty"`
	Error       *TaskError    `json:"error,omitempty"`
	StartTime   time.Time     `json:"start_time"`
	EndTime     time.Time     `json:"end_time,omitempty"`
	Duration    int           `json:"duration"` // seconds
	Caller      string        `json:"caller"`
	Steps       []StepRecord  `json:"steps,omitempty"`
}

// Attachment represents file attachment
type Attachment struct {
	Type    string `json:"type"` // file, url
	Name    string `json:"name"`
	Content string `json:"content"` // Base64 or URL
}

// AttachmentPath represents processed attachment path info
type AttachmentPath struct {
	OriginalName string `json:"original_name"` // Original file name
	Type         string `json:"type"`          // file, url, text
	FullPath     string `json:"full_path"`     // Absolute path on disk
	RelativePath string `json:"relative_path"` // Path relative to task temp dir
	Size         int64  `json:"size"`          // File size in bytes
	ContentType  string `json:"content_type"`  // MIME type
}

// TaskProgress represents execution progress
type TaskProgress struct {
	CurrentStep    int `json:"current_step"`
	StepsCompleted int `json:"steps_completed"`
	Percentage     int `json:"percentage"`
}

// TaskError represents error info
type TaskError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// StepRecord represents a step execution record
type StepRecord struct {
	StepID       string     `json:"step_id"`
	Type         string     `json:"type"` // skill, tool, llm
	Name         string     `json:"name"`
	StartTime    time.Time  `json:"start_time"`
	EndTime      time.Time  `json:"end_time,omitempty"`
	Status       TaskStatus `json:"status"`
	NestingLevel int        `json:"nesting_level"`
	Error        *TaskError `json:"error,omitempty"`
}

// TaskQuery represents query parameters
type TaskQuery struct {
	Status    []TaskStatus `json:"status,omitempty"`
	StartTime *time.Time   `json:"start_time,omitempty"`
	EndTime   *time.Time   `json:"end_time,omitempty"`
	Limit     int          `json:"limit"`
	Offset    int          `json:"offset"`
}
