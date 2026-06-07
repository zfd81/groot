# 存储抽象层设计文档

**日期**：2026-06-06  
**状态**：设计稿  
**作者**：zfd81 + Kiro  

---

## 一、功能设计

### 1.1 功能概述

`internal/storage` 是 groot 的统一文件存储抽象层，为运行时数据（如会话附件、集群状态等）提供与底层存储无关的读写接口。调用方只依赖一套稳定的 `Storage` 接口，无需感知底层是本地磁盘还是 MinIO 对象存储。存储类型在启动时由配置决定，运行期固定不变。

storage 层不做任何路径拼接：调用方传入什么 path，底层就用什么 path。这让 storage 包职责单一，且把"文件存哪、目录怎么分"的决策完全留给调用方。

### 1.2 能力清单

| 方法 | 说明 |
|------|------|
| `Write(ctx, path, r, size, contentType)` | 将数据流写入指定路径，支持大文件零拷贝；契约保证单文件原子写——目标 path 要么保留旧内容、要么写出完整新内容，不会留下半成品 |
| `Read(ctx, path)` | 以流式返回指定路径的文件内容，调用方负责关闭 |
| `Delete(ctx, path)` | 删除指定路径的单个文件 |
| `DeleteDir(ctx, path)` | 递归删除指定目录及其所有内容 |
| `Stat(ctx, path)` | 返回文件元信息（大小、MIME、修改时间、是否目录） |
| `List(ctx, dir)` | 列出指定目录下的所有文件（不递归），返回文件元信息列表 |
| `Rename(ctx, src, dst)` | 将 src 重命名为 dst，与 `os.Rename` 一致地同时支持文件和目录；dst 已存在时按"覆盖"语义处理 |

### 1.3 接口定义

```go
// Storage 是统一存储接口。
type Storage interface {
    Write(ctx context.Context, path string, r io.Reader, size int64, contentType string) error
    Read(ctx context.Context, path string) (io.ReadCloser, error)
    Delete(ctx context.Context, path string) error
    DeleteDir(ctx context.Context, path string) error
    Stat(ctx context.Context, path string) (*FileInfo, error)
    List(ctx context.Context, dir string) ([]*FileInfo, error)
    Rename(ctx context.Context, src, dst string) error
}

// FileInfo 描述一个文件或目录的元数据。
type FileInfo struct {
    Path        string    // 与调用方传入的 path 一致
    Size        int64     // 字节数
    ContentType string    // MIME 类型
    ModTime     time.Time // 最后修改时间
    IsDir       bool      // 是否为目录
}
```

### 1.4 错误约定

定义两个哨兵错误，让调用方通过 `errors.Is` 精确判断：

```go
var (
    ErrNotFound = errors.New("storage: file not found")
    ErrIsDir    = errors.New("storage: path is a directory")
)
```

- `Stat`、`Read`、`Delete` 对不存在的路径返回 `ErrNotFound`
- `Read` 对目录路径返回 `ErrIsDir`
- `Rename` 对 src 不存在返回 `ErrNotFound`
- 两种存储类型在内部将各自底层错误（`os.ErrNotExist`、MinIO `NoSuchKey`）统一映射到这两个错误
- 其他类型的错误通过 `fmt.Errorf("...: %w", err)` wrap 后返回，保留调用栈

### 1.5 path 约定

storage 层不做任何路径拼接，调用方传入什么就用什么。两种类型对 path 的语义不同：

- **local 类型**：path 必须是**文件系统绝对路径**（如 `/Users/zhangfengda/.groot/memory/sessions/abc/x.pdf`）。传入相对路径会返回错误，避免依赖进程 cwd 导致的路径漂移
- **minio 类型**：path 直接作为 **object key** 使用（如 `sessions/abc/x.pdf`），不做任何前缀处理

调用方负责：
- 决定文件物理放在哪里（如 attachment handler 决定附件位置）
- 在 local 模式下传入绝对路径
- 在 minio 模式下决定 object key 命名规则

storage 层的职责仅限于"按 path 操作底层存储"。

### 1.6 环境配置文件（不依赖 config.yaml）

MinIO 连接信息独立存放于 `$GROOT_HOME/env.yaml`（默认 `~/.groot/env.yaml`），不与 `config.yaml` 耦合。
启动时检测该文件是否存在并配置了 `minio` 节：
- 文件不存在 → 使用 local 类型（零配置）
- 文件存在但 `minio` 节为空/未配置 → 使用 local 类型
- 文件存在且 `minio` 节配置了必要字段 → 使用 minio 类型

**env.yaml 格式**：

```yaml
# 环境配置文件，存放 MinIO 等基础设施连接信息
# 删除此文件或删除 minio 节即可回退到本地磁盘存储
minio:
  endpoint: localhost:9000
  access_key: ${MINIO_ACCESS_KEY}
  secret_key: ${MINIO_SECRET_KEY}
  bucket: groot
  use_ssl: false
```

`init` 子命令会在 `~/.groot/` 下同时生成 `env.yaml`，内容**全注释**，用户取消注释并填值即可启用 MinIO。

### 1.7 加载机制

MinIO 配置仅通过 `env.yaml` 注入，`config.Load()` 内部自动处理，对外透明：

1. 解析 `config.yaml` 后，强制将 `cfg.Storage.Minio` 置为 `nil`（即 `config.yaml` 中的 `storage` 节不再生效）
2. 检测 `~/.groot/env.yaml` 是否存在：
   - 不存在 → 保持 nil（local 模式）
   - 存在 → 解析 YAML，提取 `minio` 节，展开 `${ENV_VAR}`，赋值给 `cfg.Storage.Minio`

`Config`、`StorageConfig`、`MinioConfig` 类型定义不变，`storage.New()` 签名不变。调用方（`main.go`、`chat.go`）无需任何改动。

### 1.8 目录结构

```
internal/storage/
├── storage.go    # Storage 接口、FileInfo 类型、ErrNotFound / ErrIsDir
├── local.go      # 本地磁盘存储实现
├── minio.go      # MinIO 存储实现
└── factory.go    # 根据 StorageConfig 创建对应存储类型实例
```

### 1.9 流式设计说明

接口选用 `io.Reader` / `io.ReadCloser` 而非 `[]byte`，原因如下：

- HTTP multipart 上传本身是流，`Write` 可直接透传 `r` 给底层（`os.File.ReadFrom` 或 MinIO `PutObject`）
- HTTP 下载响应 `Read` 返回的 `ReadCloser` 可直接 `io.Copy` 到 `http.ResponseWriter`，全程零拷贝

### 1.10 服务场景示例

storage 层将服务于以下场景（本期只实现 storage 层，调用方接入不在本期范围）：

| 场景 | 调用方法 | path 形态（local 模式） |
|------|---------|-----------------------|
| `/chat` 附件落地 | `Write` | `~/.groot/memory/sessions/<id>/attachments/<file>` |
| 定时任务清 session | `DeleteDir` | `~/.groot/memory/sessions/<id>/attachments/` |
| 读附件列表 | `List` | `~/.groot/memory/sessions/<id>/attachments/` |
| 读附件内容 | `Read` | `~/.groot/memory/sessions/<id>/attachments/<file>` |

minio 模式下，path 替换为对应的 object key（如 `sessions/<id>/attachments/<file>`），由调用方按业务约定构造。

### 1.11 contentType 处理的实现差异

`Write` 的 `contentType` 参数为空时：

- **local 实现**：文件系统不存储 ContentType 元数据，Stat/List 时按文件扩展名推断（参考 `mime.TypeByExtension`）
- **minio 实现**：S3 协议要求 HTTP `Content-Type` header 必须存在，minio-go 兜底成 `application/octet-stream`，无法保留为空字符串。这是 S3 协议的客观限制，不是实现 bug

调用方如果关心跨实现的 ContentType 一致性，应该在 `Write` 时显式传入正确的 ContentType。

### 1.12 Rename 实现差异与故障路径

`Rename` 同时支持文件和目录（与 `os.Rename` 一致）。两种存储类型上的实现差异较大，本节单列说明。

| 实现 | 底层操作 | 原子性 | 说明 |
|------|---------|--------|------|
| local | `os.Rename(src, dst)` | ✅ 同文件系统下原子 | 标准 POSIX rename，文件 / 目录均原子 |
| minio（文件） | `CopyObject(src, dst)` + `RemoveObject(src)` | ❌ 非原子 | CopyObject 走 `x-amz-copy-source` 头，服务端 copy，不下载数据；Copy 成功后删源 |
| minio（目录） | `ListObjects(prefix=src/, recursive=true)` 枚举 → 逐个 `CopyObject` 到 dst/...（Phase A）→ 全部 Copy 成功后逐个 `RemoveObject(src/...)`（Phase B） | ❌ 非原子 | 对象存储无目录概念，按"前缀子集搬迁"实现；细节见下文 |

minio 实现的两种形态都不是真正的原子操作，进程崩溃可能让 src 和 dst 共存。实现侧通过补偿逻辑让**语义尽量接近原子**——若失败可通过明确的恢复策略收敛——但业务层（如 schedule 的 `MoveTask`）仍需保证调用幂等，作为最后兜底。

#### 1.12.1 文件级 Rename 的补偿流程

1. **stat src**：源不存在直接返回 `ErrNotFound`
2. **清理 dst 残留**：如果 dst 已存在（上次超时或回滚失败的脏数据），先删掉
3. **CopyObject**：服务端复制 src → dst
4. **RemoveObject src**：删源；若失败，尽力回滚（删 dst）后再向上返回错误

故障路径恢复表：

| 故障点 | 结果 | 恢复方式 |
|-------|------|---------|
| src 不存在 | 返回 `ErrNotFound` | 调用方无需重试 |
| dst 残留清理失败 | 返回错误，src 未动 | 调用方重试 |
| Copy 失败 | src 存在，dst 不存在 | 调用方直接重试 |
| Copy 超时（实际成功） | src 存在，dst 可能存在 | 重试时步骤 2 清残留 |
| Delete src 失败 + 回滚成功 | src 存在,dst 不存在 | 调用方重试 |
| Delete src 失败 + 回滚失败 | src 与 dst 各一份 | 业务层幂等兜底 |
| 进程崩溃（任意步骤间） | src 与 dst 状态不可知 | 业务层幂等兜底 |

`CopyObject` 返回成功即表示 dst 已存在（S3 协议契约保证），无需额外验证。

#### 1.12.2 目录级 Rename 的补偿流程

目录在 MinIO 中表现为"具有共同 key 前缀的一组对象"。`Rename(src, dst)` 当 src 是目录时，按"先全量复制、再全量删除"两阶段执行：

1. **判别目录还是文件**：先 `StatObject(src)` 探测，命中即按文件分支处理；不命中再用 `ListObjects(prefix=src+"/", MaxKeys=1)` 探测前缀，命中即按目录分支处理；都不命中返回 `ErrNotFound`
2. **清理 dst 残留**：若 dst 在 stat 或前缀探测中存在（文件或同名目录前缀），统一调用 `DeleteDir(dst)` 兜底清理。这一步避免上一次中断遗留的脏数据让本次 Copy 出现新旧混存
3. **Phase A — 全量 Copy**：枚举 `prefix=src+"/"` 下所有对象，对每个 `key = src/sub/...` 服务端 Copy 到 `dst/sub/...`。任一对象 Copy 失败立即终止，进入"Phase A 回滚"——遍历 dst 前缀删除已 Copy 的目标对象，让 dst 回到空状态再向上返回错误
4. **Phase B — 全量 Delete**：所有对象 Copy 完成后，再次按 `prefix=src+"/"` 枚举并逐个 `RemoveObject`。任一删除失败不再回滚 dst（dst 已是权威新位置），直接向上返回错误，让业务层下一轮幂等扫描清理 src 残留

目录级故障路径恢复表：

| 故障点 | 结果 | 恢复方式 |
|-------|------|---------|
| src 不存在（无对象、无前缀命中） | 返回 `ErrNotFound` | 调用方无需重试 |
| dst 残留清理失败 | 返回错误，src 未动 | 调用方重试 |
| Phase A 中某次 Copy 失败 + 回滚成功 | src 完整，dst 已清空 | 调用方重试 |
| Phase A 中 Copy 失败 + 回滚失败 | src 完整，dst 残留若干已 Copy 对象 | 调用方重试时步骤 2 兜底清理 |
| Phase A 完整成功，Phase B 全部失败 | src 与 dst 各一份完整副本 | 调用方重试时步骤 2 清理 dst 后重新 Copy + Delete；或业务层幂等扫描收敛 |
| Phase B 中某次 Delete 失败 | src 残留部分对象，dst 已是权威完整副本 | 业务层幂等扫描收敛（识别"src 与 dst 同时存在"时优先信任 dst） |
| 进程崩溃（任意步骤间） | src 与 dst 状态不可知 | 业务层幂等兜底 |

收敛原则：**只要 dst 完整存在，业务侧应当以 dst 为权威**；src 残留视为"待清理"。这条契约由 `Storage.Rename` 的目录分支显式承诺——Phase A 成功之后，dst 一定已是完整副本，Phase B 是"可重入的清理"，不再影响业务正确性。

### 1.13 Write 单文件原子写的实现差异

`Write` 的契约要求单文件原子——目标 path 要么保留旧内容、要么写出完整新内容，不会留下半成品。两种实现各自达成原子的方式不同：

| 实现 | 底层操作 | 原子性来源 |
|------|---------|-----------|
| local | 同目录 `<path>.tmp` 写入 → `f.Sync()` → `os.Rename(<path>.tmp, <path>)` | POSIX `rename(2)` 是目录项的原子替换，要么是旧 inode、要么是新 inode，无中间态 |
| minio | 单次 `PutObject` | S3 协议契约保证 PUT 要么完整写入、要么不可见 |

local 实现细节由 `internal/storage/local.go` 自治承担，业务层完全感知不到 `.tmp`：

1. `os.MkdirAll(filepath.Dir(path), 0755)` —— 按需建目录，与原行为一致
2. **入口兜底清理孤儿 tmp**：`os.Remove(<path>.tmp)`（忽略 `ErrNotExist`），消除上一次崩溃的残留
3. 打开 `<path>.tmp`，`O_WRONLY|O_CREATE|O_TRUNC`，0644
4. `io.Copy(f, r)`；任一错误都先 `f.Close()` + `os.Remove(<path>.tmp)` 后向上返回
5. `f.Sync()` 把数据落到磁盘，再 `f.Close()`
6. `size >= 0` 时校验实际写入字节数，不一致同样删 tmp 后返回错误
7. `os.Rename(<path>.tmp, <path>)` 完成原子替换；rename 失败也删 tmp 后返回错误

`<path>.tmp` 与 `<path>` 同目录，必然在同一文件系统下，`os.Rename` 满足原子前提。`f.Sync()` 不可省略——否则崩溃时新数据仍可能停留在 page cache，rename 后看到的是空文件。

故障路径恢复表（local Write）：

| 故障点 | path 状态 | tmp 状态 | 恢复方式 |
|-------|---------|---------|---------|
| MkdirAll 失败 | 不变（旧值或不存在） | 不变 | 调用方修复目录权限后重试 |
| 入口清理 tmp 失败（非 NotExist） | 不变 | 残留 | 调用方重试；下次入口清理重新尝试 |
| OpenFile tmp 失败 | 不变 | 不变 | 调用方重试 |
| io.Copy 出错 | 不变（旧值或不存在） | 已被自动 `os.Remove` | 调用方重试 |
| Sync / Close / size 校验失败 | 不变 | 已被自动 `os.Remove` | 调用方重试 |
| Rename 失败 | 不变（旧值或不存在） | 已被自动 `os.Remove` | 调用方重试 |
| 进程崩溃（rename 之前） | 与调用前一致：path 此前存在则保留旧值，此前不存在则仍不存在 | 可能残留 `.tmp` | 下次同 path `Write` 入口处自动清理 |
| 进程崩溃（rename 期间或之后） | 已是新内容（rename 是原子原语，无中间态） | 通常已被 rename 消除；少数 fs 在 rename 后才崩可能仍有 `.tmp` | 下次同 path `Write` 入口处自动清理 |

这条契约让业务模块（`memory.saveHistory` / `SaveChatRecord`、`schedule.SaveTask` 等）**不再需要自己写 tmp + rename**——所有原子性保证下沉到 `Storage.Write` 实现内部。

---

## 二、迭代说明
**具体改动**：

- **§1.2 / §1.3 接口契约升级**：
  - `Write` 行新增"契约保证单文件原子写——目标 path 要么保留旧内容、要么写出完整新内容，不会留下半成品"
  - `Rename` 行明确"与 `os.Rename` 一致地同时支持文件和目录"，移除原"调用方拆分多对象"的限制描述
- **§1.12 Rename 拆为文件 + 目录两个分支**：
  - 表头新增 minio 目录实现行（`ListObjects(prefix, recursive=true)` + Phase A 全量 Copy + Phase B 全量 Delete）
  - 新增 §1.12.2 目录级补偿流程，覆盖 stat / 前缀探测、dst 残留清理、Phase A 失败回滚、Phase B 失败收敛四类故障
  - 显式承诺收敛原则：**Phase A 成功后 dst 即权威完整副本**，业务层在双份共存时以 dst 为准
- **§1.13 新增 Write 单文件原子写的实现差异**：
  - local 端走"同目录 `<path>.tmp` → `f.Sync()` → `os.Rename` 替换"，业务层感知不到中间文件
  - minio 端 `PutObject` 天然原子，不做额外补偿
  - 故障路径表覆盖 MkdirAll / OpenFile / io.Copy / Sync / Rename / 进程崩溃六类节点
- **下沉效果**：业务模块（`memory.saveHistory` / `SaveChatRecord` / `schedule.SaveTask` 等）**全部移除自己实现的 tmp + rename**，统一由 `Storage.Write` 兜底
- **不变**：`Storage` 接口签名、`FileInfo` 结构、错误约定（`ErrNotFound` / `ErrIsDir`）、`env.yaml` 加载机制、其余实现差异（contentType、流式语义）零改动
