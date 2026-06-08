package sync

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	istorage "github.com/zfd81/groot/internal/storage"
)

// SyncManager 管理本地 HOME 与 MinIO 之间的集群共享配置同步。
// local 模式下不可用;minio 模式由 NewSyncManager 构造可用实例。
type SyncManager interface {
	Push(paths []string) error
	Pull(paths []string) error
	Diff(paths []string) (DiffResult, error)
}

// ErrSyncDisabled 表示当前未启用 minio 模式,sync 命令不可用。
var ErrSyncDisabled = errors.New("sync: minio 模式未启用 — 请在 env.yaml 中配置 minio 节")

// disabledSyncManager 是 local 模式下的空实现,所有方法返回 ErrSyncDisabled。
type disabledSyncManager struct{}

func (d *disabledSyncManager) Push(_ []string) error { return ErrSyncDisabled }
func (d *disabledSyncManager) Pull(_ []string) error { return ErrSyncDisabled }
func (d *disabledSyncManager) Diff(_ []string) (DiffResult, error) {
	return DiffResult{}, ErrSyncDisabled
}

// localSyncManager 是可用的 SyncManager 实现:本地侧走 os.*,远端侧走 Storage 接口。
type localSyncManager struct {
	homeDir    string // 本地 HOME 绝对路径
	remoteBase string // 远端 object-key 前缀(minio)或绝对路径(local 测试)
	store      istorage.Storage
}

// NewSyncManager 创建 SyncManager。
// store 为 nil 或 remoteBase 为空时返回 disabledSyncManager。
func NewSyncManager(homeDir, remoteBase string, store istorage.Storage) SyncManager {
	if store == nil || remoteBase == "" {
		return &disabledSyncManager{}
	}
	return &localSyncManager{homeDir: homeDir, remoteBase: remoteBase, store: store}
}

// resolveSyncPaths 校验用户输入并返回交给 ComputeDiff 的相对路径列表。
// 不做"类别目录展开"——sync 需要把目录整体交给 ComputeDiff,由它递归扫描双侧
// (local 与 remote)的并集,否则会漏掉只在远端存在的子项。
// paths 为空时默认使用全部白名单根。
func resolveSyncPaths(paths []string) ([]string, error) {
	if len(paths) == 0 {
		return append([]string(nil), SyncableResourceRoots...), nil
	}
	out := make([]string, 0, len(paths))
	for _, p := range paths {
		if err := ValidateSyncPath(p); err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// Diff 计算指定 paths 的本地 vs 远端差异。paths 为 nil 时比较全部白名单资源。
func (m *localSyncManager) Diff(paths []string) (DiffResult, error) {
	resolved, err := resolveSyncPaths(paths)
	if err != nil {
		return DiffResult{}, err
	}
	return ComputeDiff(m.store, m.homeDir, m.remoteBase, resolved)
}

// Push 把本地 paths 镜像推送到远端:本地新增/修改 → 写远端;远端多余 → 删远端。
// paths 为 nil 时处理白名单全部资源。
//
// 在 DiffResult 的"local vs remote"语义下:
//   - Added    本地有/远端没有 → 推送到远端
//   - Modified 双侧不同        → 推送到远端
//   - Removed  远端有/本地没有 → 从远端删除
func (m *localSyncManager) Push(paths []string) error {
	resolved, err := resolveSyncPaths(paths)
	if err != nil {
		return err
	}
	diff, err := ComputeDiff(m.store, m.homeDir, m.remoteBase, resolved)
	if err != nil {
		return err
	}
	ctx := context.Background()

	// 推送 Added / Modified 到远端。分两轮迭代,避免对底层数组的别名修改风险。
	for _, rel := range diff.Added {
		if err := m.pushOne(ctx, rel); err != nil {
			return err
		}
	}
	for _, rel := range diff.Modified {
		if err := m.pushOne(ctx, rel); err != nil {
			return err
		}
	}
	// 远端多余文件 → 删除(镜像)
	for _, rel := range diff.Removed {
		remotePath := joinPath(m.remoteBase, rel)
		if err := m.store.Delete(ctx, remotePath); err != nil && !errors.Is(err, istorage.ErrNotFound) {
			return fmt.Errorf("sync push delete %s: %w", rel, err)
		}
	}
	return nil
}

func (m *localSyncManager) pushOne(ctx context.Context, rel string) error {
	localPath := filepath.Join(m.homeDir, filepath.FromSlash(rel))
	remotePath := joinPath(m.remoteBase, rel)
	if err := pushFile(ctx, m.store, localPath, remotePath); err != nil {
		return fmt.Errorf("sync push %s: %w", rel, err)
	}
	return nil
}

// Pull 把远端 paths 镜像拉取到本地:先清 .tmp 残留;Phase A 写新增/修改;
// Phase B 删本地多余文件(必须严格在 A 全部成功后才进入)。
// paths 为 nil 时处理白名单全部资源。
//
// 在 DiffResult 的"local vs remote"语义下,Pull 的 Phase A / Phase B 来源恰好与
// Push 相反:
//   - Removed  远端有/本地没有 → Phase A 写本地(原子 tmp+rename)
//   - Modified 双侧不同        → Phase A 覆盖本地
//   - Added    本地有/远端没有 → Phase B 删本地(镜像)
//
// 顺序保证:任何中断点本地都至少有一份完整内容,不会出现"先删后写中途崩溃"的空窗。
func (m *localSyncManager) Pull(paths []string) error {
	resolved, err := resolveSyncPaths(paths)
	if err != nil {
		return err
	}

	// 清理 *.tmp 残留(上次 pull 崩溃留下)。best-effort,不阻塞 pull。
	if err := cleanTmpFiles(m.homeDir, resolved); err != nil {
		return fmt.Errorf("sync pull cleanup tmp: %w", err)
	}

	diff, err := ComputeDiff(m.store, m.homeDir, m.remoteBase, resolved)
	if err != nil {
		return err
	}
	ctx := context.Background()

	// Phase A: 写入 Removed + Modified (远端有/本地没有 + 双侧不同 → 从远端拉取覆盖本地)
	for _, rel := range diff.Removed {
		if err := m.pullOne(ctx, rel); err != nil {
			return err
		}
	}
	for _, rel := range diff.Modified {
		if err := m.pullOne(ctx, rel); err != nil {
			return err
		}
	}

	// Phase B: 删除 Added (本地有/远端没有 → 镜像删除本地)
	for _, rel := range diff.Added {
		localPath := filepath.Join(m.homeDir, filepath.FromSlash(rel))
		if err := os.Remove(localPath); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("sync pull delete %s: %w", rel, err)
		}
	}
	return nil
}

func (m *localSyncManager) pullOne(ctx context.Context, rel string) error {
	remotePath := joinPath(m.remoteBase, rel)
	localPath := filepath.Join(m.homeDir, filepath.FromSlash(rel))
	if err := pullFile(ctx, m.store, remotePath, localPath); err != nil {
		return fmt.Errorf("sync pull %s: %w", rel, err)
	}
	return nil
}

// --- 文件级 push/pull 操作 ---

// pushFile 把本地文件写到远端(通过 Storage 接口原子写)。
func pushFile(ctx context.Context, store istorage.Storage, localPath, remotePath string) error {
	f, err := os.Open(localPath)
	if err != nil {
		return err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return err
	}
	return store.Write(ctx, remotePath, f, info.Size(), "")
}

// pullFile 把远端文件写到本地,使用 tmp+rename 保证原子写。
func pullFile(ctx context.Context, store istorage.Storage, remotePath, localPath string) error {
	rc, err := store.Read(ctx, remotePath)
	if err != nil {
		return err
	}
	defer rc.Close()

	// 读取全部内容
	data, err := io.ReadAll(rc)
	if err != nil {
		return err
	}

	// 原子写:tmp → sync → rename
	if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
		return err
	}
	tmp := localPath + ".tmp"
	_ = os.Remove(tmp) // 清理孤儿 tmp
	f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	if _, err := f.Write(data); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Sync(); err != nil {
		f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, localPath)
}

// cleanTmpFiles 递归删除 homeDir 下 resolved paths 范围内的所有 *.tmp 文件。
func cleanTmpFiles(homeDir string, resolved []string) error {
	for _, rel := range resolved {
		root := filepath.Join(homeDir, filepath.FromSlash(rel))
		_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() {
				return nil
			}
			if strings.HasSuffix(d.Name(), ".tmp") {
				_ = os.Remove(path)
			}
			return nil
		})
	}
	return nil
}

// --- 交互确认 ---

// ConfirmContinue 在 stdout 显示提示并等待用户输入 y/Y/yes 后返回 true。
// 若 stdin 不是 tty 或用户输入其他内容,返回 false(取消)。
func ConfirmContinue(r io.Reader, w io.Writer) bool {
	fmt.Fprintf(w, "Continue? (y/n): ")
	scanner := bufio.NewScanner(r)
	if scanner.Scan() {
		ans := strings.TrimSpace(strings.ToLower(scanner.Text()))
		return ans == "y" || ans == "yes"
	}
	return false
}

// FormatDiff 把 DiffResult 以 string 返回(用于命令输出)。
func FormatDiff(d DiffResult, direction string) string {
	var buf bytes.Buffer
	RenderDiff(&buf, d, direction)
	return buf.String()
}
