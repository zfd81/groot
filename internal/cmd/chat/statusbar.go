package chat

import (
	"fmt"

	"charm.land/lipgloss/v2"
)

// StatusBar holds and renders the bottom status bar state.
type StatusBar struct {
	ModelName string
	SessionID string
	Round     int
	Width     int
}

// NewStatusBar creates a pre-filled status bar.
func NewStatusBar(modelName string) StatusBar {
	return StatusBar{
		ModelName: modelName,
		SessionID: "新会话",
		Round:     0,
	}
}

// View renders the status bar as a single line: left | center | right.
func (s StatusBar) View() string {
	left := fmt.Sprintf("模型: %s", s.ModelName)
	mid := fmt.Sprintf("会话: %s", s.SessionID)
	right := fmt.Sprintf("对话: 第 %d 轮", s.Round)

	lw := lipgloss.Width(left)
	rw := lipgloss.Width(right)

	// Calculate remaining space for middle section
	midSpace := s.Width - lw - rw
	if midSpace < 4 {
		midSpace = 4
	}

	midStyle := lipgloss.NewStyle().Width(midSpace).Align(lipgloss.Center)
	content := lipgloss.JoinHorizontal(lipgloss.Top, left, midStyle.Render(mid), right)

	return StatusBarStyle.Width(s.Width).Render(content)
}

// visibleWidth counts visible runes, skipping ANSI escape sequences.
func visibleWidth(s string) int {
	n := 0
	inEscape := false
	for _, r := range s {
		if r == '\x1b' {
			inEscape = true
			continue
		}
		if inEscape {
			if r == 'm' {
				inEscape = false
			}
			continue
		}
		n++
	}
	return n
}
