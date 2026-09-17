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
	rel, err := svc.Scaffold("skill", "code-review", "")
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
	rel, err := svc.Scaffold("mcp", "my-proxy", "")
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
	rel, err := svc.Scaffold("agent", "helper", "")
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
	if _, err := svc.Scaffold("skill", "my-skill", ""); !errors.Is(err, ErrExists) {
		t.Errorf("重名 skill 应 ErrExists, got %v", err)
	}
	for _, bad := range []string{"", "a/b", "..", "a b", ".hidden"} {
		if _, err := svc.Scaffold("skill", bad, ""); !errors.Is(err, ErrInvalid) {
			t.Errorf("Scaffold(skill, %q) 应 ErrInvalid, got %v", bad, err)
		}
	}
	if _, err := svc.Scaffold("plugin", "x", ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("未知类型应 ErrInvalid, got %v", err)
	}
}

// TestScaffold_ExistsMcpAgent 验证 mcp / agent 的重名返回 ErrExists。
func TestScaffold_ExistsMcpAgent(t *testing.T) {
	svc, _ := newServiceForTest(t)
	for _, kind := range []string{"mcp", "agent"} {
		if _, err := svc.Scaffold(kind, "dup", ""); err != nil {
			t.Fatalf("首次 Scaffold(%s): %v", kind, err)
		}
		if _, err := svc.Scaffold(kind, "dup", ""); !errors.Is(err, ErrExists) {
			t.Errorf("重名 %s 应 ErrExists, got %v", kind, err)
		}
	}
}

// TestScaffold_NameLimits 验证名称规则：ASCII 字母开头、长度上限 64；
// 数字/下划线/连字符开头与非 ASCII 字母一律拒绝。
func TestScaffold_NameLimits(t *testing.T) {
	svc, _ := newServiceForTest(t)
	for _, bad := range []string{"中文技能", "1skill", "9", "_lead", "-lead"} {
		if _, err := svc.Scaffold("skill", bad, ""); !errors.Is(err, ErrInvalid) {
			t.Errorf("Scaffold(skill, %q) 应 ErrInvalid, got %v", bad, err)
		}
	}
	for _, good := range []string{"a", "A1", "my_skill-2"} {
		if _, err := svc.Scaffold("skill", good, ""); err != nil {
			t.Errorf("Scaffold(skill, %q) 应成功, got %v", good, err)
		}
	}
	if _, err := svc.Scaffold("skill", strings.Repeat("a", 64), ""); err != nil {
		t.Errorf("64 字符名称应成功: %v", err)
	}
	if _, err := svc.Scaffold("skill", strings.Repeat("a", 65), ""); !errors.Is(err, ErrInvalid) {
		t.Errorf("65 字符名称应 ErrInvalid, got %v", err)
	}
}

// TestScaffold_ForAgent 验证 agent 非空时 skill / mcp 落到 subagents/<agent>/ 下，
// 且与主 Agent 下的同名资源互不冲突。
func TestScaffold_ForAgent(t *testing.T) {
	svc, home := newServiceForTest(t)
	if _, err := svc.Scaffold("agent", "helper", ""); err != nil {
		t.Fatalf("Scaffold(agent): %v", err)
	}

	rel, err := svc.Scaffold("skill", "review", "helper")
	if err != nil {
		t.Fatalf("Scaffold(skill, helper): %v", err)
	}
	if rel != "subagents/helper/skills/review/SKILL.md" {
		t.Errorf("skill rel = %q", rel)
	}
	data, err := os.ReadFile(filepath.Join(home, "subagents/helper/skills/review/SKILL.md"))
	if err != nil {
		t.Fatalf("子 Agent skill 模板未生成: %v", err)
	}
	if !strings.Contains(string(data), `name: "review"`) {
		t.Errorf("frontmatter 缺名称: %s", data)
	}

	rel, err = svc.Scaffold("mcp", "proxy", "helper")
	if err != nil {
		t.Fatalf("Scaffold(mcp, helper): %v", err)
	}
	if rel != "subagents/helper/mcp/proxy.json" {
		t.Errorf("mcp rel = %q", rel)
	}
	data, _ = os.ReadFile(filepath.Join(home, "subagents/helper/mcp/proxy.json"))
	var doc map[string]any
	if err := json.Unmarshal(data, &doc); err != nil || doc["name"] != "proxy" {
		t.Errorf("子 Agent mcp 模板不合法: %v %s", err, data)
	}

	// 子 Agent 内重名 → ErrExists
	if _, err := svc.Scaffold("skill", "review", "helper"); !errors.Is(err, ErrExists) {
		t.Errorf("子 Agent 内重名应 ErrExists, got %v", err)
	}
	// 主 Agent 下同名 skill 不受影响
	if _, err := svc.Scaffold("skill", "review", ""); err != nil {
		t.Errorf("主 Agent 下同名 skill 应可创建, got %v", err)
	}
	// 主 Agent 的 skills/ 与 mcp/ 不应出现子 Agent 的资源
	if _, err := os.Stat(filepath.Join(home, "mcp/proxy.json")); !os.IsNotExist(err) {
		t.Errorf("子 Agent 的 mcp 不应落到主 Agent 目录, err=%v", err)
	}
}

// TestScaffold_ForAgent_Errors 验证 agent 参数的错误路径：
// 子 Agent 不存在 / 不是目录 → ErrNotFound；kind=agent 携带 agent、agent 名非法 → ErrInvalid。
func TestScaffold_ForAgent_Errors(t *testing.T) {
	svc, home := newServiceForTest(t)

	if _, err := svc.Scaffold("skill", "x", "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("子 Agent 不存在应 ErrNotFound, got %v", err)
	}
	if _, err := svc.Scaffold("mcp", "x", "ghost"); !errors.Is(err, ErrNotFound) {
		t.Errorf("子 Agent 不存在应 ErrNotFound(mcp), got %v", err)
	}

	if err := os.MkdirAll(filepath.Join(home, "subagents"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "subagents/plain"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Scaffold("skill", "x", "plain"); !errors.Is(err, ErrNotFound) {
		t.Errorf("subagents/<agent> 是文件时应 ErrNotFound, got %v", err)
	}

	if _, err := svc.Scaffold("agent", "helper", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Scaffold("agent", "nested", "helper"); !errors.Is(err, ErrInvalid) {
		t.Errorf("kind=agent 携带 agent 应 ErrInvalid, got %v", err)
	}
	for _, bad := range []string{"../x", "a/b", ".hidden", "-lead", "with space"} {
		if _, err := svc.Scaffold("skill", "x", bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("agent=%q 应 ErrInvalid, got %v", bad, err)
		}
	}
	// 错误路径不应留下任何产物
	if _, err := os.Stat(filepath.Join(home, "subagents/helper/skills/x")); !os.IsNotExist(err) {
		t.Errorf("错误路径不应创建资源, err=%v", err)
	}
}
