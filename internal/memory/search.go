package memory

import (
	"context"
	"fmt"
	"strings"
)

// SearchResult 一条搜索结果（轮次级），字段直接对应 /sess/search 响应。
type SearchResult struct {
	SessionID    string `json:"session_id"`
	ChatID       string `json:"chat_id"`
	Round        int    `json:"round"`
	Title        string `json:"title"`
	Snippet      string `json:"snippet"`
	MatchedField string `json:"matched_field"` // instruction | result
	Timestamp    int64  `json:"timestamp"`     // 轮次开始时间（毫秒）
}

const (
	searchDefaultLimit = 20
	searchMaxLimit     = 50
	snippetBefore      = 20 // 关键词前保留的 rune 数
	snippetAfter       = 60 // 关键词后保留的 rune 数
)

// Search 在历史对话（主 Agent 已完成轮次）中模糊搜索 keyword。
// userID 非空时只搜该用户的会话；keyword 去除首尾空白后为空返回空结果；
// limit 非正数回退默认值，超上限时封顶。
func (m *Manager) Search(userID, keyword string, limit int) ([]SearchResult, error) {
	keyword = strings.TrimSpace(keyword)
	if keyword == "" {
		return []SearchResult{}, nil
	}
	if limit <= 0 {
		limit = searchDefaultLimit
	}
	if limit > searchMaxLimit {
		limit = searchMaxLimit
	}
	hits, err := m.repo.SearchChats(context.Background(), userID, keyword, limit)
	if err != nil {
		return nil, fmt.Errorf("搜索对话失败: %w", err)
	}
	results := make([]SearchResult, 0, len(hits))
	for _, h := range hits {
		snippet, field := pickSnippet(h.Instruction, h.Result, keyword)
		results = append(results, SearchResult{
			SessionID:    h.SessionID,
			ChatID:       h.ChatID,
			Round:        h.Round,
			Title:        h.Title,
			Snippet:      snippet,
			MatchedField: field,
			Timestamp:    h.StartedAt.UnixMilli(),
		})
	}
	return results, nil
}

// pickSnippet 依次尝试 instruction、result，返回首个能定位到 keyword 的摘要。
// 两者都定位不到（数据库 LIKE 与 Go 大小写折叠规则不一致的罕见情形）时，
// 回退为 instruction 开头截取。
func pickSnippet(instruction, result, keyword string) (snippet, field string) {
	if s, ok := makeSnippet(instruction, keyword); ok {
		return s, "instruction"
	}
	if s, ok := makeSnippet(result, keyword); ok {
		return s, "result"
	}
	runes := []rune(instruction)
	end := snippetBefore + snippetAfter
	if end > len(runes) {
		end = len(runes)
	}
	s := string(runes[:end])
	if end < len(runes) {
		s += "…"
	}
	return s, "instruction"
}

// makeSnippet 在 text 中大小写不敏感地定位 keyword 首次出现的位置，
// 截取其前约 snippetBefore、后约 snippetAfter 个字符（按 rune，UTF-8 安全），
// 两端被截断时补省略号。定位不到时 ok=false。
func makeSnippet(text, keyword string) (snippet string, ok bool) {
	byteIdx := strings.Index(strings.ToLower(text), strings.ToLower(keyword))
	if byteIdx < 0 {
		return "", false
	}
	// ASCII 与 CJK 的小写折叠不改变字节长度，byteIdx 可直接用于原文；
	// 个别字符折叠后长度变化会导致轻微偏移，snippet 场景可接受。
	if byteIdx > len(text) {
		byteIdx = len(text)
	}
	runeIdx := len([]rune(text[:byteIdx]))
	kwLen := len([]rune(keyword))
	runes := []rune(text)
	start := runeIdx - snippetBefore
	if start < 0 {
		start = 0
	}
	end := runeIdx + kwLen + snippetAfter
	if end > len(runes) {
		end = len(runes)
	}
	if start > end { // 防御折叠偏移导致的越界
		start = 0
	}
	s := string(runes[start:end])
	if start > 0 {
		s = "…" + s
	}
	if end < len(runes) {
		s += "…"
	}
	return s, true
}
