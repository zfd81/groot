# 存储抽象与 MinIO 存储模式设计

> ⚠️ **本文档已归档**：MinIO 模式与 `internal/storage` 整包均已退役。
>
> **后续设计**：多主机多实例的远端持久化由 [`2026-06-10-database-backend-design.md`](../2026-06-10-database-backend-design.md) 定义的 MySQL / PostgreSQL 后端 + `shared_resources` 表替代。集群共享资源的同步命令 (`groot push/pull/diff`) 已重写为基于 `ResourceRepo` 与 SHA-1 hash 比对，详见 [`../2026-06-08-sync-design.md`](../2026-06-08-sync-design.md)。
>
> 本文档仅作历史参考保留。

---

**日期**：2026-06-06
**状态**：已归档（被数据库后端设计取代）
**作者**：zfd81 + Claude

---

## 目录

- [一、功能设计](#一功能设计)
  - [1.1 背景与现状](#11-背景与现状)
  - [1.2 目标](#12-目标)
  - [1.3 为什么选 MinIO](#13-为什么选-minio)
  - [1.4 非目标（本期不做）](#14-非目标本期不做)
  - [1.5 核心设计思想](#15-核心设计思想)
  - [1.6 数据归类](#16-数据归类)
  - [1.7 各模块存储适配](#17-各模块存储适配)
    - [1.7.1 basePath 与 path 拼接规则](#171-basepath-与-path-拼接规则)
    - [1.7.2 memory 模块](#172-memory-模块)
    - [1.7.3 schedule 模块](#173-schedule-模块)
    - [1.7.4 cluster 模块](#174-cluster-模块)
  - [1.8 集群共享配置同步](#18-集群共享配置同步)
    - [1.8.1 minio 模式下的资源生效方式](#181-minio-模式下的资源生效方式)
    - [1.8.2 典型运维流程](#182-典型运维流程)
    - [1.8.3 不识别冲突](#183-不识别冲突)
  - [1.9 已知限制与后续清理项](#19-已知限制与后续清理项)
    - [1.9.1 Local.Rename 目录覆盖契约偏离](#191-localrename-目录覆盖契约偏离)
    - [1.9.2 minio mtime 秒级精度边界](#192-minio-mtime-秒级精度边界)
    - [1.9.3 attachment temp 目录路径耦合 memory](#193-attachment-temp-目录路径耦合-memory)
    - [1.9.4 main.go / chat.go basePath 分流逻辑重复](#194-maingo--chatgo-basepath-分流逻辑重复)
    - [1.9.5 Memory.Directory 自定义绝对路径与 attachment temp 解耦](#195-memorydirectory-自定义绝对路径与-attachment-temp-解耦)

---

## 一、功能设计

### 1.1 背景与现状

Groot 当前所有持久化数据均以本地文件系统存储在 `GROOT_HOME` 目录下：

- 节点本地配置：`env.yaml`（含 MinIO 等基础设施连接凭据，每节点独立）
- 集群共享配置：`config.yaml`、`skills/`、`subagents/`、`mcp/`、`GROOT.md`
- 运行时数据：`memory/`、`schedules/`、`cluster/`
- 运行日志：`logs/`

热加载机制：skills 和 GROOT.md 采用无缓存、按需读取策略（每次读取直接访问文件系统）。跨主机部署时各节点数据互相隔离。集群选举通过本地文件系统的成员目录心跳实现，仅在单主机多实例场景下有效。

### 1.2 目标

`Storage` 接口已在 [存储抽象层设计文档](2026-06-06-storage-interface-design.md) 中完成定义与 local / minio 两种实现。本期的核心工作是把 memory / schedule / cluster 三个业务模块接入这个接口：

1. **local 模式**：memory / schedule / cluster 三个模块的持久化操作改为走 `Storage` 接口（由 `storage.Local` 实现，底层仍为本地文件系统）。其他数据（集群共享配置、运行日志、附件请求级暂存）不受影响，文件读写方式与改造前一致。用户无感知，100% 向后兼容。
2. **minio 模式**：`Storage` 后端切换为 `storage.Minio`，运行时数据直接落在 MinIO bucket，跨节点共享。业务代码与 local 模式完全相同——它只认接口，不区分后端。此外新增 `groot push/pull/diff` 命令，用于集群共享配置在本地 HOME 与 MinIO 之间的显式同步。minio 模式通过 `~/.groot/env.yaml` 中的 `minio` 节启用。

### 1.3 为什么选 MinIO

| 能力 | 在本设计中的作用 |
|------|----------------|
| S3 兼容 API | `Storage` 接口的 minio 实现直接映射到 MinIO SDK |
| 对象元数据（LastModified） | cluster 心跳通过 `List` 读取 ModTime 判活，等效于文件 mtime |
| 流式读写 | 附件上传/下载零拷贝，直接透传 `io.Reader` |
| 部署成熟 | 单节点或分布式部署均可，运维成本低 |
| 跨节点天然共享 | 所有节点读写同一 bucket，无需额外同步机制 |

### 1.4 非目标（本期不做）

- 其他对象存储后端（S3、GCS 等）——`Storage` 接口预留了多后端扩展空间
- 集群共享配置的自动同步（运行时 polling MinIO）——改用主动 `push/pull` 命令
- 数据迁移工具（local → minio）

### 1.5 核心设计思想

minio 模式下，按数据特性采用三类策略：

- **运行时数据**（memory / schedule / cluster）：业务通过 `Storage` 接口**直接读写 MinIO**，不在本地缓存，跨节点强一致
- **集群共享配置**（config.yaml / skills / subagents / mcp / GROOT.md）：通过 `groot push/pull/diff` 命令在本地 HOME 与 MinIO 之间显式同步。业务运行时**从本地 HOME 读取**，使用方式与 local 模式完全一致。热加载由无缓存、按需读取策略提供
- **节点本地配置**（env.yaml）：每个节点独立维护，**不参与 sync**——它包含 MinIO 连接凭据（access_key、secret_key 等基础设施信息），是 push/pull 自身的前置条件，无法靠 sync 自身分发。env.yaml 的位置、格式与加载方式见 [存储抽象层设计文档 §1.6](2026-06-06-storage-interface-design.md)
- **启动 fail-fast**：minio 模式启动时执行 BucketExists 连通校验 + 探活式 PutObject/RemoveObject（写到保留前缀 `__startup/`），确保读写权限均满足。任一失败立即退出，避免运行时第一次 Write 才暴露

### 1.6 数据归类

```
GROOT_HOME/
├── logs/             永远本地（运行日志）
├── env.yaml          节点本地配置（含 MinIO 凭据，不参与 sync）
│
├── config.yaml       集群共享配置（HOME ⇄ MinIO 同步）
├── skills/           集群共享配置（HOME ⇄ MinIO 同步）
├── subagents/        集群共享配置（HOME ⇄ MinIO 同步）
├── mcp/              集群共享配置（HOME ⇄ MinIO 同步）
└── GROOT.md          集群共享配置（HOME ⇄ MinIO 同步）

# minio 模式下，以下数据不在 HOME，业务通过 Storage 接口直连 MinIO：
# memory/    跨节点会话数据
# schedules/ 任务定义与执行历史（leader 节点执行）
# cluster/   选举心跳协调
```

| 数据 | 类别 | local 模式 | minio 模式 | 是否参与 sync |
|------|------|-----------|-----------|------------|
| `logs/` | 运行日志 | HOME | HOME | ❌ |
| `env.yaml` | 节点本地配置 | 不存在 | HOME | ❌ |
| `config.yaml` | 集群共享配置 | HOME | HOME（本地副本） + MinIO（主存） | ✅ |
| `skills/` | 集群共享配置 | HOME | HOME（本地副本） + MinIO（主存） | ✅ |
| `subagents/` | 集群共享配置 | HOME | HOME（本地副本） + MinIO（主存） | ✅ |
| `mcp/` | 集群共享配置 | HOME | HOME（本地副本） + MinIO（主存） | ✅ |
| `GROOT.md` | 集群共享配置 | HOME | HOME（本地副本） + MinIO（主存） | ✅ |
| `memory/` | 运行时数据 | `Storage`（local） | `Storage`（minio） | ❌ |
| `schedules/` | 运行时数据 | `Storage`（local） | `Storage`（minio） | ❌ |
| `cluster/` | 运行时数据 | `Storage`（local） | `Storage`（minio） | ❌ |


### 1.7 各模块存储适配

memory / schedule / cluster 三个模块的持久化操作统一走 `Storage` 接口。接口本身不感知路径拼接；路径由调用方（`main.go`）按 storage 类型注入 `basePath`，各模块内部在 `basePath` 上做相对拼接。

#### 1.7.1 basePath 与 path 拼接规则

启动时 `main.go` 按 storage 类型为每个模块计算 basePath：

| 模块 | local 模式 basePath（绝对路径） | minio 模式 basePath（object key 前缀） |
|------|-------------------------------|---------------------------------------|
| memory | `${GROOT_HOME}/memory` | `memory` |
| schedule | `${GROOT_HOME}/schedules` | `schedules` |
| cluster | `${GROOT_HOME}/cluster/members` | `cluster/members` |

业务代码内部用 `filepath.Join(basePath, ...)` 拼出完整 path，对两种 storage 类型透明。

- **local 模式**：path 是绝对路径，`storage.Local` 强制要求绝对路径，避免 cwd 漂移
- **minio 模式**：path 是 object key（形如 `memory/sessions/abc/history.json`），`storage.Minio` 直接当 key 使用

注：minio 模式下虽然写到对象存储，业务代码仍用 `filepath.Join` 拼路径——POSIX 风格的 `/` 分隔符与 S3 object key 命名约定一致，无需特殊处理。Windows 不在本期支持范围。

#### 1.7.2 memory 模块

memory 模块把所有文件读写改走 `Storage` 接口，业务层不再持有原子写逻辑。详细设计见 [Memory 模块设计](2026-05-11-memory-design.md) §1.7。

| 操作 | 改造前 | 改造后 |
|------|--------|--------|
| `saveHistory` | `os.WriteFile(tmp)` + `os.Rename` | `storage.Write`（接口内部原子写） |
| `SaveChatRecord` | `os.WriteFile(tmp)` + `os.Rename` | `storage.Write` |
| `GetHistory` | `os.ReadFile` | `storage.Read` |
| `GetChatRecord` | `os.ReadFile` | `storage.Read` |
| `ExistsSession` | `os.Stat(history.json)` | `storage.Stat` + `errors.Is(ErrNotFound)` |
| `ListSessions` | `os.ReadDir(memoryDir)` | `storage.List(memoryDir)`（`listSessionIDs` helper 统一封装） |
| `CreateSession` | `os.MkdirAll(sessionDir/chatsDir)` | 仅调用 `saveHistory`（`Write` 自动建目录） |
| `Cleanup` | 分项删 history.json + chats/ + attachments | 单次 `storage.DeleteDir(sessionDir)` 整目录递归删，失败时跳过整个 session 下次重试 |

`Manager` 注入 `storage.Storage` 字段：`NewManager(memoryDir, retentionDays, log, store)`，`store == nil` 时 panic。Manager **不预创建** `memoryDir`（避免 minio 模式下污染 cwd）。

#### 1.7.3 schedule 模块

`schedule.Storage` 结构体由直接操作 `os.*` 改为持有 `storage.Storage` 接口实例。详细设计见 [定时任务调度系统设计](2026-05-11-schedule-design.md) §1.7。

| 操作 | 改造前 | 改造后 |
|------|--------|--------|
| `SaveTask` | `os.WriteFile(tmp)` + `os.Rename` | `storage.Write` |
| `SaveExecution` | `os.WriteFile(tmp)` + `os.Rename` | `storage.Write` |
| `LoadTask` | `os.ReadFile` 遍历三个状态目录 | `storage.Read` 遍历，`ErrNotFound` 跳过 |
| `LoadExecutions` | `os.ReadDir` + `os.ReadFile` | `storage.List` + `storage.Read` |
| `listTasksIn` | `os.ReadDir` + `os.ReadFile` | `storage.List` + `storage.Read` |
| `MoveTask` | `os.Rename(srcDir, dstDir)` | `storage.Rename`（minio 走目录级补偿流程，详见 [存储抽象层设计](2026-06-06-storage-interface-design.md) §1.12.2） |
| `DeleteTask` | `os.RemoveAll` | 先 `storage.Stat` 探测三个状态 → `storage.DeleteDir` |
| `EnsureDirs` | `os.MkdirAll` × 3 | **已删除**：所有目录由 `storage.Write` 在首次写入时按需建立 |
| `GetTaskStatus` | `os.Stat` 三连 | `storage.Stat` 三连 |

`NewStorage(baseDir, store, log)`：由调用方在启动期注入 `istorage.Storage` 单例。

#### 1.7.4 cluster 模块

cluster 模块的心跳协调通过 `Storage` 接口读写成员文件。详细设计见 [集群管理设计](2026-05-15-cluster-management-design.md)。

| 操作 | 改造前 | 改造后 |
|------|--------|--------|
| `WriteRegistration` | `os.WriteFile` | `storage.Write`（内容格式不变：`role\|host:port\|pid`，单文件原子） |
| `ListMembers` | `os.ReadDir` + `entry.Info().ModTime()` | `storage.List(membersDir)` → 读取 `FileInfo.ModTime`；`ErrNotFound` 视同空切片 |
| `RemoveFile` | `os.Remove` | `storage.Delete`（`ErrNotFound` 视为已删除，幂等） |
| `EnsureMembersDir` | `os.MkdirAll` | **已删除**：所有目录由 `storage.Write` 在首次写入时按需建立 |
| `ReadRegistration` | `os.ReadFile` 解析 role/host/port/pid | **已删除**：选举与心跳只需 ModTime 与 ID，不解析对象内容 |
| `heartbeat` 自检 | `os.Stat(ownPath)` | `storage.Stat` + `errors.Is(ErrNotFound)`；非 NotFound 错误记 WARN 跳过本轮 |

心跳判活仍以 `FileInfo.ModTime` 为锚——local 模式下是文件系统 mtime，minio 模式下是 MinIO `LastModified`，语义一致；在 ±秒级精度差异内不影响 `heartbeatTimeout = 7s` 的判定。

`Cluster.New(membersDir, host, port, log, store)`：由调用方拼好 `membersDir` 后注入，cluster 包不再做 `GROOT_HOME` 拼接。


### 1.8 集群共享配置同步

minio 模式下，集群共享配置（`config.yaml` / `skills/` / `subagents/` / `mcp/` / `GROOT.md`）通过 `groot push` / `groot pull` / `groot diff` 命令在本地 HOME 与 MinIO 之间显式同步。业务运行时仍直接读本地 HOME 文件。

sync 模块是独立的功能模块，不在本 spec 描述范围。详细设计见 [sync 模块设计](2026-06-08-sync-design.md)，包括：

- 同步资源白名单与禁止操作（§1.2 / §1.3）
- SyncManager 接口（含 `CleanTmpResidue` best-effort 清理）（§1.4）
- DiffResult 语义（双侧锚定 "本地 vs 远端"，命令层重新解释）（§1.5）
- ComputeDiff 算法（size + mtime + 1s 容差，`*.tmp` 全链路过滤）（§1.6）
- push 流程与 mtime 锚定到远端 LastModified（§1.8）
- pull 的 Phase A → Phase B 顺序保证（§1.9）
- push / pull / diff 三种渲染输出（§1.10）
- 重启提示判定（§1.10.5，仅 pull 输出）
- CLI 接口（含 `-y/--yes`）（§1.12）
- 安全约束（路径遍历防护、skill 目录原子性）（§1.14）

本 spec 与 sync spec 的职责划分：

- **本 spec**：从存储抽象视角说明"哪些资源参与 sync、minio 模式启用 sync 的前提条件、运维场景"
- **sync spec**：sync 模块自身的功能、接口、命令、错误处理细节

#### 1.8.1 minio 模式下的资源生效方式

| 资源对象 | pull 后是否立即生效 | 说明 |
|---------|------------------|------|
| `config.yaml` | ❌ 需重启 | 主配置在启动期加载，运行时不重读 |
| `skills/<skill>/` | ✅ 立即生效 | eino Backend 无缓存，下次执行自动获取 |
| `subagents/<name>/skills/<skill>/` | ✅ 立即生效 | 子 Agent 实例运行时按需读 skill 内容 |
| `GROOT.md` | ✅ 立即生效 | grootmd 模块按需读取 |
| `mcp/<server>.json` | ❌ 需重启 | MCP 配置不支持热加载 |
| `subagents/<name>/agent.md` | ❌ 需重启 | 子 Agent 入口在启动期注册 |
| `subagents/<name>/mcp/<server>.json` | ❌ 需重启 | 同上 |
| `subagents/<name>/`（目录新增/删除） | ❌ 需重启 | 子 Agent 注册仅在启动期扫描 |

`groot pull` 在执行结束时输出"需重启"提示——若本次同步涉及上表中"❌ 需重启"类资源，提示用户手动重启服务。`groot push` 与 `groot diff` 不输出此提示。

具体判定算法见 [sync 模块设计](2026-06-08-sync-design.md) §1.10.5。

#### 1.8.2 典型运维流程

```
单主机多实例（同 HOME）：
   1. 运维编辑 ~/.groot/skills/x/SKILL.md
   2. groot push                 # 同步到 MinIO（同主机其他节点共享 HOME，eino Backend 自动感知）
   3. 视资源类型决定是否重启

跨主机多实例：
   1. 运维在 host-A 编辑 ~/.groot/skills/x/SKILL.md
   2. host-A: groot push          # 同步到 MinIO
   3. host-B/host-C: groot pull   # 各自从 MinIO 拉取到本地 HOME
   4. 视资源类型决定是否重启各节点
```

新节点接入是部署运维步骤（创建 env.yaml、首次 pull、启动），不属于本期开发范围。

#### 1.8.3 不识别冲突

当前判等只能给出"一致 / 不一致"的二元事实，不区分本地改还是远端改，更不能识别"双侧都改"的冲突场景：

- `groot push` 把所有 Modified 文件推到远端，可能覆盖远端他人改动
- `groot pull` 反向同理，可能覆盖本地未推送的修改

工具假设的工作模型是"运维在一台节点集中编辑，push 出去，其他节点 pull 接收"的**单写多读**场景，方向由命令名（`push`/`pull`）显式表达。多写多机同时编辑同一资源不在本期支持范围；如未来需要冲突检测，再引入"本地账本"记录上次 sync 时双侧时间，diff 时三方比较（本地当前 vs 账本 vs 远端当前）。


### 1.9 已知限制与后续清理项

本节记录运行时数据接入 Storage 抽象层（Plan A，2026-06-08）实施过程中发现但未在本期修复的偏离与限制。每条都给出场景、风险评估与后续修复方向，作为下次迭代的入口。

#### 1.9.1 Local.Rename 目录覆盖契约偏离

`Storage.Rename` 接口契约写"dst 已存在时按覆盖语义处理（实现负责清理）"，但 [internal/storage/local.go](../../../internal/storage/local.go) 当前直接走 `os.Rename`：POSIX `rename(2)` 对**非空目录**返回 `ENOTEMPTY/EEXIST`，**不覆盖**。`schedule.MoveTask` 在 dst 残留场景下会失败而非覆盖，与接口契约不一致。

- **影响**：`MoveTask` 在重启恢复 / 异常中断后的幂等性受影响。当前 `internal/schedule/storage_test.go::TestMoveTask_DstAlreadyExists` 断言的是"失败 + 两侧数据保留"行为（实测真相），不是接口契约。
- **风险评估**：低。`schedule.MoveTask` 主流程在写入新 task 前 caller 应保证目标目录不存在；只有异常恢复路径会触发，且失败可重试。
- **修复方向**：`Local.Rename` 目录场景下先 `os.RemoveAll(dst)` 再 `os.Rename`，与 `Minio.renameDir` 的 Phase 0 清理逻辑对齐。注意需要更新 `TestMoveTask_DstAlreadyExists` 测试断言。

#### 1.9.2 minio mtime 秒级精度边界

`MemberInfo.Mtime` 在 local 模式来自 fs mtime（纳秒精度），minio 模式来自 S3 `LastModified`（秒级精度）。`heartbeatTimeout = 7s` 边界附近理论上可能产生 ±1s 的判活误差。

- **影响**：minio 模式下网络抖动 + 心跳延迟叠加时，可能误判活节点为过期，触发 leader 错误清理。被清节点在下一轮自检 ErrNotFound 时会自动 re-register 自愈。
- **风险评估**：低。有自愈机制兜底，无数据损坏，无 split-brain。
- **修复方向**：把 `heartbeatTimeout` 调到 `≥ 2 × heartbeatInterval + 1s` 缓冲，降低 false positive 概率。属于运维参数调优，无需代码改造。

#### 1.9.3 attachment temp 目录路径耦合 memory

`attachment.NewHandler` 的本地暂存目录写在 `${homeDir}/memory/temp/{taskID}/`，这是历史耦合（旧版 attachment 复用 memory 路径），与 attachment 自身职责不直接相关。

- **影响**：可读性下降。新读者会以为 attachment 与 memory 模块有隐式联系。
- **风险评估**：极低。纯命名问题，行为正确。
- **修复方向**：单独发一个 commit 把 attachment temp 目录迁到 `${homeDir}/attachments/temp/`，需要附带运维迁移脚本（清理旧 `memory/temp/` 残留）。不要混进其它改动。

#### 1.9.4 main.go / chat.go basePath 分流逻辑重复

`cmd/groot/main.go` 与 `internal/cmd/chat.go` 都有相同的 `if cfg.Storage.Minio != nil { ... } else { ... }` basePath 分流块。当前两个 callsite 内联各自维护。

- **影响**：DRY 违反。如果增加第三个 entry point 或第四个模块的 basePath，重复成本会上升。
- **风险评估**：低。两份代码都很短（一个 4 行 if/else，一个 11 行），同步成本可接受。
- **修复方向**：第三个 callsite 出现时再抽 `config.RuntimePaths(cfg, homeDir) struct{ Memory, Schedule, ClusterMembers string }`。当前不抽。

#### 1.9.5 Memory.Directory 自定义绝对路径与 attachment temp 解耦

旧实现下，如果用户把 `cfg.Memory.Directory` 配置为绝对路径（如 `/data/memory`），attachment temp 会跟着落到 `/data/memory/temp/`。Plan A 改造后 attachment temp 始终落在 `${homeDir}/memory/temp/`，与 `cfg.Memory.Directory` 解耦。

- **影响**：行为变化。把 memory 数据放在主目录之外的用户，attachment temp 仍留在主目录。
- **风险评估**：低。temp 目录是上传 base64 中转，纯临时、最终归宿是 `Storage` 接口下的附件存储；本地 temp 落点对运维监控基本无意义。
- **修复方向**：与 1.9.3 一并处理（新独立 attachment temp 路径），不再蹭 memory.Directory。如确有用户需要可配置，未来给 attachment 单独加配置项。


---
