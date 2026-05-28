package agent

import (
	"os"
	"path/filepath"
	"testing"
)

// writeTempAgentMd 在临时目录写一个 agent.md，返回文件路径。
func writeTempAgentMd(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.md")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestParseAgentMd_Valid 测试合法 frontmatter + 正文的解析。
func TestParseAgentMd_Valid(t *testing.T) {
	p := writeTempAgentMd(t, `---
description: 数据库查询专家
model: gpt-4
temperature: 0.3
max_tokens: 2048
---

# 数据库查询 Agent
正文内容
`)
	md, err := parseAgentMd(p)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if md.Description != "数据库查询专家" {
		t.Errorf("description mismatch: %q", md.Description)
	}
	if md.Model != "gpt-4" {
		t.Errorf("model mismatch: %q", md.Model)
	}
	if md.Temperature == nil || *md.Temperature != 0.3 {
		t.Errorf("temperature mismatch: %v", md.Temperature)
	}
	if md.MaxTokens == nil || *md.MaxTokens != 2048 {
		t.Errorf("max_tokens mismatch: %v", md.MaxTokens)
	}
	if md.Content == "" {
		t.Error("content should not be empty")
	}
	if !contains(md.Content, "正文内容") {
		t.Errorf("content missing body: %q", md.Content)
	}
}

// TestParseAgentMd_MissingDescription 缺少 description 字段必须报错。
func TestParseAgentMd_MissingDescription(t *testing.T) {
	p := writeTempAgentMd(t, `---
model: gpt-4
---
body
`)
	_, err := parseAgentMd(p)
	if err == nil {
		t.Fatal("expected error for missing description")
	}
}

// TestParseAgentMd_EmptyDescription description 为空字符串必须报错。
func TestParseAgentMd_EmptyDescription(t *testing.T) {
	p := writeTempAgentMd(t, `---
description: ""
---
body
`)
	_, err := parseAgentMd(p)
	if err == nil {
		t.Fatal("expected error for empty description")
	}
}

// TestParseAgentMd_NoFrontmatter 没有 frontmatter 必须报错。
func TestParseAgentMd_NoFrontmatter(t *testing.T) {
	p := writeTempAgentMd(t, "just plain markdown body\n")
	_, err := parseAgentMd(p)
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}

// TestParseAgentMd_FileNotExist 文件不存在必须报错。
func TestParseAgentMd_FileNotExist(t *testing.T) {
	_, err := parseAgentMd("/nonexistent/agent.md")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

// contains 简单的子串包含检查，递归实现。
func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || (len(s) > len(sub) && (s[:len(sub)] == sub || contains(s[1:], sub))))
}
