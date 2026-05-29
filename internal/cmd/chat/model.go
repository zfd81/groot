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

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/api/types"
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
	popup      PopupModel

	client *Client
	config *config.Config

	streaming         bool
	loading           bool
	cancelCh          chan struct{}
	eventsCh          chan tea.Msg
	sessionInit       bool
	spinner           spinner.Model
	skillsList        []CompletionItem
	availableModels   []CompletionItem
	pendingModelAction string // "" = none, "popup" = show popup after fetch, otherwise model name to switch to
	availableAgents    []CompletionItem
	pendingAgentAction string // "" = none, "popup" = show popup after fetch, otherwise agent name to switch to

	embedServer interface{ Shutdown() error }
	embedMode   bool
	focusInInput bool
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
		popup:      NewPopup(),
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
	return tea.Batch(textarea.Blink, m.fetchModelsCmd(), m.fetchAgentsCmd())
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
		// 把当前 spinner 帧透到 viewport，让 tool_call 渲染分支能在
		// 「正在进行中的最近一条 tool_call」末尾拼出旋转图标
		// （skill / call_agent / 普通工具调用期间都生效）。
		m.viewport.currentSpinner = m.spinner.View()
		// loading 占位的内容也用同一帧字符
		if m.loading {
			for i := range m.viewport.messages {
				if m.viewport.messages[i].Role == "loading" {
					m.viewport.messages[i].Content = m.spinner.View()
					break
				}
			}
		}
		// streaming 中：触发一次 rerender 让 spinner 帧字符的变化反映到屏幕。
		if m.streaming {
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

	case ModelsListMsg:
		m.availableModels = msg.Models
		switch m.pendingModelAction {
		case "popup":
			items := m.buildModelPopupItems()
			m.completion.Show(items)
			m.completion.Mode = ModeModel
			m.input.SetGhostText(m.completion.GhostText())
		case "":
			// no pending action; if user is already typing /model, refresh completion
			if strings.HasPrefix(m.input.Value(), "/model ") && m.completion.IsVisible() {
				m.completion.Show(m.availableModels)
				m.completion.Mode = ModeModel
				m.completion.Filter(strings.TrimPrefix(m.input.Value(), "/model "))
				if m.completion.IsVisible() {
					m.input.SetGhostText(m.completion.GhostText())
				}
			}
		default:
			// pendingModelAction is a model name to switch to
			name := m.pendingModelAction
			if m.findModelByName(name) != nil {
				m.client.SetModel(name)
				m.status.ModelName = name
			} else {
				items := m.buildModelPopupItems()
				m.completion.Show(items)
				m.completion.Mode = ModeModel
				m.input.SetGhostText(m.completion.GhostText())
			}
		}
		m.pendingModelAction = ""
		return m, nil

	case AgentsListMsg:
		m.availableAgents = msg.Agents
		switch m.pendingAgentAction {
		case "popup":
			items := m.buildAgentPopupItems()
			m.completion.Show(items)
			m.completion.Mode = ModeAgent
			m.input.SetGhostText(m.completion.GhostText())
		case "":
			// no pending action
		default:
			name := m.pendingAgentAction
			m.applyAgentSwitch(name)
		}
		m.pendingAgentAction = ""
		return m, nil

	case tea.MouseWheelMsg:
		mouse := msg.Mouse()
		switch mouse.Button {
		case tea.MouseWheelUp:
			m.viewport.viewport.ScrollUp(3)
		case tea.MouseWheelDown:
			m.viewport.viewport.ScrollDown(3)
		}
		return m, nil

	case tea.MouseClickMsg:
		mouse := msg.Mouse()
		if mouse.Button == tea.MouseLeft {
			inputView := m.input.View(m.width)
			inputLines := strings.Count(inputView, "\n") + 1
			inputStartY := m.height - 2 - inputLines
			inputEndY := m.height - 3
			if mouse.Y >= inputStartY && mouse.Y <= inputEndY {
				m.focusInInput = true
			} else {
				m.focusInInput = false
			}
		}
		return m, nil
	}

	if !m.completion.IsVisible() {
		newInput, cmd := m.input.Update(msg)
		m.input = newInput
		// 拖拽/粘贴进来的裸路径自动添加 @ 前缀（非按键事件路径）
		if fixed, changed := autoPrefixBarePaths(m.input.Value()); changed {
			m.input.textarea.SetValue(fixed)
			m.input.textarea.CursorEnd()
		}
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
		if m.popup.IsVisible() {
			return m, nil
		}
		if m.focusInInput {
			newInput, _ := m.input.Update(msg)
			m.input = newInput
			return m, nil
		}
		m.viewport.viewport.ScrollUp(1)
		return m, nil

	case "down":
		if m.completion.IsVisible() {
			m.completion.SelectNext()
			m.input.SetGhostText(m.completion.GhostText())
			return m, nil
		}
		if m.popup.IsVisible() {
			return m, nil
		}
		if m.focusInInput {
			newInput, _ := m.input.Update(msg)
			m.input = newInput
			return m, nil
		}
		m.viewport.viewport.ScrollDown(1)
		return m, nil

	case "esc":
		if m.popup.IsVisible() {
			m.popup.Hide()
			return m, nil
		}
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
			if m.completion.Mode == ModeModel {
				sel := m.completion.Selected()
				if sel != nil {
					m.input.textarea.SetValue("/model " + sel.Name + " ")
					m.input.textarea.CursorEnd()
				}
				m.input.ClearGhostText()
				m.completion.Hide()
				return m, nil
			}
			if m.completion.Mode == ModeAgent {
				sel := m.completion.Selected()
				if sel != nil {
					m.input.textarea.SetValue("/agent " + sel.Name + " ")
					m.input.textarea.CursorEnd()
				}
				m.input.ClearGhostText()
				m.completion.Hide()
				return m, nil
			}
			return m.handleCompletionSelect()
		}

	case "enter":
		if m.popup.IsVisible() {
			m.popup.Hide()
			return m, nil
		}
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
		m.client.SetModel(sel.Name)
		m.status.ModelName = sel.Name
		m.input.Reset()
		m.completion.Hide()
		return m, nil
	case ModeAgent:
		m.applyAgentSwitch(sel.Name)
		m.input.Reset()
		m.completion.Hide()
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
	if strings.TrimSpace(text) == "" {
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

	// 提取 @path 附件引用，读取文件内容，替换文本中的引用为文件名
	var atts []types.Attachment
	if refs := ExtractFileRefs(text); len(refs) > 0 {
		var pathToNames map[string][]string
		var err error
		atts, pathToNames, err = ReadAttachments(refs)
		if err != nil {
			m.viewport.AddMessage(ChatMessage{
				Role: "error", Content: fmt.Sprintf("读取附件失败: %v", err),
			})
			return m, nil
		}
		text = StripFileRefs(text, pathToNames)
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
	m.client.SendChatStream(text, atts, m.eventsCh, m.cancelCh)
	m.status.Round++

	m.viewport.AddMessage(ChatMessage{Role: "loading", Content: m.spinner.View()})

	return m, tea.Batch(
		m.waitForEvents(),
		func() tea.Msg { return m.spinner.Tick() },
	)
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
		if len(m.availableModels) > 0 {
			items := m.buildModelPopupItems()
			m.completion.Show(items)
			m.completion.Mode = ModeModel
			m.input.SetGhostText(m.completion.GhostText())
			return m, nil
		}
		m.pendingModelAction = "popup"
		return m, m.fetchModelsCmd()

	case "switch_model":
		name := result.Content
		if len(m.availableModels) > 0 {
			if m.findModelByName(name) != nil {
				m.client.SetModel(name)
				m.status.ModelName = name
				return m, nil
			}
			items := m.buildModelPopupItems()
			m.completion.Show(items)
			m.completion.Mode = ModeModel
			m.input.SetGhostText(m.completion.GhostText())
			return m, nil
		}
		m.pendingModelAction = name
		return m, m.fetchModelsCmd()

	case "agent_popup":
		if len(m.availableAgents) > 0 {
			items := m.buildAgentPopupItems()
			m.completion.Show(items)
			m.completion.Mode = ModeAgent
			m.input.SetGhostText(m.completion.GhostText())
			return m, nil
		}
		m.pendingAgentAction = "popup"
		return m, m.fetchAgentsCmd()

	case "switch_agent":
		name := result.Content
		if len(m.availableAgents) > 0 {
			m.applyAgentSwitch(name)
			return m, nil
		}
		m.pendingAgentAction = name
		return m, m.fetchAgentsCmd()

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

	case "help_popup":
		m.popup.Show(result.Content)
		return m, nil

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

	// 任何事件到达时先把当前的 loading 占位删掉，避免它残留在被新内容追加之前的位置；
	// 处理完事件后会按需重新追加（见末尾 maybeAppendLoading）。
	m.removeLoadingMessage()

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

	case "tool_error":
		content := event.Content
		if len(content) > 200 {
			content = content[:200] + "\n[... 展开]"
		}
		m.viewport.AddMessage(ChatMessage{
			Role:    "tool_error",
			Meta:    event.ToolName,
			Content: content,
		})

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
		// finish_reason 是终态（继续流式时上游会再发别的事件），不追加 loading
		m.loading = false
		return m, m.waitForEvents()

	case "error":
		m.streaming = false
		m.loading = false
		m.viewport.AddMessage(ChatMessage{Role: "error", Content: event.Message})
		return m, nil
	}

	// 流式期间在尾部追加新 loading 占位，让"调用工具/skill/子 Agent 期间"也有动画反馈。
	// 若尾消息已经是 assistant 流式中则跳过——内容增长本身就是反馈，再叠 spinner 会闪。
	m.maybeAppendLoading()
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

// maybeAppendLoading 在流式期间的事件之间追加一个 loading 占位，使
// "调用工具 / skill / 子 Agent 期间"也有 spinner 动画反馈。
//
// 跳过条件（保持已有视觉反馈不冲突）：
//   - 不在 streaming 状态
//   - 尾部消息已是 assistant 流式中（内容增长本身就是反馈）
//   - 尾部消息已是 thinking（reasoning 流式增长本身就是反馈）
func (m *Model) maybeAppendLoading() {
	if !m.streaming {
		return
	}
	if n := len(m.viewport.messages); n > 0 {
		switch m.viewport.messages[n-1].Role {
		case "assistant", "thinking", "loading":
			return
		}
	}
	m.loading = true
	m.viewport.AddMessage(ChatMessage{Role: "loading", Content: m.spinner.View()})
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

// fetchModelsCmd fetches the model list from the /models API.
func (m Model) fetchModelsCmd() tea.Cmd {
	return func() tea.Msg {
		body, err := m.client.FetchJSON("/models")
		if err != nil {
			return StreamErrorMsg{Err: fmt.Errorf("获取模型列表失败: %w", err)}
		}
		var resp struct {
			Models []struct {
				Name    string `json:"name"`
				Model   string `json:"model"`
				BaseURL string `json:"base_url"`
			} `json:"models"`
			Default string `json:"default"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return StreamErrorMsg{Err: fmt.Errorf("解析模型列表失败: %w", err)}
		}
		items := make([]CompletionItem, len(resp.Models))
		for i, m := range resp.Models {
			items[i] = CompletionItem{Name: m.Name, Description: m.Model}
		}
		return ModelsListMsg{Models: items, Default: resp.Default}
	}
}

// fetchAgentsCmd fetches the agent list from the /agents API.
func (m Model) fetchAgentsCmd() tea.Cmd {
	return func() tea.Msg {
		body, err := m.client.FetchJSON("/agents")
		if err != nil {
			return StreamErrorMsg{Err: fmt.Errorf("获取 Agent 列表失败: %w", err)}
		}
		var resp struct {
			Agents []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
			} `json:"agents"`
		}
		if err := json.Unmarshal(body, &resp); err != nil {
			return StreamErrorMsg{Err: fmt.Errorf("解析 Agent 列表失败: %w", err)}
		}
		items := make([]CompletionItem, len(resp.Agents))
		for i, a := range resp.Agents {
			items[i] = CompletionItem{Name: a.Name, Description: a.Description}
		}
		return AgentsListMsg{Agents: items}
	}
}

// buildModelPopupItems builds the CompletionItem list for the model popup,
// marking the currently active model with a checkmark.
func (m Model) buildModelPopupItems() []CompletionItem {
	items := make([]CompletionItem, 0, len(m.availableModels))
	for _, item := range m.availableModels {
		marker := ""
		if item.Name == m.client.ModelName() {
			marker = "✓"
		}
		items = append(items, CompletionItem{
			Name: item.Name, Description: marker,
		})
	}
	return items
}

// findModelByName looks up a model in the cached availableModels list.
func (m Model) findModelByName(name string) *CompletionItem {
	for i := range m.availableModels {
		if strings.EqualFold(m.availableModels[i].Name, name) {
			return &m.availableModels[i]
		}
	}
	return nil
}

// applyAgentSwitch 切换 Agent 并新建会话；name 为空或 MainAgentName 视为切回主 Agent。
// 未识别的 name 触发 popup 让用户重选。
// 警告：本方法 mutates receiver（client/status/sessionID），调用方必须能拿到生效后的 m。
func (m *Model) applyAgentSwitch(name string) {
	isMain := name == "" || name == agent.MainAgentName
	if !isMain && m.findAgentByName(name) == nil {
		items := m.buildAgentPopupItems()
		m.completion.Show(items)
		m.completion.Mode = ModeAgent
		m.input.SetGhostText(m.completion.GhostText())
		return
	}
	if isMain {
		m.client.SetAgent("")
		m.status.AgentName = agent.MainAgentName
	} else {
		m.client.SetAgent(name)
		m.status.AgentName = name
	}
	m.clearSession()
}

// findAgentByName 在缓存的 availableAgents 中按名查找（忽略大小写）。
func (m Model) findAgentByName(name string) *CompletionItem {
	for i := range m.availableAgents {
		if strings.EqualFold(m.availableAgents[i].Name, name) {
			return &m.availableAgents[i]
		}
	}
	return nil
}

// buildAgentPopupItems 构建 popup 列表，给当前 Agent 加 ✓ 标记。
// /agents API 已包含 groot（Task 14），但代码层做去重保险。
func (m Model) buildAgentPopupItems() []CompletionItem {
	items := make([]CompletionItem, 0, len(m.availableAgents)+1)
	seenGroot := false
	for _, item := range m.availableAgents {
		if item.Name == agent.MainAgentName {
			seenGroot = true
		}
		marker := ""
		if item.Name == m.status.AgentName ||
			(item.Name == agent.MainAgentName && (m.status.AgentName == "" || m.status.AgentName == agent.MainAgentName)) {
			marker = "✓"
		}
		items = append(items, CompletionItem{Name: item.Name, Description: marker + " " + item.Description})
	}
	if !seenGroot {
		// 防御性兜底；Task 14 后 /agents 通常会首位返回 groot，正常路径不触发。
		marker := ""
		if m.status.AgentName == "" || m.status.AgentName == agent.MainAgentName {
			marker = "✓"
		}
		// 放到最前面
		items = append([]CompletionItem{{Name: agent.MainAgentName, Description: marker + " 主 Agent"}}, items...)
	}
	return items
}

// checkCompletion evaluates whether the current input should trigger completion.
func (m Model) checkCompletion() (tea.Model, tea.Cmd) {
	val := m.input.Value()

	// 自动为拖拽/粘贴进来的裸路径添加 @ 前缀
	if fixed, changed := autoPrefixBarePaths(val); changed {
		m.input.textarea.SetValue(fixed)
		m.input.textarea.CursorEnd()
		val = fixed
	}

	// 处理 @path 文件路径补全
	if ref := extractActiveFileRef(val); ref != "" {
		expanded := expandTilde(ref)
		items := listPathItems(expanded)
		if len(items) > 0 {
			atIdx := strings.LastIndex(val, "@")
			m.completion.Mode = ModeFile
			m.completion.filePrefix = val[:atIdx]
			m.completion.Show(items)
			m.completion.Filter(expanded)
			if m.completion.IsVisible() {
				m.input.SetGhostText(m.completion.GhostText())
			}
			return m, nil
		}
	}

	if !strings.HasPrefix(val, "/") {
		if m.completion.IsVisible() {
			m.completion.Hide()
			m.input.ClearGhostText()
		}
		return m, nil
	}

	switch {
	case strings.HasPrefix(val, "/model "):
		if len(m.availableModels) > 0 {
			m.completion.Show(m.availableModels)
			m.completion.Mode = ModeModel
			m.completion.Filter(strings.TrimPrefix(val, "/model "))
		}

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
// 注意：不重置 ModelName / AgentName —— 这两项跨会话保留。
func (m *Model) clearSession() {
	m.viewport.Clear()
	m.client.SetSessionID("")
	m.status.SessionID = "新会话"
	m.status.Round = 0
	m.sessionInit = false
}

	// Layout from top to bottom: Viewport -> Overlay (popup or completion) -> Input -> StatusBar.
	// When popup or completion is visible, the viewport is trimmed so the overlay sits
	// between viewport and input without pushing the input down.
	func (m Model) View() tea.View {
		overlayView := ""
		overlayLines := 0
		if m.popup.IsVisible() {
			m.popup.SetWidth(m.width - 2)
			overlayView = m.popup.View()
			if overlayView != "" {
				overlayView += "\n"
				overlayLines = strings.Count(overlayView, "\n")
			}
		} else if m.completion.IsVisible() {
			overlayView = m.completion.View()
			if overlayView != "" {
				overlayView += "\n"
				overlayLines = strings.Count(overlayView, "\n")
			}
		}

		// Trim viewport content when overlay is visible so it sits at the
		// bottom of the viewport area without pushing input down.
		vpView := m.viewport.View()
		if overlayLines > 0 {
			lines := strings.Split(vpView, "\n")
			maxOverlay := len(lines) - 1
			if maxOverlay < 1 {
				maxOverlay = 1
			}
			if overlayLines > maxOverlay {
				// Popup content is too tall; truncate inner content while
				// keeping top and bottom borders intact.
				ovLines := strings.Split(overlayView, "\n")
				if len(ovLines) > maxOverlay && maxOverlay >= 3 {
					// Keep top border (line 0) and bottom border (last line).
					keep := maxOverlay - 2 // minus top and bottom borders
					if keep < 1 {
						keep = 1
					}
					trimmed := append([]string{ovLines[0]}, ovLines[1:1+keep]...)
					trimmed = append(trimmed, ovLines[len(ovLines)-1])
					overlayView = strings.Join(trimmed, "\n") + "\n"
					overlayLines = maxOverlay
				}
			}
			if len(lines) > overlayLines {
				vpView = strings.Join(lines[:len(lines)-overlayLines], "\n")
			}
		}

		content := vpView + "\n" +
			overlayView +
			m.input.View(m.width) + "\n" +
			m.status.View()

		// 确保总行数不超出终端高度，防止 Windows 终端上的渲染溢出问题。
		// 当内容超出时，从顶部裁剪（丢掉最早的输出行），并同步修正光标 Y 坐标。
		trimmedLines := 0
		if m.height > 0 {
			contentLines := strings.Split(content, "\n")
			if len(contentLines) > m.height {
				trimmedLines = len(contentLines) - m.height
				content = strings.Join(contentLines[trimmedLines:], "\n")
			}
		}

		v := tea.NewView(content)
	v.AltScreen = true
	v.KeyboardEnhancements.ReportEventTypes = true
	v.MouseMode = tea.MouseModeCellMotion

	// Declarative cursor from textarea for IME composition support.
	// Offset textarea-internal cursor to screen coordinates:
	//   X: border(1) = 1
	//   Y: viewport lines + separator(1) + overlay lines + input top border(1) - trimmed top lines
	if c := m.input.textarea.Cursor(); c != nil {
		c.Position.X += 1
		c.Position.Y += strings.Count(vpView, "\n") + overlayLines + 2 - trimmedLines
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
		if event.IsError {
			return "tool_error"
		}
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
