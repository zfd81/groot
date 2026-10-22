# 对话中止与会话删除设计文档

## 一、功能设计

### 1.1 功能概述

groot 提供两个会话生命周期管理接口：中止一次正在生成的对话，以及删除一个会话及其全部对话记录。两者都遵循会话租户与用户隔离规则（见 `2026-10-21-session-tenant-isolation-design.md`）：调用方只能操作属于自己租户与用户的会话，归属不符的会话对调用方表现为"不存在"；Web 管理员不受此限制。

在多实例集群中，对话在哪个节点执行是该节点的进程内信息。中止与删除请求可以到达任意节点，通过集群消息把取消指令广播到所有节点，由实际执行该对话的节点完成取消。

### 1.2 能力清单

1. `POST /chat/{sid}/{cid}/stop`：中止会话 `sid` 中正在执行的对话 `cid`。被中止的对话以 `cancelled` 状态落库，SSE 流正常收尾，之后仍可通过 `GET /chat/{sid}/{cid}` 查看。
2. `DELETE /sess/{sid}`：删除会话及其所有对话记录。会话正在执行对话时先中止，等待执行结束后再删除。
3. 新增 API Key 权限点 `delete`，控制 `DELETE /sess/{sid}`。中止接口归属现有的 `chat` 权限点。
4. 运行时状态为每次对话持有可取消的上下文，供中止与删除使用；执行链路通过该上下文感知取消。
5. 跨节点取消：请求到达的节点本地没有该对话时，通过集群消息广播取消指令，所有节点各自尝试本地取消，命中者执行。

### 1.3 运行时取消机制

`agent.RuntimeState.Register(ctx, sessionID, chatID)` 接收调用方的父上下文，派生一个可取消的子上下文并把 cancel 函数保存在 `ActiveChat` 中。`/chat` 处理器把派生出的上下文交给 `Executor.Execute`，因此：

- 父上下文取消时子上下文随之取消。当前 Hertz 服务端未开启客户端断开感知，因此客户端断开不会触发父上下文取消，对话会执行完毕；这是现状，本功能不改变它。
- 外部调用 `RuntimeState.Cancel(sessionID, chatID) bool` 时，子上下文取消，执行链路在下一个检查点退出，`Executor` 把记录以 `cancelled` 状态落库，并在 goroutine 结束时从运行时状态中删除该会话。

`ActiveChat` 新增字段：

| 字段 | 类型 | 说明 |
|------|------|------|
| `ctx` | `context.Context` | 本次对话的执行上下文，不序列化 |
| `cancel` | `context.CancelFunc` | 取消函数，不序列化 |
| `done` | `chan struct{}` | 执行 goroutine 退出时关闭，不序列化 |

`RuntimeState` 新增方法：

- `Register(parentCtx context.Context, sessionID, chatID string) (*ActiveChat, error)`：签名增加父上下文；返回的 `ActiveChat` 携带 `ctx`。
- `Cancel(sessionID, chatID string) bool`：会话有活跃对话且其 `ChatID == chatID` 时调用 cancel 并返回 true，否则返回 false。不删除状态，删除仍由执行 goroutine 的 `defer Delete` 完成。
- `Delete(sessionID)`：在删除状态的同时关闭 `done`。
- `Wait(sessionID string, timeout time.Duration) bool`：会话没有活跃对话立即返回 true；有则等待其 `done` 关闭，超时返回 false。

`ActiveChat.Status` 在 `Cancel` 成功时置为 `cancelled`，读写都在 `ActiveChat.mu` 保护下进行，并提供 `StatusSnapshot() string` 供 `GET /chat/status/{sid}` 带锁读取。

`/chat` 处理器在 `Register` 之后、goroutine 启动之前的所有失败返回路径（创建会话失败、附件处理失败等）都必须调用 `RuntimeState.Delete(sessionID)`，否则 `done` 永不关闭，后续对该会话的 `Wait` 必然超时。

### 1.3.1 跨节点取消

集群消息服务（`cluster.MessageService`）新增模块 `chat`，处理器注册在 `main.go` 中与 `lifecycle` 模块并列。消息类型 `chat.cancel`，载荷：

```json
{"session_id": "...", "chat_id": "..."}
```

`TargetInstance` 为空（广播）。每个节点收到后调用本地 `RuntimeState.Cancel(session_id, chat_id)`，返回值仅记日志，处理器始终返回 nil（未命中不是错误）。消息过期时间 30 秒：轮询周期为心跳间隔 3 秒，30 秒后仍未消费的节点视为已离线或该对话早已结束。

发送方为新增的 `agent.ChatCanceller` 接口：

```go
type CancelResult int

const (
    CancelLocal      CancelResult = iota // 本地命中并已取消
    CancelBroadcast                      // 本地未命中，取消指令已广播到集群
    CancelNotRunning                     // 本地未命中，且无法广播（集群中没有其他存活节点，或广播失败）
)

type ChatCanceller interface {
    // Cancel 先尝试本地取消。广播失败时返回 CancelNotRunning 与非 nil error。
    Cancel(ctx context.Context, sessionID, chatID string) (CancelResult, error)
}
```

实现 `agent.NewClusterCanceller(rt *RuntimeState, sender MessageSender, othersAlive func(ctx) bool)`。`othersAlive` 回答"集群中是否存在除本节点以外、心跳在超时窗口（7 秒）内的其他成员"，由成员表（`repo.MemberRepo.ListAll`）与本节点 `RegID` 计算；`sender` 为 nil 或 `othersAlive` 返回 false 时，`Cancel` 只做本地尝试，不写消息。中止与删除处理器依赖 `ChatCanceller`，不直接依赖 `RuntimeState` 的取消能力。

`ChatCanceller` 同时暴露 `OthersAlive(ctx) bool`，供删除接口决定是否需要广播与等待。

### 1.4 中止接口

`POST /chat/{sid}/{cid}/stop`，权限点 `chat`。

处理流程：

1. 路径参数 `sid`、`cid` 为空：400 `invalid_request`。
2. 前置检查：`requireOwner`（Web 管理员放行；租户为空 403；用户缺失 400）。
3. 归属判定：`ownsOrAdmin` 为 false → 404 `session_not_found`，响应体与会话不存在时完全相同。
4. `ChatCanceller.Cancel(ctx, sid, cid)`：
   - 本地命中 → 200 `{"status":"success","session_id":sid,"chat_id":cid,"message":"对话已请求中止"}`。
   - 本地未命中、集群中有其他存活节点且广播成功 → 202 `{"status":"accepted","session_id":sid,"chat_id":cid,"message":"中止请求已广播到集群"}`。
   - 本地未命中且无法广播（集群中没有其他存活节点，或广播写库失败）→ 409 `{"status":"chat_not_running","session_id":sid,"chat_id":cid,"message":"该对话未在执行"}`。

中止是异步的：接口返回时执行链路正在收尾（200），或取消指令正在传播（202）。调用方通过 SSE 流的结束或 `GET /chat/status/{sid}` 回到 idle 确认完成。202 不代表远端一定命中：如果对话早已结束或 chat_id 不匹配，所有节点都会空操作。

### 1.5 删除接口

`DELETE /sess/{sid}`，权限点 `delete`。

处理流程：

1. 路径参数 `sid` 为空：400 `invalid_request`。
2. 前置检查：`requireOwner`。
3. 归属判定：`ownsOrAdmin` 为 false → 404 `session_not_found`，响应体同上。
4. 本地有活跃对话：`RuntimeState.Cancel(sid, 活跃 chat_id)`，然后 `RuntimeState.Wait(sid, 10*time.Second)`。等待超时 → 409 `{"status":"chat_still_running","session_id":sid,"message":"对话仍在执行，请稍后重试"}`，不删除任何数据。
5. 本地无活跃对话且集群中有其他存活节点（`OthersAlive`）：广播 `chat.cancel`（chat_id 为空串，表示取消该会话的任意活跃对话；`RuntimeState.Cancel` 对空 chat_id 不做匹配），然后固定等待 5 秒（心跳轮询 3 秒加余量）让远端节点收尾。没有其他存活节点时跳过本步。
6. 占位：`RuntimeState.Register(ctx, sid, "delete")` 占住该会话，使并发到达的 `POST /chat` 续聊收到 409，封堵"等待结束到删库之间插入新对话"的窗口。占位失败（Wait 之后又有新对话抢先注册）→ 409 `chat_still_running`。删库完成后 `Delete(sid)` 释放。
7. `memory.Manager.DeleteSession(sid)`：删除 `memory_chats` 与 `memory_sessions` 中的记录（现有实现，事务内完成）。失败 → 500 `{"status":"error","message":"删除会话失败"}`。
8. 成功 → 200 `{"status":"success","session_id":sid}`。

删除是终态：删除后对该 sid 的所有读取接口按"不存在"响应。

Web 管理员可删除任意会话，包括定时任务产生的归属为空的会话。

**已知边角，不在本功能处理范围内：**

- 定时任务的执行不经过运行时状态注册，也不是"对话"，其会话记录只是任务执行留下的数据。管理员在任务正在执行的瞬间删除其会话时，中止与等待不生效，已落库的记录被删除；任务执行完毕后尝试写回的那条对话记录，会被对话落库时的会话存在性检查拒绝（`SaveChat` 在事务内先查 `memory_sessions`，不存在则返回未找到），只在服务端日志留下"保存对话记录失败"，数据库中不产生任何残留。任务本身的管理在 `/schedule` 接口。
- 集群模式下远端节点收尾晚于 5 秒固定等待时，同样是该轮记录被拒绝写入并记日志，不产生残留。
- 删除接口在等待结束到占位之间若有新对话抢先注册并返回 409，之后该节点会在下一轮消息轮询（3 秒内）消费到自己刚广播的 `chat.cancel`（chat_id 为空，匹配任意对话），把这条新对话取消。这是删除意图的自然延伸，不是数据丢失，文档中说明即可。

### 1.6 权限点

`repo.ValidPermissions` 增加 `delete`。中间件路径映射：

| 路径 | 方法 | 权限点 |
|------|------|--------|
| `/chat/{sid}/{cid}/stop` | POST | `chat` |
| `/sess/{sid}` | DELETE | `delete` |

`/sess/*` 的 GET 仍为 `session`（`/sess/history` 为 `history`）。映射函数先判断 `/stop` 后缀与 DELETE 方法，再落入现有前缀规则。

Web 管理界面 API Key 权限选项与 README 权限表同步增加 `delete`。

### 1.7 路由

```
apiGroup.POST("/chat/:sid/:cid/stop", stopH.Serve)
apiGroup.DELETE("/sess/:sid", sessionH.DeleteSession)
```

`/chat/:sid/:cid/stop` 为三段路径，与现有 `/chat/:sid/:cid`（两段）和 `/chat/status/:sid` 不冲突。

### 1.8 测试要点

- `RuntimeState`：Cancel 命中/未命中/cid 不匹配；Wait 无活跃立即返回、有活跃等待 Delete、超时返回 false；父 ctx 取消传播到子 ctx。
- stop handler：400 参数缺失、403/400 前置检查、404 归属不符、409 未运行、200 命中并使 ctx.Done 关闭、Web 管理员放行。
- delete handler：404 归属不符、200 删除后 ExistsSession 为 false 且 chats 清空、运行中先取消再删除、Wait 超时 409 且数据未删、管理员删除空归属会话。
- 中间件权限映射：stop 路径需要 `chat`，DELETE /sess 需要 `delete`，GET /sess/{sid} 仍为 `session`；`delete` 在 ValidPermissions 内。
- `ClusterCanceller`：本地命中不发消息；本地未命中且 send 非 nil 发一条 `chat.cancel` 广播；send 为 nil 返回 (false, nil)；send 失败返回 (false, err)。
- `chat` 模块处理器：解析载荷并调用本地 Cancel；chat_id 为空时按会话取消；载荷非法返回 error 由消息服务记录。
- delete 占位：占位期间并发 `/chat` 续聊收到 409；删库后占位释放。

### 1.9 非目标

- 不提供批量删除或按条件删除。
- 不提供"强制删除"跳过等待。
- 不改变现有 SSE 事件格式；中止时沿用执行链路现有的取消收尾路径。
- 不中止定时任务的执行，不在数据库中记录对话所在节点。
- 不开启 Hertz 的客户端断开感知。

## 二、迭代说明

### 2.1 与上一版差异

上一版（租户隔离完成后的 master）没有中止与删除接口，`memory.Manager.DeleteSession` 与 `repo.DeleteSession` 存在但无路由；执行链路已响应 context 取消，但 cancel 函数无处保存，外部无法触发。

本版改动：

- 新增：`RuntimeState.Cancel`、`Wait`，`ActiveChat` 携带 ctx/cancel/done；`Register` 签名增加父上下文。
- 新增：`POST /chat/{sid}/{cid}/stop` 处理器与路由，权限 `chat`。
- 新增：`DELETE /sess/{sid}` 处理器与路由，权限点 `delete`（`ValidPermissions`、中间件映射、Web 权限选项、README 同步）。
- 调整：`/chat` 处理器把 `Register` 返回的 ctx 传给 `Executor.Execute`，并在 Register 之后的所有失败返回路径补上 `RuntimeState.Delete`。
- 新增：`agent.ChatCanceller`（含 `OthersAlive`）与 `ClusterCanceller`（以成员表存活探测决定是否广播）；集群消息模块 `chat`、消息类型 `chat.cancel`；`main.go` 注册处理器。
- 调整：`ActiveChat.Status` 读写加锁，新增 `StatusSnapshot()`。
