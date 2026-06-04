package grootmd

import (
	"os"
	"path/filepath"
)

// GetContent 每次调用时直接读取 GROOT.md 文件。
// 文件存在且可读 → 返回内容；文件不存在/为空/读取失败 → 返回空字符串。
// 与 Skills 的 eino Backend 策略一致：无缓存，按需读取。
func GetContent(homeDir string) string {
	path := filepath.Join(homeDir, "GROOT.md")

	info, err := os.Stat(path)
	if err != nil || info.Size() == 0 {
		return ""
	}

	content, err := os.ReadFile(path)
	if err != nil {
		return ""
	}

	return string(content)
}
