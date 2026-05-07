package config

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigNotExists(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "no_config")

	_, err := Load(homeDir)
	if err == nil {
		t.Fatal("配置不存在时应返回错误")
	}

	// 检查错误信息包含 groot init 提示
	if !strings.Contains(err.Error(), "groot init") {
		t.Errorf("错误信息应包含 'groot init' 提示，实际: %s", err.Error())
	}
}