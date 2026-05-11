package skills

import (
	"path/filepath"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
)

// Watcher monitors the skills directory for hot-reload
type Watcher struct {
	skillsDir     string
	fsWatcher     *fsnotify.Watcher
	stopChan      chan struct{}
	log           *logger.Logger
	cfg           config.HotReloadConfig
	debounceTimer *time.Timer
	mu            sync.Mutex
}

// NewWatcher creates a new skills directory watcher
func NewWatcher(skillsDir string, cfg config.HotReloadConfig, log *logger.Logger) *Watcher {
	return &Watcher{
		skillsDir: skillsDir,
		stopChan:  make(chan struct{}),
		log:       log,
		cfg:       cfg,
	}
}

// Start begins watching the skills directory
func (w *Watcher) Start() error {
	if !w.cfg.Enabled {
		w.log.Info("Skills 热插拔已禁用")
		return nil
	}

	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		w.log.Error("无法创建 Skills watcher", zap.Error(err))
		return err
	}
	w.fsWatcher = watcher

	if err := watcher.Add(w.skillsDir); err != nil {
		w.log.Error("无法监听 Skills 目录", zap.String("dir", w.skillsDir), zap.Error(err))
		return err
	}

	go w.run()
	w.log.Info("Skills watcher 已启动", zap.String("dir", w.skillsDir))
	return nil
}

// Stop stops the watcher
func (w *Watcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()

	select {
	case <-w.stopChan:
		return
	default:
		close(w.stopChan)
	}

	if w.fsWatcher != nil {
		w.fsWatcher.Close()
	}
	w.log.Info("Skills watcher 已停止")
}

// run handles file events with debounce
func (w *Watcher) run() {
	for {
		select {
		case <-w.stopChan:
			return
		case event, ok := <-w.fsWatcher.Events:
			if !ok {
				return
			}
			if w.isSkillChange(event) {
				w.debounce()
			}
		case err, ok := <-w.fsWatcher.Errors:
			if !ok {
				return
			}
			w.log.Error("Skills watcher 错误", zap.Error(err))
		}
	}
}

// isSkillChange checks if the event is a skill-related change
func (w *Watcher) isSkillChange(event fsnotify.Event) bool {
	// Watch for SKILL.md changes or directory changes
	if filepath.Base(event.Name) == "SKILL.md" {
		return true
	}
	// Directory creation/removal might indicate skill install/uninstall
	if event.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 {
		return true
	}
	return false
}

// debounce triggers a reload after the configured delay, resetting on each call
func (w *Watcher) debounce() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.debounceTimer != nil {
		w.debounceTimer.Stop()
	}
	w.debounceTimer = time.AfterFunc(time.Duration(w.cfg.DebounceDelay)*time.Second, func() {
		w.reload()
	})
}

// reload re-scans the skills directory and logs the event
func (w *Watcher) reload() {
	entries, err := filepath.Glob(filepath.Join(w.skillsDir, "*/SKILL.md"))
	if err != nil {
		w.log.Error("Skills 重载失败", zap.Error(err))
		return
	}

	count := len(entries)
	w.log.LogSkillHotReload("reloaded", "", count)
}
