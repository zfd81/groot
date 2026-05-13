package chat

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
)

// InputModel wraps bubbles textarea with ghost-text completion support.
type InputModel struct {
	textarea         textarea.Model
	ghostText        string // the remainder text shown after cursor (gray)
	completionPrefix string // what the user has typed so far
}

// NewInput creates an input component with default settings.
func NewInput() InputModel {
	ta := textarea.New()
	ta.Placeholder = "输入消息，或 / 开头使用命令..."
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.MaxHeight = 10
	ta.SetHeight(3)
	ta.Prompt = ""

	ta.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("alt+enter", "shift+enter"),
		key.WithHelp("Alt+Enter/Shift+Enter", "换行"),
	)

		ta.SetVirtualCursor(false)
	ta.Focus()

	return InputModel{textarea: ta}
}

// SetSize adjusts input width and height.
func (i *InputModel) SetSize(width, height int) {
	i.textarea.SetWidth(width - 4) // account for border padding
	if height > 0 {
		i.textarea.SetHeight(height)
	}
}

// SetGhostText sets the inline ghost completion suffix and tracking prefix.
// `full` is the full completion name; the input prefix is derived from input value.
func (i *InputModel) SetGhostText(full string) {
	prefix := i.textarea.Value()

	// full 以 prefix 开头：ghost 是剩余部分
	if strings.HasPrefix(full, prefix) {
		i.completionPrefix = prefix
		i.ghostText = full[len(prefix):]
		return
	}

	// prefix 末尾与 full 开头有重叠（如 prefix="/session l", full="list "）
	// 找到前缀末尾与 full 开头的最大匹配，ghost 只补未重叠的部分
	for j := len(prefix) - 1; j >= 0; j-- {
		suffix := prefix[j:]
		if strings.HasPrefix(full, suffix) {
			i.completionPrefix = prefix
			i.ghostText = full[len(suffix):]
			return
		}
	}

	i.completionPrefix = prefix
	i.ghostText = full
}

// ClearGhostText removes ghost text.
func (i *InputModel) ClearGhostText() {
	i.ghostText = ""
	i.completionPrefix = ""
}

// HasGhostText reports whether ghost text is active.
func (i *InputModel) HasGhostText() bool { return i.ghostText != "" }

// Value returns the current textarea content.
func (i *InputModel) Value() string { return i.textarea.Value() }

// InsertNewline inserts a literal newline at current cursor.
func (i *InputModel) InsertNewline() {
	val := i.textarea.Value()
	i.textarea.SetValue(val + "\n")
	i.textarea.CursorEnd()
}

// Reset clears both textarea and ghost text.
func (i *InputModel) Reset() {
	i.textarea.Reset()
	i.ClearGhostText()
}

// AcceptGhostText replaces the completion prefix with the full completion value.
func (i *InputModel) AcceptGhostText() {
	if i.ghostText == "" {
		return
	}
	full := i.completionPrefix + i.ghostText
	i.textarea.SetValue(full)
	i.textarea.CursorEnd()
	i.ClearGhostText()
}

// Update delegates to the underlying textarea.
func (i InputModel) Update(msg tea.Msg) (InputModel, tea.Cmd) {
	var cmd tea.Cmd
	i.textarea, cmd = i.textarea.Update(msg)
	return i, cmd
}

// View renders the input area with double-line border and ghost text appended.
func (i InputModel) View(width int) string {
	content := i.textarea.View()
	if i.ghostText != "" {
		content += GhostTextStyle.Render(i.ghostText)
	}

	// Render with double-line border
	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color("#98c379")).
		Width(width - 2).
		Padding(0, 1)

	return borderStyle.Render(content)
}
