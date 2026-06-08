package sync

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/zfd81/groot/internal/storage"
)

// newTestManager 创建测试用 SyncManager:homeDir 和 remoteBase 都是 tmpdir。
func newTestManager(t *testing.T) (*localSyncManager, string, string) {
	t.Helper()
	homeDir := t.TempDir()
	remoteDir := t.TempDir()
	store := storage.NewLocal()
	mgr := NewSyncManager(homeDir, remoteDir, store).(*localSyncManager)
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
	// store 为 nil 或 remoteBase 为空时返回 disabled 实现
	mgr := NewSyncManager("", "", nil)
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
