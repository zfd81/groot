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

### 2.1 与上一版差异

**上一版**（v1）：MinIO 配置直接写在 `config.yaml` 的 `storage.minio` 节中，与业务配置耦合在一起。

**本次迭代**（v2）：将 MinIO 连接信息从 `config.yaml` 剥离到独立的 `~/.groot/env.yaml` 文件。

**具体改动**：

- **新增**：`init` 子命令同时生成 `~/.groot/env.yaml`（内容全注释），存放 MinIO 等基础设施连接信息。用户取消注释并填值即可启用 MinIO
  - 文件权限为 `0600`（凭据文件应私密，比 `config.yaml` 的 `0644` 严格）
  - 已存在则跳过，避免覆盖用户填好的凭据
- **新增**：`init` 完成后的"下一步"引导新增一步，告知用户在哪开启 MinIO（`vim ~/.groot/env.yaml`），保持默认全注释即等价于本地磁盘存储
- **移除**：`config.yaml` 模板中的 `storage` 注释块（旧版中 MinIO 配置的载体），保留指向 `env.yaml` 的引导注释
- **调整**：`config.Load()` 加载流程
  1. 解析 `config.yaml` 后，强制将 `cfg.Storage.Minio` 置为 `nil`（即便用户在 `config.yaml` 里残留了旧的 `storage.minio` 节也不再生效）
  2. 检测 `~/.groot/env.yaml`，存在且 `minio` 节有效则赋值
- **不变**：`Config`、`StorageConfig`、`MinioConfig` 类型定义不变；`storage.New()` 签名不变；`main.go`、`chat.go` 等调用方零改动

**理由**：将基础设施配置（MinIO 凭据）与业务配置（`config.yaml`）分离，降低凭据泄露风险，便于不同环境使用不同的 `env.yaml`。
