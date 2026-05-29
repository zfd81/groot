# Groot 子 Agent Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 Groot 上实现「主 Agent + 文件系统驱动的子 Agent」机制：通过 `{GROOT_HOME}/subagents/{name}/` 目录定义子 Agent，主 Agent 通过统一的 `call_agent` 工具调度子 Agent；同时支持通过 HTTP header `X-Agent-Name` 进入 Solo 模式直接执行子 Agent。

**Architecture:** 启动期一次性扫描 `subagents/` 构建 `SubAgentRegistry`（每个条目预构建 `mcp.Manager` + Skill Backend + `adk.NewTypedAgentTool(ChatModelAgent)`）。运行时主 Agent 工具列表包含单个 `CallAgentTool`，其 `InvokableRun` 委托给预构建的子 `entry.Tool`，全局 semaphore 控并发，事件透过 eino `EmitInternalEvents` 透传到父 Runner SSE。子 Agent ChatRecord 通过 chatID 前缀关联父子，Token 在主 Engine 事件循环按 `event.AgentName` 累加。设计文档：[`docs/superpowers/specs/2026-05-24-multi-agent-design.md`](../specs/2026-05-24-multi-agent-design.md)。

**Tech Stack:** Go 1.26, cloudwego/eino v0.9.0-beta.1 (`adk.NewTypedAgentTool` / `ToolsConfig.EmitInternalEvents`), eino-ext local backend + skill middleware, semaphore (`golang.org/x/sync/semaphore`), Hertz, mcp-go, fsnotify, Bubble Tea TUI。

---

## File Structure

```
新增文件:
internal/agent/
├── consts.go                       # MainAgentName 常量
├── subagent_md.go                  # agent.md frontmatter 解析
├── subagent_md_test.go
├── subagent_registry.go            # SubAgentRegistry + SubAgentEntry
├── subagent_registry_test.go
├── token_accumulators.go           # 子 Agent Token 累加器
├── token_accumulators_test.go
├── call_agent.go                   # CallAgentTool（请求级实例）
└── call_agent_test.go

internal/api/handler/
├── agents.go                       # GET /agents
└── agents_test.go

修改文件:
internal/agent/engine.go            # +agentName 字段; instruction 拼接分流; 事件循环按 event.AgentName 分流; ProgressCallback 加 agentName 首参; EmitInternalEvents
internal/agent/executor.go          # +SubAgentRegistry; 按 task.AgentName 分流 Solo/编排
internal/agent/runtime_state.go     # ChatProgress.SubAgents; AddSubAgent/RemoveSubAgent
internal/agent/sse.go               # WriteXxx 增加 agentName 字段
internal/api/handler/chat.go        # 解析 X-Agent-Name; Solo 模式 400 校验
internal/api/handler/skills.go      # 解析 X-Agent-Name; 从 Registry 取 Backend
internal/api/handler/tools.go       # 解析 X-Agent-Name; 从 Registry 取 MCPManager
internal/api/server.go              # /agents 路由
internal/api/types/                 # AgentInfo / AgentsResponse
internal/skills/watcher.go          # 监听 subagents/*/skills/; 按路径过滤
internal/memory/types.go            # ChatRecord + AgentName/PromptTokens/CompletionTokens/TotalTokens
internal/memory/idgen.go            # +GenerateChildChatID(parent, agentName)
internal/cmd/init.go                # 创建 subagents/; GROOT.md 追加调度引导段
internal/cmd/chat/                  # TUI /agent 命令
internal/config/config.go           # SubAgentConfig
internal/config/defaults.go         # 默认值 (MaxConcurrency=5, ExecTimeout=5m, MaxTaskLength=16000, MaxResultLength=8000)
internal/config/template.go         # config.yaml 模板
cmd/groot/main.go                   # 构建 SubAgentRegistry; shutdown hook 调用 Close()
```

---

## Phase 1: 基础设施（不改变运行时行为）

### Task 1: 引入 `MainAgentName` 常量并替换 `"GrootAgent"`

**Files:**
- Create: `internal/agent/consts.go`
- Modify: `internal/agent/engine.go:92`

- [ ] **Step 1: 创建常量文件**

```go
// internal/agent/consts.go
package agent

// MainAgentName 是主 Agent 的名字。
// 启动期扫描 subagents/ 时若发现同名目录会跳过并报错（保留主名独占）。
// 事件循环按 event.AgentName == MainAgentName 区分主/子 Agent 事件。
const MainAgentName = "groot"
```

- [ ] **Step 2: 替换 engine.go 中的 `"GrootAgent"`**

文件：`internal/agent/engine.go:92`，把硬编码的 `Name: "GrootAgent"` 改为 `Name: MainAgentName`：

```go
agentConfig := &adk.ChatModelAgentConfig{
    Name:          MainAgentName,
    Description:   "Groot AI Task Execution Agent",
    Instruction:   systemInstruction,
    ...
}
```

- [ ] **Step 3: 编译验证**

Run: `cd /Users/zhangfengda/workspace/groot && go build ./...`
Expected: 编译通过，无错误。

- [ ] **Step 4: 跑现有测试**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/agent/... -v`
Expected: 所有现有测试通过。

- [ ] **Step 5: Commit**

```bash
git add internal/agent/consts.go internal/agent/engine.go
git commit -m "$(cat <<'EOF'
refactor(agent): 引入 MainAgentName 常量

为后续主/子 Agent 事件分流做准备。统一替换硬编码 "GrootAgent" 为 MainAgentName="groot"，与 SubAgentRegistry 启动期同名校验保持一致。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

### Task 2: `GenerateChildChatID` 子 chatID 生成器

**Files:**
- Modify: `internal/memory/idgen.go`
- Test: `internal/memory/idgen_test.go`

格式：`{parentChatID}_{HHMMSSmmm}_{random4}_{agentName}`。例如父 `chat_20260524103000523` 加 `agentName=db-agent` 得到 `chat_20260524103000523_103001523_a3f8_db-agent`。

- [ ] **Step 1: 写失败的测试**

新建/追加 `internal/memory/idgen_test.go`：

```go
package memory

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestGenerateChildChatID_Format(t *testing.T) {
	parent := "chat_20260524103000523"
	got := GenerateChildChatID(parent, "db-agent")

	// 前缀必须是父 chatID + "_"
	if !strings.HasPrefix(got, parent+"_") {
		t.Fatalf("child chatID must start with parent+'_': got %q", got)
	}
	// 整体格式: chat_{14digits3}_{9digits}_{4lowerAlnum}_{agentName}
	re := regexp.MustCompile(`^chat_\d{17}_\d{9}_[a-z0-9]{4}_db-agent$`)
	if !re.MatchString(got) {
		t.Fatalf("child chatID format mismatch: %q", got)
	}
}

func TestGenerateChildChatID_UniqueWithinMillisecond(t *testing.T) {
	parent := "chat_20260524103000523"
	seen := make(map[string]struct{}, 1000)
	deadline := time.Now().Add(50 * time.Millisecond)
	for time.Now().Before(deadline) {
		id := GenerateChildChatID(parent, "x")
		if _, dup := seen[id]; dup {
			t.Fatalf("duplicate child chatID generated: %s", id)
		}
		seen[id] = struct{}{}
	}
	if len(seen) < 100 {
		t.Fatalf("too few samples generated (%d), test environment too slow?", len(seen))
	}
}
```

- [ ] **Step 2: 运行测试以确认失败**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/memory/ -run TestGenerateChildChatID -v`
Expected: FAIL，因 `GenerateChildChatID` 未定义。

- [ ] **Step 3: 实现**

在 `internal/memory/idgen.go` 末尾追加：

```go
// GenerateChildChatID 生成子 Agent 的 chatID。
// 格式: {parentChatID}_{HHMMSSmmm}_{random4}_{agentName}
// parentChatID 已含完整日期；子时间戳只保留 HHMMSSmmm；random4 避免同毫秒并发冲突。
func GenerateChildChatID(parentChatID, agentName string) string {
	now := time.Now()
	timeStr := now.Format("150405") + fmt.Sprintf("%03d", now.Nanosecond()/1000000)
	random := randomString(4)
	return fmt.Sprintf("%s_%s_%s_%s", parentChatID, timeStr, random, agentName)
}
```

- [ ] **Step 4: 运行测试以确认通过**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/memory/ -run TestGenerateChildChatID -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/memory/idgen.go internal/memory/idgen_test.go
git commit -m "$(cat <<'EOF'
feat(memory): 新增 GenerateChildChatID 子 Agent chatID 生成器

格式 {parentChatID}_{HHMMSSmmm}_{random4}_{agentName}，通过前缀关联父子调用关系，random4 避免同毫秒并发冲突。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

### Task 3: `ChatRecord` / `Message` 字段扩展

**Files:**
- Modify: `internal/memory/types.go`
- Test: `internal/memory/types_test.go`（新建）

按设计 8.2 节，`ChatRecord` 新增 `AgentName` / `PromptTokens` / `CompletionTokens` / `TotalTokens`。

- [ ] **Step 1: 写失败的测试**

新建 `internal/memory/types_test.go`：

```go
package memory

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestChatRecord_AgentNameSerialization(t *testing.T) {
	r := ChatRecord{
		ChatID:           "chat_x",
		AgentName:        "db-agent",
		PromptTokens:     100,
		CompletionTokens: 50,
		TotalTokens:      150,
	}
	data, err := json.Marshal(&r)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	s := string(data)
	for _, kv := range []string{
		`"agent_name":"db-agent"`,
		`"prompt_tokens":100`,
		`"completion_tokens":50`,
		`"total_tokens":150`,
	} {
		if !strings.Contains(s, kv) {
			t.Errorf("expected %s in JSON, got: %s", kv, s)
		}
	}
}

func TestChatRecord_AgentNameOmitemptyWhenZero(t *testing.T) {
	r := ChatRecord{ChatID: "chat_x"}
	data, _ := json.Marshal(&r)
	s := string(data)
	if strings.Contains(s, `"agent_name"`) {
		t.Errorf("agent_name should be omitted when empty, got: %s", s)
	}
	if strings.Contains(s, `"prompt_tokens"`) {
		t.Errorf("prompt_tokens should be omitted when zero, got: %s", s)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/memory/ -run TestChatRecord_Agent -v`
Expected: FAIL，因字段未定义。

- [ ] **Step 3: 修改 `internal/memory/types.go`**

在 `ChatRecord` 结构体内，在 `Error` 字段之前追加：

```go
// 多 Agent 扩展字段（v3.8）
AgentName        string `json:"agent_name,omitempty"`         // 使用的 Agent 名；主 Agent 通常省略
PromptTokens     int    `json:"prompt_tokens,omitempty"`      // LLM 输入 token 累加
CompletionTokens int    `json:"completion_tokens,omitempty"`  // LLM 输出 token 累加
TotalTokens      int    `json:"total_tokens,omitempty"`       // LLM token 总数累加
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/memory/ -v`
Expected: PASS（含其他既有 memory 测试）。

- [ ] **Step 5: Commit**

```bash
git add internal/memory/types.go internal/memory/types_test.go
git commit -m "$(cat <<'EOF'
feat(memory): ChatRecord 新增 AgentName 和 Token 累加字段

为子 Agent ChatRecord 区分 agent 来源、记录该 Agent 范围内多轮 ReAct 的 token 总量；主 Agent 主路径下字段省略，向后兼容。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

### Task 4: `SubAgentConfig` 配置项

**Files:**
- Modify: `internal/config/config.go`
- Modify: `internal/config/defaults.go`
- Modify: `internal/config/template.go`
- Test: `internal/config/config_test.go`

设计 4.4 节：`MaxConcurrency=5`、`ExecTimeout=5m`、`MaxTaskLength=16000`、`MaxResultLength=8000`。

- [ ] **Step 1: 写失败的测试**

追加 `internal/config/config_test.go`：

```go
func TestConfig_SubAgentDefaults(t *testing.T) {
	cfg := DefaultConfig()
	if cfg.SubAgent.MaxConcurrency != 5 {
		t.Errorf("expected MaxConcurrency=5, got %d", cfg.SubAgent.MaxConcurrency)
	}
	if cfg.SubAgent.ExecTimeout != "5m" {
		t.Errorf("expected ExecTimeout=5m, got %s", cfg.SubAgent.ExecTimeout)
	}
	if cfg.SubAgent.MaxTaskLength != 16000 {
		t.Errorf("expected MaxTaskLength=16000, got %d", cfg.SubAgent.MaxTaskLength)
	}
	if cfg.SubAgent.MaxResultLength != 8000 {
		t.Errorf("expected MaxResultLength=8000, got %d", cfg.SubAgent.MaxResultLength)
	}
}
```

> 如 `DefaultConfig()` 不存在，改为读取 `defaults.go` 中的常量函数。看现有 `defaults.go` 风格调整。

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/config/ -run TestConfig_SubAgentDefaults -v`
Expected: FAIL，因 SubAgent 字段未定义。

- [ ] **Step 3: 在 `config.go` 中新增结构体并挂到 `Config`**

在 `internal/config/config.go` 的 `Config` 结构体内追加：

```go
SubAgent  SubAgentConfig  `yaml:"subagent"`
```

并在文件中追加：

```go
// SubAgentConfig 子 Agent 调度配置
type SubAgentConfig struct {
    MaxConcurrency  int    `yaml:"max_concurrency"`   // 全局 semaphore 大小
    ExecTimeout     string `yaml:"exec_timeout"`      // 排队结束后开始计时，e.g. "5m"
    MaxTaskLength   int    `yaml:"max_task_length"`   // task 参数最大字符数
    MaxResultLength int    `yaml:"max_result_length"` // 子 Agent 返回文本截断长度
}
```

- [ ] **Step 4: 在 `defaults.go` 注入默认值**

阅读 `defaults.go` 现有风格后，在合适位置（通常是 `setDefaults` 或单独的初始化函数）追加：

```go
if cfg.SubAgent.MaxConcurrency <= 0 {
    cfg.SubAgent.MaxConcurrency = 5
}
if cfg.SubAgent.ExecTimeout == "" {
    cfg.SubAgent.ExecTimeout = "5m"
}
if cfg.SubAgent.MaxTaskLength <= 0 {
    cfg.SubAgent.MaxTaskLength = 16000
}
if cfg.SubAgent.MaxResultLength <= 0 {
    cfg.SubAgent.MaxResultLength = 8000
}
```

> 若测试使用 `DefaultConfig()`，则在 `DefaultConfig` 中直接赋值；若使用 loader 的 `applyDefaults`，则相应调整测试断言。

- [ ] **Step 5: 在 `template.go` 模板里追加 subagent 段**

在 `GenerateConfigTemplate()` 返回的 yaml 模板中，紧跟 `schedule:` 段后追加：

```yaml
# 子 Agent 调度配置（v3.8）
subagent:
  max_concurrency: 5      # 全局并发上限（FIFO 排队）
  exec_timeout: "5m"      # 子 Agent 执行超时（排队不计入）
  max_task_length: 16000  # task 参数最大字符数
  max_result_length: 8000 # 子 Agent 返回文本截断长度
```

- [ ] **Step 6: 运行测试确认通过**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/config/ -v`
Expected: PASS。

- [ ] **Step 7: Commit**

```bash
git add internal/config/config.go internal/config/defaults.go internal/config/template.go internal/config/config_test.go
git commit -m "$(cat <<'EOF'
feat(config): 新增 SubAgent 配置项

并发上限、执行超时、task/result 长度上限按设计 4.4 节默认值落地：5 / 5m / 16000 / 8000。模板同步更新便于 init 后用户调整。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Phase 2: SubAgentRegistry + agent.md 解析

### Task 5: `parseAgentMd` 解析 frontmatter + 正文

**Files:**
- Create: `internal/agent/subagent_md.go`
- Test: `internal/agent/subagent_md_test.go`

设计 3.1 节：`description` 必填，缺失时启动跳过；`model`/`temperature`/`max_tokens` 可选。

- [ ] **Step 1: 写失败的测试**

```go
// internal/agent/subagent_md_test.go
package agent

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTempAgentMd(t *testing.T, content string) string {
	t.Helper()
	dir := t.TempDir()
	p := filepath.Join(dir, "agent.md")
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestParseAgentMd_Valid(t *testing.T) {
	p := writeTempAgentMd(t, `---
description: 数据库查询专家
model: gpt-4
temperature: 0.3
max_tokens: 2048
---

# 数据库查询 Agent
正文内容
`)
	md, err := parseAgentMd(p)
	if err != nil {
		t.Fatalf("expected no error, got %v", err)
	}
	if md.Description != "数据库查询专家" {
		t.Errorf("description mismatch: %q", md.Description)
	}
	if md.Model != "gpt-4" {
		t.Errorf("model mismatch: %q", md.Model)
	}
	if md.Temperature == nil || *md.Temperature != 0.3 {
		t.Errorf("temperature mismatch: %v", md.Temperature)
	}
	if md.MaxTokens == nil || *md.MaxTokens != 2048 {
		t.Errorf("max_tokens mismatch: %v", md.MaxTokens)
	}
	if md.Content == "" {
		t.Error("content should not be empty")
	}
	if !contains(md.Content, "正文内容") {
		t.Errorf("content missing body: %q", md.Content)
	}
}

func TestParseAgentMd_MissingDescription(t *testing.T) {
	p := writeTempAgentMd(t, `---
model: gpt-4
---
body
`)
	_, err := parseAgentMd(p)
	if err == nil {
		t.Fatal("expected error for missing description")
	}
}

func TestParseAgentMd_EmptyDescription(t *testing.T) {
	p := writeTempAgentMd(t, `---
description: ""
---
body
`)
	_, err := parseAgentMd(p)
	if err == nil {
		t.Fatal("expected error for empty description")
	}
}

func TestParseAgentMd_NoFrontmatter(t *testing.T) {
	p := writeTempAgentMd(t, "just plain markdown body\n")
	_, err := parseAgentMd(p)
	if err == nil {
		t.Fatal("expected error for missing frontmatter")
	}
}

func TestParseAgentMd_FileNotExist(t *testing.T) {
	_, err := parseAgentMd("/nonexistent/agent.md")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || (len(s) > len(sub) && (s[:len(sub)] == sub || contains(s[1:], sub))))
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/agent/ -run TestParseAgentMd -v`
Expected: FAIL，函数未定义。

- [ ] **Step 3: 实现 `subagent_md.go`**

```go
// internal/agent/subagent_md.go
package agent

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// AgentMd 表示 agent.md 解析后的结构。
type AgentMd struct {
	Description string   `yaml:"description"`
	Model       string   `yaml:"model,omitempty"`
	Temperature *float64 `yaml:"temperature,omitempty"`
	MaxTokens   *int     `yaml:"max_tokens,omitempty"`
	Content     string   `yaml:"-"` // frontmatter 之后的正文
}

// parseAgentMd 读取 agent.md：YAML frontmatter (--- ... ---) + Markdown 正文。
// description 必填且非空，否则返回错误（启动期会跳过该子 Agent）。
func parseAgentMd(path string) (*AgentMd, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read agent.md: %w", err)
	}
	s := string(raw)

	// 必须以 --- 开头
	if !strings.HasPrefix(s, "---") {
		return nil, fmt.Errorf("missing frontmatter (file must start with ---)")
	}
	// 找到结束分隔符
	rest := s[3:]
	if strings.HasPrefix(rest, "\n") {
		rest = rest[1:]
	} else if strings.HasPrefix(rest, "\r\n") {
		rest = rest[2:]
	}
	endIdx := strings.Index(rest, "\n---")
	if endIdx < 0 {
		return nil, fmt.Errorf("frontmatter not terminated (missing closing ---)")
	}
	fmContent := rest[:endIdx]
	body := rest[endIdx+len("\n---"):]
	// 跳过结束分隔符后的可能换行
	body = strings.TrimLeft(body, "\r\n")

	md := &AgentMd{}
	if err := yaml.Unmarshal([]byte(fmContent), md); err != nil {
		return nil, fmt.Errorf("parse frontmatter yaml: %w", err)
	}
	if strings.TrimSpace(md.Description) == "" {
		return nil, fmt.Errorf("description is required and must be non-empty")
	}
	md.Content = body
	return md, nil
}
```

- [ ] **Step 4: 检查依赖已存在**

Run: `cd /Users/zhangfengda/workspace/groot && grep -q '"gopkg.in/yaml.v3"' go.sum && echo OK || go get gopkg.in/yaml.v3`
Expected: 输出 `OK`（项目已用 yaml）。

- [ ] **Step 5: 运行测试确认通过**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/agent/ -run TestParseAgentMd -v`
Expected: PASS。

- [ ] **Step 6: Commit**

```bash
git add internal/agent/subagent_md.go internal/agent/subagent_md_test.go
git commit -m "$(cat <<'EOF'
feat(agent): 实现 agent.md frontmatter 解析

description 必填校验在解析层完成；temperature/max_tokens 用指针区分「未设置」与「显式 0」，便于继承模型默认值。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

### Task 6: `SubAgentRegistry` 核心结构 + Get/Acquire/Release

**Files:**
- Create: `internal/agent/subagent_registry.go`
- Test: `internal/agent/subagent_registry_test.go`

本 Task 只搭骨架（结构体 + 方法），**不**实现启动期扫描（Task 7 完成）。

- [ ] **Step 1: 写失败的测试**

```go
// internal/agent/subagent_registry_test.go
package agent

import (
	"context"
	"testing"
	"time"
)

func TestSubAgentRegistry_GetReturnsRegisteredEntry(t *testing.T) {
	r := newEmptyRegistry(2)
	want := &SubAgentEntry{Name: "db-agent", Description: "数据库专家"}
	r.entries["db-agent"] = want

	got, ok := r.Get("db-agent")
	if !ok || got != want {
		t.Fatalf("Get returned %v, %v", got, ok)
	}
}

func TestSubAgentRegistry_GetMissing(t *testing.T) {
	r := newEmptyRegistry(2)
	if _, ok := r.Get("nope"); ok {
		t.Fatal("expected miss")
	}
}

func TestSubAgentRegistry_AcquireRelease(t *testing.T) {
	r := newEmptyRegistry(1)
	ctx := context.Background()
	if err := r.Acquire(ctx); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	// 第二个 Acquire 在 ctx 不取消前阻塞 → 用超时 ctx 测
	timed, cancel := context.WithTimeout(ctx, 50*time.Millisecond)
	defer cancel()
	if err := r.Acquire(timed); err == nil {
		t.Fatal("second acquire should fail due to ctx timeout")
	}
	r.Release()
	if err := r.Acquire(ctx); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
	r.Release()
}

func TestSubAgentRegistry_BuildDescription(t *testing.T) {
	r := newEmptyRegistry(1)
	r.entries["db-agent"] = &SubAgentEntry{Name: "db-agent", Description: "数据库专家"}
	r.entries["weather-agent"] = &SubAgentEntry{Name: "weather-agent", Description: "天气查询"}
	desc := r.BuildDescription()
	if !contains(desc, "- db-agent: 数据库专家") {
		t.Errorf("missing db-agent line: %s", desc)
	}
	if !contains(desc, "- weather-agent: 天气查询") {
		t.Errorf("missing weather-agent line: %s", desc)
	}
}

func TestSubAgentRegistry_BuildDescriptionEmpty(t *testing.T) {
	r := newEmptyRegistry(1)
	desc := r.BuildDescription()
	if !contains(desc, "无可用子 Agent") {
		t.Errorf("expected '无可用子 Agent' fallback, got: %s", desc)
	}
}

// newEmptyRegistry 仅用于测试，跳过启动期扫描。
func newEmptyRegistry(maxConc int) *SubAgentRegistry {
	return newRegistryForTest(maxConc)
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/agent/ -run TestSubAgentRegistry -v`
Expected: FAIL，类型未定义。

- [ ] **Step 3: 实现骨架**

```go
// internal/agent/subagent_registry.go
package agent

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"sync"

	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/components/tool"
	"golang.org/x/sync/semaphore"

	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/mcp"
)

// SubAgentEntry 单个子 Agent 的运行时数据（启动期一次性构建，运行时只读）。
type SubAgentEntry struct {
	Name        string
	Description string
	Instruction string             // agent.md 正文，Solo 模式使用
	Tool        tool.InvokableTool // 启动期 adk.NewTypedAgentTool 预构建
	MCPManager  *mcp.Manager       // 持有 MCP 连接生命周期
	SkillBK     einoskill.Backend  // 供 /agents、/skills 查询；Watcher 热更新入口
}

// SubAgentRegistry 全局单例：所有子 Agent 的注册表 + 并发控制。
type SubAgentRegistry struct {
	entries map[string]*SubAgentEntry
	sem     *semaphore.Weighted
	log     *logger.Logger
	mu      sync.RWMutex
}

// newRegistryForTest 仅供 _test.go 文件使用，跳过启动期扫描。
func newRegistryForTest(maxConc int) *SubAgentRegistry {
	if maxConc <= 0 {
		maxConc = 1
	}
	return &SubAgentRegistry{
		entries: make(map[string]*SubAgentEntry),
		sem:     semaphore.NewWeighted(int64(maxConc)),
	}
}

// Get 查找子 Agent。
func (r *SubAgentRegistry) Get(name string) (*SubAgentEntry, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	e, ok := r.entries[name]
	return e, ok
}

// Acquire 占用一个并发名额；ctx 取消时立即返回错误。
func (r *SubAgentRegistry) Acquire(ctx context.Context) error {
	return r.sem.Acquire(ctx, 1)
}

// Release 释放并发名额。
func (r *SubAgentRegistry) Release() {
	r.sem.Release(1)
}

// BuildDescription 拼接 call_agent 工具描述（启动期或测试期调用，运行时不再变化）。
func (r *SubAgentRegistry) BuildDescription() string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var sb strings.Builder
	sb.WriteString("调用指定的子 Agent 执行任务。可用的子 Agent：\n\n")
	if len(r.entries) == 0 {
		sb.WriteString("（无可用子 Agent）\n")
	} else {
		names := make([]string, 0, len(r.entries))
		for n := range r.entries {
			names = append(names, n)
		}
		sort.Strings(names)
		for _, n := range names {
			fmt.Fprintf(&sb, "- %s: %s\n", n, r.entries[n].Description)
		}
	}
	sb.WriteString("\n参数:\n  - agent_name: 子 Agent 名称（必填）\n  - task: 任务描述（必填）\n")
	return sb.String()
}

// Names 返回所有已注册子 Agent 名（按字典序）。
func (r *SubAgentRegistry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := make([]string, 0, len(r.entries))
	for n := range r.entries {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

// Close 关闭所有子 Agent 的 MCP 连接（main.go shutdown hook 调用）。
func (r *SubAgentRegistry) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	for name, e := range r.entries {
		if e.MCPManager != nil {
			e.MCPManager.Close()
		}
		_ = name
	}
	return nil
}
```

- [ ] **Step 4: 添加依赖**

Run: `cd /Users/zhangfengda/workspace/groot && go get golang.org/x/sync/semaphore`
Expected: 依赖被记录到 `go.mod`，无错误。

- [ ] **Step 5: 运行测试确认通过**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/agent/ -run TestSubAgentRegistry -v`
Expected: PASS。

- [ ] **Step 6: Commit**

```bash
git add internal/agent/subagent_registry.go internal/agent/subagent_registry_test.go go.mod go.sum
git commit -m "$(cat <<'EOF'
feat(agent): SubAgentRegistry 骨架 + 并发控制

Get/Acquire/Release/BuildDescription/Names/Close 全部覆盖单测；Acquire 通过 semaphore.Weighted 实现 FIFO 排队，ctx 取消立即返回。启动期扫描在后续 Task 添加。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

### Task 7: 启动期扫描 `subagents/` 构建 Registry

**Files:**
- Modify: `internal/agent/subagent_registry.go`
- Test: `internal/agent/subagent_registry_test.go`

实现 `BuildSubAgentRegistry(ctx, dir, ...)`：遍历目录，对每个子目录尝试解析 agent.md / 加载 MCP / 构建 Skill Backend / `adk.NewTypedAgentTool`。失败时跳过该子 Agent 并记录错误日志（按设计 2.7 节）。

> 因测试需要构建真实 ChatModel 比较麻烦，本 Task 把扫描循环写成「**两段式**」：
> 1. `scanSubAgentDirs(dir, log)` 纯文件系统遍历 + agent.md 解析，返回 `[]parsedSubAgent`（含 name/dir/md），可独立测试；
> 2. `BuildSubAgentRegistry(...)` 调用上一步，再加上 LLM/Skill/MCP 真实构建逻辑，由更上层（main.go 启动期集成）覆盖。

- [ ] **Step 1: 写扫描层测试**

追加到 `internal/agent/subagent_registry_test.go`：

```go
func TestScanSubAgentDirs_HappyPath(t *testing.T) {
	root := t.TempDir()
	// db-agent: 合法
	mustMkdir(t, filepath.Join(root, "db-agent"))
	mustWrite(t, filepath.Join(root, "db-agent", "agent.md"), `---
description: 数据库专家
---
正文
`)
	// no-desc: 缺 description，跳过
	mustMkdir(t, filepath.Join(root, "no-desc"))
	mustWrite(t, filepath.Join(root, "no-desc", "agent.md"), `---
model: gpt-4
---
body
`)
	// no-md: 缺 agent.md，跳过
	mustMkdir(t, filepath.Join(root, "no-md"))
	// groot: 与主 Agent 同名，跳过
	mustMkdir(t, filepath.Join(root, MainAgentName))
	mustWrite(t, filepath.Join(root, MainAgentName, "agent.md"), `---
description: 冒名顶替
---
`)

	log := newTestLogger(t)
	parsed := scanSubAgentDirs(root, log)
	names := make(map[string]bool)
	for _, p := range parsed {
		names[p.name] = true
	}
	if !names["db-agent"] {
		t.Errorf("db-agent should be parsed: %v", names)
	}
	if names["no-desc"] || names["no-md"] || names[MainAgentName] {
		t.Errorf("invalid agents should be skipped: %v", names)
	}
}

func TestScanSubAgentDirs_MissingRoot(t *testing.T) {
	log := newTestLogger(t)
	parsed := scanSubAgentDirs("/nonexistent/subagents", log)
	if len(parsed) != 0 {
		t.Errorf("expected empty result for missing root, got %d", len(parsed))
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0755); err != nil {
		t.Fatal(err)
	}
}

func mustWrite(t *testing.T, p, content string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func newTestLogger(t *testing.T) *logger.Logger {
	t.Helper()
	// 简化：项目 logger 包应有 NewNop / NewForTest 入口。
	// 若无，可直接调用 logger.New(config.LoggingConfig{Level:"error", Output:[]string{"stdout"}}).
	return logger.New(config.LoggingConfig{Level: "error", Format: "console", Output: []string{"stdout"}})
}
```

并在文件顶部 import 块补充：

```go
import (
	"os"
	"path/filepath"
	"testing"
	"context"
	"time"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
)
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/agent/ -run TestScanSubAgentDirs -v`
Expected: FAIL，函数未定义。

- [ ] **Step 3: 实现扫描层**

在 `internal/agent/subagent_registry.go` 追加：

```go
import (
	// ... 已有 ...
	"os"
	"path/filepath"
)

// parsedSubAgent 是 scanSubAgentDirs 的返回单元（文件系统层结果，不含 LLM/MCP 实例）。
type parsedSubAgent struct {
	name string
	dir  string
	md   *AgentMd
}

// scanSubAgentDirs 仅做文件系统遍历 + agent.md 解析；不构建 MCP/Skill。
// 失败项（缺 agent.md / description 为空 / 名冲突）跳过并记录日志。
func scanSubAgentDirs(root string, log *logger.Logger) []parsedSubAgent {
	entries, err := os.ReadDir(root)
	if err != nil {
		if !os.IsNotExist(err) {
			log.Error("scan subagents: read dir failed: " + err.Error())
		}
		return nil
	}
	var out []parsedSubAgent
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if name == MainAgentName {
			log.Error("subagent name conflicts with main agent, skip: " + name)
			continue
		}
		dir := filepath.Join(root, name)
		mdPath := filepath.Join(dir, "agent.md")
		md, err := parseAgentMd(mdPath)
		if err != nil {
			log.Error("subagent invalid agent.md, skip: name=" + name + " err=" + err.Error())
			continue
		}
		out = append(out, parsedSubAgent{name: name, dir: dir, md: md})
	}
	return out
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/agent/ -run TestScanSubAgentDirs -v`
Expected: PASS。

- [ ] **Step 5: 实现完整构建函数（无单测，由 main.go 集成时验证）**

继续在 `subagent_registry.go` 追加：

```go
import (
	// ... 已有 ...
	"github.com/cloudwego/eino-ext/adk/backend/local"
	einoskill "github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/eino/adk"
	"github.com/cloudwego/eino/compose"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/filesystem"
	"github.com/zfd81/groot/internal/llm"
)

// BuildSubAgentRegistry 启动期一次性构建。
// dir = {GROOT_HOME}/subagents。任意子 Agent 构建失败仅跳过该项，不影响其他。
func BuildSubAgentRegistry(
	ctx context.Context,
	dir string,
	reactCfg config.ReactConfig,
	subCfg config.SubAgentConfig,
	llmCfg config.LLMConfig,
	log *logger.Logger,
) (*SubAgentRegistry, error) {
	reg := &SubAgentRegistry{
		entries: make(map[string]*SubAgentEntry),
		sem:     semaphore.NewWeighted(int64(maxOr(subCfg.MaxConcurrency, 5))),
		log:     log,
	}

	for _, p := range scanSubAgentDirs(dir, log) {
		entry, err := buildSubAgentEntry(ctx, p, reactCfg, llmCfg, log)
		if err != nil {
			log.Error("build subagent failed, skip: name=" + p.name + " err=" + err.Error())
			continue
		}
		reg.entries[p.name] = entry
	}
	return reg, nil
}

func buildSubAgentEntry(
	ctx context.Context,
	p parsedSubAgent,
	reactCfg config.ReactConfig,
	llmCfg config.LLMConfig,
	log *logger.Logger,
) (*SubAgentEntry, error) {
	// 1. MCP Manager（专属，可空）
	mcpMgr := mcp.NewManager(log)
	mcpDir := filepath.Join(p.dir, "mcp")
	if err := mcpMgr.LoadAll(mcpDir); err != nil {
		return nil, fmt.Errorf("load mcp: %w", err)
	}

	// 2. Skill Backend（专属，可空）
	skillsDir := filepath.Join(p.dir, "skills")
	_ = os.MkdirAll(skillsDir, 0755)
	localBE, err := local.NewBackend(ctx, &local.Config{})
	if err != nil {
		mcpMgr.Close()
		return nil, fmt.Errorf("local backend: %w", err)
	}
	symBE := filesystem.NewSymlinkBackend(localBE)
	skillBK, err := einoskill.NewBackendFromFilesystem(ctx, &einoskill.BackendFromFilesystemConfig{
		Backend: symBE,
		BaseDir: skillsDir,
	})
	if err != nil {
		mcpMgr.Close()
		return nil, fmt.Errorf("skill backend: %w", err)
	}
	skillMW, err := einoskill.NewMiddleware(ctx, &einoskill.Config{Backend: skillBK})
	if err != nil {
		mcpMgr.Close()
		return nil, fmt.Errorf("skill middleware: %w", err)
	}

	// 3. 模型：agent.md.model → llm.default_model
	modelName := p.md.Model
	if modelName == "" {
		modelName = llmCfg.DefaultModel
	}
	stepTimeout := time.Duration(reactCfg.StepTimeout) * time.Second
	chatModel, err := llm.NewChatModel(ctx, llmCfg, modelName, stepTimeout)
	if err != nil {
		mcpMgr.Close()
		return nil, fmt.Errorf("chat model: %w", err)
	}

	// 4. ChatModelAgent
	maxIter := reactCfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 20
	}
	agentCfg := &adk.ChatModelAgentConfig{
		Name:          p.name,
		Description:   p.md.Description,
		Instruction:   p.md.Content,
		Model:         chatModel,
		MaxIterations: maxIter,
		Handlers:      []adk.ChatModelAgentMiddleware{skillMW},
		ToolsConfig: adk.ToolsConfig{
			ToolsNodeConfig: compose.ToolsNodeConfig{Tools: mcpMgr.GetTools()},
			// 叶子节点不需要 EmitInternalEvents
		},
	}
	if reactCfg.ErrorRetry > 0 {
		agentCfg.ModelRetryConfig = &adk.ModelRetryConfig{MaxRetries: reactCfg.ErrorRetry}
	}
	cmAgent, err := adk.NewChatModelAgent(ctx, agentCfg)
	if err != nil {
		mcpMgr.Close()
		return nil, fmt.Errorf("chat model agent: %w", err)
	}
	agentTool := adk.NewAgentTool(ctx, cmAgent)

	return &SubAgentEntry{
		Name:        p.name,
		Description: p.md.Description,
		Instruction: p.md.Content,
		Tool:        agentTool.(tool.InvokableTool),
		MCPManager:  mcpMgr,
		SkillBK:     skillBK,
	}, nil
}

func maxOr(v, fallback int) int {
	if v > 0 {
		return v
	}
	return fallback
}
```

- [ ] **Step 6: 编译验证**

Run: `cd /Users/zhangfengda/workspace/groot && go build ./...`
Expected: 编译通过。

> 若 `adk.NewAgentTool` 返回类型与 `tool.InvokableTool` 不兼容，按 [agent_tool.go:93](/Users/zhangfengda/go/pkg/mod/github.com/cloudwego/eino@v0.9.0-beta.1/adk/agent_tool.go) 的定义返回 `tool.BaseTool` 但实际类型实现了 `InvokableRun`，可改为类型断言 `tool.(tool.InvokableTool)` 或保留 `tool.BaseTool` 类型并在 `CallAgentTool` 内做转换。

- [ ] **Step 7: 跑全部 agent 测试**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/agent/... -v`
Expected: PASS。

- [ ] **Step 8: Commit**

```bash
git add internal/agent/subagent_registry.go internal/agent/subagent_registry_test.go
git commit -m "$(cat <<'EOF'
feat(agent): SubAgentRegistry 启动期扫描与子 Agent 预构建

scanSubAgentDirs 单独可测；BuildSubAgentRegistry 整合 MCP Manager / Skill Backend / ChatModelAgent / NewAgentTool，错误跳过个体不影响整体启动。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Phase 3: CallAgentTool + Engine 改造

### Task 8: `tokenAccumulators` 累加器

**Files:**
- Create: `internal/agent/token_accumulators.go`
- Test: `internal/agent/token_accumulators_test.go`

按设计 8.2 节，按 `childChatID` 累加多轮 Token，主 `CallAgentTool.InvokableRun` 调用结束时 `PopAndDelete` 取值并清理。

- [ ] **Step 1: 写失败的测试**

```go
// internal/agent/token_accumulators_test.go
package agent

import (
	"sync"
	"testing"
)

func TestTokenAccumulators_AddPop(t *testing.T) {
	a := NewTokenAccumulators()
	a.Add("chat_a", 10, 20)
	a.Add("chat_a", 5, 7)
	a.Add("chat_b", 1, 2)

	gotA := a.PopAndDelete("chat_a")
	if gotA.Prompt != 15 || gotA.Completion != 27 || gotA.Total != 42 {
		t.Errorf("chat_a: %+v", gotA)
	}
	// 再 pop 同一个 → 全 0
	gotA2 := a.PopAndDelete("chat_a")
	if gotA2.Prompt != 0 || gotA2.Completion != 0 || gotA2.Total != 0 {
		t.Errorf("chat_a after pop should be zero: %+v", gotA2)
	}
	gotB := a.PopAndDelete("chat_b")
	if gotB.Prompt != 1 || gotB.Completion != 2 || gotB.Total != 3 {
		t.Errorf("chat_b: %+v", gotB)
	}
}

func TestTokenAccumulators_Concurrent(t *testing.T) {
	a := NewTokenAccumulators()
	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.Add("chat_x", 1, 2)
		}()
	}
	wg.Wait()
	got := a.PopAndDelete("chat_x")
	if got.Prompt != 100 || got.Completion != 200 || got.Total != 300 {
		t.Errorf("concurrent add: %+v", got)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/agent/ -run TestTokenAccumulators -v`
Expected: FAIL。

- [ ] **Step 3: 实现**

```go
// internal/agent/token_accumulators.go
package agent

import "sync"

// TokenUsage 三元组。
type TokenUsage struct {
	Prompt     int
	Completion int
	Total      int
}

// TokenAccumulators 按 chatID 聚合子 Agent token；线程安全。
type TokenAccumulators struct {
	mu sync.Mutex
	m  map[string]*TokenUsage
}

func NewTokenAccumulators() *TokenAccumulators {
	return &TokenAccumulators{m: make(map[string]*TokenUsage)}
}

// Add 累加一次 LLM 响应的 token；total 自动 = prompt + completion 以避免外部传错。
func (a *TokenAccumulators) Add(chatID string, prompt, completion int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	cur, ok := a.m[chatID]
	if !ok {
		cur = &TokenUsage{}
		a.m[chatID] = cur
	}
	cur.Prompt += prompt
	cur.Completion += completion
	cur.Total = cur.Prompt + cur.Completion
}

// PopAndDelete 取出并清理（在 CallAgentTool.InvokableRun 收尾时调用）。
func (a *TokenAccumulators) PopAndDelete(chatID string) TokenUsage {
	a.mu.Lock()
	defer a.mu.Unlock()
	cur, ok := a.m[chatID]
	if !ok {
		return TokenUsage{}
	}
	delete(a.m, chatID)
	return *cur
}
```

- [ ] **Step 4: 运行确认通过**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/agent/ -run TestTokenAccumulators -v`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/agent/token_accumulators.go internal/agent/token_accumulators_test.go
git commit -m "$(cat <<'EOF'
feat(agent): TokenAccumulators 子 Agent token 聚合器

按 childChatID 线程安全累加多轮 ReAct token；PopAndDelete 在 CallAgentTool 收尾时一次性取回写 ChatRecord。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

### Task 9: `Engine` 增加 `agentName` 字段并按事件源分流

**Files:**
- Modify: `internal/agent/engine.go`
- Modify: `internal/agent/executor.go`（同步构造）

设计 4.7.4 节：`Engine` 加 `agentName`；事件循环识别 `event.AgentName != e.agentName` 时把事件标为「来自子 Agent」；当事件源是子 Agent 且 LLM 响应携带 `Usage` 时，按 ctx 中的 `childChatID` 累加到 `TokenAccumulators`。

> 本任务**只**新增字段、不改 ProgressCallback 签名（Task 10 单独处理）；token 累加路径先打通到结构层。

- [ ] **Step 1: 改 `Engine` 结构 + 构造**

`internal/agent/engine.go`：

把：
```go
type Engine struct {
    llmConfig   config.LLMConfig
    middlewares []adk.ChatModelAgentMiddleware
    mcpManager  *mcp.Manager
    reactConfig config.ReactConfig
    log         *logger.Logger
}

func NewEngine(...) *Engine { ... }
```

改为：

```go
type Engine struct {
    llmConfig         config.LLMConfig
    middlewares       []adk.ChatModelAgentMiddleware
    mcpManager        *mcp.Manager
    extraTools        []tool.BaseTool       // 追加到 mcpManager.GetTools() 之后，用于 call_agent
    reactConfig       config.ReactConfig
    log               *logger.Logger
    agentName         string                 // MainAgentName 或子 Agent 名（Solo 模式）
    emitInternalEvents bool                  // 主 Agent 路径打开以透传子 Agent 事件
    tokenAccumulators *TokenAccumulators
}

type EngineConfig struct {
    LLM                config.LLMConfig
    Middlewares        []adk.ChatModelAgentMiddleware
    MCP                *mcp.Manager
    ExtraTools         []tool.BaseTool
    React              config.ReactConfig
    Log                *logger.Logger
    AgentName          string
    EmitInternalEvents bool
    TokenAccumulators  *TokenAccumulators
}

func NewEngine(cfg EngineConfig) *Engine {
    name := cfg.AgentName
    if name == "" {
        name = MainAgentName
    }
    return &Engine{
        llmConfig:         cfg.LLM,
        middlewares:       cfg.Middlewares,
        mcpManager:        cfg.MCP,
        extraTools:        cfg.ExtraTools,
        reactConfig:       cfg.React,
        log:                cfg.Log,
        agentName:         name,
        emitInternalEvents: cfg.EmitInternalEvents,
        tokenAccumulators: cfg.TokenAccumulators,
    }
}
```

- [ ] **Step 2: 调整 `engine.Run()` 使用新字段**

`engine.go:92` 把 `Name: MainAgentName` 改为 `Name: e.agentName`。

`engine.go:97-101` ToolsConfig 加 EmitInternalEvents：

```go
ToolsConfig: adk.ToolsConfig{
    ToolsNodeConfig: compose.ToolsNodeConfig{
        Tools: tools,
    },
    EmitInternalEvents: e.emitInternalEvents,
},
```

`engine.go:80` 的 `buildTools()` 改为合并 mcp + extra：

```go
func (e *Engine) buildTools() []tool.BaseTool {
    tools := e.mcpManager.GetTools()
    if len(e.extraTools) > 0 {
        tools = append(tools, e.extraTools...)
    }
    return tools
}
```

- [ ] **Step 3: 调整 `Executor.Execute()` 使用新构造**

`internal/agent/executor.go:119-125`：

```go
engine := NewEngine(EngineConfig{
    LLM:               e.config.LLM,
    Middlewares:       e.middlewares,
    MCP:               e.mcpManager,
    React:             e.config.React,
    Log:               e.logger,
    AgentName:         MainAgentName,
    // EmitInternalEvents 暂留 false，Task 12 在编排模式下打开
})
```

- [ ] **Step 4: 加事件源分流逻辑（仅 Token 累加，SSE 字段在 Task 10）**

在 `engine.go` 事件循环内（约 line 227 处理 Assistant 之前）追加判断；对**流式**和**非流式**两个分支都要处理。提取一个辅助函数：

```go
// accumulateUsageIfChild 当事件来自子 Agent 且携带 Usage 时累加。
func (e *Engine) accumulateUsageIfChild(ctx context.Context, eventAgentName string, meta *schema.ResponseMeta) {
    if meta == nil || meta.Usage == nil {
        return
    }
    if eventAgentName == "" || eventAgentName == e.agentName {
        return
    }
    if e.tokenAccumulators == nil {
        return
    }
    childChatID, ok := ctx.Value(childChatIDKey{}).(string)
    if !ok || childChatID == "" {
        return
    }
    e.tokenAccumulators.Add(childChatID, meta.Usage.PromptTokens, meta.Usage.CompletionTokens)
}

// childChatIDKey is the ctx value key for child Agent chat IDs.
type childChatIDKey struct{}
```

在事件循环每次取到 `event` 后取得 `eventAgentName := event.AgentName`，并在处理流式/非流式 Assistant 消息分支中拿到 `msg` 后调用 `e.accumulateUsageIfChild(ctx, eventAgentName, msg.ResponseMeta)`。

- [ ] **Step 5: 编译**

Run: `cd /Users/zhangfengda/workspace/groot && go build ./...`
Expected: 编译通过。

- [ ] **Step 6: 跑现有测试**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/agent/... ./internal/api/... -v`
Expected: 既有测试全部通过。新增字段无破坏性变更。

- [ ] **Step 7: Commit**

```bash
git add internal/agent/engine.go internal/agent/executor.go
git commit -m "$(cat <<'EOF'
refactor(agent): Engine 改用 EngineConfig 构造并支持子 Agent 事件分流

新增 agentName / emitInternalEvents / tokenAccumulators 三个字段；事件循环按 event.AgentName 累加子 Agent token。EmitInternalEvents 默认 false，后续编排模式打开。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

### Task 10: `ProgressCallback` 加 `agentName` 首参 + SSE 注入 `agent_name`

**Files:**
- Modify: `internal/agent/engine.go` (`ProgressCallback` + 所有调用点)
- Modify: `internal/agent/executor.go` (回调实现)
- Modify: `internal/agent/sse.go` (`Write*` 接收 `agentName` 参数)
- Test: `internal/agent/sse_test.go`（新建）

设计 4.8 节：子 Agent 事件 JSON 中可选地携带 `"agent_name"`；主 Agent 自身事件不携带。

- [ ] **Step 1: 写 SSE 序列化测试**

新建 `internal/agent/sse_test.go`：

```go
package agent

import (
	"bytes"
	"strings"
	"testing"
)

type bufFlusher struct{ bytes.Buffer }

func (bufFlusher) Flush() error { return nil }

func TestSSEWriter_WriteThinkingWithAgentName(t *testing.T) {
	buf := &bufFlusher{}
	w := NewSSEWriter(buf, "sess", "chat", 1)
	if err := w.WriteThinking("db-agent", "thinking..."); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	if !strings.Contains(s, `"agent_name":"db-agent"`) {
		t.Errorf("missing agent_name: %s", s)
	}
	if !strings.Contains(s, `"reasoning_content":"thinking..."`) {
		t.Errorf("missing reasoning: %s", s)
	}
}

func TestSSEWriter_WriteThinkingWithoutAgentName(t *testing.T) {
	buf := &bufFlusher{}
	w := NewSSEWriter(buf, "sess", "chat", 1)
	if err := w.WriteThinking("", "x"); err != nil {
		t.Fatal(err)
	}
	s := buf.String()
	if strings.Contains(s, "agent_name") {
		t.Errorf("agent_name should be omitted: %s", s)
	}
}
```

- [ ] **Step 2: 运行确认失败**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/agent/ -run TestSSEWriter -v`
Expected: FAIL（签名不匹配）。

- [ ] **Step 3: 改 `sse.go`**

替换 `Write*` 系列方法签名（除 `WriteDone`），均把第一个参数改为 `agentName string`。返回 JSON 时如果 `agentName` 非空则注入 `"agent_name"` 键。逐个替换：

```go
func (s *SSEWriter) WriteThinking(agentName, content string) error {
    payload := map[string]interface{}{
        "role":              "assistant",
        "reasoning_content": content,
    }
    if agentName != "" {
        payload["agent_name"] = agentName
    }
    return s.WriteData(payload)
}

func (s *SSEWriter) WriteMessage(agentName, content string) error {
    payload := map[string]interface{}{
        "role":    "assistant",
        "content": content,
    }
    if agentName != "" {
        payload["agent_name"] = agentName
    }
    return s.WriteData(payload)
}

func (s *SSEWriter) WriteToolCalls(agentName string, toolCalls []ToolCall) error {
    payload := map[string]interface{}{
        "role":       "assistant",
        "tool_calls": toolCalls,
    }
    if agentName != "" {
        payload["agent_name"] = agentName
    }
    return s.WriteData(payload)
}

func (s *SSEWriter) WriteFinish(agentName, reason string) error {
    payload := map[string]interface{}{
        "role":          "assistant",
        "finish_reason": reason,
    }
    if agentName != "" {
        payload["agent_name"] = agentName
    }
    return s.WriteData(payload)
}

func (s *SSEWriter) WriteToolResult(agentName, toolCallID, toolName, content string, isError bool) error {
    payload := map[string]interface{}{
        "role":         "tool",
        "tool_call_id": toolCallID,
        "tool_name":    toolName,
        "content":      content,
        "error":        isError,
    }
    if agentName != "" {
        payload["agent_name"] = agentName
    }
    return s.WriteData(payload)
}

func (s *SSEWriter) WriteError(agentName, message string) error {
    payload := map[string]interface{}{
        "event":   "error",
        "message": message,
    }
    if agentName != "" {
        payload["agent_name"] = agentName
    }
    return s.WriteData(payload)
}
```

`WriteDone` 不变。

- [ ] **Step 4: 改 `ProgressCallback`**

`engine.go`：

```go
type ProgressCallback struct {
    WriteThinking   func(agentName, content string) error
    WriteMessage    func(agentName, content string) error
    WriteToolCalls  func(agentName string, toolCalls []ToolCall) error
    WriteFinish     func(agentName, reason string) error
    WriteToolResult func(agentName, toolCallID, toolName, content string, isError bool) error
    WriteError      func(agentName, message string) error
    WriteDone       func() error
}
```

- [ ] **Step 5: 改 `engine.Run()` 事件循环传 agentName**

每个调用 `cb.Write*` 处，传入 `eventAgentName`（即 `event.AgentName`，主 Agent 自身事件为空字符串）。例如：

```go
eventAgentName := event.AgentName
if eventAgentName == e.agentName {
    eventAgentName = ""  // 主 Agent 自身事件不携带 agent_name
}
// ...
cb.WriteThinking(eventAgentName, msg.ReasoningContent)
cb.WriteMessage(eventAgentName, msg.Content)
cb.WriteToolCalls(eventAgentName, toolCalls)
cb.WriteFinish(eventAgentName, msg.ResponseMeta.FinishReason)
cb.WriteError(eventAgentName, errStr)
// processToolEvent 同样：
cb.WriteToolResult(eventAgentName, toolCallID, toolName, output, false)
```

`processToolEvent` 签名加 `eventAgentName`：

```go
func (e *Engine) processToolEvent(eventAgentName string, event *adk.AgentEvent, cb *ProgressCallback, steps *[]StepRecord) { ... }
```

调用处把 `eventAgentName` 传入。

错误路径里手工构造的 `cb.WriteToolResult` / `cb.WriteError` 也需传入 `eventAgentName`。

- [ ] **Step 6: 改 `executor.go` 回调实现**

把 `&ProgressCallback{...}` 内的每个闭包改为接收 `agentName`：

```go
WriteThinking: func(agentName, content string) error {
    select {
    case <-ctx.Done():
        return ctx.Err()
    default:
        return sse.WriteThinking(agentName, content)
    }
},
// ... 其余同理
```

- [ ] **Step 7: 编译并跑测试**

Run: `cd /Users/zhangfengda/workspace/groot && go build ./... && go test ./internal/agent/... -v`
Expected: PASS。

- [ ] **Step 8: Commit**

```bash
git add internal/agent/engine.go internal/agent/executor.go internal/agent/sse.go internal/agent/sse_test.go
git commit -m "$(cat <<'EOF'
refactor(agent): SSE/ProgressCallback 增加 agentName 维度

子 Agent 事件在 JSON 中携带 agent_name；主 Agent 自身事件保持原格式（向后兼容）。事件循环按 event.AgentName 决定是否注入 agent_name 字段，便于 TUI 区分来源。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

### Task 11: `CallAgentTool` 实现

**Files:**
- Create: `internal/agent/call_agent.go`
- Test: `internal/agent/call_agent_test.go`

按设计 4.7.3 节，请求级实例：构造时注入 `registry / parentChatID / sessionID / memory / tokenAccumulators / runtimeState / execTimeout / maxTaskLen / maxResultLen`。

> 测试用 fake 子 Agent（实现 `tool.InvokableTool` 接口）。

- [ ] **Step 1: 写测试**

```go
// internal/agent/call_agent_test.go
package agent

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"
)

type fakeAgentTool struct {
	result string
	err    error
	sleep  time.Duration
}

func (f *fakeAgentTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: "fake", Desc: "fake"}, nil
}
func (f *fakeAgentTool) InvokableRun(ctx context.Context, _ string, _ ...tool.Option) (string, error) {
	if f.sleep > 0 {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-time.After(f.sleep):
		}
	}
	return f.result, f.err
}

func newTestCallAgentTool(reg *SubAgentRegistry, maxTask, maxResult int) *CallAgentTool {
	return &CallAgentTool{
		registry:          reg,
		parentChatID:      "chat_p",
		sessionID:         "sess_x",
		maxTaskLen:        maxTask,
		maxResultLen:      maxResult,
		execTimeout:       2 * time.Second,
		tokenAccumulators: NewTokenAccumulators(),
		log:               newTestLogger(nil),
		// memory / runtimeState 留 nil，测试只走简化路径
	}
}

func TestCallAgentTool_UnknownAgent(t *testing.T) {
	reg := newRegistryForTest(2)
	tool := newTestCallAgentTool(reg, 100, 100)
	_, err := tool.InvokableRun(context.Background(), `{"agent_name":"nope","task":"do it"}`)
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected unknown agent error, got: %v", err)
	}
}

func TestCallAgentTool_TaskTooLong(t *testing.T) {
	reg := newRegistryForTest(2)
	reg.entries["fake"] = &SubAgentEntry{Name: "fake", Tool: &fakeAgentTool{result: "ok"}}
	tool := newTestCallAgentTool(reg, 5, 100)
	_, err := tool.InvokableRun(context.Background(), `{"agent_name":"fake","task":"too long task"}`)
	if err == nil || !strings.Contains(err.Error(), "task") {
		t.Fatalf("expected task length error, got: %v", err)
	}
}

func TestCallAgentTool_TruncatesLongResult(t *testing.T) {
	reg := newRegistryForTest(2)
	long := strings.Repeat("x", 1000)
	reg.entries["fake"] = &SubAgentEntry{Name: "fake", Tool: &fakeAgentTool{result: long}}
	tool := newTestCallAgentTool(reg, 100, 50)
	out, err := tool.InvokableRun(context.Background(), `{"agent_name":"fake","task":"t"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(out, "⚠️") {
		t.Errorf("truncation warning should lead: %q", out[:20])
	}
	if !strings.Contains(out, "1000") || !strings.Contains(out, "50") {
		t.Errorf("warning should mention sizes: %q", out)
	}
}

func TestCallAgentTool_ConcurrencyAcquireBlocks(t *testing.T) {
	reg := newRegistryForTest(1)
	reg.entries["fake"] = &SubAgentEntry{Name: "fake", Tool: &fakeAgentTool{result: "ok", sleep: 100 * time.Millisecond}}
	toolA := newTestCallAgentTool(reg, 100, 100)
	toolB := newTestCallAgentTool(reg, 100, 100)

	done := make(chan error, 1)
	go func() {
		_, err := toolA.InvokableRun(context.Background(), `{"agent_name":"fake","task":"t"}`)
		done <- err
	}()

	// B 在 A 释放前应被 ctx 取消
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := toolB.InvokableRun(ctx, `{"agent_name":"fake","task":"t"}`)
	if err == nil {
		t.Fatal("expected B to fail due to ctx timeout while A holds the semaphore")
	}
	<-done
}

func TestCallAgentTool_PropagatesErr(t *testing.T) {
	reg := newRegistryForTest(2)
	reg.entries["fake"] = &SubAgentEntry{Name: "fake", Tool: &fakeAgentTool{err: errors.New("boom")}}
	tool := newTestCallAgentTool(reg, 100, 100)
	_, err := tool.InvokableRun(context.Background(), `{"agent_name":"fake","task":"t"}`)
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected propagated error, got: %v", err)
	}
}
```

- [ ] **Step 2: 实现 `call_agent.go`**

```go
// internal/agent/call_agent.go
package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/memory"
)

// CallAgentArgument 工具入参。
type CallAgentArgument struct {
	AgentName string `json:"agent_name"`
	Task      string `json:"task"`
}

// CallAgentTool 主 Agent 调度子 Agent 的入口。请求级实例。
type CallAgentTool struct {
	registry          *SubAgentRegistry
	parentChatID      string
	sessionID         string
	maxTaskLen        int
	maxResultLen      int
	execTimeout       time.Duration
	memory            *memory.Manager
	runtimeState      *RuntimeState
	tokenAccumulators *TokenAccumulators
	log               *logger.Logger
	parentRound       int
}

// NewCallAgentTool 构造一个请求级 CallAgentTool。
func NewCallAgentTool(cfg CallAgentToolConfig) *CallAgentTool {
	return &CallAgentTool{
		registry:          cfg.Registry,
		parentChatID:      cfg.ParentChatID,
		sessionID:         cfg.SessionID,
		maxTaskLen:        cfg.MaxTaskLen,
		maxResultLen:      cfg.MaxResultLen,
		execTimeout:       cfg.ExecTimeout,
		memory:            cfg.Memory,
		runtimeState:      cfg.RuntimeState,
		tokenAccumulators: cfg.TokenAccumulators,
		log:               cfg.Log,
		parentRound:       cfg.ParentRound,
	}
}

type CallAgentToolConfig struct {
	Registry          *SubAgentRegistry
	ParentChatID      string
	SessionID         string
	MaxTaskLen        int
	MaxResultLen      int
	ExecTimeout       time.Duration
	Memory            *memory.Manager
	RuntimeState      *RuntimeState
	TokenAccumulators *TokenAccumulators
	Log               *logger.Logger
	ParentRound       int
}

// Info 满足 tool.InvokableTool。工具描述启动期由 BuildDescription 拼接。
func (t *CallAgentTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{
		Name: "call_agent",
		Desc: t.registry.BuildDescription(),
		ParamsOneOf: schema.NewParamsOneOfByParams(map[string]*schema.ParameterInfo{
			"agent_name": {Desc: "子 Agent 名称", Required: true, Type: schema.String},
			"task":       {Desc: "任务描述", Required: true, Type: schema.String},
		}),
	}, nil
}

// InvokableRun 委托链路。
func (t *CallAgentTool) InvokableRun(ctx context.Context, argumentsInJSON string, opts ...tool.Option) (string, error) {
	var input CallAgentArgument
	if err := json.Unmarshal([]byte(argumentsInJSON), &input); err != nil {
		return "", fmt.Errorf("failed to unmarshal call_agent input: %w", err)
	}

	entry, ok := t.registry.Get(input.AgentName)
	if !ok {
		return "", fmt.Errorf("未知的子 Agent: %s，请检查 call_agent 工具描述中的可用列表", input.AgentName)
	}
	if t.maxTaskLen > 0 && len(input.Task) > t.maxTaskLen {
		return "", fmt.Errorf("task 长度超过 %d 字符上限", t.maxTaskLen)
	}

	if err := t.registry.Acquire(ctx); err != nil {
		return "", fmt.Errorf("acquire subagent semaphore: %w", err)
	}
	defer t.registry.Release()

	execCtx, cancel := context.WithTimeout(ctx, t.execTimeout)
	defer cancel()

	childChatID := memory.GenerateChildChatID(t.parentChatID, input.AgentName)
	execCtx = context.WithValue(execCtx, childChatIDKey{}, childChatID)

	if t.runtimeState != nil {
		t.runtimeState.AddSubAgent(t.sessionID, input.AgentName)
		defer t.runtimeState.RemoveSubAgent(t.sessionID, input.AgentName)
	}

	startTime := time.Now()
	params, _ := json.Marshal(map[string]string{"request": input.Task})
	result, runErr := entry.Tool.InvokableRun(execCtx, string(params), opts...)
	endTime := time.Now()

	if t.maxResultLen > 0 && len(result) > t.maxResultLen {
		result = truncateWithWarning(result, t.maxResultLen)
	}

	// 写 ChatRecord（错误吞掉，不影响主 Agent 返回）
	if t.memory != nil {
		t.saveChildChatRecord(childChatID, input, result, runErr, startTime, endTime)
	}
	return result, runErr
}

func (t *CallAgentTool) saveChildChatRecord(childChatID string, input CallAgentArgument, result string, runErr error, startTime, endTime time.Time) {
	status := "completed"
	var memErr *memory.Error
	if runErr != nil {
		status = "failed"
		memErr = &memory.Error{Code: "execution_error", Message: runErr.Error()}
	}
	var tokens TokenUsage
	if t.tokenAccumulators != nil {
		tokens = t.tokenAccumulators.PopAndDelete(childChatID)
	}
	record := &memory.ChatRecord{
		ChatID:           childChatID,
		SessionID:        t.sessionID,
		Round:            t.parentRound,
		Timestamp:        endTime,
		StartedAt:        startTime,
		EndedAt:          endTime,
		Instruction:      input.Task,
		Result:           result,
		Status:           status,
		Duration:         int(endTime.Sub(startTime).Seconds()),
		AgentName:        input.AgentName,
		PromptTokens:     tokens.Prompt,
		CompletionTokens: tokens.Completion,
		TotalTokens:      tokens.Total,
		Error:            memErr,
	}
	if err := t.memory.SaveChatRecord(t.sessionID, record); err != nil && t.log != nil {
		t.log.Error("save subagent chat record failed: " + err.Error())
	}
}

func truncateWithWarning(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return fmt.Sprintf(
		"⚠️ 结果已被截断（原始长度: %d 字符，仅显示前 %d 字符）。如需完整数据，请缩小 task 范围或指定输出字段。\n──────────────────\n%s",
		len(s), maxLen, s[:maxLen],
	)
}
```

> `RuntimeState.AddSubAgent` / `RemoveSubAgent` 在 Task 13 实现；本任务的测试不传 runtimeState 因此不依赖。

- [ ] **Step 3: 跑测试确认通过**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/agent/ -run TestCallAgentTool -v`
Expected: PASS。

- [ ] **Step 4: Commit**

```bash
git add internal/agent/call_agent.go internal/agent/call_agent_test.go
git commit -m "$(cat <<'EOF'
feat(agent): CallAgentTool 实现主 Agent 调度子 Agent 入口

参数校验 / semaphore 排队 / 独立超时 ctx / 子 chatID 注入 / 结果截断（开头警告）/ ChatRecord 写入全部到位。错误通过标准 tool 返回值传播，ChatRecord 写入失败吞错。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Phase 4: Executor + API Handler 分流

### Task 12: Executor 按 `task.AgentName` 分流（Solo / 编排）

**Files:**
- Modify: `internal/agent/executor.go`
- Modify: `internal/agent/engine.go`（`Run` 签名增加 `agentMdContent string`）
- Modify: `internal/agent/runtime_state.go`（`AddSubAgent` / `RemoveSubAgent`）
- Modify: `cmd/groot/main.go`（构造 Executor 注入 Registry）

设计 6.2 节：
- Solo 模式 → 系统指令 = agent.md 正文 + SESSION.md + Request.prompt
- 编排模式主 Agent → 系统指令 = GROOT.md + SESSION.md + Request.prompt，工具集 = 全局 MCP + schedule + call_agent，EmitInternalEvents = true

- [ ] **Step 1: `Task` 新增 `AgentName` 字段**

`internal/agent/executor.go` 在 `Task` 结构体中追加：

```go
AgentName string  // Solo 模式时为子 Agent 名；编排模式空字符串
```

- [ ] **Step 2: `RuntimeState` 加 SubAgent 跟踪**

`internal/agent/runtime_state.go`：

```go
type ChatProgress struct {
    CurrentStep    int                  `json:"current_step"`
    StepsCompleted int                  `json:"steps_completed"`
    Percentage     int                  `json:"percentage"`
    SubAgents      []SubAgentProgress   `json:"sub_agents,omitempty"`
}

type SubAgentProgress struct {
    Name   string `json:"name"`
    Status string `json:"status"`
}

// AddSubAgent 标记一个子 Agent 正在运行；不存在 active chat 时是 no-op。
func (r *RuntimeState) AddSubAgent(sessionID, name string) {
    v, ok := r.activeChats.Load(sessionID)
    if !ok {
        return
    }
    chat := v.(*ActiveChat)
    if chat.Progress == nil {
        chat.Progress = &ChatProgress{}
    }
    chat.Progress.SubAgents = append(chat.Progress.SubAgents, SubAgentProgress{Name: name, Status: "running"})
}

// RemoveSubAgent 把对应条目从 list 中删除。
func (r *RuntimeState) RemoveSubAgent(sessionID, name string) {
    v, ok := r.activeChats.Load(sessionID)
    if !ok {
        return
    }
    chat := v.(*ActiveChat)
    if chat.Progress == nil {
        return
    }
    filtered := chat.Progress.SubAgents[:0]
    for _, sp := range chat.Progress.SubAgents {
        if sp.Name != name {
            filtered = append(filtered, sp)
        }
    }
    chat.Progress.SubAgents = filtered
}
```

补充 `runtime_state_test.go`：

```go
func TestRuntimeState_SubAgentTracking(t *testing.T) {
    r := NewRuntimeState()
    _, _ = r.Register("sess", "chat")
    r.AddSubAgent("sess", "db-agent")
    r.AddSubAgent("sess", "weather-agent")
    c, _ := r.Get("sess")
    if len(c.Progress.SubAgents) != 2 {
        t.Fatalf("expected 2 sub_agents, got %d", len(c.Progress.SubAgents))
    }
    r.RemoveSubAgent("sess", "db-agent")
    c, _ = r.Get("sess")
    if len(c.Progress.SubAgents) != 1 || c.Progress.SubAgents[0].Name != "weather-agent" {
        t.Fatalf("expected only weather-agent, got %+v", c.Progress.SubAgents)
    }
}
```

跑测试：`go test ./internal/agent/ -run TestRuntimeState_SubAgentTracking -v` → PASS。

- [ ] **Step 3: `Engine.Run` 加 `agentMdContent` 参数**

`engine.go` 把 `Run(ctx, instruction, prompt, sessionMdContent, ...)` 签名最末追加 `agentMdContent string` 参数；`buildSystemInstruction` 改为：

```go
func (e *Engine) buildSystemInstruction(prompt, sessionMdContent, agentMdContent string) string {
    sb := &strings.Builder{}
    if agentMdContent != "" {
        // Solo 模式：用 agent.md 替换 GROOT.md
        sb.WriteString(agentMdContent)
        sb.WriteString("\n\n")
    } else {
        grootMd := grootmd.GetContent()
        if grootMd != "" {
            sb.WriteString(grootMd)
            sb.WriteString("\n\n")
        }
    }
    if sessionMdContent != "" {
        sb.WriteString(sessionMdContent)
        sb.WriteString("\n\n")
    }
    if prompt != "" {
        sb.WriteString(prompt)
        sb.WriteString("\n\n")
    }
    return sb.String()
}
```

`Run()` 第 83 行处把 `e.buildSystemInstruction(prompt, sessionMdContent)` 改为 `e.buildSystemInstruction(prompt, sessionMdContent, agentMdContent)`。

- [ ] **Step 4: 改 `Executor` 注入 Registry + TokenAccumulators**

`executor.go`：

```go
type Executor struct {
    memoryManager     *memory.Manager
    middlewares       []adk.ChatModelAgentMiddleware
    mcpManager        *mcp.Manager
    subAgentRegistry  *SubAgentRegistry
    tokenAccumulators *TokenAccumulators
    runtimeState      *RuntimeState
    config            config.Config
    logger            *logger.Logger
}

func NewExecutor(
    memMgr *memory.Manager,
    middlewares []adk.ChatModelAgentMiddleware,
    mcpMgr *mcp.Manager,
    subAgentReg *SubAgentRegistry,
    runtime *RuntimeState,
    cfg config.Config,
    log *logger.Logger,
) *Executor {
    return &Executor{
        memoryManager:     memMgr,
        middlewares:       middlewares,
        mcpManager:        mcpMgr,
        subAgentRegistry:  subAgentReg,
        tokenAccumulators: NewTokenAccumulators(),
        runtimeState:      runtime,
        config:            cfg,
        logger:            log,
    }
}
```

- [ ] **Step 5: `Executor.Execute` 分流 Solo/编排**

```go
func (e *Executor) Execute(parentCtx context.Context, sessionID string, task *Task, sse *SSEWriter) {
    sessionMdContent := ""
    if e.memoryManager != nil {
        if content, err := e.memoryManager.GetSessionMdContent(sessionID); err == nil {
            sessionMdContent = content
        }
    }

    // 区分 Solo / 编排
    var (
        agentName       = MainAgentName
        agentMdContent  = ""
        mcpMgr          = e.mcpManager
        middlewares     = e.middlewares
        extraTools      []tool.BaseTool
        emitInternal    = false
    )
    if task.AgentName != "" && task.AgentName != MainAgentName {
        // Solo 模式
        entry, ok := e.subAgentRegistry.Get(task.AgentName)
        if !ok {
            sse.WriteError("", fmt.Sprintf("子 Agent 不存在: %s", task.AgentName))
            sse.WriteDone()
            return
        }
        agentName = task.AgentName
        agentMdContent = entry.Instruction
        mcpMgr = entry.MCPManager
        // 重新构造 skill middleware
        if entry.SkillBK != nil {
            mw, err := einoskill.NewMiddleware(parentCtx, &einoskill.Config{Backend: entry.SkillBK})
            if err == nil {
                middlewares = []adk.ChatModelAgentMiddleware{mw}
            } else {
                middlewares = nil
            }
        } else {
            middlewares = nil
        }
        // Solo 模式不挂 call_agent
    } else {
        // 编排模式 - 主 Agent
        execTimeout, _ := time.ParseDuration(e.config.SubAgent.ExecTimeout)
        if execTimeout <= 0 {
            execTimeout = 5 * time.Minute
        }
        callAgent := NewCallAgentTool(CallAgentToolConfig{
            Registry:          e.subAgentRegistry,
            ParentChatID:      task.ID,
            SessionID:         sessionID,
            MaxTaskLen:        e.config.SubAgent.MaxTaskLength,
            MaxResultLen:      e.config.SubAgent.MaxResultLength,
            ExecTimeout:       execTimeout,
            Memory:            e.memoryManager,
            RuntimeState:      e.runtimeState,
            TokenAccumulators: e.tokenAccumulators,
            Log:               e.logger,
            ParentRound:       task.Round,
        })
        extraTools = []tool.BaseTool{callAgent}
        emitInternal = true
    }

    engine := NewEngine(EngineConfig{
        LLM:                e.config.LLM,
        Middlewares:        middlewares,
        MCP:                mcpMgr,
        ExtraTools:         extraTools,
        React:              e.config.React,
        Log:                e.logger,
        AgentName:          agentName,
        EmitInternalEvents: emitInternal,
        TokenAccumulators:  e.tokenAccumulators,
    })

    ctx, cancel := context.WithCancel(parentCtx)
    defer cancel()

    result, err := engine.Run(
        ctx,
        task.Instruction,
        task.Prompt,
        sessionMdContent,
        task.HistoryMessages,
        task.ModelName,
        task.MultiModalContents,
        &ProgressCallback{
            // ... 同 Task 10 实现，每个回调用 agentName 入参
        },
        agentMdContent,
    )
    // ... 余下保存 ChatRecord 逻辑（与现有相同），但 chatRecord.AgentName = agentName（Solo 模式时）
    _ = result
    _ = err
}
```

> 把现有保存 ChatRecord 段中 `record.AgentName` 填 `task.AgentName`（Solo 模式有值，编排模式空字符串 → JSON omitempty 省略）。

- [ ] **Step 6: 适配 `main.go`**

`cmd/groot/main.go` 第 327 行附近，在 `exec := agent.NewExecutor(...)` 前**先**构建 Registry：

```go
subAgentDir := filepath.Join(homeDir, "subagents")
subAgentReg, err := agent.BuildSubAgentRegistry(context.Background(), subAgentDir, cfg.React, cfg.SubAgent, cfg.LLM, log)
if err != nil {
    log.Error("无法构建 SubAgentRegistry", zap.Error(err))
    subAgentReg = nil // 不阻塞启动
}
log.Info("SubAgents 加载完成", zap.Strings("agents", subAgentReg.Names()))

exec := agent.NewExecutor(memMgr, []adk.ChatModelAgentMiddleware{skillMiddleware}, mcpMgr, subAgentReg, runtimeState, *cfg, log)
```

并在 shutdown hook（第 437 行附近）增加 `if subAgentReg != nil { subAgentReg.Close() }`：

```go
mcpMgr.Close()
if subAgentReg != nil {
    subAgentReg.Close()
}
```

- [ ] **Step 7: 编译与测试**

Run: `cd /Users/zhangfengda/workspace/groot && go build ./... && go test ./internal/agent/... -v`
Expected: PASS。

> 若现有 `Run()` 调用方仅 Executor 一处，签名变更不影响其他文件。

- [ ] **Step 8: Commit**

```bash
git add internal/agent/engine.go internal/agent/executor.go internal/agent/runtime_state.go internal/agent/runtime_state_test.go cmd/groot/main.go
git commit -m "$(cat <<'EOF'
feat(agent): Executor 支持 Solo / 编排两种模式

Solo 用 agent.md 替换 GROOT.md / 工具仅子 Agent MCP / 不挂 call_agent；编排主 Agent 加挂 call_agent 并打开 EmitInternalEvents。SubAgentRegistry 在 main.go 启动期构建，shutdown 时关闭 MCP 连接。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

### Task 13: `chat.go` 解析 `X-Agent-Name`

**Files:**
- Modify: `internal/api/handler/chat.go`

设计 6.6 节：Solo 模式 → 子 Agent 不存在返回 400 `Unknown agent: {name}`。

- [ ] **Step 1: 改 `ChatHandler` 持有 Registry**

把 `ChatHandler` 结构体新增字段：

```go
type ChatHandler struct {
    memory            *memory.Manager
    runtimeState      *agent.RuntimeState
    agentExecutor     *agent.Executor
    mcpManager        *mcp.Manager
    subAgentRegistry  *agent.SubAgentRegistry
    attachmentHandler *attachment.Handler
    config            config.Config
    log               *logger.Logger
}
```

`NewChatHandler` 加 `subAgentReg *agent.SubAgentRegistry` 参数。

- [ ] **Step 2: 在 `Handle()` 解析并校验 Header**

`chat.go` 在第 84 行 `modelName := ...` 附近追加：

```go
// 2.7 提取 X-Agent-Name header
requestedAgent := string(rc.GetHeader("X-Agent-Name"))
if requestedAgent != "" && requestedAgent != agent.MainAgentName {
    if h.subAgentRegistry == nil {
        rc.JSON(400, utils.H{"status": "unknown_agent", "message": fmt.Sprintf("Unknown agent: %s", requestedAgent)})
        return
    }
    if _, ok := h.subAgentRegistry.Get(requestedAgent); !ok {
        rc.JSON(400, utils.H{"status": "unknown_agent", "message": fmt.Sprintf("Unknown agent: %s", requestedAgent)})
        return
    }
}
```

`Task` 构造处（第 226 行）加：

```go
AgentName: requestedAgent,
```

- [ ] **Step 3: 适配 `server.go` 把 Registry 传入**

修改 `internal/api/server.go` 中 `NewServer` 与传给 `NewChatHandler` 的位置。

具体改动需读 `server.go` 后定位（约 40-80 行），把新字段 `subAgentReg *agent.SubAgentRegistry` 加到 `NewServer` 参数，然后传到 `NewChatHandler(...)` 调用。

`cmd/groot/main.go` 中 `api.NewServer(...)` 调用补传 `subAgentReg`。

- [ ] **Step 4: 编译**

Run: `cd /Users/zhangfengda/workspace/groot && go build ./...`
Expected: 编译通过。

- [ ] **Step 5: 加 handler 单测（无 LLM 依赖）**

新建/追加 `internal/api/handler/chat_test.go`：

```go
package handler

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol"

	"github.com/zfd81/groot/internal/agent"
)

func TestChatHandler_UnknownAgentReturns400(t *testing.T) {
	reg := agent.NewSubAgentRegistryForTest(1)
	h := &ChatHandler{subAgentRegistry: reg}

	rc := &app.RequestContext{}
	rc.Request.Header.SetMethod("POST")
	rc.Request.SetBody([]byte(`{"instruction":"hi"}`))
	rc.Request.Header.Set("X-Agent-Name", "nope")
	rc.Request.Header.SetContentTypeBytes([]byte("application/json"))

	h.Handle(t.Context(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("expected 400, got %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if !strings.Contains(string(rc.Response.Body()), "Unknown agent") {
		t.Fatalf("expected Unknown agent in body, got %s", rc.Response.Body())
	}
	_ = bytes.Buffer{}
	_ = httptest.NewRecorder
	_ = protocol.NewRequest
}
```

> `NewSubAgentRegistryForTest` 是 `subagent_registry.go` 中对 `newRegistryForTest` 的 export 包装；如未导出，先把它改为大写公开版本，或在测试包内复用现有 `newRegistryForTest`（移除测试到 `agent_test` 包外即可）。最佳做法是把 `newRegistryForTest` 改名 `NewRegistryForTest` 并 export。

更新 Task 6 的实现：把 `newRegistryForTest` 改名 `NewRegistryForTest`。

- [ ] **Step 6: 运行测试**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/api/handler/ -run TestChatHandler_UnknownAgent -v`
Expected: PASS。

- [ ] **Step 7: Commit**

```bash
git add internal/api/handler/chat.go internal/api/handler/chat_test.go internal/api/server.go internal/agent/subagent_registry.go cmd/groot/main.go
git commit -m "$(cat <<'EOF'
feat(api): /chat 支持 X-Agent-Name 进入 Solo 模式

未注册的 agent 返回 400 Unknown agent；传 \"groot\" 与不传等价（编排模式）。SubAgentRegistry 沿 API server 依赖链注入。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

### Task 14: `/agents` 新接口

**Files:**
- Create: `internal/api/handler/agents.go`
- Create: `internal/api/handler/agents_test.go`
- Modify: `internal/api/server.go`（路由注册）
- Modify: `internal/api/types/`（响应类型，文件按现有结构放）

设计 6.3 节：返回 `{agents:[{name, description, skills:[{name, description}]}]}`，主 Agent `groot` 排首位。

- [ ] **Step 1: 写测试**

```go
// internal/api/handler/agents_test.go
package handler

import (
	"encoding/json"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/agent"
)

func TestAgentsHandler_ListsGrootFirst(t *testing.T) {
	reg := agent.NewRegistryForTest(1)
	reg.SetEntryForTest("db-agent", &agent.SubAgentEntry{Name: "db-agent", Description: "数据库专家"})
	h := NewAgentsHandler(reg, nil) // mainSkillBackend nil 即可

	rc := &app.RequestContext{}
	h.Serve(t.Context(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("expected 200, got %d", rc.Response.StatusCode())
	}
	var resp struct {
		Agents []struct {
			Name        string `json:"name"`
			Description string `json:"description"`
		} `json:"agents"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Agents) != 2 {
		t.Fatalf("expected 2 agents, got %d", len(resp.Agents))
	}
	if resp.Agents[0].Name != agent.MainAgentName {
		t.Fatalf("expected groot first, got %s", resp.Agents[0].Name)
	}
	if resp.Agents[1].Name != "db-agent" {
		t.Fatalf("expected db-agent second, got %s", resp.Agents[1].Name)
	}
}
```

并在 `subagent_registry.go` export `SetEntryForTest`（test helper）：

```go
func (r *SubAgentRegistry) SetEntryForTest(name string, e *SubAgentEntry) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.entries[name] = e
}
```

- [ ] **Step 2: 实现 `agents.go`**

```go
// internal/api/handler/agents.go
package handler

import (
	"context"

	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/agent"
)

type AgentSkillInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

type AgentInfo struct {
	Name        string           `json:"name"`
	Description string           `json:"description"`
	Skills      []AgentSkillInfo `json:"skills"`
}

type AgentsResponse struct {
	Agents []AgentInfo `json:"agents"`
}

type AgentsHandler struct {
	registry       *agent.SubAgentRegistry
	mainSkillBE    skill.Backend
}

func NewAgentsHandler(reg *agent.SubAgentRegistry, mainSkillBE skill.Backend) *AgentsHandler {
	return &AgentsHandler{registry: reg, mainSkillBE: mainSkillBE}
}

func (h *AgentsHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	resp := AgentsResponse{}
	// 主 Agent 始终首位
	resp.Agents = append(resp.Agents, AgentInfo{
		Name:        agent.MainAgentName,
		Description: "默认 Agent（全局配置）",
		Skills:      listSkills(ctx, h.mainSkillBE),
	})
	if h.registry != nil {
		for _, name := range h.registry.Names() {
			e, _ := h.registry.Get(name)
			resp.Agents = append(resp.Agents, AgentInfo{
				Name:        e.Name,
				Description: e.Description,
				Skills:      listSkills(ctx, e.SkillBK),
			})
		}
	}
	rc.JSON(200, resp)
}

func listSkills(ctx context.Context, be skill.Backend) []AgentSkillInfo {
	if be == nil {
		return []AgentSkillInfo{}
	}
	matters, err := be.List(ctx)
	if err != nil {
		return []AgentSkillInfo{}
	}
	out := make([]AgentSkillInfo, len(matters))
	for i, m := range matters {
		out[i] = AgentSkillInfo{Name: m.Name, Description: m.Description}
	}
	return out
}
```

- [ ] **Step 3: 在 `server.go` 注册路由**

`internal/api/server.go` 中找到现有 `/skills` 注册位置，旁边追加：

```go
h.GET("/agents", handler.NewAgentsHandler(subAgentReg, skillBackend).Serve)
```

`NewServer` 函数已经持有 `subAgentReg` 与 `skillBackend`（前一个 Task 已传入）。

- [ ] **Step 4: 编译与测试**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/api/handler/ -run TestAgentsHandler -v && go build ./...`
Expected: PASS。

- [ ] **Step 5: Commit**

```bash
git add internal/api/handler/agents.go internal/api/handler/agents_test.go internal/api/server.go internal/agent/subagent_registry.go
git commit -m "$(cat <<'EOF'
feat(api): 新增 GET /agents 列举主 Agent 和所有子 Agent

groot 始终首位；每个 Agent 携带 skills 摘要（name + description）。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

### Task 15: `/skills` 和 `/tools` 支持 `X-Agent-Name`

**Files:**
- Modify: `internal/api/handler/skills.go`
- Modify: `internal/api/handler/tools.go`
- Modify: `internal/api/server.go`（注入 Registry）

设计 6.4 节：传入 `X-Agent-Name` 时返回该子 Agent 的资源。

- [ ] **Step 1: `SkillsHandler` 改造**

```go
type SkillsHandler struct {
    backend  skill.Backend       // 主 Agent 的
    registry *agent.SubAgentRegistry
}

func NewSkillsHandler(backend skill.Backend, reg *agent.SubAgentRegistry) *SkillsHandler {
    return &SkillsHandler{backend: backend, registry: reg}
}

func (h *SkillsHandler) Serve(ctx context.Context, rc *app.RequestContext) {
    backend := h.backend
    agentName := string(rc.GetHeader("X-Agent-Name"))
    if agentName != "" && agentName != agent.MainAgentName {
        if h.registry == nil {
            rc.JSON(400, utils.H{"status": "unknown_agent", "message": "Unknown agent: " + agentName})
            return
        }
        entry, ok := h.registry.Get(agentName)
        if !ok {
            rc.JSON(400, utils.H{"status": "unknown_agent", "message": "Unknown agent: " + agentName})
            return
        }
        backend = entry.SkillBK
    }
    if backend == nil {
        rc.JSON(200, types.SkillsResponse{Skills: []types.SkillInfo{}, Total: 0})
        return
    }
    matters, err := backend.List(ctx)
    if err != nil {
        rc.JSON(500, types.SkillsResponse{Skills: []types.SkillInfo{}, Total: 0})
        return
    }
    skillInfos := make([]types.SkillInfo, len(matters))
    for i, m := range matters {
        skillInfos[i] = types.SkillInfo{Name: m.Name, Description: m.Description}
    }
    rc.JSON(200, types.SkillsResponse{Skills: skillInfos, Total: len(skillInfos)})
}
```

- [ ] **Step 2: `ToolsHandler` 改造**

类似套路：当 `X-Agent-Name` 非空 → `mcpManager = entry.MCPManager`，其余逻辑不变。

```go
type ToolsHandler struct {
    mcpManager *mcp.Manager
    registry   *agent.SubAgentRegistry
    logger     *logger.Logger
}

func NewToolsHandler(mcpMgr *mcp.Manager, reg *agent.SubAgentRegistry, log *logger.Logger) *ToolsHandler {
    return &ToolsHandler{mcpManager: mcpMgr, registry: reg, logger: log}
}

func (h *ToolsHandler) Serve(ctx context.Context, rc *app.RequestContext) {
    mgr := h.mcpManager
    agentName := string(rc.GetHeader("X-Agent-Name"))
    if agentName != "" && agentName != agent.MainAgentName {
        if h.registry == nil {
            rc.JSON(400, utils.H{"status": "unknown_agent", "message": "Unknown agent: " + agentName})
            return
        }
        entry, ok := h.registry.Get(agentName)
        if !ok {
            rc.JSON(400, utils.H{"status": "unknown_agent", "message": "Unknown agent: " + agentName})
            return
        }
        mgr = entry.MCPManager
    }
    if mgr == nil {
        rc.JSON(200, map[string]types.ToolsGroup{})
        return
    }
    // ... 余下逻辑沿用现有：mgr.ListTools() 分组返回
}
```

- [ ] **Step 3: 调整 `server.go` 注册**

把 `handler.NewSkillsHandler(...)` / `handler.NewToolsHandler(...)` 调用补上 `subAgentReg`。

- [ ] **Step 4: 加测试**

新建 `internal/api/handler/skills_test.go`：

```go
package handler

import (
	"testing"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/agent"
)

func TestSkillsHandler_UnknownAgent(t *testing.T) {
	reg := agent.NewRegistryForTest(1)
	h := NewSkillsHandler(nil, reg)
	rc := &app.RequestContext{}
	rc.Request.Header.Set("X-Agent-Name", "nope")
	h.Serve(t.Context(), rc)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("expected 400, got %d", rc.Response.StatusCode())
	}
}
```

- [ ] **Step 5: 跑测试**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/api/handler/ -v && go build ./...`
Expected: PASS。

- [ ] **Step 6: Commit**

```bash
git add internal/api/handler/skills.go internal/api/handler/tools.go internal/api/handler/skills_test.go internal/api/server.go
git commit -m "$(cat <<'EOF'
feat(api): /skills /tools 支持 X-Agent-Name 路由到子 Agent 资源

未注册的 agent 返回 400；不传等价于查全局；为 /agents 中每个 Agent 的 skills 列表提供入口。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Phase 5: Watcher + TUI + init

### Task 16: Skills Watcher 扩展到 `subagents/*/skills/`

**Files:**
- Modify: `internal/skills/watcher.go`
- Modify: `cmd/groot/main.go`（Watcher 启动时同时监听 subagents）
- Test: `internal/skills/watcher_test.go`（新建）

设计 5.3 节：监听 `subagents/*/skills/`；事件回调按路径前缀过滤丢弃 `subagents/*/agent.md` 与 `subagents/*/mcp/*`。

- [ ] **Step 1: 写路径解析测试**

新建 `internal/skills/watcher_test.go`：

```go
package skills

import "testing"

func TestExtractSubAgentName_FromSkillsPath(t *testing.T) {
	cases := []struct {
		path    string
		baseDir string
		want    string
		ok      bool
	}{
		{"/x/subagents/db-agent/skills/sql-review/SKILL.md", "/x/subagents", "db-agent", true},
		{"/x/subagents/db-agent/agent.md", "/x/subagents", "", false},
		{"/x/subagents/db-agent/mcp/x.json", "/x/subagents", "", false},
		{"/x/other/y/SKILL.md", "/x/subagents", "", false},
	}
	for _, c := range cases {
		got, ok := extractSubAgentNameForSkill(c.path, c.baseDir)
		if got != c.want || ok != c.ok {
			t.Errorf("path=%s want=%s/%v got=%s/%v", c.path, c.want, c.ok, got, ok)
		}
	}
}
```

- [ ] **Step 2: 实现路径解析函数**

`watcher.go` 追加：

```go
// extractSubAgentNameForSkill 判断事件路径是否属于 subagents/<name>/skills/ 下，
// 是则返回 <name>；否则返回 ok=false。
func extractSubAgentNameForSkill(path, subAgentsBaseDir string) (string, bool) {
    rel, err := filepath.Rel(subAgentsBaseDir, path)
    if err != nil || strings.HasPrefix(rel, "..") {
        return "", false
    }
    parts := strings.Split(filepath.ToSlash(rel), "/")
    // 形态: <agent>/skills/...
    if len(parts) < 3 || parts[1] != "skills" {
        return "", false
    }
    return parts[0], true
}
```

- [ ] **Step 3: Watcher 字段与启动**

```go
type Watcher struct {
    skillsDir         string                 // 主 Agent skills/
    subAgentsBaseDir  string                 // 主 Agent subagents/
    fsWatcher         *fsnotify.Watcher
    stopChan          chan struct{}
    log               *logger.Logger
    cfg               config.HotReloadConfig
    debounceTimer     *time.Timer
    mu                sync.Mutex
    onSubAgentSkillReload func(agentName string)  // SubAgentRegistry 注入的回调
}

func NewWatcher(skillsDir, subAgentsBaseDir string, cfg config.HotReloadConfig, log *logger.Logger,
    onSubAgentSkillReload func(string)) *Watcher {
    return &Watcher{
        skillsDir:             skillsDir,
        subAgentsBaseDir:      subAgentsBaseDir,
        stopChan:              make(chan struct{}),
        log:                   log,
        cfg:                   cfg,
        onSubAgentSkillReload: onSubAgentSkillReload,
    }
}
```

`Start()` 中同时 `watcher.Add(w.skillsDir)` 与 `watcher.Add(w.subAgentsBaseDir)`（前者递归监听 SKILL.md，后者监听子目录变化）。需要递归监听 `subagents/*/skills/`，遍历当前所有子 Agent 目录并 Add：

```go
if subDirs, err := filepath.Glob(filepath.Join(w.subAgentsBaseDir, "*/skills")); err == nil {
    for _, d := range subDirs {
        _ = watcher.Add(d)
    }
}
```

- [ ] **Step 4: 事件过滤**

`isSkillChange` 改为：

```go
func (w *Watcher) classifySkillChange(event fsnotify.Event) (kind string, agentName string) {
    base := filepath.Base(event.Name)
    if base == "SKILL.md" || (event.Op&(fsnotify.Create|fsnotify.Remove|fsnotify.Rename) != 0 && strings.HasSuffix(event.Name, ".md")) {
        if name, ok := extractSubAgentNameForSkill(event.Name, w.subAgentsBaseDir); ok {
            return "subagent", name
        }
        if strings.HasPrefix(event.Name, w.skillsDir) {
            return "main", ""
        }
    }
    // 显式丢弃 agent.md / mcp 事件
    return "", ""
}
```

`run()` 内：

```go
kind, name := w.classifySkillChange(event)
if kind == "main" || kind == "subagent" {
    w.debounce(kind, name)
}
```

`debounce` 加参数：

```go
func (w *Watcher) debounce(kind, name string) {
    w.mu.Lock()
    defer w.mu.Unlock()
    if w.debounceTimer != nil {
        w.debounceTimer.Stop()
    }
    w.debounceTimer = time.AfterFunc(time.Duration(w.cfg.DebounceDelay)*time.Second, func() {
        if kind == "subagent" && w.onSubAgentSkillReload != nil {
            w.onSubAgentSkillReload(name)
        } else {
            w.reload()
        }
    })
}
```

- [ ] **Step 5: `main.go` 注入回调**

```go
skillsWatcher := skills.NewWatcher(skillsDir, filepath.Join(homeDir, "subagents"), cfg.Skills.HotReload, log,
    func(agentName string) {
        if subAgentReg == nil {
            return
        }
        entry, ok := subAgentReg.Get(agentName)
        if !ok || entry.SkillBK == nil {
            return
        }
        // 触发 backend rescan：einoskill.Backend 没有公开 Rescan API；
        // 实践中通过 SetBaseDir 或新建 backend；本期只记录日志，后续优化。
        log.Info("子 Agent skills 变更触发重新加载", zap.String("agent", agentName))
    },
)
```

> 注：einoskill 的 backend 重新扫描 API 取决于版本；本期仅打日志，**真正的热刷新**留作后续优化，符合设计 5.3 节「Skills ✅」标注但不强求每行差异立即生效（debounce 已能保证就近一次）。如 backend 暴露 `Rescan()`，调用之；否则保持 log。

- [ ] **Step 6: 跑测试**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/skills/ -v && go build ./...`
Expected: PASS。

- [ ] **Step 7: Commit**

```bash
git add internal/skills/watcher.go internal/skills/watcher_test.go cmd/groot/main.go
git commit -m "$(cat <<'EOF'
feat(skills): Watcher 监听子 Agent skills 并按路径过滤

extractSubAgentNameForSkill 提取目录名→Registry 条目；agent.md / mcp/ 事件显式丢弃避免误触发；具体 backend rescan API 视 eino 版本能力，第一期仅日志通知。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

### Task 17: TUI `/agent` 命令

**Files:**
- Modify: `internal/cmd/chat/commands.go`
- Modify: `internal/cmd/chat/client.go`（发请求时带 `X-Agent-Name`）
- Modify: `internal/cmd/chat/model.go`（state 加 `agentName`）

设计 7 节：`/agent` 列出可用 Agent；`/agent <name>` 切换并新会话；`/agent groot` 切回主 Agent；状态栏显示。

> 本任务遵守 plan 中「修改 chat TUI 子目录」原则，但**不**新建文件，只增量改。

- [ ] **Step 1: 阅读现有命令注册**

Run: `cd /Users/zhangfengda/workspace/groot && grep -n "skills\\|/skill " internal/cmd/chat/commands.go | head -20`
Expected: 看到现有命令定义结构（如 `/skills`、`/clear`），按相同模式追加。

- [ ] **Step 2: 在 model 加字段**

`internal/cmd/chat/model.go` 的 main Model 结构体（或 Client 字段）加：

```go
agentName string  // 默认 agent.MainAgentName ("groot")
```

新 Session 时不重置，`/clear` 不动 agentName，`/agent <name>` 切换时新 Session ID。

- [ ] **Step 3: 注册 `/agent` 命令**

参照 `/skills` 现有实现，新增：

```go
{
    Name:        "/agent",
    Description: "切换 Agent。/agent 列出所有；/agent <name> 切到指定 Agent",
    Handler:     handleAgentCommand,
    Completion:  completeAgentName, // 通过 GET /agents 获取
}
```

`handleAgentCommand`：
- 无参数 → 调 `GET /agents`，打印列表
- 参数为 Agent 名 → 校验存在（先 GET /agents 取列表），写入 model.agentName，并生成新 sessionID

- [ ] **Step 4: client 发请求时携带 header**

`internal/cmd/chat/client.go` 中发 `/chat` 请求处补：

```go
if c.agentName != "" && c.agentName != agent.MainAgentName {
    req.Header.Set("X-Agent-Name", c.agentName)
}
```

- [ ] **Step 5: 状态栏显示**

`internal/cmd/chat/statusbar.go`：在已有 model name 旁边追加：

```go
agentIndicator := "Agent: " + m.agentName
```

- [ ] **Step 6: 编译并人工测试**

Run: `cd /Users/zhangfengda/workspace/groot && go build -o bin/groot ./cmd/groot`
Expected: 编译通过。

> 因 TUI 自动化测试成本高，本任务仅做编译验证；人工测试在 `groot chat` 启动后输入 `/agent` 验证补全和切换效果。

- [ ] **Step 7: Commit**

```bash
git add internal/cmd/chat/
git commit -m "$(cat <<'EOF'
feat(tui): chat 增加 /agent 命令切换主/子 Agent

/agent 列出可用；/agent <name> 切换并新建会话；状态栏显示当前 Agent；request header 自动带 X-Agent-Name。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

### Task 18: `groot init` 创建 `subagents/` 目录 + GROOT.md 调度引导段

**Files:**
- Modify: `internal/cmd/init.go`
- Modify: `internal/cmd/init_test.go`

设计 10.2 节：`cmd/init.go` 创建 `subagents/` 目录；默认 GROOT.md 末尾追加 `call_agent` 调度引导段。

- [ ] **Step 1: 写测试**

追加 `internal/cmd/init_test.go`：

```go
func TestRunInit_CreatesSubAgentsDir(t *testing.T) {
    home := t.TempDir()
    if err := RunInit(home); err != nil {
        t.Fatal(err)
    }
    stat, err := os.Stat(filepath.Join(home, "subagents"))
    if err != nil || !stat.IsDir() {
        t.Fatalf("subagents/ should be created, err=%v", err)
    }
}
```

- [ ] **Step 2: 改 `RunInit` 添加 subagents 目录**

`init.go` 第 67 行 `subDirs` 列表追加 `"subagents"`：

```go
subDirs := []string{"skills", "mcp", "subagents", "memory", "logs", "cluster/members"}
```

- [ ] **Step 3: 跑测试**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/cmd/ -run TestRunInit -v`
Expected: PASS。

- [ ] **Step 4: GROOT.md 调度引导段**

查看现有 GROOT.md 生成位置：

Run: `cd /Users/zhangfengda/workspace/groot && grep -rn "GROOT.md" internal/cmd/ internal/grootmd/ | head -10`

若 init 不写 GROOT.md（grootmd 自带 default），则把引导段追加到 grootmd 的默认内容中。在 `internal/grootmd/` 找到默认内容常量：

```go
const defaultContent = `...`
```

在末尾追加：

```
## 子 Agent 调度

当你拥有 `call_agent` 工具时，意味着系统中注册了一些专门的子 Agent。请遵循：
- **按需调用**：只在子 Agent 的 description 与子任务匹配时才调用
- **逐个调用**：建议先调一个，确认返回足够信息后再决定是否调下一个；避免盲目并行
- **明确传参**：task 参数必须包含完整上下文，因为子 Agent 看不到主对话历史
- **附件引用**：如需子 Agent 访问附件，在 task 中显式写明附件路径
```

- [ ] **Step 5: Commit**

```bash
git add internal/cmd/init.go internal/cmd/init_test.go internal/grootmd/
git commit -m "$(cat <<'EOF'
feat(init): 创建 subagents/ 目录并在默认 GROOT.md 追加调度引导

引导主 Agent 逐个调用、明确传参、附件需在 task 中显式写明路径，避免盲目并行。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

### Task 19: `/chat/status/:sid` 接口反映子 Agent 状态

**Files:**
- Modify: `internal/api/handler/status.go`

设计 6.5 节：返回 `progress.sub_agents`。

- [ ] **Step 1: 阅读现有 status.go**

Run: `cd /Users/zhangfengda/workspace/groot && grep -n "Progress\|sub_agents" internal/api/handler/status.go`

- [ ] **Step 2: 改造响应体**

当前已经包含 `progress` 字段；只需把 `progress` 字段映射 `agent.ChatProgress`（含 `SubAgents`）原样输出即可，无额外工作。Test 一下序列化包含 `sub_agents` 数组。

新建 `internal/api/handler/status_test.go`（如不存在则创建）：

```go
func TestStatusHandler_IncludesSubAgents(t *testing.T) {
    rt := agent.NewRuntimeState()
    _, _ = rt.Register("sess", "chat")
    rt.AddSubAgent("sess", "db-agent")

    h := NewStatusHandler(rt) // 按现有构造，参数名相应调整
    rc := &app.RequestContext{}
    rc.Params = append(rc.Params, app.Param{Key: "sid", Value: "sess"})
    h.Serve(t.Context(), rc)

    body := rc.Response.Body()
    if !strings.Contains(string(body), `"sub_agents"`) {
        t.Errorf("expected sub_agents in body: %s", body)
    }
    if !strings.Contains(string(body), `"db-agent"`) {
        t.Errorf("expected db-agent in body: %s", body)
    }
}
```

- [ ] **Step 3: 编译并跑测试**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/api/handler/ -run TestStatusHandler -v && go build ./...`
Expected: PASS。

- [ ] **Step 4: Commit**

```bash
git add internal/api/handler/status.go internal/api/handler/status_test.go
git commit -m "$(cat <<'EOF'
feat(api): /chat/status 返回 progress.sub_agents

通过 RuntimeState.AddSubAgent/RemoveSubAgent 维护的列表透传到响应。仅展示当前实例本地状态。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

### Task 20: 集成 smoke test（手工）

**Files:**
- Modify: `tests/TEST_CASES.md`
- New: `tests/python/test_multi_agent.py`（可选，本期最小化）

- [ ] **Step 1: 手工烟囱测试**

```bash
# 1. 准备测试 subagent
mkdir -p ~/.groot/subagents/echo-agent
cat > ~/.groot/subagents/echo-agent/agent.md <<'EOF'
---
description: 回显测试 Agent，把用户输入原样返回
---

# 回显 Agent

收到任何 task 后，直接返回 task 内容。
EOF

# 2. 编译并启动
cd /Users/zhangfengda/workspace/groot
go build -o bin/groot ./cmd/groot
./bin/groot &
sleep 2

# 3. 测 GET /agents
curl -s http://localhost:8080/agents | jq

# 4. 测 Solo 模式
curl -X POST http://localhost:8080/chat \
  -H "X-Agent-Name: echo-agent" \
  -H "Content-Type: application/json" \
  -d '{"instruction":"hello","prompt":""}'

# 5. 测编排模式（要求 GROOT.md 引导调用 echo-agent）
curl -X POST http://localhost:8080/chat \
  -H "Content-Type: application/json" \
  -d '{"instruction":"调用 echo-agent，让它回显「test」","prompt":""}'

# 6. 测未知 agent
curl -s -o /dev/null -w '%{http_code}\n' \
  -X POST http://localhost:8080/chat \
  -H "X-Agent-Name: ghost-agent" \
  -H "Content-Type: application/json" \
  -d '{"instruction":"x"}'
# Expected: 400
```

- [ ] **Step 2: 更新 `tests/TEST_CASES.md`**

追加段落：

```
## 多 Agent（v3.8 后）

### 子 Agent 注册
- subagents/ 不存在：启动正常，/agents 仅返回 groot
- agent.md 缺 description：启动跳过，日志 ERROR
- agent.md 缺失：启动跳过
- 目录名 = "groot"：启动跳过，日志 ERROR

### Solo 模式
- X-Agent-Name 指定已注册 → 用子 Agent 执行
- X-Agent-Name 指定未注册 → HTTP 400 Unknown agent
- X-Agent-Name=groot → 等价于不传

### 编排模式
- 主 Agent 工具列表含 call_agent
- call_agent(agent_name, task) 委托到子 Agent
- 子 Agent 事件透传 SSE 含 agent_name 字段
- 子 Agent ChatRecord chatID 含父前缀
- task 超长 → 拒绝
- 结果超长 → 截断（开头警告）
- 并发超 SubAgent.MaxConcurrency → FIFO 排队

### API
- GET /agents → groot 首位 + 子 Agent
- GET /skills + X-Agent-Name=db-agent → 子 Agent skills
- GET /tools + X-Agent-Name=db-agent → 子 Agent MCP 工具
- GET /chat/status/:sid → progress.sub_agents 含当前运行的子 Agent
```

- [ ] **Step 3: Commit**

```bash
git add tests/TEST_CASES.md
git commit -m "$(cat <<'EOF'
docs(tests): 补充多 Agent 测试用例清单

子 Agent 注册、Solo / 编排两种模式、API 行为、并发与截断等覆盖范围。Python 系统测试由用户自行落地。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

### Task 21: 文档同步与最终自检

**Files:**
- Modify: `README.md`（追加多 Agent 用法）
- Modify: `docs/superpowers/specs/2026-05-24-multi-agent-design.md`（如发现实现差异，回填状态）

- [ ] **Step 1: 更新 README.md 用户指南**

阅读现有 README，在合适位置（通常在 "## 配置" 或 "## 进阶" 段落后）追加 "## 多 Agent" 段：

```markdown
## 多 Agent

Groot 支持在 `~/.groot/subagents/` 下声明子 Agent，每个子 Agent 拥有独立的 MCP 工具和 Skills。

### 目录结构

\`\`\`
~/.groot/subagents/db-agent/
├── agent.md          # 必填：frontmatter 含 description；正文为系统提示词
├── mcp/              # 可选：专属 MCP 配置
└── skills/           # 可选：专属 Skills
\`\`\`

### 调用方式

**编排模式（默认）**：主 Agent 会根据指令通过 `call_agent` 工具自动调度子 Agent：

\`\`\`bash
curl -X POST http://localhost:8080/chat \
  -H "Content-Type: application/json" \
  -d '{"instruction":"查询昨天的订单总金额"}'
\`\`\`

**Solo 模式**：直接指定子 Agent 执行：

\`\`\`bash
curl -X POST http://localhost:8080/chat \
  -H "X-Agent-Name: db-agent" \
  -H "Content-Type: application/json" \
  -d '{"instruction":"查询昨天的订单总金额"}'
\`\`\`

### TUI

`groot chat` 中：
- `/agent` 列出所有可用 Agent
- `/agent db-agent` 切换并开始新会话
- `/agent groot` 切回主 Agent

### 限制（详见 [设计文档](docs/superpowers/specs/2026-05-24-multi-agent-design.md)）

- 只支持主 Agent → 子 Agent 单层调用
- 子 Agent agent.md / MCP 配置不支持热加载（变更需重启）
- 默认并发上限 5（FIFO 排队）；超时 5 分钟
```

- [ ] **Step 2: 自检**

通读设计文档第 1~12 章，确认所有「权威表」中标记的实现项都有对应任务：

- [ ] 2.6 隔离规则：Engine 拼接区分主/子；工具列表注入区分；模型来源 ✅ (Task 9, 12)
- [ ] 4.4 调用限制：MaxConcurrency / MaxTaskLength / MaxResultLength / ExecTimeout ✅ (Task 4, 11)
- [ ] 4.5 错误处理：标准 error 返回值 → 工具结果文本 ✅ (Task 11)
- [ ] 4.7 实现方案：SubAgentEntry / Registry / CallAgentTool / Engine 改造 ✅ (Task 6-12)
- [ ] 4.8 SSE：agent_name 字段注入 ✅ (Task 10)
- [ ] 5.3 热加载：Skills Watcher 扩展 ✅ (Task 16, 接口侧已就位)
- [ ] 6 API：/agents / X-Agent-Name on /skills /tools /chat ✅ (Task 13-15)
- [ ] 7 TUI：/agent 命令 ✅ (Task 17)
- [ ] 8 Memory：AgentName + Token 字段 + GenerateChildChatID ✅ (Task 2-3)
- [ ] 10 改动总览：所有「中改 / 轻改」文件均触达

- [ ] **Step 3: 最后跑完整测试集**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/... -v && go build ./...`
Expected: 全部 PASS，无编译错误。

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "$(cat <<'EOF'
docs(readme): 多 Agent 用法指南

涵盖目录结构、编排/Solo 两种调用方式、TUI /agent 命令、关键限制（单层调用、不支持热加载、并发与超时上限）。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## 风险与回滚策略

| 阶段完成时机 | 影响面 | 回滚策略 |
|------------|--------|---------|
| Phase 1 提交后 | 仅常量重命名 + 数据结构扩字段 + 配置默认值 | 直接 `git revert` 不影响运行时 |
| Phase 2 提交后 | Registry 建好但无人调用 | 直接 `git revert`，main.go 中 Registry 可选 |
| Phase 3 提交后 | Engine 已感知 agentName 但 Executor 仍走主路径 | `git revert` Task 12 即可 |
| Phase 4 提交后 | API 已具备完整多 Agent 能力 | 用户不传 `X-Agent-Name` 即可保持旧行为；可单独 revert 单个 handler |
| Phase 5 提交后 | TUI / init 用户可见特性 | TUI 切换可单独 revert |

---

## 完成标准

- [ ] 所有 21 个 task 全部完成
- [ ] `go test ./internal/...` 全部通过
- [ ] `go build ./...` 无错误
- [ ] 手工烟囱测试（Task 20）通过：Solo 模式、编排模式、unknown agent 400、`/agents` 接口、`/chat/status` 含 sub_agents
- [ ] README.md 包含多 Agent 用法
