package grootmd

import "sync"

// 全局 GROOT.md 内容缓存
var (
	content string
	mu      sync.RWMutex
)

// SetContent 设置 GROOT.md 内容
func SetContent(c string) {
	mu.Lock()
	content = c
	mu.Unlock()
}

// GetContent 获取 GROOT.md 内容
func GetContent() string {
	mu.RLock()
	defer mu.RUnlock()
	return content
}