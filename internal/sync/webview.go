package sync

import "sort"

// WebDiffEntry 是一条差异记录,面向 Web 面板展示。
// Status 取值以本地为基准:
//   - "A" 本地有,远端无有效记录
//   - "M" 双侧都有但内容不同
//   - "D" 远端有,本地无
type WebDiffEntry struct {
	Path            string `json:"path"`
	Status          string `json:"status"`
	RemoteDeleted   bool   `json:"remoteDeleted"`
	NeedsRestart    bool   `json:"needsRestart"`
	RemoteUpdatedAt int64  `json:"remoteUpdatedAt,omitempty"` // 毫秒;远端无记录时为 0
}

// WebDiffView 是一次差异计算的完整结果。
type WebDiffView struct {
	InSync       bool           `json:"inSync"`
	NeedsRestart bool           `json:"needsRestart"`
	Entries      []WebDiffEntry `json:"entries"`
}

// BuildWebDiff 把 DiffResult 折成按路径升序的扁平清单。
// 一致的文件(DiffResult.Same)不进入结果——面板只展示需要决策的条目。
//
// Entries 初始化为空切片而非 nil:JSON 序列化时空切片是 [],nil 是 null,
// 前端处理 [] 更简单。
func BuildWebDiff(d DiffResult) WebDiffView {
	view := WebDiffView{
		InSync:  d.IsEmpty(),
		Entries: []WebDiffEntry{},
	}
	add := func(paths []string, status string) {
		for _, p := range paths {
			// 复用 CLI 渲染的同一个谓词,避免 CLI 与 Web 的重启规则漂移。
			restart := needsRestart(p)
			e := WebDiffEntry{
				Path:         p,
				Status:       status,
				NeedsRestart: restart,
			}
			// Remote 是值类型 map,查不到时零值恰好表示「远端没有删除记录」。
			meta := d.Remote[p]
			e.RemoteDeleted = meta.Deleted
			if !meta.UpdatedAt.IsZero() {
				e.RemoteUpdatedAt = meta.UpdatedAt.UnixMilli()
			}
			if restart {
				view.NeedsRestart = true
			}
			view.Entries = append(view.Entries, e)
		}
	}
	add(d.Added, "A")
	add(d.Modified, "M")
	add(d.Removed, "D")

	sort.Slice(view.Entries, func(i, j int) bool {
		return view.Entries[i].Path < view.Entries[j].Path
	})
	return view
}
