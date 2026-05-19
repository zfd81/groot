# groot chat TUI Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Claude Code-style TUI chat interface using Bubble Tea, connected to groot API via HTTP+SSE, with auto-detection of running service.

**Architecture:** Bubble Tea Elm architecture with main Model holding 4 sub-components (StatusBar, Viewport, InputArea, CompletionPopup). SSE client goroutine sends parsed events as Bubble Tea messages via channel. Service connectivity auto-detected at startup — if no groot server is running, one is started embedded with stdout suppressed.

**Tech Stack:** Go 1.26, Bubble Tea, Bubbles (textarea, viewport), Lipgloss, existing groot internal packages (config, api, agent, logger, mcp, memory, skills, schedule)

---

## File Structure

```
New files:
internal/cmd/
├── chat.go              # subcommand entry: ParseChatFlags / RunChat
└── chat/
    ├── messages.go      # Bubble Tea message types + SSE event structs
    ├── styles.go        # Lipgloss style definitions
    ├── welcome.go       # welcome screen ASCII art
    ├── statusbar.go     # status bar component
    ├── viewport.go      # chat display viewport + message rendering
    ├── input.go         # textarea with ghost text support
    ├── completion.go    # completion popup with filtering
    ├── commands.go      # system command definitions + handlers
    ├── client.go        # HTTP + SSE client
    ├── model.go         # main Bubble Tea Model (Init/Update/View)
    ├── model_test.go    # unit tests
    ├── client_test.go   # client unit tests
    └── commands_test.go # command parsing tests

Modified files:
cmd/groot/main.go        # add "chat" case to subcommand switch
go.mod                   # add bubbletea, bubbles, lipgloss dependencies
```

---

### Task 1: Add Bubble Tea Dependencies

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add dependencies**

```bash
cd /Users/zhangfengda/workspace/groot
go get github.com/charmbracelet/bubbletea@latest
go get github.com/charmbracelet/bubbles@latest
go get github.com/charmbracelet/lipgloss@latest
go mod tidy
```

- [ ] **Step 2: Verify build still works**

```bash
go build ./...
```

Expected: builds successfully (dependencies added, no code changes yet).

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "chore: add Bubble Tea dependencies for chat TUI"
```

---

### Task 2: Create Message Types

**Files:**
- Create: `internal/cmd/chat/messages.go`

- [ ] **Step 1: Write messages.go**

```go
package chat

// SseEvent represents a parsed SSE data line from the chat stream.
// The API sends JSON events without SSE "event:" lines; type
// is determined by which JSON fields are present.
type SseEvent struct {
	Role         string     `json:"role"`
	Content      string     `json:"content,omitempty"`
	Reasoning    string     `json:"reasoning_content,omitempty"`
	ToolCalls    []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID   string     `json:"tool_call_id,omitempty"`
	ToolName     string     `json:"tool_name,omitempty"`
	FinishReason string     `json:"finish_reason,omitempty"`
	Event        string     `json:"event,omitempty"`
	Code         string     `json:"code,omitempty"`
	Message      string     `json:"message,omitempty"`
	// raw text for API responses that aren't SSE events (e.g. /tools, /skills)
	RawText string `json:"-"`
}

// ToolCall represents a tool call in OpenAI format
type ToolCall struct {
	ID       string       `json:"id"`
	Type     string       `json:"type"`
	Function FunctionCall `json:"function"`
}

// FunctionCall represents function call details
type FunctionCall struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// Bubble Tea message types (each wraps data to carry context)

// SseEventMsg carries a parsed SSE event from the stream goroutine
type SseEventMsg SseEvent

// SessionIDMsg carries the session ID extracted from X-Session-ID response header
type SessionIDMsg string

// StreamDoneMsg signals the SSE stream has ended normally
type StreamDoneMsg struct{}

// StreamErrorMsg signals an error during SSE streaming
type StreamErrorMsg struct{ Err error }

// ApiResponseMsg carries a non-stream API response (for /skills, /tools, /sess, etc.)
type ApiResponseMsg struct {
	Endpoint string
	Body     []byte
}

// CancelChatMsg signals a user-requested cancellation (ESC during streaming)
type CancelChatMsg struct{}

// CommandMsg carries a parsed system command from user input
type CommandMsg struct {
	Cmd  string // e.g. "/model"
	Args string // everything after the command, trimmed
}

// modelPopupMsg triggers the model selection popup
type modelPopupMsg struct {
	models []CompletionItem
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/cmd/chat/...
```

Expected: compiles successfully.

- [ ] **Step 3: Commit**

```bash
git add internal/cmd/chat/messages.go
git commit -m "feat(chat): add Bubble Tea message types for chat TUI"
```

---

### Task 3: Create Style Definitions

**Files:**
- Create: `internal/cmd/chat/styles.go`

- [ ] **Step 1: Write styles.go**

```go
package chat

import "github.com/charmbracelet/lipgloss"

var (
	// Status bar (dark background across full width)
	StatusBarStyle = lipgloss.NewStyle().
			Background(lipgloss.Color("#1a1a2e")).
			Foreground(lipgloss.Color("#e0e0e0")).
			Padding(0, 1)

	// User message label
	UserStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#61afef")).
			Bold(true)

	// Assistant message content
	AssistantStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#ffffff"))

	// Thinking phase label
	ThinkingLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#888888")).
				Italic(true)

	// Tool call label
	ToolCallStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#e5c07b"))

	// Tool result label
	ToolResultStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#98c379"))

	// Cancel / length warning / error
	CancelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888"))

	WarnStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#e5c07b"))

	ErrorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#e06c75"))

	// Completion popup border and items
	CompletionStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("#444444"))

	CompletionSelectedStyle = lipgloss.NewStyle().
				Background(lipgloss.Color("#444444")).
				Foreground(lipgloss.Color("#ffffff"))

	CompletionNormalStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#cccccc"))

	// Ghost text in input field
	GhostTextStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#666666"))

	// Input prompt symbol
	PromptStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#98c379")).
			Bold(true)

	// Viewport area
	ViewportStyle = lipgloss.NewStyle().
			Padding(0, 1)
)

// Pre-rendered label strings for reuse
var (
	ThinkingLabel  = ThinkingLabelStyle.Render("🤔 Thinking...")
	ToolCallPrefix = ToolCallStyle.Render("🔧 调用工具: ")
	ToolResultLabel = ToolResultStyle.Render("📋 工具结果:")
	CancelLabel    = CancelStyle.Render("⏹️ 已取消")
	LengthLabel    = WarnStyle.Render("⚠️ 已达 token 上限")
	ErrorLabel     = ErrorStyle.Render("❌ 错误")
)
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/cmd/chat/...
```

Expected: compiles successfully.

- [ ] **Step 3: Commit**

```bash
git add internal/cmd/chat/styles.go
git commit -m "feat(chat): add Lipgloss style definitions for chat TUI"
```

---

### Task 4: Create Welcome Screen

**Files:**
- Create: `internal/cmd/chat/welcome.go`

- [ ] **Step 1: Write welcome.go**

```go
package chat

// WelcomeScreen is the ASCII art displayed when the TUI first opens.
// It scrolls out of view after the first message is sent.
const WelcomeScreen = `
   ██████╗ ██████╗  ██████╗  ██████╗ ████████╗
  ██╔════╝ ██╔══██╗██╔═══██╗██╔═══██╗╚══██╔══╝
  ██║  ███╗██████╔╝██║   ██║██║   ██║   ██║
  ██║   ██║██╔══██╗██║   ██║██║   ██║   ██║
  ╚██████╔╝██║  ██║╚██████╔╝╚██████╔╝   ██║
   ╚═════╝ ╚═╝  ╚═╝ ╚═════╝  ╚═════╝    ╚═╝

        Groot AI Agent · v1.0.0
   ─────────────────────────────
   输入你的问题开始对话
   输入 /help 查看系统命令
`
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/cmd/chat/...
```

Expected: compiles successfully.

- [ ] **Step 3: Commit**

```bash
git add internal/cmd/chat/welcome.go
git commit -m "feat(chat): add welcome screen ASCII art"
```

---

### Task 5: Create Status Bar Component

**Files:**
- Create: `internal/cmd/chat/statusbar.go`

- [ ] **Step 1: Write statusbar.go**

```go
package chat

import "fmt"

// StatusBar holds and renders the top status bar state.
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

// View renders the status bar as a single line.
// Format:  🤖 <model>  │  📝 <session>  │  🔄 第 <n> 轮
func (s StatusBar) View() string {
	left := fmt.Sprintf(" 🤖 %s ", s.ModelName)
	mid := fmt.Sprintf(" 📝 %s ", s.SessionID)
	right := fmt.Sprintf(" 🔄 第 %d 轮 ", s.Round)

	// Calculate visible widths (strip ANSI escapes for measurement)
	lw := visibleWidth(left)
	mw := visibleWidth(mid)
	rw := visibleWidth(right)

	content := left
	content += mid
	// Right-align the round counter
	// Distribute remaining space between mid and right
	total := s.Width
	if total < lw+mw+rw+4 {
		total = lw + mw + rw + 4
	}
	pad := total - lw - mw - rw
	for i := 0; i < pad; i++ {
		content += " "
	}
	content += right

	return StatusBarStyle.Width(total).Render(content)
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
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/cmd/chat/...
```

Expected: compiles successfully.

- [ ] **Step 3: Commit**

```bash
git add internal/cmd/chat/statusbar.go
git commit -m "feat(chat): add status bar component"
```

---

### Task 6: Create Viewport Component

**Files:**
- Create: `internal/cmd/chat/viewport.go`

- [ ] **Step 1: Write viewport.go**

```go
package chat

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	"github.com/charmbracelet/lipgloss"
)

// ChatMessage represents a single rendered message block in the viewport.
type ChatMessage struct {
	Role        string // user, thinking, tool_call, tool_result, assistant, cancel, length, error, system
	Content     string
	Meta        string // tool name (for tool_call), unused otherwise
	Folded      bool
	FullContent string // unfolded content when Folded is true
}

// ViewportModel wraps bubbles viewport with message list management.
type ViewportModel struct {
	viewport viewport.Model
	messages []ChatMessage
}

// NewViewport creates a viewport pre-filled with the welcome screen.
func NewViewport(width, height int) ViewportModel {
	vp := viewport.New(width, height-4)
	vp.SetContent(WelcomeScreen)
	return ViewportModel{
		viewport: vp,
	}
}

// SetSize recalculates available viewport area.
func (v *ViewportModel) SetSize(width, height int) {
	v.viewport.Width = width
	v.viewport.Height = height - 4 // leave room for statusbar + input
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

// Clear resets to welcome screen.
func (v *ViewportModel) Clear() {
	v.messages = nil
	v.viewport.SetContent(WelcomeScreen)
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
	var sb strings.Builder
	for _, msg := range v.messages {
		switch msg.Role {
		case "user":
			sb.WriteString(UserStyle.Render("User: "))
			sb.WriteString(msg.Content)
			sb.WriteString("\n\n")
		case "thinking":
			sb.WriteString(ThinkingLabel)
			sb.WriteString("\n   ")
			sb.WriteString(msg.Content)
			sb.WriteString("\n\n")
		case "tool_call":
			sb.WriteString(ToolCallPrefix)
			sb.WriteString(msg.Meta)
			sb.WriteString("\n")
			if msg.Content != "" {
				sb.WriteString("   ")
				sb.WriteString(msg.Content)
				sb.WriteString("\n")
			}
			sb.WriteString("\n")
		case "tool_result":
			sb.WriteString(ToolResultLabel)
			sb.WriteString("\n   ")
			sb.WriteString(msg.Content)
			sb.WriteString("\n\n")
		case "assistant":
			sb.WriteString(lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ffffff")).
				Render(msg.Content))
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
			sb.WriteString(msg.Content)
			sb.WriteString("\n\n")
		case "system":
			sb.WriteString(msg.Content)
			sb.WriteString("\n\n")
		}
	}
	v.viewport.SetContent(sb.String())
	v.viewport.GotoBottom()
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/cmd/chat/...
```

Expected: compiles successfully.

- [ ] **Step 3: Commit**

```bash
git add internal/cmd/chat/viewport.go
git commit -m "feat(chat): add chat viewport component with message rendering"
```

---

### Task 7: Create Input Component

**Files:**
- Create: `internal/cmd/chat/input.go`

- [ ] **Step 1: Write input.go**

```go
package chat

import (
	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

// InputModel wraps bubbles textarea with ghost-text completion support.
type InputModel struct {
	textarea  textarea.Model
	ghostText string
}

// NewInput creates an input component with default settings.
func NewInput() InputModel {
	ta := textarea.New()
	ta.Placeholder = "输入消息，或 / 开头使用命令..."
	ta.ShowLineNumbers = false
	ta.CharLimit = 0
	ta.MaxHeight = 5
	ta.SetHeight(3)
	ta.Prompt = "> "

	// Enter sends, Alt+Enter inserts newline
	ta.KeyMap.InsertNewline = key.NewBinding(
		key.WithKeys("alt+enter"),
		key.WithHelp("Alt+Enter", "换行"),
	)

	return InputModel{textarea: ta}
}

// SetSize adjusts input width.
func (i *InputModel) SetSize(width int) {
	i.textarea.SetWidth(width - 2)
}

// SetGhostText sets the inline ghost completion text.
func (i *InputModel) SetGhostText(text string) {
	i.ghostText = text
}

// ClearGhostText removes ghost text.
func (i *InputModel) ClearGhostText() { i.ghostText = "" }

// HasGhostText reports whether ghost text is active.
func (i *InputModel) HasGhostText() bool { return i.ghostText != "" }

// Value returns the current textarea content.
func (i *InputModel) Value() string { return i.textarea.Value() }

// Reset clears both textarea and ghost text.
func (i *InputModel) Reset() {
	i.textarea.Reset()
	i.ghostText = ""
}

// AcceptGhostText inserts the ghost text at cursor and clears it.
func (i *InputModel) AcceptGhostText() {
	if i.ghostText == "" {
		return
	}
	val := i.textarea.Value()
	pos := i.textarea.Cursor()
	newVal := val[:pos] + i.ghostText + val[pos:]
	i.textarea.SetValue(newVal)
	i.textarea.CursorEnd()
	i.ghostText = ""
}

// Update delegates to the underlying textarea.
func (i InputModel) Update(msg tea.Msg) (InputModel, tea.Cmd) {
	var cmd tea.Cmd
	i.textarea, cmd = i.textarea.Update(msg)
	return i, cmd
}

// View renders the input area with ghost text appended.
func (i InputModel) View() string {
	view := i.textarea.View()
	if i.ghostText != "" {
		view += GhostTextStyle.Render(i.ghostText)
	}
	return view
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/cmd/chat/...
```

Expected: compiles successfully.

- [ ] **Step 3: Commit**

```bash
git add internal/cmd/chat/input.go
git commit -m "feat(chat): add input component with ghost text support"
```

---

### Task 8: Create Completion Popup

**Files:**
- Create: `internal/cmd/chat/completion.go`

- [ ] **Step 1: Write completion.go**

```go
package chat

import "strings"

// CompletionItem is a single entry in the completion popup.
type CompletionItem struct {
	Name        string
	Description string
}

// CompletionModel manages popup visibility, filtering, and selection.
type CompletionModel struct {
	visible   bool
	items     []CompletionItem
	filtered  []CompletionItem
	selected  int
	width     int
	maxItems  int
	ghostText string
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

	// Window visible items around selected
	start := c.selected - c.maxItems/2
	if start < 0 {
		start = 0
	}
	end := start + c.maxItems
	if end > len(c.filtered) {
		end = len(c.filtered)
	}

	var lines []string
	for i := start; i < end; i++ {
		item := c.filtered[i]
		line := item.Name + "  " + item.Description
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
		c.ghostText = sel.Name
	} else {
		c.ghostText = ""
	}
}

// ----- Pre-defined completion lists -----

// SystemCommands is the full list of system commands for auto-completion.
var SystemCommands = []CompletionItem{
	{Name: "/exit", Description: "退出聊天"},
	{Name: "/model", Description: "切换模型"},
	{Name: "/clear", Description: "清空对话"},
	{Name: "/help", Description: "显示帮助"},
	{Name: "/session", Description: "会话管理"},
	{Name: "/skills", Description: "查看已安装 skill"},
	{Name: "/mcp", Description: "查看可用工具"},
	{Name: "/config", Description: "查看配置（只读）"},
	{Name: "/export", Description: "导出对话"},
}

// SessionSubCommands are the sub-commands for /session.
var SessionSubCommands = []CompletionItem{
	{Name: "list", Description: "列出所有会话"},
	{Name: "switch", Description: "切换到指定会话"},
}

// ListOnlySubCommands is the shared sub-command list for /skills and /mcp.
var ListOnlySubCommands = []CompletionItem{
	{Name: "list", Description: "列出"},
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/cmd/chat/...
```

Expected: compiles successfully.

- [ ] **Step 3: Commit**

```bash
git add internal/cmd/chat/completion.go
git commit -m "feat(chat): add completion popup with filtering and ghost text"
```

---

### Task 9: Create HTTP+SSE Client

**Files:**
- Create: `internal/cmd/chat/client.go`

- [ ] **Step 1: Write client.go**

```go
package chat

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// Client communicates with the groot API over HTTP.
type Client struct {
	baseURL   string
	modelName string
	sessionID string
	httpCli   *http.Client
}

// NewClient creates a client targeting the given base URL with the default model.
func NewClient(baseURL, modelName string) *Client {
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		modelName: modelName,
		httpCli:   &http.Client{Timeout: 10 * time.Second},
	}
}

// SetSessionID stores the current session ID.
func (c *Client) SetSessionID(id string) { c.sessionID = id }

// SessionID returns the current session ID.
func (c *Client) SessionID() string { return c.sessionID }

// SetModel updates the model name sent in chat requests.
func (c *Client) SetModel(name string) { c.modelName = name }

// ModelName returns the current model name.
func (c *Client) ModelName() string { return c.modelName }

// HealthCheck tests whether the service is reachable.
func (c *Client) HealthCheck(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, "GET", c.baseURL+"/health", nil)
	if err != nil {
		return err
	}
	resp, err := c.httpCli.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("health returned %d", resp.StatusCode)
	}
	return nil
}

// SendChatStream starts an SSE streaming chat request in a goroutine.
// Parsed events are sent to `events`. Closing `cancelCh` aborts the request.
// The channel is closed when the stream ends.
func (c *Client) SendChatStream(instruction string, events chan<- tea.Msg, cancelCh <-chan struct{}) {
	go func() {
		defer close(events)

		body := map[string]interface{}{
			"instruction": instruction,
		}
		bodyBytes, _ := json.Marshal(body)

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		// Watch for cancellation
		go func() {
			<-cancelCh
			cancel()
			if c.sessionID != "" {
				req, _ := http.NewRequest("DELETE", c.baseURL+"/chat/"+c.sessionID, nil)
				c.httpCli.Do(req)
			}
		}()

		req, err := http.NewRequestWithContext(ctx, "POST", c.baseURL+"/chat", bytes.NewReader(bodyBytes))
		if err != nil {
			events <- StreamErrorMsg{Err: err}
			return
		}
		req.Header.Set("Content-Type", "application/json")
		if c.sessionID != "" {
			req.Header.Set("X-Session-ID", c.sessionID)
		}
		req.Header.Set("X-Model-Name", c.modelName)

		resp, err := c.httpCli.Do(req)
		if err != nil {
			events <- StreamErrorMsg{Err: fmt.Errorf("请求失败: %w", err)}
			return
		}
		defer resp.Body.Close()

		if resp.StatusCode >= 400 {
			b, _ := io.ReadAll(resp.Body)
			events <- StreamErrorMsg{Err: fmt.Errorf("API 错误 (%d): %s", resp.StatusCode, string(b))}
			return
		}

		// Capture session ID from response header (first message creates session)
		if sid := resp.Header.Get("X-Session-ID"); sid != "" && c.sessionID == "" {
			c.sessionID = sid
			events <- SessionIDMsg(sid)
		}

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 64*1024), 2*1024*1024)

		for scanner.Scan() {
			select {
			case <-ctx.Done():
				events <- StreamDoneMsg{}
				return
			default:
			}

			line := scanner.Text()
			if line == "" || strings.HasPrefix(line, ":") {
				continue
			}
			if !strings.HasPrefix(line, "data: ") {
				continue
			}

			data := strings.TrimPrefix(line, "data: ")
			if data == "[DONE]" {
				events <- StreamDoneMsg{}
				return
			}

			var event SseEvent
			if err := json.Unmarshal([]byte(data), &event); err != nil {
				continue
			}
			events <- SseEventMsg(event)
		}

		if err := scanner.Err(); err != nil {
			events <- StreamErrorMsg{Err: err}
			return
		}
		events <- StreamDoneMsg{}
	}()
}

// CancelChat sends a cancel request for the current session.
func (c *Client) CancelChat() error {
	if c.sessionID == "" {
		return nil
	}
	req, err := http.NewRequest("DELETE", c.baseURL+"/chat/"+c.sessionID, nil)
	if err != nil {
		return err
	}
	resp, err := c.httpCli.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// FetchJSON does a GET request and returns the parsed JSON body or raw bytes.
func (c *Client) FetchJSON(path string) ([]byte, error) {
	resp, err := c.httpCli.Get(c.baseURL + path)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/cmd/chat/...
```

Expected: compiles successfully.

- [ ] **Step 3: Commit**

```bash
git add internal/cmd/chat/client.go
git commit -m "feat(chat): add HTTP+SSE client for groot API communication"
```

---

### Task 10: Create System Command Handlers

**Files:**
- Create: `internal/cmd/chat/commands.go`

- [ ] **Step 1: Write commands.go**

```go
package chat

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zfd81/groot/internal/config"
)

// ParseCommand extracts a CommandMsg from input text if it starts with "/".
// Returns nil if the text is not a command.
func ParseCommand(input string) *CommandMsg {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "/") {
		return nil
	}
	parts := strings.SplitN(trimmed, " ", 2)
	cmd := parts[0]
	args := ""
	if len(parts) > 1 {
		args = strings.TrimSpace(parts[1])
	}
	return &CommandMsg{Cmd: cmd, Args: args}
}

// CommandResult tells the model what action to take after a command.
type CommandResult struct {
	Action  string // "quit", "clear", "render", "model_popup", "fetch", "export", "none"
	Content string // for "render": text to show in viewport
	API     string // for "fetch": API path to GET
}

// ExecuteCommand dispatches a parsed command to its handler.
func ExecuteCommand(msg CommandMsg, client *Client, configPath string) CommandResult {
	switch msg.Cmd {
	case "/exit":
		return CommandResult{Action: "quit"}
	case "/clear":
		return CommandResult{Action: "clear"}
	case "/help":
		return CommandResult{Action: "render", Content: HelpText}
	case "/model":
		if msg.Args != "" {
			// Direct model switch — validated by caller against config
			return CommandResult{Action: "switch_model", Content: msg.Args}
		}
		return CommandResult{Action: "model_popup"}
	case "/session":
		return handleSession(msg.Args)
	case "/skills":
		return CommandResult{Action: "fetch", API: "/skills"}
	case "/mcp":
		return CommandResult{Action: "fetch", API: "/tools"}
	case "/config":
		return handleConfig(configPath)
	case "/export":
		return CommandResult{Action: "export"}
	default:
		return CommandResult{Action: "render",
			Content: fmt.Sprintf("未知命令: %s\n输入 /help 查看可用命令", msg.Cmd)}
	}
}

func handleSession(args string) CommandResult {
	parts := strings.SplitN(args, " ", 2)
	sub := ""
	sid := ""
	if len(parts) > 0 {
		sub = parts[0]
	}
	if len(parts) > 1 {
		sid = parts[1]
	}

	switch sub {
	case "list":
		return CommandResult{Action: "fetch", API: "/sess/history"}
	case "switch":
		if sid == "" {
			return CommandResult{Action: "render", Content: "用法: /session switch <session_id>"}
		}
		return CommandResult{Action: "fetch", API: "/sess/" + sid}
	default:
		return CommandResult{Action: "render",
			Content: "用法: /session list | /session switch <id>"}
	}
}

func handleConfig(configPath string) CommandResult {
	homeDir := filepath.Dir(configPath)
	cfg, err := config.Load(homeDir)
	if err != nil {
		return CommandResult{Action: "render",
			Content: fmt.Sprintf("读取配置失败: %v", err)}
	}

	var sb strings.Builder
	sb.WriteString("## 当前配置\n\n")
	sb.WriteString(fmt.Sprintf("- **默认模型**: %s\n", cfg.LLM.DefaultModel))
	sb.WriteString(fmt.Sprintf("- **服务端口**: %d\n", cfg.Server.Port))
	sb.WriteString(fmt.Sprintf("- **日志级别**: %s\n", cfg.Logging.Level))
	sb.WriteString(fmt.Sprintf("- **Memory 保留天数**: %d\n", cfg.Memory.MaxHistoryDays))
	sb.WriteString(fmt.Sprintf("- **Skills 目录**: %s\n", cfg.Skills.Dir))
	sb.WriteString(fmt.Sprintf("- **MCP 目录**: %s\n", cfg.MCP.Dir))
	sb.WriteString("\n### 模型列表\n\n")

	for name, model := range cfg.LLM.Models {
		apiKey := maskAPIKey(model.APIKey)
		marker := ""
		if name == cfg.LLM.DefaultModel {
			marker = " **(默认)**"
		}
		sb.WriteString(fmt.Sprintf("**%s**%s\n", name, marker))
		sb.WriteString(fmt.Sprintf("  - Model: %s\n", model.Model))
		sb.WriteString(fmt.Sprintf("  - Base URL: %s\n", model.BaseURL))
		sb.WriteString(fmt.Sprintf("  - API Key: %s\n", apiKey))
		if model.Temperature != 0 {
			sb.WriteString(fmt.Sprintf("  - Temperature: %.2f\n", model.Temperature))
		}
		if model.MaxCompletionTokens != 0 {
			sb.WriteString(fmt.Sprintf("  - Max Tokens: %d\n", model.MaxCompletionTokens))
		}
	}

	return CommandResult{Action: "render", Content: sb.String()}
}

// maskAPIKey hides the middle of API keys.
// Shows first 3 and last 3 chars for keys >= 7 chars.
// Preserves env-var references like ${VAR_NAME}.
func maskAPIKey(key string) string {
	if key == "" {
		return "(未设置)"
	}
	if strings.HasPrefix(key, "${") && strings.HasSuffix(key, "}") {
		return key // env var reference, shown as-is
	}
	if len(key) <= 6 {
		return strings.Repeat("*", len(key))
	}
	return key[:3] + "..." + key[len(key)-3:]
}

// ExportToMarkdown writes session data to ~/.groot/exports/chat-<id>.md.
func ExportToMarkdown(body []byte) (string, error) {
	homeDir, _ := os.UserHomeDir()
	exportDir := filepath.Join(homeDir, ".groot", "exports")
	os.MkdirAll(exportDir, 0755)

	// Try to parse as JSON and build markdown
	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
		// Not JSON, write raw
		filename := filepath.Join(exportDir, "chat-export.md")
		return filename, os.WriteFile(filename, body, 0644)
	}

	var sb strings.Builder
	sb.WriteString("# Groot Chat Export\n\n")

	if session, ok := data["session"].(map[string]interface{}); ok {
		sb.WriteString(fmt.Sprintf("**会话 ID**: %v\n", session["session_id"]))
		sb.WriteString(fmt.Sprintf("**创建时间**: %v\n", session["created_at"]))
		sb.WriteString(fmt.Sprintf("**轮数**: %v\n\n", session["round_count"]))
		sb.WriteString("---\n\n")
	}

	if history, ok := data["history"].(map[string]interface{}); ok {
		if messages, ok := history["messages"].([]interface{}); ok {
			for _, m := range messages {
				if msg, ok := m.(map[string]interface{}); ok {
					role := fmt.Sprintf("%v", msg["role"])
					content := fmt.Sprintf("%v", msg["content"])
					sb.WriteString(fmt.Sprintf("### %s\n\n%s\n\n", role, content))
				}
			}
		}
	}

	var sessionID string
	if session, ok := data["session"].(map[string]interface{}); ok {
		sessionID = fmt.Sprintf("%v", session["session_id"])
	} else {
		sessionID = "unknown"
	}
	filename := filepath.Join(exportDir, fmt.Sprintf("chat-%s.md", sessionID))
	if err := os.WriteFile(filename, []byte(sb.String()), 0644); err != nil {
		return "", err
	}
	return filename, nil
}

// HelpText is shown when the user types /help.
const HelpText = `## 系统命令

| 命令 | 参数 | 功能 |
|------|------|------|
| /exit | 无 | 退出 TUI |
| /model | [model_name] | 切换模型，无参数弹出选择列表 |
| /clear | 无 | 开始新对话 |
| /help | 无 | 显示本帮助 |
| /session | list / switch <id> | 会话管理 |
| /skills | list | 查看已安装 skill |
| /mcp | list | 查看可用工具 |
| /config | 无 | 查看配置（只读，密钥已脱敏） |
| /export | 无 | 导出当前对话为 Markdown |

## 快捷键

| 按键 | 行为 |
|------|------|
| Enter | 发送消息 |
| Alt+Enter | 插入换行 |
| Tab | 接受补全 / 切换下一项 |
| ESC | 取消 / 关闭补全 / 清空输入 |
| Ctrl+C | 退出 TUI |
`
```

- [ ] **Step 2: Add missing imports**

The file needs `encoding/json` for `ExportToMarkdown` and `strings`. Add to import header:

```go
import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/zfd81/groot/internal/config"
)
```

- [ ] **Step 3: Verify compilation**

```bash
go build ./internal/cmd/chat/...
```

Expected: compiles successfully.

- [ ] **Step 4: Commit**

```bash
git add internal/cmd/chat/commands.go
git commit -m "feat(chat): add system command handlers and /config /help /export"
```

---

### Task 11: Create Main Bubble Tea Model

**Files:**
- Create: `internal/cmd/chat/model.go`

- [ ] **Step 1: Write model.go**

```go
package chat

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/bubbles/textarea"

	"github.com/zfd81/groot/internal/config"
)

// Model is the top-level Bubble Tea model holding all sub-components and state.
type Model struct {
	width  int
	height int

	status     StatusBar
	viewport   ViewportModel
	input      InputModel
	completion CompletionModel

	client     *Client
	config     *config.Config
	configPath string

	streaming    bool
	cancelCh     chan struct{}
	eventsCh     chan tea.Msg
	sessionInit  bool

	// Embed server (only populated in embed mode)
	embedServer interface{ Shutdown() error }
	embedMode   bool
}

// NewModel creates a fully initialized TUI model.
func NewModel(cfg *config.Config, configPath string, baseURL string) Model {
	client := NewClient(baseURL, cfg.LLM.DefaultModel)
	status := NewStatusBar(cfg.LLM.DefaultModel)

	// Default terminal size (will be updated by WindowSizeMsg on first render)
	width := 80
	height := 24

	vp := NewViewport(width, height)
	input := NewInput()
	input.SetSize(width)
	completion := NewCompletion()
	completion.SetWidth(width - 2)

	return Model{
		status:     status,
		viewport:   vp,
		input:      input,
		completion: completion,
		client:     client,
		config:     cfg,
		configPath: configPath,
	}
}

// SetEmbedServer stores an embedded server reference for cleanup on exit.
func (m *Model) SetEmbedServer(srv interface{ Shutdown() error }) {
	m.embedServer = srv
	m.embedMode = true
}

// Init implements tea.Model. Starts the textarea blink and enters alt screen.
func (m Model) Init() tea.Cmd {
	return tea.Batch(
		textarea.Blink,
		tea.EnterAltScreen,
	)
}

// Update implements tea.Model. Central event dispatcher.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.SetSize(msg.Width, msg.Height)
		m.input.SetSize(msg.Width)
		m.completion.SetWidth(msg.Width - 2)
		m.status.Width = msg.Width
		return m, nil

	case tea.KeyMsg:
		return m.handleKeyMsg(msg)

	case SseEventMsg:
		return m.handleSseEvent(SseEvent(msg))

	case SessionIDMsg:
		m.client.SetSessionID(string(msg))
		sid := string(msg)
		if len(sid) > 12 {
			sid = sid[:12] + "..."
		}
		m.status.SessionID = sid
		m.sessionInit = true
		return m, nil

	case StreamDoneMsg:
		m.streaming = false
		return m, nil

	case StreamErrorMsg:
		m.streaming = false
		m.viewport.AddMessage(ChatMessage{
			Role: "error", Content: msg.Err.Error(),
		})
		return m, nil

	case modelPopupMsg:
		m.completion.Show(msg.models)
		return m, nil
	}

	// Delegate text input to textarea when not in completion mode
	if !m.completion.IsVisible() {
		newInput, cmd := m.input.Update(msg)
		m.input = newInput
		return m, tea.Batch(cmd)
	}
	return m, nil
}

// handleKeyMsg processes keyboard input.
func (m Model) handleKeyMsg(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {

	case tea.KeyCtrlC:
		return m, tea.Quit

	case tea.KeyEsc:
		// Priority: completion popup > streaming cancel > clear input
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

	case tea.KeyUp:
		if m.completion.IsVisible() {
			m.completion.SelectPrev()
			m.input.SetGhostText(m.completion.GhostText())
			return m, nil
		}

	case tea.KeyDown:
		if m.completion.IsVisible() {
			m.completion.SelectNext()
			m.input.SetGhostText(m.completion.GhostText())
			return m, nil
		}

	case tea.KeyTab:
		if m.completion.IsVisible() {
			m.completion.SelectNext()
			m.input.SetGhostText(m.completion.GhostText())
			return m, nil
		}

	case tea.KeyEnter:
		if msg.Alt {
			return m, nil // let textarea insert newline
		}
		if m.completion.IsVisible() {
			m.input.AcceptGhostText()
			m.completion.Hide()
			return m, nil
		}
		if m.streaming {
			return m, nil
		}
		return m.handleSendMessage()
	}

	// After any key, re-evaluate completion
	return m.checkCompletion()
}

// handleSendMessage processes the input text as either a command or chat message.
func (m Model) handleSendMessage() (tea.Model, tea.Cmd) {
	text := m.input.Value()
	if text == "" {
		return m, nil
	}
	m.input.Reset()

	// Route as command
	if cmdMsg := ParseCommand(text); cmdMsg != nil {
		return m.handleCommand(*cmdMsg)
	}

	// Route as chat message
	if !m.sessionInit {
		// First message: clear welcome screen
		m.viewport.messages = nil
		m.viewport.viewport.SetContent("")
	}

	m.viewport.AddMessage(ChatMessage{Role: "user", Content: text})

	m.streaming = true
	m.cancelCh = make(chan struct{})
	m.eventsCh = make(chan tea.Msg, 100)
	m.client.SendChatStream(text, m.eventsCh, m.cancelCh)
	m.status.Round++

	return m, m.waitForEvents()
}

// handleCommand executes a system command.
func (m Model) handleCommand(msg CommandMsg) (tea.Model, tea.Cmd) {
	result := ExecuteCommand(msg, m.client, m.configPath)

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
		return m, nil

	case "switch_model":
		name := result.Content
		if _, ok := m.config.LLM.Models[name]; !ok {
			// Invalid model name — show popup instead
			items := make([]CompletionItem, 0, len(m.config.LLM.Models))
			for n := range m.config.LLM.Models {
				items = append(items, CompletionItem{Name: n})
			}
			m.completion.Show(items)
			return m, nil
		}
		m.client.SetModel(name)
		m.status.ModelName = name
		return m, nil

		// NOTE: 后续变更 (2026-05-19) — 模型列表数据源已从 m.config.LLM.Models（本地配置文件）
		// 改为通过 GET /models API 获取，模型列表缓存在 Model.availableModels 字段中。
		// model_popup、switch_model 和 checkCompletion 中对应的读取逻辑已改为使用 API 数据。

	case "fetch":
		return m, m.doFetchAPI(result.API)

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

		// Special handling for export
		if strings.Contains(path, "/sess/") && !strings.Contains(path, "/history") {
			filename, err := ExportToMarkdown(body)
			if err != nil {
				return StreamErrorMsg{Err: fmt.Errorf("导出失败: %w", err)}
			}
			return SseEventMsg{
				Event:   "api_response",
				Content: fmt.Sprintf("对话已导出到: %s", filename),
			}
		}

		// Render API response as formatted JSON
		var pretty map[string]interface{}
		if json.Unmarshal(body, &pretty) == nil {
			formatted := formatAPIResponse(pretty)
			return SseEventMsg{Event: "api_response", Content: formatted}
		}
		return SseEventMsg{Event: "api_response", Content: string(body)}
	}
}

// handleSseEvent routes a parsed SSE event to the viewport.
func (m Model) handleSseEvent(event SseEvent) (tea.Model, tea.Cmd) {
	// API responses (non-stream) handled separately
	if event.Event == "api_response" {
		m.viewport.AddMessage(ChatMessage{Role: "system", Content: event.Content})
		return m, nil
	}

	eType := classifyEvent(event)

	switch eType {
	case "thinking":
		// Check if last message is already a thinking block — append, else create
		if len(m.viewport.messages) > 0 &&
			m.viewport.messages[len(m.viewport.messages)-1].Role == "thinking" {
			m.viewport.UpdateLastMessage(event.Reasoning)
		} else {
			m.viewport.AddMessage(ChatMessage{Role: "thinking", Content: event.Reasoning})
		}

	case "tool_calls":
		for _, tc := range event.ToolCalls {
			m.viewport.AddMessage(ChatMessage{
				Role:    "tool_call",
				Meta:    tc.Function.Name,
				Content: tc.Function.Arguments,
			})
		}

	case "tool_result":
		content := event.Content
		if len(content) > 200 {
			content = content[:200] + "\n[... 展开]"
		}
		m.viewport.AddMessage(ChatMessage{Role: "tool_result", Content: content})

	case "message":
		// Check if last message is already assistant — append, else create
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
		m.viewport.AddMessage(ChatMessage{Role: "error", Content: event.Message})
	}

	return m, nil
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

	// Route to the right completion list based on command prefix
	switch {
	case strings.HasPrefix(val, "/model "):
		models := make([]CompletionItem, 0, len(m.config.LLM.Models))
		for name := range m.config.LLM.Models {
			models = append(models, CompletionItem{Name: name})
		}
		m.completion.Show(models)
		m.completion.Filter(strings.TrimPrefix(val, "/model "))

	case strings.HasPrefix(val, "/session "):
		m.completion.Show(SessionSubCommands)
		m.completion.Filter(strings.TrimPrefix(val, "/session "))

	case strings.HasPrefix(val, "/skills ") || strings.HasPrefix(val, "/mcp "):
		m.completion.Show(ListOnlySubCommands)
		m.completion.Filter(strings.TrimPrefix(strings.SplitN(val, " ", 2)[0]+" ", val+" "))

	default:
		m.completion.Show(SystemCommands)
		m.completion.Filter(val)
	}

	if m.completion.IsVisible() {
		m.input.SetGhostText(m.completion.GhostText())
	}
	return m, nil
}

// waitForEvents returns a command that reads the next event from the SSE channel.
// It polls with a short timeout so the event loop stays responsive.
func (m Model) waitForEvents() tea.Cmd {
	return func() tea.Msg {
		select {
		case event, ok := <-m.eventsCh:
			if !ok {
				return StreamDoneMsg{}
			}
			return event
		case <-time.After(50 * time.Millisecond):
			// No event yet; re-enter the event loop to check again
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
func (m Model) View() string {
	statusView := m.status.View()

	completionView := ""
	if m.completion.IsVisible() {
		completionView = m.completion.View()
		if completionView != "" {
			completionView += "\n"
		}
	}

	// Layout: statusbar → viewport → completion → input
	return statusView + "\n" +
		m.viewport.View() + "\n" +
		completionView +
		m.input.View()
}

// classifyEvent determines the event type from JSON fields.
// Matches the SSE format from internal/agent/sse.go.
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
	// Use simple key-value formatting for common API responses
	var sb strings.Builder

	if status, ok := data["status"]; ok {
		sb.WriteString(fmt.Sprintf("**状态**: %v\n\n", status))
	}

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

	// Fallback: show as indented key-value pairs
	for k, v := range data {
		if k == "status" || k == "total" || k == "limit" || k == "offset" {
			continue
		}
		sb.WriteString(fmt.Sprintf("**%s**:\n```\n%v\n```\n\n", k, v))
	}
	return sb.String()
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/cmd/chat/...
```

Expected: compiles successfully. Fix any issues:
- `model.go` must import `"encoding/json"` (already used in `doFetchAPI`)
- `model.go` must import `"fmt"` (already used)
- `model.go` must import `"strings"`
- `model.go` must import `"time"`
- `model.go` must import `tea "github.com/charmbracelet/bubbletea"`
- `model.go` must import `"github.com/charmbracelet/bubbles/textarea"`
- `model.go` must import `"github.com/zfd81/groot/internal/config"`

- [ ] **Step 3: Commit**

```bash
git add internal/cmd/chat/model.go
git commit -m "feat(chat): add main Bubble Tea model with event routing"
```

---

### Task 12: Create Subcommand Entry Point

**Files:**
- Create: `internal/cmd/chat.go`

- [ ] **Step 1: Write chat.go**

```go
package cmd

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/zfd81/groot/internal/api"
	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/cmd/chat"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/grootmd"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/mcp"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/message"
	"github.com/zfd81/groot/internal/schedule"
)

// RunChat starts the chat TUI.
func RunChat() error {
	homeDir := getHomeDir()

	// 1. Load config
	cfg, err := config.Load(homeDir)
	if err != nil {
		return fmt.Errorf("配置文件不存在，请先执行 groot init 初始化配置")
	}
	configPath := homeDir + "/config.yaml"

	baseURL := fmt.Sprintf("http://localhost:%d", cfg.Server.Port)

	// 2. Check if service is running
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	serviceRunning := checkService(ctx, baseURL)

	// 3. Start embedded service if needed
	if serviceRunning {
		fmt.Printf("检测到已有服务运行 (端口 %d)\n", cfg.Server.Port)
	} else {
		fmt.Println("未检测到运行中的服务，正在启动嵌入服务...")
		srv, err := startEmbedServer(homeDir, configPath, cfg)
		if err != nil {
			return fmt.Errorf("无法启动嵌入服务: %w", err)
		}
		defer srv.Shutdown()
		fmt.Printf("嵌入服务已启动 (端口 %d)\n", cfg.Server.Port)
	}

	// 4. Create and run TUI
	model := chat.NewModel(cfg, configPath, baseURL)

	p := tea.NewProgram(model, tea.WithAltScreen())
	if _, err := p.Run(); err != nil {
		return fmt.Errorf("TUI 运行错误: %w", err)
	}

	if !serviceRunning {
		fmt.Println("嵌入服务已关闭")
	}
	return nil
}

func checkService(ctx context.Context, baseURL string) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", baseURL+"/health", nil)
	if err != nil {
		return false
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	return resp.StatusCode == 200
}

// startEmbedServer bootstraps a full groot API server in-process.
// Mirrors cmd/groot/main.go startServer() with stdout logging suppressed.
func startEmbedServer(homeDir, configPath string, cfg *config.Config) (*api.Server, error) {
	// Suppress stdout: override logging output to file-only
	logCfg := cfg.Logging
	logCfg.Output = []string{"file"}
	log := logger.New(logCfg)

	// Resolve directories
	memoryDir := config.ResolvePath(homeDir, cfg.Memory.Directory)
	logDir := config.ResolvePath(homeDir, cfg.Logging.File.Directory)
	skillsDir := config.ResolvePath(homeDir, cfg.Skills.Dir)
	mcpDir := config.ResolvePath(homeDir, cfg.MCP.Dir)

	os.MkdirAll(memoryDir, 0755)
	os.MkdirAll(logDir, 0755)
	os.MkdirAll(skillsDir, 0755)
	os.MkdirAll(mcpDir, 0755)

	// Skills backend
	skillBackend := newSkillBackend(homeDir, skillsDir, log)

	// MCP manager
	mcpMgr, err := mcp.NewManager(mcpDir, log)
	if err != nil {
		return nil, fmt.Errorf("无法创建 MCP 管理器: %w", err)
	}

	// Memory manager
	mem, err := memory.NewManager(memoryDir, cfg.Memory)
	if err != nil {
		return nil, fmt.Errorf("无法创建 Memory 管理器: %w", err)
	}

	// Runtime state
	runtime := agent.NewRuntimeState()

	// Message layer
	msgLayer := message.NewLayer(cfg.Message, log)

	// Agent executor
	exec := agent.NewExecutor(mem, mcpMgr, runtime, msgLayer, cfg.Agent, log)

	// Schedule engine
	var scheduleMgr *schedule.Manager
	if cfg.Schedule.Enabled {
		scheduleMgr, _ = schedule.NewManager(
			config.ResolvePath(homeDir, cfg.Schedule.Dir),
			exec,
			log,
		)
	}

	// GROOT.md
	grootMDFile := homeDir + "/GROOT.md"
	if _, err := os.Stat(grootMDFile); os.IsNotExist(err) {
		grootMDFile = ""
	}

	// Create server (pass nil for skill middleware — not needed for chat)
	srv := api.NewServer(
		*cfg, homeDir, memoryDir, log, mem, runtime,
		skillBackend, nil, mcpMgr, exec, scheduleMgr,
	)

	if err := srv.Start(); err != nil {
		return nil, fmt.Errorf("服务启动失败: %w", err)
	}

	// Wait for health check
	if err := waitForHealth(fmt.Sprintf("http://localhost:%d", cfg.Server.Port), 10*time.Second); err != nil {
		srv.Shutdown()
		return nil, fmt.Errorf("嵌入服务启动超时: %w", err)
	}

	return srv, nil
}

func waitForHealth(baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == 200 {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("health 检查超时")
}

func newSkillBackend(homeDir, skillsDir string, log *logger.Logger) interface{} {
	// Create symlink-aware filesystem backend for skills
	// Simplified: use local backend if available
	_ = homeDir
	_ = skillsDir
	_ = log
	return nil
}
```

- [ ] **Step 2: Verify compilation**

```bash
go build ./internal/cmd/...
```

Expected: Some compilation errors. The `newSkillBackend` function and the `api.NewServer` call need to match actual signatures. Read the actual `internal/api/server.go` to get parameter types right, then fix.

This step requires checking actual function signatures and type names from the codebase. Key things to verify:
- `api.NewServer` parameter list and types
- `memory.NewManager` signature
- `mcp.NewManager` signature
- `agent.NewExecutor` signature
- `agent.NewRuntimeState` signature  
- `message.NewLayer` signature
- Skills backend creation (from `internal/filesystem/`)

Fix any type mismatches before committing.

- [ ] **Step 3: Commit**

```bash
git add internal/cmd/chat.go
git commit -m "feat(chat): add chat subcommand entry point with embed server support"
```

---

### Task 13: Wire Chat Subcommand into main.go

**Files:**
- Modify: `cmd/groot/main.go`

- [ ] **Step 1: Add "chat" case to the subcommand switch**

In `cmd/groot/main.go`, find the subcommand dispatch (around line 78-88) and add a case for "chat":

```go
case "chat":
    if err := cmd.RunChat(); err != nil {
        fmt.Fprintf(os.Stderr, "错误: %v\n", err)
        os.Exit(1)
    }
    return
```

- [ ] **Step 2: Add cmd import**

Ensure `main.go` has the import for `cmd` package (it already does — verify).

- [ ] **Step 3: Verify build**

```bash
go build -o bin/groot ./cmd
```

Expected: builds successfully.

- [ ] **Step 4: Verify help output**

```bash
./bin/groot --help
```

Expected: help should show `chat` as an available subcommand.

- [ ] **Step 5: Commit**

```bash
git add cmd/groot/main.go
git commit -m "feat(chat): wire chat subcommand into main.go"
```

---

### Task 14: Write Unit Tests — Message Types and Command Parsing

**Files:**
- Create: `internal/cmd/chat/commands_test.go`

- [ ] **Step 1: Write commands_test.go**

```go
package chat

import (
	"testing"
)

func TestParseCommand(t *testing.T) {
	tests := []struct {
		input string
		want  *CommandMsg
	}{
		{"/exit", &CommandMsg{Cmd: "/exit", Args: ""}},
		{"/model gpt-4o", &CommandMsg{Cmd: "/model", Args: "gpt-4o"}},
		{"/session list", &CommandMsg{Cmd: "/session", Args: "list"}},
		{"/session switch abc123", &CommandMsg{Cmd: "/session", Args: "switch abc123"}},
		{"/skills list", &CommandMsg{Cmd: "/skills", Args: "list"}},
		{"/mcp list", &CommandMsg{Cmd: "/mcp", Args: "list"}},
		{"hello world", nil},
		{"", nil},
		{"  /exit  ", &CommandMsg{Cmd: "/exit", Args: ""}},
	}

	for _, tt := range tests {
		got := ParseCommand(tt.input)
		if tt.want == nil && got != nil {
			t.Errorf("ParseCommand(%q) = %v, want nil", tt.input, got)
			continue
		}
		if tt.want != nil && got == nil {
			t.Errorf("ParseCommand(%q) = nil, want %v", tt.input, tt.want)
			continue
		}
		if got != nil {
			if got.Cmd != tt.want.Cmd {
				t.Errorf("ParseCommand(%q).Cmd = %q, want %q", tt.input, got.Cmd, tt.want.Cmd)
			}
			if got.Args != tt.want.Args {
				t.Errorf("ParseCommand(%q).Args = %q, want %q", tt.input, got.Args, tt.want.Args)
			}
		}
	}
}

func TestExecuteCommandRouting(t *testing.T) {
	tests := []struct {
		msg  CommandMsg
		want string // Action field
	}{
		{CommandMsg{Cmd: "/exit"}, "quit"},
		{CommandMsg{Cmd: "/clear"}, "clear"},
		{CommandMsg{Cmd: "/help"}, "render"},
		{CommandMsg{Cmd: "/model"}, "model_popup"},
		{CommandMsg{Cmd: "/model", Args: "gpt-4o"}, "switch_model"},
		{CommandMsg{Cmd: "/session", Args: "list"}, "fetch"},
		{CommandMsg{Cmd: "/session", Args: "switch abc"}, "fetch"},
		{CommandMsg{Cmd: "/session"}, "render"}, // usage hint
		{CommandMsg{Cmd: "/skills", Args: "list"}, "fetch"},
		{CommandMsg{Cmd: "/mcp", Args: "list"}, "fetch"},
		{CommandMsg{Cmd: "/config"}, "render"},
		{CommandMsg{Cmd: "/export"}, "export"},
		{CommandMsg{Cmd: "/unknown"}, "render"}, // error hint
	}

	for _, tt := range tests {
		client := NewClient("http://localhost:8080", "gpt-4o")
		result := ExecuteCommand(tt.msg, client, "/tmp/config.yaml")
		if result.Action != tt.want {
			t.Errorf("ExecuteCommand(%v).Action = %q, want %q", tt.msg, result.Action, tt.want)
		}
	}
}

func TestMaskAPIKey(t *testing.T) {
	tests := []struct {
		key  string
		want string
	}{
		{"", "(未设置)"},
		{"${OPENAI_API_KEY}", "${OPENAI_API_KEY}"},
		{"sk-abc123def456", "sk-...456"},
		{"short", "*****"},
		{"abcdefg", "abc...efg"},
	}

	for _, tt := range tests {
		got := maskAPIKey(tt.key)
		if got != tt.want {
			t.Errorf("maskAPIKey(%q) = %q, want %q", tt.key, got, tt.want)
		}
	}
}
```

- [ ] **Step 2: Run tests**

```bash
go test ./internal/cmd/chat/... -v -run "TestParseCommand|TestExecuteCommandRouting|TestMaskAPIKey"
```

Expected: all tests pass.

- [ ] **Step 3: Commit**

```bash
git add internal/cmd/chat/commands_test.go
git commit -m "test(chat): add unit tests for command parsing and routing"
```

---

### Task 15: Write Unit Tests — SSE Classification and Client

**Files:**
- Create: `internal/cmd/chat/client_test.go`

- [ ] **Step 1: Write client_test.go**

```go
package chat

import (
	"testing"
)

func TestClassifyEvent(t *testing.T) {
	tests := []struct {
		name  string
		event SseEvent
		want  string
	}{
		{
			name:  "thinking",
			event: SseEvent{Reasoning: "Let me think..."},
			want:  "thinking",
		},
		{
			name:  "tool_calls",
			event: SseEvent{ToolCalls: []ToolCall{{ID: "1", Function: FunctionCall{Name: "read"}}}},
			want:  "tool_calls",
		},
		{
			name:  "tool_result",
			event: SseEvent{Role: "tool", ToolName: "read", Content: "file content"},
			want:  "tool_result",
		},
		{
			name:  "message",
			event: SseEvent{Content: "Hello!"},
			want:  "message",
		},
		{
			name:  "finish_reason",
			event: SseEvent{FinishReason: "stop"},
			want:  "finish_reason",
		},
		{
			name:  "error",
			event: SseEvent{Event: "error", Message: "something went wrong"},
			want:  "error",
		},
		{
			name:  "thinking over message",
			event: SseEvent{Reasoning: "thinking...", Content: "text"},
			want:  "thinking",
		},
	}

	for _, tt := range tests {
		got := classifyEvent(tt.event)
		if got != tt.want {
			t.Errorf("classifyEvent(%s) = %q, want %q", tt.name, got, tt.want)
		}
	}
}

func TestNewClientDefaults(t *testing.T) {
	client := NewClient("http://localhost:8080/", "gpt-4o")
	if client.baseURL != "http://localhost:8080" {
		t.Errorf("baseURL = %q, want 'http://localhost:8080'", client.baseURL)
	}
	if client.modelName != "gpt-4o" {
		t.Errorf("modelName = %q, want 'gpt-4o'", client.modelName)
	}
}
```

- [ ] **Step 2: Run tests**

```bash
go test ./internal/cmd/chat/... -v -run "TestClassifyEvent|TestNewClientDefaults"
```

Expected: all tests pass.

- [ ] **Step 3: Commit**

```bash
git add internal/cmd/chat/client_test.go
git commit -m "test(chat): add unit tests for SSE classification and client"
```

---

### Task 16: Write Unit Tests — Built-in Types

**Files:**
- Create: `internal/cmd/chat/model_test.go`

- [ ] **Step 1: Write model_test.go**

```go
package chat

import (
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
	// Should match /model
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
	// Should wrap to 0
	if cm.selected != 0 {
		t.Errorf("After wrapping, selected = %d, want 0", cm.selected)
	}

	cm.SelectPrev()
	// Should wrap to last (2)
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
```

- [ ] **Step 2: Run tests**

```bash
go test ./internal/cmd/chat/... -v -run "TestStatusBarView|TestCompletion|TestVisibleWidth"
```

Expected: all tests pass.

- [ ] **Step 3: Commit**

```bash
git add internal/cmd/chat/model_test.go
git commit -m "test(chat): add unit tests for status bar and completion components"
```

---

### Task 17: Build and Integration Check

**Files:**
- Build: `bin/groot`

- [ ] **Step 1: Full build**

```bash
cd /Users/zhangfengda/workspace/groot
go build -o bin/groot ./cmd
```

Expected: builds without errors.

- [ ] **Step 2: Verify subcommand is listed**

```bash
./bin/groot --help
```

Expected: `chat` appears in the available subcommands.

- [ ] **Step 3: Run all tests**

```bash
go test ./internal/cmd/chat/... -v
```

Expected: all tests pass with no failures.

- [ ] **Step 4: Commit**

```bash
git add bin/  # if you want to track the binary
git commit -m "build: add groot binary with chat TUI support"
```
