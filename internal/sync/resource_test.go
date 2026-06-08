package sync

import (
	"testing"
)

func TestSyncableRoots(t *testing.T) {
	roots := SyncableResourceRoots
	if len(roots) != 5 {
		t.Fatalf("expected 5 roots, got %d", len(roots))
	}
	for _, r := range []string{"config.yaml", "skills", "subagents", "mcp", "GROOT.md"} {
		found := false
		for _, root := range roots {
			if root == r {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("missing root: %s", r)
		}
	}
}

func TestValidateSyncPath_WhitelistRoot(t *testing.T) {
	if err := ValidateSyncPath("config.yaml"); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if err := ValidateSyncPath("skills"); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if err := ValidateSyncPath("skills/weather"); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
	if err := ValidateSyncPath("subagents/db-agent/agent.md"); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestValidateSyncPath_Rejected(t *testing.T) {
	cases := []string{"env.yaml", "logs", "memory", "cluster", "../etc/passwd", ""}
	for _, p := range cases {
		if err := ValidateSyncPath(p); err == nil {
			t.Errorf("expected error for %q, got nil", p)
		}
	}
}

func TestValidateSyncPath_SkillFileDirect(t *testing.T) {
	// 禁止直接操作 skill 目录下的单个文件
	if err := ValidateSyncPath("skills/weather/SKILL.md"); err == nil {
		t.Error("expected error for direct skill file path")
	}
	if err := ValidateSyncPath("subagents/db-agent/skills/sql/SKILL.md"); err == nil {
		t.Error("expected error for direct subagent skill file path")
	}
}
