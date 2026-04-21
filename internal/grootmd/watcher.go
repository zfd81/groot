package grootmd

import (
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
)

// Watcher monitors GROOT.md file for hot-reload
type Watcher struct {
	homeDir  string
	watcher  *fsnotify.Watcher
	stopChan chan struct{}
	log      *logger.Logger
	mu       sync.Mutex
}

// NewWatcher creates a new GROOT.md watcher
func NewWatcher(homeDir string, log *logger.Logger) *Watcher {
	return &Watcher{
		homeDir:  homeDir,
		stopChan: make(chan struct{}),
		log:      log,
	}
}

// Start begins watching GROOT.md file
func (w *Watcher) Start() error {
	// 初始加载
	w.reload()

	// 启动 fsnotify 监听
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		w.log.Error("无法创建 GROOT.md watcher", zap.Error(err))
		return err
	}
	w.watcher = watcher

	// 监听 homeDir 目录（文件可能不存在，监听目录以感知文件创建）
	if err := watcher.Add(w.homeDir); err != nil {
		w.log.Error("无法监听 GROOT_HOME 目录", zap.String("dir", w.homeDir), zap.Error(err))
		return err
	}

	go w.run()

	w.log.Info("GROOT.md watcher 已启动", zap.String("home", w.homeDir))
	return nil
}

// Stop stops the watcher
func (w *Watcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()

	select {
	case <-w.stopChan:
		// 已经关闭
		return
	default:
		close(w.stopChan)
	}

	if w.watcher != nil {
		w.watcher.Close()
	}
	w.log.Info("GROOT.md watcher 已停止")
}

// reload reads GROOT.md content or clears cache
func (w *Watcher) reload() {
	path := filepath.Join(w.homeDir, "GROOT.md")

	// 文件不存在，清空缓存
	if _, err := os.Stat(path); os.IsNotExist(err) {
		SetContent("")
		w.log.Debug("GROOT.md 不存在，清空缓存")
		return
	}

	// 读取文件内容
	content, err := os.ReadFile(path)
	if err != nil {
		w.log.Info("无法读取 GROOT.md", zap.Error(err))
		SetContent("")
		return
	}

	// 内容为空，清空缓存
	if len(content) == 0 {
		SetContent("")
		w.log.Debug("GROOT.md 为空，清空缓存")
		return
	}

	// 写入缓存
	SetContent(string(content))
	w.log.Info("GROOT.md 已加载", zap.Int("size", len(content)))
}

// run handles file events
func (w *Watcher) run() {
	for {
		select {
		case <-w.stopChan:
			return
		case event, ok := <-w.watcher.Events:
			if !ok {
				return
			}
			// 只处理 GROOT.md 相关事件
			if strings.HasSuffix(event.Name, "GROOT.md") {
				w.log.Debug("GROOT.md 文件变化", zap.String("event", event.Op.String()))
				w.reload()
			}
		case err, ok := <-w.watcher.Errors:
			if !ok {
				return
			}
			w.log.Error("GROOT.md watcher 错误", zap.Error(err))
		}
	}
}