# 存储抽象层设计文档

**日期**：2026-06-06
**状态**：实现稿
**作者**：zfd81 + Kiro

---

## 一、功能设计

### 1.1 功能概述

`internal/storage` 是 groot 的统一文件存储抽象层，为运行时数据（会话历史、附件、调度任务、集群心跳等）提供与底层无关的读写接口。调用方只依赖一套稳定的 `Storage` 接口，无需感知底层是本地磁盘还是 MinIO 对象存储。存储类型在启动时由配置决定，运行期固定不变。

storage 层不做任何路径拼接：调用方传入什么 path，底层就用什么 path。这让 storage 包职责单一，且把"文件存哪、目录怎么分"的决策完全留给调用方。

### 1.2 能力清单

| 方法 | 说明 |
|------|------|
| `Write(ctx, path, r, size, contentType)` | 将数据流写入指定路径；契约保证单文件原子写——目标 path 要么保留旧内容、要么写出完整新内容，不会留下半成品 |
| `Read(ctx, path)` | 以流式返回指定路径的文件内容，调用方负责关闭 |
| `Delete(ctx, path)` | 删除指定路径的单个文件 |
| `DeleteDir(ctx, path)` | 递归删除指定目录及其所有内容 |
| `Stat(ctx, path)` | 返回文件或目录元信息（大小、MIME、修改时间、是否目录） |
| `List(ctx, dir)` | 列出指定目录下的直接子项（不递归），返回元信息列表 |
| `Rename(ctx, src, dst)` | 将 src 重命名为 dst，与 `os.Rename` 一致地同时支持文件和目录；dst 已存在时按"覆盖"语义处理 |

### 1.3 接口定义

```go
type Storage interface {
    Write(ctx context.Context, path string, r io.Reader, size int64, contentType string) error
    Read(ctx context.Context, path string) (io.ReadCloser, error)
    Delete(ctx context.Context, path string) error
    DeleteDir(ctx context.Context, path string) error
    Stat(ctx context.Context, path string) (*FileInfo, error)
    List(ctx context.Context, dir string) ([]*FileInfo, error)
    Rename(ctx context.Context, src, dst string) error
}

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

| 接口 | 错误约定 |
|---|---|
| `Stat` | path 不存在 → `ErrNotFound` |
| `Read` | path 不存在 → `ErrNotFound`；path 是目录 → `ErrIsDir` |
| `Delete` | path 不存在 → `ErrNotFound`；path 是目录 → `ErrIsDir`（与 `Read` 对称） |
| `DeleteDir` | path 不存在视为已删除 → `nil`（与 `os.RemoveAll` 一致） |
| `Rename` | src 不存在 → `ErrNotFound` |
| `Write` | `size >= 0` 时实际写入字节数不等于 `size` → 报错（详见 §1.13） |

两种存储类型在内部将各自底层错误（`os.ErrNotExist`、MinIO `NoSuchKey` / `NoSuchBucket`）统一映射到这两个哨兵错误。其他类型的错误通过 `fmt.Errorf("...: %w", err)` wrap 后返回，保留调用栈。

minio 实现中专门定义了 `isNotExist(err)`：判断 `errors.As` 到 `minio.ErrorResponse` 后 Code 是否为 `NoSuchKey` / `NoSuchBucket`。

### 1.5 path 约定

storage 层不做任何路径拼接，调用方传入什么就用什么。两种类型对 path 的语义不同：

- **local 类型**：path 必须是**文件系统绝对路径**（如 `/Users/zhangfengda/.groot/memory/sessions/abc/x.pdf`）。`Local.ensureAbs` 强制校验，传入相对路径返回 `storage: path must be absolute, got %q`，避免依赖进程 cwd 导致的路径漂移
- **minio 类型**：path 直接作为 **object key** 使用（如 `sessions/abc/x.pdf`），不做任何前缀处理

调用方负责：
- 决定文件物理放在哪里（如 attachment handler 决定附件位置）
- 在 local 模式下传入绝对路径
- 在 minio 模式下决定 object key 命名规则

### 1.6 环境配置文件（独立于 config.yaml）

MinIO 连接信息独立存放于 `$GROOT_HOME/env.yaml`（默认 `~/.groot/env.yaml`），不与 `config.yaml` 耦合：

- 文件不存在 → 使用 local 类型（零配置）
- 文件存在但 `minio` 节为空/未配置 → 使用 local 类型
- 文件存在且 `minio` 节配置了**全部 4 个必填字段** → 使用 minio 类型

#### 1.6.1 必填字段

`factory.New(cfg)` 在 minio 节存在时校验以下字段，缺一报错：

| 字段 | 报错措辞 |
|---|---|
| `endpoint` | `storage: minio.endpoint is required` |
| `bucket` | `storage: minio.bucket is required` |
| `access_key` | `storage: minio.access_key is required (set directly or via ${ENV_VAR})` |
| `secret_key` | `storage: minio.secret_key is required (set directly or via ${ENV_VAR})` |

`use_ssl` 是布尔可选项（默认 false）。

#### 1.6.2 env.yaml 模板

`init` 子命令在 `~/.groot/` 下生成 `env.yaml`，内容**全注释**：

```yaml
# Groot 基础设施环境配置
# 存放 MinIO 等外部服务的连接凭据，与业务配置 (config.yaml) 解耦。

#minio:
#  endpoint: localhost:9000          # MinIO 服务地址（host:port）
#  access_key: ${MINIO_ACCESS_KEY}   # 访问密钥（建议使用环境变量）
#  secret_key: ${MINIO_SECRET_KEY}   # 密钥（建议使用环境变量）
#  bucket: groot                     # 存储桶名称
#  use_ssl: false                    # 是否启用 HTTPS
```

模板使用"先缩进后 #"格式（如 `#  endpoint:`），用户删掉行首 `#` 后 yaml 缩进自动正确。新增字段请遵循同样格式。

#### 1.6.3 config.yaml 模板不再生成 storage 节

`init` 生成的 `config.yaml` 中**不再包含** `storage:` 节，凭据完全由 env.yaml 维护。

`Config.Storage`（`StorageConfig`）类型定义保留，因 `cfg.Storage.Minio` 是 storage 工厂的入口字段；但 config.yaml 中的 `storage:` 节即便用户手动添加也不会生效（loadEnvFile 强制置 nil）。

### 1.7 加载机制

MinIO 配置仅通过 `env.yaml` 注入，`config.Load()` 内部自动处理，对外透明。完整加载顺序：

1. **解析 config.yaml**：`yaml.Unmarshal(data, cfg)` 把用户主配置加载到 `cfg`
2. **`loadEnvFile(cfg, homeDir)`**：
   - 入口先 `cfg.Storage.Minio = nil`，确保 config.yaml 残留的 storage.minio 节不生效
   - 读 `~/.groot/env.yaml`：不存在 → 保持 nil；存在 → `yaml.Unmarshal` 后取出 `minio` 节赋给 `cfg.Storage.Minio`
   - **本步不做环境变量展开**，仅按 yaml 原样注入
3. **`applyDefaults(cfg)`**：填充其他模块默认值
4. **`expandConfigEnvVars(cfg)`**：集中展开 `${ENV_VAR}` 占位符
   - LLM Models 的 `APIKey`
   - Security.Auth APIKey 的 `Key`
   - **`Storage.Minio.AccessKey` 与 `SecretKey`**（仅这两个字段）

#### 1.7.1 env 展开范围

`expandConfigEnvVars` 对 storage.minio 仅展开 `access_key` 与 `secret_key` 两个字段；`endpoint` / `bucket` / `use_ssl` 原样保留。这是有意的——凭据通常通过 env 注入避免硬编码到 yaml，而 endpoint / bucket 通常是稳定字符串。

#### 1.7.2 加载机制要点

- env.yaml 的展开发生在 `expandConfigEnvVars` 阶段，**不在 `loadEnvFile` 中**（loadEnvFile 顶部注释明确）
- 把展开与读取解耦，避免 env.yaml 为非法格式时的报错混在 ExpandEnv 错误里
- `Config`、`StorageConfig`、`MinioConfig` 类型定义不变，`storage.New()` 签名不变

### 1.8 目录结构

```
internal/storage/
├── storage.go    # Storage 接口、FileInfo 类型、ErrNotFound / ErrIsDir
├── local.go      # 本地磁盘存储实现
├── minio.go      # MinIO 存储实现
└── factory.go    # 根据 StorageConfig 创建对应存储类型实例
```

### 1.9 流式设计

接口选用 `io.Reader` / `io.ReadCloser` 而非 `[]byte`：

- HTTP multipart 上传本身是流，`Write` 可直接透传 `r` 给底层
- HTTP 下载响应 `Read` 返回的 `ReadCloser` 可直接 `io.Copy` 到 `http.ResponseWriter`，全程零拷贝

### 1.10 服务场景

| 场景 | 调用方法 | path 形态（local 模式） |
|------|---------|-----------------------|
| `/chat` 附件落地 | `Write` | `~/.groot/memory/sessions/<id>/attachments/<file>` |
| 定时任务清 session | `DeleteDir` | `~/.groot/memory/sessions/<id>/attachments/` |
| 读附件列表 | `List` | `~/.groot/memory/sessions/<id>/attachments/` |
| 读附件内容 | `Read` | `~/.groot/memory/sessions/<id>/attachments/<file>` |
| schedule 状态搬迁 | `Rename` | `~/.groot/schedules/active/{id}` → `~/.groot/schedules/disabled/{id}` |
| cluster 心跳写注册 | `Write` | `~/.groot/cluster/members/{regID}` |
| cluster 列成员 | `List` | `~/.groot/cluster/members/` |

minio 模式下，path 替换为对应 object key（如 `sessions/<id>/attachments/<file>`）。

### 1.11 contentType 处理的实现差异

`Write` 的 `contentType` 参数为空时：

- **local 实现**：文件系统不存储 ContentType 元数据，Stat / List 时按文件扩展名推断（`mime.TypeByExtension`）；扩展名为空时返回空字符串
- **minio 实现**：S3 协议要求 HTTP `Content-Type` header 必须存在，minio-go 兜底成 `application/octet-stream`，无法保留为空

调用方如果关心跨实现的 ContentType 一致性，应该在 `Write` 时显式传入正确的 ContentType。

### 1.12 Rename 实现差异与故障路径

`Rename` 同时支持文件和目录（与 `os.Rename` 一致）。两种存储类型上的实现差异较大。

| 实现 | 底层操作 | 原子性 | 说明 |
|---|---|---|---|
| local | `os.Rename(src, dst)`（先 `MkdirAll(dir(dst))`） | ✅ 同文件系统下原子 | 标准 POSIX rename，文件 / 空目录均原子 |
| minio（文件） | 清 dst 残留 → `CopyObject(src, dst)` → `RemoveObject(src)` | ❌ 非原子 | 服务端复制走 `x-amz-copy-source` 头，不下载数据；Copy 成功后删源 |
| minio（目录） | Phase 0：清 dst 残留 → Phase A：`ListObjects(prefix=src/, recursive=true)` 枚举 → 逐个 `CopyObject` 到 dst/...；任一失败则回滚已 Copy 的 dst → Phase B：全部 Copy 成功后逐个 `RemoveObject(src/...)` | ❌ 非原子 | 对象存储无目录概念，按"前缀子集搬迁"实现 |

minio 实现的两种形态都不是真正的原子操作，进程崩溃可能让 src 与 dst 共存。实现侧通过补偿逻辑让**语义尽量接近原子**——失败可通过明确的恢复策略收敛——但业务层（如 schedule 的 `MoveTask`）仍需保证调用幂等。

#### 1.12.1 文件级 Rename 的补偿流程

`Minio.renameFile`：

1. **判别文件还是目录**：`StatObject(src)` 命中即走文件分支
2. **清 dst 残留**（`cleanupDstObject`）：`StatObject(dst)` 命中则 `RemoveObject(dst)`；不存在视为正常返回 nil
3. **`CopyObject(src → dst)`**：服务端复制
4. **`RemoveObject(src)`**：删源；若失败，尽力回滚（`RemoveObject(dst)` 删除已 Copy 的 dst）后向上返回错误

故障路径恢复表：

| 故障点 | 结果 | 恢复方式 |
|---|---|---|
| src 不存在 | 返回 `ErrNotFound` | 调用方无需重试 |
| dst 残留清理失败 | 返回错误，src 未动 | 调用方重试 |
| Copy 失败 | src 存在，dst 不存在 | 调用方直接重试 |
| Copy 超时（实际成功） | src 存在，dst 可能存在 | 重试时步骤 2 清残留 |
| Delete src 失败 + 回滚成功 | src 存在，dst 不存在 | 调用方重试 |
| Delete src 失败 + 回滚失败 | src 与 dst 各一份 | 业务层幂等兜底 |
| 进程崩溃（任意步骤间） | src 与 dst 状态不可知 | 业务层幂等兜底 |

`CopyObject` 返回成功即表示 dst 已存在（S3 协议契约保证）。

#### 1.12.2 目录级 Rename 的补偿流程

`Minio.renameDir`：

1. **判别**：`StatObject(src)` 不命中再 `ListObjects(prefix=src+"/", MaxKeys=1)`，命中即走目录分支；都不命中返回 `ErrNotFound`
2. **Phase 0 — 清 dst 残留**：先 `cleanupDstObject(dst)` 删裸对象，再 `RemoveObjectsRecursive(dstPrefix)` 兜底清理 dst 前缀。同时兜两种形态（dst 是裸对象 / dst 是同名前缀），避免上一次中断遗留的脏数据让本次 Copy 出现新旧混存
3. **Phase A — 全量 Copy**：`ListObjects(prefix=srcPrefix, recursive=true)` 枚举所有 key；逐个 `CopyObject` 到 dstPrefix。任一对象 Copy 失败 → 回滚已 Copy 的 dst 子对象 → 返回错误，src 完整
4. **Phase B — 全量 Delete**：所有 Copy 完成后，逐个 `RemoveObject(srcKey)`。失败不回滚（dst 已是权威新位置），向上返回错误，让业务层幂等扫描兜底

故障路径恢复表：

| 故障点 | 结果 | 恢复方式 |
|---|---|---|
| src 不存在（无对象、无前缀命中） | 返回 `ErrNotFound` | 调用方无需重试 |
| dst 残留清理失败 | 返回错误，src 未动 | 调用方重试 |
| Phase A 中某次 Copy 失败 + 回滚成功 | src 完整，dst 已清空 | 调用方重试 |
| Phase A 中 Copy 失败 + 回滚失败 | src 完整，dst 残留若干已 Copy 对象 | 调用方重试时 Phase 0 兜底清理 |
| Phase A 完整成功，Phase B 全部失败 | src 与 dst 各一份完整副本 | 调用方重试时 Phase 0 清 dst 后重新 Copy + Delete；或业务层幂等扫描收敛 |
| Phase B 中某次 Delete 失败 | src 残留部分对象，dst 已是权威完整副本 | 业务层幂等扫描收敛（双份共存时优先信任 dst） |
| 进程崩溃（任意步骤间） | src 与 dst 状态不可知 | 业务层幂等兜底 |

收敛原则：**只要 dst 完整存在，业务侧应当以 dst 为权威**；src 残留视为"待清理"。Phase A 成功之后，dst 一定已是完整副本，Phase B 是"可重入的清理"，不再影响业务正确性。

### 1.13 Write 单文件原子写

`Write` 的契约要求单文件原子——目标 path 要么保留旧内容、要么写出完整新内容。

| 实现 | 底层操作 | 原子性来源 |
|---|---|---|
| local | 同目录 `<path>.tmp` 写入 → `f.Sync()` → `os.Rename(<path>.tmp, <path>)` | POSIX `rename(2)` 是目录项的原子替换 |
| minio | 单次 `PutObject` | S3 协议契约保证 PUT 要么完整写入、要么不可见 |

local 实现细节由 `internal/storage/local.go` 自治承担，业务层完全感知不到 `.tmp`：

1. `os.MkdirAll(filepath.Dir(path), 0755)` —— 按需建目录
2. **入口兜底清理孤儿 tmp**：`os.Remove(<path>.tmp)`（忽略 `ErrNotExist`），消除上一次崩溃的残留
3. 打开 `<path>.tmp`，`O_WRONLY|O_CREATE|O_TRUNC`，0644
4. `io.Copy(f, r)` → `n` 字节
5. `f.Sync()` 把数据落到磁盘
6. `f.Close()`
7. **size 严格校验**：`size >= 0` 时必须 `n == size`，不一致 → 删 tmp 后报错 `storage: write %s: declared size %d but wrote %d bytes`
   - `size < 0` 表示长度未知（minio 走分片上传场景），跳过校验
8. `os.Rename(<path>.tmp, <path>)` 完成原子替换

任一步骤失败都先 `os.Remove(<path>.tmp)` 后向上返回错误。`<path>.tmp` 与 `<path>` 同目录，必然在同一文件系统下，`os.Rename` 满足原子前提。`f.Sync()` 不可省略——否则崩溃时新数据仍可能停留在 page cache，rename 后看到的是空文件。

故障路径恢复表（local Write）：

| 故障点 | path 状态 | tmp 状态 | 恢复方式 |
|---|---|---|---|
| MkdirAll 失败 | 不变 | 不变 | 调用方修复目录权限后重试 |
| 入口清理 tmp 失败（非 NotExist） | 不变 | 残留 | 调用方重试 |
| OpenFile tmp 失败 | 不变 | 不变 | 调用方重试 |
| io.Copy 出错 | 不变 | 已自动 `os.Remove` | 调用方重试 |
| Sync / Close / size 校验失败 | 不变 | 已自动 `os.Remove` | 调用方重试 |
| Rename 失败 | 不变 | 已自动 `os.Remove` | 调用方重试 |
| 进程崩溃（rename 之前） | 与调用前一致 | 可能残留 `.tmp` | 下次同 path Write 入口处自动清理 |
| 进程崩溃（rename 期间或之后） | 已是新内容 | 通常已被 rename 消除 | 下次同 path Write 入口处自动清理 |

这条契约让业务模块（`memory.saveHistory` / `SaveChatRecord`、`schedule.SaveTask` 等）**不再需要自己写 tmp + rename**——所有原子性保证下沉到 `Storage.Write` 实现内部。

### 1.14 MinIO 启动 fail-fast 探活

`NewMinio(...)` 在 minio-go 客户端构造完后调用 `newMinioWithClient(client, bucket)` 执行三步探活，任一步失败立即返回 error，避免运行时第一次 Write 才暴露：

| 步骤 | 操作 | 验证 |
|---|---|---|
| 1 | `BucketExists(bucket)` | bucket 存在且账号有访问权限 |
| 2 | `PutObject(bucket, "__startup/probe-{UnixNano}", "", 0, ContentType="application/octet-stream")` | 写权限 |
| 3 | `RemoveObject(bucket, probeKey)` | 删权限并清理探针痕迹 |

每步独立 **10 秒 timeout** 包裹（`context.WithTimeout`），避免单次网络 hang 拖死启动。

#### 1.14.1 保留前缀

`__startup/` 是 storage 层保留的探活前缀。约定：

- 所有探针对象写到 `__startup/probe-{nanos}` 形态，正常关闭流程会立即清理
- 业务层不应把任何数据放在 `__startup/` 前缀下
- 进程崩溃留下的孤儿探针文件不影响功能（被覆盖或随 bucket 清理消除）

#### 1.14.2 错误包装格式

| 阶段 | 错误模板 |
|---|---|
| BucketExists 失败 | `storage: minio probe bucket %s: %w` |
| BucketExists 返回 false | `storage: minio probe bucket %s: bucket does not exist` |
| PutObject 失败 | `storage: minio probe put %s: %w`（`%s` 为 probeKey） |
| RemoveObject 失败 | `storage: minio probe remove %s: %w` |

### 1.15 MinIO 实现的若干细节

#### 1.15.1 Read 先 Stat 兜底

minio-go 的 `GetObject` 是延迟执行的，错误要等到第一次 Read/Stat 调用才暴露。`Minio.Read` 入口先 `StatObject`，命中 `isNotExist` → 立即返回 `ErrNotFound`，不返回 ReadCloser。

#### 1.15.2 Delete 先 Stat 补 ErrNotFound 语义

S3 `RemoveObject` 对不存在的 key 是 noop（idempotent delete）。为符合 `ErrNotFound` 接口语义，`Minio.Delete` 入口先 `StatObject`，不存在直接返回 `ErrNotFound`；然后 `RemoveObject`。这增加一次 RTT，但保证语义一致。

#### 1.15.3 DeleteDir 行为

`prefix := strings.TrimSuffix(path, "/") + "/"` 后调用 `RemoveObjectsRecursive`：

- 内部 `ListObjects(prefix, recursive=true)` 枚举 → 喂给 `RemoveObjects` 批量删
- 前缀为空（无对象） → 自然返回 nil（幂等）

#### 1.15.4 Stat 对目录前缀的 fallback

`Minio.Stat` 接收的 path 可能是裸对象，也可能是"目录前缀"（业务调用方按 POSIX 风格命名）。逻辑：

1. `StatObject(path)` 成功 → 返回 `IsDir=false` 的 `FileInfo`
2. `StatObject` 返回 `isNotExist`：
   - `ListObjects(prefix=path+"/", MaxKeys=1)` 探测前缀
   - 命中 → 返回 `&FileInfo{Path: path, IsDir: true}`，**Size=0、ContentType=""、ModTime=零值**（前缀没有真实元数据，避免泄漏 1970-01-01 等假数据）
   - 不命中 → 返回 `ErrNotFound`
3. 其他错误 → wrap 后透传

#### 1.15.5 List 对 CommonPrefix 的处理

`Minio.List` 调用 `ListObjects(prefix=dir+"/", recursive=false)`。返回的对象包含两种：

- **裸对象**（key 不以 `/` 结尾）：`isDir = false`，所有元数据按 minio 返回值填充
- **CommonPrefix**（key 以 `/` 结尾，对应 S3 的前缀分组）：`isDir = true`，**显式置零** Size / ContentType / ModTime（与 §1.15.4 一致），避免业务把 1970-01-01 当成真实 mtime

返回值统一是 `[]*FileInfo`，`out := make([]*FileInfo, 0)` 保证空集合也是空切片不是 nil。

### 1.16 实现汇总（code 视角）

#### 1.16.1 Local

- 编译期 `var _ Storage = (*Local)(nil)` 断言
- 所有方法入口 `ensureAbs(path)`
- `Stat` / `List` 通过 `mime.TypeByExtension` 推断 ContentType
- `List` 中 `e.Info()` 返回 `os.ErrNotExist`（并发删除竞态）→ 跳过该项继续，不报错
- `DeleteDir` 直接走 `os.RemoveAll`（与 `os.RemoveAll` 一致：不存在视为已删除）

#### 1.16.2 Minio

- 内部抽象 `minioAPI` 接口便于单元测试 mock
- `minioClient` 包装 `*minio.Client`
- `RemoveObjectsRecursive`：自实现 `goroutine + chan` 把 `ListObjects` 的输出喂给 `RemoveObjects`，捕获 List 与 ctx 取消错误
- `objectInfoToFileInfo` 把 `minio.ObjectInfo` 转 `*FileInfo`（`ModTime` 取 `LastModified`）

#### 1.16.3 factory

- `cfg.Minio == nil` → `NewLocal()`（local 模式）
- 4 个必填字段任一缺失 → 报错（§1.6.1）
- 全部就位 → `NewMinio(...)` 触发 §1.14 探活

## 二、迭代说明

### 2.1 与上一版差异

#### 接口契约升级

- **§1.4 错误约定补全**：`Delete` 对目录返回 `ErrIsDir`（与 `Read` 对称）；`DeleteDir` 不存在返回 nil（与 `os.RemoveAll` 一致）
- **§1.13 Write size 严格校验**：`size >= 0` 时实际写入字节数必须等于 `size`，不一致删 tmp 后报错；`size < 0` 表示长度未知，跳过校验

#### MinIO 启动探活

- **§1.14 新增**：`NewMinio` 启动 fail-fast 三步探活（BucketExists / PutObject / RemoveObject）
- **§1.14.1**：保留前缀 `__startup/probe-{ts}`
- **§1.14.2**：每步独立 10 秒 timeout
- **§1.14.3**：错误包装模板

#### MinIO 实现细节补全

- **§1.15.1**：`Read` 先 Stat 兜底（弥补 minio-go 延迟错误）
- **§1.15.2**：`Delete` 先 Stat 补 `ErrNotFound` 语义
- **§1.15.4**：`Stat` 对目录前缀的 ListObjects fallback；返回 `IsDir=true` 的 FileInfo，Size/ContentType/ModTime 显式置零
- **§1.15.5**：`List` 对 CommonPrefix（目录条目）显式置零 Size/ContentType/ModTime，避免泄漏 1970-01-01 等假数据

#### env.yaml 加载机制澄清

- **§1.6.1 必填字段表**：列出 4 个必填字段（endpoint / bucket / access_key / secret_key）与对应报错措辞
- **§1.6.3**：明确 `init` 生成的 `config.yaml` 不再包含 `storage:` 节
- **§1.7 加载顺序修正**：明确"展开发生在 `expandConfigEnvVars` 阶段，loadEnvFile 不做展开"
- **§1.7.1**：env 展开**仅作用于 access_key / secret_key**，endpoint / bucket / use_ssl 原样保留

#### Rename 文件 + 目录两个分支（保留）

- §1.12.1 文件级补偿流程
- §1.12.2 目录级补偿流程（Phase 0 / Phase A / Phase B）
