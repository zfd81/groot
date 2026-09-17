// internal/webfiles/scaffold.go
package webfiles

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// scaffoldNameRe 创建名称：ASCII 字母开头（不能以数字开头），之后仅字母、数字、下划线、连字符，≤64 字符。
// 比 validName 更严——名称会成为 skill/mcp/agent 的标识符。
var scaffoldNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]{0,63}$`)

const skillTemplate = `---
name: %q
description: ""
---

# %s
`

const mcpTemplate = `{
  "name": %q,
  "type": "stdio",
  "description": "",
  "isActive": false,
  "command": "",
  "args": []
}
`

const agentTemplate = `---
description: ""
---
<!-- 填写 description 后该 Agent 才会生效 -->
`

// Scaffold 按模板创建 skill / mcp / agent，返回主文件的相对路径。
//
// agent 为空时资源归主 Agent：skills/<name>、mcp/<name>.json、subagents/<name>。
// agent 非空时 skill / mcp 创建到该子 Agent 目录下：
// subagents/<agent>/skills/<name>、subagents/<agent>/mcp/<name>.json，
// 与子 Agent 加载器（internal/agent/subagent_registry.go）按固定名称查找的目录一致。
//
// 目标已存在返回 ErrExists；名称或类型非法返回 ErrInvalid；
// kind 为 agent 时不接受 agent 参数（子 Agent 不能嵌套）返回 ErrInvalid；
// 指定的子 Agent 目录不存在返回 ErrNotFound。
func (s *Service) Scaffold(kind, name, agent string) (string, error) {
	if !scaffoldNameRe.MatchString(name) {
		return "", ErrInvalid
	}
	base, err := s.scaffoldBase(kind, agent)
	if err != nil {
		return "", err
	}
	switch kind {
	case "skill":
		dir := base + "skills/" + name
		abs, _, err := s.res.Resolve(dir)
		if err != nil {
			return "", err
		}
		content := fmt.Sprintf(skillTemplate, name, name)
		if err := scaffoldDir(abs, nil, "SKILL.md", content); err != nil {
			return "", err
		}
		return dir + "/SKILL.md", nil
	case "mcp":
		rel := base + "mcp/" + name + ".json"
		abs, _, err := s.res.Resolve(rel)
		if err != nil {
			return "", err
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return "", err
		}
		// O_EXCL 原子化"不存在才创建"，避免 Lstat 与创建之间的竞态。
		f, err := os.OpenFile(abs, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
		if err != nil {
			if os.IsExist(err) {
				return "", ErrExists
			}
			return "", err
		}
		if _, err := f.WriteString(fmt.Sprintf(mcpTemplate, name)); err != nil {
			f.Close()
			os.Remove(abs)
			return "", err
		}
		if err := f.Close(); err != nil {
			os.Remove(abs)
			return "", err
		}
		return rel, nil
	case "agent":
		dir := "subagents/" + name
		abs, _, err := s.res.Resolve(dir)
		if err != nil {
			return "", err
		}
		if err := scaffoldDir(abs, []string{"mcp", "skills"}, "agent.md", agentTemplate); err != nil {
			return "", err
		}
		return dir + "/agent.md", nil
	default:
		return "", ErrInvalid
	}
}

// scaffoldBase 返回资源目录的相对前缀："" 表示主 Agent（home 根），
// 非空形如 "subagents/<agent>/"。子 Agent 名称沿用 scaffoldNameRe 校验，
// 天然排除路径分隔符与 ".."；子 Agent 目录必须已存在且是目录。
func (s *Service) scaffoldBase(kind, agent string) (string, error) {
	if agent == "" {
		return "", nil
	}
	if kind == "agent" || !scaffoldNameRe.MatchString(agent) {
		return "", ErrInvalid
	}
	abs, _, err := s.res.Resolve("subagents/" + agent)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		return "", ErrNotFound
	}
	return "subagents/" + agent + "/", nil
}

// scaffoldDir 原子认领目录 abs（os.Mkdir，已存在 → ErrExists），
// 再创建子目录 subs 并写入主文件；认领后任何失败回滚删除整个目录。
func scaffoldDir(abs string, subs []string, mainFile, content string) error {
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		return err
	}
	// os.Mkdir 原子化"不存在才创建"，避免 Lstat 与创建之间的竞态。
	if err := os.Mkdir(abs, 0o755); err != nil {
		if os.IsExist(err) {
			return ErrExists
		}
		return err
	}
	// 目录归本次调用所有，后续失败回滚安全。
	for _, sub := range subs {
		if err := os.Mkdir(filepath.Join(abs, sub), 0o755); err != nil {
			os.RemoveAll(abs)
			return err
		}
	}
	if err := os.WriteFile(filepath.Join(abs, mainFile), []byte(content), 0o644); err != nil {
		os.RemoveAll(abs)
		return err
	}
	return nil
}
