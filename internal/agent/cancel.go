package agent

import (
	"sync"
)

// CancelManager manages task cancellation
type CancelManager struct {
	cancellations map[string]chan struct{}
	mu            sync.RWMutex
}

// NewCancelManager creates a new cancel manager
func NewCancelManager() *CancelManager {
	return &CancelManager{
		cancellations: make(map[string]chan struct{}),
	}
}

// Register registers a task for cancellation tracking
func (c *CancelManager) Register(taskID string) chan struct{} {
	c.mu.Lock()
	defer c.mu.Unlock()

	ch := make(chan struct{})
	c.cancellations[taskID] = ch
	return ch
}

// Cancel cancels a task
func (c *CancelManager) Cancel(taskID string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	if ch, ok := c.cancellations[taskID]; ok {
		close(ch)
		delete(c.cancellations, taskID)
		return true
	}
	return false
}

// Unregister removes task from cancellation tracking
func (c *CancelManager) Unregister(taskID string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if _, ok := c.cancellations[taskID]; ok {
		// Don't close the channel - let the task finish naturally
		delete(c.cancellations, taskID)
	}
}

// IsCancelled checks if a task is cancelled
func (c *CancelManager) IsCancelled(taskID string) bool {
	c.mu.RLock()
	defer c.mu.RUnlock()

	_, ok := c.cancellations[taskID]
	return !ok // If not in map, it was either cancelled or finished
}

// Count returns number of tracked tasks
func (c *CancelManager) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.cancellations)
}
