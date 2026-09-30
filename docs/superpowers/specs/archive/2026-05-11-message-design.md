# 消息层设计

## 概述

消息层是 Groot 内置的事件通知系统。调用方发布事件并指定发送渠道，消息层异步并发发送到各渠道。不经过 LLM。

**核心设计目标：**
- **显式**：调用方指定渠道，发送意图明确
- **可靠**：全链路日志，生产端和消费端均有记录
- **可控**：有界队列，并发限制，不 OOM
- **解耦**：异步发送，不阻塞调用方

## 整体架构

```
调用方 (Runner / 系统事件)
  │
  │  Layer.Publish(event, ["webhook", "email"])   异步，不阻塞调用方
  │  + 日志：记录事件投递
  │
  ▼
┌──────────────────────────────────────────────────┐
│                 Message Layer                     │
│                                                  │
│  环节1           环节2             环节3          │
│  ┌──────┐    ┌────────┐    ┌──────────────┐    │
│  │异步投放│ → │队列缓冲 │ → │  Worker 处理  │    │
│  └──────┘    └────────┘    │              │    │
│                            │ 过滤未启用渠道  │    │
│                            │ 并发调用Sender │    │
│                            └──────┬───────┘    │
│                                   │            │
│                       ┌───────────┼──────┐     │
│                       ▼           ▼      ▼     │
│                   webhook     email   stdout   │
│                                                  │
│  环节4: 结果收集 → 返回给调用方                      │
│  + 日志：记录每个渠道的发送结果                       │
└──────────────────────────────────────────────────┘
```

4 个环节，渠道由调用方显式传入，Worker 只负责过滤 + 发送。

---

## 四个环节详解

### 环节1：异步投放

**解决的问题**：调用方不应被消息发送阻塞。

**机制**：
- 调用方调用 `Layer.Publish(ctx, event, channels)`，显式指定发送渠道
- Layer 包装为 `sendJob` 投入有界 channel（发送队列）
- `Publish()` 立即返回 `<-chan []SendResult`（future 模式），调用方稍后取结果
- 当 `channels` 为空时，返回已关闭的空 channel，调用方读取不会阻塞

**日志记录（生产端）**：投入队列时打 INFO 日志，记录事件标题和目标渠道。无论后续发送成功与否，生产动作已留痕。

**异常处理**：
- 入队前先显式检查 `ctx.Err()`，已取消则直接返回错误，避免 `select` 在 `ctx.Done` 与 `queue` 同时就绪时随机命中入队
- 队列满 → 返回 `ErrQueueFull`，打 INFO 日志，事件**不投入**队列
- context 取消 → 不投入队列，返回 `ctx.Err()`，打 INFO 日志

实现位于 [internal/message/layer.go](../../../internal/message/layer.go) 的 `Publish` 方法。

### 环节2：队列缓冲

**解决的问题**：削峰填谷。同一时刻多个事件，先入队，Worker 按能力消费。

**机制**：
- 有界 channel，容量由 `queue_size` 配置（默认 256）
- FIFO 顺序
- Worker Pool 从队列取事件

**异常处理**：
- 队列满（环节1已处理） → `ErrQueueFull`
- Worker 全部繁忙 → 事件在队列中等候，不会丢

### 环节3：Worker 处理（过滤渠道 + 并发发送）

**解决的问题**：控制并发度，过滤掉未启用的渠道，并发调用各 Sender。

**Worker 内部两步**：

```
Worker 取到 job
  │
  ├── 过滤未启用渠道：job.channels 中去掉 sender 未注册或 enabled=false 的
  │
  └── 并发发送：每个剩余渠道一个 goroutine 调用 Send()，WaitGroup 等待全部完成
```

**为什么渠道过滤放在 Worker 而不是 Publish**：渠道的启用/禁用状态可能运行时变化，Worker 发送时判断最准确。

**Sender 超时**：`processJob` 统一为本次发送派生一个 10s 超时的 context（`context.WithTimeout(job.ctx, 10*time.Second)`），并将其传给所有 Sender。Sender 实现只需遵守该 context 的取消/超时即可。

**异常处理**：
- 过滤后无可用渠道 → 打 DEBUG 日志，向 `resultCh` 写入空 slice，避免调用方永久阻塞
- Worker panic → recover，打 ERROR 日志，Worker 继续运行
- Sender.Send() 超时 → 该渠道返回 `SendResult{Success: false}`
- Sender.Send() panic → recover 单个 goroutine，该渠道失败，其他不受影响

实现位于 [internal/message/layer.go](../../../internal/message/layer.go) 的 `worker` 与 `processJob` 方法。

### 环节4：结果收集 + 日志记录

**解决的问题**：发送结果可追溯。调用方需要知道每个渠道的状态。

**机制**：
- `Publish()` 返回 `<-chan []SendResult`
- Worker 完成后将结果写入 channel（无可用渠道时写入空 slice，避免调用方阻塞）
- 调用方读取结果，自行决定如何记录或落库

**日志记录（消费端）**：已在环节3的 `processJob` 中处理——发送完成后立即对每条结果打日志。

---

## 日志规范

| 位置 | 场景 | 级别 | 内容 |
|------|------|------|------|
| Publish | 消息投入队列 | INFO | title、channels |
| Publish | 队列满 | INFO | title、queue_size |
| Publish | ctx 取消 | INFO | title |
| Worker | 过滤后无渠道 | DEBUG | title、requested channels |
| Worker | Sender 发送成功 | INFO | channel、title、sent_at |
| Worker | Sender 发送失败 | ERROR | channel、title、reason |
| Worker | Worker panic | ERROR | panic 详情 |
| Worker | Sender panic | ERROR | channel、title、panic 详情 |
| Start | 消息层启动 | INFO | workers、queue_size |
| Stop | 消息层停止 | INFO | - |

---

## 核心接口

定义在 [internal/message/sender.go](../../../internal/message/sender.go)。

### Event

```go
type Event struct {
    Type     string         // 事件类型，如 "schedule.completed"、"system.alert"
    Time     time.Time      // 事件发生时间
    Title    string         // 事件标题
    Content  string         // 事件内容
    Metadata map[string]any // 附加元数据
}
```

### SendResult

```go
type SendResult struct {
    Channel   string    // 渠道名
    Success   bool      // 是否发送成功
    Message   string    // 结果描述
    Timestamp time.Time // 发送时间
}
```

### Sender

```go
type Sender interface {
    Name() string
    Send(ctx context.Context, event Event) SendResult
}
```

### sendJob

```go
// sendJob 是一次发送任务的内部包装
type sendJob struct {
    ctx      context.Context   // 调用方传入的 context，用于超时控制和取消
    event    Event             // 待发送的事件
    channels []string          // 目标渠道列表
    resultCh chan []SendResult // 结果回传 channel（buffer=1，防止 Worker 阻塞）
}
```

### ErrQueueFull

```go
// ErrQueueFull 在发送队列已满时由 Publish 返回
var ErrQueueFull = fmt.Errorf("消息队列已满")
```

### Layer

定义在 [internal/message/layer.go](../../../internal/message/layer.go)。

```go
type Layer struct {
    queue         chan *sendJob                  // 发送队列（有界）
    queueSize     int                            // 队列容量（来自配置）
    senders       map[string]Sender              // 已注册的发送器实例（key=渠道名）
    senderConfigs map[string]config.SenderConf   // 发送器配置（key=渠道名，含 Enabled 等字段）
    workers       int                            // Worker 数量
    stopCh        chan struct{}                  // 停止信号
    wg            sync.WaitGroup                 // 等待 Worker 退出
    log           *logger.Logger                 // 日志器
}

// NewLayer 根据 MessageConfig 构造一个新的 Layer
func NewLayer(cfg config.MessageConfig, log *logger.Logger) *Layer

// Publish 发布事件并指定渠道，异步发送。返回 future channel 获取结果。
// 调用方从返回的 channel 读取 []SendResult（或直接忽略，channel buffer=1 保证不阻塞 Worker）。
func (l *Layer) Publish(ctx context.Context, event Event, channels []string) (<-chan []SendResult, error)

// Register 注册发送器实例及其配置（初始化时调用，name 为渠道名如 "webhook"）
func (l *Layer) Register(name string, sender Sender, cfg config.SenderConf)

// isSenderEnabled 检查渠道是否可用：已注册 且 配置 Enabled=true
func (l *Layer) isSenderEnabled(name string) bool

// Start 启动 Worker 协程池（workers 个 goroutine），开始消费队列
// Stop 关闭 stopCh → 等待 Worker 退出 → 队列剩余消息直接丢弃
func (l *Layer) Start()
func (l *Layer) Stop()
```

`config.SenderConf` 与 `config.MessageConfig` 的定义见 [internal/config/config.go](../../../internal/config/config.go)。Layer 直接复用 `config.SenderConf` 作为渠道配置类型，所有字段集中在该结构中：

```go
type MessageConfig struct {
    QueueSize int                   `yaml:"queue_size"`
    Workers   int                   `yaml:"workers"`
    Senders   map[string]SenderConf `yaml:"senders"`
}

type SenderConf struct {
    Enabled  bool   `yaml:"enabled"`
    URL      string `yaml:"url,omitempty"`
    SMTPHost string `yaml:"smtp_host,omitempty"`
    SMTPPort int    `yaml:"smtp_port,omitempty"`
    Username string `yaml:"username,omitempty"`
    Password string `yaml:"password,omitempty"`
    From     string `yaml:"from,omitempty"`
}
```

### 渠道名到 Sender 的注册映射

配置中 `message.senders` 的 key 就是渠道名。Groot 启动时（见 [cmd/groot/main.go](../../../cmd/groot/main.go) 的 message layer 初始化段）按以下流程注册：

```
1. 调用 message.NewLayer(cfg.Message, log) 构造 Layer
2. 当 cfg.Message.Senders["webhook"].Enabled 为 true：
     msgLayer.Register("webhook", senders.NewWebhook(url), cfg)
3. 当 cfg.Message.Senders["email"].Enabled 为 true：
     msgLayer.Register("email", senders.NewEmail(host, port, user, pass, from), cfg)
4. 始终注册 stdout：
     msgLayer.Register("stdout", senders.NewStdout(), config.SenderConf{Enabled: true})
5. msgLayer.Start()
```

`stdout` 渠道在配置文件中无需声明，由代码硬编码 `Enabled: true` 注册，便于开发调试。

调用方 `Publish(ctx, event, ["webhook", "email"])` 时，渠道名直接匹配 `senders` map 中的 key。

### 生命周期

**Start（启动）：**

```
1. 启动 workers 个 goroutine，每个运行 worker() 循环；wg.Add(1) 跟踪每个 worker
2. worker() 从 l.queue 读取 job，调用 processJob() 处理
3. 队列空时 worker 阻塞在 select 等待
4. 启动后打 INFO 日志，包含 workers 与 queue_size
```

**Stop（停止）：**

```
1. close(l.stopCh)  →  所有 worker 的 select 收到 stopCh 信号，从循环中返回（defer wg.Done()）
2. l.wg.Wait()  →  等待所有 worker goroutine 退出
3. 队列中剩余的 job 不再处理，直接丢弃（chan 中的元素随 Layer 一起被回收）
4. 打 INFO 日志：消息层已停止
```

> **设计决策：** Stop 时丢弃队列中未处理的消息，不做排空等待。原因：(1) 调用方已有超时机制，消息已过时；(2) 排空等待可能无限阻塞（如果 Sender 慢）；(3) 简化退出逻辑。

---

## 内置 Sender

实现位于 [internal/message/senders/](../../../internal/message/senders/)。

| Sender | 渠道名 | 说明 | 关键配置字段 |
|--------|------|------|---------|
| [`WebhookSender`](../../../internal/message/senders/webhook.go) | `webhook` | HTTP POST JSON，事件序列化为 JSON body | `url` |
| [`EmailSender`](../../../internal/message/senders/email.go) | `email` | SMTP 发送邮件，主题为 `[Groot] <Title>`，正文为 `Content` | `smtp_host`, `smtp_port`, `username`, `password`, `from` |
| [`StdoutSender`](../../../internal/message/senders/stdout.go) | `stdout` | 控制台输出 `[消息] <RFC3339时间> \| <Title>\n  <Content>`，开发调试用 | 无 |

**注意点**：

- `WebhookSender` 内置 `http.Client{Timeout: 10s}`，并在请求上挂载 `processJob` 派生的 ctx，HTTP 状态码在 200~299 视为成功
- `EmailSender` 当前实现以 `from` 作为收件人（`smtp.SendMail(addr, auth, s.from, []string{s.from}, msg)`），并通过 goroutine + select 监听 ctx 取消
- `StdoutSender` 始终返回 `Success: true`

超时控制由 `processJob` 统一创建带超时的 context（默认 10s）传给各 Sender，Sender 实现遵守该 context 的取消即可。

---

## 配置

`message` 段对应 [internal/config/config.go](../../../internal/config/config.go) 的 `MessageConfig`，默认值见 [internal/config/defaults.go](../../../internal/config/defaults.go)：

```yaml
message:
  queue_size: 256           # 发送队列容量，默认 256
  workers: 2                # 发送工作协程数，默认 2
  senders:
    webhook:
      enabled: false
      url: ""
    email:
      enabled: false
      smtp_host: ""
      smtp_port: 587
      username: ""
      password: ""
      from: ""
```

`stdout` 渠道由代码硬编码注册（`Enabled: true`），不在配置文件中声明。

---

## 与定时任务的集成

定时任务的 `Task` 结构（[internal/schedule/types.go](../../../internal/schedule/types.go)）持有 `Notification` 字段，按执行状态选择渠道列表：

```json
{
  "notification": {
    "on_success": ["webhook"],
    "on_failure": ["webhook", "email"]
  }
}
```

[internal/schedule/runner.go](../../../internal/schedule/runner.go) 中的 `sendNotifications` 在任务执行结束后被调用：

```go
func (r *Runner) sendNotifications(task *Task, status string, result string) {
    var channels []string
    if status == "completed" {
        channels = task.Notification.OnSuccess
    } else {
        channels = task.Notification.OnFailure
    }

    if len(channels) == 0 {
        return
    }

    eventType := fmt.Sprintf("schedule.%s", status)
    resultCh, err := r.msgLayer.Publish(context.Background(), message.Event{
        Type:    eventType,
        Time:    time.Now(),
        Title:   task.Name,
        Content: result,
        Metadata: map[string]any{
            "task_id": task.ID,
        },
    }, channels)

    if err != nil {
        r.log.Error("消息发布失败", zap.String("task_id", task.ID), zap.Error(err))
        return
    }

    go func() {
        results := <-resultCh
        for _, res := range results {
            if res.Success {
                r.log.Info("通知发送成功", ...)
            } else {
                r.log.Error("通知发送失败", ...)
            }
        }
    }()
}
```

调用方读取 `resultCh` 后只做日志记录，不写回执行记录文件。

---

## 异常处理汇总

| 环节 | 异常 | 处理 |
|------|------|------|
| 异步投放 | 队列满 | 返回 `ErrQueueFull`，打 INFO 日志 |
| 异步投放 | ctx 取消 | 返回 `ctx.Err()`，打 INFO 日志 |
| 异步投放 | channels 为空 | 返回已关闭的空 channel，调用方读取不阻塞 |
| 队列缓冲 | - | 有界 channel，天然背压 |
| Worker 处理 | Worker panic | recover，打 ERROR 日志，Worker 继续 |
| Worker 处理 | 过滤后无可用渠道 | 打 DEBUG 日志，写入空结果到 resultCh（避免调用方阻塞） |
| Worker 处理 | 单个 Sender panic | recover，打 ERROR 日志，该渠道失败，其他继续 |
| Worker 处理 | Sender 超时（10s） | ctx 超时，Sender 返回失败，打 ERROR 日志 |
| 结果收集 | 调用方不关心结果 | resultCh buffer=1 保证 Worker 不阻塞；调用方 goroutine 泄露风险由调用方负责（不读 channel 则结果残留） |
| 生命周期 | Stop 时队列有残留 | 丢弃未处理消息，Worker 直接退出（不排空等待） |

---

## 目录结构

```
internal/message/
├── layer.go              # Layer 实现（NewLayer / Publish / Register / Start / Stop / worker / processJob）
├── sender.go             # Event / SendResult / Sender / sendJob / ErrQueueFull
├── senders/
│   ├── webhook.go        # WebhookSender
│   ├── email.go          # EmailSender
│   └── stdout.go         # StdoutSender
└── layer_test.go         # 单元测试
```

---

## 扩展指南

新增渠道：

1. 在 [internal/message/senders/](../../../internal/message/senders/) 下实现 `Sender` 接口
2. 在 [cmd/groot/main.go](../../../cmd/groot/main.go) 的消息层初始化段调用 `msgLayer.Register(name, sender, cfg)`
3. 在 `config.yaml` 的 `message.senders` 中添加配置段，必要时为新字段扩展 [`config.SenderConf`](../../../internal/config/config.go)

---

## 测试

[internal/message/layer_test.go](../../../internal/message/layer_test.go) 覆盖：

- 队列满、ctx 取消、channels 为空场景
- Worker panic / Sender panic 时的 recover 行为
- 渠道过滤（未注册、enabled=false）
- 并发发送多个 Sender 的结果聚合
- mock Sender 验证 Publish → Worker → 结果收集 全链路
