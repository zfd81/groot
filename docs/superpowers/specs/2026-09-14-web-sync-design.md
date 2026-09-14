# Web 工作空间配置同步设计

## 一、功能设计

### 1.1 功能概述

在 Web 工作空间面板中提供集群共享配置的双向同步能力。用户在浏览器里查看本地 `~/.groot/` 与数据库之间的配置差异，逐条了解每个文件的状态，然后自行决定推送到数据库还是从数据库拉取。

同步的资源范围是白名单内的集群共享配置：`config.yaml`、`skills/`、`subagents/`、`mcp/`、`GROOT.md`。`env.yaml` 属于单机环境配置，不参与同步。

数据库中的资源记录带状态标识，删除操作只修改记录状态并更新修改时间，记录本身保留。这使得"远端从未有过这个文件"与"远端曾有此文件但已被删除"成为两种可区分的情况，用户据此判断某个差异是自己新建的还是他人删除的。

本地文件系统层面不保留任何删除痕迹：拉取时该删的文件真实删除，`~/.groot/` 目录只反映当前配置状态。

### 1.2 能力清单

- 查看全量差异：一次列出白名单范围内所有本地与数据库不一致的文件
- 查看单个资源差异：针对文件树中的某个文件或目录单独比较
- 逐条状态标识：每个差异文件标注 A / M / D，说明本地相对数据库的状态
- 远端时间参考：显示数据库侧该记录的最后修改时间，以及是否处于已删除状态
- 推送：以本地为准覆盖数据库，包含在数据库侧标记删除
- 拉取：以数据库为准覆盖本地，包含真实删除本地文件
- 删除前确认：操作将导致文件删除时明确告知数量
- 重启提示：拉取内容涉及需重启才生效的配置时给出提示

### 1.3 差异语义

差异状态以本地为基准表述，同一份清单在两个方向下共用：

| 标识 | 含义 | 推送时的动作 | 拉取时的动作 |
|---|---|---|---|
| `A` | 本地有，数据库无有效记录 | 写入数据库 | 删除本地文件 |
| `M` | 两侧都有，内容不同 | 本地覆盖数据库 | 数据库覆盖本地 |
| `D` | 本地无，数据库有 | 在数据库标记删除 | 写入本地 |

标识只陈述客观状态，不隐含方向。方向由用户点击推送或拉取来决定。

数据库记录的状态与本地文件是否存在，组合出四种判定：

| 数据库记录 | 本地文件 | 判定 |
|---|---|---|
| 有效，内容哈希不同 | 存在 | `M` |
| 有效，内容哈希相同 | 存在 | 一致，不列出 |
| 有效 | 不存在 | `D` |
| 已标记删除 | 存在 | `A` |
| 已标记删除 | 不存在 | 一致，不列出 |
| 无记录 | 存在 | `A` |

其中两种 `A` 通过远端记录是否存在来区分：已标记删除的记录说明该文件被他人删除，无记录说明该文件是本地新建。

### 1.4 数据模型

`shared_resources` 表包含状态列：

```sql
status TEXT NOT NULL DEFAULT 'active'   -- 'active' | 'deleted'
```

默认值 `active` 使已有数据在加列后语义正确。

删除操作将记录状态置为 `deleted`，同时清空内容、大小与哈希，并把 `updated_at` 更新为删除时刻。内容清空后不占用存储，`updated_at` 统一表达"这条记录最后一次变更的时间"，不区分变更是修改还是删除。

写入操作无条件将状态置回 `active`，因此一个曾被删除的路径重新推送后自然恢复，`updated_at` 变为新的推送时间。

### 1.5 资源仓库接口

`repo.Resource` 与 `repo.ResourceEntry` 携带 `Status` 字段。

`ResourceRepo` 的读方法默认只返回有效记录：`Get`、`Stat`、`List` 过滤 `status='active'`，查询到已删除记录时按不存在处理，返回 `repo.ErrNotFound`。对调用方而言"已删除"与"不存在"表现一致。

差异比较需要看到已删除记录，由独立方法提供：

```go
// ListWithDeleted 返回 prefix 下所有记录，含已标记删除的记录。
// 仅供同步差异比较使用；常规读取请用 List。
ListWithDeleted(ctx context.Context, prefix string) ([]*ResourceEntry, error)
```

数据库实现（`resourcedb`）承载完整语义。写入时状态列参与 upsert 的更新字段列表，确保复活场景下状态正确回到 `active`。

本地文件系统实现（`resourcelocal`）的存储介质是文件系统，删除即真实删除文件，不存在记录层。其 `ListWithDeleted` 等同于 `List`，`Status` 恒为 `active`。该实现服务于 SQLite 单机模式，此模式下同步功能整体不启用。

### 1.6 后端架构

同步核心逻辑由 `internal/sync` 提供，Web 层复用同一个 `SyncManager`，不实现独立的同步算法。

```
NewServer(..., resources repo.ResourceRepo)
  └─ handler.NewSyncHandler(homeDir, resources)
       └─ sync.NewSyncManager(homeDir, resources)
```

SQLite 模式下 `NewSyncManager` 返回禁用实现，所有方法回 `ErrSyncDisabled`，Web 层无需自行判断数据库模式。

三个端点注册在 `webGroup` 下，与 `/web/files` 平级：

| 端点 | 方法 | 说明 |
|---|---|---|
| `/web/sync/diff` | POST | 计算差异，只读 |
| `/web/sync/push` | POST | 本地 → 数据库 |
| `/web/sync/pull` | POST | 数据库 → 本地 |

请求体统一为 `{"paths": ["skills/weather"]}`，`paths` 省略或为空表示全量。差异查询使用 POST 而非 GET，因为路径是数组，请求体承载比查询串更清晰。

服务端不保存差异快照。用户确认后，`Push` 与 `Pull` 内部重新计算差异再执行，以执行那一刻的真实状态为准，不存在过期快照问题。

路径合法性由 `SyncManager` 内部的 `resolveSyncPaths` 校验，覆盖白名单与路径遍历。Web 层不做第二套校验，避免规则漂移。

响应结构在三个端点间共用：

```json
{
  "entries": [
    {"path": "skills/weather/SKILL.md", "status": "A", "remote_deleted": true, "remote_updated_at": 1757745000000},
    {"path": "subagents/db-agent/agent.md", "status": "A", "remote_deleted": false, "remote_updated_at": null},
    {"path": "config.yaml", "status": "M", "remote_deleted": false, "remote_updated_at": 1757734800000},
    {"path": "mcp/github/config.json", "status": "D", "remote_deleted": false, "remote_updated_at": 1757808300000}
  ],
  "in_sync": 12,
  "needs_restart": true
}
```

`status` 使用单字母，与界面显示一致。`remote_deleted` 区分两种 `A`。`remote_updated_at` 为毫秒时间戳，无记录时为 null。`in_sync` 只给出一致文件的数量，不列出清单。`needs_restart` 由服务端根据路径前缀判定。

推送与拉取执行完成后返回重新计算的差异，正常情况下 `entries` 为空且 `in_sync` 等于总数。

### 1.7 拉取的本地副作用

拉取写入文件使用 tmp + rename 原子写。删除本地文件后逐级清理变空的父目录，向上清理到白名单根目录为止，白名单根目录自身保留，使工作空间面板中不留空壳目录。

拉取成功后前端刷新文件树，并关闭已打开的编辑器与预览，避免用户在陈旧内容上保存从而覆盖刚拉取的内容。

### 1.8 前端交互

工作空间面板头部工具栏提供同步入口，点击后请求差异并打开对话框。文件树行的「⋯」菜单提供针对单个文件或目录的同步入口，带该节点路径请求差异，打开同一个对话框。

对话框主体是一份扁平清单，按路径排序，不分组。每行三段：文件名、所属目录（灰色小字）、状态标识。状态标识配色沿用版本控制惯例：`A` 绿色、`M` 橙色、`D` 红色。行尾显示远端记录时间，或标注"远端已删除"、"远端无记录"。

清单下方给出一致文件数量与重启提示。底部两个按钮分别为推送到数据库与从数据库拉取。

操作将导致删除时弹出二次确认，明确告知数量：推送且清单含 `D` 时提示将在数据库删除若干文件，拉取且清单含 `A` 时提示将删除本地若干文件。清单只含 `M` 时直接执行。

无差异时清单显示已是最新，两个按钮禁用。

同步功能未启用时隐藏入口图标。

界面文案通过 i18n 提供中英文。状态标识 A / M / D 作为符号不翻译，其悬停解释文案翻译。

### 1.9 错误处理

| 情况 | HTTP | 响应 |
|---|---|---|
| 同步功能未启用 | 409 | `{"status":"sync_disabled"}` |
| 路径不在白名单或含路径遍历 | 400 | 校验错误信息 |
| 数据库或文件系统错误 | 500 | 错误信息 |

前端收到 `sync_disabled` 时隐藏同步入口。

### 1.10 测试策略

Go 单元测试覆盖：

- `internal/repo/resourcedb`：删除后 `Get`/`Stat`/`List` 查不到而 `ListWithDeleted` 能查到；重新写入使状态回到 `active`；删除不存在的路径不报错
- `internal/sync/diff`：1.3 节六种判定组合，重点是已删除记录与本地文件存在与否的两种交叉
- `internal/sync/sync`：推送在数据库侧标记删除；拉取真实删除本地文件；空父目录清理
- `internal/api/handler/sync`：三个端点的正常返回、`sync_disabled` 的 409、非法路径的 400
- `internal/db/migrate`：已有库加列后旧数据状态为 `active`

系统测试由用户在 `tests/python/` 下自行运行。

## 二、迭代说明

### 2.1 与上一版差异

**数据模型**

- 新增：`shared_resources` 表增加 `status` 列，三种数据库的建表语句同步调整
- 新增：针对已部署数据库的 `ALTER TABLE ADD COLUMN` 迁移。现有迁移是一组幂等建表语句，没有版本号机制，因此加列需要配合列存在性检查，参照 `dropIndexIfExists` 的现有模式实现
- 调整：`resourcedb.Delete` 从物理删除改为修改状态列、清空内容并更新 `updated_at`
- 调整：`resourcedb` 的 `Get`/`Stat`/`List` 增加有效状态过滤；`Put` 的 upsert 更新字段列表纳入 `status`

**接口**

- 新增：`ResourceRepo.ListWithDeleted`
- 新增：`repo.Resource` 与 `repo.ResourceEntry` 的 `Status` 字段
- 说明：`ResourceRepo` 在生产代码中的消费方目前仅有 `cmd/groot/main.go` 的 `openSyncRepo`，配置与 skill 加载走文件系统，不读此表，因此读过滤的影响范围限于同步功能

**同步逻辑**

- 调整：`ComputeDiff` 改用 `ListWithDeleted`，并跳过数据库已删除且本地也不存在的路径
- 新增：`Pull` 删除本地文件后清理变空的父目录

**Web 层**

- 新增：`internal/api/handler/sync.go` 与三个端点
- 调整：`NewServer` 增加 `repo.ResourceRepo` 参数
- 新增：`web/src/components/files/SyncDialog.vue`、`web/src/api/sync.ts`
- 调整：工作空间面板工具栏增加同步入口，文件树行菜单增加同步项
- 新增：`zh-cn.ts` 与 `en.ts` 的 `sync` 文案段

**CLI**

- 调整：`groot diff` 输出标注远端已删除状态，与 Web 侧信息一致
- 修正：`internal/sync/render.go` 的重启提示文案中遗留的 "MinIO" 字样改为数据库，该字样是迁移到数据库后端前的残留

