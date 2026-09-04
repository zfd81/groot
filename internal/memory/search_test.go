package memory

import (
	"strings"
	"testing"
	"time"
)

// seedManagerChat 通过 Manager 落一条主 Agent 轮次。
func seedManagerChat(t *testing.T, m *Manager, sid, chatID, instruction, result string) {
	t.Helper()
	if !m.ExistsSession(sid) {
		if err := m.CreateSession(sid, ""); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
	}
	err := m.SaveChatRecord(sid, &ChatRecord{
		ChatID: chatID, Instruction: instruction, Result: result,
		Status: "completed", StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("SaveChatRecord: %v", err)
	}
}

func TestSearch_EmptyKeyword(t *testing.T) {
	m := newTestManager(t)
	for _, kw := range []string{"", "   ", "\t\n"} {
		results, err := m.Search("", kw, 20)
		if err != nil {
			t.Fatalf("Search(%q): %v", kw, err)
		}
		if len(results) != 0 {
			t.Errorf("Search(%q): expected empty results, got %d", kw, len(results))
		}
	}
}

func TestSearch_ResultFields(t *testing.T) {
	m := newTestManager(t)
	seedManagerChat(t, m, "sr-1", "c1", "如何部署 groot 服务", "使用 systemd 部署即可")

	results, err := m.Search("", "部署", 20)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	r := results[0]
	if r.SessionID != "sr-1" || r.ChatID != "c1" || r.Round != 1 {
		t.Errorf("unexpected identity fields: %+v", r)
	}
	if r.Title != "如何部署 groot 服务" {
		t.Errorf("unexpected title: %q", r.Title)
	}
	// instruction 与 result 都含关键词时优先 instruction
	if r.MatchedField != "instruction" {
		t.Errorf("expected matched_field=instruction, got %q", r.MatchedField)
	}
	if !strings.Contains(r.Snippet, "部署") {
		t.Errorf("snippet should contain keyword, got %q", r.Snippet)
	}
	if r.Timestamp <= 0 {
		t.Errorf("expected positive timestamp, got %d", r.Timestamp)
	}
}

func TestSearch_MatchedFieldResult(t *testing.T) {
	m := newTestManager(t)
	seedManagerChat(t, m, "sr-2", "c2", "随便聊聊", "冒泡排序是最基础的排序算法")

	results, err := m.Search("", "冒泡", 20)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].MatchedField != "result" {
		t.Errorf("expected matched_field=result, got %q", results[0].MatchedField)
	}
}

func TestSearch_LimitFallback(t *testing.T) {
	m := newTestManager(t)
	seedManagerChat(t, m, "sr-3", "c3", "限值回退测试", "")

	// limit<=0 与超上限都不报错，正常返回
	for _, limit := range []int{0, -1, 999} {
		results, err := m.Search("", "限值回退", limit)
		if err != nil {
			t.Fatalf("Search(limit=%d): %v", limit, err)
		}
		if len(results) != 1 {
			t.Errorf("Search(limit=%d): expected 1 result, got %d", limit, len(results))
		}
	}
}

func TestSearch_UserIDFilter(t *testing.T) {
	m := newTestManager(t)
	sid := "sr-user"
	if err := m.CreateSession(sid, "some-user"); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	err := m.SaveChatRecord(sid, &ChatRecord{
		ChatID: "c-u1", Instruction: "用户过滤关键词测试", Result: "",
		Status: "completed", StartedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("SaveChatRecord: %v", err)
	}

	// 其他用户搜不到
	results, err := m.Search("other-user", "用户过滤关键词", 20)
	if err != nil {
		t.Fatalf("Search(other-user): %v", err)
	}
	if len(results) != 0 {
		t.Errorf("Search(other-user): expected 0 results, got %d", len(results))
	}

	// 会话所属用户能搜到
	results, err = m.Search("some-user", "用户过滤关键词", 20)
	if err != nil {
		t.Fatalf("Search(some-user): %v", err)
	}
	if len(results) != 1 {
		t.Errorf("Search(some-user): expected 1 result, got %d", len(results))
	}
}

func TestPickSnippet_Fallback(t *testing.T) {
	// instruction 与 result 都定位不到关键词：回退为 instruction 开头截取
	instruction := strings.Repeat("长", 100)
	snippet, field := pickSnippet(instruction, "", "无关词")
	if field != "instruction" {
		t.Errorf("expected field=instruction, got %q", field)
	}
	if strings.HasPrefix(snippet, "…") {
		t.Errorf("no leading ellipsis expected: %q", snippet)
	}
	if !strings.HasSuffix(snippet, "…") {
		t.Errorf("expected trailing ellipsis: %q", snippet)
	}
	// 前 snippetBefore+snippetAfter=80 个 rune + 尾部省略号 = 81
	wantLen := snippetBefore + snippetAfter + 1
	if n := len([]rune(snippet)); n != wantLen {
		t.Errorf("expected %d runes, got %d (%q)", wantLen, n, snippet)
	}
	if want := strings.Repeat("长", snippetBefore+snippetAfter) + "…"; snippet != want {
		t.Errorf("unexpected snippet: %q", snippet)
	}

	// instruction 与 result 都为空：不 panic，返回空摘要
	snippet, field = pickSnippet("", "", "无关词")
	if snippet != "" {
		t.Errorf("expected empty snippet, got %q", snippet)
	}
	if field != "instruction" {
		t.Errorf("expected field=instruction, got %q", field)
	}
}

func TestMakeSnippet(t *testing.T) {
	// 中文长文本：关键词前 20、后 60 rune，两端补省略号
	prefix := strings.Repeat("前", 30)
	suffix := strings.Repeat("后", 80)
	text := prefix + "目标词" + suffix
	s, ok := makeSnippet(text, "目标词")
	if !ok {
		t.Fatal("expected found")
	}
	if !strings.HasPrefix(s, "…") || !strings.HasSuffix(s, "…") {
		t.Errorf("expected ellipsis on both ends: %q", s)
	}
	if !strings.Contains(s, "目标词") {
		t.Errorf("snippet must contain keyword: %q", s)
	}
	// rune 计数：省略号2 + 前 snippetBefore + 关键词 + 后 snippetAfter
	want := 2 + snippetBefore + len([]rune("目标词")) + snippetAfter
	if n := len([]rune(s)); n != want {
		t.Errorf("expected %d runes, got %d (%q)", want, n, s)
	}

	// 关键词在开头：无前置省略号
	s, ok = makeSnippet("目标词开头的短句", "目标词")
	if !ok {
		t.Fatal("expected found")
	}
	if strings.HasPrefix(s, "…") {
		t.Errorf("no leading ellipsis expected: %q", s)
	}
	if strings.HasSuffix(s, "…") {
		t.Errorf("no trailing ellipsis for short text: %q", s)
	}

	// 大小写不敏感
	if _, ok = makeSnippet("Hello World", "world"); !ok {
		t.Error("expected case-insensitive match")
	}

	// 找不到
	if _, ok = makeSnippet("abc", "xyz"); ok {
		t.Error("expected not found")
	}
}
