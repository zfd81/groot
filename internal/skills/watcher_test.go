package skills

import (
	"testing"

	"github.com/fsnotify/fsnotify"

	"github.com/zfd81/groot/internal/logger"
)

// TestExtractSubAgentNameForSkill 验证路径解析：
//   - subagents/<name>/skills/... 形态 → 返回 name + ok=true
//   - subagents/<name>/agent.md / mcp/* 等其它路径 → ok=false（不触发 skill 重载）
//   - 不在 subagents 目录下 → ok=false
func TestExtractSubAgentNameForSkill(t *testing.T) {
	cases := []struct {
		name    string
		path    string
		baseDir string
		want    string
		ok      bool
	}{
		{"skills 子目录命中", "/x/subagents/db-agent/skills/sql-review/SKILL.md", "/x/subagents", "db-agent", true},
		{"skills 直接子文件命中", "/x/subagents/db-agent/skills/x.md", "/x/subagents", "db-agent", true},
		{"agent.md 不在 skills 目录下", "/x/subagents/db-agent/agent.md", "/x/subagents", "", false},
		{"mcp/ 子目录不在 skills 目录下", "/x/subagents/db-agent/mcp/x.json", "/x/subagents", "", false},
		{"路径不在 subagents 之下", "/x/other/y/SKILL.md", "/x/subagents", "", false},
		{"空路径", "", "/x/subagents", "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, ok := extractSubAgentNameForSkill(c.path, c.baseDir)
			if got != c.want || ok != c.ok {
				t.Errorf("path=%s want=%s/%v got=%s/%v", c.path, c.want, c.ok, got, ok)
			}
		})
	}
}

// TestClassifySkillChange 验证事件分类：
//   - SKILL.md 在主 skills 目录 → kind="main"
//   - SKILL.md 在 subagents/<n>/skills 下 → kind="subagent", name=<n>
//   - agent.md / mcp/ 等非 skills 路径 → kind=""（丢弃）
func TestClassifySkillChange(t *testing.T) {
	w := &Watcher{
		skillsDir:        "/x/skills",
		subAgentsBaseDir: "/x/subagents",
	}
	type c struct {
		name      string
		eventName string
		wantKind  string
		wantAgent string
	}
	cases := []c{
		{"主 SKILL.md", "/x/skills/foo/SKILL.md", "main", ""},
		{"子 SKILL.md", "/x/subagents/db-agent/skills/x/SKILL.md", "subagent", "db-agent"},
		{"agent.md 丢弃", "/x/subagents/db-agent/agent.md", "", ""},
		{"mcp 丢弃", "/x/subagents/db-agent/mcp/x.json", "", ""},
		{"非 skills 路径", "/tmp/random.md", "", ""},
		{"伪主 skills 前缀（skillsbackup）不应误命中", "/x/skillsbackup/foo/SKILL.md", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			kind, agent := w.classifySkillChange(fsnotifyEventForTest(tc.eventName))
			if kind != tc.wantKind || agent != tc.wantAgent {
				t.Errorf("event=%s want=(%q,%q) got=(%q,%q)", tc.eventName, tc.wantKind, tc.wantAgent, kind, agent)
			}
		})
	}
}

// fsnotifyEventForTest 构造一个最小 fsnotify.Event；Op 选 Create 让事件类匹配 isSkillChange 的 OR 条件。
func fsnotifyEventForTest(name string) fsnotify.Event {
	return fsnotify.Event{Name: name, Op: fsnotify.Create}
}

// TestNewSubAgentReloadCallback 验证回调工厂的过滤与放行行为：
//   - nameKnown=nil → 放行（不会 panic，仅 log）
//   - nameKnown 返回 false → 直接 return，不会触发后续 log
//   - nameKnown 返回 true → 走完 log 路径（无法直接断言 log 内容，但路径覆盖且不 panic）
func TestNewSubAgentReloadCallback(t *testing.T) {
	log := logger.NewNop()

	t.Run("nameKnown 为 nil 时放行", func(t *testing.T) {
		cb := NewSubAgentReloadCallback(log, nil)
		if cb == nil {
			t.Fatal("回调不应为 nil")
		}
		// 不 panic 即视为通过
		cb("any-agent")
	})

	t.Run("谓词返回 false 时不调用后续路径", func(t *testing.T) {
		called := false
		predicate := func(name string) bool {
			called = true
			return false
		}
		cb := NewSubAgentReloadCallback(log, predicate)
		cb("unknown-agent")
		if !called {
			t.Error("谓词应当被调用")
		}
		// false 之后只有 return，无法直接断言；保证不 panic 即可
	})

	t.Run("谓词返回 true 时走完 log 路径", func(t *testing.T) {
		predicate := func(name string) bool { return true }
		cb := NewSubAgentReloadCallback(log, predicate)
		// 不 panic 即视为通过
		cb("known-agent")
	})
}
