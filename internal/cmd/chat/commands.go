package chat

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
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
	Action  string // "quit", "clear", "render", "help_popup", "model_popup", "skills_popup", "switch_model", "fetch", "export", "none"
	Content string // for "render": text to show in viewport
	API     string // for "fetch": API path to GET
}

// ExecuteCommand dispatches a parsed command to its handler.
func ExecuteCommand(msg CommandMsg) CommandResult {
	switch msg.Cmd {
	case "/exit":
		return CommandResult{Action: "quit"}
	case "/clear":
		return CommandResult{Action: "clear"}
	case "/help":
		return CommandResult{Action: "help_popup", Content: HelpText}
	case "/model":
		if msg.Args != "" {
			return CommandResult{Action: "switch_model", Content: msg.Args}
		}
		return CommandResult{Action: "model_popup"}
	case "/skills":
		return CommandResult{Action: "skills_popup"}
	case "/mcp":
		return CommandResult{Action: "fetch", API: "/tools"}
	case "/export":
		return CommandResult{Action: "export"}
	default:
		return CommandResult{Action: "help_popup",
			Content: fmt.Sprintf("未知命令: %s\n输入 /help 查看可用命令", msg.Cmd)}
	}
}

// ExportToMarkdown writes session data to ~/.groot/exports/chat-<id>.md.
func ExportToMarkdown(body []byte) (string, error) {
	homeDir, _ := os.UserHomeDir()
	exportDir := filepath.Join(homeDir, ".groot", "exports")
	os.MkdirAll(exportDir, 0755)

	var data map[string]interface{}
	if err := json.Unmarshal(body, &data); err != nil {
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
const HelpText = `  
	命令                     			快捷键
  ───────────────────             	 ───────────────────────────
  /exit         退出       			Enter           发送
  /model [name] 切换       			Alt+Enter / Shift+Enter 换行
  /clear        新对话       		  Tab             补全
  /help         帮助       			ESC             关闭 / 取消
  /skills       技能       			Ctrl+C          退出
  /mcp          工具
  /export       导出
	`
