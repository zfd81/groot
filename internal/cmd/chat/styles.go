package chat

import "charm.land/lipgloss/v2"

var (
	// Status bar (no background, plain text)
	StatusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#888888"))

	// User message label
	UserStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#61afef")).
			Bold(true)

	// User message content (subtle background like Claude Code)
	UserContentStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ffffff")).
				Background(lipgloss.Color("#2c313a"))

	// User message full-line style (> + content, background spans full width)
	UserMessageStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("#ffffff")).
				Background(lipgloss.Color("#2c313a")).
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

	// Welcome screen ASCII art
	WelcomeStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#98c379"))

	// Viewport area
	ViewportStyle = lipgloss.NewStyle()

	// Loading indicator
	LoadingStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#98c379")).
			Italic(true)
)

// Pre-rendered label strings for reuse
var (
	ThinkingLabel   = ThinkingLabelStyle.Render("🤔 Thinking...")
	ToolCallPrefix  = ToolCallStyle.Render("🔧 调用工具: ")
	SkillCallPrefix = lipgloss.NewStyle().Foreground(lipgloss.Color("#c678dd")).Render("⚡ 调用技能: ")
	CancelLabel     = CancelStyle.Render("⏹️ 已取消")
	LengthLabel     = WarnStyle.Render("⚠️ 已达 token 上限")
	ErrorLabel      = ErrorStyle.Render("❌ 错误")
	LoadingText     = LoadingStyle.Render("正在思考...")
)
