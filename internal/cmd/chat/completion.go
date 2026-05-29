package chat

import "strings"

// CompletionMode indicates what the completion popup is being used for.
type CompletionMode int

const (
	ModeCommand CompletionMode = iota // 命令/子命令补全（填入输入框）
	ModeModel                         // 模型选择（直接切换）
	ModeSkill                         // 技能选择（填入输入框）
	ModeFile                          // 文件路径补全（@path）
	ModeAgent                         // Agent 选择（直接切换）
)

// CompletionModel manages popup visibility, filtering, and selection.
type CompletionModel struct {
	visible   bool
	items     []CompletionItem
	filtered  []CompletionItem
	selected  int
	width     int
	maxItems  int
	ghostText string
	Mode      CompletionMode

	// filePrefix is the text before '@' when in ModeFile, used to
	// construct full-line ghost text for path completion.
	filePrefix string
}

// NewCompletion creates a hidden completion model.
func NewCompletion() CompletionModel {
	return CompletionModel{maxItems: 8}
}

// Show makes the popup visible with the given items.
func (c *CompletionModel) Show(items []CompletionItem) {
	c.visible = true
	c.items = items
	c.filtered = items
	c.selected = 0
	c.updateGhostText()
}

// Hide dismisses the popup.
func (c *CompletionModel) Hide() {
	c.visible = false
	c.items = nil
	c.filtered = nil
	c.selected = 0
	c.ghostText = ""
}

// IsVisible reports whether the popup is currently displayed.
func (c *CompletionModel) IsVisible() bool { return c.visible }

// GhostText returns the ghost completion suffix for the current selection.
func (c *CompletionModel) GhostText() string { return c.ghostText }

// Filter narrows items by prefix match. Case-insensitive.
func (c *CompletionModel) Filter(prefix string) {
	if !c.visible {
		return
	}
	c.filtered = nil
	lower := strings.ToLower(prefix)
	for _, item := range c.items {
		if strings.HasPrefix(strings.ToLower(item.Name), lower) {
			c.filtered = append(c.filtered, item)
		}
	}
	if len(c.filtered) == 0 {
		c.Hide()
		return
	}
	c.selected = 0
	c.updateGhostText()
}

// SelectNext moves highlight down, wraps around.
func (c *CompletionModel) SelectNext() {
	if len(c.filtered) == 0 {
		return
	}
	c.selected = (c.selected + 1) % len(c.filtered)
	c.updateGhostText()
}

// SelectPrev moves highlight up, wraps around.
func (c *CompletionModel) SelectPrev() {
	if len(c.filtered) == 0 {
		return
	}
	c.selected--
	if c.selected < 0 {
		c.selected = len(c.filtered) - 1
	}
	c.updateGhostText()
}

// Selected returns the currently highlighted item, or nil.
func (c *CompletionModel) Selected() *CompletionItem {
	if len(c.filtered) == 0 || c.selected >= len(c.filtered) {
		return nil
	}
	return &c.filtered[c.selected]
}

// SetWidth sets the popup render width.
func (c *CompletionModel) SetWidth(w int) { c.width = w }

// View renders the popup (empty string when hidden).
func (c *CompletionModel) View() string {
	if !c.visible || len(c.filtered) == 0 {
		return ""
	}

	start := c.selected - c.maxItems/2
	if start < 0 {
		start = 0
	}
	end := start + c.maxItems
	if end > len(c.filtered) {
		end = len(c.filtered)
	}

	// 计算名称列的最大宽度用于对齐
	maxNameLen := 0
	for i := start; i < end; i++ {
		if len(c.filtered[i].Name) > maxNameLen {
			maxNameLen = len(c.filtered[i].Name)
		}
	}

	// 描述可用宽度
	descMaxLen := c.width - maxNameLen - 6 // 留出边框和间距
	if descMaxLen < 10 {
		descMaxLen = 10
	}

	var lines []string
	for i := start; i < end; i++ {
		item := c.filtered[i]
		name := item.Name
		// 对齐名称列
		padding := maxNameLen - len(name)
		if padding > 0 {
			name += strings.Repeat(" ", padding)
		}
		desc := item.Description
		if len(desc) > descMaxLen {
			desc = desc[:descMaxLen-3] + "..."
		}
		line := name + "  " + desc
		if i == c.selected {
			lines = append(lines, CompletionSelectedStyle.Render(line))
		} else {
			lines = append(lines, CompletionNormalStyle.Render(line))
		}
	}

	return CompletionStyle.Width(c.width).Render(strings.Join(lines, "\n"))
}

func (c *CompletionModel) updateGhostText() {
	if sel := c.Selected(); sel != nil {
		if c.Mode == ModeFile {
			c.ghostText = c.filePrefix + "@" + sel.Name
		} else {
			c.ghostText = sel.Name + " "
		}
	} else {
		c.ghostText = ""
	}
}

// ----- Pre-defined completion lists -----

// SystemCommands is the full list of system commands for auto-completion.
var SystemCommands = []CompletionItem{
	{Name: "/exit", Description: "退出聊天"},
	{Name: "/model", Description: "切换模型"},
	{Name: "/agent", Description: "切换 Agent"},
	{Name: "/clear", Description: "清空对话"},
	{Name: "/help", Description: "显示帮助"},
	{Name: "/skills", Description: "查看已安装 skill"},
	{Name: "/mcp", Description: "查看可用工具"},
	{Name: "/export", Description: "导出对话"},
}

