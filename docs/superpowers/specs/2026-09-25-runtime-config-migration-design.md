# 限流、消息发送器与调度开关的运行时配置设计文档

## 一、功能设计

### 1.1 功能概述

接口限流参数、消息发送渠道参数、定时任务开关三项配置由设置面板维护，保存后即刻生效，无需重启服务。

三项配置各自有一个运行中的持有对象：限流参数的持有对象是限流器，发送渠道参数的持有对象是消息层，定时任务开关的持有对象是内置工具门控。配置表是持久来源。限流与发送渠道一次保存同时更新配置表与持有对象；调度开关只写表，门控与调度接口在每次求值时读表。

生效语义统一：已发起的调用按保存前的参数走完，保存后新发起的调用按新参数。正在进行的请求不被中途打断，避免一个已放行的请求在归还名额时对不上账，或一次已开始的网络投递结果无处归属。

解决的问题：

- 限流阈值需要根据实际流量调整，每次调整都重启会中断正在进行的对话。
- Webhook 地址与 SMTP 参数属于部署环境信息，由运维人员在界面上填写比编辑 YAML 更直接。
- 是否允许模型创建定时任务是一项策略判断，应当能随时切换。

### 1.2 能力清单

| 能力 | 说明 |
|---|---|
| 限流参数在线调整 | 开关、默认 QPS、默认并发、全局 QPS、全局并发五项，保存后对新调用方生效 |
| 发送渠道在线配置 | Webhook 地址与开关，SMTP 主机、端口、账号、密码、发件人与开关，保存后对新入队的消息生效 |
| 密码脱敏回读 | SMTP 密码回读时脱敏：长度超过 8 位显示 `****` 加尾四位，否则只显示 `****`，未设置时为空串；提交空串表示保持不变 |
| 定时任务开关 | 决定模型在对话中是否能看到调度工具，保存后下一次对话生效 |
| 参数校验 | 越界或不完整的参数整次拒绝，不做部分写入 |
| 关闭时免校验 | 关闭的渠道不校验其参数，使用者可在参数不全时先把渠道关掉 |

### 1.3 配置项

#### 1.3.1 限流

| 键 | 类型 | 取值范围 | 含义 |
|---|---|---|---|
| `security.rate_limit.enabled` | bool | | 是否启用限流 |
| `security.rate_limit.global_qps` | float | 0 ~ 100000 | 全部调用方合计的每秒请求数上限，0 表示不限制 |
| `security.rate_limit.global_concurrency` | int | 0 ~ 100000 | 全部调用方合计的同时对话数上限，0 表示不限制 |
| `security.rate_limit.default_qps` | float | 0 ~ 100000 | 每个调用方的每秒请求数上限，0 表示不限制 |
| `security.rate_limit.default_concurrency` | int | 0 ~ 100000 | 每个调用方的同时对话数上限，0 表示不限制 |

空闲桶的回收周期 `cleanup_interval` 是后台协程的定时参数，恒为 YAML 值。

QPS 是浮点数，校验与写表都按浮点处理；写表采用最短往返表示，`100` 写成 `100` 而非 `100.000000`。QPS 校验拒绝 NaN 与无穷；表内无法解析的浮点值退回基准值。

#### 1.3.2 发送渠道

键名带一层渠道名：`message.senders.<渠道>.<字段>`。可配置的渠道是 `webhook` 与 `email`，标准输出渠道无参数可配且恒为启用。

| 字段 | 适用渠道 | 含义 |
|---|---|---|
| `enabled` | 两者 | 是否投递到该渠道 |
| `url` | webhook | 推送地址，须为完整的 http 或 https 链接 |
| `smtp_host` | email | SMTP 主机 |
| `smtp_port` | email | SMTP 端口，1 ~ 65535 |
| `username` | email | SMTP 账号 |
| `password` | email | SMTP 密码 |
| `from` | email | 发件人地址，启用时必填 |

队列容量与工作协程数是消息层构造参数，恒为 YAML 值。

#### 1.3.3 定时任务

| 键 | 类型 | 含义 |
|---|---|---|
| `schedule.enabled` | bool | 是否允许模型在对话中创建与管理定时任务 |

调度器的并发数与目录同步间隔是构造参数，恒为 YAML 值。

### 1.4 生效机制

#### 1.4.1 限流器

限流器持有一份配置与一个全局桶，按调用方各建一个桶。全部读取路径先在读锁下取一次配置与全局桶的快照，再据此判定，一次判定内不会看到前后不一致的配置。

保存新参数时替换配置与全局桶。已建的调用方桶保留原容量，直到空闲超过回收窗口被清掉；随后首次出现的调用方按新参数建桶。归还名额按调用方名查当前桶，与取名额落在同一个桶上，收紧上限不会让已放行的请求对不上账。

全局桶只有一个实例。`GlobalQPS` 或 `GlobalConcurrency` 变化时才重建，未变则保留桶及其令牌状态。重建瞬间正在占用全局名额的请求，其归还落到新桶上，新桶因此出现「持有者多于令牌」的欠账；名额的归还一律非阻塞，欠账不会挂住任何请求，并在新桶的通道下一次排空时被吸收。

限流器是每个节点各一份的实例，保存后的替换只作用于处理该请求的节点。多节点共享同一数据库时，其他节点在下一次启动时从配置表取到新值。

#### 1.4.2 消息层

消息层的发送器注册表由读写锁保护。注册表是单个 map，值为 `registration{sender, cfg}`，把发送器实例与其配置放在同一条记录里：替换一个渠道是一次写入，查询一个渠道是一次读取，实例与启用状态之间不存在中间状态。投递一条消息时先在锁内解析出目标渠道的发送器实例，再在锁外调用它发送——发送是网络操作，持锁会把整个消息层卡住。

保存新参数时按渠道替换发送器实例并更新其启用状态。已解析出实例、正在投递的消息按旧实例发完；随后开始处理的消息走新实例，包括保存时已在队列中等待的消息。两个可配置渠道在启动时无条件注册，投递与否由启用状态决定，界面上打开渠道无需重启。

消息层是每个节点各一份的实例，保存后的替换只作用于处理该请求的节点。多节点共享同一数据库时，其他节点在下一次启动时从配置表取到新值。

SMTP 密码一旦设置，无法经接口清空：提交空串表示保持不变。需要停用凭据时换新密码或关闭渠道。

#### 1.4.3 内置工具门控

内置工具按组挂门控，门控是一个返回布尔值的函数。每次取工具时每个组的门控求值一次，求值在管理器的锁之外进行，门控读配置表不会占着锁。被挡住的工具不出现在交给模型的工具列表里，也不出现在界面的工具清单与计数中，两处口径一致。门控只作用于内置工具，外部 MCP 服务的工具不受影响，即便其名称与某个组名相同。

调度工具在服务成为 Leader 时无条件注册，挂上读取 `schedule.enabled` 的门控。门控的组键是导出常量 `mcp.BuiltinGroupSchedule`，注册方与挂门控方引用同一个符号。开关是每次取工具时读一次配置表，保存后下一次对话即按新值。这次读取带 2 秒超时；配置表读取失败或超时时门控按 YAML 值判定，一次查询失败不会关掉整组工具，也不会让取工具无限等待。

`/schedule` 系列接口同受该开关控制：开关为关时返回 503 `schedule_unavailable`，判定同样每次请求读一次配置表，保存即生效；配置表读取失败时按 YAML 值判定。

### 1.5 接口

| 路由 | 说明 |
|---|---|
| `GET /web/settings/runtime` | 读取运行时配置，含 `rate_limit` 与 `schedule` 分区 |
| `PUT /web/settings/runtime` | 整体保存，成功后同步限流器 |
| `GET /web/settings/senders` | 读取两个渠道的参数，密码脱敏 |
| `PUT /web/settings/senders` | 保存提交的渠道，成功后注册进消息层 |
| `/schedule*` | 开关为关时返回 503，判定每次请求读配置表 |

运行时配置读写同构。发送渠道接口的 `password` 字段方向不同：响应中是脱敏值，请求中空串表示保持不变。

`PUT /web/settings/runtime` 的 `rate_limit` 与 `schedule` 分区为必填，缺失即返回 400 `invalid_request`。这两个分区的全零值是合法配置（0 表示不限制、false 表示关闭），因此以指针字段区分「未携带」与「显式关闭」；其余分区的零值会被校验拒绝，缺失即被捕获。

运行时配置写入时，请求体不携带的字段（`cleanup_interval`、`max_concurrent_tasks`、`sync_interval`）以 YAML 值为底值：这几项恒为 YAML 值、不进配置表，写入前无需读表，也就不存在读改写窗口。

写表在前、通知持有对象在后。写表失败直接返回，不留下「持有对象已改、表内仍是旧值」的状态。

### 1.6 设置面板

四个分组位于「配置」分区，在附件之后：限流、定时任务、Webhook 通知、邮件通知。每组一项一行，标题下附说明。

限流与定时任务随运行时配置整体保存。Webhook 与邮件两组随发送渠道配置一起整体保存，一次提交两个渠道。开关依赖的字段排在开关上方：推送地址在 Webhook 开关上方，SMTP 各项在邮件开关上方，使前置条件在界面上直接可见。

每个控件改动即保存。保存进行中再次改动只记脏标记，由正在进行的保存收尾时再发一轮；只在最后一轮成功后用服务端回传值覆盖本地副本，避免先发后到的响应冲掉后来的改动。

SMTP 密码输入框不回显已存密码，带 `autocomplete="new-password"`，浏览器不会把已存的站点密码自动填进来。密码已设置时占位文字提示「已设置，留空保持不变」，未设置时提示「尚未设置」。

### 1.7 改动清单

| 文件 | 说明 |
|---|---|
| `internal/ratelimit/limiter.go` | 配置快照、在线重建、按调用方桶的空闲时间加锁 |
| `internal/message/layer.go` | 注册表加锁、替换与注销发送器、渠道可用性查询 |
| `internal/mcp/manager.go` | 内置工具组门控，取工具、列清单、计数、按名取一律经门控 |
| `internal/setting/runtime.go` | 限流五键与调度开关的键、边界、校验、编解码 |
| `internal/setting/message.go` | 发送渠道的键、校验、编解码，空密码保留原值 |
| `internal/setting/settings.go` | `Security`、`Message`、`Schedule` 叠加配置表 |
| `internal/api/handler/setting.go` | 命名参数构造、写表后同步持有对象、发送渠道两个接口 |
| `internal/api/handler/schedule.go` | 调度接口的可用判定：管理器存在且 `schedule.enabled` 为真，每次请求读配置表 |
| `internal/api/types/types.go` | 限流、调度、发送渠道的接口结构 |
| `internal/api/router.go` | 发送渠道两条路由 |
| `internal/api/server.go` | 把限流器与消息层交给设置 handler |
| `cmd/groot/main.go` | 启动时应用表内限流值，发送渠道无条件注册，调度工具挂门控 |
| `web/src/api/runtime.ts` | 限流与调度分区的类型、默认值、克隆、取值边界 |
| `web/src/api/senders.ts` | 发送渠道的类型、克隆、可编辑副本、接口 |
| `web/src/stores/senders.ts` | 发送渠道状态 |
| `web/src/components/settings/SettingsModal.vue` | 四个分组 |
| `web/src/i18n/messages/*` | 两语言文案 |

## 二、迭代说明

### 2.1 与上一版差异

- 新增：限流五项、发送渠道参数、调度开关进配置表，保存即生效。此前这三项在启动时读取 YAML 并固化进限流器、消息层与调度工具注册判断，改动需重启。
- 新增：`RateLimiter.Reconfigure` 与 `Config`，限流器支持在线替换参数。
- 新增：`Layer.SetSender`、`RemoveSender`、`ChannelEnabled`，消息层支持在线替换与注销发送器。`Register` 保留为启动路径的别名。
- 新增：`Manager.SetBuiltinGate`，内置工具按组挂可见性门控；`GetTools`、`ListTools`、`ToolCount`、`GetTool` 一律经门控。
- 新增：`mcp.BuiltinGroupSchedule` 常量；`Manager.hiddenGroups` 每组每次调用求值一次且在锁外；门控只作用于 `builtinTools` 成员。
- 新增：`GET`、`PUT /web/settings/senders` 两条路由。
- 新增：`sendersToPayload` 供 GET/PUT 共用；`PutSenders` 用已回读的值构造响应，不二次读表。
- 新增：`RuntimeSettingsPayload.RateLimit` 与 `Schedule` 为指针字段，缺分区返回 400；此前四个分区为值字段。
- 新增：`main.go` 门控读表带 2 秒超时。
- 新增：启用邮件时 `from` 必填；`parseFloat`/`checkFloatRange` 拒绝 NaN 与 Inf。
- 新增：前端 `saveRuntime`/`saveSenders` 脏标记串行化；SMTP 密码框 `autocomplete="new-password"`。
- 调整：`NewSettingHandler` 从五个位置参数改为 `SettingHandlerDeps` 命名参数。
- 新增：`payloadToRuntime` 增加 base 参数，取 `RuntimeStatic()`（YAML 值）补齐请求体不携带的 `CleanupInterval`、`MaxConcurrentTasks`、`SyncInterval`；这些项不进表，写入前不读表，无读改写窗口。
- 调整：配置表键名镜像 YAML 路径：`security.rate_limit.*`、`message.senders.*`、`schedule.enabled`。
- 调整：`Settings.Security`、`Message`、`Schedule` 从纯 YAML 透传改为叠加配置表。
- 调整：启动时发送渠道从「按 enabled 决定是否注册」改为无条件注册；调度工具从「按 `cfg.Schedule.Enabled` 决定是否注册」改为无条件注册加门控。
- 调整：`ScheduleHandler` 的 503 判定从「调度管理器是否存在」改为「管理器存在且 `schedule.enabled` 为真」，管理器在 Leader 上恒存在。
- 调整：消息层的 `senders` 与 `senderConfigs` 两个 map 合并为 `senders map[string]registration`。
- 调整：`ratelimit.RateLimiter.Acquire` 回滚路径与 `Release` 改为非阻塞归还，`Release` 不再检查 `Enabled`；`Reconfigure` 只在全局参数变化时重建全局桶。
- 修复：限流器的 `cfg` 字段与调用方桶的 `lastUsed` 此前无锁读写，`-race` 可报数据竞争；消息层的两个注册表 map 此前同样无锁读写。本次一并加锁，消息层两个 map 合并为一个受锁保护的 map。
- 保持不变：`security.rate_limit.cleanup_interval`、`message.queue_size`、`message.workers`、`schedule.max_concurrent_tasks`、`schedule.sync_interval` 留在 YAML。

### 2.2 后续独立迭代

- 限流的全局桶在替换瞬间存在名额归还错位，偏差不超过瞬时并发数；若日后需要严格计数，可把全局桶也改为按取得时的实例归还，做法同子 Agent 并发上限的信号量替换。
- SMTP 密码无法经接口清空；若出现无鉴权中继的需求，可在发送渠道接口增加显式的清空标志。
- 限流器与消息层的在线替换只作用于本节点；多节点若需即时同步，可经集群消息广播一条「重读配置表」指令。
- 主 Agent 一轮对话在请求路径上有三次配置表范围查询（附件校验、执行快照、内置工具门控）；当出现慢查询或改用网络型数据库时，可让门控复用执行快照。

## 三、测试

### 3.1 单元测试（Go）

| 测试对象 | 覆盖点 |
|---|---|
| `internal/ratelimit` | 重建后新调用方按新上限、已建桶保留旧容量、开关在线关停与恢复、全局桶重建、重建与请求并发无竞争、关停期间在途请求归还不泄漏名额、全局参数未变时桶实例不换、参数归零后全局桶为空、重建与清理协程并发 |
| `internal/message` | 替换发送器后新消息走新实例、注销后不投递、禁用渠道不投递且重新启用无需重注册、替换与投递并发无竞争 |
| `internal/mcp` | 门控关闭时取工具与列清单均不含该组、未挂门控的组一律可见、计数与清单同口径、`GetTool` 尊重门控、同名 MCP 的工具不被挡且同组内置工具仍被挡、一次 `GetTools`/`ListTools` 门控各求值一次 |
| `internal/setting` | 限流五项边界、浮点 QPS 解析与写出、脏数据回落、`cleanup_interval` 不写表、NaN 拒绝且表内 "NaN" 退回基准值、限流五键经假仓库往返；发送渠道往返、空密码保留原值、未知渠道与不完整参数拒绝、关闭渠道免校验、无仓库时读 YAML、启用邮件缺发件人拒绝、脏 smtp_port 回退、无仓库时 `SetMessage` 返回 `ErrNoSettingStore`、只提交 email 时不写 webhook 行、空 map 不写入；调度开关 `false` 能写进表、`schedule.enabled` 脏值回落与空表回落 |
| `internal/api/handler` | 限流保存后限流器即刻生效、越界不动限流器、回读含限流分区、缺 `rate_limit` 分区 400（消息含 rate_limit）、缺 `schedule` 分区 400、限流保存后 GET 回读 7/3/true；发送渠道密码脱敏且不含原文、保存后消息层可用、关闭后不可用、校验失败不动消息层、空密码保留、非法 JSON、只提交 webhook 后 email 在消息层仍启用；调度开关经接口往返、调度接口在开关关闭时 503、打开时放行 |

运行：`go test ./internal/ratelimit/ ./internal/message/ ./internal/mcp/ ./internal/setting/ ./internal/api/handler/ -race -v`

### 3.2 系统测试（Python）

`tests/python/` 下可补充：保存限流参数后以高于阈值的速率请求收到 429、关闭限流后恢复、发送渠道接口回读密码脱敏。由使用者自行运行。
