package sync

import (
	"fmt"
	"io"
	"strings"
)

// needsRestartPaths 列出 pull 后需要重启服务才生效的路径前缀。
// 参考 spec §1.8.8。
var needsRestartPaths = []string{
	"config.yaml",
	"mcp/",
	"subagents/",
}

// RenderDiff 把 DiffResult 渲染为可读输出写入 w。
// direction 为 "push" / "pull" / "diff" 之一,决定 4 分组的语义:
//
//   - DiffResult.Added 永远是"本地有/远端没有"
//   - DiffResult.Removed 永远是"远端有/本地没有"
//
// push 命令:Added → 推到远端(标 Added);Removed → 从远端删(标 Removed)
// pull 命令:Added → 从本地删(标 "Removed locally");Removed → 拉到本地(标 "Added locally")
// diff 命令:用中性措辞("Local only" / "Remote only" / "Modified")
func RenderDiff(w io.Writer, d DiffResult, direction string) {
	if d.IsEmpty() {
		fmt.Fprintln(w, "No differences found — already in sync.")
		return
	}

	switch direction {
	case "pull":
		fmt.Fprintln(w, "\nChanges to pull (MinIO → HOME):")
		// pull 视角:本地多余 = 要删本地;双侧不同 = 拉远端覆盖本地;远端多余 = 拉到本地
		printGroup(w, "  Removed locally:", d.Added)
		printGroup(w, "  Modified locally (overwritten by remote):", d.Modified)
		printGroup(w, "  Added locally:", d.Removed)
	case "diff":
		fmt.Fprintln(w, "\nDifferences (HOME ↔ MinIO):")
		printGroup(w, "  Local only:", d.Added)
		printGroup(w, "  Modified (size or mtime differs):", d.Modified)
		printGroup(w, "  Remote only:", d.Removed)
	default: // "push"
		fmt.Fprintln(w, "\nChanges to push (HOME → MinIO):")
		printGroup(w, "  Added:", d.Added)
		printGroup(w, "  Modified:", d.Modified)
		printGroup(w, "  Removed:", d.Removed)
	}

	// pull 时如果涉及需重启的资源,给出提示
	if direction == "pull" {
		allChanged := append(append([]string(nil), d.Added...), d.Modified...)
		allChanged = append(allChanged, d.Removed...)
		if anyNeedsRestart(allChanged) {
			fmt.Fprintln(w, "\n⚠  Some resources require a service restart to take effect:")
			fmt.Fprintln(w, "   config.yaml, mcp configs, subagent entry files (agent.md).")
			fmt.Fprintln(w, "   Please restart groot after pull completes.")
		}
	}
	fmt.Fprintln(w)
}

func printGroup(w io.Writer, header string, items []string) {
	if len(items) == 0 {
		return
	}
	fmt.Fprintln(w, header)
	for _, f := range items {
		fmt.Fprintf(w, "    %s\n", f)
	}
}

// anyNeedsRestart 判断变更列表中是否含需重启的资源。
func anyNeedsRestart(paths []string) bool {
	for _, p := range paths {
		for _, prefix := range needsRestartPaths {
			if p == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(p, prefix) {
				return true
			}
		}
	}
	return false
}
