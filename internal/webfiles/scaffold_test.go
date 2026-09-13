package webfiles

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScaffold_Skill 验证生成 skills/<名>/SKILL.md 且 frontmatter 含名称。
func TestScaffold_Skill(t *testing.T) {
	svc, home := newServiceForTest(t)
	rel, err := svc.Scaffold("skill", "code-review")
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	if rel != "skills/code-review/SKILL.md" {
		t.Errorf("rel = %q", rel)
	}
	data, err := os.ReadFile(filepath.Join(home, "skills/code-review/SKILL.md"))
	if err != nil {
		t.Fatalf("模板文件未生成: %v", err)
	}
	if !strings.Contains(string(data), `name: "code-review"`) {
		t.Errorf("frontmatter 缺名称: %s", data)
	}
}

// TestScaffold_Mcp 验证生成 mcp/<名>.json 且为合法 JSON 骨架。
func TestScaffold_Mcp(t *testing.T) {
	svc, home := newServiceForTest(t)
	rel, err := svc.Scaffold("mcp", "my-proxy")
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	if rel != "mcp/my-proxy.json" {
		t.Errorf("rel = %q", rel)
	}
	data, _ := os.ReadFile(filepath.Join(home, "mcp/my-proxy.json"))
	for _, want := range []string{`"name": "my-proxy"`, `"type": "stdio"`, `"isActive": false`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("模板缺少 %s: %s", want, data)
		}
	}
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("模板不是合法 JSON: %v", err)
	}
	if doc["name"] != "my-proxy" {
		t.Errorf("JSON name 字段 = %v", doc["name"])
	}
}

// TestScaffold_Agent 验证生成 subagents/<名>/{agent.md, mcp/, skills/}。
func TestScaffold_Agent(t *testing.T) {
	svc, home := newServiceForTest(t)
	rel, err := svc.Scaffold("agent", "helper")
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	if rel != "subagents/helper/agent.md" {
		t.Errorf("rel = %q", rel)
	}
	for _, p := range []string{"subagents/helper/agent.md", "subagents/helper/mcp", "subagents/helper/skills"} {
		if _, err := os.Stat(filepath.Join(home, p)); err != nil {
			t.Errorf("%s 未生成: %v", p, err)
		}
	}
}

// TestScaffold_Invalid 验证重名、非法名称、未知类型。
func TestScaffold_Invalid(t *testing.T) {
	svc, _ := newServiceForTest(t)
	if _, err := svc.Scaffold("skill", "my-skill"); !errors.Is(err, ErrExists) {
		t.Errorf("重名 skill 应 ErrExists, got %v", err)
	}
	for _, bad := range []string{"", "a/b", "..", "a b", ".hidden"} {
		if _, err := svc.Scaffold("skill", bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("Scaffold(skill, %q) 应 ErrInvalid, got %v", bad, err)
		}
	}
	if _, err := svc.Scaffold("plugin", "x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("未知类型应 ErrInvalid, got %v", err)
	}
}

// TestScaffold_ExistsMcpAgent 验证 mcp / agent 的重名返回 ErrExists。
func TestScaffold_ExistsMcpAgent(t *testing.T) {
	svc, _ := newServiceForTest(t)
	for _, kind := range []string{"mcp", "agent"} {
		if _, err := svc.Scaffold(kind, "dup"); err != nil {
			t.Fatalf("首次 Scaffold(%s): %v", kind, err)
		}
		if _, err := svc.Scaffold(kind, "dup"); !errors.Is(err, ErrExists) {
			t.Errorf("重名 %s 应 ErrExists, got %v", kind, err)
		}
	}
}

// TestScaffold_NameLimits 验证 Unicode 名称与 64 字符长度上限。
func TestScaffold_NameLimits(t *testing.T) {
	svc, home := newServiceForTest(t)
	if _, err := svc.Scaffold("skill", "中文技能"); err != nil {
		t.Errorf("Unicode 名称应成功: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "skills/中文技能/SKILL.md")); err != nil {
		t.Errorf("Unicode skill 未生成: %v", err)
	}
	if _, err := svc.Scaffold("skill", strings.Repeat("a", 64)); err != nil {
		t.Errorf("64 字符名称应成功: %v", err)
	}
	if _, err := svc.Scaffold("skill", strings.Repeat("a", 65)); !errors.Is(err, ErrInvalid) {
		t.Errorf("65 字符名称应 ErrInvalid, got %v", err)
	}
}
