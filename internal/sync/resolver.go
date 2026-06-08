package sync

import (
	"fmt"
	"os"
	"path/filepath"
)

// ResolveLocalPaths 把用户输入的 paths(相对于 homeDir)展开为资源对象相对路径列表。
//
//   - paths 为 nil 时默认展开白名单内所有已存在的资源对象
//   - "skills" → 展开为 ["skills/weather", "skills/translator", ...]
//   - "skills/weather" → 直接返回 ["skills/weather"](目录资源对象)
//   - "config.yaml" → 直接返回 ["config.yaml"](文件资源对象)
//   - "subagents/db-agent" → 直接返回 ["subagents/db-agent"](递归目录)
//   - "subagents/db-agent/agent.md" → 直接返回 ["subagents/db-agent/agent.md"](文件)
//   - 直接操作 skill 目录内单文件 → 返回错误(如 "skills/weather/SKILL.md")
func ResolveLocalPaths(homeDir string, paths []string) ([]string, error) {
	if len(paths) == 0 {
		return resolveAll(homeDir)
	}
	var result []string
	for _, p := range paths {
		if err := ValidateSyncPath(p); err != nil {
			return nil, err
		}
		expanded, err := expandPath(homeDir, p)
		if err != nil {
			return nil, err
		}
		result = append(result, expanded...)
	}
	return result, nil
}

// resolveAll 展开白名单内所有已存在于 homeDir 的资源对象。
func resolveAll(homeDir string) ([]string, error) {
	var result []string
	for _, root := range SyncableResourceRoots {
		expanded, err := expandPath(homeDir, root)
		if err != nil {
			return nil, err
		}
		result = append(result, expanded...)
	}
	return result, nil
}

// expandPath 展开单个相对路径为一个或多个资源对象路径。
// 对于类别目录(skills / mcp / subagents),列出其下直接子项作为资源对象。
// 对于其他目录/文件,直接返回该路径(若存在)。
func expandPath(homeDir, rel string) ([]string, error) {
	abs := filepath.Join(homeDir, filepath.FromSlash(rel))

	info, err := os.Stat(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil // 不存在视为空(推送时远端可能有,pull 时需)
		}
		return nil, fmt.Errorf("sync resolver: stat %s: %w", rel, err)
	}

	// 类别目录展开:skills / mcp / subagents 列一级子项
	if info.IsDir() && isCategoryDir(rel) {
		return listCategoryChildren(abs, rel)
	}
	// 其他:直接返回
	return []string{rel}, nil
}

// isCategoryDir 判断是否是需要"展开一级子项"的类别目录。
func isCategoryDir(rel string) bool {
	return rel == "skills" || rel == "mcp" || rel == "subagents"
}

// listCategoryChildren 列出 abs 下直接子项的相对路径。
func listCategoryChildren(abs, relBase string) ([]string, error) {
	entries, err := os.ReadDir(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("sync resolver: read dir %s: %w", relBase, err)
	}
	var result []string
	for _, e := range entries {
		child := relBase + "/" + e.Name()
		result = append(result, child)
	}
	return result, nil
}
