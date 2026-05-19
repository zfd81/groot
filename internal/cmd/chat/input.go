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
	baseHeight       int    // desired textarea height (without ghost text)
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

	return InputModel{textarea: ta, baseHeight: 3}
}

// SetSize adjusts input width and height.
func (i *InputModel) SetSize(width, height int) {
	i.textarea.SetWidth(width - 2) // account for double border
	if height > 0 {
		i.baseHeight = height
		if i.ghostText != "" && height > 1 {
			i.textarea.SetHeight(height - 1)
		} else {
			i.textarea.SetHeight(height)
		}
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
		i.shrinkForGhost()
		return
	}

	// prefix 末尾与 full 开头有重叠（如 prefix="/session l", full="list "）
	// 找到前缀末尾与 full 开头的最大匹配，ghost 只补未重叠的部分
	// 大小写不敏感匹配，因为用户输入可能与模型名大小写不同
	for j := len(prefix) - 1; j >= 0; j-- {
		suffix := prefix[j:]
		if strings.HasPrefix(strings.ToLower(full), strings.ToLower(suffix)) {
			i.completionPrefix = prefix
			i.ghostText = full[len(suffix):]
			i.shrinkForGhost()
			return
		}
	}

	i.completionPrefix = prefix
	i.ghostText = full
	i.shrinkForGhost()
}

func (i *InputModel) shrinkForGhost() {
	if i.baseHeight > 1 {
		i.textarea.SetHeight(i.baseHeight - 1)
	}
}

// ClearGhostText removes ghost text.
func (i *InputModel) ClearGhostText() {
	i.ghostText = ""
	i.completionPrefix = ""
	i.textarea.SetHeight(i.baseHeight)
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

// View renders the input area with double-line border.
// Ghost text occupies the bottom line inside the border; textarea height
// is reduced by 1 to keep the total border height constant.
func (i InputModel) View(width int) string {
	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.DoubleBorder()).
		BorderForeground(lipgloss.Color("#98c379")).
		Width(width)

	if i.ghostText != "" {
		inner := i.textarea.View() + "\n" + GhostTextStyle.Render(i.ghostText)
		return borderStyle.Render(inner)
	}
	return borderStyle.Render(i.textarea.View())
}