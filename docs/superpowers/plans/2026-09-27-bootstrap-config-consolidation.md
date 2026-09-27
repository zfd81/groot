# 单一 bootstrap.yaml 配置文件实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 用单一的 `~/.groot/bootstrap.yaml` 取代 `config.yaml` 与 `env.yaml`，文件只保留启动路径上读取的配置，其余业务配置全部存放于数据库配置表。

**Architecture:** 配置按「读取时机」而非「是否敏感」划分归属。`bootstrap.yaml` 承载在数据库连接建立之前、或不连数据库就要读到的项：`agent`、`server`、`logging`、`database`，以及消息层、调度器、限流回收协程这三处的构造参数。其余业务项（含 JWT 签名密钥）存放于数据库配置表，由 `internal/setting.Settings` 统一读取。老部署首次启动时做一次性迁移：`config.yaml` 与 `env.yaml` 的内容分别落到 `bootstrap.yaml` 与配置表，两个老文件原地保留、不再被读取。

**Tech Stack:** Go 1.x、`gopkg.in/yaml.v3`、Go 标准测试框架、Vue 3（前端一处常量）

**配置归属对照表**（实现时以此表为准）

| 配置项 | 归属 |
|---|---|
| `agent.name` / `agent.version` | bootstrap.yaml |
| `server.host` / `server.port` | bootstrap.yaml |
| `logging.*` | bootstrap.yaml |
| `database.*` | bootstrap.yaml |
| `message.queue_size` / `message.workers` | bootstrap.yaml |
| `schedule.max_concurrent_tasks` / `schedule.sync_interval` | bootstrap.yaml |
| `security.rate_limit.cleanup_interval` | bootstrap.yaml |
| `security.auth.secret` / `security.auth.header_name` | 配置表（启动时读一次） |
| `security.rate_limit` 其余五项 | 配置表（已在表中） |
| `memory.*` / `react.*` / `subagent.*` / `attachment.*` | 配置表（已在表中） |
| `schedule.enabled` | 配置表（已在表中） |
| `message.senders.*` | 配置表（已在表中） |

---

## Task 1: 设计文档

**Files:**
- Create: `docs/superpowers/specs/2026-09-27-bootstrap-config-design.md`

项目规范要求先写设计文档再写代码，且「功能设计」与「迭代说明」两部分禁止交叉 —— 功能设计部分独立完整地正面陈述，不得出现"相比之前""新增了""原来是 X 现在是 Y"之类措辞；对比性语言一律放进「二、迭代说明」章节。

- [ ] **Step 1: 写设计文档**

按以下骨架撰写，内容取自本计划的 Architecture 段与配置归属对照表：

```markdown
# 单一 bootstrap.yaml 配置文件设计文档

## 一、功能设计

### 1.1 功能概述
（Groot 的文件配置集中于 ~/.groot/bootstrap.yaml 一个文件，承载启动路径上
读取的配置；业务配置存放于数据库配置表，由设置面板维护。正面陈述，不提旧版。）

### 1.2 划分依据
（读取时机：日志要先就绪才能记录数据库打开失败；服务端口要在监听前确定；
groot status 与 groot tail 不连数据库也要取到端口与日志目录。）

### 1.3 配置项归属
（照搬本计划的配置归属对照表。）

### 1.4 文件形态
（bootstrap.yaml 权限 0600；全注释即可工作，缺省值由代码提供；
DSN 支持 ${ENV_VAR} 展开。）

### 1.5 JWT 签名密钥
（存放于配置表：集群各节点共享同一数据库，因此共享同一密钥，
A 节点签发的 API Key 在 B 节点可验证。启动时读取一次，缺失则生成并写入。
更换密钥使全部 API Key 失效，因此改动需重启，不经界面暴露。）

### 1.6 启动顺序
（LoadBootstrap → 迁移老文件 → logger → db.Open → repos → settings →
EnsureAuthSecret → AssembleConfig → 其余组件。）

### 1.7 老部署迁移
（首次启动的一次性迁移规则，含"只迁非零值""目标侧已有值不覆盖"
"老文件原地保留"三条。）

### 1.8 集群同步范围
（同步白名单为 skills、subagents、mcp、GROOT.md，均为纯资源文件。
配置的共享经数据库配置表完成。）

### 1.9 改动清单
（文件级清单，照搬本计划各 Task 的 Files 段。）

## 二、迭代说明

### 2.1 与上一版差异
- 新增：……
- 移除：……
- 调整：……
```

- [ ] **Step 2: 提交**

```bash
git add docs/superpowers/specs/2026-09-27-bootstrap-config-design.md
git commit -m "docs: 单一 bootstrap.yaml 配置文件设计文档"
```

---

## Task 2: Bootstrap 结构与加载器

**Files:**
- Create: `internal/config/bootstrap.go`
- Test: `internal/config/bootstrap_test.go`

本任务只新增代码，不删改现有的 `Load` / `loadEnvFile`，编译与既有测试全程保持通过。

- [ ] **Step 1: 写失败的测试**

`internal/config/bootstrap_test.go`：

```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

// writeBootstrap 在 dir 写入 bootstrap.yaml
func writeBootstrap(t *testing.T, dir, content string) {
	t.Helper()
	p := filepath.Join(dir, BootstrapFileName)
	if err := os.WriteFile(p, []byte(content), 0600); err != nil {
		t.Fatalf("写入 bootstrap.yaml 失败: %v", err)
	}
}

// TestLoadBootstrap_Missing 文件缺失时提示先运行 init
func TestLoadBootstrap_Missing(t *testing.T) {
	_, err := LoadBootstrap(t.TempDir())
	if err == nil {
		t.Fatal("文件缺失应报错")
	}
	if !strings.Contains(err.Error(), "groot init") {
		t.Errorf("错误信息应提示运行 groot init, got %q", err.Error())
	}
}

// TestLoadBootstrap_AllCommented 全注释文件应等价于全套缺省值
func TestLoadBootstrap_AllCommented(t *testing.T) {
	dir := t.TempDir()
	writeBootstrap(t, dir, "# 全注释\n")

	b, err := LoadBootstrap(dir)
	if err != nil {
		t.Fatalf("LoadBootstrap: %v", err)
	}
	if b.Agent.Name != "groot" || b.Agent.Version != "1.0.0" {
		t.Errorf("agent = %+v, want groot/1.0.0", b.Agent)
	}
	if b.Server.Host != "0.0.0.0" || b.Server.Port != 8080 {
		t.Errorf("server = %+v, want 0.0.0.0:8080", b.Server)
	}
	if b.Message.QueueSize != 256 || b.Message.Workers != 2 {
		t.Errorf("message = %+v, want 256/2", b.Message)
	}
	if b.Schedule.MaxConcurrentTasks != 3 || b.Schedule.SyncInterval != "30s" {
		t.Errorf("schedule = %+v, want 3/30s", b.Schedule)
	}
	if b.Security.RateLimit.CleanupInterval != "5m" {
		t.Errorf("cleanup_interval = %q, want 5m", b.Security.RateLimit.CleanupInterval)
	}
	if b.Logging.Level != "info" || b.Logging.Format != "json" {
		t.Errorf("logging = %+v, want info/json", b.Logging)
	}
	if b.Logging.File.Directory != "logs" || b.Logging.File.MaxAge != 7 {
		t.Errorf("logging.file = %+v, want logs/7", b.Logging.File)
	}
	if b.Database != nil {
		t.Errorf("无 database 节时应为 nil, got %+v", b.Database)
	}
}

// TestLoadBootstrap_Values 显式值覆盖缺省值
func TestLoadBootstrap_Values(t *testing.T) {
	dir := t.TempDir()
	writeBootstrap(t, dir, `
server:
  host: 127.0.0.1
  port: 9090
logging:
  level: debug
  file:
    directory: /var/log/groot
message:
  queue_size: 512
schedule:
  sync_interval: 10s
security:
  rate_limit:
    cleanup_interval: 1m
`)

	b, err := LoadBootstrap(dir)
	if err != nil {
		t.Fatalf("LoadBootstrap: %v", err)
	}
	if b.Server.Host != "127.0.0.1" || b.Server.Port != 9090 {
		t.Errorf("server = %+v", b.Server)
	}
	if b.Logging.Level != "debug" || b.Logging.File.Directory != "/var/log/groot" {
		t.Errorf("logging = %+v", b.Logging)
	}
	// 同节内未给出的字段仍取缺省值
	if b.Logging.Format != "json" || b.Message.Workers != 2 {
		t.Errorf("同节内缺省值未填充: format=%q workers=%d", b.Logging.Format, b.Message.Workers)
	}
	if b.Message.QueueSize != 512 || b.Schedule.SyncInterval != "10s" {
		t.Errorf("message/schedule = %+v / %+v", b.Message, b.Schedule)
	}
	if b.Security.RateLimit.CleanupInterval != "1m" {
		t.Errorf("cleanup_interval = %q", b.Security.RateLimit.CleanupInterval)
	}
}

// TestLoadBootstrap_DatabaseEnvExpand DSN 中的 ${VAR} 展开为环境变量值
func TestLoadBootstrap_DatabaseEnvExpand(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("GROOT_TEST_DSN", "user:pw@tcp(h:3306)/groot")
	writeBootstrap(t, dir, `
database:
  driver: mysql
  dsn: ${GROOT_TEST_DSN}
`)

	b, err := LoadBootstrap(dir)
	if err != nil {
		t.Fatalf("LoadBootstrap: %v", err)
	}
	if b.Database == nil {
		t.Fatal("database 节应被解析")
	}
	if b.Database.Driver != "mysql" {
		t.Errorf("driver = %q, want mysql", b.Database.Driver)
	}
	if b.Database.DSN != "user:pw@tcp(h:3306)/groot" {
		t.Errorf("DSN 未展开: %q", b.Database.DSN)
	}
}

// TestLoadBootstrap_Malformed 非法 yaml 应报错而非静默取缺省值
func TestLoadBootstrap_Malformed(t *testing.T) {
	dir := t.TempDir()
	writeBootstrap(t, dir, "server:\n  port: [不是数字\n")
	if _, err := LoadBootstrap(dir); err == nil {
		t.Fatal("非法 yaml 应报错")
	}
}
```

测试用到 `strings`，import 段补上 `"strings"`。

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/config/ -run TestLoadBootstrap -v`
Expected: 编译失败，`undefined: BootstrapFileName`、`undefined: LoadBootstrap`

- [ ] **Step 3: 写实现**

`internal/config/bootstrap.go`：

```go
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// BootstrapFileName 是 Groot 唯一的 YAML 配置文件名。
const BootstrapFileName = "bootstrap.yaml"

// Bootstrap 是 bootstrap.yaml 的结构，承载启动路径上读取的配置。
//
// 判据是「读取点在数据库连接建立之前，或不依赖数据库」：日志要先就绪才能
// 记录数据库打开失败，服务端口要在监听前确定，groot status 与 groot tail
// 不连数据库也要取到端口与日志目录。其余业务配置存放于数据库配置表。
type Bootstrap struct {
	Agent    AgentConfig       `yaml:"agent"`
	Server   ServerConfig      `yaml:"server"`
	Logging  LoggingConfig     `yaml:"logging"`
	Database *DatabaseConfig   `yaml:"database,omitempty"`
	Message  MessageBootstrap  `yaml:"message"`
	Schedule ScheduleBootstrap `yaml:"schedule"`
	Security SecurityBootstrap `yaml:"security"`
}

// MessageBootstrap 消息层的构造参数。发送渠道参数在配置表中。
type MessageBootstrap struct {
	QueueSize int `yaml:"queue_size"`
	Workers   int `yaml:"workers"`
}

// ScheduleBootstrap 调度器的构造参数。enabled 开关在配置表中。
type ScheduleBootstrap struct {
	MaxConcurrentTasks int    `yaml:"max_concurrent_tasks"`
	SyncInterval       string `yaml:"sync_interval"`
}

// SecurityBootstrap 只承载限流空闲桶的回收周期（后台协程的定时参数）。
// auth 与限流的五项阈值在配置表中。
type SecurityBootstrap struct {
	RateLimit RateLimitBootstrap `yaml:"rate_limit"`
}

// RateLimitBootstrap 限流的启动期参数。
type RateLimitBootstrap struct {
	CleanupInterval string `yaml:"cleanup_interval"`
}

// LoadBootstrap 读取并解析 bootstrap.yaml，填充缺省值并展开 DSN 中的环境变量。
// 文件缺失视为未初始化，提示先运行 groot init。
func LoadBootstrap(homeDir string) (*Bootstrap, error) {
	path := filepath.Join(homeDir, BootstrapFileName)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("配置文件不存在，请先运行 'groot init' 初始化")
		}
		return nil, fmt.Errorf("failed to read bootstrap file: %w", err)
	}

	b := &Bootstrap{}
	if err := yaml.Unmarshal(data, b); err != nil {
		return nil, fmt.Errorf("failed to parse bootstrap file: %w", err)
	}

	applyBootstrapDefaults(b)
	if b.Database != nil {
		b.Database.DSN = ExpandEnv(b.Database.DSN)
	}
	return b, nil
}

// applyBootstrapDefaults 为未给出的字段填充缺省值，
// 使全注释的 bootstrap.yaml 即为一份可用配置。
func applyBootstrapDefaults(b *Bootstrap) {
	if b.Agent.Name == "" {
		b.Agent.Name = "groot"
	}
	if b.Agent.Version == "" {
		b.Agent.Version = "1.0.0"
	}

	if b.Server.Host == "" {
		b.Server.Host = "0.0.0.0"
	}
	if b.Server.Port == 0 {
		b.Server.Port = 8080
	}

	if b.Message.QueueSize == 0 {
		b.Message.QueueSize = 256
	}
	if b.Message.Workers == 0 {
		b.Message.Workers = 2
	}

	if b.Schedule.MaxConcurrentTasks == 0 {
		b.Schedule.MaxConcurrentTasks = 3
	}
	if b.Schedule.SyncInterval == "" {
		b.Schedule.SyncInterval = "30s"
	}

	if b.Security.RateLimit.CleanupInterval == "" {
		b.Security.RateLimit.CleanupInterval = "5m"
	}

	if b.Logging.Level == "" {
		b.Logging.Level = "info"
	}
	if b.Logging.Format == "" {
		b.Logging.Format = "json"
	}
	if len(b.Logging.Output) == 0 {
		b.Logging.Output = []string{"stdout", "file"}
	}
	if b.Logging.File.Directory == "" {
		b.Logging.File.Directory = "logs"
	}
	if b.Logging.File.FilenamePattern == "" {
		b.Logging.File.FilenamePattern = "groot-{date}.log"
	}
	if b.Logging.File.MaxAge == 0 {
		b.Logging.File.MaxAge = 7
	}
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./internal/config/ -run TestLoadBootstrap -v`
Expected: 全部 PASS

- [ ] **Step 5: 提交**

```bash
git add internal/config/bootstrap.go internal/config/bootstrap_test.go
git commit -m "feat(config): 新增 bootstrap.yaml 结构与加载器"
```

---

## Task 3: bootstrap.yaml 模板

**Files:**
- Create: `internal/config/bootstrap_template.go`
- Test: `internal/config/bootstrap_template_test.go`

- [ ] **Step 1: 写失败的测试**

`internal/config/bootstrap_template_test.go`：

```go
package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// TestGenerateBootstrapTemplate_Parses 模板必须是合法 yaml，
// 且解析后等价于全套缺省值（模板全注释）。
func TestGenerateBootstrapTemplate_Parses(t *testing.T) {
	tpl := GenerateBootstrapTemplate()

	var b Bootstrap
	if err := yaml.Unmarshal([]byte(tpl), &b); err != nil {
		t.Fatalf("模板不是合法 yaml: %v", err)
	}
	if b.Server.Port != 0 || b.Database != nil {
		t.Errorf("模板应全注释，解析后字段为零值, got port=%d database=%+v", b.Server.Port, b.Database)
	}

	// 经 LoadBootstrap 读取后应拿到缺省值
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, BootstrapFileName), []byte(tpl), 0600); err != nil {
		t.Fatalf("写入模板失败: %v", err)
	}
	loaded, err := LoadBootstrap(dir)
	if err != nil {
		t.Fatalf("LoadBootstrap: %v", err)
	}
	if loaded.Server.Port != 8080 || loaded.Logging.Level != "info" {
		t.Errorf("模板经加载后应为缺省值, got port=%d level=%q", loaded.Server.Port, loaded.Logging.Level)
	}
}

// TestGenerateBootstrapTemplate_NoBusinessSections 模板不应引导使用者
// 在文件里配置已迁入配置表的业务项。
func TestGenerateBootstrapTemplate_NoBusinessSections(t *testing.T) {
	tpl := GenerateBootstrapTemplate()
	for _, banned := range []string{"react:", "attachment:", "subagent:", "memory:", "senders:", "secret:"} {
		if strings.Contains(tpl, banned) {
			t.Errorf("模板不应包含已迁入配置表的 %q", banned)
		}
	}
	// 数据库两种驱动各一个示例块
	if !strings.Contains(tpl, "driver: mysql") || !strings.Contains(tpl, "driver: postgres") {
		t.Error("模板应为 MySQL 与 PostgreSQL 各提供示例")
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/config/ -run TestGenerateBootstrapTemplate -v`
Expected: 编译失败，`undefined: GenerateBootstrapTemplate`

- [ ] **Step 3: 写实现**

`internal/config/bootstrap_template.go`。注意两个数据库示例块沿用 `env.yaml` 模板的「先缩进后 #」格式（如 `#  driver:`），使用者删掉行首 `#` 后 yaml 缩进自动正确；两块同时取消注释会因重复键解析失败，这是预期行为。

```go
package config

// GenerateBootstrapTemplate 返回 ~/.groot/bootstrap.yaml 的初始模板。
//
// 模板内容全注释：每一项都由代码提供缺省值，使用者只在需要偏离缺省值时
// 取消对应行的注释。业务配置不出现在模板中 —— 它们存放于数据库配置表，
// 由 Web 设置面板维护。
func GenerateBootstrapTemplate() string {
	return `# Groot 配置文件
#
# 本文件承载启动时读取的配置：服务监听、日志、数据库连接，以及消息层与
# 调度器的构造参数。其余业务配置（模型、限流阈值、通知渠道、附件限制、
# 定时任务开关等）存放在数据库中，请启动服务后在 Web 设置面板中维护。
#
# 全部配置项都有缺省值，整个文件保持注释即可正常启动。
# 改动本文件需重启服务才生效。

# ─── Agent 元信息 ───
#agent:
#  name: groot                        # Agent 名称
#  version: 1.0.0                     # Agent 版本号

# ─── HTTP 服务 ───
#server:
#  host: 0.0.0.0                      # 监听地址
#  port: 8080                         # 监听端口

# ─── 日志 ───
#logging:
#  level: info                        # 日志级别：debug/info/warn/error
#  format: json                       # 日志格式：json/text
#  output: [stdout, file]             # 输出目标
#  file:
#    directory: logs                  # 日志目录（相对路径以 GROOT_HOME 为基准）
#    filename_pattern: groot-{date}.log
#    max_age: 7                       # 日志保留天数

# ─── 消息层构造参数 ───
# 通知渠道（Webhook / 邮件）的地址与凭据在设置面板中配置。
#message:
#  queue_size: 256                    # 发送队列容量
#  workers: 2                         # 发送工作协程数

# ─── 调度器构造参数 ───
# 是否允许模型创建定时任务，在设置面板中开关。
#schedule:
#  max_concurrent_tasks: 3            # 最大并发执行数
#  sync_interval: 30s                 # 目录同步间隔

# ─── 限流后台协程 ───
# 限流开关与各项阈值在设置面板中配置。
#security:
#  rate_limit:
#    cleanup_interval: 5m             # 空闲限流器回收周期

# ─── 数据库 ───
# 整节保持注释即为 SQLite 本地模式（数据库文件 GROOT_HOME/groot.db），零配置。
# 启用 MySQL / PostgreSQL：二选一，取消对应示例块的注释并填入真实连接信息。
# DSN 中的密码建议通过 ${ENV_VAR} 引用环境变量。
# 注意：同一时间只能启用一个 database 块，否则 yaml 解析会冲突。

# ─── 示例 1：MySQL ───
#database:
#  driver: mysql
#  dsn: "user:${GROOT_DB_PASSWORD}@tcp(host:3306)/groot?charset=utf8mb4&parseTime=True&loc=UTC"
#  max_open_conns: 20                 # 最大打开连接数（默认 20）
#  max_idle_conns: 5                  # 最大空闲连接数（默认 5）
#  conn_max_lifetime: 30m             # 连接最大生命周期（默认 30m）

# ─── 示例 2：PostgreSQL ───
#database:
#  driver: postgres
#  dsn: "host=host port=5432 user=groot password=${GROOT_DB_PASSWORD} dbname=groot sslmode=disable TimeZone=UTC"
#  max_open_conns: 20
#  max_idle_conns: 5
#  conn_max_lifetime: 30m

# 完整配置说明请参考：https://github.com/zfd81/groot
`
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./internal/config/ -run TestGenerateBootstrapTemplate -v`
Expected: 全部 PASS

- [ ] **Step 5: 提交**

```bash
git add internal/config/bootstrap_template.go internal/config/bootstrap_template_test.go
git commit -m "feat(config): 新增 bootstrap.yaml 模板"
```

---

## Task 4: setting 包的代码默认值

**Files:**
- Modify: `internal/setting/defaults.go`
- Test: `internal/setting/defaults_test.go`

配置表分类此前以 YAML 值为基准层。`config.yaml` 消失后基准层变为代码默认值，需要把原先散落在 `config.applyDefaults` 里的业务项默认值搬进 setting 包。

- [ ] **Step 1: 写失败的测试**

`internal/setting/defaults_test.go`：

```go
package setting

import "testing"

// TestDefaults 断言各分类的代码默认值。数值取自迁移前 config.applyDefaults
// 中的同名项，保证未在表中设置过任何值的部署行为不变。
func TestDefaults(t *testing.T) {
	if got := defaultMemory(); got.HistoryWindow != 20 {
		t.Errorf("memory.history_window = %d, want 20", got.HistoryWindow)
	}

	r := defaultReact()
	if r.MaxIterations != 20 || r.StepTimeout != 60 || r.ErrorRetry != 2 {
		t.Errorf("react = %+v, want 20/60/2", r)
	}

	s := defaultSubAgent()
	if s.MaxConcurrency != 5 || s.ExecTimeout != "5m" {
		t.Errorf("subagent 并发/超时 = %d/%q, want 5/5m", s.MaxConcurrency, s.ExecTimeout)
	}
	if s.MaxTaskLength != 16000 || s.MaxResultLength != 8000 {
		t.Errorf("subagent 长度 = %d/%d, want 16000/8000", s.MaxTaskLength, s.MaxResultLength)
	}

	a := defaultAttachment()
	if a.MaxSize != 50 || a.MaxTotalSize != 100 || a.MaxCount != 10 {
		t.Errorf("attachment = %+v, want 50/100/10", a)
	}
	if len(a.AllowedTypes) != 0 {
		t.Errorf("attachment.allowed_types 默认应为空（允许所有）, got %v", a.AllowedTypes)
	}

	rl := defaultRateLimit()
	if rl.Enabled {
		t.Error("限流默认应关闭")
	}
	if rl.DefaultQPS != 10 || rl.DefaultConcurrency != 5 {
		t.Errorf("限流默认配额 = %v/%d, want 10/5", rl.DefaultQPS, rl.DefaultConcurrency)
	}
	if rl.GlobalQPS != 0 || rl.GlobalConcurrency != 0 {
		t.Errorf("全局限流默认应为 0（不限制）, got %v/%d", rl.GlobalQPS, rl.GlobalConcurrency)
	}

	if defaultScheduleEnabled() {
		t.Error("定时任务默认应关闭")
	}

	if got := defaultAuthHeaderName(); got != "X-API-Key" {
		t.Errorf("auth.header_name = %q, want X-API-Key", got)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/setting/ -run TestDefaults -v`
Expected: 编译失败，`undefined: defaultMemory` 等

- [ ] **Step 3: 写实现**

在 `internal/setting/defaults.go` 中追加（保留已有的 `defaultVoice`），并确保 import 了 `"github.com/zfd81/groot/internal/config"`：

```go
// 以下各函数是配置表分类的基准层。表中缺失的键回落到这里，
// 因此配置表在创建后为空即可工作。

// defaultMemory 记忆模块默认值。
func defaultMemory() config.MemoryConfig {
	return config.MemoryConfig{HistoryWindow: 20}
}

// defaultReact ReAct 循环默认值。
func defaultReact() config.ReactConfig {
	return config.ReactConfig{MaxIterations: 20, StepTimeout: 60, ErrorRetry: 2}
}

// defaultSubAgent 子 Agent 调度默认值。
func defaultSubAgent() config.SubAgentConfig {
	return config.SubAgentConfig{
		MaxConcurrency:  5,
		ExecTimeout:     "5m",
		MaxTaskLength:   16000,
		MaxResultLength: 8000,
	}
}

// defaultAttachment 附件默认值。AllowedTypes 为空表示允许所有类型。
func defaultAttachment() config.AttachmentConfig {
	return config.AttachmentConfig{MaxSize: 50, MaxTotalSize: 100, MaxCount: 10}
}

// defaultRateLimit 限流默认值。CleanupInterval 不在此列：
// 它是后台回收协程的周期，恒取 bootstrap.yaml 的值。
func defaultRateLimit() config.RateLimitConfig {
	return config.RateLimitConfig{
		Enabled:            false,
		GlobalQPS:          0,
		GlobalConcurrency:  0,
		DefaultQPS:         10,
		DefaultConcurrency: 5,
	}
}

// defaultScheduleEnabled 定时任务默认关闭：
// 允许模型自行创建定时任务是一项需要明确开启的策略。
func defaultScheduleEnabled() bool { return false }

// defaultAuthHeaderName API Key 请求头的默认名称。
func defaultAuthHeaderName() string { return "X-API-Key" }
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./internal/setting/ -run TestDefaults -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/setting/defaults.go internal/setting/defaults_test.go
git commit -m "feat(setting): 配置表分类的代码默认值"
```

---

## Task 5: JWT 签名密钥与请求头名进配置表

**Files:**
- Create: `internal/setting/auth.go`
- Test: `internal/setting/auth_test.go`

密钥放进配置表而非 `bootstrap.yaml`，是为了保住集群语义：各节点共享同一数据库因而共享同一密钥，A 节点签发的 API Key 在 B 节点可以验证。密钥只在启动时读一次 —— 认证中间件在每个请求上执行，不能为它增加一次数据库查询；而更换密钥会使全部已签发的 API Key 立即失效，本就该是重启级别的操作。

注意：此时 `setting.New` 的签名仍是 `New(config.Config, repo.SettingRepo)`（Task 6 才改为 `config.Bootstrap`），本任务的测试先用 `config.Config{}` 构造，Task 6 的 Step 4 会统一改为 `config.Bootstrap{}`。

- [ ] **Step 1: 写失败的测试**

`internal/setting/auth_test.go`。复用包内已有的假仓库（见 `runtime_test.go` / `message_test.go` 中的 fake `repo.SettingRepo` 实现，构造函数名以那里的为准）：

```go
package setting

import (
	"context"
	"encoding/hex"
	"errors"
	"testing"

	"github.com/zfd81/groot/internal/config"
)

// TestAuth_Defaults 空表时返回默认请求头名与空密钥
func TestAuth_Defaults(t *testing.T) {
	s := New(config.Config{}, newFakeSettingRepo())

	a, err := s.Auth(context.Background())
	if err != nil {
		t.Fatalf("Auth: %v", err)
	}
	if a.HeaderName != "X-API-Key" {
		t.Errorf("HeaderName = %q, want X-API-Key", a.HeaderName)
	}
	if a.Secret != "" {
		t.Errorf("空表时 Secret 应为空, got %q", a.Secret)
	}
}

// TestEnsureAuthSecret_Generates 密钥缺失时生成并写表，返回值为 64 位 hex
func TestEnsureAuthSecret_Generates(t *testing.T) {
	s := New(config.Config{}, newFakeSettingRepo())
	ctx := context.Background()

	secret, err := s.EnsureAuthSecret(ctx)
	if err != nil {
		t.Fatalf("EnsureAuthSecret: %v", err)
	}
	if len(secret) != 64 {
		t.Fatalf("密钥长度 = %d, want 64", len(secret))
	}
	if _, err := hex.DecodeString(secret); err != nil {
		t.Errorf("密钥应为 hex: %v", err)
	}

	// 已落表：再次读取拿到同一个值
	a, err := s.Auth(ctx)
	if err != nil {
		t.Fatalf("Auth: %v", err)
	}
	if a.Secret != secret {
		t.Errorf("表中密钥 = %q, want %q", a.Secret, secret)
	}
}

// TestEnsureAuthSecret_Idempotent 已有密钥时原样返回，不改表
func TestEnsureAuthSecret_Idempotent(t *testing.T) {
	s := New(config.Config{}, newFakeSettingRepo())
	ctx := context.Background()

	first, err := s.EnsureAuthSecret(ctx)
	if err != nil {
		t.Fatalf("首次 EnsureAuthSecret: %v", err)
	}
	second, err := s.EnsureAuthSecret(ctx)
	if err != nil {
		t.Fatalf("再次 EnsureAuthSecret: %v", err)
	}
	if first != second {
		t.Errorf("密钥被重新生成: %q → %q（会使已签发的 API Key 全部失效）", first, second)
	}
}

// TestEnsureAuthSecret_NoRepo 无配置表仓库时明确报错，不静默放过空密钥
func TestEnsureAuthSecret_NoRepo(t *testing.T) {
	s := New(config.Config{}, nil)
	if _, err := s.EnsureAuthSecret(context.Background()); !errors.Is(err, ErrNoSettingStore) {
		t.Errorf("err = %v, want ErrNoSettingStore", err)
	}
}

// TestAuth_HeaderNameFromTable 表中的请求头名覆盖默认值
func TestAuth_HeaderNameFromTable(t *testing.T) {
	r := newFakeSettingRepo()
	s := New(config.Config{}, r)
	ctx := context.Background()

	if err := s.SetAuthHeaderName(ctx, "X-Groot-Key"); err != nil {
		t.Fatalf("SetAuthHeaderName: %v", err)
	}
	a, err := s.Auth(ctx)
	if err != nil {
		t.Fatalf("Auth: %v", err)
	}
	if a.HeaderName != "X-Groot-Key" {
		t.Errorf("HeaderName = %q, want X-Groot-Key", a.HeaderName)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/setting/ -run 'TestAuth|TestEnsureAuthSecret' -v`
Expected: 编译失败，`undefined: (*Settings).Auth`、`undefined: (*Settings).EnsureAuthSecret`

- [ ] **Step 3: 写实现**

`internal/setting/auth.go`：

```go
// internal/setting/auth.go
// 认证配置：JWT 签名密钥与 API Key 请求头名。
//
// 密钥存放在配置表而非 bootstrap.yaml，因此集群各节点共享同一密钥，
// 任一节点签发的 API Key 在其余节点均可验证。
//
// 读取时机是启动一次：认证中间件在每个请求上执行，不为它增加数据库查询；
// 更换密钥会使全部已签发的 API Key 立即失效，属于重启级别的操作。
package setting

import (
	"context"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/repo"
)

// 认证配置在配置表中的键名。
const (
	KeyAuthSecret     = "security.auth.secret"
	KeyAuthHeaderName = "security.auth.header_name"
)

// Auth 读取认证配置。密钥缺失时 Secret 为空串，
// 由 EnsureAuthSecret 在启动时补齐。
func (s *Settings) Auth(ctx context.Context) (config.AuthConfig, error) {
	out := config.AuthConfig{HeaderName: defaultAuthHeaderName()}
	if s.repo == nil {
		return out, nil
	}
	vals, err := s.globalValues(ctx)
	if err != nil {
		return config.AuthConfig{}, err
	}
	if raw, ok := vals[KeyAuthHeaderName]; ok && raw != "" {
		out.HeaderName = raw
	}
	out.Secret = vals[KeyAuthSecret]
	return out, nil
}

// EnsureAuthSecret 返回 JWT 签名密钥：表中已有则原样返回，
// 缺失则生成一个并写入表中。
//
// 幂等是硬要求 —— 每次启动都换一个密钥会让已发出去的 API Key 全部失效。
func (s *Settings) EnsureAuthSecret(ctx context.Context) (string, error) {
	if s.repo == nil {
		return "", ErrNoSettingStore
	}
	vals, err := s.globalValues(ctx)
	if err != nil {
		return "", err
	}
	if secret := vals[KeyAuthSecret]; secret != "" {
		return secret, nil
	}
	secret, err := config.GenerateAuthSecret()
	if err != nil {
		return "", err
	}
	if err := s.repo.Upsert(ctx,
		&repo.Setting{Scope: repo.ScopeGlobal, Name: KeyAuthSecret, Value: secret}); err != nil {
		return "", err
	}
	return secret, nil
}

// SetAuthHeaderName 保存 API Key 请求头名。改动需重启服务才生效。
func (s *Settings) SetAuthHeaderName(ctx context.Context, name string) error {
	if s.repo == nil {
		return ErrNoSettingStore
	}
	return s.repo.Upsert(ctx,
		&repo.Setting{Scope: repo.ScopeGlobal, Name: KeyAuthHeaderName, Value: name})
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./internal/setting/ -run 'TestAuth|TestEnsureAuthSecret' -v`
Expected: 全部 PASS

若假仓库的构造函数名与 `newFakeSettingRepo` 不同，改测试去对齐包内既有命名，不要新增一份重复的假实现。

- [ ] **Step 5: 提交**

```bash
git add internal/setting/auth.go internal/setting/auth_test.go
git commit -m "feat(setting): JWT 签名密钥与请求头名迁入配置表"
```

---

## Task 6: Settings 改以 Bootstrap 为静态层

**Files:**
- Modify: `internal/setting/settings.go`
- Modify: `internal/setting/runtime.go`（`RuntimeSettings` 的基准层）
- Modify: `internal/setting/message.go:57`（`Message` 方法的基准层）
- Test: `internal/setting/settings_test.go`

`Settings.static` 的类型从 `config.Config` 换成 `config.Bootstrap`，各分类的基准层改用 Task 4 的默认值函数。这一步会让 `internal/setting` 的既有测试与调用方一起编译失败，同一提交内改完。

- [ ] **Step 1: 改 Settings 结构与 YAML 分类**

`internal/setting/settings.go`：

```go
// Settings 配置对象。
//
// 全部分类方法统一签名 Xxx(ctx) (T, error)：来自 bootstrap.yaml 的分类不使用
// ctx 且 error 恒为 nil，但保持签名一致，使某一分类日后迁移到配置表时
// 调用方无需改动。
//
// 来自配置表的分类每次调用查询数据库，不做内存缓存：设置面板保存后当次请求
// 即可读到新值，多节点共享同一数据库时各节点取值一致。
type Settings struct {
	static config.Bootstrap
	repo   repo.SettingRepo
}

// New 构造配置对象。settingRepo 为 nil 时，来自配置表的分类一律返回代码默认值，
// SetVoice 等写入方法返回 ErrNoSettingStore，
// 便于在尚未接入数据库的场景（如部分单元测试）中使用。
func New(static config.Bootstrap, settingRepo repo.SettingRepo) *Settings {
	return &Settings{static: static, repo: settingRepo}
}

// ---- 来自 bootstrap.yaml 的分类 ----

// Agent 返回 agent 元信息。
func (s *Settings) Agent(ctx context.Context) (config.AgentConfig, error) {
	return s.static.Agent, nil
}

// Server 返回 HTTP 服务配置。
func (s *Settings) Server(ctx context.Context) (config.ServerConfig, error) {
	return s.static.Server, nil
}

// Logging 返回日志配置。
func (s *Settings) Logging(ctx context.Context) (config.LoggingConfig, error) {
	return s.static.Logging, nil
}

// Database 返回数据库连接配置；nil 表示 SQLite 本地模式。
func (s *Settings) Database(ctx context.Context) (*config.DatabaseConfig, error) {
	return s.static.Database, nil
}
```

- [ ] **Step 2: 改配置表分类的基准层**

同文件内，把六个分类的基准从 `s.static.X` 换成默认值函数。`Memory` 为例，其余五个同法：

```go
// Memory 读取记忆配置。
func (s *Settings) Memory(ctx context.Context) (config.MemoryConfig, error) {
	base := defaultMemory()
	if s.repo == nil {
		return base, nil
	}
	vals, err := s.globalValues(ctx)
	if err != nil {
		return config.MemoryConfig{}, err
	}
	return memoryFrom(base, vals), nil
}
```

对应替换：`React` → `defaultReact()`，`SubAgent` → `defaultSubAgent()`，`Attachment` → `defaultAttachment()`。

`Security` 的 Auth 部分改为复用 Task 5 的 `Auth`，RateLimit 的 `CleanupInterval` 取自 bootstrap：

```go
// Security 返回安全配置。Auth 与限流阈值来自配置表，
// 限流的 CleanupInterval 来自 bootstrap.yaml（后台回收协程的周期）。
func (s *Settings) Security(ctx context.Context) (config.SecurityConfig, error) {
	auth, err := s.Auth(ctx)
	if err != nil {
		return config.SecurityConfig{}, err
	}
	base := defaultRateLimit()
	base.CleanupInterval = s.static.Security.RateLimit.CleanupInterval
	out := config.SecurityConfig{Auth: auth}
	if s.repo == nil {
		out.RateLimit = base
		return out, nil
	}
	vals, err := s.globalValues(ctx)
	if err != nil {
		return config.SecurityConfig{}, err
	}
	out.RateLimit = rateLimitFrom(base, vals)
	return out, nil
}
```

`Schedule` 的两个构造参数取自 bootstrap，`Enabled` 取自表：

```go
// Schedule 返回定时任务配置。Enabled 来自配置表，
// MaxConcurrentTasks 与 SyncInterval 来自 bootstrap.yaml（调度器构造参数）。
func (s *Settings) Schedule(ctx context.Context) (config.ScheduleConfig, error) {
	base := config.ScheduleConfig{
		Enabled:            defaultScheduleEnabled(),
		MaxConcurrentTasks: s.static.Schedule.MaxConcurrentTasks,
		SyncInterval:       s.static.Schedule.SyncInterval,
	}
	if s.repo == nil {
		return base, nil
	}
	vals, err := s.globalValues(ctx)
	if err != nil {
		return config.ScheduleConfig{}, err
	}
	return scheduleFrom(base, vals), nil
}
```

`Runtime` 与 `RuntimeStatic`（若存在）的 base 同样改用默认值函数加 bootstrap 的三个构造参数：

```go
// Runtime 一次取出全部运行时配置，供设置面板加载。
func (s *Settings) Runtime(ctx context.Context) (RuntimeSettings, error) {
	rl := defaultRateLimit()
	rl.CleanupInterval = s.static.Security.RateLimit.CleanupInterval
	base := RuntimeSettings{
		Memory:     defaultMemory(),
		React:      defaultReact(),
		SubAgent:   defaultSubAgent(),
		Attachment: defaultAttachment(),
		RateLimit:  rl,
		Schedule: config.ScheduleConfig{
			Enabled:            defaultScheduleEnabled(),
			MaxConcurrentTasks: s.static.Schedule.MaxConcurrentTasks,
			SyncInterval:       s.static.Schedule.SyncInterval,
		},
	}
	if s.repo == nil {
		return base, nil
	}
	vals, err := s.globalValues(ctx)
	if err != nil {
		return RuntimeSettings{}, err
	}
	return RuntimeSettings{
		Memory:     memoryFrom(base.Memory, vals),
		React:      reactFrom(base.React, vals),
		SubAgent:   subAgentFrom(base.SubAgent, vals),
		Attachment: attachmentFrom(base.Attachment, vals),
		RateLimit:  rateLimitFrom(base.RateLimit, vals),
		Schedule:   scheduleFrom(base.Schedule, vals),
	}, nil
}
```

- [ ] **Step 3: 改 Message 的基准层**

`internal/setting/message.go:57` 的 `Message` 方法，队列参数取自 bootstrap，渠道默认全关：

```go
// Message 读取消息通知配置。QueueSize 与 Workers 来自 bootstrap.yaml
// （消息层构造参数），发送渠道参数来自配置表。
func (s *Settings) Message(ctx context.Context) (config.MessageConfig, error) {
	base := config.MessageConfig{
		QueueSize: s.static.Message.QueueSize,
		Workers:   s.static.Message.Workers,
		Senders:   map[string]config.SenderConf{},
	}
	if s.repo == nil {
		return base, nil
	}
	vals, err := s.globalValues(ctx)
	if err != nil {
		return config.MessageConfig{}, err
	}
	out := base
	out.Senders = map[string]config.SenderConf{
		"webhook": senderConfFrom(config.SenderConf{}, "webhook", vals),
		"email":   senderConfFrom(config.SenderConf{SMTPPort: 587}, "email", vals),
	}
	return out, nil
}
```

保留该方法原有的渠道枚举方式与 `senderConfFrom` 调用形态，只替换 base 的来源；若原实现从 `s.static.Message.Senders` 遍历渠道名，改为遍历上面的固定两个渠道名。

- [ ] **Step 4: 修既有测试的构造调用**

`internal/setting/` 下全部测试里的 `New(config.Config{...}, ...)` 改为 `New(config.Bootstrap{...}, ...)`。原先通过 `config.Config` 注入 YAML 基准值来断言"YAML 居中层"的用例，改为断言代码默认值 —— YAML 不再是业务项的基准层。

Run: `go test ./internal/setting/ -v 2>&1 | tail -40`
逐个修到编译通过、全绿。

- [ ] **Step 5: 运行测试，确认通过**

Run: `go test ./internal/setting/ -race -v`
Expected: 全部 PASS

- [ ] **Step 6: 提交**

```bash
git add internal/setting/
git commit -m "refactor(setting): 静态层由 config.Config 改为 config.Bootstrap"
```

---

## Task 7: 老配置文件迁移 —— 文件侧

**Files:**
- Create: `internal/config/migrate_legacy.go`
- Test: `internal/config/migrate_legacy_test.go`

把老 `config.yaml` 的 bootstrap 项与老 `env.yaml` 的 `database` 节写成 `bootstrap.yaml`，并把 `config.yaml` 里的业务项交给调用方（Task 8 写入配置表）。

两条关键规则：

1. **只迁非零值。** 老 `config.yaml` 模板几乎全是注释，解析结果多为零值；把零值迁走会写入 `port: 0` 这类无效配置。零值一律跳过，由缺省值兜底。布尔项的 `false` 与默认值相同，跳过无影响。
2. **`bootstrap.yaml` 已存在即视为迁移完毕**，直接返回，不做任何事。

- [ ] **Step 1: 写失败的测试**

`internal/config/migrate_legacy_test.go`：

```go
package config

import (
	"os"
	"path/filepath"
	"testing"

	"gopkg.in/yaml.v3"
)

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
		t.Fatalf("写入 %s 失败: %v", name, err)
	}
}

// TestMigrateLegacy_NoLegacyFiles 干净目录上不生成任何文件
func TestMigrateLegacy_NoLegacyFiles(t *testing.T) {
	dir := t.TempDir()
	lb, err := MigrateLegacy(dir)
	if err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}
	if lb != nil {
		t.Errorf("无老文件时应返回 nil, got %+v", lb)
	}
	if _, err := os.Stat(filepath.Join(dir, BootstrapFileName)); !os.IsNotExist(err) {
		t.Error("无老文件时不应生成 bootstrap.yaml")
	}
}

// TestMigrateLegacy_AlreadyMigrated bootstrap.yaml 已存在则原样不动
func TestMigrateLegacy_AlreadyMigrated(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, BootstrapFileName, "server:\n  port: 7777\n")
	writeFile(t, dir, "config.yaml", "server:\n  port: 9999\n")

	lb, err := MigrateLegacy(dir)
	if err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}
	if lb != nil {
		t.Errorf("已迁移过应返回 nil, got %+v", lb)
	}
	data, _ := os.ReadFile(filepath.Join(dir, BootstrapFileName))
	if !strings.Contains(string(data), "7777") {
		t.Errorf("已存在的 bootstrap.yaml 被覆盖: %s", data)
	}
}

// TestMigrateLegacy_BootstrapFields bootstrap 项落入 bootstrap.yaml
func TestMigrateLegacy_BootstrapFields(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", `
agent:
  name: myagent
server:
  host: 127.0.0.1
  port: 9090
logging:
  level: debug
  file:
    directory: /var/log/groot
    max_age: 30
message:
  queue_size: 512
  workers: 4
schedule:
  max_concurrent_tasks: 8
  sync_interval: 15s
security:
  rate_limit:
    cleanup_interval: 2m
`)
	writeFile(t, dir, "env.yaml", `
database:
  driver: mysql
  dsn: "u:p@tcp(h:3306)/groot"
  max_open_conns: 50
`)

	if _, err := MigrateLegacy(dir); err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}

	b, err := LoadBootstrap(dir)
	if err != nil {
		t.Fatalf("LoadBootstrap: %v", err)
	}
	if b.Agent.Name != "myagent" {
		t.Errorf("agent.name = %q, want myagent", b.Agent.Name)
	}
	if b.Server.Host != "127.0.0.1" || b.Server.Port != 9090 {
		t.Errorf("server = %+v", b.Server)
	}
	if b.Logging.Level != "debug" || b.Logging.File.Directory != "/var/log/groot" || b.Logging.File.MaxAge != 30 {
		t.Errorf("logging = %+v", b.Logging)
	}
	if b.Message.QueueSize != 512 || b.Message.Workers != 4 {
		t.Errorf("message = %+v", b.Message)
	}
	if b.Schedule.MaxConcurrentTasks != 8 || b.Schedule.SyncInterval != "15s" {
		t.Errorf("schedule = %+v", b.Schedule)
	}
	if b.Security.RateLimit.CleanupInterval != "2m" {
		t.Errorf("cleanup_interval = %q", b.Security.RateLimit.CleanupInterval)
	}
	if b.Database == nil || b.Database.Driver != "mysql" || b.Database.MaxOpenConns != 50 {
		t.Fatalf("database 未从 env.yaml 迁入: %+v", b.Database)
	}
	// 权限 0600：文件不再含密钥，但仍含数据库凭据
	info, err := os.Stat(filepath.Join(dir, BootstrapFileName))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0600 {
		t.Errorf("bootstrap.yaml 权限 = %o, want 0600", perm)
	}
}

// TestMigrateLegacy_BusinessFields 业务项经返回值交出，不写进文件
func TestMigrateLegacy_BusinessFields(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", `
memory:
  history_window: 50
react:
  max_iterations: 30
  step_timeout: 90
attachment:
  max_size: 200
  allowed_types: [".pdf", ".txt"]
schedule:
  enabled: true
security:
  auth:
    secret: "deadbeef"
    header_name: X-Groot-Key
  rate_limit:
    enabled: true
    default_qps: 25
message:
  senders:
    webhook:
      enabled: true
      url: "https://hook.example.com"
`)

	lb, err := MigrateLegacy(dir)
	if err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}
	if lb == nil {
		t.Fatal("应返回待迁入配置表的业务项")
	}
	if lb.Memory.HistoryWindow != 50 || lb.React.MaxIterations != 30 || lb.React.StepTimeout != 90 {
		t.Errorf("memory/react 未交出: %+v %+v", lb.Memory, lb.React)
	}
	if lb.Attachment.MaxSize != 200 || len(lb.Attachment.AllowedTypes) != 2 {
		t.Errorf("attachment 未交出: %+v", lb.Attachment)
	}
	if !lb.ScheduleEnabled {
		t.Error("schedule.enabled=true 未交出")
	}
	if lb.Auth.Secret != "deadbeef" || lb.Auth.HeaderName != "X-Groot-Key" {
		t.Errorf("auth 未交出: %+v", lb.Auth)
	}
	if !lb.RateLimit.Enabled || lb.RateLimit.DefaultQPS != 25 {
		t.Errorf("rate_limit 未交出: %+v", lb.RateLimit)
	}
	w, ok := lb.Senders["webhook"]
	if !ok || !w.Enabled || w.URL != "https://hook.example.com" {
		t.Errorf("senders 未交出: %+v", lb.Senders)
	}

	// 业务项不得出现在 bootstrap.yaml 中
	data, _ := os.ReadFile(filepath.Join(dir, BootstrapFileName))
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatalf("生成的 bootstrap.yaml 非法: %v", err)
	}
	for _, banned := range []string{"memory", "react", "attachment", "subagent"} {
		if _, ok := raw[banned]; ok {
			t.Errorf("bootstrap.yaml 不应包含业务节 %q", banned)
		}
	}
	if !strings.Contains(string(data), "# 本文件由") {
		t.Error("迁移生成的文件应带说明性头注释")
	}
}

// TestMigrateLegacy_AllCommentedLegacy 全注释的老 config.yaml 迁移后
// 不把零值写进文件，加载结果为全套缺省值
func TestMigrateLegacy_AllCommentedLegacy(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, dir, "config.yaml", "# 全注释\n")
	writeFile(t, dir, "env.yaml", "# 全注释\n")

	if _, err := MigrateLegacy(dir); err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}
	b, err := LoadBootstrap(dir)
	if err != nil {
		t.Fatalf("LoadBootstrap: %v", err)
	}
	if b.Server.Port != 8080 || b.Logging.Level != "info" || b.Message.QueueSize != 256 {
		t.Errorf("全注释迁移后应为缺省值: port=%d level=%q queue=%d",
			b.Server.Port, b.Logging.Level, b.Message.QueueSize)
	}
	if b.Database != nil {
		t.Errorf("env.yaml 全注释时 database 应为 nil, got %+v", b.Database)
	}
}

// TestMigrateLegacy_KeepsLegacyFiles 老文件原地保留，不删不改
func TestMigrateLegacy_KeepsLegacyFiles(t *testing.T) {
	dir := t.TempDir()
	const original = "server:\n  port: 9090\n"
	writeFile(t, dir, "config.yaml", original)

	if _, err := MigrateLegacy(dir); err != nil {
		t.Fatalf("MigrateLegacy: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.yaml"))
	if err != nil {
		t.Fatalf("老 config.yaml 被删除: %v", err)
	}
	if string(data) != original {
		t.Errorf("老 config.yaml 被改动:\n%s", data)
	}
}
```

import 段需含 `"strings"`。

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/config/ -run TestMigrateLegacy -v`
Expected: 编译失败，`undefined: MigrateLegacy`

- [ ] **Step 3: 写实现**

`internal/config/migrate_legacy.go`：

```go
// internal/config/migrate_legacy.go
// 老配置文件（config.yaml + env.yaml）到 bootstrap.yaml 的一次性迁移。
//
// bootstrap 项写入 bootstrap.yaml；业务项打包为 LegacyBusiness 返回，
// 由调用方在数据库就绪后写入配置表。老文件原地保留、不再被读取，
// 使用者确认无误后可自行删除。
package config

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"
)

// LegacyBusiness 是老 config.yaml 中待迁入配置表的业务项。
// 各字段为解析原文件的结果，零值表示原文件未设置该项 ——
// 写表方（cmd 层）遵循「只迁非零值、表内已有值不覆盖」。
type LegacyBusiness struct {
	Memory          MemoryConfig
	React           ReactConfig
	SubAgent        SubAgentConfig
	Attachment      AttachmentConfig
	RateLimit       RateLimitConfig
	ScheduleEnabled bool
	Auth            AuthConfig
	Senders         map[string]SenderConf
}

// legacyConfigFile 描述老 config.yaml 的顶层结构（bootstrap 项与业务项的并集）。
type legacyConfigFile struct {
	Agent      AgentConfig      `yaml:"agent"`
	Server     ServerConfig     `yaml:"server"`
	Memory     MemoryConfig     `yaml:"memory"`
	React      ReactConfig      `yaml:"react"`
	Attachment AttachmentConfig `yaml:"attachment"`
	Schedule   ScheduleConfig   `yaml:"schedule"`
	Message    MessageConfig    `yaml:"message"`
	SubAgent   SubAgentConfig   `yaml:"subagent"`
	Security   SecurityConfig   `yaml:"security"`
	Logging    LoggingConfig    `yaml:"logging"`
}

// legacyEnvFile 描述老 env.yaml 的顶层结构。
type legacyEnvFile struct {
	Database *DatabaseConfig `yaml:"database"`
}

// MigrateLegacy 检测 homeDir 下的老配置文件并执行文件侧迁移。
//
// 返回值语义：
//   - (nil, nil)：无需迁移（bootstrap.yaml 已存在，或没有任何老文件）
//   - (lb, nil)：迁移完成，lb 为待写入配置表的业务项
//
// 迁移只在 bootstrap.yaml 不存在时发生一次，失败时不留下半成品文件。
func MigrateLegacy(homeDir string) (*LegacyBusiness, error) {
	bootstrapPath := filepath.Join(homeDir, BootstrapFileName)
	if _, err := os.Stat(bootstrapPath); err == nil {
		return nil, nil // 已迁移过
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("检查 bootstrap.yaml 失败: %w", err)
	}

	legacyCfg, cfgExists, err := readLegacyConfig(homeDir)
	if err != nil {
		return nil, err
	}
	legacyDB, envExists, err := readLegacyEnv(homeDir)
	if err != nil {
		return nil, err
	}
	if !cfgExists && !envExists {
		return nil, nil // 全新安装，交给 groot init
	}

	b := buildBootstrapFromLegacy(legacyCfg, legacyDB)
	data, err := marshalBootstrapWithHeader(b)
	if err != nil {
		return nil, err
	}
	// 0600：文件含数据库凭据
	if err := os.WriteFile(bootstrapPath, data, 0600); err != nil {
		return nil, fmt.Errorf("写入 bootstrap.yaml 失败: %w", err)
	}

	return &LegacyBusiness{
		Memory:          legacyCfg.Memory,
		React:           legacyCfg.React,
		SubAgent:        legacyCfg.SubAgent,
		Attachment:      legacyCfg.Attachment,
		RateLimit:       legacyCfg.Security.RateLimit,
		ScheduleEnabled: legacyCfg.Schedule.Enabled,
		Auth:            legacyCfg.Security.Auth,
		Senders:         legacyCfg.Message.Senders,
	}, nil
}

// readLegacyConfig 读取老 config.yaml；不存在时返回零值与 false。
func readLegacyConfig(homeDir string) (legacyConfigFile, bool, error) {
	var lc legacyConfigFile
	data, err := os.ReadFile(filepath.Join(homeDir, "config.yaml"))
	if err != nil {
		if os.IsNotExist(err) {
			return lc, false, nil
		}
		return lc, false, fmt.Errorf("读取老 config.yaml 失败: %w", err)
	}
	if err := yaml.Unmarshal(data, &lc); err != nil {
		return lc, false, fmt.Errorf("解析老 config.yaml 失败: %w", err)
	}
	return lc, true, nil
}

// readLegacyEnv 读取老 env.yaml 的 database 节；不存在时返回 nil 与 false。
func readLegacyEnv(homeDir string) (*DatabaseConfig, bool, error) {
	data, err := os.ReadFile(filepath.Join(homeDir, EnvFileName))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("读取老 env.yaml 失败: %w", err)
	}
	var le legacyEnvFile
	if err := yaml.Unmarshal(data, &le); err != nil {
		return nil, false, fmt.Errorf("解析老 env.yaml 失败: %w", err)
	}
	return le.Database, true, nil
}

// buildBootstrapFromLegacy 从老文件的解析结果组装 Bootstrap，只取非零值 ——
// 老模板几乎全注释，解析出的零值写进新文件会变成无效配置（如 port: 0）。
// 序列化时零值字段因 omitempty 被略去，加载时由缺省值兜底。
func buildBootstrapFromLegacy(lc legacyConfigFile, db *DatabaseConfig) *Bootstrap {
	b := &Bootstrap{Database: db}
	b.Agent = lc.Agent
	b.Server = lc.Server
	b.Logging = lc.Logging
	b.Message = MessageBootstrap{QueueSize: lc.Message.QueueSize, Workers: lc.Message.Workers}
	b.Schedule = ScheduleBootstrap{
		MaxConcurrentTasks: lc.Schedule.MaxConcurrentTasks,
		SyncInterval:       lc.Schedule.SyncInterval,
	}
	b.Security = SecurityBootstrap{RateLimit: RateLimitBootstrap{
		CleanupInterval: lc.Security.RateLimit.CleanupInterval,
	}}
	return b
}

// marshalBootstrapWithHeader 序列化 Bootstrap 并加说明性头注释。
func marshalBootstrapWithHeader(b *Bootstrap) ([]byte, error) {
	body, err := yaml.Marshal(b)
	if err != nil {
		return nil, fmt.Errorf("序列化 bootstrap.yaml 失败: %w", err)
	}
	header := `# 本文件由 Groot 从 config.yaml 与 env.yaml 自动迁移生成。
# 老文件已不再被读取，确认本文件内容无误后可自行删除它们。
# 业务配置（模型、限流阈值、通知渠道等）已迁入数据库，请在 Web 设置面板中维护。

`
	return append([]byte(header), body...), nil
}
```

`Bootstrap` 结构体按 marshal 需求补 `omitempty`：Task 2 的结构体定义中，`Agent`、`Server`、`Logging`、`Message`、`Schedule`、`Security` 及其内部各字段的 yaml tag 全部加 `,omitempty`（如 `yaml:"queue_size,omitempty"`），使零值不落盘。`LoggingConfig` 等复用自 `config.go` 的类型也要同步加 `omitempty`（这些 tag 只影响 marshal，不影响既有 unmarshal 路径）。

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./internal/config/ -run 'TestMigrateLegacy|TestLoadBootstrap' -v`
Expected: 全部 PASS（含 Task 2 的用例回归）

- [ ] **Step 5: 提交**

```bash
git add internal/config/
git commit -m "feat(config): 老配置文件到 bootstrap.yaml 的一次性迁移（文件侧）"
```

---

## Task 8: 老配置迁移 —— 配置表侧

**Files:**
- Create: `internal/setting/migrate_legacy.go`
- Test: `internal/setting/migrate_legacy_test.go`

把 Task 7 交出的 `LegacyBusiness` 写入配置表。规则：只写非零值；**目标键在表内已有值时不覆盖**（表值是使用者后来在界面上的改动，比老 YAML 新）。

- [ ] **Step 1: 写失败的测试**

`internal/setting/migrate_legacy_test.go`：

```go
package setting

import (
	"context"
	"testing"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/repo"
)

// TestImportLegacy_WritesNonZero 非零业务项写入表，随后可读回
func TestImportLegacy_WritesNonZero(t *testing.T) {
	s := New(config.Bootstrap{}, newFakeSettingRepo())
	ctx := context.Background()

	lb := &config.LegacyBusiness{
		Memory: config.MemoryConfig{HistoryWindow: 50},
		React:  config.ReactConfig{MaxIterations: 30},
		Auth:   config.AuthConfig{Secret: "deadbeef", HeaderName: "X-Groot-Key"},
		RateLimit: config.RateLimitConfig{
			Enabled:    true,
			DefaultQPS: 25,
		},
		ScheduleEnabled: true,
		Senders: map[string]config.SenderConf{
			"webhook": {Enabled: true, URL: "https://hook.example.com"},
		},
	}
	if err := s.ImportLegacy(ctx, lb); err != nil {
		t.Fatalf("ImportLegacy: %v", err)
	}

	mem, _ := s.Memory(ctx)
	if mem.HistoryWindow != 50 {
		t.Errorf("history_window = %d, want 50", mem.HistoryWindow)
	}
	r, _ := s.React(ctx)
	if r.MaxIterations != 30 {
		t.Errorf("max_iterations = %d, want 30", r.MaxIterations)
	}
	// react 其余字段未设置，保持默认值
	if r.StepTimeout != 60 {
		t.Errorf("step_timeout = %d, want 默认 60", r.StepTimeout)
	}
	a, _ := s.Auth(ctx)
	if a.Secret != "deadbeef" || a.HeaderName != "X-Groot-Key" {
		t.Errorf("auth = %+v", a)
	}
	sec, _ := s.Security(ctx)
	if !sec.RateLimit.Enabled || sec.RateLimit.DefaultQPS != 25 {
		t.Errorf("rate_limit = %+v", sec.RateLimit)
	}
	sch, _ := s.Schedule(ctx)
	if !sch.Enabled {
		t.Error("schedule.enabled 未迁入")
	}
	msg, _ := s.Message(ctx)
	if w := msg.Senders["webhook"]; !w.Enabled || w.URL != "https://hook.example.com" {
		t.Errorf("webhook = %+v", w)
	}
}

// TestImportLegacy_DoesNotOverwrite 表内已有值不被老 YAML 覆盖
func TestImportLegacy_DoesNotOverwrite(t *testing.T) {
	r := newFakeSettingRepo()
	s := New(config.Bootstrap{}, r)
	ctx := context.Background()

	// 使用者已在界面上把窗口改成 100
	if err := r.Upsert(ctx, &repo.Setting{
		Scope: repo.ScopeGlobal, Name: KeyMemoryHistoryWindow, Value: "100",
	}); err != nil {
		t.Fatalf("预置表值: %v", err)
	}

	lb := &config.LegacyBusiness{Memory: config.MemoryConfig{HistoryWindow: 50}}
	if err := s.ImportLegacy(ctx, lb); err != nil {
		t.Fatalf("ImportLegacy: %v", err)
	}

	mem, _ := s.Memory(ctx)
	if mem.HistoryWindow != 100 {
		t.Errorf("表内已有值被覆盖: %d, want 100", mem.HistoryWindow)
	}
}

// TestImportLegacy_SkipsZero 零值不写表（老模板全注释的常态）
func TestImportLegacy_SkipsZero(t *testing.T) {
	r := newFakeSettingRepo()
	s := New(config.Bootstrap{}, r)
	ctx := context.Background()

	if err := s.ImportLegacy(ctx, &config.LegacyBusiness{}); err != nil {
		t.Fatalf("ImportLegacy: %v", err)
	}
	items, err := r.ListByScope(ctx, repo.ScopeGlobal, "")
	if err != nil {
		t.Fatalf("ListByScope: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("零值不应写表, got %d 行: %+v", len(items), items)
	}
}

// TestImportLegacy_Nil nil 输入是显式的空操作
func TestImportLegacy_Nil(t *testing.T) {
	s := New(config.Bootstrap{}, newFakeSettingRepo())
	if err := s.ImportLegacy(context.Background(), nil); err != nil {
		t.Errorf("nil 输入应为空操作, got %v", err)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/setting/ -run TestImportLegacy -v`
Expected: 编译失败，`undefined: (*Settings).ImportLegacy`

- [ ] **Step 3: 写实现**

`internal/setting/migrate_legacy.go`：

```go
// internal/setting/migrate_legacy.go
// 老 config.yaml 业务项到配置表的一次性写入。
package setting

import (
	"context"
	"strconv"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/repo"
)

// ImportLegacy 把老 config.yaml 解析出的业务项写入配置表。
//
// 两条规则：
//   - 只写非零值：老模板几乎全注释，零值代表「使用者没配过」，写进表里
//     会把无效值（如 max_iterations=0）固化成显式设置。
//   - 表内已有的键跳过：表值来自使用者后来在界面上的改动，比老 YAML 新。
//
// 布尔项特殊：false 与零值不可区分，只在 true 时写入。默认值本就是
// false，因此丢失「显式的 false」不改变行为。
func (s *Settings) ImportLegacy(ctx context.Context, lb *config.LegacyBusiness) error {
	if lb == nil {
		return nil
	}
	if s.repo == nil {
		return ErrNoSettingStore
	}
	existing, err := s.globalValues(ctx)
	if err != nil {
		return err
	}

	var rows []*repo.Setting
	add := func(key, value string) {
		if _, ok := existing[key]; ok {
			return
		}
		rows = append(rows, &repo.Setting{Scope: repo.ScopeGlobal, Name: key, Value: value})
	}
	addInt := func(key string, v int) {
		if v != 0 {
			add(key, strconv.Itoa(v))
		}
	}
	addFloat := func(key string, v float64) {
		if v != 0 {
			add(key, strconv.FormatFloat(v, 'f', -1, 64))
		}
	}
	addStr := func(key, v string) {
		if v != "" {
			add(key, v)
		}
	}
	addTrue := func(key string, v bool) {
		if v {
			add(key, "true")
		}
	}

	addInt(KeyMemoryHistoryWindow, lb.Memory.HistoryWindow)

	addInt(KeyReactMaxIterations, lb.React.MaxIterations)
	addInt(KeyReactStepTimeout, lb.React.StepTimeout)
	addInt(KeyReactErrorRetry, lb.React.ErrorRetry)

	addInt(KeySubAgentMaxConcurrency, lb.SubAgent.MaxConcurrency)
	addStr(KeySubAgentExecTimeout, lb.SubAgent.ExecTimeout)
	addInt(KeySubAgentMaxTaskLength, lb.SubAgent.MaxTaskLength)
	addInt(KeySubAgentMaxResultLength, lb.SubAgent.MaxResultLength)

	addInt(KeyAttachmentMaxSize, lb.Attachment.MaxSize)
	addInt(KeyAttachmentMaxTotalSize, lb.Attachment.MaxTotalSize)
	addInt(KeyAttachmentMaxCount, lb.Attachment.MaxCount)
	if len(lb.Attachment.AllowedTypes) > 0 {
		add(KeyAttachmentAllowedTypes, encodeAllowedTypes(lb.Attachment.AllowedTypes))
	}

	addTrue(KeyRateLimitEnabled, lb.RateLimit.Enabled)
	addFloat(KeyRateLimitGlobalQPS, lb.RateLimit.GlobalQPS)
	addInt(KeyRateLimitGlobalConcurrency, lb.RateLimit.GlobalConcurrency)
	addFloat(KeyRateLimitDefaultQPS, lb.RateLimit.DefaultQPS)
	addInt(KeyRateLimitDefaultConcurrency, lb.RateLimit.DefaultConcurrency)

	addTrue(KeyScheduleEnabled, lb.ScheduleEnabled)

	addStr(KeyAuthSecret, lb.Auth.Secret)
	addStr(KeyAuthHeaderName, lb.Auth.HeaderName)

	for name, conf := range lb.Senders {
		if name != "webhook" && name != "email" {
			continue // 未知渠道不迁移
		}
		addTrue(senderKey(name, "enabled"), conf.Enabled)
		addStr(senderKey(name, "url"), conf.URL)
		addStr(senderKey(name, "smtp_host"), conf.SMTPHost)
		addInt(senderKey(name, "smtp_port"), conf.SMTPPort)
		addStr(senderKey(name, "username"), conf.Username)
		addStr(senderKey(name, "password"), conf.Password)
		addStr(senderKey(name, "from"), conf.From)
	}

	if len(rows) == 0 {
		return nil
	}
	return s.repo.Upsert(ctx, rows...)
}
```

`encodeAllowedTypes` 是 `attachment.allowed_types` 在表内的既有编码函数（JSON 数组，见 `runtime.go` 中 `rows()` 对该键的处理）；若实际函数名不同，以 `runtime.go` 里的为准，保持同一键的编码只有一种。`senderKey` 复用 `message.go:35` 的既有函数。

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./internal/setting/ -run TestImportLegacy -v`
Expected: 全部 PASS

- [ ] **Step 5: 提交**

```bash
git add internal/setting/migrate_legacy.go internal/setting/migrate_legacy_test.go
git commit -m "feat(setting): 老配置业务项一次性迁入配置表"
```

---

## Task 9: 组装启动视图 AssembleConfig

**Files:**
- Create: `internal/setting/assemble.go`
- Test: `internal/setting/assemble_test.go`

`api.NewServer` 及各 handler 整体接收 `config.Config`（见 `internal/api/server.go:40`）。保留 `config.Config` 作为「启动时组装的完整视图」，避免重写全部 handler 签名：main.go 在数据库就绪后调用一次 `AssembleConfig`，把 bootstrap 静态项与配置表业务项合成一个 `config.Config` 交给下游。组装是启动时的一次快照 —— 运行时的在线生效仍走各持有对象（限流器 Reconfigure、消息层 SetSender、门控读表），与现状一致。

- [ ] **Step 1: 写失败的测试**

`internal/setting/assemble_test.go`：

```go
package setting

import (
	"context"
	"testing"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/repo"
)

// TestAssembleConfig 静态项来自 bootstrap，业务项来自配置表，
// 表中缺失的业务项为代码默认值。
func TestAssembleConfig(t *testing.T) {
	b := config.Bootstrap{}
	b.Server = config.ServerConfig{Host: "127.0.0.1", Port: 9090}
	b.Message = config.MessageBootstrap{QueueSize: 512, Workers: 4}
	b.Schedule = config.ScheduleBootstrap{MaxConcurrentTasks: 8, SyncInterval: "15s"}
	b.Security.RateLimit.CleanupInterval = "2m"

	r := newFakeSettingRepo()
	s := New(b, r)
	ctx := context.Background()
	if err := r.Upsert(ctx,
		&repo.Setting{Scope: repo.ScopeGlobal, Name: KeyMemoryHistoryWindow, Value: "50"},
		&repo.Setting{Scope: repo.ScopeGlobal, Name: KeyAuthSecret, Value: "deadbeef"},
	); err != nil {
		t.Fatalf("预置表值: %v", err)
	}

	cfg, err := s.AssembleConfig(ctx)
	if err != nil {
		t.Fatalf("AssembleConfig: %v", err)
	}
	// 静态项
	if cfg.Server.Port != 9090 || cfg.Message.QueueSize != 512 || cfg.Message.Workers != 4 {
		t.Errorf("静态项未透传: %+v %+v", cfg.Server, cfg.Message)
	}
	if cfg.Schedule.MaxConcurrentTasks != 8 || cfg.Schedule.SyncInterval != "15s" {
		t.Errorf("schedule 静态项未透传: %+v", cfg.Schedule)
	}
	if cfg.Security.RateLimit.CleanupInterval != "2m" {
		t.Errorf("cleanup_interval = %q", cfg.Security.RateLimit.CleanupInterval)
	}
	// 表项与默认值
	if cfg.Memory.HistoryWindow != 50 {
		t.Errorf("表项未合入: history_window = %d", cfg.Memory.HistoryWindow)
	}
	if cfg.Security.Auth.Secret != "deadbeef" {
		t.Errorf("密钥未合入: %q", cfg.Security.Auth.Secret)
	}
	if cfg.React.MaxIterations != 20 {
		t.Errorf("表中缺失的项应为默认值: %d", cfg.React.MaxIterations)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/setting/ -run TestAssembleConfig -v`
Expected: 编译失败，`undefined: (*Settings).AssembleConfig`

- [ ] **Step 3: 写实现**

`internal/setting/assemble.go`：

```go
// internal/setting/assemble.go
// 启动视图组装：把 bootstrap.yaml 的静态项与配置表的业务项合成一个
// config.Config，供 api.NewServer 等按整体接收配置的构造路径使用。
//
// 这是启动时的一次快照。业务项的在线生效不经过它 —— 限流参数经
// RateLimiter.Reconfigure、发送渠道经 Layer.SetSender、调度开关经门控读表。
package setting

import (
	"context"

	"github.com/zfd81/groot/internal/config"
)

// AssembleConfig 组装完整的 config.Config。
func (s *Settings) AssembleConfig(ctx context.Context) (*config.Config, error) {
	rt, err := s.Runtime(ctx)
	if err != nil {
		return nil, err
	}
	auth, err := s.Auth(ctx)
	if err != nil {
		return nil, err
	}
	msg, err := s.Message(ctx)
	if err != nil {
		return nil, err
	}

	return &config.Config{
		Agent:      s.static.Agent,
		Server:     s.static.Server,
		Logging:    s.static.Logging,
		Database:   s.static.Database,
		Memory:     rt.Memory,
		React:      rt.React,
		SubAgent:   rt.SubAgent,
		Attachment: rt.Attachment,
		Schedule:   rt.Schedule,
		Message:    msg,
		Security: config.SecurityConfig{
			Auth:      auth,
			RateLimit: rt.RateLimit,
		},
	}, nil
}
```

`rt.RateLimit.CleanupInterval` 与 `rt.Schedule` 的两个构造参数已由 Task 6 的 `Runtime` 从 bootstrap 填充，这里无需重复处理。

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./internal/setting/ -race -v`
Expected: 全部 PASS

- [ ] **Step 5: 提交**

```bash
git add internal/setting/assemble.go internal/setting/assemble_test.go
git commit -m "feat(setting): AssembleConfig 组装启动配置视图"
```

---

## Task 10: groot init 产出 bootstrap.yaml

**Files:**
- Modify: `internal/cmd/init.go`
- Test: `internal/cmd/init_test.go`

- [ ] **Step 1: 改既有测试**

`internal/cmd/init_test.go`：

- 断言 `config.yaml` 的用例（约 89-151 行区域）改为断言 `bootstrap.yaml`：存在、权限 0600、内容可被 `config.LoadBootstrap` 加载。init 不再生成密钥，删除对 `security.auth.secret` 非空的断言。
- `TestRunInit_CreatesEnvYaml`（`init_test.go:203`）与 `TestRunInit_PreservesExistingEnvYaml`（`init_test.go:239`）整体删除，替换为：

```go
// TestRunInit_NoLegacyFiles init 不再产出老配置文件
func TestRunInit_NoLegacyFiles(t *testing.T) {
	home := t.TempDir()
	if err := RunInit(home); err != nil {
		t.Fatalf("RunInit: %v", err)
	}
	for _, legacy := range []string{"config.yaml", "env.yaml"} {
		if _, err := os.Stat(filepath.Join(home, legacy)); !os.IsNotExist(err) {
			t.Errorf("init 不应生成 %s", legacy)
		}
	}
}

// TestRunInit_PreservesExistingBootstrap 已存在的 bootstrap.yaml 不被覆盖
func TestRunInit_PreservesExistingBootstrap(t *testing.T) {
	home := t.TempDir()
	custom := "server:\n  port: 9999\n"
	p := filepath.Join(home, config.BootstrapFileName)
	if err := os.WriteFile(p, []byte(custom), 0600); err != nil {
		t.Fatalf("预置文件: %v", err)
	}
	if err := RunInit(home); err != nil {
		t.Fatalf("RunInit: %v", err)
	}
	data, _ := os.ReadFile(p)
	if string(data) != custom {
		t.Errorf("用户自定义 bootstrap.yaml 被覆盖:\n%s", data)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/cmd/ -run TestRunInit -v`
Expected: FAIL（init 仍在生成老文件）

- [ ] **Step 3: 改实现**

`internal/cmd/init.go`：

- `RunInit` 中把 `createConfigFile` 与 `createEnvFile` 两个调用替换为一个 `createBootstrapFile(homeDir)`；
- 删除 `createConfigFile`、`createEnvFile` 两个函数，新增：

```go
// createBootstrapFile 在 homeDir 写入 bootstrap.yaml；已存在则跳过，
// 避免覆盖用户已填好的数据库凭据。
// 0600：文件可能承载数据库凭据。JWT 签名密钥不在此文件中 ——
// 它由服务首次启动时生成并存入数据库配置表。
func createBootstrapFile(homeDir string) error {
	path := filepath.Join(homeDir, config.BootstrapFileName)

	_, err := os.Stat(path)
	if err == nil {
		fmt.Println("配置文件 bootstrap.yaml 已存在，跳过创建")
		return nil
	}
	if !os.IsNotExist(err) {
		return fmt.Errorf("检查配置文件失败: %w", err)
	}

	if err := os.WriteFile(path, []byte(config.GenerateBootstrapTemplate()), 0600); err != nil {
		return fmt.Errorf("创建配置文件失败: %w", err)
	}

	fmt.Println("配置文件 bootstrap.yaml 创建成功")
	return nil
}
```

- `printNextSteps` 改为：

```go
func printNextSteps(homeDir string) {
	shortPath := shortenPath(homeDir, true)
	fmt.Println("下一步：")
	fmt.Println("  1. （可选）调整启动配置：服务端口、日志、数据库连接")
	fmt.Printf("     vim %s/bootstrap.yaml   # 默认全注释 → SQLite 本地模式\n", shortPath)
	fmt.Println("  2. 启动服务")
	fmt.Println("     groot")
	fmt.Println("  3. 打开 Web 界面，在设置面板中配置模型与其他业务项")
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./internal/cmd/ -run TestRunInit -v`
Expected: 全部 PASS

- [ ] **Step 5: 提交**

```bash
git add internal/cmd/init.go internal/cmd/init_test.go
git commit -m "feat(init): groot init 产出单一 bootstrap.yaml"
```

---

## Task 11: main.go 启动装配

**Files:**
- Modify: `cmd/groot/main.go`

启动顺序改为：`LoadBootstrap` → `MigrateLegacy`（文件侧）→ logger → `db.Open` → repos → `setting.New` → `ImportLegacy`（表侧）→ `EnsureAuthSecret` → `AssembleConfig` → 其余组件照旧。本任务无独立单元测试（main 包无测试传统），以 `go build` 与全量测试回归验收。

- [ ] **Step 1: 改 startServer 的加载段**

`cmd/groot/main.go` `startServer` 内，把开头的 `config.Load` / `EnsureAuthSecret` 段（约 202-218 行）替换为：

```go
	// 读取 bootstrap 配置；老部署首次启动时先做文件侧迁移
	legacy, err := config.MigrateLegacy(homeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "迁移老配置文件失败: %s\n", err)
		os.Exit(1)
	}
	boot, err := config.LoadBootstrap(homeDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "无法加载配置: %s\n", err)
		os.Exit(1)
	}

	// Override port if specified
	if port > 0 {
		boot.Server.Port = port
	}

	// Resolve log directory path (before logger initialization)
	boot.Logging.File.Directory = config.ResolvePath(boot.Logging.File.Directory, homeDir)
```

logger 构造与启动日志改用 `boot`：

```go
	log := logger.New(boot.Logging)
	logger.SetDefault(log)
	defer log.Sync()

	log.Info("Groot Agent 启动中...",
		zap.String("home", homeDir),
		zap.String("config", filepath.Join(homeDir, config.BootstrapFileName)),
	)
	if legacy != nil {
		log.Info("已从 config.yaml/env.yaml 迁移到 bootstrap.yaml，老文件保留原地，确认后可删除")
	}
```

- [ ] **Step 2: 改数据库与 settings 装配段**

`db.Open` 改传 `boot.Database`。`setting.New` 之后（原 `settings.Security` 补丁段之前）插入表侧迁移与密钥兜底，并组装 cfg：

```go
	settings := setting.New(*boot, repos.Setting)

	// 老部署首次启动：把老 config.yaml 的业务项写入配置表（只写表内没有的键）
	if legacy != nil {
		if err := settings.ImportLegacy(context.Background(), legacy); err != nil {
			log.Error("迁移业务配置到配置表失败", zap.Error(err))
			os.Exit(1)
		}
		log.Info("业务配置已迁入配置表")
	}

	// JWT 签名密钥存于配置表：缺失时生成（首启或老版本升级），幂等
	if _, err := settings.EnsureAuthSecret(context.Background()); err != nil {
		log.Error("初始化认证密钥失败", zap.Error(err))
		os.Exit(1)
	}

	// 组装完整配置视图：bootstrap 静态项 + 配置表业务项（启动时快照）。
	// 原「启动时把配置表限流值补到 cfg」的补丁由此覆盖，一并删除。
	cfg, err := settings.AssembleConfig(context.Background())
	if err != nil {
		log.Error("组装配置失败", zap.Error(err))
		os.Exit(1)
	}
```

删除原 `if sec, err := settings.Security(...)` 补丁段（`main.go:325-329` 一带）—— `AssembleConfig` 已含表内限流值。`cfg` 之后的使用点（`NewServer`、`BuildSubAgentRegistry`、`cluster.New` 等）不变。

`main.go` 中另一处 `config.Load`（约 154 行，非 server 路径）同样改为 `LoadBootstrap`，`db.Open(boot.Database, homeDir)`。

- [ ] **Step 3: 编译并跑全量测试**

Run: `go build -o dist/groot ./cmd && go test ./... 2>&1 | grep -v "^ok" | head -30`
Expected: 编译通过；此时 `internal/config` 的老测试（loader_test / template_test / env_test / secret_test 中依赖 `Load`/`GenerateConfigTemplate` 的用例）可能仍引用未删的旧代码而通过 —— 旧代码在 Task 14 删除，这里只要求无新增失败。

- [ ] **Step 4: 冒烟验证**

```bash
GROOT_TEST_HOME=$(mktemp -d)
dist/groot init -H "$GROOT_TEST_HOME" 2>/dev/null || GROOT_HOME="$GROOT_TEST_HOME" dist/groot init
ls "$GROOT_TEST_HOME"   # 应见 bootstrap.yaml，无 config.yaml / env.yaml
```

（init 指定 home 的方式以 `cmd/groot/main.go` 实际支持的参数为准；若仅支持默认 `~/.groot`，用一台干净环境或临时改 HOME 验证。）

- [ ] **Step 5: 提交**

```bash
git add cmd/groot/main.go
git commit -m "feat(main): 启动装配改为 bootstrap.yaml + 配置表"
```

---

## Task 12: 不连数据库的 CLI 命令（status / tail）

**Files:**
- Modify: `internal/cmd/status.go:80`
- Modify: `internal/cmd/tail.go:117-137`
- Test: `internal/cmd/status_test.go`、`internal/cmd/tail_test.go`（如存在；无则新建 tail 的读取用例）

这两个命令不连数据库，读 `bootstrap.yaml` 取端口与日志目录。老部署在服务第一次启动前可能只有 `config.yaml`（迁移发生在服务启动时），因此 bootstrap 缺失时回落读老文件，两个命令不报错。

- [ ] **Step 1: 写失败的测试**

在 `internal/cmd/status_test.go` 追加（写文件的辅助方式参照该文件既有用例，如 `status_test.go:360` 直接 `os.WriteFile`）：

```go
// TestStatusPort_FromBootstrap 端口取自 bootstrap.yaml
func TestStatusPort_FromBootstrap(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "bootstrap.yaml"), []byte("server:\n  port: 9191\n"), 0600)

	port, err := resolveStatusPort(home)
	if err != nil {
		t.Fatalf("resolveStatusPort: %v", err)
	}
	if port != 9191 {
		t.Errorf("port = %d, want 9191", port)
	}
}

// TestStatusPort_LegacyFallback bootstrap 缺失时回落老 config.yaml
func TestStatusPort_LegacyFallback(t *testing.T) {
	home := t.TempDir()
	os.WriteFile(filepath.Join(home, "config.yaml"), []byte("server:\n  port: 9292\n"), 0600)

	port, err := resolveStatusPort(home)
	if err != nil {
		t.Fatalf("resolveStatusPort: %v", err)
	}
	if port != 9292 {
		t.Errorf("port = %d, want 9292", port)
	}
}

// TestStatusPort_NoFiles 两个文件都没有时取默认端口
func TestStatusPort_NoFiles(t *testing.T) {
	port, err := resolveStatusPort(t.TempDir())
	if err != nil {
		t.Fatalf("resolveStatusPort: %v", err)
	}
	if port != 8080 {
		t.Errorf("port = %d, want 8080", port)
	}
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/cmd/ -run TestStatusPort -v`
Expected: 编译失败，`undefined: resolveStatusPort`

- [ ] **Step 3: 写实现**

`internal/cmd/status.go` 新增辅助函数，`RunStatus` 中原 `config.Load` 段（`status.go:80` 一带）改为调用它：

```go
// resolveStatusPort 解析服务端口：bootstrap.yaml 优先，缺失时回落老
// config.yaml（服务从未启动过的老部署），两者都没有时用默认端口。
// status 只是探活，不因配置文件缺失而失败。
func resolveStatusPort(homeDir string) (int, error) {
	if b, err := config.LoadBootstrap(homeDir); err == nil {
		return b.Server.Port, nil
	}
	data, err := os.ReadFile(filepath.Join(homeDir, "config.yaml"))
	if err != nil {
		return 8080, nil
	}
	var legacy struct {
		Server struct {
			Port int `yaml:"port"`
		} `yaml:"server"`
	}
	if err := yaml.Unmarshal(data, &legacy); err != nil || legacy.Server.Port == 0 {
		return 8080, nil
	}
	return legacy.Server.Port, nil
}
```

`RunStatus` 内：

```go
	port := flags.Port
	if port == 0 {
		port, _ = resolveStatusPort(homeDir)
	}
```

`internal/cmd/tail.go` 的 `loadConfig`（`tail.go:117-137`）改为先读 `bootstrap.yaml`、缺失时读 `config.yaml`、都缺失时返回默认值（原有的默认值路径保留）：

```go
// loadConfig 读取日志目录配置：bootstrap.yaml 优先，缺失时回落老
// config.yaml，都没有时返回默认值。两个文件的 logging 节结构相同。
func loadConfig(homeDir string) (*LogConfig, error) {
	for _, name := range []string{config.BootstrapFileName, "config.yaml"} {
		data, err := os.ReadFile(filepath.Join(homeDir, name))
		if err != nil {
			if os.IsNotExist(err) {
				continue
			}
			return nil, fmt.Errorf("failed to read config file: %w", err)
		}
		var cfg LogConfig
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return nil, fmt.Errorf("failed to parse config file: %w", err)
		}
		return &cfg, nil
	}
	return getDefaultLogConfig(), nil
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./internal/cmd/ -v`
Expected: 全部 PASS（含既有 status/tail 用例；若某个既有用例以 `config.yaml` 供数据且被 bootstrap 优先级影响，按新优先级修正用例）

- [ ] **Step 5: 提交**

```bash
git add internal/cmd/status.go internal/cmd/tail.go internal/cmd/status_test.go
git commit -m "feat(cli): status/tail 读取 bootstrap.yaml，兼容老文件"
```

---

## Task 13: 同步白名单与 Web 文件面板

**Files:**
- Modify: `internal/sync/resource.go:29-35`
- Modify: `internal/sync/webview.go:10-14`
- Modify: `internal/sync/resolver.go`（注释与默认解析）
- Modify: `internal/webfiles/resolver.go:151-155`
- Modify: `web/src/components/files/FileTree.vue:104`
- Test: `internal/sync/resource_test.go`、`internal/sync/resolver_test.go`、`internal/sync/webview_test.go`、`internal/webfiles/resolver_test.go`、`internal/webfiles/service_test.go`

`config.yaml` 退出同步白名单（配置的集群共享改经数据库配置表），`bootstrap.yaml` 成为 Web 面板的只读文件。老文件 `config.yaml` / `env.yaml` 在面板中保持只读 —— 迁移后它们还留在磁盘上，不能让使用者从面板改动一份已经不生效的配置造成误解，也避免改名绕过。

- [ ] **Step 1: 改测试**

- `internal/sync/resource_test.go:13`：白名单枚举 `{"config.yaml", "skills", ...}` 改为 `{"skills", "subagents", "mcp", "GROOT.md"}`；`resource_test.go:28` 的 `ValidateSyncPath("config.yaml")` 改为断言返回 `ErrInvalidPath`。
- `internal/sync/resolver_test.go`：以 `config.yaml` 为合法路径的用例（39-66 行一带）改用 `GROOT.md`；默认解析用例中删去对 `config.yaml` 的期待。
- `internal/sync/webview_test.go`：`config.yaml` 条目改为 `GROOT.md` 或 `mcp/...` 路径；`needsRestart` 相关断言同步调整（`GROOT.md` 不需重启，`mcp/` 需要）。
- `internal/sync/diff_test.go`、`internal/sync/sync_test.go`、`internal/sync/push_db_test.go`：其中 `config.yaml` 只是作为「白名单内的一个文件资源」使用，一律替换为 `GROOT.md`。
- `internal/webfiles/resolver_test.go:91-97`：只读断言改为 `bootstrap.yaml`、`config.yaml`、`env.yaml` 三者皆只读，`skills/bootstrap.yaml` 与 `GROOT.md` 非只读。
- `internal/webfiles/service_test.go`：`mustWrite("env.yaml", ...)` 一带补 `mustWrite("bootstrap.yaml", "server:\n")`；只读期待（76 行 `wantRO`）改为三个文件名；写/改名/删除 403 的用例对 `bootstrap.yaml` 各加一条。

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/sync/ ./internal/webfiles/ -v 2>&1 | grep -E "FAIL|PASS: 0" | head`
Expected: FAIL

- [ ] **Step 3: 改实现**

`internal/sync/resource.go:27-35`：

```go
// SyncableResourceRoots 是受 sync 管理的根路径白名单，全部为纯资源文件。
// 配置不在此列：bootstrap.yaml 是节点本地的启动配置（含数据库凭据，
// 且各节点可以不同），业务配置经数据库配置表在集群内共享。
var SyncableResourceRoots = []string{
	"skills",
	"subagents",
	"mcp",
	"GROOT.md",
}
```

`internal/sync/webview.go:10-14` 的 `needsRestartPaths` 删去 `"config.yaml"`，保留 `mcp/`、`subagents/`。`internal/sync/resolver.go:14` 一带的注释示例改用 `GROOT.md`。

`internal/webfiles/resolver.go:151-155`：

```go
// ReadOnly 判定 n 是否只读（n 必须是 Normalize/Resolve 的返回值）：
// home 根下的 bootstrap.yaml（当前配置），以及老部署迁移后遗留的
// config.yaml 与 env.yaml —— 遗留文件已不生效，面板上改它只会造成误解。
// 大小写不敏感。
func (r *Resolver) ReadOnly(n string) bool {
	return strings.EqualFold(n, "bootstrap.yaml") ||
		strings.EqualFold(n, "config.yaml") ||
		strings.EqualFold(n, "env.yaml")
}
```

`web/src/components/files/FileTree.vue:104`：

```ts
const SYNCABLE_ROOTS = ['skills', 'subagents', 'mcp', 'GROOT.md']
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./internal/sync/ ./internal/webfiles/ -v`
Expected: 全部 PASS

- [ ] **Step 5: 前端构建验证**

Run: `cd web && npm run build 2>&1 | tail -5`
Expected: 构建成功

- [ ] **Step 6: 提交**

```bash
git add internal/sync/ internal/webfiles/ web/src/components/files/FileTree.vue
git commit -m "feat(sync,webfiles): config.yaml 退出同步白名单，bootstrap.yaml 面板只读"
```

---

## Task 14: 删除老配置加载代码

**Files:**
- Delete: `internal/config/template.go`、`internal/config/template_test.go`
- Delete: `internal/config/env_template.go`
- Modify: `internal/config/loader.go`（删除 `Load` 与 `applyDefaults`）
- Modify: `internal/config/loader_test.go`（删除对应用例）
- Modify: `internal/config/env.go`、`internal/config/env_test.go`
- Modify: `internal/config/secret.go`、`internal/config/secret_test.go`

老代码只在确认无引用后删除。`config.Config` 结构体保留（AssembleConfig 的产物、api 包的入参）。

- [ ] **Step 1: 确认无残留引用**

```bash
grep -rn "config\.Load(\|GenerateConfigTemplate\|GenerateEnvTemplate\|EnsureAuthSecret(homeDir\|config\.EnsureAuthSecret" --include="*.go" . | grep -v _test.go
```

Expected: 无输出（Task 10、11 已改掉全部调用方）。有输出则先改调用方。

- [ ] **Step 2: 删代码**

- `internal/config/template.go`、`template_test.go`、`env_template.go` 整文件删除。
- `internal/config/loader.go`：删除 `Load` 与 `applyDefaults`，只留 `expandConfigEnvVars` 若仍被引用（无引用则一并删，`ResolvePath` 等其他函数如在本文件则保留）。
- `internal/config/env.go`：删除 `envFile` 与 `loadEnvFile`，保留 `EnvFileName` 常量（`MigrateLegacy` 与 webfiles 注释仍引用文件名概念；若 `MigrateLegacy` 用的是字面量则连常量一起删，以实际引用为准）。
- `internal/config/secret.go`：保留 `GenerateAuthSecret`（Task 5 引用），删除 `EnsureAuthSecret`、`writeSecretToFile`、`writeFileAtomic`、`hasTopLevelKey`、`ensureMapChild`、`setMapValue`。
- 对应测试文件删除已删函数的用例，保留 `GenerateAuthSecret` 的用例（如有）。
- `internal/config/config.go:20` 的 `Database` 字段注释改为 `// assembled from bootstrap.yaml`；`config.go:107` Secret 字段注释改为 `// JWT 签名密钥；存于数据库配置表，启动时由 EnsureAuthSecret 兜底生成`。

- [ ] **Step 3: 全量回归**

Run: `go build ./... && go test ./... 2>&1 | grep -v "^ok" | head -30`
Expected: 编译通过，无 FAIL

- [ ] **Step 4: gofmt**

Run: `gofmt -l internal/ cmd/`
Expected: 无输出

- [ ] **Step 5: 提交**

```bash
git add -A internal/config/
git commit -m "refactor(config): 删除 config.yaml/env.yaml 加载与模板代码"
```

---

## Task 15: 文档与系统测试对齐

**Files:**
- Modify: `README.md`
- Modify: `tests/TEST_CASES.md`
- Modify: `tests/python/test_cli_commands.py`
- Modify: `docs/superpowers/specs/2026-09-27-bootstrap-config-design.md`（迭代说明补全）

- [ ] **Step 1: 更新 README.md**

README 是用户手册（项目规范：每次更新使用手册必须改此文件）。逐处更新，`grep -n "config\.yaml\|env\.yaml" README.md` 找全引用点（约 20 处，见 197、215、224、272、278、308、336、394、698、704-757、1218、2330、2371、2382 行一带）：

- 目录结构图：`config.yaml` + `env.yaml` 两行合为 `bootstrap.yaml  # 配置文件（服务、日志、数据库连接等启动配置）`。
- 「配置详解」章：原 config.yaml 各业务节的说明改为指向 Web 设置面板；原 4.7「数据库配置（env.yaml）」改为「数据库配置（bootstrap.yaml）」，示例中文件名替换，凭据与环境变量说明保留。
- 快速开始 / init 输出说明：初始化产出 `bootstrap.yaml`、`GROOT.md` 及资源目录。
- 文件面板说明（336 行一带）：只读文件列表改为 `bootstrap.yaml`（及迁移遗留的 `config.yaml`/`env.yaml`）。
- 新增一小节「从老版本升级」：首次启动自动迁移，老文件保留原地、确认后可删；JWT 密钥迁入数据库，已签发的 API Key 不受影响。
- 环境变量表（2371 行一带）：`GROOT_DB_PASSWORD` 的引用位置说明改为 bootstrap.yaml。
- 遵循记忆中的手册口径：只写对外 API，Web 自用端点不写。

- [ ] **Step 2: 更新 tests/TEST_CASES.md 与 Python 系统测试**

- `tests/TEST_CASES.md:320` 只读规则一行改为「根目录 bootstrap.yaml/config.yaml/env.yaml 写/改名/删除 403」；`TC-CLI-101`（789 行）改为「`groot init` 生成 bootstrap.yaml（权限 0600、全注释模板）、GROOT.md 与 skills/mcp/subagents/logs 目录，不生成 config.yaml/env.yaml」。
- `tests/python/test_cli_commands.py` 的 TC-CLI-101 用例（89-115 行一带）按上述口径改写：断言 `bootstrap.yaml` 存在、0600、不含生效的 `database:` 行；断言 `config.yaml` 与 `env.yaml` 不存在。模块 docstring（8-17 行）同步更新。Python 系统测试由用户运行，此处只改代码不执行。

- [ ] **Step 3: 补全设计文档的迭代说明**

在 `docs/superpowers/specs/2026-09-27-bootstrap-config-design.md` 的「二、迭代说明」中列出实际差异（此时实现已定型）：

```markdown
### 2.1 与上一版差异

- 新增：`bootstrap.yaml` 单一配置文件；`config.LoadBootstrap`、`GenerateBootstrapTemplate`。
- 新增：`security.auth.secret`、`security.auth.header_name` 进配置表；
  `Settings.Auth` / `EnsureAuthSecret` / `SetAuthHeaderName`。
- 新增：`config.MigrateLegacy`（文件侧）与 `Settings.ImportLegacy`（表侧）
  一次性迁移；老文件保留原地。
- 新增：`Settings.AssembleConfig` 组装启动配置视图。
- 移除：`config.yaml` 与 `env.yaml` 的生成与加载（`config.Load`、
  `loadEnvFile`、`GenerateConfigTemplate`、`GenerateEnvTemplate`、
  `config.EnsureAuthSecret`）。
- 移除：`config.yaml` 退出集群同步白名单；`needsRestartPaths` 中对应条目。
- 调整：`Settings` 静态层从 `config.Config` 改为 `config.Bootstrap`；
  配置表分类的基准层从 YAML 值改为代码默认值。
- 调整：`groot init` 产出 `bootstrap.yaml`；`groot status` / `groot tail`
  读取 `bootstrap.yaml` 并向老文件回落。
- 调整：Web 文件面板只读文件为 `bootstrap.yaml` 与迁移遗留的两个老文件。
```

- [ ] **Step 4: 最终全量回归**

Run: `go build -o dist/groot ./cmd && go test ./... 2>&1 | grep -v "^ok" | head -20`
Expected: 编译通过，无 FAIL

- [ ] **Step 5: 提交**

```bash
git add README.md tests/TEST_CASES.md tests/python/test_cli_commands.py docs/superpowers/specs/2026-09-27-bootstrap-config-design.md
git commit -m "docs: 手册与测试用例对齐 bootstrap.yaml"
```

---

## 遗留与不做的事

- **老文件清理**：迁移后 `config.yaml` / `env.yaml` 留在磁盘由使用者自行删除，程序不删用户文件。
- **`security.auth.header_name` 的界面入口**：本次只入表不上面板（改动需重启，不符合设置面板「保存即生效」的语义），需要时另行迭代。
- **多节点老部署的迁移竞态**：多个老节点同时首次启动新版本时，`ImportLegacy` 的「表内已有键跳过」使先到者生效、后到者跳过，密钥同理经 `EnsureAuthSecret` 幂等收敛；极端并发下 `Upsert` 的行级覆盖也只会写入等价数据，不做额外加锁。
- **`agent.name` 等项进表**：Agent 元信息目前无在线改动诉求，留在 bootstrap.yaml。

