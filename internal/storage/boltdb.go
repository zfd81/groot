package storage

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"time"

	bolt "go.etcd.io/bbolt"
)

// BoltDBStorage implements TaskStorage using BoltDB
type BoltDBStorage struct {
	db         *bolt.DB
	bucketName string
}

// NewBoltDBStorage creates a BoltDB storage instance
func NewBoltDBStorage(dbPath string, bucketName string) (*BoltDBStorage, error) {
	// Ensure directory exists
	dir := filepath.Dir(dbPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create db directory: %w", err)
	}

	db, err := bolt.Open(dbPath, 0600, &bolt.Options{Timeout: 1 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("failed to open boltdb: %w", err)
	}

	// Create bucket
	err = db.Update(func(tx *bolt.Tx) error {
		_, err := tx.CreateBucketIfNotExists([]byte(bucketName))
		return err
	})
	if err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to create bucket: %w", err)
	}

	return &BoltDBStorage{
		db:         db,
		bucketName: bucketName,
	}, nil
}

// Create stores a new task
func (s *BoltDBStorage) Create(task *Task) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(s.bucketName))
		data, err := json.Marshal(task)
		if err != nil {
			return fmt.Errorf("failed to marshal task: %w", err)
		}
		return b.Put([]byte(task.ID), data)
	})
}

// Get retrieves a task by ID
func (s *BoltDBStorage) Get(taskID string) (*Task, error) {
	var task Task
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(s.bucketName))
		data := b.Get([]byte(taskID))
		if data == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}
		return json.Unmarshal(data, &task)
	})
	if err != nil {
		return nil, err
	}
	return &task, nil
}

// Update updates task fields
func (s *BoltDBStorage) Update(taskID string, updates map[string]interface{}) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(s.bucketName))
		data := b.Get([]byte(taskID))
		if data == nil {
			return fmt.Errorf("task not found: %s", taskID)
		}

		var task Task
		if err := json.Unmarshal(data, &task); err != nil {
			return fmt.Errorf("failed to unmarshal task: %w", err)
		}

		// Apply updates to task fields
		for key, value := range updates {
			if value == nil {
				continue
			}
			switch key {
			case "status":
				if v, ok := value.(string); ok {
					task.Status = TaskStatus(v)
				}
			case "progress":
				if v, ok := value.(*TaskProgress); ok {
					task.Progress = v
				} else {
					task.Progress = nil
				}
			case "result":
				task.Result = value // interface{}, accept any
			case "error":
				if v, ok := value.(*TaskError); ok {
					task.Error = v
				} else {
					task.Error = nil
				}
			case "end_time":
				if v, ok := value.(time.Time); ok {
					task.EndTime = v
				}
			case "duration":
				if v, ok := value.(int); ok {
					task.Duration = v
				}
			case "steps":
				if v, ok := value.([]StepRecord); ok {
					task.Steps = v
				}
			}
		}

		newData, err := json.Marshal(task)
		if err != nil {
			return fmt.Errorf("failed to marshal task: %w", err)
		}
		return b.Put([]byte(taskID), newData)
	})
}

// Delete removes a task
func (s *BoltDBStorage) Delete(taskID string) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(s.bucketName))
		return b.Delete([]byte(taskID))
	})
}

// List queries tasks with filters and pagination
func (s *BoltDBStorage) List(query *TaskQuery) ([]*Task, int, error) {
	// Handle nil query with defaults
	if query == nil {
		query = &TaskQuery{}
	}

	var tasks []*Task
	var total int

	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(s.bucketName))
		c := b.Cursor()

		for k, v := c.First(); k != nil; k, v = c.Next() {
			var task Task
			if err := json.Unmarshal(v, &task); err != nil {
				continue // skip malformed records
			}

			// Filter by status
			if len(query.Status) > 0 {
				match := false
				for _, s := range query.Status {
					if task.Status == s {
						match = true
						break
					}
				}
				if !match {
					continue
				}
			}

			// Filter by time range
			if query.StartTime != nil && task.StartTime.Before(*query.StartTime) {
				continue
			}
			if query.EndTime != nil && task.StartTime.After(*query.EndTime) {
				continue
			}

			total++

			// Apply pagination
			if total > query.Offset && (query.Limit == 0 || len(tasks) < query.Limit) {
				tasks = append(tasks, &task)
			}
		}
		return nil
	})

	return tasks, total, err
}

// Exists checks if a task exists
func (s *BoltDBStorage) Exists(taskID string) bool {
	err := s.db.View(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(s.bucketName))
		data := b.Get([]byte(taskID))
		if data == nil {
			return fmt.Errorf("not found")
		}
		return nil
	})
	return err == nil
}

// Close closes the database
func (s *BoltDBStorage) Close() error {
	return s.db.Close()
}
