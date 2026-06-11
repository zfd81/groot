package sync

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/zfd81/groot/internal/repo"
)

// DiffResult 描述本地 HOME 与远端 DB 之间的差异,以相对路径表示。
type DiffResult struct {
	Added    []string // 本地有,远端没有
	Modified []string // 双侧都有但 size 或 content_hash 不同
	Removed  []string // 远端有,本地没有
	Same     []string // 一致
}

// IsEmpty 返回是否没有任何差异。
func (d DiffResult) IsEmpty() bool {
	return len(d.Added)+len(d.Modified)+len(d.Removed) == 0
}

// localFileInfo 保存本地文件的元数据用于 diff 比较。
type localFileInfo struct {
	size int64
	hash string // SHA-1 hex
}

// ComputeDiff 对 paths(相对于 localBase 的相对路径列表)进行双侧 diff。
// 每个 path 如果是目录则递归展开其下所有文件再比较。
// localBase 必须是绝对路径(本地 HOME 下某目录)。
// 比较维度:size + content_hash(SHA-1),不再依赖 mtime。
func ComputeDiff(r repo.ResourceRepo, localBase string, paths []string) (DiffResult, error) {
	var result DiffResult

	for _, rel := range paths {
		localPath := filepath.Join(localBase, filepath.FromSlash(rel))

		localFiles, err := walkLocalFiles(localPath, localBase)
		if err != nil {
			return result, fmt.Errorf("sync diff: scan local %s: %w", rel, err)
		}

		remoteEntries, err := r.List(context.Background(), rel)
		if err != nil && !errors.Is(err, repo.ErrNotFound) {
			return result, fmt.Errorf("sync diff: list remote %s: %w", rel, err)
		}

		remoteMap := make(map[string]*repo.ResourceEntry, len(remoteEntries))
		for _, e := range remoteEntries {
			remoteMap[e.Path] = e
		}

		for relPath, localInfo := range localFiles {
			if remote, ok := remoteMap[relPath]; ok {
				if localInfo.size != remote.Size || localInfo.hash != remote.ContentHash {
					result.Modified = append(result.Modified, relPath)
				} else {
					result.Same = append(result.Same, relPath)
				}
				delete(remoteMap, relPath)
			} else {
				result.Added = append(result.Added, relPath)
			}
		}
		for relPath := range remoteMap {
			result.Removed = append(result.Removed, relPath)
		}
	}
	return result, nil
}

// walkLocalFiles 遍历 localPath 下所有文件(递归),返回相对于 base 的路径和元数据。
// 如果 localPath 本身是文件,只返回一个元素。
// 如果 localPath 不存在,返回空 map(非错误)。
//
// 跳过 *.tmp 文件——它们是 sync 工具自己用作原子写中转的临时产物。
func walkLocalFiles(localPath, localBase string) (map[string]localFileInfo, error) {
	files := make(map[string]localFileInfo)
	info, err := os.Stat(localPath)
	if os.IsNotExist(err) {
		return files, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		if strings.HasSuffix(localPath, ".tmp") {
			return files, nil
		}
		content, err := os.ReadFile(localPath)
		if err != nil {
			return nil, err
		}
		rel, _ := filepath.Rel(localBase, localPath)
		h := sha1.Sum(content)
		files[filepath.ToSlash(rel)] = localFileInfo{
			size: int64(len(content)),
			hash: fmt.Sprintf("%x", h),
		}
		return files, nil
	}
	return files, filepath.WalkDir(localPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(path, ".tmp") {
			return nil
		}
		content, e := os.ReadFile(path)
		if e != nil {
			return nil
		}
		rel, _ := filepath.Rel(localBase, path)
		h := sha1.Sum(content)
		files[filepath.ToSlash(rel)] = localFileInfo{
			size: int64(len(content)),
			hash: fmt.Sprintf("%x", h),
		}
		return nil
	})
}

// joinPath 拼接 base + rel,统一用 "/" 分隔符。
// os.* 调用方再做 filepath.FromSlash 转换。
func joinPath(base, rel string) string {
	if base == "" {
		return rel
	}
	return base + "/" + rel
}
