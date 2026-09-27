package sync

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveLocalPaths_Category(t *testing.T) {
	homeDir := t.TempDir()
	// 准备两个 skill 目录
	os.MkdirAll(filepath.Join(homeDir, "skills", "weather"), 0755)
	os.MkdirAll(filepath.Join(homeDir, "skills", "translator"), 0755)

	paths, err := ResolveLocalPaths(homeDir, []string{"skills"})
	if err != nil {
		t.Fatalf("ResolveLocalPaths: %v", err)
	}
	if len(paths) != 2 {
		t.Errorf("expected 2 skill paths, got %d: %v", len(paths), paths)
	}
}

func TestResolveLocalPaths_SpecificSkill(t *testing.T) {
	homeDir := t.TempDir()
	os.MkdirAll(filepath.Join(homeDir, "skills", "weather"), 0755)

	paths, err := ResolveLocalPaths(homeDir, []string{"skills/weather"})
	if err != nil {
		t.Fatalf("ResolveLocalPaths: %v", err)
	}
	if len(paths) != 1 || paths[0] != "skills/weather" {
		t.Errorf("expected [skills/weather], got %v", paths)
	}
}

func TestResolveLocalPaths_FileResource(t *testing.T) {
	homeDir := t.TempDir()
	os.WriteFile(filepath.Join(homeDir, "GROOT.md"), []byte("# GROOT\n"), 0644)

	paths, err := ResolveLocalPaths(homeDir, []string{"GROOT.md"})
	if err != nil {
		t.Fatalf("ResolveLocalPaths: %v", err)
	}
	if len(paths) != 1 || paths[0] != "GROOT.md" {
		t.Errorf("expected [GROOT.md], got %v", paths)
	}
}

func TestResolveLocalPaths_DefaultAll(t *testing.T) {
	homeDir := t.TempDir()
	// 只创建部分资源
	os.MkdirAll(filepath.Join(homeDir, "skills", "w"), 0755)
	os.WriteFile(filepath.Join(homeDir, "GROOT.md"), []byte(""), 0644)

	paths, err := ResolveLocalPaths(homeDir, nil) // nil = all
	if err != nil {
		t.Fatalf("ResolveLocalPaths: %v", err)
	}
	// 应当包含 GROOT.md 和 skills/w,不应包含不存在的 mcp / subagents
	found := map[string]bool{}
	for _, p := range paths {
		found[p] = true
	}
	if !found["GROOT.md"] {
		t.Error("expected GROOT.md in default resolve")
	}
	if !found["skills/w"] {
		t.Error("expected skills/w in default resolve")
	}
	// 不存在的类别不应出现
	if found["mcp"] {
		t.Error("mcp dir does not exist, should not appear")
	}
}

func TestResolveLocalPaths_InvalidPath(t *testing.T) {
	homeDir := t.TempDir()
	_, err := ResolveLocalPaths(homeDir, []string{"env.yaml"})
	if err == nil {
		t.Error("expected error for env.yaml, got nil")
	}
}

func TestResolveLocalPaths_SkillFileDirect(t *testing.T) {
	homeDir := t.TempDir()
	_, err := ResolveLocalPaths(homeDir, []string{"skills/weather/SKILL.md"})
	if err == nil {
		t.Error("expected error for direct skill file path")
	}
}
