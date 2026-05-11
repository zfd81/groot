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
- Layer 包装为 job 投入 Go channel（发送队列）
- `Publish()` 立即返回 `<-chan []SendResult`（future 模式），调用方稍后取结果

**日志记录（生产端）**：投入队列时打 INFO 日志，记录事件内容和目标渠道。无论后续发送成功与否，生产动作已留痕。

**异常处理**：
- 队列满 → 返回 `ErrQueueFull`，打 WARN 日志，事件**不投入**队列
- context 取消 → 不投入队列，返回 `ctx.Err()`

```go
func (l *Layer) Publish(ctx context.Context, event Event, channels []string) (<-chan []SendResult, error) {
    if len(channels) == 0 {
        // 返回已关闭的 channel，调用方读取不会阻塞
        ch := make(chan []SendResult)
        close(ch)
        return ch, nil
    }

    job := &sendJob{
        ctx:      ctx,
        event:    event,
        channels: channels,
        resultCh: make(chan []SendResult, 1),
    }

    select {
    case l.queue <- job:
        logger.Info("消息入队",
            zap.String("title", event.Title),
            zap.Strings("channels", channels),
        )
        return job.resultCh, nil
    case <-ctx.Done():
        logger.Warn("消息入队失败: context已取消", zap.String("title", event.Title))
        return nil, ctx.Err()
    default:
        logger.Warn("消息入队失败: 队列已满",
            zap.String("title", event.Title),
            zap.Int("queue_size", l.queueSize),
        )
        return nil, ErrQueueFull
    }
}
```

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
  ├── 过滤未启用渠道：job.channels 中去掉 sender 未配置或 enabled=false 的
  │
  └── 并发发送：每个剩余渠道一个 goroutine 调用 Send()，WaitGroup 等待全部完成
```

**为什么渠道过滤放在 Worker 而不是 Publish**：渠道的启用/禁用状态可能运行时变化，Worker 发送时判断最准确。

**异常处理**：
- 过滤后无可用渠道 → 返回空结果
- Worker panic → recover，打 ERROR 日志，Worker 继续运行
- Sender.Send() 超时 → 该渠道返回 `SendResult{Success: false}`
- Sender.Send() panic → recover 单个 goroutine，该渠道失败，其他不受影响

```go
func (l *Layer) worker() {
    for {
        select {
        case job := <-l.queue:
            l.processJob(job)
        case <-l.stopCh:
            return
        }
    }
}

func (l *Layer) processJob(job *sendJob) {
    defer func() {
        if r := recover(); r != nil {
            logger.Error("Worker panic", zap.Any("panic", r))
        }
    }()

    // 过滤启用的渠道：sender 已注册 且 配置中 enabled=true
    var enabledChannels []string
    for _, name := range job.channels {
        if l.isSenderEnabled(name) {
            enabledChannels = append(enabledChannels, name)
        }
    }

    if len(enabledChannels) == 0 {
        logger.Debug("无可用渠道，跳过发送",
            zap.String("title", job.event.Title),
            zap.Strings("requested", job.channels),
        )
        // 仍需写入结果，避免调用方永久阻塞
        job.resultCh <- []SendResult{}
        return
    }

    // 创建带超时的 context（每个 Sender 默认 10s）
    ctx, cancel := context.WithTimeout(job.ctx, 10*time.Second)
    defer cancel()

    // 并发发送
    results := make([]SendResult, len(enabledChannels))
    var wg sync.WaitGroup
    for i, name := range enabledChannels {
        wg.Add(1)
        go func(idx int, channelName string) {
            defer wg.Done()
            defer func() {
                if r := recover(); r != nil {
                    results[idx] = SendResult{
                        Channel: channelName,
                        Success: false,
                        Message: fmt.Sprintf("panic: %v", r),
                    }
                    logger.Error("Sender panic",
                        zap.String("channel", channelName),
                        zap.String("title", job.event.Title),
                        zap.Any("panic", r),
                    )
                }
            }()
            results[idx] = l.senders[channelName].Send(ctx, job.event)
        }(i, name)
    }
    wg.Wait()

    // 记录发送结果日志
    for _, r := range results {
        if r.Success {
            logger.Info("消息发送成功",
                zap.String("channel", r.Channel),
                zap.String("title", job.event.Title),
                zap.Time("sent_at", r.Timestamp),
            )
        } else {
            logger.Error("消息发送失败",
                zap.String("channel", r.Channel),
                zap.String("title", job.event.Title),
                zap.String("reason", r.Message),
            )
        }
    }

    job.resultCh <- results
}
```

### 环节4：结果收集 + 日志记录

**解决的问题**：发送结果可追溯。调用方需要知道每个渠道的状态。

**机制**：
- `Publish()` 返回 `<-chan []SendResult`
- Worker 完成后将结果写入 channel（无可用渠道时写入空 slice，避免调用方阻塞）
- 调用方读取结果，写入执行记录的 `notifications` 字段

**日志记录（消费端）**：已在环节3的 `processJob` 中处理——发送完成后立即对每条结果打日志。

---

## 日志规范

| 位置 | 场景 | 级别 | 内容 |
|------|------|------|------|
| Publish | 消息投入队列 | INFO | title、channels |
| Publish | 队列满 | WARN | title、queue_size |
| Publish | ctx 取消 | WARN | title |
| Worker | 过滤后无渠道 | DEBUG | title、requested channels |
| Worker | Sender 发送成功 | INFO | channel、title、sent_at |
| Worker | Sender 发送失败 | ERROR | channel、title、reason |
| Worker | Worker panic | ERROR | panic 详情 |
| Worker | Sender panic | ERROR | channel、title、panic 详情 |

---

## 核心接口

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
    ctx      context.Context // 调用方传入的 context，用于超时控制和取消
    event    Event           // 待发送的事件
    channels []string        // 目标渠道列表
    resultCh chan []SendResult // 结果回传 channel（buffer=1，防止 Worker 阻塞）
}
```

### Layer

```go
type Layer struct {
    queue         chan *sendJob          // 发送队列（有界）
    queueSize     int                    // 队列容量（来自配置）
    senders       map[string]Sender      // 已注册的发送器实例（key=渠道名）
    senderConfigs map[string]SenderConfig // 发送器配置（key=渠道名，含 enabled 字段）
    workers       int                    // Worker 数量
    stopCh        chan struct{}          // 停止信号
    wg            sync.WaitGroup         // 等待 Worker 退出
}

// SenderConfig 单个发送器的配置
type SenderConfig struct {
    Enabled bool // 是否启用
}

// Publish 发布事件并指定渠道，异步发送。返回 future channel 获取结果。
// 调用方从返回的 channel 读取 []SendResult（或直接忽略，channel buffer=1 保证不阻塞 Worker）。
func (l *Layer) Publish(ctx context.Context, event Event, channels []string) (<-chan []SendResult, error)

// Register 注册发送器实例（初始化时调用，key 为渠道名如 "webhook"）
func (l *Layer) Register(name string, sender Sender, cfg SenderConfig)

// isSenderEnabled 检查渠道是否可用：已注册 且 配置 enabled=true
func (l *Layer) isSenderEnabled(name string) bool {
    cfg, ok := l.senderConfigs[name]
    if !ok {
        return false
    }
    if !cfg.Enabled {
        return false
    }
    _, registered := l.senders[name]
    return registered
}

// Start 启动 Worker 协程池（workers 个 goroutine），开始消费队列
// Stop 优雅停止：关闭 stopCh → 等待队列清空 → Worker 退出 → wg.Wait()
func (l *Layer) Start()
func (l *Layer) Stop()
```

### 渠道名到 Sender 配置的映射

配置中 `message.senders` 的 key 就是渠道名。初始化流程：

```
1. 读取 config.yaml 中 message.senders 的每个 key（如 webhook、email）
2. 为每个 key 创建对应的 Sender 实例
3. 调用 Layer.Register(key, sender, SenderConfig{Enabled: cfg.Enabled})
   - key = 配置中的渠道名（如 "webhook"）
   - sender = 对应实现（WebhookSender / EmailSender / StdoutSender）
   - cfg.Enabled = 配置中的 enabled 字段
4. Register 内部将 sender 存入 l.senders[key]，配置存入 l.senderConfigs[key]
```

调用方 `Publish(ctx, event, ["webhook", "email"])` 时，渠道名直接匹配配置中的 key。

### 生命周期

**Start（启动）：**

```
1. 启动 workers 个 goroutine，每个运行 worker() 循环
2. worker() 从 l.queue 读取 job，调用 processJob() 处理
3. 队列空时 worker 阻塞等待
```

**Stop（优雅停止）：**

```
1. close(l.stopCh)  →  所有 worker 的 select 收到 stopCh 信号，退出循环
2. l.wg.Wait()  →  等待所有 worker goroutine 完成
3. 此时队列中可能还有未处理的 job → 不处理，直接丢弃
   （定时任务场景下，消息丢失可接受；如果调用方已超时，重发也无意义）
```

> **设计决策：** Stop 时丢弃队列中未处理的消息，不做排空等待。原因：(1) 调用方已有超时机制，消息已过时；(2) 排空等待可能无限阻塞（如果 Sender 慢）；(3) 简化退出逻辑。

---

## 内置 Sender

| Sender | 说明 | 关键配置 |
|--------|------|---------|
| `webhook` | 通用 HTTP POST JSON | `url` |
| `email` | SMTP 发送邮件 | `smtp_host`, `smtp_port`, `from`, `to` |
| `stdout` | 控制台输出 | 无，开发调试用 |

超时控制由 `processJob` 统一创建带超时的 context（默认 10s）传给各 Sender，Sender 实现只需遵守 context 取消即可。

---

## 配置

```yaml
message:
  queue_size: 256           # 发送队列容量
  workers: 2                # 发送工作协程数
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

---

## 与定时任务的集成

task.json 中保留通知配置，Runner 执行完后根据配置构造 channels 列表：

```json
{
  "notification": {
    "on_success": ["webhook"],
    "on_failure": ["webhook", "email"]
  }
}
```

```go
func (r *Runner) run(task Task) {
    // ... Agent 执行 ...

    // 根据执行状态选择渠道
    var channels []string
    if status == "completed" {
        channels = task.Notification.OnSuccess
    } else {
        channels = task.Notification.OnFailure
    }

    if len(channels) == 0 {
        return  // 未配置通知，跳过
    }

    // 发布事件，显式传入渠道
    resultCh, err := r.msgLayer.Publish(ctx, Event{
        Type:     fmt.Sprintf("schedule.%s", status),
        Time:     time.Now(),
        Title:    task.Name,
        Content:  agentOutput,
        Metadata: map[string]any{
            "task_id":     task.ID,
            "duration_ms": duration,
            "step_count":  steps,
        },
    }, channels)
    if err != nil {
        logger.Error("消息发布失败", zap.Error(err))
        return
    }

    // 异步等结果，写入执行记录
    go func() {
        results := <-resultCh
        execRecord.Notifications = results
        r.saveExecution(execRecord)
    }()
}
```

---

## 异常处理汇总

| 环节 | 异常 | 处理 |
|------|------|------|
| 异步投放 | 队列满 | 返回 `ErrQueueFull`，打 WARN 日志 |
| 异步投放 | ctx 取消 | 返回 `ctx.Err()`，打 WARN 日志 |
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
├── layer.go              # Layer 实现
├── sender.go             # Sender / Event / SendResult 类型定义
├── senders/
│   ├── webhook.go        # Webhook 发送器
│   ├── email.go          # 邮件发送器
│   └── stdout.go         # 控制台输出
└── layer_test.go         # 单元测试
```

---

## 扩展指南

新增渠道：

1. 实现 `Sender` 接口
2. 在 Layer 初始化时 `Register`
3. 在 config.yaml `message.senders` 中添加配置段

---

## 测试

- `layer_test.go` — 队列满/Worker panic/渠道过滤/并发发送/mock Sender
- `layer_test.go` — 日志输出验证（生产端和消费端均有日志）
