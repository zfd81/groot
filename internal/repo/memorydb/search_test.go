package memorydb

import (
	"context"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/repo"
)

// seedSession 建会话；userID 可为空串。
func seedSession(t *testing.T, r repo.MemoryRepo, sid, userID string) {
	t.Helper()
	err := r.CreateSession(context.Background(), &repo.Session{
		SessionID: sid, UserID: userID, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateSession %s: %v", sid, err)
	}
}

// seedChat 存一条主 Agent 轮次（round 由 SaveChat 自动递增）。
func seedChat(t *testing.T, r repo.MemoryRepo, sid, chatID, instruction, result string, startedAt time.Time) {
	t.Helper()
	err := r.SaveChat(context.Background(), &repo.ChatRecord{
		ChatID: chatID, SessionID: sid,
		Instruction: instruction, Result: result,
		Status: "completed", StartedAt: startedAt,
	})
	if err != nil {
		t.Fatalf("SaveChat %s: %v", chatID, err)
	}
}

func TestSearchChats_MatchInstructionAndResult(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	base := time.Now().Add(-time.Hour)
	if err := r.CreateSession(ctx, &repo.Session{
		SessionID: "s1", Title: "排序会话", CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("CreateSession s1: %v", err)
	}
	seedChat(t, r, "s1", "c1", "怎么写快速排序", "快排代码如下……", base)
	seedChat(t, r, "s1", "c2", "换个话题", "冒泡排序更简单", base.Add(time.Minute))
	seedChat(t, r, "s1", "c3", "今天天气如何", "晴天", base.Add(2*time.Minute))

	hits, err := r.SearchChats(ctx, nil, "排序", 20)
	if err != nil {
		t.Fatalf("SearchChats: %v", err)
	}
	// c1 命中 instruction，c2 命中 result；c3 不命中。按 started_at 倒序：c2 在前。
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits, got %d", len(hits))
	}
	if hits[0].ChatID != "c2" || hits[1].ChatID != "c1" {
		t.Errorf("expected order [c2 c1], got [%s %s]", hits[0].ChatID, hits[1].ChatID)
	}
	if hits[0].Title != "排序会话" || hits[1].Title != "排序会话" {
		t.Errorf("expected title from session column, got %q / %q", hits[0].Title, hits[1].Title)
	}
	if hits[1].Round != 1 || hits[0].Round != 2 {
		t.Errorf("unexpected rounds: c1=%d c2=%d", hits[1].Round, hits[0].Round)
	}
}

func TestSearchChats_ExcludesSubAgentAndUncompleted(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	seedSession(t, r, "s2", "")
	// 主 Agent 轮次（占 round 1），不含关键词
	seedChat(t, r, "s2", "m1", "无关内容", "无关回复", time.Now())
	// 子 Agent 记录：含关键词但不应命中
	if err := r.SaveChat(ctx, &repo.ChatRecord{
		ChatID: "sub1", SessionID: "s2", AgentName: "weather", Round: 1,
		Instruction: "特殊关键词xyz", Result: "", Status: "completed", StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("SaveChat sub: %v", err)
	}
	// 失败状态的主 Agent 轮次：含关键词但不应命中
	if err := r.SaveChat(ctx, &repo.ChatRecord{
		ChatID: "f1", SessionID: "s2",
		Instruction: "特殊关键词xyz", Result: "", Status: "failed", StartedAt: time.Now(),
	}); err != nil {
		t.Fatalf("SaveChat failed-status: %v", err)
	}

	hits, err := r.SearchChats(ctx, nil, "特殊关键词xyz", 20)
	if err != nil {
		t.Fatalf("SearchChats: %v", err)
	}
	if len(hits) != 0 {
		t.Errorf("expected 0 hits, got %d", len(hits))
	}
}

func TestSearchChats_EscapesLikeSpecials(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	seedSession(t, r, "s3", "")
	seedChat(t, r, "s3", "e1", "进度是100%完成", "", time.Now())
	seedChat(t, r, "s3", "e2", "进度是100X完成", "", time.Now().Add(time.Second))
	seedChat(t, r, "s3", "e3", "变量 a_b 的含义", "", time.Now().Add(2*time.Second))
	seedChat(t, r, "s3", "e4", "变量 aXb 的含义", "", time.Now().Add(3*time.Second))

	// "100%" 若不转义，% 会通配匹配到 e2
	hits, err := r.SearchChats(ctx, nil, "100%", 20)
	if err != nil {
		t.Fatalf("SearchChats: %v", err)
	}
	if len(hits) != 1 || hits[0].ChatID != "e1" {
		t.Fatalf("expected only e1, got %d hits", len(hits))
	}
	// "a_b" 若不转义，_ 会通配匹配到 e4
	hits, err = r.SearchChats(ctx, nil, "a_b", 20)
	if err != nil {
		t.Fatalf("SearchChats: %v", err)
	}
	if len(hits) != 1 || hits[0].ChatID != "e3" {
		t.Fatalf("expected only e3, got %d hits", len(hits))
	}
	// 转义符本身：搜 "!" 不应报错、不应误匹配
	seedChat(t, r, "s3", "e5", "感叹号!在这里", "", time.Now().Add(4*time.Second))
	hits, err = r.SearchChats(ctx, nil, "号!在", 20)
	if err != nil {
		t.Fatalf("SearchChats: %v", err)
	}
	if len(hits) != 1 || hits[0].ChatID != "e5" {
		t.Fatalf("expected only e5, got %d hits", len(hits))
	}
}

// seedOwnedSession 建带租户归属的会话。
func seedOwnedSession(t *testing.T, r repo.MemoryRepo, sid, tenantID, userID string) {
	t.Helper()
	err := r.CreateSession(context.Background(), &repo.Session{
		SessionID: sid, TenantID: tenantID, UserID: userID, CreatedAt: time.Now(), UpdatedAt: time.Now(),
	})
	if err != nil {
		t.Fatalf("CreateSession %s: %v", sid, err)
	}
}

func TestSearchChats_OwnerFilter(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	seedOwnedSession(t, r, "crm-s", "crm", "u1")
	seedOwnedSession(t, r, "erp-s", "erp", "u1")
	seedChat(t, r, "crm-s", "oc1", "租户过滤测试", "", time.Now())
	seedChat(t, r, "erp-s", "oc2", "租户过滤测试", "", time.Now())

	// owner nil：不过滤
	hits, err := r.SearchChats(ctx, nil, "租户过滤", 20)
	if err != nil {
		t.Fatalf("SearchChats: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits without owner filter, got %d", len(hits))
	}
	// 同 user 不同 tenant：只命中 crm
	hits, err = r.SearchChats(ctx, &repo.SessionOwner{TenantID: "crm", UserID: "u1"}, "租户过滤", 20)
	if err != nil {
		t.Fatalf("SearchChats: %v", err)
	}
	if len(hits) != 1 || hits[0].SessionID != "crm-s" {
		t.Fatalf("expected only crm-s, got %+v", hits)
	}
	// 同 tenant 不同 user：无命中
	hits, err = r.SearchChats(ctx, &repo.SessionOwner{TenantID: "crm", UserID: "u2"}, "租户过滤", 20)
	if err != nil {
		t.Fatalf("SearchChats: %v", err)
	}
	if len(hits) != 0 {
		t.Fatalf("expected 0 hits for crm/u2, got %d", len(hits))
	}
}

func TestSearchChats_Limit(t *testing.T) {
	r := newMemRepo(t)
	ctx := context.Background()
	seedSession(t, r, "s4", "")
	base := time.Now()
	seedChat(t, r, "s4", "l1", "限流测试词", "", base)
	seedChat(t, r, "s4", "l2", "限流测试词", "", base.Add(time.Second))
	seedChat(t, r, "s4", "l3", "限流测试词", "", base.Add(2*time.Second))

	hits, err := r.SearchChats(ctx, nil, "限流测试词", 2)
	if err != nil {
		t.Fatalf("SearchChats: %v", err)
	}
	if len(hits) != 2 {
		t.Fatalf("expected 2 hits (limit), got %d", len(hits))
	}
	// 倒序：最新的 l3 在前
	if hits[0].ChatID != "l3" {
		t.Errorf("expected l3 first, got %s", hits[0].ChatID)
	}
}
