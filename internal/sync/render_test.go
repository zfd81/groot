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

func TestRenderDiff_Pull(t *testing.T) {
	d := DiffResult{
		Added: []string{"GROOT.md"},
	}
	var buf bytes.Buffer
	RenderDiff(&buf, d, "pull")
	out := buf.String()
	if !strings.Contains(out, "MinIO → HOME") {
		t.Errorf("expected 'MinIO → HOME' in pull output:\n%s", out)
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
