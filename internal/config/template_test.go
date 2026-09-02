package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestGenerateConfigTemplate(t *testing.T) {
	content := GenerateConfigTemplate()

	// 模型配置已迁入数据库（Web UI 管理），模板不应再包含 llm 段
	if strings.Contains(content, "\nllm:") {
		t.Error("模板不应包含 llm 配置段（模型配置已迁入数据库）")
	}
	if strings.Contains(content, "default_model:") {
		t.Error("模板不应包含 default_model 配置（模型配置已迁入数据库）")
	}
	// 模板不再保留模型配置的迁移说明文案，保持文件头简洁
	if strings.Contains(content, "模型配置通过 Web UI 管理") {
		t.Error("模板不应包含模型配置迁移说明文案")
	}
	if !strings.Contains(content, "#") {
		t.Error("模板缺少注释说明")
	}
}

func TestGenerateConfigTemplate_NoStorageBlock(t *testing.T) {
	tpl := GenerateConfigTemplate()
	// database 凭据已剥离到 env.yaml，config.yaml 模板不应再生成 storage/minio/database 顶层节。
	if strings.Contains(tpl, "\nstorage:") {
		t.Error("config.yaml 模板不应包含 'storage:' 顶层节")
	}
	if strings.Contains(tpl, "minio") {
		t.Error("config.yaml 模板不应包含 minio 相关内容（存储抽象层已移除）")
	}
}

// TestGenerateConfigTemplate_NoRemovedKeys 验证已删除的配置项不再出现在
// 模板中：memory.directory（记忆已迁入数据库）、react.max_tokens 与
// react.nesting_max_depth（引擎从未据此终止，字段已从 ReactConfig 移除）。
func TestGenerateConfigTemplate_NoRemovedKeys(t *testing.T) {
	tpl := GenerateConfigTemplate()
	for _, key := range []string{"directory: memory", "max_tokens: 100000", "nesting_max_depth"} {
		if strings.Contains(tpl, key) {
			t.Errorf("模板不应再包含已删除的配置项 %q", key)
		}
	}
}

// TestGenerateConfigTemplate_HasMessageSection 验证 message 节出现在模板中：
// 它是生效配置（main.go 据此注册 webhook/email 发送器），必须对用户可见。
func TestGenerateConfigTemplate_HasMessageSection(t *testing.T) {
	tpl := GenerateConfigTemplate()
	for _, key := range []string{"#message:", "queue_size:", "webhook:", "smtp_host:"} {
		if !strings.Contains(tpl, key) {
			t.Errorf("模板缺少 message 配置项 %q", key)
		}
	}
}

// TestGenerateConfigTemplate_IsValidYAML 验证模板原文就是合法 yaml，
// 捕获未来维护时引入的语法错误（缩进错乱、tab/space 混用等）。
func TestGenerateConfigTemplate_IsValidYAML(t *testing.T) {
	tpl := GenerateConfigTemplate()
	var c Config
	if err := yaml.Unmarshal([]byte(tpl), &c); err != nil {
		t.Fatalf("template should be valid YAML, unmarshal error: %v", err)
	}
	// config.yaml 不包含 database/storage 节（凭据在 env.yaml 中）
	if c.Database != nil {
		t.Error("config.yaml 模板解析后 Database 必须为 nil（已迁移到 env.yaml）")
	}
}

// TestGenerateEnvTemplate_DefaultIsLocal 验证默认 env.yaml 全注释，
// 解析后不会产生 database 配置（=SQLite 本地模式）。
func TestGenerateEnvTemplate_DefaultIsLocal(t *testing.T) {
	tpl := GenerateEnvTemplate()
	var ef envFile
	if err := yaml.Unmarshal([]byte(tpl), &ef); err != nil {
		t.Fatalf("env template should be valid YAML: %v", err)
	}
	if ef.Database != nil {
		t.Error("默认 env.yaml 模板（全注释）解析后 database 必须为 nil（=SQLite 模式）")
	}
}

// TestGenerateEnvTemplate_NoSQLiteExample 验证 SQLite 不出现在模板的
// driver 示例中：SQLite 是默认零配置模式，模板里不需要 sqlite driver
// 占位（避免误导用户以为需要写 sqlite 才启用本地模式）。
func TestGenerateEnvTemplate_NoSQLiteExample(t *testing.T) {
	tpl := GenerateEnvTemplate()
	if strings.Contains(tpl, "driver: sqlite") {
		t.Error("env.yaml 模板不应包含 'driver: sqlite' 示例：SQLite 是零配置模式，无需在模板中声明")
	}
}

// uncommentDatabaseBlock 把模板中以 marker 开头紧跟的连续 "#database:" /
// "#  ..." 行去掉行首 #，返回取消注释后的文本，方便测试逐个示例块。
func uncommentDatabaseBlock(tpl, marker string) string {
	lines := strings.Split(tpl, "\n")
	var out []string
	uncommenting := false
	hit := false
	for _, line := range lines {
		if !uncommenting {
			if strings.Contains(line, marker) {
				hit = true
				uncommenting = true
				out = append(out, line)
				continue
			}
			out = append(out, line)
			continue
		}
		// 已进入示例块：去掉以 # 开头的 database/缩进字段行
		if strings.HasPrefix(line, "#database:") || strings.HasPrefix(line, "#  ") {
			out = append(out, line[1:])
			continue
		}
		// 遇到第一行不属于该块的内容 → 结束
		uncommenting = false
		out = append(out, line)
	}
	if !hit {
		return ""
	}
	return strings.Join(out, "\n")
}

// TestGenerateEnvTemplate_MySQLExampleParses 验证 MySQL 示例块取消
// 注释后能被解析为 driver=mysql 的合法 DatabaseConfig。
func TestGenerateEnvTemplate_MySQLExampleParses(t *testing.T) {
	tpl := GenerateEnvTemplate()
	uncommented := uncommentDatabaseBlock(tpl, "MySQL")
	if uncommented == "" {
		t.Fatal("模板中找不到 MySQL 示例块的标记")
	}

	var ef envFile
	if err := yaml.Unmarshal([]byte(uncommented), &ef); err != nil {
		t.Fatalf("MySQL 示例块取消注释后应为合法 YAML: %v", err)
	}
	if ef.Database == nil {
		t.Fatal("MySQL 示例块取消注释后 database 节应被解析")
	}
	if ef.Database.Driver != "mysql" {
		t.Errorf("Driver = %q, want mysql", ef.Database.Driver)
	}
	if !strings.Contains(ef.Database.DSN, "@tcp(") {
		t.Errorf("MySQL DSN 应包含 @tcp() 段，实际 = %q", ef.Database.DSN)
	}
	if ef.Database.MaxOpenConns != 20 {
		t.Errorf("MaxOpenConns = %d, want 20", ef.Database.MaxOpenConns)
	}
}

// TestGenerateEnvTemplate_PostgresExampleParses 验证 PostgreSQL 示例块
// 取消注释后能被解析为 driver=postgres 的合法 DatabaseConfig。
func TestGenerateEnvTemplate_PostgresExampleParses(t *testing.T) {
	tpl := GenerateEnvTemplate()
	uncommented := uncommentDatabaseBlock(tpl, "PostgreSQL")
	if uncommented == "" {
		t.Fatal("模板中找不到 PostgreSQL 示例块的标记")
	}

	var ef envFile
	if err := yaml.Unmarshal([]byte(uncommented), &ef); err != nil {
		t.Fatalf("PostgreSQL 示例块取消注释后应为合法 YAML: %v", err)
	}
	if ef.Database == nil {
		t.Fatal("PostgreSQL 示例块取消注释后 database 节应被解析")
	}
	if ef.Database.Driver != "postgres" {
		t.Errorf("Driver = %q, want postgres", ef.Database.Driver)
	}
	if !strings.Contains(ef.Database.DSN, "host=") || !strings.Contains(ef.Database.DSN, "dbname=") {
		t.Errorf("PostgreSQL DSN 应包含 host=/dbname= 段，实际 = %q", ef.Database.DSN)
	}
}
