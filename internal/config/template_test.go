package config

import (
	"strings"
	"testing"
)

func TestGenerateConfigTemplate(t *testing.T) {
	content := GenerateConfigTemplate()

	// 检查必要字段存在
	if !strings.Contains(content, "llm:") {
		t.Error("模板缺少 llm 配置")
	}
	if !strings.Contains(content, "default_model:") {
		t.Error("模板缺少 default_model 配置")
	}
	if !strings.Contains(content, "api_key:") {
		t.Error("模板缺少 api_key 配置")
	}
	if !strings.Contains(content, "${OPENAI_API_KEY}") {
		t.Error("模板缺少环境变量引用示例")
	}
	if !strings.Contains(content, "#") {
		t.Error("模板缺少注释说明")
	}
}

func TestGenerateConfigTemplate_HasStorageBlock(t *testing.T) {
	tpl := GenerateConfigTemplate()
	if !strings.Contains(tpl, "# 存储抽象层配置") {
		t.Error("missing storage section header")
	}
	if !strings.Contains(tpl, "storage:") {
		t.Error("missing storage: key")
	}
	if !strings.Contains(tpl, "#   minio:") {
		t.Error("missing commented minio block")
	}
	if !strings.Contains(tpl, "${MINIO_ACCESS_KEY}") {
		t.Error("missing minio access_key env placeholder")
	}
}
