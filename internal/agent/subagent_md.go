package agent

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// AgentMd 表示 agent.md 解析后的结构。
// Temperature/MaxTokens 用指针类型，区分「未设置」（nil，继承模型默认值）
// 与「显式设置为 0」两种情况。
type AgentMd struct {
	Description string   `yaml:"description"`
	Model       string   `yaml:"model,omitempty"`
	Temperature *float64 `yaml:"temperature,omitempty"`
	MaxTokens   *int     `yaml:"max_tokens,omitempty"`
	Content     string   `yaml:"-"` // frontmatter 之后的正文
}

// parseAgentMd 读取 agent.md：YAML frontmatter (--- ... ---) + Markdown 正文。
// description 必填且非空，否则返回错误（启动期会跳过该子 Agent）。
func parseAgentMd(path string) (*AgentMd, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read agent.md: %w", err)
	}
	s := string(raw)

	// 必须以 --- 开头
	if !strings.HasPrefix(s, "---") {
		return nil, fmt.Errorf("missing frontmatter (file must start with ---)")
	}
	// 跳过开头的 --- 及其后的换行
	rest := s[3:]
	if strings.HasPrefix(rest, "\n") {
		rest = rest[1:]
	} else if strings.HasPrefix(rest, "\r\n") {
		rest = rest[2:]
	}
	// 找到结束分隔符（必须以 \n--- 形式出现）
	endIdx := strings.Index(rest, "\n---")
	if endIdx < 0 {
		return nil, fmt.Errorf("frontmatter not terminated (missing closing ---)")
	}
	fmContent := rest[:endIdx]
	body := rest[endIdx+len("\n---"):]
	// 跳过结束分隔符后的可能换行（包括 \r\n 与 \n）
	body = strings.TrimLeft(body, "\r\n")

	md := &AgentMd{}
	if err := yaml.Unmarshal([]byte(fmContent), md); err != nil {
		return nil, fmt.Errorf("parse frontmatter yaml: %w", err)
	}
	if strings.TrimSpace(md.Description) == "" {
		return nil, fmt.Errorf("description is required and must be non-empty")
	}
	md.Content = body
	return md, nil
}
