package sync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zfd81/groot/internal/repo/resourcelocal"
)

// newTestManager 创建测试用 SyncManager:homeDir 使用 tmpdir,远端使用另一个 tmpdir 的 resourcelocal。
func newTestManager(t *testing.T) (*localSyncManager, string, string) {
	t.Helper()
	homeDir := t.TempDir()
	remoteDir := t.TempDir()
	r := resourcelocal.New(remoteDir)
	mgr := NewSyncManager(homeDir, r).(*localSyncManager)
	return mgr, homeDir, remoteDir
}

func TestSyncManager_Diff_NoFiles(t *testing.T) {
	mgr, _, _ := newTestManager(t)
	result, err := mgr.Diff(nil)
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !result.IsEmpty() {
		t.Errorf("expected empty diff, got %+v", result)
	}
}

func TestSyncManager_Push_SingleFile(t *testing.T) {
	mgr, homeDir, remoteDir := newTestManager(t)

	os.WriteFile(filepath.Join(homeDir, "config.yaml"), []byte("agent: groot\n"), 0644)

	if err := mgr.Push([]string{"config.yaml"}); err != nil {
		t.Fatalf("Push: %v", err)
	}

	remote := filepath.Join(remoteDir, "config.yaml")
	data, err := os.ReadFile(remote)
	if err != nil {
		t.Fatalf("remote file not found after push: %v", err)
	}
	if string(data) != "agent: groot\n" {
		t.Errorf("remote content mismatch: %q", data)
	}
}

func TestSyncManager_Push_MirrorDelete(t *testing.T) {
	mgr, homeDir, remoteDir := newTestManager(t)

	// 远端有文件,本地没有 → push 应该删除远端
	os.MkdirAll(filepath.Join(remoteDir, "mcp"), 0755)
	os.WriteFile(filepath.Join(remoteDir, "mcp", "old.json"), []byte("{}"), 0644)

	if err := mgr.Push([]string{"mcp"}); err != nil {
		t.Fatalf("Push: %v", err)
	}

	_, err := os.Stat(filepath.Join(remoteDir, "mcp", "old.json"))
	if !os.IsNotExist(err) {
		t.Error("expected remote file to be deleted after push mirror")
	}
	_ = homeDir
}

func TestSyncManager_Pull_SingleFile(t *testing.T) {
	mgr, homeDir, remoteDir := newTestManager(t)

	os.WriteFile(filepath.Join(remoteDir, "GROOT.md"), []byte("# GROOT\n"), 0644)

	if err := mgr.Pull([]string{"GROOT.md"}); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	local := filepath.Join(homeDir, "GROOT.md")
	data, err := os.ReadFile(local)
	if err != nil {
		t.Fatalf("local file not found after pull: %v", err)
	}
	if string(data) != "# GROOT\n" {
		t.Errorf("local content mismatch: %q", data)
	}
}

func TestSyncManager_Pull_PhaseABeforePhaseB(t *testing.T) {
	mgr, homeDir, remoteDir := newTestManager(t)

	// 远端有 new.md,本地有 old.md → pull 应先写 new.md 再删 old.md
	os.MkdirAll(filepath.Join(remoteDir, "mcp"), 0755)
	os.WriteFile(filepath.Join(remoteDir, "mcp", "new.json"), []byte(`{"new":true}`), 0644)
	os.MkdirAll(filepath.Join(homeDir, "mcp"), 0755)
	os.WriteFile(filepath.Join(homeDir, "mcp", "old.json"), []byte(`{"old":true}`), 0644)

	if err := mgr.Pull([]string{"mcp"}); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	// new.json 应存在
	if _, err := os.Stat(filepath.Join(homeDir, "mcp", "new.json")); err != nil {
		t.Error("expected new.json after pull")
	}
	// old.json 应被删除
	if _, err := os.Stat(filepath.Join(homeDir, "mcp", "old.json")); !os.IsNotExist(err) {
		t.Error("expected old.json to be removed after pull mirror")
	}
}

func TestSyncManager_Pull_CleanTmpFiles(t *testing.T) {
	mgr, homeDir, remoteDir := newTestManager(t)

	// 模拟上次 pull 崩溃留下 .tmp 残留
	os.MkdirAll(filepath.Join(homeDir, "skills", "weather"), 0755)
	os.WriteFile(filepath.Join(homeDir, "skills", "weather", "SKILL.md.tmp"), []byte("stale"), 0644)

	// 远端有 skills/weather/SKILL.md
	os.MkdirAll(filepath.Join(remoteDir, "skills", "weather"), 0755)
	os.WriteFile(filepath.Join(remoteDir, "skills", "weather", "SKILL.md"), []byte("fresh"), 0644)

	if err := mgr.Pull([]string{"skills"}); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	// .tmp 文件应被清理
	tmpPath := filepath.Join(homeDir, "skills", "weather", "SKILL.md.tmp")
	if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
		t.Error("expected .tmp file to be cleaned before pull")
	}
	_ = remoteDir
}

func TestNewSyncManager_LocalMode_Disabled(t *testing.T) {
	// r 为 nil 时返回 disabled 实现
	mgr := NewSyncManager("", nil)
	if _, err := mgr.Diff(nil); err == nil {
		t.Error("expected ErrSyncDisabled in local mode")
	}
	if err := mgr.Push(nil); err == nil {
		t.Error("expected ErrSyncDisabled in local mode")
	}
	if err := mgr.Pull(nil); err == nil {
		t.Error("expected ErrSyncDisabled in local mode")
	}
}

// TestSyncManager_Push_IdempotentAfterPush 验证 push 完成后立刻再 diff,
// 内容相同(size+hash 一致)时应当 IsEmpty。
func TestSyncManager_Push_IdempotentAfterPush(t *testing.T) {
	mgr, homeDir, _ := newTestManager(t)

	makeFile(t, homeDir, "GROOT.md", "# v1\n")

	if err := mgr.Push([]string{"GROOT.md"}); err != nil {
		t.Fatalf("Push: %v", err)
	}

	// push 完立即 diff,应当 IsEmpty
	d, err := mgr.Diff([]string{"GROOT.md"})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !d.IsEmpty() {
		t.Errorf("expected IsEmpty after push (content-hash based), got %+v", d)
	}
}

// TestPull_RemovesEmptyParentDirsButKeepsWhitelistRoot 验证 pull 镜像删除本地文件后,
// 变空的中间目录被一并清理(否则工作空间面板会显示空壳目录),
// 但白名单根目录本身必须保留。
func TestPull_RemovesEmptyParentDirsButKeepsWhitelistRoot(t *testing.T) {
	home := t.TempDir()
	r := newDiffTestRepo(t)

	// 本地独有 → pull 时应删除;删完后 weather/ 变空也应清理
	makeFile(t, home, "skills/weather/SKILL.md", "local only")

	m := NewSyncManager(home, r)
	if err := m.Pull(nil); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	if _, err := os.Stat(filepath.Join(home, "skills", "weather", "SKILL.md")); !os.IsNotExist(err) {
		t.Fatalf("file should be deleted, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "skills", "weather")); !os.IsNotExist(err) {
		t.Fatalf("empty dir skills/weather should be cleaned, stat err = %v", err)
	}
	// 白名单根目录必须保留
	if _, err := os.Stat(filepath.Join(home, "skills")); err != nil {
		t.Fatalf("whitelist root skills/ must be kept: %v", err)
	}
}

// TestPull_KeepsNonEmptyParentDir 验证目录内还有同步范围外的文件时不清理该目录。
//
// 用 subagents/ 下的具体文件而非 skills/ 下的:ValidateSyncPath 禁止直接指定
// skill 目录内的单个文件(必须整目录操作),所以 skills/weather/SKILL.md
// 作为 Pull 参数会直接被校验拦下,测不到清理逻辑。
func TestPull_KeepsNonEmptyParentDir(t *testing.T) {
	home := t.TempDir()
	r := newDiffTestRepo(t)

	makeFile(t, home, "subagents/db-agent/agent.md", "local only")
	makeFile(t, home, "subagents/db-agent/keep.md", "sibling")

	// 只拉取被删的那个文件,兄弟文件不在同步范围内时目录应保留
	m := NewSyncManager(home, r)
	if err := m.Pull([]string{"subagents/db-agent/agent.md"}); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	if _, err := os.Stat(filepath.Join(home, "subagents", "db-agent", "agent.md")); !os.IsNotExist(err) {
		t.Fatalf("file should be deleted, stat err = %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "subagents", "db-agent")); err != nil {
		t.Fatalf("dir with remaining sibling must be kept: %v", err)
	}
}

// TestPull_NeverRemovesHomeDir 是最坏情况的防线:直接位于 HOME 根下的白名单文件
// 被镜像删除后,HOME 目录自身绝不能被清理掉(清理逻辑写错会删掉整个 ~/.groot)。
func TestPull_NeverRemovesHomeDir(t *testing.T) {
	home := t.TempDir()
	r := newDiffTestRepo(t)

	// GROOT.md 位于 HOME 根下,删除后 HOME 变空
	makeFile(t, home, "GROOT.md", "local only")

	m := NewSyncManager(home, r)
	if err := m.Pull(nil); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	if _, err := os.Stat(filepath.Join(home, "GROOT.md")); !os.IsNotExist(err) {
		t.Fatalf("file should be deleted, stat err = %v", err)
	}
	info, err := os.Stat(home)
	if err != nil {
		t.Fatalf("HOME dir must never be removed: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("HOME must remain a directory")
	}
}

// TestPull_RemovesNestedEmptyDirsUpToWhitelistRoot 验证清理是逐级向上进行的,
// 而不是只删一层:subagents/db-agent/skills/sql/ 下的文件被镜像删除后,
// sql/、skills/、db-agent/ 三级空目录都应清理,白名单根 subagents/ 保留。
//
// 这条路径同时是「清理到 HOME 边界」的场景,所以末尾也断言 HOME 仍存在:
// 逐级向上时 HOME 是靠白名单根判据保住的,与 TestPull_NeverRemovesHomeDir
// 覆盖的「首轮即到 HOME」短路径不是同一条防线。
func TestPull_RemovesNestedEmptyDirsUpToWhitelistRoot(t *testing.T) {
	home := t.TempDir()
	r := newDiffTestRepo(t)

	makeFile(t, home, "subagents/db-agent/skills/sql/SKILL.md", "local only")

	// Pull(nil) 走全白名单,绕过显式路径校验
	m := NewSyncManager(home, r)
	if err := m.Pull(nil); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	for _, rel := range []string{
		"subagents/db-agent/skills/sql/SKILL.md",
		"subagents/db-agent/skills/sql",
		"subagents/db-agent/skills",
		"subagents/db-agent",
	} {
		p := filepath.Join(home, filepath.FromSlash(rel))
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Errorf("%s should be cleaned, stat err = %v", rel, err)
		}
	}
	// 白名单根目录必须保留
	if _, err := os.Stat(filepath.Join(home, "subagents")); err != nil {
		t.Fatalf("whitelist root subagents/ must be kept: %v", err)
	}
	// HOME 永不删除
	info, err := os.Stat(home)
	if err != nil {
		t.Fatalf("HOME dir must never be removed: %v", err)
	}
	if !info.IsDir() {
		t.Fatal("HOME must remain a directory")
	}
}

// TestSyncManager_Pull_IdempotentAfterPull 验证 pull 后 diff 也 IsEmpty。
func TestSyncManager_Pull_IdempotentAfterPull(t *testing.T) {
	mgr, homeDir, remoteDir := newTestManager(t)

	// 远端有文件,本地没有
	makeFile(t, remoteDir, "GROOT.md", "# v1\n")

	if err := mgr.Pull([]string{"GROOT.md"}); err != nil {
		t.Fatalf("Pull: %v", err)
	}

	// pull 完立即 diff,应当 IsEmpty
	d, err := mgr.Diff([]string{"GROOT.md"})
	if err != nil {
		t.Fatalf("Diff: %v", err)
	}
	if !d.IsEmpty() {
		t.Errorf("expected IsEmpty after pull (content-hash based), got %+v", d)
	}
	_ = homeDir
}
