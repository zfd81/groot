package memory

import (
	"errors"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/repo"
)

func TestOwnsSession(t *testing.T) {
	m := newTestManager(t)
	if err := m.CreateSession("s1", "crm", "u1", ""); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	cases := []struct {
		sid, tenant, user string
		want              bool
	}{
		{"s1", "crm", "u1", true},
		{"s1", "crm", "u2", false},
		{"s1", "erp", "u1", false},
		{"missing", "crm", "u1", false},
	}
	for _, c := range cases {
		if got := m.OwnsSession(c.sid, c.tenant, c.user); got != c.want {
			t.Errorf("OwnsSession(%q,%q,%q)=%v want %v", c.sid, c.tenant, c.user, got, c.want)
		}
	}
}

func TestListSessions_ByOwner(t *testing.T) {
	m := newTestManager(t)
	if err := m.CreateSession("a", "crm", "u1", ""); err != nil {
		t.Fatalf("CreateSession a: %v", err)
	}
	if err := m.CreateSession("b", "erp", "u1", ""); err != nil {
		t.Fatalf("CreateSession b: %v", err)
	}
	got, total, err := m.ListSessions(&repo.SessionOwner{TenantID: "crm", UserID: "u1"}, 20, 0)
	if err != nil {
		t.Fatalf("ListSessions(owner): %v", err)
	}
	if total != 1 || len(got) != 1 || got[0].SessionID != "a" {
		t.Errorf("got %+v total=%d", got, total)
	}
	all, totalAll, err := m.ListSessions(nil, 20, 0)
	if err != nil {
		t.Fatalf("ListSessions(nil): %v", err)
	}
	if totalAll != 2 || len(all) != 2 {
		t.Errorf("nil owner: %d", totalAll)
	}
}

func TestGetChatRecord_RejectsForeignChat(t *testing.T) {
	m := newTestManager(t)
	if err := m.CreateSession("s1", "crm", "u1", ""); err != nil {
		t.Fatalf("CreateSession s1: %v", err)
	}
	if err := m.CreateSession("s2", "crm", "u1", ""); err != nil {
		t.Fatalf("CreateSession s2: %v", err)
	}
	err := m.SaveChatRecord("s1", &ChatRecord{ChatID: "c1", Instruction: "x", Result: "y", Status: "completed", StartedAt: time.Now()})
	if err != nil {
		t.Fatalf("SaveChatRecord: %v", err)
	}
	if _, err := m.GetChatRecord("s1", "c1"); err != nil {
		t.Fatalf("own chat: %v", err)
	}
	if _, err := m.GetChatRecord("s2", "c1"); err == nil {
		t.Fatal("chat of s1 must not be readable via s2")
	}
}

func TestCreateSession_TitleAndRename(t *testing.T) {
	m := newTestManager(t)
	if err := m.CreateSession("s1", "crm", "u1", "初始标题"); err != nil {
		t.Fatal(err)
	}
	info, _ := m.GetSessionInfo("s1")
	if info.Title != "初始标题" {
		t.Errorf("Title=%q", info.Title)
	}
	if err := m.RenameSession("s1", "新标题"); err != nil {
		t.Fatal(err)
	}
	info, _ = m.GetSessionInfo("s1")
	if info.Title != "新标题" {
		t.Errorf("Title=%q", info.Title)
	}
	if err := m.RenameSession("missing", "x"); !errors.Is(err, repo.ErrNotFound) {
		t.Errorf("want ErrNotFound got %v", err)
	}
}

// total 来自 COUNT 查询：越界页也应返回完整总数，且按 owner 统计。
func TestListSessions_TotalIndependentOfPage(t *testing.T) {
	m := newTestManager(t)
	for i := 0; i < 4; i++ {
		if err := m.CreateSession(GenerateSessionID(), "crm", "u1", ""); err != nil {
			t.Fatalf("CreateSession: %v", err)
		}
		time.Sleep(time.Millisecond)
	}
	if err := m.CreateSession(GenerateSessionID(), "erp", "u9", ""); err != nil {
		t.Fatalf("CreateSession erp: %v", err)
	}
	owner := &repo.SessionOwner{TenantID: "crm", UserID: "u1"}
	page, total, err := m.ListSessions(owner, 3, 3)
	if err != nil {
		t.Fatalf("ListSessions: %v", err)
	}
	if total != 4 || len(page) != 1 {
		t.Errorf("offset=3: total=%d len=%d, want 4/1", total, len(page))
	}
	beyond, total, err := m.ListSessions(owner, 3, 99)
	if err != nil {
		t.Fatalf("ListSessions beyond: %v", err)
	}
	if total != 4 || len(beyond) != 0 {
		t.Errorf("offset=99: total=%d len=%d, want 4/0", total, len(beyond))
	}
}
