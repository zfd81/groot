package storage

// TaskStorage defines the storage interface for task persistence
type TaskStorage interface {
	// Create stores a new task record
	Create(task *Task) error

	// Get retrieves a task by ID
	Get(taskID string) (*Task, error)

	// Update updates specific fields of a task
	Update(taskID string, updates map[string]interface{}) error

	// Delete removes a task record
	Delete(taskID string) error

	// List queries tasks with filters and pagination
	List(query *TaskQuery) ([]*Task, int, error)

	// Exists checks if a task exists
	Exists(taskID string) bool

	// Close closes the storage connection
	Close() error
}
