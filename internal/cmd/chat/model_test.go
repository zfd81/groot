package chat

import (
	"strings"
	"testing"
)

func TestStatusBarView(t *testing.T) {
	sb := NewStatusBar("gpt-4o")
	sb.Width = 80
	view := sb.View()
	if view == "" {
		t.Error("StatusBar.View() returned empty string")
	}
}

func TestCompletionFilter(t *testing.T) {
	cm := NewCompletion()
	cm.Show(SystemCommands)

	cm.Filter("/mod")
	if !cm.IsVisible() {
		t.Error("Expected visible after filtering '/mod'")
	}
	if len(cm.filtered) == 0 {
		t.Error("Expected filtered items for '/mod'")
	}
	if cm.filtered[0].Name != "/model" {
		t.Errorf("Expected first match '/model', got %q", cm.filtered[0].Name)
	}
}

func TestCompletionHide(t *testing.T) {
	cm := NewCompletion()
	cm.Show(SystemCommands)
	cm.Hide()
	if cm.IsVisible() {
		t.Error("Expected hidden after Hide()")
	}
}

func TestCompletionSelectWrap(t *testing.T) {
	cm := NewCompletion()
	cm.Show([]CompletionItem{
		{Name: "a"}, {Name: "b"}, {Name: "c"},
	})

	cm.SelectNext()
	if cm.selected != 1 {
		t.Errorf("After SelectNext, selected = %d, want 1", cm.selected)
	}
	cm.SelectNext()
	cm.SelectNext()
	if cm.selected != 0 {
		t.Errorf("After wrapping, selected = %d, want 0", cm.selected)
	}

	cm.SelectPrev()
	if cm.selected != 2 {
		t.Errorf("After SelectPrev wrap, selected = %d, want 2", cm.selected)
	}
}

func TestCompletionFilterNoMatch(t *testing.T) {
	cm := NewCompletion()
	cm.Show(SystemCommands)
	cm.Filter("/zzz_no_match")
	if cm.IsVisible() {
		t.Error("Expected hidden when no matches")
	}
}

func TestVisibleWidth(t *testing.T) {
	tests := []struct {
		s    string
		want int
	}{
		{"hello", 5},
		{"你好", 2},
	}
	for _, tt := range tests {
		got := visibleWidth(tt.s)
		if got != tt.want {
			t.Errorf("visibleWidth(%q) = %d, want %d", tt.s, got, tt.want)
		}
	}
}

func TestNormalizeMarkdown(t *testing.T) {
	input := "  • 查看：列出目录内容\n    • 注：自动映射"
	output := normalizeMarkdown(input)
	if strings.Contains(output, "•") {
		t.Error("normalizeMarkdown failed to convert Unicode bullets")
	}
	if !strings.Contains(output, "  - 查看") {
		t.Error("normalizeMarkdown did not produce '- ' for indented bullet")
	}
	if !strings.Contains(output, "    - 注") {
		t.Error("normalizeMarkdown did not produce '- ' for sub-bullet")
	}
}

func TestNormalizeMarkdownVariants(t *testing.T) {
	bullets := []string{"•", "◦", "▪", "●", "○", "·", "∙", "⋅", "‣", "▸"}
	for _, b := range bullets {
		input := b + " 测试内容"
		output := normalizeMarkdown(input)
		if strings.Contains(output, b) {
			t.Errorf("normalizeMarkdown did not convert %q", b)
		}
		if !strings.HasPrefix(strings.TrimSpace(output), "- ") {
			t.Errorf("normalizeMarkdown result for %q does not start with '- '", b)
		}
	}
}

func TestPreserveLineBreaksWithHeading(t *testing.T) {
	input := "text\n\n  ### Heading\n\nmore text"
	output := preserveLineBreaks(input)
	if strings.Contains(output, "### Heading  \n") {
		t.Error("preserveLineBreaks added hard break after heading")
	}
}

func TestPreserveLineBreaksWithList(t *testing.T) {
	input := "text\n\n  - item 1\n  - item 2\n    - sub item"
	output := preserveLineBreaks(input)
	if strings.Contains(output, "  \n  -") {
		t.Error("preserveLineBreaks added hard break between list items")
	}
}

func TestIsHeading(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"# H1", true},
		{"## H2", true},
		{"  ### H3", true},
		{"   #### H4", true},
		{"no heading", false},
		{"  plain text", false},
	}
	for _, tt := range tests {
		got := isHeading(tt.line)
		if got != tt.want {
			t.Errorf("isHeading(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}

func TestIsOrderedList(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"1. first", true},
		{"2) second", true},
		{"  3. indented", true},
		{"123. long number", true},
		{"no number", false},
		{"1.no space", false},
	}
	for _, tt := range tests {
		got := isOrderedList(tt.line)
		if got != tt.want {
			t.Errorf("isOrderedList(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}

func TestIsHorizontalRule(t *testing.T) {
	tests := []struct {
		line string
		want bool
	}{
		{"---", true},
		{"***", true},
		{"___", true},
		{"- - -", true},
		{"not a rule", false},
		{"--", false},
	}
	for _, tt := range tests {
		got := isHorizontalRule(tt.line)
		if got != tt.want {
			t.Errorf("isHorizontalRule(%q) = %v, want %v", tt.line, got, tt.want)
		}
	}
}

func TestBreakLongLines(t *testing.T) {
	// Short line should not be broken
	input := "short line"
	output := breakLongLines(input, 80)
	if output != input {
		t.Errorf("Short line changed: %q", output)
	}

	// Long CJK line should be broken
	longCJK := strings.Repeat("中文", 50) // 100 CJK characters
	output = breakLongLines(longCJK, 40)
	if len(strings.Split(output, "\n")) < 2 {
		t.Error("Long CJK line was not broken")
	}
}
