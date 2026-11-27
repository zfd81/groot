# Python 插件与 ASR 语种识别插件设计文档

## 一、功能设计

### 1.1 功能概述

Groot 在运行期托管一个 **Python 插件进程**。它是一个由 Groot 启动与监督的 Python 进程，监听本机环回地址上的一个临时端口；`{GROOT_HOME}/plugins/` 下的每个目录是它的一个**插件**，在进程内对应一个路径 `/<插件名>` 与一个处理函数，没有下级路径。Groot 在自己唯一的对外端口上以 `/plugins/<插件名>` 暴露这些插件，请求经鉴权后代理到 Python 插件进程的对应路径。

每个 Groot 实例拥有一个自己的 Python 插件进程，一对一归属：Groot 退出或重启时它随之结束，不同实例之间不共享。端口由内核在启动时分配，不配置、不固定。

调用方访问插件时使用与 Groot 其他接口完全相同的 API Key。请求经过相同的验签、吊销检查、权限点校验与限流，然后由 Groot 代理到 Python 插件进程。Python 插件进程不直接对外，不实现面向调用方的鉴权，也无法通过网络被其他主机访问。

`groot plugins init` 在 `{GROOT_HOME}` 下创建 `plugins/` 及其固定布局：`.sdk/` 目录、全局配置 `plugin.yaml` 模板、基础依赖清单 `requirements.txt` 模板，以及新建插件用的模板目录 `_template/`。布局与共享虚拟环境由 `groot plugins init` 独立负责；`groot init` 只准备工作目录根与资源目录，不创建 `plugins/` 及其内容。

第一个插件是 `asr-router`，承担语音语种识别：接收一段音频，识别其语种，返回语种代码、置信度与候选语种列表。结果供上层应用自行决定后续调用哪个 ASR 模型；Groot 与本插件都不做「语言到 ASR 模型」的映射。语种识别后端在 v1 采用 FireRedLID 的 MLX INT8 转换版本，后端实现封装在插件内部，替换后端不改变对外协议，也不改动 Groot。

该能力解决的问题：

- 语音、视觉、语言识别等模型以 Python 生态为主，无法在 Go 进程内直接加载使用。
- 逐次命令行调用需要重复加载模型权重，启动耗时与内存开销不可接受。
- 若每个 Python 能力各自监听端口并各自实现鉴权，会产生多端口暴露、重复鉴权逻辑与分散的密钥管理。
- 上层应用需要一个稳定的语种识别接口，接口不应随底层 LID 模型的更换而变化。

### 1.2 能力清单

| 能力 | 说明 |
|---|---|
| 插件发现 | 扫描 `{GROOT_HOME}/plugins/*/`，识别符合标准结构的插件 |
| 单进程托管 | 每个 Groot 实例拉起一个 Python 插件进程，所有插件注册其中 |
| 一插件一地址 | 每个插件对应进程内的一个路径 `/<插件名>` 与一个处理函数 |
| 生命周期托管 | 拉起、就绪握手、崩溃重启、优雅停止、Groot 退出时级联结束 |
| 单端口暴露 | 所有插件通过 Groot 的对外端口以 `/plugins/<插件名>` 访问 |
| 统一鉴权 | 复用 API Key 验签、吊销检查、权限点与限流，不新增鉴权实现 |
| 身份透传 | 向插件注入调用方、租户、用户、请求 ID，插件无需解析 API Key |
| 内部令牌 | Python 插件进程每次启动生成独立令牌，仅 Groot 与该进程知道 |
| 透明代理 | 方法、状态码与响应头原样透传；请求体与响应体边读边转发，大文件不整体进内存 |
| 插件级隔离 | 某个插件加载失败或请求出错，不影响同进程的其他插件 |
| 多实例共存 | 同一主机可运行多个 Groot 实例，各自拉起并管理自己的 Python 插件进程 |
| 跨平台 | Linux、macOS、Windows 使用同一套协议 |
| 状态可见 | 健康检查接口与 `groot status` 展示 Python 插件进程与每个插件的运行状态 |
| 语种识别 | `POST /plugins/asr-router`，返回 `language`、`confidence` 与候选列表 |
| 后端可替换 | LID 后端抽象为插件内部接口，v1 实现为 FireRedLID |

### 1.3 模块边界

| 模块 | 职责 |
|---|---|
| Groot 宿主（`internal/plugin`） | 发现插件、读取全局配置、解压 SDK、拉起与监督 Python 插件进程、就绪握手、反向代理、权限解析、日志收编、状态汇总 |
| Groot 认证中间件 | 对 `/plugins/...` 执行与其他接口一致的 API Key 鉴权、权限点与限流 |
| Python SDK（`groot_plugin_sdk`） | Python 插件进程的启动器：加载并注册全部插件、令牌校验、环回地址绑定、就绪上报、身份头解析、统一错误信封、健康端点 |
| 插件（如 `plugins/asr-router`） | 业务逻辑、模型加载与推理、一个处理函数 |
| 上层应用 | 根据返回的语种自行选择 ASR 模型，再调用 Groot 的转录接口 |

Groot 不感知插件的业务语义，只处理进程与 HTTP。SDK 不包含业务逻辑，只实现宿主与插件的公共协议。插件作者只编写处理函数与启动、停止钩子。

### 1.4 目录与契约

#### 1.4.1 目录结构

```
{GROOT_HOME}/plugins/             # Python 插件的根目录
├── plugin.yaml                   # 全局配置，可选；缺省时使用默认值
├── requirements.txt              # 基础依赖（fastapi、uvicorn、python-multipart），所有插件共用
├── .venv/                        # 共享 Python 环境；不参与插件扫描
├── .sdk/                         # Groot 解压的 Python SDK；不参与插件扫描
├── _template/                    # 新建插件的模板；不参与插件扫描
│   ├── requirements.txt
│   └── app/
│       ├── __init__.py
│       └── main.py
└── <插件名>/
    ├── requirements.txt          # 必须；业务依赖清单，可为空
    ├── app/
    │   ├── __init__.py
    │   └── main.py               # 必须定义处理函数 handle
    └── models/                   # 可选；模型权重等运行资源
```

Windows 上 `{GROOT_HOME}` 为 `%USERPROFILE%\.groot`。

| 路径 | 内容 | 归属 |
|---|---|---|
| `plugins/plugin.yaml` | 全局配置：启动超时、请求超时、插件权限点 | `groot plugins init` 创建模板，使用者维护 |
| `plugins/requirements.txt` | 基础依赖：fastapi、uvicorn、python-multipart | `groot plugins init` 创建模板 |
| `plugins/.venv/` | 第三方 Python 库，所有插件共用 | `groot plugins init` 创建并安装基础依赖 |
| `plugins/.sdk/` | `groot_plugin_sdk` 协议库，随 Groot 发布 | `groot plugins init` 创建目录，Groot 启动时解压 |
| `plugins/_template/` | 插件骨架，复制改名即为新插件 | `groot plugins init` 创建 |
| `plugins/<插件名>/app/` | 插件业务代码 | 插件开发者 |
| `plugins/<插件名>/requirements.txt` | 该插件的业务依赖 | 插件开发者 |
| `plugins/<插件名>/models/` | 第三方模型权重（如 FireRedLID）；可选，无模型的插件不创建 | 使用者准备 |

第三方依赖库与 Groot 自有的 SDK 分开：前者进虚拟环境，后者按版本解压到 `.sdk`。模型权重单独放在插件目录的 `models/` 下，不进虚拟环境，也不进代码仓库。

#### 1.4.2 固定约定

以下内容为标准，不通过配置改变：

| 项 | 固定值 |
|---|---|
| 插件名 | 目录名；必须匹配 `[a-z0-9][a-z0-9_-]*` |
| 对外路径 | `/plugins/<插件名>` |
| 进程内路径 | `/<插件名>` |
| 入口 | `app/main.py` 中的函数 `handle` |
| 进程内模块名 | `groot_plugins.<插件名>.app`，插件名中的 `-` 换为 `_` |
| 解释器 | 唯一环境 `{GROOT_HOME}/plugins/.venv/bin/python`（Windows 为 `Scripts\python.exe`） |
| 启动命令 | `<解释器> -m groot_plugin_sdk.launcher` |
| 工作目录 | `{GROOT_HOME}/plugins` |
| 进程数 | 每个 Groot 实例一个 |
| 进程归属 | 每个 Groot 实例一个，随实例创建与退出，实例间不共享进程、端口与令牌 |
| 启动条件 | 存在至少一个通过校验的插件且共享解释器存在；否则不拉起进程，状态保持 `disabled` |
| Python 环境 | `{GROOT_HOME}/plugins/.venv` 全局唯一，所有插件共用，不按插件创建环境 |
| 访问入口 | 仅 Groot 的 `/plugins/<插件名>`；Python 端口只在环回地址可达，且除 `/healthz` 外需内部令牌 |
| 监听地址 | `127.0.0.1`，端口由内核分配 |
| 令牌传递 | stdin 第一行 |
| 插件清单传递 | stdin 第二行 |
| 就绪上报 | stdout 结构化就绪行 |
| 健康端点 | `GET /healthz`，由 SDK 提供 |
| 全局配置 | `{GROOT_HOME}/plugins/plugin.yaml` |
| 停止等待 | 固定 10 秒，超时强制杀死 |

#### 1.4.3 共享 Python 环境

所有插件共用 `{GROOT_HOME}/plugins/.venv`，与操作系统 Python 隔离。环境全局唯一，插件不单独拥有虚拟环境；插件目录中即使出现 `.venv` 也不参与解析。使用者执行 `groot plugins init` 创建共享环境并安装基础依赖，插件业务依赖由使用者安装：

```
{GROOT_HOME}/plugins/.venv/bin/pip install -r {GROOT_HOME}/plugins/<插件名>/requirements.txt
```

Windows 把 `bin/pip` 换成 `Scripts\pip.exe`。

`fastapi`、`uvicorn` 与 `python-multipart`（表单/文件上传所需）只在 `plugins/requirements.txt` 中声明一次；每个插件的 `requirements.txt` 只列业务依赖，没有额外依赖时保留注释即为空清单。所有插件运行在同一个解释器中，依赖版本须互相兼容，由插件开发者在开发期保证。

Groot 运行期不安装或修复依赖，不下载模型权重；基础依赖安装失败时由使用者按命令输出的手动安装命令补装。

#### 1.4.4 插件入口契约

所有插件运行在同一个 Python 插件进程中，进程入口统一为 SDK 启动器。启动器持有唯一的 FastAPI 应用，按固定契约加载每个插件目录下的 `app/main.py`，把其中的 `handle` 函数注册为路径 `/<插件名>` 的处理器。插件作者不创建 FastAPI 应用、不写路由装饰器、不写入口函数、不写 `if __name__ == "__main__"`。

**模块名**：启动器把 `<插件目录>/app/` 注册为包 `groot_plugins.<插件名>.app`，插件名中的 `-` 换为 `_`，例如 `asr-router` 的入口模块为 `groot_plugins.asr_router.app.main`。多个插件的 `app` 包因此互不冲突，各自的模块级变量相互隔离。目录名换算后相同（如 `asr-router` 与 `asr_router`）视为重名。

`app/main.py` 的模板（即 `_template/app/main.py` 的内容）：

```python
import logging
from fastapi import File, UploadFile

from .lid import create_backend          # 插件内部模块一律相对导入

log = logging.getLogger(__name__)        # 记录器名自带插件名

METHODS = ["POST"]                       # 可选；接口接受的 HTTP 方法，默认 ["POST"]

_backend = None                          # 插件状态放模块级变量，各插件模块相互隔离

async def startup() -> None:             # 可选；进程启动时调用一次，加载模型等重资源
    global _backend
    _backend = create_backend()
    _backend.load()
    log.info("模型加载完成")

async def shutdown() -> None:            # 可选；进程停止时调用一次，释放资源
    pass

async def handle(file: UploadFile = File(...)) -> dict:   # 必须；本插件唯一的处理函数
    ...
    return {"language": "zh", "confidence": 0.97}
```

| 名称 | 必须 | 说明 |
|---|---|---|
| `handle` | 是 | 处理函数，签名按 FastAPI 规则声明参数（表单、文件、查询、JSON 体、`Depends`）；返回值按 FastAPI 规则序列化，也可直接返回 `Response` |
| `METHODS` | 否 | 字符串列表，接口接受的 HTTP 方法；缺省 `["POST"]` |
| `startup` | 否 | 无参协程或普通函数，进程启动时按插件目录名顺序逐个调用；抛出异常则该插件标为失败并不注册 |
| `shutdown` | 否 | 无参协程或普通函数，进程停止时按启动的逆序调用；异常只记日志 |

插件代码的要求：

1. `app/main.py` 中存在名为 `handle` 的可调用对象。缺失时该插件加载失败。
2. 启动阶段逻辑（加载模型等）写在 `startup`，释放写在 `shutdown`。不定义 FastAPI 应用，不使用 `lifespan` 或 `on_event`。
3. 插件不绑定端口、不启动 uvicorn、不读 stdin、不写就绪行、不实现令牌校验、不定义 `/healthz`；这些由 SDK 启动器完成。
4. 插件不读取 API Key；调用方身份通过 `Depends(groot_identity)` 声明为 `handle` 的参数获取（见 1.9）。
5. 插件只有 `/<插件名>` 一个路径。需要多个操作时，建多个插件目录，或在请求体中用字段区分。
6. 插件内部的模块引用使用相对导入（`from .lid import ...`）；绝对导入 `import app.xxx` 不可用。
7. 插件通过自身文件位置定位资源目录（如 `Path(__file__).resolve().parent.parent / "models"`），不依赖进程工作目录。
8. 日志使用 `logging.getLogger(__name__)`，不自行配置 handler；启动器把根 logger 接到 stdout，由 Groot 收编。
9. 插件不修改进程级状态：不调用 `sys.exit`、`os.chdir`、`os.environ` 写入、`signal.signal`，不重新配置根 logger，不启动守护进程。线程池、GPU 句柄等资源归插件模块持有，在 `shutdown` 中释放。
10. 插件返回业务错误时抛出 SDK 的 `APIError`（见 1.9）；不自行注册异常处理器。
11. 插件不负责调用启动器；启动不需要导入 SDK，只有需要身份或错误信封时才导入 SDK 辅助接口。

新增插件的步骤：复制 `_template/` 为 `<插件名>/`，编写 `app/main.py` 与 `requirements.txt`，安装依赖，重启 Groot。

#### 1.4.5 全局配置

`{GROOT_HOME}/plugins/plugin.yaml` 承载 Python 插件的公共配置。`groot plugins init` 创建全注释模板，已存在时跳过，不覆盖使用者内容：

```yaml
# Python 插件全局配置；取消注释即生效，缺省值如下。
# startup_timeout: 180s
# request_timeout: 120s
# permission:
#   asr-router: chat
```

| 项 | 默认 | 说明 |
|---|---|---|
| `startup_timeout` | `180s` | 等待 Python 插件进程就绪行的上限，覆盖全部插件 `startup` 的总时间；模型多或下载慢时调大 |
| `request_timeout` | `120s` | 代理到插件的首字节超时 |
| `permission` | `{}` | 插件名到权限点的映射；未列出的插件使用 `all` |

三项均在启动时读取，改动后重启生效。模板等价于全部默认值；安装 `asr-router` 时取消 `permission` 两行注释，即把该插件归入 `chat` 权限。

配置文件不存在时使用全部默认值。解析失败或出现未知字段时记录错误日志并按默认值运行；默认权限 `all` 是更严格的取值，回退不会放宽访问。`permission` 中出现未识别的插件名时记录警告并忽略；权限点不在已知集合中时该插件使用 `all` 并记录错误。

`bootstrap.yaml` 不新增相关配置节。插件的启用范围由目录本身决定：目录存在且通过校验即加载。

#### 1.4.6 识别与校验

插件的识别只由 Groot 做一次，结果通过 stdin 交给启动器；启动器不自行扫描目录（`--dev` 调试模式除外）。

Groot 扫描 `{GROOT_HOME}/plugins/` 的直接子目录，忽略以 `.` 或 `_` 开头的目录；目录名符合命名规则、存在 `app/__init__.py`、`app/main.py` 与 `requirements.txt` 的目录视为插件。`app/__init__.py` 让启动器能把 `app/` 注册为包并支持插件内部的相对导入。`healthz` 是 SDK 健康端点 `/healthz` 的保留名称，同名目录视为 `invalid`，避免插件遮蔽健康端点。

1. 不符合规则的目录记为 `invalid`，记录错误日志并给出修复提示，不注册路由；请求由现有兜底处理器返回 404，与访问任意不存在的路径行为一致。
2. 插件名重复（目录名在大小写不敏感文件系统上的冲突，或 `-`/`_` 换算后相同）视为配置错误，只保留一个并记录警告。
3. 通过校验的插件注册 `/plugins/<插件名>` 路由，并进入交给启动器的清单。
4. 共享解释器不存在时不拉起 Python 插件进程，记录一条错误并提示创建环境的命令，所有插件不可用。
5. 清单为空时不拉起 Python 插件进程。

启动器按清单逐个加载：

1. 某个插件导入失败（语法错误、依赖缺失、无 `handle`）或 `startup` 抛出异常，记录该插件的错误，不注册其路径，继续处理其余插件；进程照常就绪。
2. 加载结果随就绪行上报，Groot 据此把失败的插件标为 `failed`，对其请求返回 503。
3. 全部插件都加载失败时进程仍就绪，所有插件返回 503，错误原因在状态中逐项可见。

### 1.5 运行时协议

#### 1.5.1 启动顺序

1. Groot 在创建 HTTP 服务器之前完成插件扫描与全局配置读取，得到插件名、权限点与可注册状态，用于路由注册与权限解析。
2. Groot 构造 HTTP 服务器后，与服务器开始监听同时异步调用 `Manager.Start`。SDK 解压发生在 `Manager.Start` 内部、拉起 Python 进程之前：把嵌入的 SDK 解压到 `{GROOT_HOME}/plugins/.sdk/<sdk版本>/`（见 1.9）；解压失败时进程状态记为 `failed`，不拉起插件。
3. Python 插件进程的拉起不阻塞对外端口就绪，`Manager.Start` 在后台完成以下动作：
   1. 生成 32 字节随机令牌，十六进制编码。
   2. 创建 stdin、stdout、stderr 三条管道。
   3. 以 `{GROOT_HOME}/plugins` 为工作目录执行 `<解释器> -m groot_plugin_sdk.launcher`。环境变量继承 Groot 进程，`PYTHONPATH` 前置 SDK 目录（已有值时保留在其后），并设置 `PYTHONUNBUFFERED=1`、`GROOT_LOG_LEVEL=<Groot 日志级别>`。
   4. 向 stdin 写入两行：第一行令牌，第二行通过校验的插件名 JSON 数组（如 `["asr-router","ocr"]`）。保留写端不关闭。
   5. 读取 stdout，等待就绪行；其余 stdout/stderr 行写入 Groot 日志。

#### 1.5.2 就绪握手

启动器完成以下动作后向 stdout 输出一行就绪消息：

1. 从 stdin 读取第一行作为内部令牌、第二行作为插件清单，随后启动后台线程继续读 stdin（生命线，见 1.5.3）。
2. 配置进程日志：根 logger 输出到 stdout，格式含记录器名，级别取环境变量 `GROOT_LOG_LEVEL`（缺省 `INFO`）。
3. 创建唯一的 FastAPI 应用，注册令牌校验中间件、`GET /healthz`，以及 `APIError`、`HTTPException`、`RequestValidationError` 与通用 `Exception` 四类错误信封处理器。
4. 按清单顺序逐个把 `<插件目录>/app/` 注册为 `groot_plugins.<插件名>.app` 并导入其 `main`。导入失败或缺少 `handle` 的插件记录错误并跳过。
5. 创建 TCP socket 并绑定 `127.0.0.1:0`，由内核分配端口。
6. 以该已绑定 socket 启动 uvicorn。应用的 lifespan 启动阶段按清单顺序逐个调用各插件的 `startup`；成功的插件随即以 `app.add_api_route("/<插件名>", handle, methods=METHODS)` 注册路径，失败的记录错误、不注册。
7. 输出就绪行。

就绪行格式：

```
GROOT_PLUGIN_READY {"port":53124,"pid":1234,"sdk":"1.0.0","plugins":{"asr-router":"ok","ocr":"error: No module named 'torch'"}}
```

`plugins` 为插件名到加载结果的映射：`ok` 表示已注册路径，`error: <原因>` 表示该插件未注册。

Groot 在 `startup_timeout` 内未读到就绪行、或进程提前退出，判定启动失败。端口只来自该就绪行，Groot 不解析 uvicorn 日志、不预先选端口。`sdk` 字段与宿主嵌入的 SDK 版本不一致时判定启动失败并记录错误，避免 `PYTHONPATH` 被其他来源的同名包覆盖。

绑定地址由 SDK 固定为 `127.0.0.1`，不接受配置覆盖，不解析 `localhost`，不允许 `0.0.0.0`。

#### 1.5.3 生命线

stdin 管道同时是父进程生命线。Groot 写完两行后保持写端打开；Python 的后台线程持续读取，读到 EOF 即说明 Groot 已退出或崩溃，启动器随即触发优雅停止并退出。

该机制覆盖 Linux、macOS 与 Windows，避免 Groot 被强制杀死后留下孤儿进程。

#### 1.5.4 令牌与身份头

Groot 代理请求时：

1. 无条件删除客户端携带的全部 `X-Groot-*` 请求头。
2. 写入以下请求头：

| 请求头 | 内容 |
|---|---|
| `X-Groot-Internal-Token` | Python 插件进程本次启动的令牌 |
| `X-Groot-Caller` | API Key 名称；Web 会话为 `web` |
| `X-Groot-Tenant-Id` | API Key 所属租户；Web 会话为空串 |
| `X-Groot-User-Id` | 请求头解析出的用户标识；Web 会话为 Web 用户 ID |
| `X-Groot-Request-Id` | Groot 为本次请求生成的随机 ID，写入两端日志用于关联 |
| `X-Groot-Plugin` | 插件名 |

SDK 对除 `/healthz` 外的全部请求校验 `X-Groot-Internal-Token`，使用常数时间比较；不匹配一律返回 401 错误信封，且校验发生在路由解析与请求体读取之前。所有插件共用这一道校验，插件代码不参与。

令牌不写入命令行参数、环境变量、磁盘或日志。每次进程启动重新生成，旧进程退出后旧令牌立即失效。

Python 插件进程只绑定 `127.0.0.1` 上的临时端口，端口不写入配置、不作为公开信息提供。调用方因此只能经 Groot 的 `/plugins/<插件名>` 访问插件：外部主机无法建立连接；本机其他应用即使探测到端口，也因缺少内部令牌而不通过校验，业务请求一律返回 401。唯一例外是 `GET /healthz`，它不校验令牌，只返回进程与插件的就绪状态，不包含业务数据。

#### 1.5.5 停止

1. Groot 关闭 stdin 写端，触发 SDK 的生命线退出；Linux/macOS 同时发送 `SIGTERM`。Windows 无等价信号，优雅停止只依赖生命线。
2. 启动器停止接受新连接，等待进行中的请求结束，按 `startup` 的逆序调用各插件的 `shutdown`，再关闭 uvicorn。
3. Groot 等待固定 10 秒，超时强制杀死。
4. Groot 自身关闭时，先停止接受新请求，再停止 Python 插件进程。

#### 1.5.6 重启策略

- 进程非正常退出、启动超时、就绪后异常退出，均按退避序列重启：1s、2s、4s、8s、16s、30s，之后保持 30s。
- 存活时间少于 60 秒的退出计为快速失败。连续快速失败达到 10 次后停止重启，全部插件保持不可用，直到 Groot 下次启动。
- 每次重启生成新令牌、新端口；旧代理目标随之替换。
- 重启粒度是整个进程：单个插件的加载失败不触发重启，只在状态中标为 `failed`；进程级崩溃（段错误、OOM、未捕获的致命异常）导致全部插件一起重启。

### 1.6 HTTP 网关

#### 1.6.1 路由

- 每个通过校验的插件注册一条路由：`/plugins/<插件名>`，接受任意 HTTP 方法；方法是否允许由 Python 侧的 `METHODS` 决定，不允许时 Python 插件进程返回 405 信封并原样透传。
- 转发时去掉 `/plugins` 前缀，查询串、方法、请求头、请求体原样转发。`POST /plugins/asr-router` 转发为 `POST /asr-router`。
- `/plugins/<插件名>/` 之下的任何多级路径不注册，由现有兜底处理器返回 404 错误信封。仅带尾斜杠的 `/plugins/<插件名>/` 沿用框架全局行为，301 重定向到无斜杠路径。
- 未通过校验的插件名不注册路由，同样返回 404。

#### 1.6.2 鉴权与权限点

`/plugins/...` 与 `/chat` 等接口注册在同一鉴权分组，完整复用：

1. Web 会话 Cookie 或 Bearer API Key 校验。
2. JWT 验签与按 `jti` 回表确认 Key 未删除（吊销即时生效）。
3. 权限点校验。
4. 限流。

权限点来自全局配置 `permission` 映射；未声明的插件取 `all`。`asr-router` 配置为 `chat`，与转录、合成、图片解析一致，不新增权限点。

认证中间件的路径到权限点映射引入可注入的解析器：宿主实现 `/plugins/<插件名>` 的查询，其余路径沿用现有静态规则。

Web 会话 Cookie 访问 `/plugins/...` 时沿用现有 Web 语义：视为持有 `all` 权限，不检查插件声明的权限点。插件需要区分调用来源时读取 `caller`。

#### 1.6.3 转发细节

| 项 | 行为 |
|---|---|
| 请求体 | 以流方式转发，不解析表单，不整体读入内存；受 Groot 现有 200MB 请求体上限约束 |
| 响应体 | 以流方式回写，不缓冲 |
| 逐跳头 | 移除 `Connection`、`Keep-Alive`、`Transfer-Encoding`、`Upgrade` 等 |
| 上游地址 | `127.0.0.1:<就绪行中的端口>` |
| 连接超时 | 固定 5 秒，不随配置变化；连接失败（含超时）映射为 502 |
| 首字节超时 | 等待上游响应头的超时为 `request_timeout`，默认 120 秒；超时映射为 504 |
| 响应体传输 | 首字节到达后不再设总时长上限；客户端断开时取消上游 HTTP 请求。若插件已在 `asyncio.to_thread` 中开始推理，该次推理会继续执行完毕（不排队新请求），之后不再产生新的上游工作 |

代理层不要求插件采用流式响应。普通 JSON 响应与 SSE 等长连接响应走同一条转发路径，是否使用流式由各插件的 `handle` 返回值决定；Groot 只保证不额外缓冲、不截断，也不强制统一响应形态。

#### 1.6.4 错误映射

| 场景 | 状态码 | 错误码 |
|---|---|---|
| 插件名不存在、未通过校验或路径带下级段 | 404 | `invalid_request` |
| Python 插件进程未就绪 / 重启中 / 已放弃重启；或该插件加载失败 | 503 | `plugin_unavailable` |
| 连接被拒、上游读写失败 | 502 | `upstream_error` |
| 首字节超时 | 504 | `upstream_error` |
| Python 插件进程自身返回的错误（含 405） | 原状态码原响应体 | 由 Python 插件进程决定 |

SDK 生成的错误使用与 Groot 相同的 OpenAI 错误信封结构，调用方只需一套解析逻辑。`errbody` 新增错误码 `plugin_unavailable`。

#### 1.6.5 状态展示

健康检查 `GET /web/health` 的 `checks` 新增 `plugins` 项，描述 Python 插件进程与每个已识别的插件：

```json
"plugins": {
  "status": "degraded",
  "info": {
    "process": {"status": "running", "pid": 1234, "port": 53124,
                "restarts": 0, "started_at": "2026-11-23T10:00:00Z"},
    "plugins": [
      {"name": "asr-router", "status": "running"},
      {"name": "ocr", "status": "failed", "error": "No module named 'torch'"},
      {"name": "broken", "status": "invalid", "error": "缺少 app/main.py"}
    ]
  }
}
```

| `process.status` | 含义 |
|---|---|
| `running` | Python 插件进程已就绪 |
| `starting` | 已拉起，等待就绪行；含退避等待中的重启 |
| `failed` | 连续快速失败达到上限，已放弃重启 |
| `disabled` | 未拉起：共享解释器缺失或没有任何插件 |
| `stopped` | 已按关闭流程停止；仅在 Groot 关闭期间可见 |

| 插件 `status` | 含义 |
|---|---|
| `running` | 已注册路径，可接受请求 |
| `failed` | 启动器加载失败或 `startup` 抛出异常，`error` 为原因 |
| `invalid` | 未通过目录校验，未注册路由 |
| `pending` | Python 插件进程未就绪，尚无加载结果 |

`process` 的 `pid`、`port`、`started_at` 仅 `running` 时给出；`restarts` 为本次 Groot 启动以来的重启次数；除 `running` 与 `stopped` 外的状态带 `error` 说明最近一次失败原因。

`plugins` 项的 `status`：`process` 为 `running` 且全部插件 `running`，或没有任何插件时为 `healthy`；其余情况为 `degraded`。该状态只描述 Python 插件进程，不改变顶层 `status`。

`groot status` 在「组件状态」区新增一行汇总，并在其下逐行列出非 `running` 的插件：

```
组件状态:
  LLM:         healthy (gpt-4o)
  MCP Servers: healthy (2 个)
  Skills:      healthy (5 个)
  Memory:      healthy (12 个会话)
  Plugins:     degraded (1/3 运行中, 进程 pid 1234)
    ocr         failed     No module named 'torch'
    broken      invalid    缺少 app/main.py
```

全部运行时只显示汇总行，如 `Plugins:     healthy (3/3 运行中, 进程 pid 1234)`。Python 插件进程非 `running` 时汇总行显示进程状态与原因，如 `Plugins:     degraded (进程 starting: 等待就绪行超时, 已重启 2 次)`。没有任何插件目录时显示 `Plugins:     healthy (0 个)`。

### 1.7 多实例与平台

#### 1.7.1 实例归属

- Python 插件进程由 Groot 工作进程直接创建，一对一归属。受监督模式下只有工作进程拉起它，监督进程不加载业务资源。
- Python 插件进程不跨实例复用。同一主机运行多个 Groot 实例时，每个实例拥有自己的进程、令牌与端口。
- 不同 `GROOT_HOME` 的实例完全隔离：插件代码、虚拟环境、模型权重各自独立。
- 共用同一 `GROOT_HOME` 的实例共享插件代码、虚拟环境与模型文件（运行期只读），但各自的进程各自加载模型、各自占用内存或显存；这些实例都会加载全部已识别插件。
- 进程绑定 `127.0.0.1:0`，端口由内核分配，实例之间不会冲突。
- Groot 收编进程 stdout/stderr 时每行带 Python 进程 PID（日志字段 `plugin_pid`），不带 `reg_id`。插件级归属由记录器名体现（`groot_plugins.<插件名>...`），插件代码使用 `logging.getLogger(__name__)` 即可。

#### 1.7.2 平台差异

三平台使用同一协议：TCP 环回、stdin 令牌与清单与生命线、stdout 就绪行。差异只有两处：

| 项 | Linux / macOS | Windows |
|---|---|---|
| 解释器路径 | `plugins/.venv/bin/python` | `plugins\.venv\Scripts\python.exe` |
| 停止方式 | 关闭 stdin 并发送 `SIGTERM` | 只关闭 stdin，依赖生命线退出 |

### 1.8 故障处理

| 故障 | 行为 |
|---|---|
| 插件目录结构不完整、缺少依赖清单 | 该插件 `invalid`，不注册路由；其他插件与 Groot 正常 |
| 共享解释器缺失 | 不拉起 Python 插件进程，记录错误并提示创建环境的命令；Groot 其他功能正常 |
| 全局配置非法 | 使用默认值并记录错误；默认权限 `all` 不放宽访问 |
| SDK 版本不一致 | 判定启动失败，记录错误，按退避重启 |
| 某个插件导入失败、缺少 `handle` 或 `startup` 抛异常 | 该插件 `failed`，返回 503；Python 插件进程与其他插件正常 |
| 某个插件 `handle` 抛出未捕获异常 | 该请求返回 500 信封，堆栈进日志；Python 插件进程与其他插件正常 |
| Python 插件进程启动超时 | 杀死进程，按退避重启 |
| Python 插件进程崩溃（段错误、OOM 等） | 全部插件一起按退避重启，新端口、新令牌 |
| 连续快速失败 10 次 | 停止重启，全部插件不可用直到 Groot 重启 |
| 连接上游超时或失败 | 连接超时固定 5 秒；返回 502 `upstream_error` |
| 等待上游响应头超时 | `request_timeout`（默认 120 秒）内未收到响应头，返回 504 `upstream_error`；上游 HTTP 请求被取消 |
| 客户端断开 | 取消上游 HTTP 请求；若插件已在 `asyncio.to_thread` 中开始推理，该次推理会执行完毕（不排队新请求）。Windows 上宿主使用的 HTTP 传输层对带流式请求体的请求不做断连探测，取消要等到 `request_timeout` 到期 |
| Groot 被 `kill -9` | Python 插件进程读到 stdin EOF 后自行退出 |

### 1.9 Python SDK

SDK 以 Python 源码形式嵌在 Groot 二进制中，Groot 启动时解压到 `{GROOT_HOME}/plugins/.sdk/<sdk版本>/`，通过 `PYTHONPATH` 暴露给 Python 插件进程。解压采用临时目录加原子改名，已存在且完整时跳过。

SDK 不安装进虚拟环境，版本始终与宿主一致，使用者无需额外安装步骤。就绪行中的 `sdk` 字段用于宿主核对版本。

启动器 `groot_plugin_sdk.launcher` 承担 1.5.2 列出的全部协议动作，插件作者不调用它。

对外辅助接口，插件按需导入：

```python
from fastapi import Depends, File, UploadFile
from groot_plugin_sdk import APIError, GrootIdentity, groot_identity

async def handle(
    file: UploadFile = File(...),
    identity: GrootIdentity = Depends(groot_identity),
) -> dict:
    if not file.filename.endswith((".wav", ".mp3")):
        raise APIError(400, "不支持的音频格式",
                       type="invalid_request_error", code="unsupported_type")
    return {"caller": identity.caller, "tenant_id": identity.tenant_id}
```

| 接口 | 用途 |
|---|---|
| `GrootIdentity` | 调用方身份，字段 `caller`、`tenant_id`、`user_id`、`request_id`、`plugin` |
| `groot_identity` | FastAPI 依赖，从 `X-Groot-*` 请求头解析身份，作为 `handle` 的参数声明 |
| `APIError` | 抛出后由启动器注册的处理器转为 OpenAI 错误信封 |

启动器在应用上统一注册四类处理器：`APIError` 按其字段输出信封；`HTTPException` 映射为同状态码的信封，`code` 为 `invalid_request`；`RequestValidationError` 映射为 400 信封，`param` 取首个出错字段；其余未捕获异常映射为 500 信封并记录堆栈到日志，响应体不含堆栈。

依赖约定：`fastapi`、`uvicorn` 与 `python-multipart` 由 `plugins/requirements.txt` 提供，SDK 除这几项外只使用 Python 标准库。

调试模式：启动器接受 `--dev`，不读 stdin，自行按 1.4.6 的规则扫描工作目录，跳过令牌校验，身份字段全部为空串，仍只绑定 `127.0.0.1`，并把端口打印到 stderr，供开发者不经 Groot 手动调试。`--dev --only <插件名>` 只加载指定插件。Groot 正常启动时不使用这些参数。

### 1.10 ASR 语种识别插件

#### 1.10.1 接口

```
POST /plugins/asr-router
Content-Type: multipart/form-data
```

| 字段 | 必填 | 说明 |
|---|---|---|
| `file` | 是 | 音频文件 |

支持格式：`wav`、`mp3`、`m4a`、`flac`、`ogg`、`webm`。非音频扩展名返回 400 `unsupported_type`。文件大小上限 50MB，超出返回 413 `file_too_large`。音频时长须在 1.5 到 35 秒之间，超出返回 400 `invalid_request` 并说明范围。权限点在全局配置中设为 `chat`。

成功响应：

```json
{
  "language": "zh",
  "dialect": "mandarin",
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
| `dialect` | 可选；模型给出的方言或口音标签（如 `mandarin`、`yue`、`wu`），没有时省略 |
| `confidence` | 首要语种置信度，0 到 1 |
| `alternatives` | 其余候选，按置信度降序，最多 2 项（当前后端 beam=3），可为空数组 |
| `duration_ms` | 音频时长，毫秒 |

#### 1.10.2 后端抽象

插件内部定义语种识别后端接口，输入为预处理后的 16kHz 单声道 PCM，输出为候选语言、方言与置信度：

```python
class LIDBackend(Protocol):
    def load(self) -> None: ...
    def detect(self, pcm: np.ndarray) -> list[tuple[str, str | None, float]]: ...
```

v1 实现为 `FireRedLIDBackend`。后端选择在插件代码的工厂函数 `create_backend()` 中完成，不引入外部配置。更换后端时，新增实现类并调整工厂即可，接口、错误信封与响应格式保持不变。

v1 使用 `aufklarer/FireRedLID-887M-MLX-8bit`，这是 `FireRedTeam/FireRedLID` 的 MLX INT8 转换版本，revision 固定为 `1ae1296262c51cfd38de0f134011e6fa4b657543`。模型目录必须包含 `config.json`、`cmvn.ark`、`dict.txt`、`model.safetensors`、`frontend.py`、`mlx_model.py`。

- 目录来源：优先取环境变量 `GROOT_PLUGIN_MODELS_DIR`；未设置时取 `plugins/asr-router/models/`。两者都不存在时插件启动失败，错误信息给出下载命令。
- 获取方式：`huggingface-cli download aufklarer/FireRedLID-887M-MLX-8bit --revision 1ae1296262c51cfd38de0f134011e6fa4b657543 --local-dir <模型目录>`。权重不进入代码仓库。
- 加载方式：以唯一模块名从模型目录导入 `frontend.py` 与 `mlx_model.py`（避免与其他插件的同名模块冲突）；用 `mlx.core.load` 读取 `model.safetensors`，用 `FireRedTokenDictionary.load` 读取 `dict.txt`，构造 `FireRedLIDModel`；用 `FireRedFrontend(cmvn.ark)` 构造前端。
- 音频预处理：优先用 `soundfile` 读取其支持的格式（wav/flac/ogg），不支持的格式（mp3/m4a/webm）用 `ffmpeg` 解码为 32 位浮点 PCM；统一重采样为 16kHz、单声道后交给 `FireRedFrontend.extract_pcm`，全程在内存中完成，不产生临时文件。`ffmpeg` 只在解码 soundfile 不支持的格式时需要，写入 README。
- 特征与推理：`frontend.extract_pcm(pcm, 16000)` 得到 80 维 Kaldi fbank 特征；`model.identify_features(mx.array(features[None]), beam_size=3, nbest=3)` 返回最多 3 个假设。
- 标签归一化：假设文本形如 `zh mandarin`、`en`。第一个字段是语言代码，映射为 ISO 639-1 后作为 `language`；第二个字段若为方言或口音标签（`mandarin`、`yue`、`wu`、`xiang`、`xinan`、`north`、`min` 等），作为 `dialect` 返回，否则省略。首个假设作为主结果，其余两个作为 `alternatives`。
- `und` 判定：仅在无有效语音、解码失败或首个标签无法映射时返回 `und`。
- 平台：MLX 只在 Apple Silicon 的 macOS 上运行。插件在 `startup` 中检测平台，不满足时抛出明确错误，状态标为 `failed`；宿主、Groot 与其他插件不受影响。Linux 与 Windows 上要使用本能力，需新增 PyTorch 后端实现，放在同一个 `LIDBackend` 接口之后。

#### 1.10.3 运行特征

- 模型在 `startup` 中加载一次，就绪即代表可推理。加载耗时计入全局 `startup_timeout`，与同进程其他插件的 `startup` 时间累加；首次下载较慢时在 `plugins/plugin.yaml` 中调大。
- 推理通过 `asyncio.to_thread` 交给默认线程池，后端内部用锁保证同时只跑 1 个推理任务，避免多个请求争抢 GPU 或导致内存峰值叠加；排队请求受 Groot 首字节超时约束。该限制只作用于本插件，不影响同进程其他插件。`shutdown` 调用后端的可选 `close()` 释放资源。
- 模型的上游训练音频范围为 1.5 到 35 秒；超出范围的输入由插件在推理前拒绝。

### 1.11 目录规范与测试

仓库新增与调整：

```
groot/
├── internal/plugin/          # Go 宿主：发现、监督、代理、权限解析、状态
│   ├── sdk/                 # 嵌入二进制的 Python SDK 源码
│   ├── template/            # 嵌入二进制的 _template 与 plugin.yaml、requirements.txt 模板，供 groot plugins init 写出
│   └── *_test.go
├── plugins/                  # 插件源码
│   └── asr-router/
│       ├── requirements.txt
│       ├── README.md
│       ├── app/
│       └── models/          # 仅占位；权重不进仓库
└── tests/
    └── plugins/
        ├── sdk/             # SDK 的 pytest 测试
        └── asr-router/      # 插件的 pytest 测试
```

`groot plugins init` 写出的 `_template/`、`plugin.yaml`、`requirements.txt` 以嵌入资源的形式放在 `internal/plugin/template/`，与 SDK 一样随二进制发布；仓库的 `plugins/` 只放真实插件的源码。

同步更新 `CLAUDE.md` 的目录结构规范与测试规范，新增 `plugins/` 与 `tests/plugins/`。

| 测试类型 | 位置 |
|---|---|
| Go 宿主单元测试 | `internal/plugin/*_test.go` |
| Python SDK 单元测试 | `tests/plugins/sdk/`（pytest） |
| asr-router 单元测试 | `tests/plugins/asr-router/`（pytest） |
| 端到端系统测试 | `tests/python/`（用户执行） |

### 1.12 非目标

- 不接受插件热加载：新增或修改插件需重启 Groot。
- 运行期不自动安装依赖、不自动下载模型权重、不创建或修复虚拟环境；初始化命令 `groot plugins init` 创建虚拟环境并尝试安装基础依赖，失败时只提示手动安装。
- 不为非 Python 插件提供 SDK；协议本身与语言无关，其他语言可按协议自行实现。
- 不提供跨节点调用：每个节点只代理本机的 Python 插件进程。
- 不在 Web 界面提供管理页面；状态只通过健康检查接口与 `groot status` 查看。
- 不按插件拆分进程：所有插件共用一个进程，进程级故障影响全部插件。
- 一个插件不提供多个路径：需要多个操作时建多个插件。
- v1 的 asr-router 只在 Apple Silicon 的 macOS 上可运行；其余平台需另加 PyTorch 后端。
- 不强制资源限额（CPU、内存、GPU）；资源约束由部署环境负责。
- 不主动轮询健康：以进程存活、请求超时与重启策略作为可用性依据。

## 二、迭代说明

宿主能力为首次引入；后续命令调整见 2.1。

- 新增 Python 插件宿主：插件发现、单进程监督、就绪握手、反向代理、多实例行为。
- 新增 `/plugins/<插件名>` 路由与对应的权限点解析。
- 新增内部令牌与身份头协议，新增 `plugin_unavailable` 错误码。
- 健康检查 `checks` 新增 `plugins` 项，`groot status` 新增 Plugins 行。
- 新增嵌入式 Python SDK，启动器负责加载各插件的 `handle` 并注册为 `/<插件名>`。
- 新增第一个插件 `asr-router` 及其语种识别接口 `POST /plugins/asr-router`。
- 新增 `{GROOT_HOME}/plugins/plugin.yaml` 全局配置与 `plugins/requirements.txt` 基础依赖清单；首次引入时 `groot init` 创建 `plugins/`、`.sdk/`、`_template/` 及这两份模板。
- 目录规范新增 `plugins/` 与 `tests/plugins/`。

### 2.1 Python 插件初始化命令调整

- 新增：`groot plugins init` 命令，创建 `plugins/` 固定布局与共享虚拟环境 `.venv`，并尝试安装基础依赖。
- 移除：`groot init` 不再创建 `plugins/`；基础依赖不再要求使用者从零手动安装。
- 调整：基础依赖由初始化命令安装，失败只提示手动安装、不影响命令成功；插件业务依赖仍由使用者安装。
- 调整：启动时 `plugins/` 目录不存在、不是目录或无法访问，以及共享解释器缺失的停用文案分别说明原因并指向 `groot plugins init`。
