package sync

import (
	"bytes"
	"strings"
	"testing"
)

func TestRenderDiff_Push(t *testing.T) {
	d := DiffResult{
		Added:    []string{"skills/weather/SKILL.md"},
		Modified: []string{"config.yaml"},
		Removed:  []string{"skills/old/SKILL.md"},
	}
	var buf bytes.Buffer
	RenderDiff(&buf, d, "push")
	out := buf.String()

	for _, want := range []string{
		"Changes to push",
		"HOME → DB",
		"Added:",
		"skills/weather/SKILL.md",
		"Modified:",
		"config.yaml",
		"Removed:",
		"skills/old/SKILL.md",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n\nOutput:\n%s", want, out)
		}
	}
}

// TestRenderDiff_Pull 验证 pull 渲染按"操作 to be performed on local"语义打标签:
//   - DiffResult.Added(本地有/远端没有) → 渲染为 "Removed locally"(因为 pull 会删本地这些文件)
//   - DiffResult.Modified                → "Modified locally (overwritten by remote)"
//   - DiffResult.Removed(远端有/本地没有) → "Added locally"(从远端拉到本地)
//
// 这是修正自系统测试中发现的语义颠倒 bug:之前 pull 时也按 push 视角打 "Added/Modified/Removed",
// 但 pull 实际操作的方向相反,会让用户严重误解。
func TestRenderDiff_Pull_Labels(t *testing.T) {
	d := DiffResult{
		Added:    []string{"local-only.md"},    // pull 后将被删
		Modified: []string{"both-modified.md"}, // pull 后被远端版本覆盖
		Removed:  []string{"remote-only.md"},   // pull 后会到本地
	}
	var buf bytes.Buffer
	RenderDiff(&buf, d, "pull")
	out := buf.String()

	for _, want := range []string{
		"Changes to pull",
		"DB → HOME",
		"Removed locally:",
		"local-only.md",
		"Modified locally",
		"both-modified.md",
		"Added locally:",
		"remote-only.md",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n\nOutput:\n%s", want, out)
		}
	}
}

// TestRenderDiff_Diff 验证 diff 命令使用中性措辞,不暗示任何操作方向。
func TestRenderDiff_Diff_Neutral(t *testing.T) {
	d := DiffResult{
		Added:    []string{"only-local.md"},
		Modified: []string{"both-differ.md"},
		Removed:  []string{"only-remote.md"},
	}
	var buf bytes.Buffer
	RenderDiff(&buf, d, "diff")
	out := buf.String()

	for _, want := range []string{
		"Differences",
		"HOME ↔ DB",
		"Local only:",
		"only-local.md",
		"Modified",
		"both-differ.md",
		"Remote only:",
		"only-remote.md",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\n\nOutput:\n%s", want, out)
		}
	}
	// diff 命令不应出现 "push" / "pull" 暗示
	if strings.Contains(out, "Changes to push") || strings.Contains(out, "Changes to pull") {
		t.Errorf("diff output should not imply direction:\n%s", out)
	}
}

// TestRenderDiff_Diff_AnnotatesRemoteDeleted 验证 diff 方向下,Added 里那些
// 远端存在删除记录的路径被标注出来:它们是被他人删除的,不是本地新建的。
// 本地新建的路径不得带标注。
func TestRenderDiff_Diff_AnnotatesRemoteDeleted(t *testing.T) {
	d := DiffResult{
		Added: []string{"skills/weather/SKILL.md", "subagents/db-agent/agent.md"},
		Remote: map[string]RemoteMeta{
			"skills/weather/SKILL.md": {Deleted: true},
		},
	}
	var buf bytes.Buffer
	RenderDiff(&buf, d, "diff")
	out := buf.String()

	if !strings.Contains(out, "skills/weather/SKILL.md  (deleted on remote)") {
		t.Errorf("tombstoned path should be annotated:\n%s", out)
	}
	if strings.Contains(out, "subagents/db-agent/agent.md  (deleted on remote)") {
		t.Errorf("locally-created path must not be annotated:\n%s", out)
	}
	if !strings.Contains(out, "subagents/db-agent/agent.md") {
		t.Errorf("locally-created path missing from output:\n%s", out)
	}
}

func TestRenderDiff_Empty(t *testing.T) {
	d := DiffResult{}
	var buf bytes.Buffer
	RenderDiff(&buf, d, "push")
	out := buf.String()
	if !strings.Contains(out, "No differences") {
		t.Errorf("expected 'No differences' for empty diff:\n%s", out)
	}
}

// TestNeedsRestart 覆盖 needsRestartPaths 三个前缀的正例、精确匹配分支和反例。
func TestNeedsRestart(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		// 三个前缀各自的正例
		{"config.yaml", true},
		{"mcp/db/config.json", true},
		{"subagents/x/agent.md", true},

		// 精确匹配分支:不带斜杠、不带后续内容,走 strings.TrimSuffix 那条
		{"mcp", true},
		{"subagents", true},

		// 反例
		{"skills/w/SKILL.md", false},
		{"mcpfoo/bar.json", false},
		{"subagentsfoo/a.md", false},
		{"a/config.yaml", false},

		// config.yaml 是唯一不带斜杠的前缀,所以 HasPrefix 会让 config.yaml.bak 命中。
		// 这是宽松前缀匹配的已知副作用,但在 sync 里不可达:ValidateSyncPath 拒绝
		// "config.yaml.bak" 进入 sync,且全量遍历时它不在任何白名单根之下。
		// 此处断言现状仅为锁定行为——将来若要收紧匹配,应能看到这条用例和它的理由。
		{"config.yaml.bak", true},
	}

	for _, tt := range tests {
		if got := needsRestart(tt.path); got != tt.want {
			t.Errorf("needsRestart(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestRenderDiff_NeedsRestart(t *testing.T) {
	d := DiffResult{
		Modified: []string{"config.yaml"},
	}
	var buf bytes.Buffer
	RenderDiff(&buf, d, "pull")
	out := buf.String()
	if !strings.Contains(out, "restart") && !strings.Contains(out, "重启") {
		t.Errorf("expected restart notice for config.yaml in pull output:\n%s", out)
	}
}
