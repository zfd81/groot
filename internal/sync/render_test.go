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
		"HOME → MinIO",
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
		Added:    []string{"local-only.md"},      // pull 后将被删
		Modified: []string{"both-modified.md"},   // pull 后被远端版本覆盖
		Removed:  []string{"remote-only.md"},     // pull 后会到本地
	}
	var buf bytes.Buffer
	RenderDiff(&buf, d, "pull")
	out := buf.String()

	for _, want := range []string{
		"Changes to pull",
		"MinIO → HOME",
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
		"HOME ↔ MinIO",
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

func TestRenderDiff_Empty(t *testing.T) {
	d := DiffResult{}
	var buf bytes.Buffer
	RenderDiff(&buf, d, "push")
	out := buf.String()
	if !strings.Contains(out, "No differences") {
		t.Errorf("expected 'No differences' for empty diff:\n%s", out)
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
