package skill

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewRegistry(t *testing.T) {
	registry := NewRegistry()
	if registry == nil {
		t.Fatal("NewRegistry() 返回 nil")
	}
	if registry.Count() != 0 {
		t.Errorf("NewRegistry() 初始应有 0 个 skills, got %d", registry.Count())
	}
}

func TestRegistry_Register(t *testing.T) {
	registry := NewRegistry()
	skill := &Skill{
		Name:        "test_skill",
		Description: "测试技能",
		Instructions: "测试指令",
	}

	registry.Register(skill)

	if registry.Count() != 1 {
		t.Errorf("Register() 后 Count 应为 1, got %d", registry.Count())
	}

	got, ok := registry.Get("test_skill")
	if !ok {
		t.Error("Register() 后 Get 应找到 skill")
	}
	if got.Name != "test_skill" {
		t.Errorf("Get() 返回错误 skill: got %s, want test_skill", got.Name)
	}
}

func TestRegistry_RegisterMultiple(t *testing.T) {
	registry := NewRegistry()

	for i := 1; i <= 5; i++ {
		registry.Register(&Skill{
			Name:        "skill_" + string(rune('0'+i)),
			Description: "技能" + string(rune('0'+i)),
		})
	}

	if registry.Count() != 5 {
		t.Errorf("RegisterMultiple() Count 应为 5, got %d", registry.Count())
	}
}

func TestRegistry_RegisterOverride(t *testing.T) {
	registry := NewRegistry()

	// 注册同名 skill 应覆盖
	registry.Register(&Skill{Name: "test", Description: "第一次"})
	registry.Register(&Skill{Name: "test", Description: "第二次"})

	if registry.Count() != 1 {
		t.Errorf("RegisterOverride() Count 应为 1, got %d", registry.Count())
	}

	got, _ := registry.Get("test")
	if got.Description != "第二次" {
		t.Errorf("RegisterOverride() 应覆盖: got %s, want 第二次", got.Description)
	}
}

func TestRegistry_Unregister(t *testing.T) {
	registry := NewRegistry()

	registry.Register(&Skill{Name: "test1"})
	registry.Register(&Skill{Name: "test2"})

	registry.Unregister("test1")

	if registry.Count() != 1 {
		t.Errorf("Unregister() Count 应为 1, got %d", registry.Count())
	}

	if _, ok := registry.Get("test1"); ok {
		t.Error("Unregister() 后 Get 应找不到 skill")
	}
}

func TestRegistry_Get(t *testing.T) {
	registry := NewRegistry()

	// 不存在的 skill
	_, ok := registry.Get("nonexistent")
	if ok {
		t.Error("Get() 对不存在的 skill 应返回 false")
	}

	// 存在的 skill
	registry.Register(&Skill{Name: "test"})
	_, ok = registry.Get("test")
	if !ok {
		t.Error("Get() 对存在的 skill 应返回 true")
	}
}

func TestRegistry_List(t *testing.T) {
	registry := NewRegistry()

	registry.Register(&Skill{Name: "skill1", Description: "d1"})
	registry.Register(&Skill{Name: "skill2", Description: "d2"})
	registry.Register(&Skill{Name: "skill3", Description: "d3"})

	list := registry.List()
	if len(list) != 3 {
		t.Errorf("List() 应返回 3 个 skills, got %d", len(list))
	}

	// 验证所有 skill 都在列表中
	names := make(map[string]bool)
	for _, s := range list {
		names[s.Name] = true
	}
	for _, expected := range []string{"skill1", "skill2", "skill3"} {
		if !names[expected] {
			t.Errorf("List() 缺少 skill: %s", expected)
		}
	}
}

func TestRegistry_Count(t *testing.T) {
	registry := NewRegistry()

	// 初始为 0
	if registry.Count() != 0 {
		t.Errorf("Count() 初始应为 0, got %d", registry.Count())
	}

	// 添加后
	registry.Register(&Skill{Name: "test1"})
	registry.Register(&Skill{Name: "test2"})
	if registry.Count() != 2 {
		t.Errorf("Count() 应为 2, got %d", registry.Count())
	}

	// 删除后
	registry.Unregister("test1")
	if registry.Count() != 1 {
		t.Errorf("Count() 应为 1, got %d", registry.Count())
	}
}

func TestNewLoader(t *testing.T) {
	registry := NewRegistry()
	loader := NewLoader(registry)

	if loader == nil {
		t.Fatal("NewLoader() 返回 nil")
	}
}

func TestParseSKILLMd(t *testing.T) {
	content := []byte(`---
name: test_skill
description: 测试技能描述
dependencies:
  - dep1
  - dep2
---

# 测试指令

这是测试指令内容。
`)

	skill, err := parseSKILLMd(content)
	if err != nil {
		t.Fatalf("parseSKILLMd() 失败: %v", err)
	}

	if skill.Name != "test_skill" {
		t.Errorf("parseSKILLMd().Name = %s, want test_skill", skill.Name)
	}

	if skill.Description != "测试技能描述" {
		t.Errorf("parseSKILLMd().Description = %s, want 测试技能描述", skill.Description)
	}

	if len(skill.Dependencies) != 2 {
		t.Errorf("parseSKILLMd().Dependencies 长度 = %d, want 2", len(skill.Dependencies))
	}

	if skill.Instructions == "" {
		t.Error("parseSKILLMd().Instructions 不应为空")
	}
}

func TestParseSKILLMd_MissingFrontmatter(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
	}{
		{
			name:    "无 frontmatter",
			content: []byte("没有 YAML frontmatter"),
		},
		{
			name:    "只有开始标记",
			content: []byte("---\nname: test"),
		},
		{
			name:    "只有结束标记",
			content: []byte("name: test\n---"),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseSKILLMd(tt.content)
			if err == nil {
				t.Error("parseSKILLMd() 应返回错误当缺少 frontmatter")
			}
		})
	}
}

func TestParseSKILLMd_MissingRequiredFields(t *testing.T) {
	tests := []struct {
		name    string
		content []byte
	}{
		{
			name: "缺少 name",
			content: []byte(`---
description: 测试描述
---
内容`),
		},
		{
			name: "缺少 description",
			content: []byte(`---
name: test
---
内容`),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := parseSKILLMd(tt.content)
			if err == nil {
				t.Errorf("parseSKILLMd() 应返回错误当 %s", tt.name)
			}
		})
	}
}

func TestLoader_Load(t *testing.T) {
	// 创建临时目录和 SKILL.md
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test_skill")
	os.MkdirAll(skillDir, 0755)

	skillContent := `---
name: test_skill
description: 测试技能描述
---

# 测试指令

测试指令内容。
`
	skillPath := filepath.Join(skillDir, "SKILL.md")
	os.WriteFile(skillPath, []byte(skillContent), 0644)

	registry := NewRegistry()
	loader := NewLoader(registry)

	err := loader.Load(skillPath)
	if err != nil {
		t.Fatalf("Load() 失败: %v", err)
	}

	if registry.Count() != 1 {
		t.Errorf("Load() 后 Count 应为 1, got %d", registry.Count())
	}

	skill, ok := registry.Get("test_skill")
	if !ok {
		t.Fatal("Load() 后 Get 找不到 skill")
	}

	if skill.FilePath != skillPath {
		t.Errorf("Load().FilePath = %s, want %s", skill.FilePath, skillPath)
	}
}

func TestLoader_LoadAll(t *testing.T) {
	// 创建临时目录和多个 skill
	tmpDir := t.TempDir()

	// 创建 skill1
	skill1Dir := filepath.Join(tmpDir, "skill1")
	os.MkdirAll(skill1Dir, 0755)
	os.WriteFile(filepath.Join(skill1Dir, "SKILL.md"), []byte(`---
name: skill1
description: 技能1
---
内容1`), 0644)

	// 创建 skill2
	skill2Dir := filepath.Join(tmpDir, "skill2")
	os.MkdirAll(skill2Dir, 0755)
	os.WriteFile(filepath.Join(skill2Dir, "SKILL.md"), []byte(`---
name: skill2
description: 技能2
---
内容2`), 0644)

	// 创建空目录（无 SKILL.md）
	os.MkdirAll(filepath.Join(tmpDir, "empty_dir"), 0755)

	registry := NewRegistry()
	loader := NewLoader(registry)

	err := loader.LoadAll(tmpDir)
	if err != nil {
		t.Fatalf("LoadAll() 失败: %v", err)
	}

	if registry.Count() != 2 {
		t.Errorf("LoadAll() Count 应为 2, got %d", registry.Count())
	}
}

func TestLoader_LoadAll_NonexistentDir(t *testing.T) {
	registry := NewRegistry()
	loader := NewLoader(registry)

	// 不存在的目录不应报错
	err := loader.LoadAll("/nonexistent/path")
	if err != nil {
		t.Errorf("LoadAll() 对不存在的目录不应报错: %v", err)
	}
}

func TestLoader_Unload(t *testing.T) {
	registry := NewRegistry()
	loader := NewLoader(registry)

	// 先加载一个 skill
	tmpDir := t.TempDir()
	skillDir := filepath.Join(tmpDir, "test_skill")
	os.MkdirAll(skillDir, 0755)
	skillPath := filepath.Join(skillDir, "SKILL.md")
	os.WriteFile(skillPath, []byte(`---
name: test_skill
description: 测试
---
内容`), 0644)

	loader.Load(skillPath)

	if registry.Count() != 1 {
		t.Fatalf("Load() 后 Count 应为 1, got %d", registry.Count())
	}

	// Unload
	loader.Unload(skillPath)

	if registry.Count() != 0 {
		t.Errorf("Unload() 后 Count 应为 0, got %d", registry.Count())
	}
}