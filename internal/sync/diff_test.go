package sync

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/resourcedb"
	"github.com/zfd81/groot/internal/repo/resourcelocal"
)

// newDiffTestRepo 创建基于 DB 的资源仓库。
// 软删除相关的 diff 测试必须用它,不能用同文件其他测试所用的 resourcelocal:
// 后者以文件系统为存储,删除即真实删除文件,ListWithDeleted 直接委托给 List,
// 不存在可标记的墓碑记录。用 resourcelocal 测软删除,Delete 后远端记录直接消失,
// Remote 永远是空 map,ComputeDiff 里的 Deleted 分支一行都执行不到——
// 测试会以完全错误的路径通过,虽绿但毫无防护力。
func newDiffTestRepo(t *testing.T) repo.ResourceRepo {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return resourcedb.New(sqlxDB, dialect)
}

// makeFile 在 dir 下创建文件,内容为 content,返回绝对路径。
func makeFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestComputeDiff_Added(t *testing.T) {
	localDir := t.TempDir()
	remoteDir := t.TempDir()

	makeFile(t, localDir, "config.yaml", "agent:\n  name: groot\n")

	r := resourcelocal.New(remoteDir)
	result, err := ComputeDiff(r, localDir, []string{"config.yaml"})
	if err != nil {
		t.Fatalf("ComputeDiff: %v", err)
	}
	if len(result.Added) != 1 || result.Added[0] != "config.yaml" {
		t.Errorf("expected Added=[config.yaml], got %+v", result)
	}
	if len(result.Modified) != 0 || len(result.Removed) != 0 {
		t.Errorf("unexpected changes: %+v", result)
	}
}

func TestComputeDiff_Removed(t *testing.T) {
	localDir := t.TempDir()
	remoteDir := t.TempDir()

	// 只在远端有文件
	makeFile(t, remoteDir, "GROOT.md", "# GROOT\n")

	r := resourcelocal.New(remoteDir)
	result, err := ComputeDiff(r, localDir, []string{"GROOT.md"})
	if err != nil {
		t.Fatalf("ComputeDiff: %v", err)
	}
	if len(result.Removed) != 1 || result.Removed[0] != "GROOT.md" {
		t.Errorf("expected Removed=[GROOT.md], got %+v", result)
	}
}

func TestComputeDiff_Same(t *testing.T) {
	localDir := t.TempDir()
	remoteDir := t.TempDir()

	content := "agent:\n  name: groot\n"
	makeFile(t, localDir, "config.yaml", content)
	makeFile(t, remoteDir, "config.yaml", content)

	r := resourcelocal.New(remoteDir)
	result, err := ComputeDiff(r, localDir, []string{"config.yaml"})
	if err != nil {
		t.Fatalf("ComputeDiff: %v", err)
	}
	if len(result.Same) != 1 {
		t.Errorf("expected Same=[config.yaml], got %+v", result)
	}
}

func TestComputeDiff_Modified_SizeDiff(t *testing.T) {
	localDir := t.TempDir()
	remoteDir := t.TempDir()

	makeFile(t, localDir, "GROOT.md", "# v2\n")
	makeFile(t, remoteDir, "GROOT.md", "# v1 (longer content)\n")

	r := resourcelocal.New(remoteDir)
	result, err := ComputeDiff(r, localDir, []string{"GROOT.md"})
	if err != nil {
		t.Fatalf("ComputeDiff: %v", err)
	}
	if len(result.Modified) != 1 {
		t.Errorf("expected Modified=[GROOT.md], got %+v", result)
	}
}

// TestComputeDiff_Modified_SameSizeDiffContent 验证 size 相同但内容不同时
// (hash 不同)判为 Modified。
func TestComputeDiff_Modified_SameSizeDiffContent(t *testing.T) {
	localDir := t.TempDir()
	remoteDir := t.TempDir()

	// 相同长度,不同内容 → hash 不同 → Modified
	makeFile(t, localDir, "GROOT.md", "aaaa\n")
	makeFile(t, remoteDir, "GROOT.md", "bbbb\n")

	r := resourcelocal.New(remoteDir)
	result, err := ComputeDiff(r, localDir, []string{"GROOT.md"})
	if err != nil {
		t.Fatalf("ComputeDiff: %v", err)
	}
	if len(result.Modified) != 1 {
		t.Errorf("expected Modified=[GROOT.md] (same size, diff hash), got %+v", result)
	}
}

func TestComputeDiff_RecursiveDir(t *testing.T) {
	localDir := t.TempDir()
	remoteDir := t.TempDir()

	makeFile(t, localDir, "skills/weather/SKILL.md", "weather skill\n")
	makeFile(t, localDir, "skills/weather/handler.go", "package main\n")

	r := resourcelocal.New(remoteDir)
	result, err := ComputeDiff(r, localDir, []string{"skills/weather"})
	if err != nil {
		t.Fatalf("ComputeDiff: %v", err)
	}
	if len(result.Added) != 2 {
		t.Errorf("expected 2 Added files, got %d: %v", len(result.Added), result.Added)
	}
	for _, f := range result.Added {
		if !strings.HasPrefix(f, "skills/weather/") {
			t.Errorf("unexpected path: %s", f)
		}
	}
}

// TestComputeDiff_SkipsTmpFiles 验证 ComputeDiff 跳过 *.tmp 残留文件。
// 它们是 sync 工具自己用作原子写中转的临时产物,不应被列入 diff
// (否则在 diff/pull 输出里会展示为"差异",甚至被 push 推到远端)。
func TestComputeDiff_SkipsTmpFiles(t *testing.T) {
	localDir := t.TempDir()
	remoteDir := t.TempDir()

	makeFile(t, localDir, "skills/weather/SKILL.md", "weather\n")
	makeFile(t, localDir, "skills/weather/SKILL.md.tmp", "stale residue\n")
	makeFile(t, localDir, "skills/weather/probe.tmp", "another residue\n")

	r := resourcelocal.New(remoteDir)
	result, err := ComputeDiff(r, localDir, []string{"skills"})
	if err != nil {
		t.Fatalf("ComputeDiff: %v", err)
	}
	if len(result.Added) != 1 || result.Added[0] != "skills/weather/SKILL.md" {
		t.Errorf("expected Added=[skills/weather/SKILL.md] (no .tmp), got %+v", result.Added)
	}
}

// TestComputeDiff_SkipsSystemFiles 验证系统生成的文件(.DS_Store 等)不进入 diff。
// 它们由 macOS/Windows 自动生成,不属于用户配置,若列入差异会污染同步清单
// 并被 push 推到数据库,再由其他机器 pull 下来。
func TestComputeDiff_SkipsSystemFiles(t *testing.T) {
	localDir := t.TempDir()
	remoteDir := t.TempDir()

	makeFile(t, localDir, "subagents/weather/agent.md", "weather\n")
	makeFile(t, localDir, "subagents/.DS_Store", "\x00finder\n")
	makeFile(t, localDir, "subagents/weather/.DS_Store", "\x00finder\n")
	makeFile(t, localDir, "subagents/weather/skills/.DS_Store", "\x00finder\n")
	makeFile(t, localDir, "subagents/weather/._agent.md", "\x00appledouble\n")

	r := resourcelocal.New(remoteDir)
	result, err := ComputeDiff(r, localDir, []string{"subagents"})
	if err != nil {
		t.Fatalf("ComputeDiff: %v", err)
	}
	if len(result.Added) != 1 || result.Added[0] != "subagents/weather/agent.md" {
		t.Errorf("expected Added=[subagents/weather/agent.md] only, got %+v", result.Added)
	}
}

// TestComputeDiff_SkipsSystemFilesOnRemote 验证远端已有的系统文件记录
// (早先版本推上去的脏数据)也被忽略,不判为 Removed。
// 若只过滤本地侧,这些记录会显示为「数据库独有」并被 pull 写回本地。
func TestComputeDiff_SkipsSystemFilesOnRemote(t *testing.T) {
	home := t.TempDir()
	r := newDiffTestRepo(t)
	ctx := context.Background()

	// 远端有一条正常记录和两条系统文件脏记录,本地目录为空。
	for _, p := range []string{"subagents/weather/agent.md", "subagents/.DS_Store", "subagents/weather/.DS_Store"} {
		if err := r.Put(ctx, &repo.Resource{Path: p, Content: []byte("x"), Size: 1}); err != nil {
			t.Fatalf("Put %s: %v", p, err)
		}
	}

	result, err := ComputeDiff(r, home, []string{"subagents"})
	if err != nil {
		t.Fatalf("ComputeDiff: %v", err)
	}
	if len(result.Removed) != 1 || result.Removed[0] != "subagents/weather/agent.md" {
		t.Errorf("expected Removed=[subagents/weather/agent.md] only, got %+v", result.Removed)
	}
	if _, ok := result.Remote["subagents/.DS_Store"]; ok {
		t.Error("被忽略的远端路径不应出现在 result.Remote 中")
	}
}

// TestComputeDiff_RemoteDeletedLocalPresentIsAdded 验证远端记录已被标记删除、
// 本地文件仍存在时判为 Added(他人删除了这个文件,本地还留着),
// 且差异项附带的远端元信息标明该记录已删除。
func TestComputeDiff_RemoteDeletedLocalPresentIsAdded(t *testing.T) {
	home := t.TempDir()
	r := newDiffTestRepo(t)
	ctx := context.Background()

	content := "local content"
	makeFile(t, home, "skills/weather/SKILL.md", content)
	if err := r.Put(ctx, &repo.Resource{
		Path: "skills/weather/SKILL.md", Content: []byte(content),
		Size: int64(len(content)), ContentHash: resourcedb.SHA1Hex([]byte(content)),
		UpdatedAt: time.UnixMilli(1757734800000),
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := r.Delete(ctx, "skills/weather/SKILL.md"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	d, err := ComputeDiff(r, home, []string{"skills"})
	if err != nil {
		t.Fatalf("ComputeDiff: %v", err)
	}
	if len(d.Added) != 1 || d.Added[0] != "skills/weather/SKILL.md" {
		t.Fatalf("Added = %v, want [skills/weather/SKILL.md]", d.Added)
	}
	if len(d.Modified) != 0 || len(d.Removed) != 0 {
		t.Fatalf("want only Added; got Modified=%v Removed=%v", d.Modified, d.Removed)
	}
	meta, ok := d.Remote["skills/weather/SKILL.md"]
	if !ok {
		t.Fatal("Remote metadata missing for tombstoned path")
	}
	if !meta.Deleted {
		t.Fatal("Remote[...].Deleted = false, want true")
	}
}

// TestComputeDiff_RemoteDeletedLocalAbsentIsInSync 验证远端记录已被标记删除、
// 本地也没有该文件时双方一致,不出现在任何差异分组中。
func TestComputeDiff_RemoteDeletedLocalAbsentIsInSync(t *testing.T) {
	home := t.TempDir()
	r := newDiffTestRepo(t)
	ctx := context.Background()

	if err := r.Put(ctx, &repo.Resource{
		Path: "skills/old/SKILL.md", Content: []byte("gone"), Size: 4,
		ContentHash: resourcedb.SHA1Hex([]byte("gone")), UpdatedAt: time.UnixMilli(1757734800000),
	}); err != nil {
		t.Fatalf("put: %v", err)
	}
	if err := r.Delete(ctx, "skills/old/SKILL.md"); err != nil {
		t.Fatalf("delete: %v", err)
	}

	d, err := ComputeDiff(r, home, []string{"skills"})
	if err != nil {
		t.Fatalf("ComputeDiff: %v", err)
	}
	if !d.IsEmpty() {
		t.Fatalf("want no differences; got Added=%v Modified=%v Removed=%v",
			d.Added, d.Modified, d.Removed)
	}
}

// TestComputeDiff_LocalOnlyHasNoRemoteMetadata 验证本地新建的文件(远端从无记录)
// 判为 Added,且不会被标记为远端已删除。
func TestComputeDiff_LocalOnlyHasNoRemoteMetadata(t *testing.T) {
	home := t.TempDir()
	r := newDiffTestRepo(t)

	makeFile(t, home, "subagents/db-agent/agent.md", "fresh")

	d, err := ComputeDiff(r, home, []string{"subagents"})
	if err != nil {
		t.Fatalf("ComputeDiff: %v", err)
	}
	if len(d.Added) != 1 {
		t.Fatalf("Added = %v, want 1 entry", d.Added)
	}
	if _, ok := d.Remote["subagents/db-agent/agent.md"]; ok {
		t.Fatal("locally-created file must not appear in Remote")
	}
	if len(d.Remote) != 0 {
		t.Fatalf("Remote = %v, want empty (remote has no records at all)", d.Remote)
	}
}

// TestComputeDiff_MultiPathRemoteMetaAccumulates 验证多个 path 的远端元信息累积合并
// (不被后续 path 覆盖),UpdatedAt 如实反映远端最后变更时间,
// 且活记录内容不同时判为 Modified 且不带删除标记。
func TestComputeDiff_MultiPathRemoteMetaAccumulates(t *testing.T) {
	home := t.TempDir()
	r := newDiffTestRepo(t)
	ctx := context.Background()

	const tombstoned = "skills/weather/SKILL.md"
	const live = "subagents/db-agent/agent.md"
	putAt := time.UnixMilli(1757734800000)

	// skills/ 下:远端墓碑 + 本地保留文件
	makeFile(t, home, tombstoned, "local content")
	if err := r.Put(ctx, &repo.Resource{
		Path: tombstoned, Content: []byte("remote content"), Size: 14,
		ContentHash: resourcedb.SHA1Hex([]byte("remote content")), UpdatedAt: putAt,
	}); err != nil {
		t.Fatalf("put tombstoned: %v", err)
	}
	if err := r.Delete(ctx, tombstoned); err != nil {
		t.Fatalf("delete: %v", err)
	}

	// subagents/ 下:远端活记录 + 本地内容不同
	makeFile(t, home, live, "local version")
	if err := r.Put(ctx, &repo.Resource{
		Path: live, Content: []byte("remote version"), Size: 14,
		ContentHash: resourcedb.SHA1Hex([]byte("remote version")), UpdatedAt: putAt,
	}); err != nil {
		t.Fatalf("put live: %v", err)
	}

	// 墓碑的 UpdatedAt 由 Delete 内部生成,无法预知,先读出来作为期望值。
	entries, err := r.ListWithDeleted(ctx, "skills")
	if err != nil {
		t.Fatalf("ListWithDeleted: %v", err)
	}
	var wantTombstoneTime time.Time
	for _, e := range entries {
		if e.Path == tombstoned {
			wantTombstoneTime = e.UpdatedAt
		}
	}
	if wantTombstoneTime.IsZero() {
		t.Fatalf("tombstone entry not found in %v", entries)
	}

	d, err := ComputeDiff(r, home, []string{"skills", "subagents"})
	if err != nil {
		t.Fatalf("ComputeDiff: %v", err)
	}

	if len(d.Added) != 1 || d.Added[0] != tombstoned {
		t.Fatalf("Added = %v, want [%s]", d.Added, tombstoned)
	}
	if len(d.Modified) != 1 || d.Modified[0] != live {
		t.Fatalf("Modified = %v, want [%s]", d.Modified, live)
	}
	if len(d.Removed) != 0 {
		t.Fatalf("Removed = %v, want empty", d.Removed)
	}

	// 两个 path 的元信息都要在,后一轮不得清掉前一轮。
	if len(d.Remote) != 2 {
		t.Fatalf("len(Remote) = %d, want 2; metadata from earlier paths was lost: %v",
			len(d.Remote), d.Remote)
	}

	tombMeta, ok := d.Remote[tombstoned]
	if !ok {
		t.Fatalf("Remote missing %s", tombstoned)
	}
	if !tombMeta.Deleted {
		t.Error("tombstoned path: Deleted = false, want true")
	}
	if !tombMeta.UpdatedAt.Equal(wantTombstoneTime) {
		t.Errorf("tombstoned UpdatedAt = %v, want %v", tombMeta.UpdatedAt, wantTombstoneTime)
	}

	liveMeta, ok := d.Remote[live]
	if !ok {
		t.Fatalf("Remote missing %s", live)
	}
	if liveMeta.Deleted {
		t.Error("live record: Deleted = true, want false")
	}
	if !liveMeta.UpdatedAt.Equal(putAt) {
		t.Errorf("live UpdatedAt = %v, want %v", liveMeta.UpdatedAt, putAt)
	}
}
