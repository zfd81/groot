package chat

import (
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/zfd81/groot/internal/config"

	"sort"
)

var dotSpinner = spinner.Spinner{
	Frames: []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"},
	FPS:    time.Second / 10,
}

// Model is the top-level Bubble Tea model holding all sub-components and state.
type Model struct {
	width  int
	height int

	status     StatusBar
	viewport   ViewportModel
	input      InputModel
	completion CompletionModel

	client *Client
	config *config.Config

	streaming   bool
	loading     bool
	cancelCh    chan struct{}
	eventsCh    chan tea.Msg
	sessionInit bool
	spinner     spinner.Model
	skillsList  []CompletionItem

	embedServer interface{ Shutdown() error }
	embedMode   bool
}

// NewModel creates a fully initialized TUI model.
func NewModel(cfg *config.Config, baseURL string) Model {
	client := NewClient(baseURL, cfg.LLM.DefaultModel)
	status := NewStatusBar(cfg.LLM.DefaultModel)

	width := 80
	height := 24

	vp := NewViewport(width, height)
	input := NewInput()
	input.SetSize(width, 5)
	completion := NewCompletion()
	completion.SetWidth(width - 2)

	s := spinner.New(spinner.WithSpinner(dotSpinner), spinner.WithStyle(lipgloss.NewStyle().Foreground(lipgloss.Color("#98c379"))))

	return Model{
		status:     status,
		viewport:   vp,
		input:      input,
		completion: completion,
		client:     client,
		config:     cfg,
		spinner:    s,
	}
}

// SetEmbedServer stores an embedded server reference for cleanup on exit.
func (m *Model) SetEmbedServer(srv interface{ Shutdown() error }) {
	m.embedServer = srv
	m.embedMode = true
}

// Init implements tea.Model.
func (m Model) Init() tea.Cmd {
	return textarea.Blink
}

// Update implements tea.Model. Central event dispatcher.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.SetSize(msg.Width, msg.Height)
		m.input.SetSize(msg.Width, 3)
		m.completion.SetWidth(msg.Width - 2)
		m.status.Width = msg.Width
		return m, nil

	case tea.KeyboardEnhancementsMsg:
		// Keyboard enhancements are now active; Shift+Enter and other
		// modifier keys will be properly reported by the terminal.
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKeyMsg(msg)

	case SseEventMsg:
		return m.handleSseEvent(SseEvent(msg))

	case SessionIDMsg:
		m.client.SetSessionID(string(msg))
		m.status.SessionID = string(msg)
		m.sessionInit = true
		return m, m.waitForEvents()

	case StreamDoneMsg:
		m.streaming = false
		if m.loading {
			m.loading = false
			m.removeLoadingMessage()
		}
		return m, nil

	case StreamErrorMsg:
		m.streaming = false
		if m.loading {
			m.loading = false
			m.removeLoadingMessage()
		}
		m.viewport.AddMessage(ChatMessage{
			Role: "error", Content: msg.Err.Error(),
		})
		return m, nil

	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spinner, cmd = m.spinner.Update(msg)
		if m.loading {
			for i := range m.viewport.messages {
				if m.viewport.messages[i].Role == "loading" {
					m.viewport.messages[i].Content = m.spinner.View()
					break
				}
			}
			m.viewport.rerender()
			return m, cmd
		}
		return m, nil

	case waitMsg:
		return m, m.waitForEvents()

	case modelPopupMsg:
		m.completion.Show(msg.models)
		m.input.SetGhostText(m.completion.GhostText())
		return m, nil

	case SkillsListMsg:
		m.skillsList = msg.Skills
		m.completion.Show(msg.Skills)
		m.completion.Mode = ModeSkill
		m.input.SetGhostText(m.completion.GhostText())
		return m, nil
	}

	if !m.completion.IsVisible() {
		newInput, cmd := m.input.Update(msg)
		m.input = newInput
		return m, cmd
	}
	return m, nil
}

// handleKeyMsg processes keyboard input.
func (m Model) handleKeyMsg(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch msg.String() {

	case "ctrl+c":
		return m, tea.Quit

	case "pgup":
		m.viewport.viewport.HalfPageUp()
		return m, nil

	case "pgdown":
		m.viewport.viewport.HalfPageDown()
		return m, nil

	case "up":
		if m.completion.IsVisible() {
			m.completion.SelectPrev()
			m.input.SetGhostText(m.completion.GhostText())
			return m, nil
		}
		newInput, _ := m.input.Update(msg)
		m.input = newInput
		return m, nil

	case "down":
		if m.completion.IsVisible() {
			m.completion.SelectNext()
			m.input.SetGhostText(m.completion.GhostText())
			return m, nil
		}
		newInput, _ := m.input.Update(msg)
		m.input = newInput
		return m, nil

	case "esc":
		if m.completion.IsVisible() {
			m.completion.Hide()
			m.input.ClearGhostText()
			return m, nil
		}
		if m.streaming {
			close(m.cancelCh)
			m.cancelCh = nil
			return m, nil
		}
		m.input.Reset()
		return m, nil

	case "tab":
		if m.completion.IsVisible() {
			return m.handleCompletionSelect()
		}

	case "enter":
		if m.completion.IsVisible() {
			return m.handleCompletionSelect()
		}
		if m.streaming {
			return m, nil
		}
		return m.handleSendMessage()

	case "shift+enter", "alt+enter":
		newInput, _ := m.input.Update(msg)
		m.input = newInput
		return m.checkCompletion()
	}

	// For non-special keys (characters, backspace, etc.), forward to textarea
	newInput, _ := m.input.Update(msg)
	m.input = newInput
	return m.checkCompletion()
}

// handleCompletionSelect executes the completion selection based on the current mode.
func (m Model) handleCompletionSelect() (tea.Model, tea.Cmd) {
	sel := m.completion.Selected()
	if sel == nil {
		m.completion.Hide()
		m.input.ClearGhostText()
		return m, nil
	}
	switch m.completion.Mode {
	case ModeModel:
		// 直接切换模型，更新状态栏，不修改输入框
		name := sel.Name
		m.client.SetModel(name)
		m.status.ModelName = name
		m.completion.Hide()
		m.input.ClearGhostText()
		return m, nil
	default:
		// 命令/技能补全：填入输入框
		m.input.AcceptGhostText()
		m.completion.Hide()
		return m, nil
	}
}

// handleSendMessage processes the input text as either a command or chat message.
func (m Model) handleSendMessage() (tea.Model, tea.Cmd) {
	text := m.input.Value()
	if text == "" {
		return m, nil
	}
	m.input.Reset()

	// 检测 skill 前缀：/skillName 指令内容
	if strings.HasPrefix(text, "/") && len(m.skillsList) > 0 {
		for _, skill := range m.skillsList {
			prefix := skill.Name + " "
			if strings.HasPrefix(text, prefix) {
				skillName := strings.TrimPrefix(skill.Name, "/")
				userInput := strings.TrimPrefix(text, prefix)
				text = fmt.Sprintf("请使用 %s skill 来处理以下指令：%s", skillName, userInput)
				break
			}
		}
	}

	if cmdMsg := ParseCommand(text); cmdMsg != nil {
		return m.handleCommand(*cmdMsg)
	}

	if !m.sessionInit {
		m.viewport.messages = nil
		m.viewport.viewport.SetContent("")
	}

	m.viewport.AddMessage(ChatMessage{Role: "user", Content: text})

	m.streaming = true
	m.loading = true
	m.cancelCh = make(chan struct{})
	m.eventsCh = make(chan tea.Msg, 100)
	m.client.SendChatStream(text, m.eventsCh, m.cancelCh)
	m.status.Round++

	m.viewport.AddMessage(ChatMessage{Role: "loading", Content: m.spinner.View()})

	return m, tea.Batch(m.waitForEvents(), func() tea.Msg { return m.spinner.Tick() })
}

// handleCommand executes a system command.
func (m Model) handleCommand(msg CommandMsg) (tea.Model, tea.Cmd) {
	result := ExecuteCommand(msg)

	switch result.Action {
	case "quit":
		return m, tea.Quit

	case "clear":
		m.clearSession()
		return m, nil

	case "model_popup":
		items := make([]CompletionItem, 0, len(m.config.LLM.Models))
		for name := range m.config.LLM.Models {
			marker := ""
			if name == m.client.ModelName() {
				marker = "✓"
			}
			items = append(items, CompletionItem{
				Name: name, Description: marker,
			})
		}
		m.completion.Show(items)
		m.completion.Mode = ModeModel
		m.input.SetGhostText(m.completion.GhostText())
		return m, nil

	case "switch_model":
		name := result.Content
		if _, ok := m.config.LLM.Models[name]; !ok {
			items := make([]CompletionItem, 0, len(m.config.LLM.Models))
			for n := range m.config.LLM.Models {
				items = append(items, CompletionItem{Name: n})
			}
			m.completion.Show(items)
			m.completion.Mode = ModeModel
			m.input.SetGhostText(m.completion.GhostText())
			return m, nil
		}
		m.client.SetModel(name)
		m.status.ModelName = name
		return m, nil

	case "fetch":
		return m, m.doFetchAPI(result.API)

	case "skills_popup":
		return m, m.fetchSkillsCmd()

	case "export":
		if m.client.SessionID() == "" {
			m.viewport.AddMessage(ChatMessage{
				Role: "system", Content: "没有活动会话可以导出，请先开始对话",
			})
			return m, nil
		}
		return m, m.doFetchAPI("/sess/" + m.client.SessionID())

	case "render":
		m.viewport.AddMessage(ChatMessage{Role: "system", Content: result.Content})
		return m, nil
	}
	return m, nil
}

// doFetchAPI GETs a path and renders the result.
func (m Model) doFetchAPI(path string) tea.Cmd {
	return func() tea.Msg {
		body, err := m.client.FetchJSON(path)
		if err != nil {
			return StreamErrorMsg{Err: fmt.Errorf("API 请求失败 (%s): %w", path, err)}
		}

		if strings.Contains(path, "/sess/") && !strings.Contains(path, "/history") && !strings.Contains(path, "/sess/history") {
			filename, err := ExportToMarkdown(body)
			if err != nil {
				return StreamErrorMsg{Err: fmt.Errorf("导出失败: %w", err)}
			}
			return SseEventMsg(SseEvent{
				Event:   "api_response",
				Content: fmt.Sprintf("对话已导出到: %s", filename),
			})
		}

		var pretty map[string]interface{}
		if json.Unmarshal(body, &pretty) == nil {
			formatted := formatAPIResponse(pretty)
			return SseEventMsg(SseEvent{Event: "api_response", Content: formatted})
		}
		return SseEventMsg(SseEvent{Event: "api_response", Content: string(body)})
	}
}

// handleSseEvent routes a parsed SSE event to the viewport.
func (m Model) handleSseEvent(event SseEvent) (tea.Model, tea.Cmd) {
	if event.Event == "api_response" {
		m.viewport.AddMessage(ChatMessage{Role: "system", Content: event.Content})
		return m, nil
	}

	if m.loading {
		m.loading = false
		m.removeLoadingMessage()
	}

	eType := classifyEvent(event)

	switch eType {
	case "thinking":
		if len(m.viewport.messages) > 0 &&
			m.viewport.messages[len(m.viewport.messages)-1].Role == "thinking" {
			m.viewport.UpdateLastMessage(
				m.viewport.messages[len(m.viewport.messages)-1].Content + event.Reasoning,
			)
		} else {
			m.viewport.AddMessage(ChatMessage{Role: "thinking", Content: event.Reasoning})
		}

	case "tool_calls":
		for _, tc := range event.ToolCalls {
			if !m.viewport.UpdateToolCall(tc.ID, tc.Function.Name, tc.Function.Arguments, tc.Index) {
				m.viewport.AddMessage(ChatMessage{
					Role:       "tool_call",
					Meta:       tc.Function.Name,
					Content:    tc.Function.Arguments,
					ToolCallID: tc.ID,
					ToolIndex:  tc.Index,
				})
			}
		}

	case "tool_result":
		content := event.Content
		if len(content) > 200 {
			content = content[:200] + "\n[... 展开]"
		}
		m.viewport.AddMessage(ChatMessage{Role: "tool_result", Content: content})

	case "message":
		if len(m.viewport.messages) > 0 &&
			m.viewport.messages[len(m.viewport.messages)-1].Role == "assistant" {
			m.viewport.UpdateLastMessage(
				m.viewport.messages[len(m.viewport.messages)-1].Content + event.Content,
			)
		} else {
			m.viewport.AddMessage(ChatMessage{Role: "assistant", Content: event.Content})
		}

	case "finish_reason":
		switch event.FinishReason {
		case "stop":
			// silent
		case "cancelled", "user_cancelled":
			m.viewport.AddMessage(ChatMessage{Role: "cancel"})
		case "length":
			m.viewport.AddMessage(ChatMessage{Role: "length"})
		}

	case "error":
		m.streaming = false
		if m.loading {
			m.loading = false
			m.removeLoadingMessage()
		}
		m.viewport.AddMessage(ChatMessage{Role: "error", Content: event.Message})
		return m, nil
	}

	return m, m.waitForEvents()
}

// removeLoadingMessage removes the loading indicator from the viewport.
func (m *Model) removeLoadingMessage() {
	for i := len(m.viewport.messages) - 1; i >= 0; i-- {
		if m.viewport.messages[i].Role == "loading" {
			m.viewport.messages = append(m.viewport.messages[:i], m.viewport.messages[i+1:]...)
			m.viewport.rerender()
			break
		}
	}
}

// fetchSkillsCmd fetches the skill list from the API and returns a SkillsListMsg.
func (m Model) fetchSkillsCmd() tea.Cmd {
	return func() tea.Msg {
		body, err := m.client.FetchJSON("/skills")
		if err != nil {
			return StreamErrorMsg{Err: fmt.Errorf("获取 skills 列表失败: %w", err)}
		}
		var resp struct {
			Skills []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"skills"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return StreamErrorMsg{Err: fmt.Errorf("解析 skills 响应失败: %w", err)}
		}
		items := make([]CompletionItem, len(resp.Skills))
		for i, s := range resp.Skills {
			items[i] = CompletionItem{Name: "/" + s.Name, Description: s.Description}
		}
		return SkillsListMsg{Skills: items}
	}
}

// checkCompletion evaluates whether the current input should trigger completion.
func (m Model) checkCompletion() (tea.Model, tea.Cmd) {
	val := m.input.Value()

	if !strings.HasPrefix(val, "/") {
		if m.completion.IsVisible() {
			m.completion.Hide()
			m.input.ClearGhostText()
		}
		return m, nil
	}

	switch {
	case strings.HasPrefix(val, "/model "):
		models := make([]CompletionItem, 0, len(m.config.LLM.Models))
		for name := range m.config.LLM.Models {
			models = append(models, CompletionItem{Name: name})
		}
		m.completion.Show(models)
		m.completion.Mode = ModeModel
		m.completion.Filter(strings.TrimPrefix(val, "/model "))

	default:
		m.completion.Show(SystemCommands)
		m.completion.Mode = ModeCommand
		m.completion.Filter(val)
	}

	if m.completion.IsVisible() {
		m.input.SetGhostText(m.completion.GhostText())
	}
	return m, nil
}

// waitForEvents returns a command that reads the next event from the SSE channel.
func (m Model) waitForEvents() tea.Cmd {
	return func() tea.Msg {
		select {
		case event, ok := <-m.eventsCh:
			if !ok {
				return StreamDoneMsg{}
			}
			return event
		case <-time.After(50 * time.Millisecond):
			return waitMsg{}
		}
	}
}

// waitMsg is a sentinel message that triggers another waitForEvents call.
type waitMsg struct{}

// clearSession resets the TUI state for a new conversation.
func (m *Model) clearSession() {
	m.viewport.Clear()
	m.client.SetSessionID("")
	m.status.SessionID = "新会话"
	m.status.Round = 0
	m.sessionInit = false
}

// View renders the entire TUI layout.
// Layout from top to bottom: Viewport -> Completion (overlay) -> Input -> StatusBar.
// When completion is visible, the viewport height is temporarily reduced so the
// completion overlays the bottom of the viewport area without pushing the input down.
func (m Model) View() tea.View {
	completionView := ""
	compLines := 0
	if m.completion.IsVisible() {
		completionView = m.completion.View()
		if completionView != "" {
			completionView += "\n"
			compLines = strings.Count(completionView, "\n")
		}
	}

	// Trim viewport content when completion is visible so the completion
	// overlays the bottom of the viewport area without pushing input down.
	vpView := m.viewport.View()
	if compLines > 0 {
		lines := strings.Split(vpView, "\n")
		if len(lines) > compLines {
			vpView = strings.Join(lines[:len(lines)-compLines], "\n")
		}
	}

	content := vpView + "\n" +
		completionView +
		m.input.View(m.width) + "\n" +
		m.status.View()

	v := tea.NewView(content)
	v.AltScreen = true
	v.KeyboardEnhancements.ReportEventTypes = true

	// Declarative cursor from textarea for IME composition support.
	// Offset textarea-internal cursor to screen coordinates:
	//   X: border(1) + padding(1) = 2
	//   Y: viewport lines + separator(1) + completion lines + input top border(1)
	if c := m.input.textarea.Cursor(); c != nil {
		c.Position.X += 2
		c.Position.Y += strings.Count(vpView, "\n") + compLines + 2
		v.Cursor = c
	}

	return v
}

// classifyEvent determines the event type from JSON fields.
func classifyEvent(event SseEvent) string {
	if event.Event == "error" {
		return "error"
	}
	if event.Reasoning != "" {
		return "thinking"
	}
	if len(event.ToolCalls) > 0 {
		return "tool_calls"
	}
	if event.Role == "tool" {
		return "tool_result"
	}
	if event.FinishReason != "" {
		return "finish_reason"
	}
	if event.Content != "" {
		return "message"
	}
	return "unknown"
}

// formatAPIResponse pretty-prints API data for display.
func formatAPIResponse(data map[string]interface{}) string {
	var sb strings.Builder

	if status, ok := data["status"]; ok {
		sb.WriteString(fmt.Sprintf("**状态**: %v\n\n", status))
	}

	// 按 MCP 分组的工具列表 (GET /tools)
	if groups := detectGroupedTools(data); groups != nil {
		writeToolsTree(&sb, groups)
		return sb.String()
	}

	// 技能列表
	if skills, ok := data["skills"].([]interface{}); ok {
		sb.WriteString("| 技能名 | 描述 |\n")
		sb.WriteString("|--------|------|\n")
		for _, s := range skills {
			if skill, ok := s.(map[string]interface{}); ok {
				name := skill["name"]
				desc := fmt.Sprintf("%v", skill["description"])
				if len(desc) > 80 {
					desc = desc[:77] + "..."
				}
				sb.WriteString(fmt.Sprintf("| %v | %s |\n", name, desc))
			}
		}
		return sb.String()
	}

	// 会话列表
	if sessions, ok := data["sessions"].([]interface{}); ok {
		sb.WriteString("| 会话 ID | 创建时间 | 轮数 |\n")
		sb.WriteString("|----------|----------|------|\n")
		for _, s := range sessions {
			if sess, ok := s.(map[string]interface{}); ok {
				sb.WriteString(fmt.Sprintf("| %v | %v | %v |\n",
					sess["session_id"], sess["created_at"], sess["round_count"]))
			}
		}
		return sb.String()
	}

	for k, v := range data {
		if k == "status" || k == "total" || k == "limit" || k == "offset" {
			continue
		}
		sb.WriteString(fmt.Sprintf("**%s**:\n```\n%v\n```\n\n", k, v))
	}
	return sb.String()
}

// detectGroupedTools checks if data represents a map of MCP name → ToolsGroup.
func detectGroupedTools(data map[string]interface{}) map[string][]toolEntry {
	// Known keys that are NOT MCP server names
	knownKeys := map[string]bool{
		"status": true, "skills": true, "tools": true, "total": true,
		"limit": true, "offset": true, "sessions": true,
	}
	groups := make(map[string][]toolEntry)
	for k, v := range data {
		if knownKeys[k] {
			continue
		}
		group, ok := v.(map[string]interface{})
		if !ok {
			return nil
		}
		toolsRaw, hasTools := group["tools"]
		if !hasTools {
			return nil
		}
		toolsArr, ok := toolsRaw.([]interface{})
		if !ok {
			return nil
		}
		var entries []toolEntry
		for _, t := range toolsArr {
			tool, ok := t.(map[string]interface{})
			if !ok {
				continue
			}
			name := fmt.Sprintf("%v", tool["name"])
			desc := fmt.Sprintf("%v", tool["description"])
			entries = append(entries, toolEntry{Name: name, Description: desc})
		}
		groups[k] = entries
	}
	if len(groups) == 0 {
		return nil
	}
	return groups
}

type toolEntry struct {
	Name        string
	Description string
}

// writeToolsTree writes grouped tools in tree format to sb.
func writeToolsTree(sb *strings.Builder, groups map[string][]toolEntry) {
	// Sort MCP names for deterministic output
	names := make([]string, 0, len(groups))
	for name := range groups {
		names = append(names, name)
	}
	sort.Strings(names)

	// Calculate max tool name width for alignment across all groups
	maxNameLen := 0
	for _, entries := range groups {
		for _, e := range entries {
			if len(e.Name) > maxNameLen {
				maxNameLen = len(e.Name)
			}
		}
	}

	// approximate terminal width for description wrapping
	totalWidth := 80

	for i, name := range names {
		entries := groups[name]
		if i > 0 {
			sb.WriteString("\n")
		}

		if len(entries) == 0 {
			sb.WriteString(fmt.Sprintf("🔧 %s (无工具)\n", name))
			continue
		}

		sb.WriteString(fmt.Sprintf("🔧 %s (%d 个工具)\n", name, len(entries)))

		for _, entry := range entries {
			lineStart := fmt.Sprintf("  %s — ", entry.Name)
			// Pad name to align descriptions
			padding := maxNameLen - len(entry.Name)
			if padding > 0 {
				lineStart = fmt.Sprintf("  %s%s — ", entry.Name, strings.Repeat(" ", padding))
			}
			desc := entry.Description
			if desc == "" {
				desc = "(无描述)"
			}
			// Word wrap description
			wrapWidth := totalWidth - len(lineStart)
			if wrapWidth < 20 {
				wrapWidth = 20
			}
			wrapped := wrapText(desc, wrapWidth)
			for wi, line := range wrapped {
				if wi == 0 {
					sb.WriteString(lineStart + line + "\n")
				} else {
					// Align continuation lines with description start
					sb.WriteString(strings.Repeat(" ", len(lineStart)) + line + "\n")
				}
			}
		}
	}
}

// wrapText wraps text at word boundaries within the given width.
func wrapText(text string, width int) []string {
	if width <= 0 || len(text) <= width {
		return []string{text}
	}
	var lines []string
	remaining := text
	for len(remaining) > width {
		// Find last space within width
		brk := width
		for brk > 0 && remaining[brk] != ' ' {
			brk--
		}
		if brk == 0 {
			// No space found, hard break at width
			brk = width
		}
		lines = append(lines, remaining[:brk])
		// Skip spaces after break point
		for brk < len(remaining) && remaining[brk] == ' ' {
			brk++
		}
		remaining = remaining[brk:]
	}
	if len(remaining) > 0 {
		lines = append(lines, remaining)
	}
	return lines
}
