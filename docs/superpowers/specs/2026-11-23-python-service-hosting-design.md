# Python 服务宿主与 ASR 语种识别服务设计文档

## 一、功能设计

### 1.1 功能概述

Groot 在运行期托管一组以 Python 编写的本地服务。服务放置在 `{GROOT_HOME}/services/<服务名>/` 目录中，Groot 启动时扫描这些目录，按统一协议拉起对应的 Python 子进程，并在自己唯一的对外端口上以 `/services/<服务名>/...` 路径暴露这些服务的能力。

调用方访问这些接口时使用与 Groot 其他接口完全相同的 API Key。请求经过相同的验签、吊销检查、权限点校验与限流，然后由 Groot 代理到本地 Python 进程。Python 服务不直接对外，不实现面向调用方的鉴权，也无法通过网络被其他主机访问。

服务与 Groot 实例一对一所属：每个 Groot 工作进程只托管由它自己拉起的子进程，Groot 退出或重启时这些子进程随之结束，不同实例之间不共享 Python 进程。

第一个托管服务是 `asr-router`，承担语音语种识别：接收一段音频，识别其语种，返回语种代码、置信度与候选语种列表。语种识别结果供上层应用自行决定后续调用哪个 ASR 模型；Groot 与本服务都不做「语言到 ASR 模型」的映射。语种识别后端在 v1 采用 FireRedLID，后端实现封装在服务内部，替换后端不改变对外协议，也不改动 Groot。

该能力解决的问题：

- 语音、视觉、语言识别等模型以 Python 生态为主，无法在 Go 进程内直接加载使用。
- 逐次命令行调用需要重复加载模型权重，启动耗时与内存开销不可接受。
- 若每个 Python 服务各自监听端口并各自实现鉴权，会产生多端口暴露、重复鉴权逻辑与分散的密钥管理。
- 上层应用需要一个稳定的语种识别接口，接口不应随底层 LID 模型的更换而变化。

### 1.2 能力清单

| 能力 | 说明 |
|---|---|
| 目录发现 | 扫描 `{GROOT_HOME}/services/*/`，识别符合标准结构的 Python 服务 |
| 生命周期托管 | 拉起、就绪握手、崩溃重启、优雅停止、Groot 退出时级联结束 |
| 单端口暴露 | 所有服务通过 Groot 的对外端口以 `/services/<服务名>/...` 访问 |
| 统一鉴权 | 复用 API Key 验签、吊销检查、权限点与限流，不新增鉴权实现 |
| 身份透传 | 向服务注入调用方、租户、用户、请求 ID，服务无需解析 API Key |
| 内部令牌 | 每个服务进程、每次启动生成独立令牌，仅 Groot 与该子进程知道 |
| 流式代理 | 请求体与响应体流式转发，不整体缓冲，支持大文件上传与流式响应 |
| 进程隔离 | Python 崩溃不影响 Groot；单个服务不可用不影响其他服务 |
| 多实例共存 | 同一主机可运行多个 Groot 实例，各自拉起并管理自己的服务进程 |
| 跨平台 | Linux、macOS、Windows 使用同一套协议 |
| 语种识别 | `POST /services/asr-router/detect`，返回 `language`、`confidence` 与候选列表 |
| 后端可替换 | LID 后端抽象为服务内部接口，v1 实现为 FireRedLID |

### 1.3 模块边界

| 模块 | 职责 |
|---|---|
| Groot 宿主（`internal/pysvc`） | 发现服务、解析清单、拉起与监督子进程、就绪握手、反向代理、权限解析、日志收编 |
| Groot 认证中间件 | 对 `/services/...` 执行与其他接口一致的 API Key 鉴权、权限点与限流 |
| Python SDK（`groot_service_sdk`） | 标准启动器、令牌校验、环回地址绑定、就绪上报、身份头解析、统一错误信封、健康端点 |
| Python 服务（`services/asr-router`） | 业务逻辑、模型加载与推理、业务路由 |
| 上层应用 | 根据返回的语种自行选择 ASR 模型，再调用 Groot 的转录接口 |

Groot 不感知服务的业务语义，只处理进程与 HTTP。SDK 不包含业务逻辑，只实现宿主与服务的公共协议。服务作者只编写业务路由。

### 1.4 服务目录与识别标准

#### 1.4.1 目录结构

```
{GROOT_HOME}/services/<服务名>/
├── service.yaml          # 可选；只承载授权与超时策略
├── requirements.txt      # 必须；业务依赖清单
├── app/
│   ├── __init__.py
│   └── main.py           # 必须暴露名为 app 的 ASGI 对象
├── .venv/                # 可选；存在时优先使用，否则使用共享环境
└── models/               # 可选；模型权重等运行资源
```

Windows 上 `{GROOT_HOME}` 为 `%USERPROFILE%\.groot`。

#### 1.4.2 固定约定

以下内容为标准，不通过配置改变：

| 项 | 固定值 |
|---|---|
| 服务名 | 目录名；必须匹配 `[a-z0-9][a-z0-9_-]*` |
| 对外路径 | `/services/<目录名>/...` |
| 入口 | `app/main.py` 中的 `app` 对象 |
| 解释器 | 服务目录存在 `.venv` 时用 `<服务目录>/.venv/{bin/python 或 Scripts/python.exe}`，否则用 `{GROOT_HOME}/venv/...` |
| 启动命令 | `<解释器> -m groot_service_sdk.launcher` |
| 工作目录 | 服务目录 |
| 令牌传递 | stdin 首行 |
| 就绪上报 | stdout 结构化就绪行 |
| 健康端点 | `GET /healthz`，由 SDK 提供 |

#### 1.4.3 共享 Python 环境

所有服务默认共用 `{GROOT_HOME}/venv`，与操作系统 Python 隔离。服务作者自行为该环境安装依赖：

```
python3 -m venv {GROOT_HOME}/venv
{GROOT_HOME}/venv/bin/pip install -r {GROOT_HOME}/services/<服务名>/requirements.txt
```

Windows 对应 `{GROOT_HOME}\venv\Scripts\python.exe`。

单个服务目录内如存在自己的 `.venv`，Groot 自动改用该环境，无需配置。这一后路用于个别服务依赖冲突时做隔离。

Groot 不执行 `pip install`，不下载模型权重，不创建或修复虚拟环境。

#### 1.4.4 识别与校验

1. Groot 扫描 `{GROOT_HOME}/services/` 的直接子目录，忽略以 `.` 开头的目录。
2. 目录名不符合命名规则、缺少 `app/main.py`、`app/main.py` 缺失 `app` 对象的目录不视为服务。
3. 服务名重复（目录名在大小写不敏感文件系统上的冲突）视为配置错误，只保留一个并记录警告。
4. `requirements.txt` 缺失时该服务被禁用，记录错误日志。
5. 解释器不存在时该服务被禁用，记录错误日志，并提示创建环境的命令。
6. `service.yaml` 字段非法时该服务被禁用，记录错误日志；不影响 Groot 启动与其他服务。

`app/main.py` 是否真的暴露了可用的 `app` 对象，由启动时的导入结果判定：导入失败表现为进程退出，错误输出进入 Groot 日志。

### 1.5 运行时协议

#### 1.5.1 启动顺序

1. Groot 在创建 HTTP 服务器之前完成服务扫描，得到服务名、权限点与就绪状态，用于路由注册与权限解析。
2. HTTP 服务器开始监听后，Groot 异步启动各服务，不阻塞对外端口就绪。
3. 对每个服务：
   1. 解析解释器路径。
   2. 生成 32 字节随机令牌，十六进制编码。
   3. 创建 stdin、stdout、stderr 三条管道。
   4. 以服务目录为工作目录执行 `<解释器> -m groot_service_sdk.launcher`，环境变量继承 Groot 进程，并设置 `PYTHONPATH={GROOT_HOME}/sdk/<sdk版本>`、`PYTHONUNBUFFERED=1`。
   5. 向 stdin 写入令牌一行，保留写端不关闭。
   6. 读取 stdout，等待就绪行；同时把其余 stdout/stderr 行写入 Groot 日志。

#### 1.5.2 就绪握手

Python SDK 启动器完成以下动作后向 stdout 输出一行就绪消息：

1. 从 stdin 读取第一行作为内部令牌，随后启动后台线程继续读 stdin。
2. 创建 TCP socket 并绑定 `127.0.0.1:0`，由内核分配端口。
3. 以该已绑定 socket 启动 uvicorn。
4. 等待 ASGI lifespan 启动完成（服务可在此加载模型）。
5. 输出就绪行。

就绪行格式：

```
GROOT_SERVICE_READY {"port":53124,"pid":1234,"sdk":"1.0.0"}
```

Groot 在 `startup_timeout` 内未读到就绪行、或进程提前退出，判定启动失败。端口只来自该就绪行，Groot 不解析 uvicorn 日志、不预先选端口。

绑定地址由 SDK 固定为 `127.0.0.1`，不接受服务配置覆盖，不解析 `localhost`，不允许 `0.0.0.0`。

#### 1.5.3 生命线

stdin 管道同时是父进程生命线。Groot 写完令牌后保持写端打开；Python 的后台线程持续读取，读到 EOF 即说明 Groot 已退出或崩溃，启动器随即触发优雅停止并退出。

该机制覆盖三平台，不依赖 Linux `PR_SET_PDEATHSIG` 或 Windows Job Object，避免 Groot 被强制杀死后留下孤儿进程。

#### 1.5.4 令牌与身份头

Groot 代理请求时：

1. 无条件删除客户端携带的全部 `X-Groot-*` 请求头。
2. 写入以下请求头：

| 请求头 | 内容 |
|---|---|
| `X-Groot-Internal-Token` | 本服务进程本次启动的令牌 |
| `X-Groot-Caller` | API Key 名称 |
| `X-Groot-Tenant-Id` | API Key 所属租户 |
| `X-Groot-User-Id` | 请求头解析出的用户标识，可能为空 |
| `X-Groot-Request-Id` | 本次请求的随机 ID，用于两端日志关联 |
| `X-Groot-Service` | 服务名 |

Python SDK 对除 `/healthz` 外的全部请求校验 `X-Groot-Internal-Token`，使用常数时间比较；不匹配一律返回 401 错误信封，且校验发生在路由解析与请求体读取之前。

令牌不写入命令行参数、环境变量、磁盘或日志。每次服务启动重新生成，旧进程退出后旧令牌立即失效。

#### 1.5.5 停止

1. Groot 关闭该服务的 stdin 写端，触发 SDK 的生命线退出。
2. 同时发送终止信号（Linux/macOS 为 `SIGTERM`，Windows 走进程终止）。
3. 等待 `shutdown_timeout`（默认 10 秒），超时强制杀死。
4. Groot 自身关闭时，先停止接受新请求，再依次停止全部服务。

#### 1.5.6 重启策略

- 进程非正常退出、启动超时、就绪后异常退出，均按退避序列重启：1s、2s、4s、8s、16s、30s，之后保持 30s。
- 存活时间少于 60 秒的退出计为快速失败。连续快速失败达到 10 次后停止重启，服务保持不可用，直到 Groot 下次启动。
- 每次重启生成新令牌、新端口；旧代理目标随之替换。

### 1.6 HTTP 网关

#### 1.6.1 路由

- 每个已发现的服务注册一组路由：`/services/<服务名>/*path`。
- 转发时去掉 `/services/<服务名>` 前缀，其余路径、查询串、方法、请求体原样转发。例如 `POST /services/asr-router/detect` 转发为 `POST /detect`。
- 路径中若出现 `..` 或解码后越界的路径段，返回 400 错误信封，不转发。
- 未发现的服务名不注册路由，由现有兜底处理器返回 404 错误信封。

#### 1.6.2 鉴权与权限点

`/services/...` 与 `/chat` 等接口注册在同一鉴权分组，完整复用：

1. Web 会话 Cookie 或 Bearer API Key 校验。
2. JWT 验签与按 `jti` 回表确认 Key 未删除（吊销即时生效）。
3. 权限点校验。
4. 限流。

权限点由服务的 `service.yaml` 声明；未声明时取 `all`。权限点取值限定为现有已知集合，非法值使该服务被禁用。`asr-router` 声明 `chat`，与转录、合成、图片解析一致，不新增权限点。

认证中间件的路径到权限点映射引入可注入的解析器：宿主实现 `/services/<服务名>` 的查询，其余路径沿用现有静态规则。

#### 1.6.3 转发细节

| 项 | 行为 |
|---|---|
| 请求体 | 以流方式转发，不解析表单，不整体读入内存 |
| 响应体 | 以流方式回写，不缓冲 |
| 逐跳头 | 移除 `Connection`、`Keep-Alive`、`Transfer-Encoding`、`Upgrade` 等 |
| 上游地址 | `127.0.0.1:<就绪行中的端口>` |
| 首字节超时 | `request_timeout`，默认 120 秒，覆盖连接与等待响应头 |
| 响应体传输 | 首字节到达后不再设总时长上限；客户端断开即取消上游请求 |
| 请求 ID | 无客户端传入的关联 ID 时由 Groot 生成 |

#### 1.6.4 错误映射

| 场景 | 状态码 | 错误码 |
|---|---|---|
| 服务名不存在 | 404 | `invalid_request` |
| 服务已发现但未就绪 / 重启中 | 503 | `service_unavailable` |
| 连接被拒、上游读写失败 | 502 | `upstream_error` |
| 首字节超时 | 504 | `upstream_error` |
| 服务自身返回的错误 | 原状态码原响应体 | 由服务决定 |

SDK 生成的服务端错误使用与 Groot 相同的 OpenAI 错误信封结构，保证调用方只需一套解析逻辑。`errbody` 新增错误码 `service_unavailable`。

### 1.7 多实例与平台

#### 1.7.1 实例归属

- 服务子进程由 Groot 工作进程直接创建，一对一归属。受监督模式下只有工作进程拉起服务，监督进程不加载业务资源。
- 服务进程不跨实例复用。同一主机运行多个 Groot 实例时，每个实例拥有自己的子进程、令牌与端口。
- 不同 `GROOT_HOME` 的实例完全隔离：服务代码、虚拟环境、模型权重各自独立。
- 共用同一 `GROOT_HOME` 的实例共享服务代码、共享虚拟环境与模型文件（运行期只读），但各自加载模型、各自占用内存或显存。
- 每个服务绑定 `127.0.0.1:0`，端口由内核分配，实例之间不会冲突。
- 日志中的每条服务输出带服务名与子进程 PID；接入集群时附带 `reg_id`。

#### 1.7.2 按实例启停

`bootstrap.yaml` 提供实例级覆盖：

```yaml
services:
  enabled: true          # 默认 true；false 时本实例不启动任何服务
  disable: [asr-router]  # 本实例额外跳过的服务
```

该配置只影响启动行为，改动后需重启。清单中的 `enabled` 是全局默认，实例级 `disable` 在其之上做减法。

#### 1.7.3 平台差异

| 平台 | 实现 |
|---|---|
| Linux / macOS | 同一协议；解释器路径 `.venv/bin/python` |
| Windows | 同一协议；解释器路径 `.venv\Scripts\python.exe`；进程终止与超时行为由 Groot 统一封装 |

三平台都使用 TCP 环回与内部令牌，不使用 Unix domain socket，避免传输方式分叉。

### 1.8 故障处理

| 故障 | 行为 |
|---|---|
| 清单非法、目录结构不完整 | 跳过该服务，记录错误；其他服务与 Groot 正常 |
| 虚拟环境或解释器缺失 | 跳过该服务，日志给出创建环境的命令 |
| 模型加载失败、导入失败 | 进程退出，Groot 记录 stderr，按退避重启 |
| 启动超时 | 杀死进程，按退避重启 |
| 运行中崩溃 | 按退避重启，新端口、新令牌 |
| 连续快速失败 10 次 | 停止重启，服务不可用直到 Groot 重启 |
| 单个服务不可用 | `/services/<该服务>/...` 返回 503；其他服务与 Groot 其他接口不受影响 |
| 请求超时 | 返回 504；上游请求被取消 |
| 客户端断开 | 取消上游请求，不继续消耗推理资源 |
| Groot 被 `kill -9` | 子进程读到 stdin EOF 后自行退出 |

### 1.9 Python SDK

SDK 以 Python 源码形式嵌在 Groot 二进制中，启动时解压到 `{GROOT_HOME}/sdk/<sdk版本>/`，通过 `PYTHONPATH` 暴露给服务进程。解压采用临时目录加原子改名，已存在且完整时跳过。

不把 SDK 安装进虚拟环境的原因：

- SDK 版本始终与宿主一致，不会出现协议错配。
- 使用者无需额外执行安装步骤。
- 服务升级 SDK 不会影响其他服务。

`PYTHONPATH` 位于 `sys.path` 前部，即使虚拟环境中安装了同名包，也以嵌入版本为准。

启动器职责：

1. 读取 stdin 令牌。
2. 绑定 `127.0.0.1:0`，启动 uvicorn。
3. 等待 lifespan 完成，输出就绪行。
4. 安装令牌校验中间件。
5. 提供 `/healthz`，返回 `200` 与 SDK 版本。
6. 解析并暴露身份头：`caller`、`tenant_id`、`user_id`、`request_id`。
7. 提供错误信封辅助函数。
8. 监听 stdin EOF，触发优雅停止。

依赖约定：`requirements.txt` 必须包含 `fastapi` 与 `uvicorn`。SDK 除这两项外只使用 Python 标准库。

调试模式：启动器接受 `--dev`，跳过令牌校验，但仍只绑定 `127.0.0.1`，供开发者不经 Groot 手动调试。Groot 正常启动时不使用该参数。

### 1.10 ASR 语种识别服务

#### 1.10.1 接口

```
POST /services/asr-router/detect
Content-Type: multipart/form-data
```

| 字段 | 必填 | 说明 |
|---|---|---|
| `file` | 是 | 音频文件 |

支持格式：`wav`、`mp3`、`m4a`、`flac`、`ogg`、`webm`。非音频扩展名返回 400 `unsupported_type`。文件大小上限 50MB，超出返回 413 `file_too_large`。

成功响应：

```json
{
  "language": "zh",
  "confidence": 0.97,
  "alternatives": [
    {"language": "en", "confidence": 0.02},
    {"language": "ja", "confidence": 0.01}
  ],
  "duration_ms": 3200
}
```

| 字段 | 说明 |
|---|---|
| `language` | ISO 639-1 两字母代码；无法判定时为 `und` |
| `confidence` | 首要语种置信度，0 到 1 |
| `alternatives` | 其余候选，按置信度降序，最多 3 项，可为空数组 |
| `duration_ms` | 音频时长，毫秒 |

语种标签由服务内部归一化：FireRedLID 的原始标签在服务内映射为 ISO 639-1。更换后端时只替换映射与推理实现，对外字段不变。

#### 1.10.2 后端抽象

服务内部定义语种识别后端接口：

```python
class LIDBackend(Protocol):
    def load(self) -> None: ...
    def detect(self, audio: AudioBuffer) -> list[tuple[str, float]]: ...
```

v1 实现为 `FireRedLIDBackend`。后端选择在服务代码的工厂函数中完成，不引入外部配置。更换后端时，新增实现类并调整工厂即可，接口、错误信封与响应格式保持不变。

#### 1.10.3 运行特征

- 模型在 lifespan 启动阶段加载一次，就绪行在加载完成后输出，因此就绪即代表可推理。
- 推理在受限线程池中执行，默认同时只跑 1 个推理任务，避免多个请求争抢 GPU 或导致内存峰值叠加；排队请求受 Groot 首字节超时约束。
- 音频解码与重采样在服务内部完成，必要时使用系统 `ffmpeg`；`ffmpeg` 作为部署前置依赖写入服务 README。
- 上传音频只在处理期间存在，不写入磁盘、不进入日志、不落库。
- 模型权重默认位于 `services/asr-router/models/`，可由环境变量 `GROOT_SERVICE_MODELS_DIR` 覆盖。

#### 1.10.4 权限与路径

- 服务名：`asr-router`
- 对外路径：`/services/asr-router/detect`
- 权限点：`chat`

### 1.11 配置项

#### 1.11.1 bootstrap.yaml

```yaml
services:
  enabled: true
  disable: []
```

| 项 | 默认 | 生效方式 |
|---|---|---|
| `services.enabled` | `true` | 重启 |
| `services.disable` | `[]` | 重启 |

#### 1.11.2 service.yaml

```yaml
permission: chat
enabled: true
startup_timeout: 180s
request_timeout: 120s
```

| 项 | 默认 | 说明 |
|---|---|---|
| `permission` | `all` | 调用该服务所需权限点，必须是已知权限点 |
| `enabled` | `true` | 是否启动 |
| `startup_timeout` | `180s` | 等待就绪行的上限 |
| `request_timeout` | `120s` | 首字节超时 |

未知字段按错误处理，避免拼写错误被静默忽略。

### 1.12 目录规范与测试

仓库新增与调整：

```
groot/
├── internal/pysvc/          # Go 宿主：发现、监督、代理、权限解析
│   ├── sdk/                 # 嵌入二进制的 Python SDK 源码
│   └── *_test.go
├── services/                # Python 服务源码
│   └── asr-router/
│       ├── service.yaml
│       ├── requirements.txt
│       ├── README.md
│       ├── app/
│       └── models/          # 仅占位；权重不进仓库
└── tests/
    └── services/
        └── asr-router/      # 服务的 pytest 测试
```

同步更新 `CLAUDE.md` 的目录结构规范，新增 `services/` 与 `tests/services/`。

测试归属：

| 类型 | 位置 |
|---|---|
| Go 宿主单元测试 | `internal/pysvc/*_test.go` |
| Python SDK 与 asr-router 单元测试 | `tests/services/asr-router/`（pytest） |
| 端到端系统测试 | `tests/python/`（用户执行） |

### 1.13 非目标

- 不接受服务热加载：新增或修改服务需重启 Groot。
- 不自动安装依赖、不自动下载模型权重、不创建虚拟环境。
- 不为非 Python 服务提供 SDK；协议本身与语言无关，其他语言可按协议自行实现。
- 不提供跨节点服务调用：每个节点只代理本机服务。
- 不在 Web 界面提供服务管理页面。
- 不强制资源限额（CPU、内存、GPU）；资源约束由部署环境负责。
- 不主动轮询服务健康：以进程存活、请求超时与重启策略作为可用性依据。

## 二、设计决策

### 2.1 集成方式：本机 HTTP 服务

| 方案 | 结论 |
|---|---|
| 命令行逐次调用 | 每次调用重新加载模型权重，延迟与资源不可接受；无并发与健康状态 |
| Go 内嵌 Python（CGO / 嵌入解释器） | 需要 CGO 与 ABI 对齐，Python 侧崩溃会带崩 Groot，依赖升级与打包复杂 |
| 本机 HTTP 常驻服务 | 模型常驻、并发可控、崩溃隔离、便于替换实现，选此方案 |

### 2.2 交付方式：外置服务目录

- **外置目录**（选用）：服务放在 `{GROOT_HOME}/services/`，虚拟环境、依赖与权重由使用者准备，Groot 只发现与托管。
- **二进制内置并自动安装**（未选用）：首启解压、建环境、装依赖、下权重。三平台下 torch 变体、Python 版本、VC++ 运行库、权重下载差异过大，安装失败难以诊断，宿主也会变得难以预测。

选择外置目录，同时保留 `go:embed` 嵌入 SDK，使协议实现随宿主升级而不会错配。

### 2.3 传输方式：TCP 环回与内部令牌

- **Unix domain socket**：隔离更彻底，但 uvicorn 在 Windows 不支持 `--uds`，会导致传输方式分叉。
- **TCP 127.0.0.1 随机端口 + 内部令牌**（选用）：三平台同一实现；其他主机不可达；同机其他进程可以建立连接，但没有令牌无法通过校验；令牌仅经 stdin 下发，不入 argv 与环境变量。

### 2.4 令牌传递：stdin 管道

| 方式 | 可行性 |
|---|---|
| 命令行参数 | `/proc/<pid>/cmdline` 通常全局可读，`ps` 可见 |
| 环境变量 | 同用户可读 `/proc/<pid>/environ`，并被后续子进程继承 |
| stdin 管道（选用） | 只有子进程可读，读后即止；同时充当父进程生命线 |

令牌每次启动随机生成，不持久化、不配置、不记录日志。

### 2.5 运行环境：默认共享，服务级可覆盖

- 默认所有服务共用 `{GROOT_HOME}/venv`。服务由同一开发者维护，依赖冲突在开发期控制，共享环境减少磁盘占用、简化升级。
- 服务目录存在 `.venv` 时自动改用独立环境，无需配置。冲突出现时按服务隔离，不改宿主。
- SDK 不进入任何虚拟环境，由 `PYTHONPATH` 指向嵌入版本。

### 2.6 语种识别边界：只识别，不路由

`asr-router` 只返回语种。ASR 模型选择属于上层应用的业务规则，不进入 Groot，也不进入本服务。上层应用拿到语种后，可通过现有 `POST /audio/transcriptions` 显式指定模型完成转录。

### 2.7 固定约定优先于配置

目录结构、入口位置、解释器路径、启动命令、就绪协议、令牌与身份头均为固定标准；`service.yaml` 只保留授权与超时这类策略项。新增服务不需要编写启动代码或协议代码，减少配置错误面。

## 三、迭代说明

本能力为首次引入，无上一版实现。

- 新增 Python 服务宿主：目录发现、进程监督、就绪握手、反向代理、多实例行为。
- 新增 `/services/<服务名>/...` 路由与对应的权限点解析。
- 新增内部令牌与身份头协议，新增 `service_unavailable` 错误码。
- 新增嵌入式 Python SDK。
- 新增第一个托管服务 `asr-router` 及其语种识别接口 `POST /services/asr-router/detect`。
- 新增 `bootstrap.yaml` 的 `services` 配置节。
- 目录规范新增 `services/` 与 `tests/services/`。
