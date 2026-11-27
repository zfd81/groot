package plugin

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// writeConfig 在临时目录写入配置文件并返回其路径。
func writeConfig(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("写入配置文件失败: %v", err)
	}
	return path
}

func TestLoadGlobalConfig_MissingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "not-exist.yaml")

	cfg, err := LoadGlobalConfig(path)
	if err != nil {
		t.Fatalf("配置文件不存在时不应返回错误, got %v", err)
	}
	if cfg.StartupTimeout != 180*time.Second {
		t.Errorf("StartupTimeout = %v, want %v", cfg.StartupTimeout, 180*time.Second)
	}
	if cfg.RequestTimeout != 120*time.Second {
		t.Errorf("RequestTimeout = %v, want %v", cfg.RequestTimeout, 120*time.Second)
	}
	if !reflect.DeepEqual(cfg, DefaultGlobalConfig()) {
		t.Errorf("配置 = %+v, want %+v", cfg, DefaultGlobalConfig())
	}
}

func TestLoadGlobalConfig_ExplicitValues(t *testing.T) {
	path := writeConfig(t, `
startup_timeout: 300s
request_timeout: 60s
permission:
  asr-router: chat
`)

	cfg, err := LoadGlobalConfig(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	if cfg.StartupTimeout != 300*time.Second {
		t.Errorf("StartupTimeout = %v, want %v", cfg.StartupTimeout, 300*time.Second)
	}
	if cfg.RequestTimeout != 60*time.Second {
		t.Errorf("RequestTimeout = %v, want %v", cfg.RequestTimeout, 60*time.Second)
	}
	if got := cfg.Permission["asr-router"]; got != "chat" {
		t.Errorf("Permission[asr-router] = %q, want %q", got, "chat")
	}
}

func TestLoadGlobalConfig_UnknownField(t *testing.T) {
	path := writeConfig(t, "unknown: 1\n")

	cfg, err := LoadGlobalConfig(path)
	if err == nil {
		t.Fatal("未知字段应返回错误")
	}
	if !reflect.DeepEqual(cfg, DefaultGlobalConfig()) {
		t.Errorf("失败时配置 = %+v, want 默认值 %+v", cfg, DefaultGlobalConfig())
	}
}

func TestLoadGlobalConfig_InvalidDuration(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{name: "startup 为零", content: "startup_timeout: 0s\n"},
		{name: "request 为负", content: "request_timeout: -5s\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			path := writeConfig(t, tt.content)

			cfg, err := LoadGlobalConfig(path)
			if err == nil {
				t.Fatal("非正数超时应返回错误")
			}
			if !reflect.DeepEqual(cfg, DefaultGlobalConfig()) {
				t.Errorf("失败时配置 = %+v, want 默认值 %+v", cfg, DefaultGlobalConfig())
			}
		})
	}
}

func TestValidPermission(t *testing.T) {
	valid := []string{"chat", "status", "detail", "history", "delete", "session", "schedule", "all"}
	for _, p := range valid {
		if !ValidPermission(p) {
			t.Errorf("ValidPermission(%q) = false, want true", p)
		}
	}

	invalid := []string{"Chat", "foo", ""}
	for _, p := range invalid {
		if ValidPermission(p) {
			t.Errorf("ValidPermission(%q) = true, want false", p)
		}
	}
}

func TestLoadGlobalConfig_KeepsUnknownPermissionValue(t *testing.T) {
	path := writeConfig(t, `
permission:
  asr-router: nope
`)

	cfg, err := LoadGlobalConfig(path)
	if err != nil {
		t.Fatalf("未知 permission 值不应导致加载失败: %v", err)
	}
	if got := cfg.Permission["asr-router"]; got != "nope" {
		t.Errorf("Permission[asr-router] = %q, want %q (原样保留)", got, "nope")
	}
}

func TestLoadGlobalConfig_CommentOnlyFile(t *testing.T) {
	// groot plugins init 写入的全注释模板，等价于全部默认值。
	path := writeConfig(t, `# Python 插件全局配置；取消注释即生效，缺省值如下。
# startup_timeout: 180s
# request_timeout: 120s
# permission:
#   asr-router: chat
`)

	cfg, err := LoadGlobalConfig(path)
	if err != nil {
		t.Fatalf("全注释模板不应返回错误: %v", err)
	}
	if !reflect.DeepEqual(cfg, DefaultGlobalConfig()) {
		t.Errorf("配置 = %+v, want 默认值 %+v", cfg, DefaultGlobalConfig())
	}
}

func TestLoadGlobalConfig_NilPermissionMap(t *testing.T) {
	path := writeConfig(t, "permission:\n")

	cfg, err := LoadGlobalConfig(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	if cfg.Permission == nil {
		t.Fatal("Permission 为 nil, want 非 nil 空 map")
	}
	if len(cfg.Permission) != 0 {
		t.Errorf("Permission = %v, want 空 map", cfg.Permission)
	}
}

func TestLoadGlobalConfig_PartialValues(t *testing.T) {
	path := writeConfig(t, "request_timeout: 60s\n")

	cfg, err := LoadGlobalConfig(path)
	if err != nil {
		t.Fatalf("加载配置失败: %v", err)
	}
	if cfg.StartupTimeout != 180*time.Second {
		t.Errorf("StartupTimeout = %v, want 默认值 %v", cfg.StartupTimeout, 180*time.Second)
	}
	if cfg.RequestTimeout != 60*time.Second {
		t.Errorf("RequestTimeout = %v, want %v", cfg.RequestTimeout, 60*time.Second)
	}
}
