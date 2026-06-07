package memory

import _ "embed"

// defaultSessionRules 通过 go:embed 嵌入会话规则正文,在每轮系统指令中注入,
// 告知 LLM 何时调用内置工具 groot_file_list / groot_file_read,
// 何时降级到用户自配的文件系统 MCP 工具。
//
//go:embed session_rules.md
var defaultSessionRules string
