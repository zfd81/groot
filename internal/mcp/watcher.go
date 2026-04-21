package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
)

// Watcher monitors MCP directory for hot-reload
type Watcher struct {
	manager  *Manager
	config   config.MCPConfig
	logger   *logger.Logger
	watcher  *fsnotify.Watcher
	debounce map[string]time.Time
	mu       sync.Mutex
	stopChan chan struct{}
}

// NewWatcher creates a new MCP watcher
func NewWatcher(manager *Manager, cfg config.MCPConfig, log *logger.Logger) *Watcher {
	return &Watcher{
		manager:  manager,
		config:   cfg,
		logger:   log,
		debounce: make(map[string]time.Time),
		stopChan: make(chan struct{}),
	}
}

// Start begins watching the MCP directory
func (w *Watcher) Start(dir string) error {
	if !w.config.HotReload.Enabled {
		return nil
	}

	// Ensure directory exists
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	w.watcher = watcher

	if err := watcher.Add(dir); err != nil {
		return err
	}

	go w.run(dir)
	return nil
}

// run handles file events with debouncing
func (w *Watcher) run(dir string) {
	debounceDelay := time.Duration(w.config.HotReload.DebounceDelay) * time.Second

	for {
		select {
		case <-w.stopChan:
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			w.handleEvent(event, debounceDelay)
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			w.logger.Error("MCP watcher error", zap.Error(err))
		}
	}
}

// handleEvent processes a file event
func (w *Watcher) handleEvent(event fsnotify.Event, debounceDelay time.Duration) {
	// Only process .json files
	if !strings.HasSuffix(event.Name, ".json") {
		return
	}

	// Record the time when this event arrived
	w.mu.Lock()
	eventTime := time.Now()
	w.debounce[event.Name] = eventTime
	w.mu.Unlock()

	// Wait for debounce period
	time.Sleep(debounceDelay)

	// Check if a newer event arrived during the debounce period
	w.mu.Lock()
	currentTime := w.debounce[event.Name]
	// Only process if this event's timestamp is still the latest
	shouldProcess := currentTime.Equal(eventTime)
	if shouldProcess {
		delete(w.debounce, event.Name)
	}
	w.mu.Unlock()

	if !shouldProcess {
		// A newer event arrived, skip this one
		return
	}

	// Process event
	switch event.Op {
	case fsnotify.Create, fsnotify.Write:
		if err := w.manager.Load(event.Name); err != nil {
			w.logger.Error("failed to load MCP", zap.Error(err), zap.String("path", event.Name))
		} else {
			mcpName := extractMCPName(event.Name)
			w.logger.LogMCPHotReload("added", mcpName, "config", w.manager.Count())
		}

	case fsnotify.Remove, fsnotify.Rename:
		mcpName := extractMCPName(event.Name)
		w.manager.Unregister(mcpName)
		w.logger.LogMCPHotReload("removed", mcpName, "config", w.manager.Count())
	}
}

// Stop stops the watcher
func (w *Watcher) Stop() {
	close(w.stopChan)
	if w.watcher != nil {
		w.watcher.Close()
	}
}

// extractMCPName extracts MCP name from path
func extractMCPName(path string) string {
	filename := filepath.Base(path)
	return strings.TrimSuffix(filename, ".json")
}
