package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestGenerateBootstrapTemplate_Parses 模板必须是合法 yaml，
// 且解析后等价于全套缺省值（模板全注释）。
func TestGenerateBootstrapTemplate_Parses(t *testing.T) {
	tpl := GenerateBootstrapTemplate()

	var b Bootstrap
	if err := yaml.Unmarshal([]byte(tpl), &b); err != nil {
		t.Fatalf("模板不是合法 yaml: %v", err)
	}
	if b.Server.Port != 0 || b.Database != nil {
		t.Errorf("模板应全注释，解析后字段为零值, got port=%d database=%+v", b.Server.Port, b.Database)
	}

	// 经 LoadBootstrap 读取后应拿到缺省值
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, BootstrapFileName), []byte(tpl), 0600); err != nil {
		t.Fatalf("写入模板失败: %v", err)
	}
	loaded, err := LoadBootstrap(dir)
	if err != nil {
		t.Fatalf("LoadBootstrap: %v", err)
	}
	if loaded.Server.Port != 8080 || loaded.Logging.Level != "info" {
		t.Errorf("模板经加载后应为缺省值, got port=%d level=%q", loaded.Server.Port, loaded.Logging.Level)
	}
}

// TestGenerateBootstrapTemplate_NoBusinessSections 模板不应引导使用者
// 在文件里配置已迁入配置表的业务项。
func TestGenerateBootstrapTemplate_NoBusinessSections(t *testing.T) {
	tpl := GenerateBootstrapTemplate()
	for _, banned := range []string{"react:", "attachment:", "subagent:", "memory:", "senders:", "secret:"} {
		if strings.Contains(tpl, banned) {
			t.Errorf("模板不应包含已迁入配置表的 %q", banned)
		}
	}
	// 数据库两种驱动各一个示例块
	if !strings.Contains(tpl, "driver: mysql") || !strings.Contains(tpl, "driver: postgres") {
		t.Error("模板应为 MySQL 与 PostgreSQL 各提供示例")
	}
}
