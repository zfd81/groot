package chat

import "charm.land/lipgloss/v2"

// PopupModel is a simple floating text display overlay, dismissed with ESC/Enter.
// Used for /help and system messages that should not pollute the viewport history.
type PopupModel struct {
	visible bool
	content string
	width   int
}

// NewPopup creates a hidden popup.
func NewPopup() PopupModel {
	return PopupModel{}
}

// Show makes the popup visible with the given content.
func (p *PopupModel) Show(content string) {
	p.visible = true
	p.content = content
}

// Hide dismisses the popup.
func (p *PopupModel) Hide() {
	p.visible = false
	p.content = ""
}

// IsVisible reports whether the popup is currently displayed.
func (p *PopupModel) IsVisible() bool { return p.visible }

// SetWidth sets the render width for the popup.
func (p *PopupModel) SetWidth(w int) { p.width = w }

// View renders the popup with rounded border and word-wrapped content.
// Returns empty string when hidden.
func (p *PopupModel) View() string {
	if !p.visible || p.content == "" {
		return ""
	}

	contentStyle := lipgloss.NewStyle().Width(p.width - 4) // account for border + padding
	wrapped := contentStyle.Render(p.content)

	return CompletionStyle.Width(p.width).Render(wrapped)
}
