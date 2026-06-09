package sync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	istorage "github.com/zfd81/groot/internal/storage"
)

// mtimeTolerance 是本地 mtime 与 MinIO LastModified 之间允许的精度误差。
const mtimeTolerance = time.Second

// DiffResult 描述本地 HOME 与 MinIO 之间的差异,以相对路径表示。
type DiffResult struct {
	Added    []string // 本地有,远端没有
	Modified []string // 双侧都有但内容/时间不同
	Removed  []string // 远端有,本地没有
	Same     []string // 一致
}

// IsEmpty 返回是否没有任何差异。
func (d DiffResult) IsEmpty() bool {
	return len(d.Added)+len(d.Modified)+len(d.Removed) == 0
}

// ComputeDiff 对 paths(相对于 localBase 和 remoteBase 的相对路径列表)进行双侧 diff。
// 每个 path 如果是目录则递归展开其下所有文件再比较。
// localBase 必须是绝对路径(本地 HOME 下某目录)。
// remoteBase 由 Storage 接口决定(local 模式为绝对路径,minio 模式为 object-key 前缀)。
// store 用于读取远端元数据。
func ComputeDiff(store istorage.Storage, localBase, remoteBase string, paths []string) (DiffResult, error) {
	var result DiffResult

	for _, rel := range paths {
		localPath := filepath.Join(localBase, filepath.FromSlash(rel))
		remotePath := joinPath(remoteBase, rel)

		localFiles, err := walkLocalFiles(localPath, localBase)
		if err != nil {
			return result, fmt.Errorf("sync diff: scan local %s: %w", rel, err)
		}
		remoteFiles, err := walkRemoteFiles(store, remotePath, remoteBase)
		if err != nil {
			return result, fmt.Errorf("sync diff: scan remote %s: %w", rel, err)
		}

		// 合并
		localMap := make(map[string]os.FileInfo)
		for _, f := range localFiles {
			localMap[f.relPath] = f.info
		}
		remoteMap := make(map[string]remoteFileInfo)
		for _, f := range remoteFiles {
			remoteMap[f.relPath] = f
		}

		for relPath, li := range localMap {
			ri, inRemote := remoteMap[relPath]
			if !inRemote {
				result.Added = append(result.Added, relPath)
				continue
			}
			if differsFromRemote(li, ri) {
				result.Modified = append(result.Modified, relPath)
			} else {
				result.Same = append(result.Same, relPath)
			}
		}
		for relPath := range remoteMap {
			if _, inLocal := localMap[relPath]; !inLocal {
				result.Removed = append(result.Removed, relPath)
			}
		}
	}
	return result, nil
}

// differsFromRemote 判断本地文件与远端文件是否不同(size 或 mtime 超出容差)。
func differsFromRemote(local os.FileInfo, remote remoteFileInfo) bool {
	if local.Size() != remote.size {
		return true
	}
	diff := local.ModTime().Sub(remote.mtime)
	if diff < 0 {
		diff = -diff
	}
	return diff > mtimeTolerance
}

// --- 本地遍历 ---

type localFile struct {
	relPath string
	info    os.FileInfo
}

// walkLocalFiles 遍历 absPath 下所有文件(递归),返回相对于 base 的路径。
// 如果 absPath 本身就是文件,只返回一个元素。
// 如果 absPath 不存在,返回空切片(非错误)。
//
// 跳过 *.tmp 文件——它们是 sync 工具自己用作原子写中转的临时产物,
// 不应被纳入 diff 视野(否则会被错误展示为差异、甚至被 push 推到远端)。
func walkLocalFiles(absPath, base string) ([]localFile, error) {
	var files []localFile
	info, err := os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		if isTmpFile(info.Name()) {
			return nil, nil
		}
		rel, _ := filepath.Rel(base, absPath)
		files = append(files, localFile{relPath: filepath.ToSlash(rel), info: info})
		return files, nil
	}
	err = filepath.Walk(absPath, func(path string, fi os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if fi.IsDir() {
			return nil
		}
		if isTmpFile(fi.Name()) {
			return nil
		}
		rel, _ := filepath.Rel(base, path)
		files = append(files, localFile{relPath: filepath.ToSlash(rel), info: fi})
		return nil
	})
	return files, err
}

// isTmpFile 判断文件名是否是 sync 工具的 *.tmp 中转文件。
func isTmpFile(name string) bool {
	return strings.HasSuffix(name, ".tmp")
}

// --- 远端遍历 ---

type remoteFileInfo struct {
	relPath string
	size    int64
	mtime   time.Time
}

// walkRemoteFiles 通过 Storage.List 递归列出 absRemotePath 下所有文件。
// 如果远端路径不存在,返回空切片(非错误)。
func walkRemoteFiles(store istorage.Storage, absRemotePath, remoteBase string) ([]remoteFileInfo, error) {
	ctx := context.Background()
	fi, err := store.Stat(ctx, absRemotePath)
	if err != nil {
		if errors.Is(err, istorage.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	if !fi.IsDir {
		rel := relPath(remoteBase, absRemotePath)
		return []remoteFileInfo{{relPath: rel, size: fi.Size, mtime: fi.ModTime}}, nil
	}
	return listRemoteRecursive(store, ctx, absRemotePath, remoteBase)
}

func listRemoteRecursive(store istorage.Storage, ctx context.Context, dir, base string) ([]remoteFileInfo, error) {
	entries, err := store.List(ctx, dir)
	if err != nil {
		if errors.Is(err, istorage.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	var files []remoteFileInfo
	for _, entry := range entries {
		if entry.IsDir {
			sub, err := listRemoteRecursive(store, ctx, entry.Path, base)
			if err != nil {
				return nil, err
			}
			files = append(files, sub...)
		} else {
			rel := relPath(base, entry.Path)
			files = append(files, remoteFileInfo{relPath: rel, size: entry.Size, mtime: entry.ModTime})
		}
	}
	return files, nil
}

// joinPath 拼接 base + rel,兼容 local(filepath.Join)和 minio(object key / 分隔)。
// 统一用 "/" 分隔符,os.* 调用方再做 filepath.FromSlash 转换。
func joinPath(base, rel string) string {
	if base == "" {
		return rel
	}
	return base + "/" + rel
}

// relPath 计算 path 相对于 base 的相对路径(统一 "/" 分隔符)。
func relPath(base, path string) string {
	if base == "" {
		return path
	}
	prefix := base + "/"
	if len(path) > len(prefix) && path[:len(prefix)] == prefix {
		return path[len(prefix):]
	}
	return path
}
