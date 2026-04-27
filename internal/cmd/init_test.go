package cmd

import (
	"os"
	"path/filepath"
	"testing"
)

func TestParseInitFlags(t *testing.T) {
	tests := []struct {
		name      string
		args      []string
		wantHome  string
		wantError bool
		errMsg    string
	}{
		{
			name:     "default values",
			args:     []string{},
			wantHome: "", // will be set by getDefaultHome()
		},
		{
			name:     "short flag -H",
			args:     []string{"-H", "/opt/groot"},
			wantHome: "/opt/groot",
		},
		{
			name:     "long flag --home",
			args:     []string{"--home", "/opt/groot"},
			wantHome: "/opt/groot",
		},
		{
			name:      "missing value for -H",
			args:      []string{"-H"},
			wantError: true,
			errMsg:    "-H/--home requires a value",
		},
		{
			name:      "missing value for --home",
			args:      []string{"--home"},
			wantError: true,
			errMsg:    "-H/--home requires a value",
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
			flags, err := ParseInitFlags(tt.args)

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

			if tt.wantHome != "" && flags.HomeDir != tt.wantHome {
				t.Errorf("expected HomeDir '%s' but got '%s'", tt.wantHome, flags.HomeDir)
			}
		})
	}
}

func TestParseInitFlagsWithEnvVar(t *testing.T) {
	// Test that GROOT_HOME env var is used as default
	os.Setenv("GROOT_HOME", "/custom/groot")
	defer os.Unsetenv("GROOT_HOME")

	flags, err := ParseInitFlags([]string{})
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}

	if flags.HomeDir != "/custom/groot" {
		t.Errorf("expected HomeDir '/custom/groot' but got '%s'", flags.HomeDir)
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
	expectedDirs := []string{"skills", "mcp", "memory", "logs"}
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
	expectedDirs := []string{"skills", "mcp", "memory", "logs"}
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