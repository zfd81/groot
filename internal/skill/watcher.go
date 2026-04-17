package skill

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

// Watcher monitors skills directory for hot-reload
type Watcher struct {
	loader   *Loader
	config   config.SkillsConfig
	logger   *logger.Logger
	watcher  *fsnotify.Watcher
	debounce map[string]time.Time
	mu       sync.Mutex
	stopChan chan struct{}
}

// NewWatcher creates a new watcher
func NewWatcher(loader *Loader, cfg config.SkillsConfig, log *logger.Logger) *Watcher {
	return &Watcher{
		loader:   loader,
		config:   cfg,
		logger:   log,
		debounce: make(map[string]time.Time),
		stopChan: make(chan struct{}),
	}
}

// Start begins watching the skills directory
func (w *Watcher) Start(dir string) error {
	if !w.config.HotReload.Enabled {
		return nil // Hot-reload disabled
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	w.watcher = watcher

	// Watch the directory
	if err := watcher.Add(dir); err != nil {
		return err
	}

	// Watch existing skill subdirectories
	entries, err := os.ReadDir(dir)
	if err == nil {
		for _, entry := range entries {
			if entry.IsDir() {
				subdir := filepath.Join(dir, entry.Name())
				watcher.Add(subdir)
			}
		}
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
			w.handleEvent(dir, event, debounceDelay)
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			w.logger.Error("watcher error", zap.Error(err))
		}
	}
}

// handleEvent processes a file event
func (w *Watcher) handleEvent(dir string, event fsnotify.Event, debounceDelay time.Duration) {
	// Check if it's a new directory - add to watcher
	if event.Op == fsnotify.Create {
		info, err := os.Stat(event.Name)
		if err == nil && info.IsDir() {
			w.watcher.Add(event.Name)
			return
		}
	}

	// Only process SKILL.md files
	if !strings.HasSuffix(event.Name, "SKILL.md") {
		return
	}

	// Debounce: record event time
	w.mu.Lock()
	w.debounce[event.Name] = time.Now()
	w.mu.Unlock()

	// Wait for debounce delay
	time.Sleep(debounceDelay)

	// Check if another event happened during debounce
	w.mu.Lock()
	lastTime := w.debounce[event.Name]
	delete(w.debounce, event.Name) // Clean up
	w.mu.Unlock()

	if time.Since(lastTime) < debounceDelay {
		return // Another event occurred, skip this one
	}

	// Process event based on operation
	switch event.Op {
	case fsnotify.Create, fsnotify.Write:
		if err := w.loader.Load(event.Name); err != nil {
			w.logger.Error("failed to load skill", zap.Error(err))
		} else {
			skillName := extractSkillName(event.Name)
			w.logger.LogSkillHotReload("added", skillName, w.loader.registry.Count())
		}

	case fsnotify.Remove, fsnotify.Rename:
		w.loader.Unload(event.Name)
		skillName := extractSkillName(event.Name)
		w.logger.LogSkillHotReload("removed", skillName, w.loader.registry.Count())
	}
}

// Stop stops the watcher
func (w *Watcher) Stop() {
	close(w.stopChan)
	if w.watcher != nil {
		w.watcher.Close()
	}
}

// extractSkillName extracts skill name from SKILL.md path
func extractSkillName(path string) string {
	// Path format: .../skills/{skill_name}/SKILL.md
	parts := strings.Split(path, "/")
	for i, part := range parts {
		if part == "skills" && i+1 < len(parts) {
			return parts[i+1]
		}
	}
	// Fallback: use directory name
	dir := filepath.Dir(path)
	return filepath.Base(dir)
}
