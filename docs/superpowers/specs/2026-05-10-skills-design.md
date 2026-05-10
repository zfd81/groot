# groot skills 命令设计文档

**Goal:** 为 groot 添加 Skills 管理子命令，支持列出、安装、卸载 Skill。

**Architecture:** 新增 `groot skills` 子命令，直接操作 `{GROOT_HOME}/skills/` 目录中的 Skill 子目录，不依赖运行中实例。

**Tech Stack:** Go

---

## 1. 命令用法

```
groot skills <子命令> [选项]
```

| 子命令 | 参数 | 说明 |
|--------|------|------|
| `list` | 无 | 列出所有已安装的 Skills |
| `install` | `<path>` | 安装 Skill（支持绝对/相对路径） |
| `uninstall` | `<name>` | 卸载 Skill |

---

## 2. 子命令详解

### 2.1 list - 列出已安装 Skills

扫描 `{GROOT_HOME}/skills/` 目录，以表格形式展示。

- 只识别子目录，忽略普通文件
- 读取每个子目录下的 `SKILL.md` 获取描述和最后修改时间
- 缺少 `SKILL.md` 的子目录标记为「⚠ 缺少 SKILL.md」
- 未安装任何 Skill 时显示「未安装任何 Skill」
- Skills 目录不存在时也显示「未安装任何 Skill」

输出格式：

```
NAME             LAST_UPDATED         DESCRIPTION
---------------  -------------------  ----------------------------------
web-search       2026-05-01 10:30     支持多搜索引擎的智能检索
my-skill         2026-05-09 08:12     我的自定义技能
broken-skill                          ⚠ 缺少 SKILL.md
```

列宽规则：
- NAME 列宽：根据最长 Skill 名称动态计算，上限 30
- LAST_UPDATED 列宽：固定 16
- DESCRIPTION 列宽：根据最长描述动态计算，上限 60（超出截断加 `...`）

### 2.2 install - 安装 Skill

将源目录拷贝到 `{GROOT_HOME}/skills/<目录名>/`。

安装流程：
1. 如果是相对路径，通过 `os.Getwd()` 转为绝对路径
2. 检查源路径存在且为目录
3. 检查源目录下存在 `SKILL.md`
4. 目标已存在时，先删除再拷贝（覆盖安装）
5. 递归拷贝所有文件和子目录，保留文件权限

### 2.3 uninstall - 卸载 Skill

删除 `{GROOT_HOME}/skills/<name>/` 目录。

- 检查目录存在
- 直接删除，无需确认
- 已删除时再次执行报错

---

## 3. SKILL.md 格式

每个 Skill 目录必须包含 `SKILL.md`，使用 YAML frontmatter：

```markdown
---
name: skill-name
description: "Skill 描述"
---
具体指令内容...
```

`description` 字段支持带引号和不带引号两种写法。

---

## 4. 错误处理

| 场景 | 处理 |
|------|------|
| 未知子命令 | 输出错误信息，exit 1 |
| install 缺少路径 | 输出错误信息，exit 1 |
| install 源路径不存在 | 输出错误信息，exit 1 |
| install 缺少 SKILL.md | 输出错误信息，exit 1 |
| uninstall 缺少名称 | 输出错误信息，exit 1 |
| uninstall Skill 不存在 | 输出错误信息，exit 1 |
| list 收到额外参数 | 输出错误信息，exit 1 |
| 未知 flag | 输出错误信息，exit 1 |

---

## 5. 文件结构

```
cmd/groot/main.go          # 修改：添加 skills 子命令分发
internal/cmd/
  ├── skills.go            # skills 命令核心实现
  └── skills_test.go       # 单元测试
```

---

## 6. 核心数据结构

### SkillsFlags

```go
type SkillsFlags struct {
    Subcommand string // list, install, uninstall
    Path       string // source path for install
    Name       string // skill name for uninstall
}
```

### skillItem

```go
type skillItem struct {
    name        string
    description string
    valid       bool
    lastUpdated string
}
```

---

## 7. 测试要点

| 测试项 | 验证内容 |
|--------|----------|
| 参数解析 | list/install/uninstall 正确解析 |
| 参数解析 | 无参数报错 |
| 参数解析 | 未知子命令报错 |
| 参数解析 | install 缺少路径报错 |
| 参数解析 | uninstall 缺少名称报错 |
| 参数解析 | install 多余参数报错 |
| 参数解析 | list 多余参数报错 |
| 参数解析 | 未知 flag 报错 |
| SKILL.md 解析 | 带引号描述正确提取 |
| SKILL.md 解析 | 不带引号描述正确提取 |
| SKILL.md 解析 | 无 description 字段返回空 |
| SKILL.md 解析 | 无 frontmatter 返回空 |
| list | 表格输出包含所有必要列 |
| list | 有效/无效 Skill 正确标记 |
| list | 空目录显示提示 |
| list | 不存在目录显示提示 |
| install | 目录和文件正确拷贝 |
| install | 覆盖安装旧文件被清除 |
| install | 源路径不存在报错 |
| install | 缺少 SKILL.md 报错 |
| uninstall | 目录正确删除 |
| uninstall | 不存在报错 |
| 集成测试 | install → list → uninstall → list 完整流程 |
