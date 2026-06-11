package repo

// TaskStatus is the lifecycle state of a scheduled task.
type TaskStatus = string

const (
	TaskStatusActive   TaskStatus = "active"
	TaskStatusDisabled TaskStatus = "disabled"
	TaskStatusArchive  TaskStatus = "archive"
)
