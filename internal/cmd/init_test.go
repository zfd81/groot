package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseInitFlags(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantError bool
		errMsg    string
	}{
		{
			name:      "default values",
			args:      []string{},
			wantError: false,
		},
		{
			name:      "unknown flag",
			args:      []string{"--invalid"},
			wantError: true,
			errMsg:    "unknown flag: --invalid",
		},
		{
			name:      "unexpected argument",
			args:      []string{"unexpected"},
			wantError: true,
			errMsg:    "unexpected argument: unexpected",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseInitFlags(tt.args)

			if tt.wantError {
				if err == nil {
					t.Errorf("expected error but got nil")
				} else if err.Error() != tt.errMsg {
					t.Errorf("expected error '%s' but got '%s'", tt.errMsg, err.Error())
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}
		})
	}
}

func TestGetDefaultHome(t *testing.T) {
	// Test that GROOT_HOME env var is used as default
	os.Setenv("GROOT_HOME", "/custom/groot")
	defer os.Unsetenv("GROOT_HOME")

	homeDir := GetDefaultHome()

	if homeDir != "/custom/groot" {
		t.Errorf("expected HomeDir '/custom/groot' but got '%s'", homeDir)
	}
}

func TestRunInit(t *testing.T) {
	// 创建临时测试目录
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "test_groot")

	err := RunInit(homeDir)
	if err != nil {
		t.Fatalf("RunInit failed: %v", err)
	}

	// 检查目录创建
	expectedDirs := []string{"skills", "mcp", "subagents", "memory", "logs", "cluster/members"}
	for _, dir := range expectedDirs {
		path := filepath.Join(homeDir, dir)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("目录 %s 未创建", dir)
		}
	}

	// 检查配置文件创建
	configPath := filepath.Join(homeDir, "config.yaml")
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Errorf("配置文件未创建")
	}
}

func TestRunInitExistingDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "existing_groot")

	// 预创建目录
	os.MkdirAll(filepath.Join(homeDir, "skills"), 0755)
	os.MkdirAll(filepath.Join(homeDir, "mcp"), 0755)

	err := RunInit(homeDir)
	if err != nil {
		t.Fatalf("RunInit failed: %v", err)
	}

	// 检查所有目录仍存在
	expectedDirs := []string{"skills", "mcp", "subagents", "memory", "logs", "cluster/members"}
	for _, dir := range expectedDirs {
		path := filepath.Join(homeDir, dir)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("目录 %s 不存在", dir)
		}
	}
}

func TestRunInitExistingConfig(t *testing.T) {
	tmpDir := t.TempDir()
	homeDir := filepath.Join(tmpDir, "config_exists")

	// 预创建配置文件
	os.MkdirAll(homeDir, 0755)
	configPath := filepath.Join(homeDir, "config.yaml")
	os.WriteFile(configPath, []byte("existing: config"), 0644)

	err := RunInit(homeDir)
	if err != nil {
		t.Fatalf("RunInit failed: %v", err)
	}

	// 检查配置文件未被覆盖
	data, _ := os.ReadFile(configPath)
	if string(data) != "existing: config" {
		t.Errorf("配置文件被覆盖了")
	}
}

// TestRunInit_CreatesSubAgentsDir 验证 init 创建 subagents/ 子目录（设计 10.2 节）。
func TestRunInit_CreatesSubAgentsDir(t *testing.T) {
	home := t.TempDir()
	if err := RunInit(home); err != nil {
		t.Fatalf("RunInit failed: %v", err)
	}
	stat, err := os.Stat(filepath.Join(home, "subagents"))
	if err != nil || !stat.IsDir() {
		t.Fatalf("subagents/ should be created, err=%v", err)
	}
}

// TestRunInit_WritesGrootMdWithSchedulingHint 验证 init 写入默认 GROOT.md，
// 内容包含「子 Agent 调度」段与 call_agent 工具引导（设计 10.2 节）。
func TestRunInit_WritesGrootMdWithSchedulingHint(t *testing.T) {
	home := t.TempDir()
	if err := RunInit(home); err != nil {
		t.Fatalf("RunInit failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(home, "GROOT.md"))
	if err != nil {
		t.Fatalf("GROOT.md 未创建: %v", err)
	}
	got := string(data)
	for _, want := range []string{"子 Agent 调度", "call_agent", "按需调用", "逐个调用", "明确传参", "附件引用"} {
		if !strings.Contains(got, want) {
			t.Errorf("GROOT.md 缺少关键词 %q\n实际内容:\n%s", want, got)
		}
	}
}

// TestRunInit_PreservesExistingGrootMd 验证用户已有的 GROOT.md 不会被覆盖。
func TestRunInit_PreservesExistingGrootMd(t *testing.T) {
	home := t.TempDir()
	if err := os.MkdirAll(home, 0755); err != nil {
		t.Fatal(err)
	}
	custom := "# 我自己的 GROOT.md\n请别覆盖我。\n"
	mdPath := filepath.Join(home, "GROOT.md")
	if err := os.WriteFile(mdPath, []byte(custom), 0644); err != nil {
		t.Fatal(err)
	}

	if err := RunInit(home); err != nil {
		t.Fatalf("RunInit failed: %v", err)
	}

	data, err := os.ReadFile(mdPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != custom {
		t.Errorf("用户自定义 GROOT.md 被覆盖\n期望:\n%s\n实际:\n%s", custom, string(data))
	}
}