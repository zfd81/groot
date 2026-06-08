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
// direction 为 "push" 或 "pull"。
func RenderDiff(w io.Writer, d DiffResult, direction string) {
	if d.IsEmpty() {
		fmt.Fprintln(w, "No differences found — already in sync.")
		return
	}

	arrow := "HOME → MinIO"
	verb := "push"
	if direction == "pull" {
		arrow = "MinIO → HOME"
		verb = "pull"
	}
	fmt.Fprintf(w, "\nChanges to %s (%s):\n", verb, arrow)

	if len(d.Added) > 0 {
		fmt.Fprintln(w, "  Added:")
		for _, f := range d.Added {
			fmt.Fprintf(w, "    %s\n", f)
		}
	}
	if len(d.Modified) > 0 {
		fmt.Fprintln(w, "  Modified:")
		for _, f := range d.Modified {
			fmt.Fprintf(w, "    %s\n", f)
		}
	}
	if len(d.Removed) > 0 {
		fmt.Fprintln(w, "  Removed:")
		for _, f := range d.Removed {
			fmt.Fprintf(w, "    %s\n", f)
		}
	}

	// pull 时如果涉及需重启的资源,给出提示
	if direction == "pull" {
		allChanged := append(append(d.Added, d.Modified...), d.Removed...)
		if anyNeedsRestart(allChanged) {
			fmt.Fprintln(w, "\n⚠  Some resources require a service restart to take effect:")
			fmt.Fprintln(w, "   config.yaml, mcp configs, subagent entry files (agent.md).")
			fmt.Fprintln(w, "   Please restart groot after pull completes.")
		}
	}
	fmt.Fprintln(w)
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
