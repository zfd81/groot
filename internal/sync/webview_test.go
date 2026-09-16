package sync

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestBuildWebDiff_SortsAndLabelsEntries(t *testing.T) {
	d := DiffResult{
		Added:    []string{"skills/weather/SKILL.md"},
		Modified: []string{"config.yaml"},
		Removed:  []string{"mcp/db/config.json"},
		Same:     []string{"GROOT.md"},
		Remote: map[string]RemoteMeta{
			"skills/weather/SKILL.md": {Deleted: true, UpdatedAt: time.UnixMilli(1757734800000)},
			"config.yaml":             {Deleted: false, UpdatedAt: time.UnixMilli(1757734900000)},
			"mcp/db/config.json":      {Deleted: false, UpdatedAt: time.UnixMilli(1757735000000)},
		},
	}

	view := BuildWebDiff(d)

	if view.InSync {
		t.Fatal("InSync = true, want false")
	}
	if len(view.Entries) != 3 {
		t.Fatalf("len(Entries) = %d, want 3 (Same excluded)", len(view.Entries))
	}
	// 按路径升序
	wantPaths := []string{"config.yaml", "mcp/db/config.json", "skills/weather/SKILL.md"}
	for i, want := range wantPaths {
		if view.Entries[i].Path != want {
			t.Fatalf("Entries[%d].Path = %q, want %q", i, view.Entries[i].Path, want)
		}
	}
	if view.Entries[0].Status != "M" {
		t.Fatalf("config.yaml status = %q, want M", view.Entries[0].Status)
	}
	if view.Entries[1].Status != "D" {
		t.Fatalf("mcp/db/config.json status = %q, want D", view.Entries[1].Status)
	}
	if view.Entries[2].Status != "A" {
		t.Fatalf("skills/... status = %q, want A", view.Entries[2].Status)
	}
	if !view.Entries[2].RemoteDeleted {
		t.Fatal("tombstoned path should have RemoteDeleted = true")
	}
	if view.Entries[0].RemoteDeleted {
		t.Fatal("config.yaml should have RemoteDeleted = false")
	}
}

func TestBuildWebDiff_NeedsRestartFlags(t *testing.T) {
	d := DiffResult{
		Modified: []string{"config.yaml", "mcp/db/config.json", "subagents/x/agent.md", "skills/w/SKILL.md"},
		Remote:   map[string]RemoteMeta{},
	}

	view := BuildWebDiff(d)

	byPath := map[string]bool{}
	for _, e := range view.Entries {
		byPath[e.Path] = e.NeedsRestart
	}
	for _, p := range []string{"config.yaml", "mcp/db/config.json", "subagents/x/agent.md"} {
		if !byPath[p] {
			t.Errorf("%s NeedsRestart = false, want true", p)
		}
	}
	if byPath["skills/w/SKILL.md"] {
		t.Error("skills/w/SKILL.md NeedsRestart = true, want false")
	}
	if !view.NeedsRestart {
		t.Error("view.NeedsRestart = false, want true")
	}
}

// TestBuildWebDiff_JSONShape 断言 marshal 后的字符串,而不是 Go 结构体字段——
// 后者会绕开 json tag,tag 拼错时 Go 侧仍然全绿而前端拿不到数据。
// 这个测试同时锁定三件事:tag 拼写、entries 是 [] 而非 null、
// 以及远端无记录时 remoteUpdatedAt 整个 key 不出现(零值 time.Time 守卫)。
func TestBuildWebDiff_JSONShape(t *testing.T) {
	emptyJSON, err := json.Marshal(BuildWebDiff(DiffResult{Same: []string{"config.yaml"}}))
	if err != nil {
		t.Fatalf("marshal empty diff: %v", err)
	}
	wantEmpty := `{"inSync":true,"needsRestart":false,"entries":[]}`
	if string(emptyJSON) != wantEmpty {
		t.Errorf("empty diff JSON =\n  %s\nwant\n  %s", emptyJSON, wantEmpty)
	}

	d := DiffResult{
		Added:    []string{"skills/new/SKILL.md"}, // 远端无记录 → 无 remoteUpdatedAt
		Modified: []string{"config.yaml"},         // 远端有记录 → 有 remoteUpdatedAt
		Remote: map[string]RemoteMeta{
			"config.yaml": {Deleted: true, UpdatedAt: time.UnixMilli(1757734900000)},
		},
	}
	fullJSON, err := json.Marshal(BuildWebDiff(d))
	if err != nil {
		t.Fatalf("marshal full diff: %v", err)
	}
	wantFull := `{"inSync":false,"needsRestart":true,"entries":[` +
		`{"path":"config.yaml","status":"M","remoteDeleted":true,"needsRestart":true,"remoteUpdatedAt":1757734900000},` +
		`{"path":"skills/new/SKILL.md","status":"A","remoteDeleted":false,"needsRestart":false}]}`
	if string(fullJSON) != wantFull {
		t.Errorf("full diff JSON =\n  %s\nwant\n  %s", fullJSON, wantFull)
	}

	// 远端无记录的条目不得输出零值时间(会是负数毫秒)。
	if strings.Count(string(fullJSON), "remoteUpdatedAt") != 1 {
		t.Errorf("remoteUpdatedAt should appear exactly once (only for the entry with a remote record):\n%s", fullJSON)
	}
	if strings.Contains(string(fullJSON), `"remoteUpdatedAt":-`) {
		t.Errorf("zero-value time leaked as negative millis:\n%s", fullJSON)
	}
}

func TestBuildWebDiff_EmptyDiffIsInSync(t *testing.T) {
	view := BuildWebDiff(DiffResult{Same: []string{"config.yaml"}, Remote: map[string]RemoteMeta{}})

	if !view.InSync {
		t.Fatal("InSync = false, want true")
	}
	if len(view.Entries) != 0 {
		t.Fatalf("len(Entries) = %d, want 0", len(view.Entries))
	}
	if view.NeedsRestart {
		t.Fatal("NeedsRestart = true, want false for empty diff")
	}
}

// TestNeedsRestart 覆盖 needsRestartPaths 三个前缀的正例、精确匹配分支和反例。
func TestNeedsRestart(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		// 三个前缀各自的正例
		{"config.yaml", true},
		{"mcp/db/config.json", true},
		{"subagents/x/agent.md", true},

		// 精确匹配分支:不带斜杠、不带后续内容,走 strings.TrimSuffix 那条
		{"mcp", true},
		{"subagents", true},

		// 反例
		{"skills/w/SKILL.md", false},
		{"mcpfoo/bar.json", false},
		{"subagentsfoo/a.md", false},
		{"a/config.yaml", false},

		// config.yaml 是唯一不带斜杠的前缀,所以 HasPrefix 会让 config.yaml.bak 命中。
		// 这是宽松前缀匹配的已知副作用,但在 sync 里不可达:ValidateSyncPath 拒绝
		// "config.yaml.bak" 进入 sync,且全量遍历时它不在任何白名单根之下。
		// 此处断言现状仅为锁定行为——将来若要收紧匹配,应能看到这条用例和它的理由。
		{"config.yaml.bak", true},
	}

	for _, tt := range tests {
		if got := needsRestart(tt.path); got != tt.want {
			t.Errorf("needsRestart(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}
