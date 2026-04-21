package config

import "path/filepath"

// ResolvePath 解析路径：绝对路径直接使用，相对路径相对于 homeDir
func ResolvePath(path, homeDir string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(homeDir, path)
}