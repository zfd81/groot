package sync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/resourcedb"
)

// newDBTestManager 用真实的数据库仓储（SQLite 后端）构造 SyncManager。
//
// 为什么不用 sync_test.go 的 newTestManager：resourcelocal 把内容存成文件，
// 读取时按文件内容现算 ContentHash，完全忽略 Put 传入的该字段。这让它对
// 「调用方忘记填 ContentHash」这类缺陷免疫。resourcedb 才是生产路径——它把
// ContentHash 原样写入数据库列，字段留空就会一直空着。
func newDBTestManager(t *testing.T) (*localSyncManager, string) {
	t.Helper()
	homeDir := t.TempDir()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	r := resourcedb.New(sqlxDB, dialect)
	return NewSyncManager(homeDir, r).(*localSyncManager), homeDir
}

// TestPush_WritesContentHashToDB 锁定 pushOne 必须填 ContentHash。
// 直接查仓储记录，而不是只看 Diff 结果，这样断言失败时能指明是哪个字段空了。
func TestPush_WritesContentHashToDB(t *testing.T) {
	mgr, homeDir := newDBTestManager(t)
	content := []byte("# Groot\n")
	if err := os.WriteFile(filepath.Join(homeDir, "GROOT.md"), content, 0644); err != nil {
		t.Fatalf("write local: %v", err)
	}

	if err := mgr.Push([]string{"GROOT.md"}); err != nil {
		t.Fatalf("Push: %v", err)
	}

	entry, err := mgr.repo.Stat(t.Context(), "GROOT.md")
	if err != nil {
		t.Fatalf("Stat after push: %v", err)
	}
	want := sha1Hex(content)
	if entry.ContentHash != want {
		t.Errorf("ContentHash = %q, want %q", entry.ContentHash, want)
	}
	if entry.Size != int64(len(content)) {
		t.Errorf("Size = %d, want %d", entry.Size, len(content))
	}
	if entry.UpdatedAt.IsZero() {
		t.Error("UpdatedAt is zero, want the push timestamp")
	}
}

// TestPush_ThenDiffIsClean 复现用户报的缺陷：推送后未做任何本地改动，
// 再次 Diff 却把文件判为 Modified（界面显示「内容不同」）。
// 覆盖多种资源形态，确保修复不是只对单个文件生效。
func TestPush_ThenDiffIsClean(t *testing.T) {
	mgr, homeDir := newDBTestManager(t)

	files := map[string]string{
		"GROOT.md":                              "# Groot\n",
		"config.yaml":                           "agent: groot\n",
		"skills/weather/SKILL.md":               "# Weather\n",
		"subagents/weather/agent.md":            "# Agent\n",
		"subagents/weather/mcp/api-proxy.json":  "{}\n",
		"subagents/weather/skills/get/SKILL.md": "# Get\n",
	}
	for rel, body := range files {
		p := filepath.Join(homeDir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
			t.Fatalf("mkdir %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(body), 0644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}

	if err := mgr.Push(nil); err != nil {
		t.Fatalf("Push: %v", err)
	}

	// 本地一字未改，此时差异必须为空。
	got, err := mgr.Diff(nil)
	if err != nil {
		t.Fatalf("Diff after push: %v", err)
	}
	if !got.IsEmpty() {
		t.Errorf("diff after push not empty:\n  Added=%v\n  Modified=%v\n  Removed=%v",
			got.Added, got.Modified, got.Removed)
	}
	if len(got.Same) != len(files) {
		t.Errorf("Same has %d entries, want %d: %v", len(got.Same), len(files), got.Same)
	}
}

// TestPush_ModifiedContentIsDetectedAndRepushed 确认修复没有把比较逻辑
// 弄成「永远相同」：真实改动仍要被判为 Modified，且再次推送后归于一致。
func TestPush_ModifiedContentIsDetectedAndRepushed(t *testing.T) {
	mgr, homeDir := newDBTestManager(t)
	local := filepath.Join(homeDir, "GROOT.md")
	if err := os.WriteFile(local, []byte("v1\n"), 0644); err != nil {
		t.Fatalf("write v1: %v", err)
	}
	if err := mgr.Push([]string{"GROOT.md"}); err != nil {
		t.Fatalf("Push v1: %v", err)
	}

	// 改成同样长度的不同内容，强制比较落到哈希而非 size。
	if err := os.WriteFile(local, []byte("v2\n"), 0644); err != nil {
		t.Fatalf("write v2: %v", err)
	}
	got, err := mgr.Diff([]string{"GROOT.md"})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if len(got.Modified) != 1 || got.Modified[0] != "GROOT.md" {
		t.Fatalf("Modified = %v, want [GROOT.md]", got.Modified)
	}

	if err := mgr.Push([]string{"GROOT.md"}); err != nil {
		t.Fatalf("Push v2: %v", err)
	}
	got, err = mgr.Diff([]string{"GROOT.md"})
	if err != nil {
		t.Fatalf("Diff after repush: %v", err)
	}
	if !got.IsEmpty() {
		t.Errorf("diff after repush not empty: Modified=%v", got.Modified)
	}

	res, err := mgr.repo.Get(t.Context(), "GROOT.md")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(res.Content) != "v2\n" {
		t.Errorf("remote content = %q, want %q", res.Content, "v2\n")
	}
}

// TestPush_RepushAfterRemoteDeleteResetsStatus 确认推送一个曾被删除的路径后，
// 状态复位为 active 且哈希写对，不会因残留的空哈希再次判为 Modified。
func TestPush_RepushAfterRemoteDeleteResetsStatus(t *testing.T) {
	mgr, homeDir := newDBTestManager(t)
	content := []byte("# Groot\n")
	if err := os.WriteFile(filepath.Join(homeDir, "GROOT.md"), content, 0644); err != nil {
		t.Fatalf("write local: %v", err)
	}
	if err := mgr.Push([]string{"GROOT.md"}); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if err := mgr.repo.Delete(t.Context(), "GROOT.md"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// 删除后本地仍有文件 → 应判为 Added（而非 Same）。
	got, err := mgr.Diff([]string{"GROOT.md"})
	if err != nil {
		t.Fatalf("Diff after remote delete: %v", err)
	}
	if len(got.Added) != 1 {
		t.Fatalf("Added = %v, want [GROOT.md]", got.Added)
	}

	if err := mgr.Push([]string{"GROOT.md"}); err != nil {
		t.Fatalf("Repush: %v", err)
	}
	entry, err := mgr.repo.Stat(t.Context(), "GROOT.md")
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if entry.Status != repo.ResourceStatusActive {
		t.Errorf("Status = %q, want %q", entry.Status, repo.ResourceStatusActive)
	}
	if entry.ContentHash != sha1Hex(content) {
		t.Errorf("ContentHash = %q, want %q", entry.ContentHash, sha1Hex(content))
	}
	got, err = mgr.Diff([]string{"GROOT.md"})
	if err != nil {
		t.Fatalf("Diff after repush: %v", err)
	}
	if !got.IsEmpty() {
		t.Errorf("diff after repush not empty: %+v", got)
	}
}
