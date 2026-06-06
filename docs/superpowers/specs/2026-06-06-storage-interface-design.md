# 存储抽象层设计文档

**日期**：2026-06-06  
**状态**：设计稿  
**作者**：zfd81 + Kiro  

---

## 一、功能设计

### 1.1 功能概述

`internal/storage` 是 groot 的统一文件存储抽象层，为附件管理提供与底层存储无关的读写接口。调用方只依赖一套稳定的 `Storage` 接口，无需感知底层是本地磁盘还是 MinIO 对象存储。存储类型在启动时由配置决定，运行期固定不变。

storage 层不做任何路径拼接：调用方传入什么 path，底层就用什么 path。这让 storage 包职责单一，且把"文件存哪、目录怎么分"的决策完全留给调用方。

### 1.2 能力清单

| 方法 | 说明 |
|------|------|
| `Write(ctx, path, r, size, contentType)` | 将数据流写入指定路径，支持大文件零拷贝 |
| `Read(ctx, path)` | 以流式返回指定路径的文件内容，调用方负责关闭 |
| `Delete(ctx, path)` | 删除指定路径的单个文件 |
| `DeleteDir(ctx, path)` | 递归删除指定目录及其所有内容 |
| `Stat(ctx, path)` | 返回文件元信息（大小、MIME、修改时间、是否目录） |
| `List(ctx, dir)` | 列出指定目录下的所有文件（不递归），返回文件元信息列表 |
| `Rename(ctx, src, dst)` | 将 src 重命名为 dst，dst 已存在时按"覆盖"语义处理 |

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

### 1.6 配置结构

在现有 `Config` 中新增 `Storage` 字段：

```go
type StorageConfig struct {
    Minio *MinioConfig `yaml:"minio"` // 非 nil 则使用 minio 类型；nil 则使用 local 类型
}

type MinioConfig struct {
    Endpoint  string `yaml:"endpoint"`
    AccessKey string `yaml:"access_key"`
    SecretKey string `yaml:"secret_key"`
    Bucket    string `yaml:"bucket"`
    UseSSL    bool   `yaml:"use_ssl"`
}
```

**类型选择规则**（由 `factory.go` 实现）：配置了 `minio` 节则使用 minio 类型；未配置则使用 local 类型。local 类型零配置，无需任何参数。

`init` 子命令生成的 `config.yaml` 默认包含完整的 storage 节，minio 配置以注释形式给出，方便用户切换时取消注释填值即可：

```yaml
storage:
  # 未配置 minio 时使用本地磁盘存储（默认）
  # minio:
  #   endpoint: localhost:9000
  #   access_key: ${MINIO_ACCESS_KEY}
  #   secret_key: ${MINIO_SECRET_KEY}
  #   bucket: groot
  #   use_ssl: false
```

### 1.7 目录结构

```
internal/storage/
├── storage.go    # Storage 接口、FileInfo 类型、ErrNotFound / ErrIsDir
├── local.go      # 本地磁盘存储实现
├── minio.go      # MinIO 存储实现
└── factory.go    # 根据 StorageConfig 创建对应存储类型实例
```

### 1.8 流式设计说明

接口选用 `io.Reader` / `io.ReadCloser` 而非 `[]byte`，原因如下：

- HTTP multipart 上传本身是流，`Write` 可直接透传 `r` 给底层（`os.File.ReadFrom` 或 MinIO `PutObject`）
- HTTP 下载响应 `Read` 返回的 `ReadCloser` 可直接 `io.Copy` 到 `http.ResponseWriter`，全程零拷贝

### 1.9 服务场景示例

storage 层将服务于以下场景（本期只实现 storage 层，调用方接入不在本期范围）：

| 场景 | 调用方法 | path 形态（local 模式） |
|------|---------|-----------------------|
| `/chat` 附件落地 | `Write` | `~/.groot/memory/sessions/<id>/attachments/<file>` |
| 定时任务清 session | `DeleteDir` | `~/.groot/memory/sessions/<id>/attachments/` |
| 读附件列表 | `List` | `~/.groot/memory/sessions/<id>/attachments/` |
| 读附件内容 | `Read` | `~/.groot/memory/sessions/<id>/attachments/<file>` |

minio 模式下，path 替换为对应的 object key（如 `sessions/<id>/attachments/<file>`），由调用方按业务约定构造。

### 1.10 contentType 处理的实现差异

`Write` 的 `contentType` 参数为空时：

- **local 实现**：文件系统不存储 ContentType 元数据，Stat/List 时按文件扩展名推断（参考 `mime.TypeByExtension`）
- **minio 实现**：S3 协议要求 HTTP `Content-Type` header 必须存在，minio-go 兜底成 `application/octet-stream`，无法保留为空字符串。这是 S3 协议的客观限制，不是实现 bug

调用方如果关心跨实现的 ContentType 一致性，应该在 `Write` 时显式传入正确的 ContentType。

### 1.11 Rename 实现差异与故障路径

`Rename` 在两种存储类型上的实现差异较大，本节单列说明。

| 实现 | 底层操作 | 原子性 | 说明 |
|------|---------|--------|------|
| local | `os.Rename(src, dst)` | ✅ 同文件系统下原子 | 标准 POSIX rename，开销为零 |
| minio | `CopyObject(src, dst)` + `RemoveObject(src)` | ❌ 非原子 | CopyObject 走 `x-amz-copy-source` 头，服务端 copy，不下载数据；Copy 成功后删源 |

minio 实现下 Copy + Delete 不原子，进程崩溃会导致 src 和 dst 各有一份。业务层（如 schedule 的 `MoveTask`）需保证幂等：依赖 `DueTasks` 扫描或 `SetStatus` 重试自然修复，不依赖 Rename 本身的原子性。

minio 实现通过补偿逻辑让语义尽量接近原子：

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
| Delete src 失败 + 回滚成功 | src 存在，dst 不存在 | 调用方重试 |
| Delete src 失败 + 回滚失败 | src 与 dst 各一份 | 业务层幂等兜底 |
| 进程崩溃（任意步骤间） | src 与 dst 状态不可知 | 业务层幂等兜底 |

`CopyObject` 返回成功即表示 dst 已存在（S3 协议契约保证），无需额外验证。

---

## 二、迭代说明

### 2.1 改动清单

#### 2.1.1 storage 包本体（新增）

| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/storage/storage.go` | 新增 | `Storage` 接口、`FileInfo` 类型、`ErrNotFound` / `ErrIsDir` |
| `internal/storage/local.go` | 新增 | 本地磁盘存储实现，校验 path 必须为绝对路径 |
| `internal/storage/minio.go` | 新增 | MinIO 存储实现，path 直接作为 object key |
| `internal/storage/factory.go` | 新增 | 根据配置自动选择存储类型 |

#### 2.1.2 配置层（修改）

| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/config/config.go` | 修改 | 新增 `StorageConfig`、`MinioConfig` 结构体；`Config` 加 `Storage StorageConfig` 字段 |
| `internal/config/defaults.go` | 修改 | storage 默认值为空（local 模式零配置） |
| `internal/config/template.go` | 修改 | init 生成的配置模板加 storage 块，minio 配置以注释形式给出 |
| `go.mod` | 修改 | 新增 `github.com/minio/minio-go/v7` 依赖 |

#### 2.1.3 现有附件读写迁移到 storage（修改）

为了让 storage 接口立刻发挥作用，本期将现有 memory 模块中**附件相关**的文件操作改为调用 storage 层。注意：只迁移附件读写，不动 `history.json`、`chats/`、`SESSION.md` 等会话元数据的读写（那些不是附件）。

| 文件 | 操作 | 说明 |
|------|------|------|
| `internal/memory/manager.go` | 修改 | `Manager` 结构体新增 `storage Storage` 字段；`NewManager` 增加 storage 参数 |
| `internal/memory/manager.go` | 修改 | `CreateSession` 中创建 attachments 目录的 `os.MkdirAll` 调用移除，由 storage `Write` 时按需创建 |
| `internal/memory/manager.go` | 修改 | `SaveAttachment` 由 `os.WriteFile` 改为 `storage.Write`，path 传 `attachmentsDir + filename` 绝对路径 |
| `internal/memory/manager.go` | 修改 | `Cleanup` 中清理过期会话的删除步骤拆为两步：先调 `storage.DeleteDir(attachmentsDir)` 删附件（确保 minio 模式下对象存储里的附件也被清理），再 `os.RemoveAll(sessionDir)` 删会话根目录下的元数据（`history.json`、`chats/`、`SESSION.md`）。local 模式下 attachments 目录已被根目录删除一并清掉，DeleteDir 是 no-op |
| `internal/memory/memory_test.go` | 修改 | 同步调整 `NewManager` 调用，注入 storage 实例 |
| `cmd/groot/main.go` 或 Manager 组装位置 | 修改 | 启动时按 `cfg.Storage` 创建 storage 实例并注入 `Manager` |

**关于 Cleanup 的拆分**：现有 `Cleanup` 一次性 `os.RemoveAll(sessionDir)` 同时删除附件和元数据，是基于"附件就在 session 根目录下"的隐含假设。引入 storage 抽象后这个假设不再成立——minio 模式下附件根本不在文件系统里。因此必须把"删附件"和"删元数据"的责任分开：附件交给 storage，元数据继续由 os 直接处理。这是迁移工作里**唯一一处**触及非附件路径的改动，触及的也只是删除时序，不改元数据本身的读写实现。

**保留不动的部分**：
- `Manager.attachmentsDir(sessionID)`、`GetAttachmentPath` 等路径计算函数保留——它们是 memory 模块对"附件路径长什么样"的内部约定，调用 storage 之前由它们组装绝对路径
- `Manager.SaveAttachment` 的入参签名、返回值不变，调用方零改动
- `internal/attachment/handler.go` 不动——它处理的是 `/chat` 接口的请求级临时暂存（`{memoryDir}/temp/<taskID>/`），生命周期是单次请求，用完即删，不是持久化附件，不归 storage 层管

### 2.2 不在本期范围

以下模块本期不动：

- `internal/schedule/` — 定时任务的 `task.json` / `executions/*.json` 是任务元数据，不是附件
- `internal/filesystem/` — eino skill 专用的 symlink 实现，与本设计无关
- `internal/cmd/skills.go`、`internal/cmd/tail.go` 等的 `os.ReadFile` 调用 — 都是配置/日志读取，不是附件
