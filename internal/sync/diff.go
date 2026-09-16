package sync

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/zfd81/groot/internal/repo"
)

// RemoteMeta 保存远端记录的元信息,供调用方区分「远端从无记录」与
// 「远端曾有此文件但已被删除」,以及展示远端最后变更时间。
type RemoteMeta struct {
	Deleted   bool
	UpdatedAt time.Time
}

// DiffResult 描述本地 HOME 与远端 DB 之间的差异,以相对路径表示。
type DiffResult struct {
	Added    []string // 本地有,远端没有有效记录
	Modified []string // 双侧都有但 size 或 content_hash 不同
	Removed  []string // 远端有,本地没有
	Same     []string // 一致

	// Remote 按相对路径保存远端记录元信息;仅包含远端存在记录的路径
	// (含已删除记录)。本地新建的文件不在其中。
	//
	// 值是值类型,查不到时返回零值 RemoteMeta{Deleted:false},语义上恰好正确:
	// 「本地新建」与「远端有活记录」都不属于「远端已删除」。因此调用方可直接写
	// d.Remote[p].Deleted,不必检查 ok。不要改成 map[string]*RemoteMeta,
	// 那会把这个安全的零值变成 nil 解引用。
	Remote map[string]RemoteMeta
}

// IsEmpty 返回是否没有任何差异。
func (d DiffResult) IsEmpty() bool {
	return len(d.Added)+len(d.Modified)+len(d.Removed) == 0
}

// sha1Hex 计算内容的 SHA-1 十六进制摘要。
// 这是 sync 模块的内容指纹算法：ComputeDiff 用它算本地侧指纹，pushOne 用它填
// Resource.ContentHash，两处必须同源，否则推送后的比较会永远判为 Modified。
func sha1Hex(content []byte) string {
	h := sha1.Sum(content)
	return fmt.Sprintf("%x", h)
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
	result.Remote = make(map[string]RemoteMeta)

	for _, rel := range paths {
		localPath := filepath.Join(localBase, filepath.FromSlash(rel))

		localFiles, err := walkLocalFiles(localPath, localBase)
		if err != nil {
			return result, fmt.Errorf("sync diff: scan local %s: %w", rel, err)
		}

		// 用 ListWithDeleted:已删除记录参与比较,才能把「他人删了、本地还留着」
		// 判为 Added,而不是误判为本地新建。
		remoteEntries, err := r.ListWithDeleted(context.Background(), rel)
		if err != nil && !errors.Is(err, repo.ErrNotFound) {
			return result, fmt.Errorf("sync diff: list remote %s: %w", rel, err)
		}

		remoteMap := make(map[string]*repo.ResourceEntry, len(remoteEntries))
		for _, e := range remoteEntries {
			// 远端也要过滤:数据库里可能有早先版本推上去的 .DS_Store 等记录,
			// 若只过滤本地侧,它们会被判成「数据库独有」并由 pull 写回本地。
			if IsIgnoredPath(e.Path) {
				continue
			}
			remoteMap[e.Path] = e
			result.Remote[e.Path] = RemoteMeta{
				Deleted:   e.Status == repo.ResourceStatusDeleted,
				UpdatedAt: e.UpdatedAt,
			}
		}

		for relPath, localInfo := range localFiles {
			remote, ok := remoteMap[relPath]
			switch {
			case !ok || remote.Status == repo.ResourceStatusDeleted:
				// 远端无记录,或记录已删除 → 本地独有
				result.Added = append(result.Added, relPath)
			case localInfo.size != remote.Size || localInfo.hash != remote.ContentHash:
				result.Modified = append(result.Modified, relPath)
			default:
				result.Same = append(result.Same, relPath)
			}
			delete(remoteMap, relPath)
		}
		// remoteMap 剩下的是本地不存在的路径(已配对项在上一个循环里被删掉)
		for relPath, remote := range remoteMap {
			// 已删除记录且本地也不存在 → 双方一致,不是差异
			if remote.Status == repo.ResourceStatusDeleted {
				continue
			}
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
		if IsIgnoredPath(localPath) {
			return files, nil
		}
		content, err := os.ReadFile(localPath)
		if err != nil {
			return nil, err
		}
		rel, _ := filepath.Rel(localBase, localPath)
		files[filepath.ToSlash(rel)] = localFileInfo{
			size: int64(len(content)),
			hash: sha1Hex(content),
		}
		return files, nil
	}
	return files, filepath.WalkDir(localPath, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || IsIgnoredPath(path) {
			return nil
		}
		content, e := os.ReadFile(path)
		if e != nil {
			return nil
		}
		rel, _ := filepath.Rel(localBase, path)
		files[filepath.ToSlash(rel)] = localFileInfo{
			size: int64(len(content)),
			hash: sha1Hex(content),
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
