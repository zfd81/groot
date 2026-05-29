package chat

import (
	"encoding/json"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/viewport"
	"github.com/charmbracelet/glamour"
	"charm.land/lipgloss/v2"
)

// ChatMessage represents a single rendered message block in the viewport.
type ChatMessage struct {
	Role        string // user, thinking, tool_call, tool_result, tool_error, assistant, cancel, length, error, system
	Content     string
	Meta       string // tool name (for tool_call display)
	ToolCallID string // OpenAI tool call ID, used to aggregate streaming deltas
	ToolIndex  *int   // from OpenAI streaming, disambiguates consecutive same-name calls
	Folded      bool
	FullContent string // unfolded content when Folded is true
}

// ViewportModel wraps bubbles viewport with message list management.
type ViewportModel struct {
	viewport   viewport.Model
	messages   []ChatMessage
	mdRenderer *glamour.TermRenderer
	lastWidth  int
	// currentSpinner 是当前 spinner 帧字符；spinner.Tick 来时由 Model 设置。
	// 用于在「正在进行中的 tool_call」消息（尾部 tool_call 且未跟 tool_result）
	// 后追加旋转图标，让用户在 skill / call_agent 调用期间看到动画反馈。
	currentSpinner string
}

// bottomReserve is the vertical space reserved for bottom elements below the viewport:
// input (3 content + 2 border) + 1 separator + 1 statusbar = 7.
// The viewport height is terminal_height - bottomReserve.
// Total content lines = viewport lines + separator + input + separator + status.
const bottomReserve = 6

// NewViewport creates a viewport pre-filled with the welcome screen.
func NewViewport(width, height int) ViewportModel {
	vp := viewport.New(viewport.WithWidth(width), viewport.WithHeight(height-bottomReserve))
	vp.SetContent(WelcomeStyle.Render(WelcomeScreen))
	r, _ := glamour.NewTermRenderer(
		glamour.WithStylePath("pink"),
		glamour.WithWordWrap(width-4),
		glamour.WithPreservedNewLines(),
	)
	return ViewportModel{
		viewport:   vp,
		mdRenderer: r,
		lastWidth:  width,
	}
}

// SetSize recalculates available viewport area.
func (v *ViewportModel) SetSize(width, height int) {
	v.viewport.SetWidth(width)
	v.viewport.SetHeight(height - bottomReserve)
	if width != v.lastWidth {
		v.lastWidth = width
		v.mdRenderer, _ = glamour.NewTermRenderer(
			glamour.WithStylePath("pink"),
			glamour.WithWordWrap(width-4),
			glamour.WithPreservedNewLines(),
		)
		if len(v.messages) > 0 {
			v.rerender()
		}
	}
}

// AddMessage appends a message and re-renders.
func (v *ViewportModel) AddMessage(msg ChatMessage) {
	v.messages = append(v.messages, msg)
	v.rerender()
}

// UpdateLastMessage replaces the last message content (streaming updates).
func (v *ViewportModel) UpdateLastMessage(content string) {
	if len(v.messages) > 0 {
		v.messages[len(v.messages)-1].Content = content
		v.rerender()
	}
}

// UpdateToolCall finds a tool_call message within the current request to append
// streaming deltas to. Stops at user messages to avoid cross-request matching.
// Matching order: 1) ToolCallID, 2) ToolIndex, 3) by name (last matching).
func (v *ViewportModel) UpdateToolCall(toolCallID, toolName, arguments string, toolIndex *int) bool {
	// 1. Match by ID (exact, most reliable)
	if toolCallID != "" {
		for i := len(v.messages) - 1; i >= 0; i-- {
			if v.messages[i].Role == "user" {
				break
			}
			if v.messages[i].Role == "tool_call" && v.messages[i].ToolCallID == toolCallID {
				v.messages[i].Content += arguments
				v.rerender()
				return true
			}
		}
		// ID not found — this is a new tool call; don't fall through to Index/Name
		// which would incorrectly match a different tool call with the same index.
		return false
	}
	// 2. Match by Index — requires name match (or incoming name empty);
	// prevents matching across different tool calls that share the same index.
	if toolIndex != nil {
		for i := len(v.messages) - 1; i >= 0; i-- {
			if v.messages[i].Role == "user" {
				break
			}
			if v.messages[i].Role == "tool_call" &&
				v.messages[i].ToolIndex != nil &&
				*v.messages[i].ToolIndex == *toolIndex &&
				(toolName == "" || v.messages[i].Meta == toolName) {
				v.messages[i].Content += arguments
				v.rerender()
				return true
			}
		}
	}
	// 3. Fallback: match last tool_call by name within current request (name must be non-empty)
	if toolName != "" {
		for i := len(v.messages) - 1; i >= 0; i-- {
			if v.messages[i].Role == "user" {
				break
			}
			if v.messages[i].Role == "tool_call" && v.messages[i].Meta == toolName {
				v.messages[i].Content += arguments
				v.rerender()
				return true
			}
		}
	}
	return false
}

// Clear resets to welcome screen.
func (v *ViewportModel) Clear() {
	v.messages = nil
	v.viewport.SetContent(WelcomeStyle.Render(WelcomeScreen))
	v.viewport.GotoTop()
}

// SetContent replaces viewport content directly (for loading session history).
func (v *ViewportModel) SetContent(content string) {
	v.viewport.SetContent(content)
}

// ScrollToBottom scrolls to bottom.
func (v *ViewportModel) ScrollToBottom() {
	v.viewport.GotoBottom()
}

// AtBottom reports whether the viewport is scrolled to the end.
func (v *ViewportModel) AtBottom() bool {
	return v.viewport.AtBottom()
}

// View returns the rendered viewport.
func (v *ViewportModel) View() string {
	return ViewportStyle.Render(v.viewport.View())
}

func (v *ViewportModel) rerender() {
	wasAtBottom := v.viewport.AtBottom() || v.viewport.TotalLineCount() <= v.viewport.Height()
	contentWidth := v.viewport.Width() - 2 // 两侧各留 1 字符

	var sb strings.Builder
	for i, msg := range v.messages {
		// 清理 Windows 行尾 \r 字符，防止终端光标异常
		msg.Content = strings.ReplaceAll(msg.Content, "\r", "")
		// 「正在进行中的 tool_call」标志：当前消息是 tool_call 且后面没有更多消息——
		// 用于在 skill / call_agent / 普通工具的调用期间持续展示 spinner。
		isPendingToolCall := msg.Role == "tool_call" && i == len(v.messages)-1
		switch msg.Role {
		case "user":
			content := "> " + msg.Content
			// Pad content to contentWidth before styling so the background
			// is emitted as a single ANSI sequence rather than split across
			// a reset + background-only sequence (lipgloss v2 padding behavior).
			plainWidth := lipgloss.Width(content)
			if plainWidth < contentWidth {
				content += strings.Repeat(" ", contentWidth-plainWidth)
			}
			rendered := UserMessageStyle.Render(content)
			sb.WriteString(" ")
			sb.WriteString(rendered)
			sb.WriteString("\n\n")
		case "thinking":
			sb.WriteString(ThinkingLabel)
			sb.WriteString("\n")
			wrapped := lipgloss.NewStyle().
				Foreground(lipgloss.Color("#888888")).
				Italic(true).
				Width(contentWidth).
				PaddingLeft(3).
				Render(msg.Content)
			sb.WriteString(wrapped)
			sb.WriteString("\n\n")
		case "tool_call":
			// 进行中时在工具名后面追加一个 spinner 帧，让用户感知调用还在进行；
			// 调用结束（后面追加了非 tool_call 消息）后 isPendingToolCall=false，
			// spinner 自然消失。
			spinnerSuffix := ""
			if isPendingToolCall && v.currentSpinner != "" {
				spinnerSuffix = " " + v.currentSpinner
			}
			if msg.Meta == "skill" {
				sb.WriteString(SkillCallPrefix)
				skillName := extractSkillName(msg.Content)
				sb.WriteString(skillName)
				sb.WriteString(spinnerSuffix)
				sb.WriteString("\n")
			} else if msg.Meta == "call_agent" {
				sb.WriteString(SubAgentCallPrefix)
				agentName := extractSubAgentName(msg.Content)
				sb.WriteString(agentName)
				sb.WriteString(spinnerSuffix)
				sb.WriteString("\n")
			} else {
				sb.WriteString(ToolCallPrefix)
				sb.WriteString(msg.Meta)
				sb.WriteString(spinnerSuffix)
				sb.WriteString("\n")
				if msg.Content != "" {
					formatted := formatToolArgs(msg.Content)
					wrapped := lipgloss.NewStyle().
						Foreground(lipgloss.Color("#888888")).
						Width(contentWidth).
						PaddingLeft(3).
						Render(formatted)
					sb.WriteString(wrapped)
					sb.WriteString("\n")
				}
			}
			sb.WriteString("\n")
		case "tool_result":
			// 工具结果不展示，由 LLM 回答中体现
		case "tool_error":
			sb.WriteString(ToolErrorPrefix)
			sb.WriteString(msg.Meta)
			sb.WriteString("\n")
			sb.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color("#e06c75")).
				Width(contentWidth).
				PaddingLeft(3).
				Render(msg.Content))
			sb.WriteString("\n\n")
		case "assistant":
			if v.mdRenderer != nil {
				prepped := normalizeMarkdown(msg.Content)
				prepped = preserveLineBreaks(prepped)
				prepped = breakLongLines(prepped, contentWidth)
				rendered, err := v.mdRenderer.Render(prepped)
				if err == nil {
					sb.WriteString(strings.TrimRight(rendered, "\n"))
				} else {
					sb.WriteString(breakLongLines(normalizeMarkdown(msg.Content), contentWidth))
				}
			} else {
				sb.WriteString(breakLongLines(normalizeMarkdown(msg.Content), contentWidth))
			}
			sb.WriteString("\n\n")
		case "cancel":
			sb.WriteString(CancelLabel)
			sb.WriteString("\n\n")
		case "length":
			sb.WriteString(LengthLabel)
			sb.WriteString("\n\n")
		case "error":
			sb.WriteString(ErrorLabel)
			sb.WriteString(": ")
			sb.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color("#e06c75")).
				Width(contentWidth).
				Render(msg.Content))
			sb.WriteString("\n\n")
		case "loading":
			sb.WriteString("   ")
			sb.WriteString(msg.Content)
			sb.WriteString(" ")
			sb.WriteString(LoadingText)
			sb.WriteString("\n\n")
		case "system":
			sb.WriteString(lipgloss.NewStyle().
				Width(contentWidth).
				Render(msg.Content))
			sb.WriteString("\n\n")
		}
	}
	v.viewport.SetContent(sb.String())
	if wasAtBottom {
		v.viewport.GotoBottom()
	}
}

// normalizeMarkdown converts common // normalizeMarkdown converts common Unicode list markers to Markdown-standard syntax
// so that goldmark/glamour can recognize them as proper list items.
func normalizeMarkdown(s string) string {
	lines := strings.Split(s, "\n")
	var result strings.Builder
	for i, line := range lines {
		if i > 0 {
			result.WriteString("\n")
		}
		// Convert Unicode bullet characters to Markdown "- "
		trimmed := strings.TrimLeft(line, " \t")
		if len(trimmed) >= 2 {
			r := []rune(trimmed)[0]
			// Common Unicode bullet characters used by LLMs
			if r == '•' || r == '◦' || r == '▪' || r == '●' || r == '○' ||
				r == '·' || r == '∙' || r == '⋅' || r == '‣' || r == '▸' {
				// Replace the bullet with Markdown "- "
				idx := strings.Index(line, string(r))
				if idx >= 0 {
					line = line[:idx] + "-" + line[idx+len(string(r)):]
				}
			}
		}
		result.WriteString(line)
	}
	return result.String()
}

// preserveLineBreaks converts single newlines to Markdown hard breaks (trailing two spaces)
// so that glamour doesn't merge consecutive lines into one paragraph.
// Skips lines that are already part of Markdown block structures (code blocks, lists, headers, blank lines).
func preserveLineBreaks(s string) string {
	lines := strings.Split(s, "\n")
	var result strings.Builder
	inCodeBlock := false
	for i, line := range lines {
		if strings.HasPrefix(line, "```") {
			inCodeBlock = !inCodeBlock
		}
		result.WriteString(line)
		if i < len(lines)-1 {
			next := ""
			if i+1 < len(lines) {
				next = lines[i+1]
			}
			if inCodeBlock || line == "" || next == "" ||
				isHeading(line) ||
				strings.HasPrefix(line, "- ") ||
				strings.HasPrefix(line, "* ") ||
				strings.HasPrefix(line, "> ") ||
				strings.HasPrefix(line, "| ") ||
				strings.HasPrefix(line, "```") ||
				isHeading(next) ||
				strings.HasPrefix(next, "- ") ||
				strings.HasPrefix(next, "* ") ||
				strings.HasPrefix(next, "> ") ||
				strings.HasPrefix(next, "| ") ||
				strings.HasPrefix(next, "```") ||
				strings.HasSuffix(line, "  ") ||
				isOrderedList(line) || isHorizontalRule(line) ||
				isContinuationLine(line) ||
				isOrderedList(next) || isHorizontalRule(next) ||
				isContinuationLine(next) {
				result.WriteString("\n")
			} else {
				result.WriteString("  \n")
			}
		}
	}
	return result.String()
}

// isOrderedList checks if line starts with a numbered list marker (e.g., "1. ", "2) ").
func isOrderedList(s string) bool {
	trimmed := strings.TrimLeft(s, " ")
	if len(trimmed) < 3 {
		return false
	}
	for i := 0; i < len(trimmed); i++ {
		if trimmed[i] >= '0' && trimmed[i] <= '9' {
			continue
		}
		if (trimmed[i] == '.' || trimmed[i] == ')') && i > 0 {
			return i+1 < len(trimmed) && trimmed[i+1] == ' '
		}
		return false
	}
	return false
}

// isHorizontalRule checks if line is a horizontal rule (---, ***, ___).
func isHorizontalRule(s string) bool {
	t := strings.TrimSpace(s)
	if len(t) < 3 {
		return false
	}
	c := t[0]
	if c != '-' && c != '*' && c != '_' {
		return false
	}
	for _, ch := range t {
		if byte(ch) != c && ch != ' ' {
			return false
		}
	}
	return true
}

// isContinuationLine checks if line is a continuation of a previous block element.
func isContinuationLine(s string) bool {
	return len(s) > 0 && (s[0] == ' ' || s[0] == '\t')
}

// isHeading checks if line is an ATX heading (with optional leading whitespace).
func isHeading(s string) bool {
	trimmed := strings.TrimLeft(s, " \t")
	return strings.HasPrefix(trimmed, "#")
}

// breakLongLines inserts line breaks for very long CJK lines that wordwrap can't handle.
// CJK text has no spaces, so the wordwrap library treats the entire paragraph as one word
// and never breaks it. This function detects such lines and inserts hard breaks.
func breakLongLines(s string, width int) string {
	if width <= 0 {
		width = 80
	}
	lines := strings.Split(s, "\n")
	var result strings.Builder
	for i, line := range lines {
		if i > 0 {
			result.WriteString("\n")
		}
		if len(line) <= width {
			result.WriteString(line)
			continue
		}
		// Break long lines at word boundaries, falling back to hard break for CJK
		var current strings.Builder
		for _, r := range line {
			current.WriteRune(r)
			if current.Len() >= width {
				// Find last space in current buffer to break at word boundary
				cur := current.String()
				lastSpace := strings.LastIndexByte(cur, ' ')
				if lastSpace > width/2 {
					// Break at space
					result.WriteString(cur[:lastSpace])
					result.WriteString("\n")
					current.Reset()
					current.WriteString(cur[lastSpace+1:])
				} else {
					// No good break point — hard break for CJK or long words
					result.WriteString(cur)
					result.WriteString("\n")
					current.Reset()
				}
			}
		}
		if current.Len() > 0 {
			result.WriteString(current.String())
		}
	}
	return result.String()
}

func formatToolArgs(raw string) string {
	keys, args := orderedUnmarshal(raw)
	if keys == nil {
		if len(raw) > 100 {
			return raw[:97] + "..."
		}
		return raw
	}
	var parts []string
	for _, k := range keys {
		v := args[k]
		switch val := v.(type) {
		case map[string]interface{}:
			parts = append(parts, fmt.Sprintf("├─ %s:", k))
			for sk, sv := range val {
				s := formatValue(sv)
				if len(s) > 70 {
					s = s[:67] + "..."
				}
				parts = append(parts, fmt.Sprintf("│  %s = %s", sk, s))
			}
			_ = val
		default:
			s := formatValue(v)
			if len(s) > 70 {
				s = s[:67] + "..."
			}
			parts = append(parts, fmt.Sprintf("├─ %s = %s", k, s))
		}
	}
	return strings.Join(parts, "\n")
}

// orderedUnmarshal parses JSON and returns keys in their original order.
func orderedUnmarshal(raw string) ([]string, map[string]interface{}) {
	dec := json.NewDecoder(strings.NewReader(raw))
	t, err := dec.Token()
	if err != nil || t != json.Delim('{') {
		return nil, nil
	}
	var keys []string
	args := make(map[string]interface{})
	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			break
		}
		key := fmt.Sprintf("%v", keyToken)
		keys = append(keys, key)
		var val interface{}
		if err := dec.Decode(&val); err != nil {
			break
		}
		args[key] = val
	}
	return keys, args
}

// formatValue converts an interface value to a readable string.
func formatValue(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case float64:
		if val == float64(int64(val)) {
			return fmt.Sprintf("%d", int64(val))
		}
		return fmt.Sprintf("%g", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	case nil:
		return ""
	case []interface{}:
		items := make([]string, 0, len(val))
		for _, item := range val {
			items = append(items, formatValue(item))
		}
		return "[" + strings.Join(items, ", ") + "]"
	case map[string]interface{}:
		items := make([]string, 0, len(val))
		for k, v := range val {
			items = append(items, k+"="+formatValue(v))
		}
		return "{" + strings.Join(items, ", ") + "}"
	default:
		return fmt.Sprintf("%v", v)
	}
}

// eino skill middleware uses {"skill": "skill-name"} format.
func extractSkillName(raw string) string {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		if len(raw) > 50 {
			return raw[:47] + "..."
		}
		return raw
	}
	if name, ok := args["skill"]; ok {
		return fmt.Sprintf("%v", name)
	}
	if name, ok := args["name"]; ok {
		return fmt.Sprintf("%v", name)
	}
	if name, ok := args["skill_name"]; ok {
		return fmt.Sprintf("%v", name)
	}
	return raw
}

// extractSubAgentName extracts the sub-agent name from call_agent tool's JSON arguments.
// CallAgentTool 入参格式：{"agent_name": "...", "task": "..."}（见 internal/agent/call_agent.go:18）。
// 流式增量在解析完成前可能 unmarshal 失败，此时退回截断后的 raw 字符串以便用户看到反馈。
func extractSubAgentName(raw string) string {
	var args map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		if len(raw) > 50 {
			return raw[:47] + "..."
		}
		return raw
	}
	if name, ok := args["agent_name"]; ok {
		return fmt.Sprintf("%v", name)
	}
	return raw
}
