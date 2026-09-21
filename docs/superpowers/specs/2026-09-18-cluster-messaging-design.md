# 集群消息指令系统设计

## 一、功能概述

集群消息指令系统用于 Groot 多实例间的协调通信，支持广播和点对点两种消息模式。实例通过数据库表发送和接收消息，轮询机制与现有心跳循环整合，实现秒级响应的实例间指令传递。

### 适用范围

**仅服务于集群实例间通信**，单实例内部模块通信使用 Go 的 channel 或直接函数调用。

### 消息模式

- **广播消息**：所有实例都接收并处理（如资源同步、配置重载、重启控制）
- **点对点消息**：指定实例处理特定任务

### 可靠性保证

**At-least-once（至少一次）**：消息保证送达但可能重复，业务处理需实现幂等性。

### 典型场景

- 资源同步：通知所有实例同步配置、刷新缓存
- 重启控制：管理员触发所有实例或指定实例重启
- 配置重载：通知所有实例重新加载配置文件
- 任务分配：指定某个实例执行特定任务

## 二、数据库表结构

### 2.1 消息主表

```sql
CREATE TABLE IF NOT EXISTS cluster_messages (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    message_type    TEXT    NOT NULL,                 -- 消息类型，如 'sync_resource', 'restart', 'reload_config'
    payload         TEXT    NOT NULL,                 -- 消息数据（JSON 文本）
    target_instance TEXT    NOT NULL DEFAULT '',      -- '' = 广播，否则为目标实例 ID
    target_module   TEXT    NOT NULL DEFAULT '',      -- 目标模块名（用于路由到具体处理器）
    priority        INTEGER NOT NULL DEFAULT 5,       -- 1 = 最高优先级，10 = 最低优先级
    created_at      INTEGER NOT NULL,                 -- 创建时间（Unix 毫秒）
    expires_at      INTEGER NOT NULL,                 -- 过期时间（Unix 毫秒，防止无限堆积）
    source_instance TEXT    NOT NULL DEFAULT ''       -- 发送方实例 ID
);
CREATE INDEX IF NOT EXISTS idx_cm_target_expires ON cluster_messages(target_instance, expires_at);
CREATE INDEX IF NOT EXISTS idx_cm_priority_created ON cluster_messages(priority, created_at);
```

**字段说明：**

- `message_type`：消息类型标识符，用于区分不同的业务场景
- `payload`：消息携带的数据，JSON 格式，具体内容由业务场景决定
- `target_instance`：
  - 空串 `''` 表示广播消息，所有实例都会处理
  - 非空表示点对点消息，只有指定实例处理
- `target_module`：可选字段，用于将消息路由到实例内的具体模块处理器
- `priority`：优先级，数字越小越优先，用于紧急消息优先处理
- `expires_at`：强制过期时间，防止消息无限堆积，创建时必须指定

### 2.2 消费记录表

```sql
CREATE TABLE IF NOT EXISTS cluster_message_consumers (
    message_id    INTEGER NOT NULL,
    instance_id   TEXT    NOT NULL,
    consumed_at   INTEGER NOT NULL,                   -- 消费时间（Unix 毫秒）
    status        TEXT    NOT NULL,                   -- 'success' 或 'failed'
    error_message TEXT    NOT NULL DEFAULT '',        -- 失败时的错误信息
    PRIMARY KEY (message_id, instance_id)
);
CREATE INDEX IF NOT EXISTS idx_cmc_instance_consumed ON cluster_message_consumers(instance_id, consumed_at);
```

**设计要点：**

- 每个实例处理消息后插入一条消费记录
- 广播消息：N 个实例会产生 N 条消费记录
- 点对点消息：只有目标实例产生 1 条消费记录
- 记录处理状态和错误信息，便于监控和排查问题

## 三、核心流程

### 3.1 消息发送

内部 API，只在代码内部调用，不对外暴露：

```go
func (s *MessageService) SendMessage(ctx context.Context, msg Message) error
```

**校验与默认值：**

- `Type`、`TargetModule` 为必填，缺失时返回错误
- `Priority` 为 0 时取默认值 5
- `ExpiresAt` 为零值时取默认值当前时间 + 1 小时；非零值必须晚于当前时间
- `Payload` 序列化为 JSON 后不得超过 64KB
- 发送前自动填充 `CreatedAt`（当前时间）和 `SourceInstance`（本实例 ID）

**写入流程：**

- 通过 `repo.MessageRepo.Insert` 写入 `cluster_messages`
- 写入成功后记录 "集群消息已发送" 日志（字段：type / target / module / priority）

**失败处理（发送阶段）：**

- 数据库写入失败时立即重试 1 次
- 第二次仍失败则返回错误给调用方
- 调用方负责处理错误（如记录日志、返回给前端、告警等）

### 3.2 消息轮询（整合到心跳）

轮询由 `Cluster.run()` 的 3 秒心跳 ticker 驱动，每个 tick 先执行 `heartbeat()`（持有写锁），写锁释放后再调用 `triggerPoll()`：

```go
// Cluster.run() 中的 ticker 分支
case <-ticker.C:
    c.heartbeat()    // 更新注册、Leader 清理过时成员（持有写锁）
    c.triggerPoll()  // 写锁已释放，异步触发一轮消息轮询
```

`triggerPoll()` 的机制：

- 使用原子 CAS 标志 `polling` 保证同一时刻只有一轮轮询在运行
- CAS 成功后在独立 goroutine 中执行 `MessageService.Poll(c.ctx)`，结束时复位标志
- 上一轮轮询尚未结束时，本轮直接跳过，等待下一个 tick
- 未设置 `MessageService` 时不做任何操作

**设计要点：**

- 与心跳循环整合，复用 3 秒定时器
- 减少数据库并发访问（心跳、消息轮询由同一个 ticker 驱动）
- 轮询间隔 = 3 秒，满足"秒级响应"需求
- 处理器不在心跳写锁内执行，耗时处理器不会阻塞心跳

### 3.3 消息查询

每个实例轮询时执行以下 SQL：

```sql
SELECT m.* 
FROM cluster_messages m
WHERE (m.target_instance = '' OR m.target_instance = ?)  -- 广播或点对点
  AND m.expires_at > ?                                      -- 当前时间（Unix 毫秒），过滤已过期消息
  AND NOT EXISTS (
      SELECT 1 FROM cluster_message_consumers c
      WHERE c.message_id = m.id AND c.instance_id = ?       -- 自己未消费过
  )
ORDER BY m.priority ASC, m.created_at ASC, m.id ASC         -- 优先级优先，先进先出
LIMIT ?                                                      -- 每次最多 10 条
```

**查询逻辑：**

1. 筛选目标为自己的消息（广播或点对点）
2. 排除已过期的消息
3. 排除自己已消费过的消息（通过 `NOT EXISTS` 子查询）
4. 按优先级和创建时间排序
5. 限制单次处理数量，避免阻塞心跳

### 3.4 消息处理

```go
// Poll 执行一轮轮询：取本实例待处理消息并逐条处理
func (s *MessageService) Poll(ctx context.Context)

// processMessage 处理单条消息并写入消费记录
func (s *MessageService) processMessage(ctx context.Context, instanceID string, rm *repo.ClusterMessage)
```

**轮询流程（`Poll`）：**

- 通过 `selfID()` 获取本实例 ID，为空则直接返回
- 调用 `repo.ListPending(ctx, instanceID, now, 10)` 查询待处理消息（SQL 见 3.3 节）
- 逐条调用 `processMessage`

**单条处理流程（`processMessage`）：**

1. 将 `payload` JSON 文本解析为 `map[string]any`，解析失败记录 `failed`
2. 按 `target_module` 查找已注册的处理器，未注册记录 `failed`
3. 在 `context.WithTimeout(ctx, 30s)` 下调用处理器，处理器返回错误记录 `failed`
4. 处理器 panic 由 `safeHandle` 捕获并转换为错误，记录 `failed`
5. 处理器正常返回记录 `success`
6. 通过 `recordResult` 向 `cluster_message_consumers` 写入一条记录（`status` + `error_message`），并输出 "集群消息处理成功" / "集群消息处理失败" 日志（字段：msg_id / type / module）

**失败处理（处理阶段）：**

- payload 解析失败、未注册模块、处理器返回错误、处理器 panic 四种情况均记录 `status='failed'`，不重试
- 单条处理器超时 30 秒
- 直接记录 `status='failed'` 到 `cluster_message_consumers` 表
- 记录错误日志，便于排查
- 继续处理下一条消息，不阻塞心跳循环
- 消费记录写入失败时只记录日志，消息会在下一轮重新投递（至少一次语义）

## 四、模块扩展机制

### 4.1 处理器接口

```go
// MessageHandler 是消息处理器的抽象接口
type MessageHandler interface {
    // Handle 处理消息，返回错误表示处理失败
    Handle(ctx context.Context, msg Message) error
}
```

### 4.2 消息服务

```go
// MessageService 管理消息的发送、轮询处理、处理器注册和清理
type MessageService struct {
    repo     repo.MessageRepo           // 仓储接口（cluster_messages / cluster_message_consumers）
    log      *logger.Logger
    selfID   func() string              // 返回本实例 ID，由 Cluster 提供
    mu       sync.RWMutex               // 保护 handlers
    handlers map[string]MessageHandler  // 模块名 -> 处理器
}

// NewMessageService 构造消息服务
func NewMessageService(msgRepo repo.MessageRepo, log *logger.Logger, selfID func() string) *MessageService

// RegisterHandler 注册模块处理器；同名模块后注册者覆盖先注册者
func (s *MessageService) RegisterHandler(module string, h MessageHandler)

// SendMessage 发送消息（内部 API，见 3.1 节）
func (s *MessageService) SendMessage(ctx context.Context, msg Message) error

// Poll 执行一轮轮询（见 3.4 节）
func (s *MessageService) Poll(ctx context.Context)

// Cleanup 删除 30 天前的消息及其孤立消费记录（见第五章）
func (s *MessageService) Cleanup(ctx context.Context) (deletedMessages, deletedConsumers int, err error)
```

### 4.3 使用示例（未来实现）

```go
// 启动时注册处理器
msgService.RegisterHandler("resource_sync", &ResourceSyncHandler{})
msgService.RegisterHandler("restart", &RestartHandler{})
msgService.RegisterHandler("config_reload", &ConfigReloadHandler{})

// 发送广播消息：通知所有实例同步资源
err := msgService.SendMessage(ctx, Message{
    Type:           "sync_resource",
    TargetInstance: "",  // 空字符串 = 广播
    TargetModule:   "resource_sync",
    Payload: map[string]any{
        "resource_type": "config",
        "version":       "v1.2.3",
    },
    Priority:  5,
    ExpiresAt: time.Now().Add(1 * time.Hour),
})

// 发送点对点消息：通知指定实例重启
err := msgService.SendMessage(ctx, Message{
    Type:           "restart",
    TargetInstance: "instance-B",  // 指定实例 ID
    TargetModule:   "restart",
    Payload: map[string]any{
        "graceful":      true,
        "delay_seconds": 30,
    },
    Priority:  1,  // 高优先级
    ExpiresAt: time.Now().Add(10 * time.Minute),
})
```

### 4.4 具体处理器实现（示例）

具体的业务处理器在有真实场景时再实现，目前只定义框架：

```go
// 资源同步处理器（示例）
type ResourceSyncHandler struct {
    resourceManager *ResourceManager
}

func (h *ResourceSyncHandler) Handle(ctx context.Context, msg Message) error {
    // 解析 payload
    resourceType := msg.Payload["resource_type"].(string)
    version := msg.Payload["version"].(string)
    
    // 执行同步逻辑
    return h.resourceManager.Sync(resourceType, version)
}

// 重启处理器（示例）
type RestartHandler struct {
    lifecycleManager *LifecycleManager
}

func (h *RestartHandler) Handle(ctx context.Context, msg Message) error {
    graceful := msg.Payload["graceful"].(bool)
    delaySeconds := int(msg.Payload["delay_seconds"].(float64))
    
    // 执行重启逻辑
    return h.lifecycleManager.Restart(graceful, delaySeconds)
}
```

## 五、清理策略

### 5.1 保留期限

**硬编码 30 天**，不可配置。

### 5.2 执行方式

Leader 的 gocron 调度器每天 03:00 执行清理任务（`sched.AddDaily(3, 0, ...)`，任务标签 `system-cluster-message-cleanup`）。Follower 不执行。Leader 切换时新 Leader 的调度器重新注册该任务。

### 5.3 清理逻辑

```sql
-- 1. 清理过期消息主表
DELETE FROM cluster_messages WHERE created_at < ?;  -- 当前时间 - 30 天（Unix 毫秒），由 repo.DeleteBefore 传入

-- 2. 清理孤立的消费记录（消息已删除）
DELETE FROM cluster_message_consumers 
WHERE message_id NOT IN (SELECT id FROM cluster_messages);
```

### 5.4 实现位置

`internal/cluster/message_cleanup.go` 提供任务函数 `NewMessageCleanupTask`，`cmd/groot/main.go` 的 `startLeaderTasks` 中以 `sched.AddDaily(3, 0, ...)` 注册，仅 Leader 执行。

## 六、代码组织

仓储层沿用项目惯例（接口在 `internal/repo/`，实现在 `internal/repo/*db/`），集群包只依赖接口：

```
internal/repo/
├── message.go                # ClusterMessage / MessageConsumer 类型、MessageRepo 接口
└── messagedb/
    ├── message.go            # MessageRepo 的 sqlx 实现（SQLite/MySQL/PostgreSQL）
    └── message_test.go

internal/cluster/
├── cluster.go                # 现有集群管理；心跳 tick 中调用 triggerPoll()
├── election.go               # 现有选举逻辑
├── addr.go                   # 现有地址工具
├── message_handler.go        # Message 结构、MessageHandler 接口、HandlerFunc、与 repo 类型互转
├── messaging.go              # MessageService：构造、RegisterHandler、SendMessage（重试一次）、Cleanup
├── message_poller.go         # MessageService.Poll / processMessage；Cluster.SetMessageService / triggerPoll
├── message_cleanup.go        # NewMessageCleanupTask（gocron 每日任务包装）
└── message_test.go           # 单元测试
```

**心跳整合方式：** `Cluster.run()` 在每个 3 秒 tick 中先执行 `heartbeat()`，再调用 `triggerPoll()`。`triggerPoll` 用原子标志保证同一时刻只有一轮 `Poll` 在独立 goroutine 中运行；处理器不在心跳写锁内执行，耗时处理器不会阻塞心跳，上一轮未结束时本轮跳过。

**存储约定：** 时间列统一为 Unix 毫秒 `BIGINT`；`target_instance = ''` 表示广播；`payload` 以 JSON 文本存于 `TEXT`/`LONGTEXT` 列。

## 七、监控和日志

### 7.1 日志规范

```go
// 发送成功
log.Info("集群消息已发送",
    zap.String("type", msg.Type),
    zap.String("target", msg.TargetInstance),
    zap.String("module", msg.TargetModule),
    zap.Int("priority", msg.Priority))

// 轮询到消息
log.Debug("集群消息轮询",
    zap.Int("pending_count", len(messages)))

// 处理成功
log.Info("集群消息处理成功",
    zap.Int64("msg_id", msg.ID),
    zap.String("type", msg.Type),
    zap.String("module", msg.TargetModule))

// 处理失败
log.Error("集群消息处理失败",
    zap.Int64("msg_id", msg.ID),
    zap.String("type", msg.Type),
    zap.String("module", msg.TargetModule),
    zap.Error(err))

// 清理过期消息
log.Info("集群消息清理完成",
    zap.Int("deleted_messages", deletedCount),
    zap.Int("deleted_consumers", deletedConsumerCount))

// 清理失败
log.Error("集群消息清理失败", zap.Error(err))
```

### 7.2 监控策略

**仅日志，不增加独立监控接口**，理由：

- 消息量不大（手动触发的管理操作）
- 日志足够排查问题
- 可从日志中提取指标（如使用 ELK）
- 符合 YAGNI 原则

## 八、数据库迁移

### 8.1 迁移 SQL

建表语句见第二章（2.1 / 2.2 为 SQLite 版本）。三种方言的 DDL 分别追加在 `internal/db/migrate.go` 的 `sqliteDDL()` / `mysqlDDL()` / `postgresDDL()` 返回切片末尾，随现有迁移流程一并执行：

- SQLite：`INTEGER PRIMARY KEY AUTOINCREMENT`，时间列 `INTEGER`（Unix 毫秒），`payload` 为 `TEXT`
- MySQL：`BIGINT AUTO_INCREMENT`，时间列 `BIGINT`，`payload` 为 `LONGTEXT`
- PostgreSQL：`BIGINT GENERATED ALWAYS AS IDENTITY`，时间列 `BIGINT`，`payload` 为 `TEXT`

### 8.2 兼容性

- 使用 `CREATE TABLE IF NOT EXISTS`，幂等操作
- 新表对现有功能无影响
- 支持 SQLite、MySQL、PostgreSQL 三种数据库

## 九、测试策略

### 9.1 单元测试

位置：`internal/cluster/message_test.go`

覆盖场景：

- **发送测试**：正常发送、发送失败重试、并发发送
- **轮询测试**：查询广播消息、查询点对点消息、过滤已消费消息、过滤过期消息
- **处理测试**：处理成功记录、处理失败记录、未注册处理器
- **清理测试**：清理过期消息、清理孤立消费记录

### 9.2 集成测试

位置：`tests/python/` 或 `internal/cluster/integration_test.go`

覆盖场景：

- 双实例广播消息：A 发送，A 和 B 都处理
- 点对点消息：A 发送给 B，只有 B 处理
- 优先级验证：高优先级消息优先处理
- 过期验证：过期消息不被处理
- 清理验证：Leader 清理 30 天前的消息

## 十、安全性考虑

### 10.1 权限控制

- **内部 API**：只能在 Groot 代码内部调用，不对外暴露 HTTP 端点
- **未来扩展**：如需对外暴露管理 API，需增加鉴权机制（如 admin token）

### 10.2 数据验证

- 发送消息时验证必填字段（`message_type`、`payload`、`expires_at`）
- 限制 `payload` 大小（如最大 64KB），防止数据库膨胀
- 验证 `expires_at` 必须大于当前时间

### 10.3 SQL 注入防护

- 使用参数化查询（`sqlx` 自动防护）
- 不拼接 SQL 字符串

## 十一、性能考虑

### 11.1 轮询开销

- 每 3 秒查询一次数据库
- 20 个实例 = 每秒约 6.7 次查询
- 通过索引优化（`idx_target_expires`、`idx_priority_created`）
- 单次查询限制 10 条消息，避免单次处理时间过长

### 11.2 消费记录表增长

- 每条消息 × 实例数 = 消费记录数
- 广播消息：1 条消息 × 20 实例 = 20 条记录
- 30 天清理策略控制表大小

### 11.3 数据库连接池

- 复用现有数据库连接池配置
- 心跳和消息轮询在同一事务中执行（可选优化）

## 十二、未来扩展

### 12.1 优先级队列优化

当前按 `priority` 排序，未来可考虑更复杂的调度策略（如时间片轮转）。

### 12.2 消息确认机制

当前 At-least-once，未来可增加显式确认机制，支持更复杂的重试逻辑。

### 12.3 外部 API

如需对外暴露管理 API，可在 `internal/api/` 中增加端点，配合鉴权机制。

### 12.4 消息统计

在 Web UI 中展示消息统计信息（发送量、失败率、处理延迟等）。

## 十三、与现有模块的关系

### 13.1 与 cluster 模块

- **整合方式**：消息轮询逻辑整合到 `Cluster.heartbeat()`
- **共享资源**：复用 `instance_id`、数据库连接
- **独立关注点**：集群管理关注成员注册和选举，消息系统关注指令传递

### 13.2 与 message 模块

- **职责分离**：
  - `internal/message`：外部通知层（webhook/email/stdout），用于任务完成通知
  - `internal/cluster`：集群内部指令层（cluster_messages），用于实例间协调
- **无重叠**：两者服务不同场景，互不干扰

### 13.3 与 schedule 模块

- **复用调度器**：清理任务复用 `schedule.CleanupTask` 的调度机制
- **Leader 执行**：清理任务只在 Leader 执行，和定时任务保持一致

## 十四、迭代说明

### 与上一版的差异

本设计为集群消息指令系统的首次设计，无上一版本。

### 2026-09-21 实现对齐

- 调整：仓储层拆分为 `internal/repo/message.go` + `internal/repo/messagedb/`，与项目其他表一致
- 调整：时间列改为 Unix 毫秒 `BIGINT`；广播以 `target_instance = ''` 表示
- 调整：轮询在心跳 tick 中由 `triggerPoll()` 异步触发（原子标志防并发），不在 `heartbeat()` 写锁内执行
- 调整：清理任务由 Leader 的 gocron 调度器每日 03:00 执行（`AddDaily`），不复用不存在的 memory cleanup
- 新增：处理器 panic 被捕获并记录为 failed；单条处理器 30 秒超时
- 调整：功能设计章节（3.1–3.4、4.2、5.2、7.1）伪代码更新为实际实现形态

---

**设计完成时间**：2026-09-18  
**设计作者**：Claude Code  
**目标版本**：Groot v1.1.0
