# Python 服务初始化命令设计文档

## 一、功能设计

### 1.1 功能概述

`groot services init` 是 Groot 的 Python 子服务环境初始化命令。它把 `{GROOT_HOME}/services/` 准备到可以直接放置并运行子服务的状态：创建固定目录布局与配置模板，创建所有子服务共用的虚拟环境 `services/.venv`，并在该环境中安装基础依赖（`services/requirements.txt` 声明的 fastapi、uvicorn、python-multipart）。命令幂等，重复执行只补齐缺失项，不覆盖使用者已有内容。

Python 子服务是 Groot 的可选能力。Groot 主服务、Web 界面与其它接口不依赖 `services/` 是否存在；未初始化时 Python 服务保持停用，初始化后重启 Groot 即生效。

Python 解释器的检查是命令的硬性前置：本机没有可用解释器或版本低于 3.9 时，初始化失败并提示安装；虚拟环境创建失败同样视为初始化失败。基础依赖安装由命令实际执行，但不保证成功：失败时打印 pip 输出与手动安装命令，命令仍以成功状态结束。依赖的后续安装与修复由使用者负责，Groot 不在运行期安装、修复依赖。

子服务的业务依赖（如 asr-router 的 mlx）不在命令范围内：它们要等子服务目录就位后才存在，由使用者在子服务目录复制完成后，用同一个虚拟环境的 pip 安装。Groot 不下载模型权重。

### 1.2 能力清单

- 创建 `{GROOT_HOME}/services/` 固定布局：`services/`、`services/.sdk/`、`services/_template/app/`、`services/service.yaml`、`services/requirements.txt`。
- 创建共享虚拟环境 `services/.venv`，目标解释器为 `services/.venv/bin/python`（Windows 为 `services/.venv/Scripts/python.exe`）。
- 自动选择可用 Python 解释器：非 Windows 依次尝试 `python3`、`python`；Windows 依次尝试 `py -3`、`python3`、`python`。
- 要求 Python 版本不低于 3.9；没有可用解释器或版本过低时初始化失败，打印各平台安装指引。
- 支持 `--py <路径>` 指定解释器，指定后只使用该解释器，不再自动探测。
- 在共享虚拟环境中安装基础依赖：执行 `<venv python> -m pip install --retries 1 --timeout 10 -r {services}/requirements.txt`。
- 已存在可用虚拟环境时跳过创建，但仍执行基础依赖安装。
- 基础依赖安装成功时打印完成；失败时打印 pip 输出末尾与手动安装命令，命令整体仍成功结束。
- 已存在的布局目录与模板文件一律跳过，不覆盖使用者维护的 `service.yaml`、`requirements.txt` 等。
- 初始化完成后打印解释器路径与版本，以及子服务业务依赖的安装命令提示。
- 允许在 `groot init` 之前单独执行：缺少 `{GROOT_HOME}` 时一并创建，并在输出中提示其余工作目录内容由 `groot init` 准备。

### 1.3 设计细节

#### 1.3.1 命令形态与帮助

命令采用两级子命令，与现有 `groot user reset` 的结构一致：

```
groot services init [--py <路径>]
groot services -h
groot services init -h
```

主帮助的子命令列表增加 `services  Python 子服务环境管理`；`groot services` 不带子命令时打印自己的帮助并以非 0 退出；未知子命令报错并打印帮助。

#### 1.3.2 执行流程

1. 打印 `初始化 Python 服务环境...`。
2. 确保 `{GROOT_HOME}` 存在；新建时打印工作目录创建成功。
3. 调用 `pysvc.EnsureLayout` 创建固定布局，逐项打印本次新建的路径；全部已存在时打印跳过说明。
4. 调用 `pysvc.EnsureVenv` 处理共享虚拟环境：
   - 目标解释器已存在且是普通文件：打印 `共享虚拟环境 .venv 已存在，跳过创建`，不执行创建命令。
   - `.venv` 目录存在但目标解释器缺失：这是损坏的环境。命令不删除、不重建，打印警告、手动修复命令与 `rm -rf` 提示，以非 0 退出。
   - 没有可用解释器或版本过低：打印探测过的候选、版本要求与各平台安装指引，以非 0 退出。
   - 解释器可用：执行 `<python> -m venv <services>/.venv`，完成后再次校验目标解释器存在；创建成功的 `.venv` 路径按用户主目录前缀缩写为 `~` 展示（不在主目录下时显示完整路径）。创建失败或校验失败时打印命令输出与手动创建命令，以非 0 退出。
5. 安装基础依赖：执行 `<venv python> -m pip install --retries 1 --timeout 10 -r <services>/requirements.txt`。
   - 开始前打印 `正在安装基础依赖（第一次可能需要几分钟）...`。
   - 虚拟环境已存在时同样执行本步骤，让缺失的依赖被补齐；pip 对已满足的依赖不重复安装。
   - 成功时打印 `基础依赖安装完成`。
   - 失败时不中断命令：打印 pip 输出末尾、警告 `基础依赖未安装成功，请手动安装` 与手动命令，继续执行第 6 步。
6. 打印使用中的解释器路径与版本，以及子服务业务依赖的安装提示：
   - `{venv}/bin/pip install -r {services}/requirements.txt`（基础依赖，仅在第 5 步失败时出现）
   - `{venv}/bin/pip install -r {services}/<子服务名>/requirements.txt`（各子服务业务依赖）
   - Windows 使用 `Scripts\pip.exe`。
   - 子服务业务依赖提示中的路径用正斜杠拼接（`<services>/<子服务名>/requirements.txt`），跨平台一致。
   - 基础依赖安装成功时以 `初始化完成` 结尾；失败时以 `初始化完成（基础依赖需手动安装）` 结尾。

布局创建失败、虚拟环境创建失败时以非 0 退出；已完成的步骤保留，不滚回。基础依赖安装失败不影响退出码。

#### 1.3.3 解释器探测与版本校验

候选按顺序逐个校验，使用第一个同时满足「可执行」与「版本 ≥ 3.9」的候选：

- 通过 `exec.LookPath` 解析可执行文件；`py -3` 作为一个带参数的命令处理。
- 版本通过 `<python> -c "import sys; print('%d.%d' % sys.version_info[:2])"` 获取并解析。
- 候选执行失败、输出无法解析、版本过低时记录原因并尝试下一个。

没有可用候选时，错误信息包含探测过的候选与失败原因，并给出安装指引：

- macOS：`brew install python@3.12`，或从 https://www.python.org/downloads/ 安装。
- Ubuntu/Debian：`sudo apt install python3 python3-venv`。
- Windows：从 https://www.python.org/downloads/ 安装，安装时勾选 `Add python.exe to PATH`。
- 已安装在非标准路径时，用 `--py <路径>` 指定。

`--py` 指定时只校验该解释器，失败直接报错，不回落自动探测。错误只包含该解释器不可用的原因，不追加平台安装指引或 `--py` 提示。

#### 1.3.4 基础依赖安装失败处理

基础依赖安装不区分失败原因：断网、软件源不可用、包版本不存在、构建失败等一律打印 pip 输出末尾（建议保留最后 20 行）与手动安装命令，不改变命令的成功状态。手动命令同时给出联网与离线两种形式：

```
{venv}/bin/pip install -r {services}/requirements.txt
{venv}/bin/pip install --no-index --find-links <wheel 目录> -r {services}/requirements.txt
```

命令不探测网络，也不判断是否配置了私有源：安装结果由 pip 自己给出，失败即提示使用者接手。Groot 运行期不重试、不修复依赖。

#### 1.3.5 错误处理与退出码

- 成功（含基础依赖安装失败）：退出码 0。
- 布局创建失败、解释器不可用或版本过低、虚拟环境创建/校验失败、环境损坏：退出码非 0，错误信息包含原因与可直接执行的手动命令。
- Debian/Ubuntu 缺少 `ensurepip` 导致 `python -m venv` 失败时，错误提示中补充 `apt install python3-venv`；若 `python -m pip` 不可用，按基础依赖安装失败处理并提示该命令。

#### 1.3.6 运行时配合

- `groot init` 不再创建 `services/` 及其任何内容。
- Groot 启动时，`{GROOT_HOME}/services/` 不可用时 Python 服务状态为 `disabled`，原因文案按情况细分，均指向 `groot services init`：
  - 目录不存在：`Python 服务未初始化: 目录 <dir> 不存在（请运行 groot services init）`。
  - 路径存在但不是目录：`Python 服务目录 <dir> 不是目录（请检查配置，或运行 groot services init）`。
  - 其它访问错误：`无法访问 Python 服务目录 <dir>: <err>（请检查权限，或运行 groot services init）`。
- 目录可用但共享解释器缺失时同样保持 `disabled`，文案为 `共享解释器缺失: <path>（请运行 groot services init 创建虚拟环境并安装依赖）`。
- 运行时不自动创建、修复虚拟环境，也不安装依赖，仍只使用 `services/.venv` 中的解释器。

#### 1.3.7 代码位置

- `internal/pysvc/venv.go`：`EnsureVenv`、解释器探测与版本校验、基础依赖安装 `InstallBaseDeps`；命令执行通过可注入的函数字段进行，便于单元测试。
- `internal/pysvc/assets.go`：`EnsureLayout` 保持现状，注释中的归属改为 `groot services init`。
- `internal/cmd/services.go`：`ParseServicesInitFlags`、`RunServicesInit`、帮助文本。
- `cmd/groot/main.go`：`services` 命令分发与主帮助。
- `internal/cmd/init.go`：移除 `EnsureLayout` 调用及相关输出，下一步提示中增加 Python 服务环境指引。

#### 1.3.8 测试

- `internal/pysvc/venv_test.go`（Go 标准测试）：
  - 解释器已存在：跳过创建，不调用 venv 命令，仍执行基础依赖安装。
  - 正常创建：写入假解释器文件后校验通过。
  - 无可用解释器：返回错误，错误信息包含候选与各平台安装指引。
  - 版本过低、候选部分可用。
  - `python -m venv` 执行失败。
  - `.venv` 存在但解释器缺失（损坏）。
  - `--py` 指定路径的行为。
  - 基础依赖安装成功。
  - 基础依赖安装失败：返回“未安装成功”状态而非错误，结果中包含 pip 输出末尾。
- `internal/cmd/services_test.go`：布局、虚拟环境与基础依赖安装的编排；重复执行的幂等性；基础依赖失败时仍成功退出并打印手动命令；Python/venv 失败时非 0 退出。命令执行器注入假实现。
- `internal/cmd/init_test.go`：删除 `services` 布局相关断言，改为断言 init 不创建 `services/`。

## 二、迭代说明

### 2.1 与上一版差异

- 新增：`groot services init` 命令，负责创建 `services/` 固定布局、`services/.venv` 共享虚拟环境，并安装基础依赖（不保证成功）。
- 移除：`groot init` 不再创建 `services/`、`.sdk/`、`_template/`、`service.yaml`、`requirements.txt`。
- 调整：`services/` 布局的创建归属从 `groot init` 改为 `groot services init`；`pysvc.EnsureLayout` 保留，改由新命令调用。
- 调整：`services/requirements.txt` 的基础依赖从「使用者手动安装」改为「由 `groot services init` 安装（不保证成功）」；安装失败只提示使用者手动安装，不影响命令成功；子服务业务依赖仍由使用者安装。
- 调整：Python 解释器检查从运行期（启动时解释器缺失即停用）前移到初始化命令：没有解释器或版本过低直接初始化失败并提示安装。
- 调整：启动时 `services/` 目录不存在、不是目录或无法访问，以及共享解释器缺失的停用原因文案分别说明原因并指向 `groot services init`；工作目录与 `.venv` 路径按用户主目录前缀缩写为 `~` 展示（不在主目录下时显示完整路径）；布局逐项显示完整路径，子服务依赖提示显示完整路径并用正斜杠拼接。
- 调整：README §7.8 与 `docs/superpowers/specs/2026-11-23-python-service-hosting-design.md` 中「`groot init` 创建布局」「使用者自行创建虚拟环境」「Groot 不执行 pip install」的表述同步改为新命令；Groot 仍不在启动时安装依赖、不安装子服务业务依赖、不下载模型、不自动修复环境。

### 2.2 方案选择

- 命令形态：在 `groot services init`、`groot init-services`、`groot init --services` 三者中选用 `groot services init`。它与现有 `groot user reset` 的两级结构一致，后续可扩展 `groot services list` 等子命令；平铺命名缺少扩展位；开关方式与「独立命令」的意图不符，且会让 `groot init` 因创建虚拟环境而变慢、依赖本机 Python。
- 创建范围：布局与虚拟环境一起由新命令负责。只建 `services/`、`.venv`、`requirements.txt` 三样会让 `service.yaml` 与 `_template/` 失去创建入口，使用者无法配置全局参数或新建子服务。
- 依赖安装：基础依赖纳入命令并实际执行安装，子服务业务依赖不纳入。基础依赖是固定的全局清单，环境一建好就该可用；业务依赖要等子服务目录就位后才存在（asr-router 不随 init 创建），命令无法预知。
- 失败边界：解释器检查与环境创建是硬性前置，失败即初始化失败并提示安装 Python；基础依赖安装是尽力而为，任何原因失败都只提示手动安装，不改变命令成功状态。依赖属于可后补、可替换的部分，使用者可自行选择镜像、离线 wheel 或指定版本；把它当成硬失败会让气隙环境或私有源配置不标准的机器无法完成初始化。
- 不增加 `--skip-install`、`--find-links` 等开关：依赖安装失败已经降级为手动处理，离线场景无需额外参数；配置私有源或本地 wheel 时直接复用 pip 自身的配置。
- 不探测网络：安装结果由 pip 给出，失败即提示手动处理，避免连通性探测因私有源、代理、内网镜像误判。
- 损坏环境处理：只警告不自动删除。`rm -rf` 属于破坏性操作，交由使用者确认执行。
