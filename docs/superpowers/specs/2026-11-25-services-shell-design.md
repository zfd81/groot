# groot services 进入 Python 服务环境设计文档

## 一、功能设计

### 1.1 功能概述

`groot services` 不带子命令时，打开一个继承当前终端的交互式 shell，并把使用者直接放进 Python 子服务的运行环境：工作目录为 `{GROOT_HOME}/services`，共享虚拟环境 `services/.venv` 已激活。使用者在该 shell 中执行的 `pip install`、`python`、`uvicorn` 等命令直接作用于服务环境；输入 `exit` 或在提示符处按 Ctrl+C 返回原终端（退出码 130），Ctrl+D 不再退出，退出码透传。

该功能解决的是"为子服务安装依赖、调试环境"时反复手动 `cd {GROOT_HOME}/services` 与 `source .venv/bin/activate` 的重复操作。

### 1.2 能力清单

- `groot services`：无子命令时进入服务环境 shell。
- 环境已激活：子 shell 中 `VIRTUAL_ENV` 指向 `services/.venv`，`PATH` 前置该虚拟环境的可执行目录，`python`/`pip` 直接命中共享环境。
- 工作目录固定为 `services` 根目录，不进入任何子服务目录。
- 退出方式：输入 `exit` 或在提示符处按 Ctrl+C 退出（退出码 130）；Ctrl+D 不退出；运行命令时 Ctrl+C 仍只中断该命令。
- 进入时在 stderr 打印一行提示（环境路径 + 退出方式），不污染 shell 内命令的输出。
- 共享虚拟环境缺失时：交互终端下询问是否立即初始化，确认后执行与 `groot services init` 相同的初始化流程再进入；拒绝则退出并提示手动执行。
- 非交互环境（stdin/stdout 不是字符设备）下不进入 shell，直接报错并提示可用命令。
- `groot services -h` / `--help`：显示 services 子命令帮助，含进入环境的用法与示例。
- 子命令 `init` 保持原有行为不变。

### 1.3 设计细节

**命令分派**（`cmd/groot/main.go` → `internal/cmd`）

- `groot services`（`args` 为空）调用 `cmd.RunServicesShell(...)`。
- `groot services -h|--help` 调用 `cmd.PrintServicesHelp()`，退出码 0。
- `groot services init [...]` 走现有 `RunServicesInit`。
- 其它未知子命令：报错并打印帮助，退出码 1。

**交互性判定**

仅使用标准库：对 stdin 与 stdout 分别 `Stat()`，两者均带 `os.ModeCharDevice` 时才视为交互终端；否则返回错误：

```
groot services 需要在交互终端中运行；初始化环境请执行: groot services init
```

不引入 `golang.org/x/term` 依赖。

**shell 选择与启动**

- Unix：取 `$SHELL`，为空回退 `/bin/sh`；Windows：取 `%COMSPEC%`，为空回退 `cmd.exe`。
- 以子进程启动：继承 `os.Stdin`/`os.Stdout`/`os.Stderr`，`Dir` 为 `pysvc.ServicesDir(home)`。
- 环境变量在 `os.Environ()` 基础上设置 `VIRTUAL_ENV={services}/.venv`，并把虚拟环境可执行目录（`filepath.Dir(pysvc.InterpreterPath(servicesDir))`，Unix 为 `.venv/bin`，Windows 为 `.venv/Scripts`）前置到 `PATH`（Unix 用 `:`，Windows 用 `;` 连接）。
- 子进程正常退出时返回其退出码；被信号终止时返回 1。

**用户 shell 配置覆盖共享环境的问题与处理**

交互式 shell 启动后还会执行用户自己的 rc（如 `~/.zshrc`）；若 rc 中激活了另一个虚拟环境，仅靠启动前注入的 `VIRTUAL_ENV` 会被覆盖，共享虚拟环境不再生效。为在保留用户配置的同时确保共享环境最终生效，Unix 的 zsh/bash 使用临时垫片目录：

- zsh：以临时目录作为 `ZDOTDIR`，其中写入 `.zshenv`、`.zprofile`、`.zshrc`；前两者先加载用户原目录下的同名文件，`.zshrc` 先把 `ZDOTDIR` 还原为用户原值再加载用户 `.zshrc`，最后执行共享虚拟环境的 `activate`。
- bash：以 `--rcfile` 指向临时 rc 文件，先加载 `~/.bashrc`，再执行共享虚拟环境的 `activate`。

垫片只负责加载用户配置并重新激活共享虚拟环境，子进程结束后删除临时目录；创建垫片失败时回退为只通过环境变量激活（`VIRTUAL_ENV` 与 `PATH`）。其它 shell 以及 Windows 只做环境变量激活。

**退出方式**

zsh/bash 垫片在用户配置与激活之后设置退出行为：

- zsh：`setopt MONITOR` 启用作业控制，`setopt IGNORE_EOF` 让 Ctrl+D 不退出；用 `add-zsh-hook` 注册 `preexec`/`precmd` 钩子维护「是否正在运行命令」标记（不覆盖用户已有钩子），并设置 `TRAPINT() { (( __groot_running )) || exit 130 }`。zsh 在前台命令被 Ctrl+C 中断后会向自身重发 SIGINT，因此该标记用于区分「提示符处」与「命令运行中」：提示符处退出，运行命令时只中断命令。
- bash：`set -m` 启用作业控制，`set -o ignoreeof` 让 Ctrl+D 不退出，`trap 'exit 130' INT` 让提示符处的 Ctrl+C 退出；运行命令时 SIGINT 只发给前台进程组，trap 不触发（bash 下连续 10 次 EOF 才会退出）。
- 其它 shell 与 Windows 不注入退出设置，保持 shell 自身行为；`exit` 始终可用。

**虚拟环境缺失的处理**

- 先 `os.Stat` 共享解释器：存在则直接进入。
- 不存在时向用户确认：

```
共享虚拟环境不存在: {services}/.venv
是否现在初始化？[y/N]
```

  - 回答 `y`/`Y`/`yes`：调用 `RunServicesInit(home, ServicesInitOptions{})`，初始化成功后再进入 shell；初始化失败原样返回错误。
  - 其它回答或读到 EOF：退出码 1，提示 `groot services init`。
  - `.venv` 目录存在但解释器缺失（破损环境）时，`RunServicesInit` 会返回带修复提示的错误，直接展示给用户。

**提示文本**

进入成功后向 stderr 输出：

```
已进入 Python 服务环境: {services}/.venv
工作目录: {services}（输入 exit 或 Ctrl+C 退出）
```

**帮助文本**

`PrintServicesHelp` 增加"无子命令进入环境"的说明，示例包含 `groot services` 与 `groot services init`；顶层 `printHelp` 中 services 的描述调整为"进入 Python 服务环境 / 子服务环境管理"。

**测试**

新增 `internal/cmd/services_shell_test.go`，覆盖：环境变量构造（`VIRTUAL_ENV`、`PATH` 前置、跨平台分隔符）、shell 与回退选择、非交互报错、虚拟环境缺失时的确认/拒绝分支、子进程退出码透传。通过 `RunServicesShell` 的可注入字段（获取环境变量、交互判定、进程启动函数）隔离真实 TTY 与真实 shell。

## 二、迭代说明

### 2.1 与上一版差异

- 调整：`groot services` 无子命令时的行为，由"打印帮助并以退出码 1 退出"改为"进入 Python 服务环境 shell"。
- 新增：服务环境 shell（虚拟环境激活、工作目录定位、缺失环境的交互式初始化、退出码透传）。
- 调整：查看 services 帮助改为 `groot services -h` / `--help`。
- 调整：顶层与 services 帮助文本、README 第 6 章与 7.8 节补充进入环境的用法。

### 2.2 实现方式选择

- **启动真实子 shell（采用）**：`exec` 一个 `$SHELL` 子进程并继承 TTY。交互程序（`python` 交互式、`pip` 进度条、Ctrl-C 中断、`top` 类程序）行为与普通终端完全一致，`exit` 自然回到原终端；退出键与 Ctrl+D 语义按 1.3「退出方式」由 zsh/bash 垫片调整。代价是多一层进程嵌套。
- **打印 `export`/`source` 语句由用户 `eval`**：不在目标目录中、每次都要先 `eval "$(groot services env)"`，且无法处理"进入"这种状态化体验，放弃。
- **在 Go 中实现 REPL 并转执行命令**：要自行处理行编辑、信号、终端控制与子进程交互，`pip` 的进度与交互式 `python` 都会失真，放弃。

需求决定的两点：只进入 `services` 根目录（不进入子服务目录）；进入环境用无子命令形式 `groot services` 表达，而不是单独的 `shell` 子命令。
