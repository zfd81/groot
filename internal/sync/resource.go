package sync

import (
	"fmt"
	"strings"
)

// SyncableResourceRoots 是受 sync 管理的根路径白名单。
// env.yaml / memory / schedules / cluster 不在此列。
var SyncableResourceRoots = []string{
	"config.yaml",
	"skills",
	"subagents",
	"mcp",
	"GROOT.md",
}

// ValidateSyncPath 校验用户指定的 path 是否在白名单范围内且符合资源对象操作规则:
//   - 必须以白名单根为前缀
//   - 不允许直接操作 skills/{name}/SKILL.md (必须操作整个 skill 目录)
//   - 不允许操作 env.yaml 等黑名单路径
func ValidateSyncPath(path string) error {
	if path == "" {
		return fmt.Errorf("sync: empty path")
	}
	// 路径遍历防护
	if strings.Contains(path, "..") {
		return fmt.Errorf("sync: path traversal not allowed: %q", path)
	}

	// 校验白名单根
	matched := false
	for _, root := range SyncableResourceRoots {
		if path == root || strings.HasPrefix(path, root+"/") {
			matched = true
			break
		}
	}
	if !matched {
		return fmt.Errorf("sync: path %q is not in sync whitelist", path)
	}

	// 禁止直接操作 skill 目录内的单个文件:
	//   skills/{name}/XXX → 必须操作 skills/{name}
	//   subagents/{sa}/skills/{name}/XXX → 同上
	if isDirectSkillFile(path) {
		return fmt.Errorf("sync: path %q is inside a skill directory — operate the skill directory instead (e.g. %q)",
			path, parentSkillDir(path))
	}

	return nil
}

// isDirectSkillFile 判断 path 是否是 skill 目录下的具体文件:
//
//	skills/{skill}/{file}  (depth >= 3 且 prefix = "skills/")
//	subagents/{sa}/skills/{skill}/{file} (depth >= 5 且含 "skills/")
func isDirectSkillFile(path string) bool {
	parts := strings.Split(path, "/")
	switch {
	case len(parts) >= 3 && parts[0] == "skills":
		// skills/weather/SKILL.md — 深度 3,文件在 skill 目录里
		return true
	case len(parts) >= 5 && parts[0] == "subagents" && parts[2] == "skills":
		// subagents/db-agent/skills/sql/SKILL.md
		return true
	}
	return false
}

// parentSkillDir 返回 skill 目录路径(去掉末尾文件名)。
func parentSkillDir(path string) string {
	parts := strings.Split(path, "/")
	switch {
	case len(parts) >= 3 && parts[0] == "skills":
		return strings.Join(parts[:2], "/")
	case len(parts) >= 5 && parts[0] == "subagents" && parts[2] == "skills":
		return strings.Join(parts[:4], "/")
	}
	return path
}
