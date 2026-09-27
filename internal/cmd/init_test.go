package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/zfd81/groot/internal/config"
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
	expectedDirs := []string{"skills", "mcp", "subagents", "logs"}
	for _, dir := range expectedDirs {
		path := filepath.Join(homeDir, dir)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("目录 %s 未创建", dir)
		}
	}

	// 检查配置文件创建：init 只产出 bootstrap.yaml
	bootstrapPath := filepath.Join(homeDir, config.BootstrapFileName)
	stat, err := os.Stat(bootstrapPath)
	if err != nil {
		t.Fatalf("stat bootstrap.yaml: %v", err)
	}
	// bootstrap.yaml 可能承载数据库凭据，权限要求 0600（仅当前用户可读写）
	if perm := stat.Mode().Perm(); perm != 0o600 {
		t.Errorf("bootstrap.yaml 权限 = %o, want 0600（可能承载数据库凭据应私密）", perm)
	}

	// 模板应可被 LoadBootstrap 加载，且全注释模板等价于缺省配置
	b, err := config.LoadBootstrap(homeDir)
	if err != nil {
		t.Fatalf("LoadBootstrap: %v", err)
	}
	if b.Server.Port != 8080 {
		t.Errorf("Server.Port = %d, want 8080（全注释模板应得到缺省值）", b.Server.Port)
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
	expectedDirs := []string{"skills", "mcp", "subagents", "logs"}
	for _, dir := range expectedDirs {
		path := filepath.Join(homeDir, dir)
		if _, err := os.Stat(path); os.IsNotExist(err) {
			t.Errorf("目录 %s 不存在", dir)
		}
	}
}

// TestRunInit_PreservesExistingBootstrap 已存在的 bootstrap.yaml 不被覆盖
func TestRunInit_PreservesExistingBootstrap(t *testing.T) {
	home := t.TempDir()
	custom := "server:\n  port: 9999\n"
	p := filepath.Join(home, config.BootstrapFileName)
	if err := os.WriteFile(p, []byte(custom), 0600); err != nil {
		t.Fatalf("预置文件: %v", err)
	}
	if err := RunInit(home); err != nil {
		t.Fatalf("RunInit: %v", err)
	}
	data, _ := os.ReadFile(p)
	if string(data) != custom {
		t.Errorf("用户自定义 bootstrap.yaml 被覆盖:\n%s", data)
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

// TestRunInit_NoLegacyFiles init 不再产出老配置文件
func TestRunInit_NoLegacyFiles(t *testing.T) {
	home := t.TempDir()
	if err := RunInit(home); err != nil {
		t.Fatalf("RunInit: %v", err)
	}
	for _, legacy := range []string{"config.yaml", "env.yaml"} {
		if _, err := os.Stat(filepath.Join(home, legacy)); !os.IsNotExist(err) {
			t.Errorf("init 不应生成 %s", legacy)
		}
	}
}
