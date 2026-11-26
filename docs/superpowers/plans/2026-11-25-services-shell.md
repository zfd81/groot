# groot services 进入 Python 服务环境 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** `groot services` 不带子命令时，在 `{GROOT_HOME}/services` 下打开共享虚拟环境已激活的交互式 shell，`exit` 返回。

**Architecture:** `internal/cmd/services_shell.go` 负责解析 shell 与构造环境变量（纯函数，可直接测），并按"交互判定 → 检查共享解释器 → 必要时确认并初始化 → 启动 `$SHELL` 子进程 → 透传退出码"的顺序执行；`cmd/groot/main.go` 只做分派，帮助与文档同步更新。

**Tech Stack:** Go 1.26、标准库 `os/exec`（不引入 `golang.org/x/term`）、标准 `testing`。

**Spec:** `docs/superpowers/specs/2026-11-25-services-shell-design.md`

## Global Constraints

- 直接在 `master` 工作区开发，不创建分支或 worktree；不自动提交；每个任务结束记录悬空快照（`git write-tree` + `git commit-tree`，父提交为上一任务快照，不触碰 HEAD/分支；任务间的 tree 由 `git add -A` 后写入，记录 hash 到 `.superpowers/sdd/services-shell/progress.md`）。
- 只使用标准库，不新增模块依赖；TTY 判定用 `os.File.Stat()` 的 `os.ModeCharDevice`。
- 用户可见文案一律中文；`init` 子命令现有行为与文案不变。
- 非交互终端（stdin/stdout 任一不是字符设备）绝不启动 shell；必须退出码 1 并提示 `groot services init`。
- 环境变量只改 `VIRTUAL_ENV` 与 `PATH`，其余原样保留；`PATH` 中旧的 `VIRTUAL_ENV` 必须移除，不能残留两份。
- 共享虚拟环境路径统一为 `{GROOT_HOME}/services/.venv`，解释器统一用 `pysvc.InterpreterPath(servicesDir)` 获取；工作目录统一为 `pysvc.ServicesDir(home)`。
- `gofmt` 干净；`go test ./internal/... -count=1` 全绿。

## 文件结构

- 新建 `internal/cmd/services_shell.go`：shell 解析、环境构造、确认提示、`RunServicesShell` 主流程、默认进程启动。
- 新建 `internal/cmd/services_shell_test.go`：上述全部单元测试（复用同包 `services_test.go` 的 `servicesVenvInterpreterRel`）。
- 修改 `internal/cmd/services.go`：`PrintServicesHelp` 文案。
- 修改 `cmd/groot/main.go`：`handleServicesCommand` 无子命令分派、`printHelp` 文案。
- 修改 `README.md`：6.1 命令表、新增 6.5 并顺延编号、7.8 安装依赖示例。
- 修改 `tests/TEST_CASES.md`：新增 1.18 并更新统计汇总。

## Review Focus

- 管道/CI 中执行 `groot services`：stdin 不是终端时若照常启动 shell 会挂到 EOF 才退，用户以为命令卡死 → 必须先报错退出 1，不创建目录、不初始化。测试落在 Task 2。
- `.venv` 目录存在但解释器缺失、或解释器路径是目录：不能安静地进入一个坏环境 → 前者走确认初始化，初始化报错要带 `rm -rf` 修复提示；后者直接报错带 `rm -rf`。测试落在 Task 2。
- 已在其它 venv 中启动 groot：`VIRTUAL_ENV` 与 `PATH` 必须被覆盖且只保留一份，否则新 venv 不生效或旧 venv 优先。测试落在 Task 1。
- Windows：`%COMSPEC%` 回退、`PATH` 用 `;` 连接、环境键大小写不敏感（`Path=` 也要被替换）。测试落在 Task 1。
- 退出码透传：shell 里最后一条命令失败后 `exit`，或 `$SHELL` 无法启动时，用户拿到的退出码必须可区分（子进程码透传、启动失败返回错误+1）。测试落在 Task 2。

---

### Task 1: 环境构造、shell 解析与确认提示（纯函数）

**Files:**
- Create: `internal/cmd/services_shell.go`
- Test: `internal/cmd/services_shell_test.go`

**Interfaces:**
- Consumes: 无。
- Produces（后续任务依赖这些名字与签名）:
  - `func servicesShellEnv(parent []string, venvDir, venvBin, goos string) []string`
  - `func servicesShell(goos string, env []string) (name string, args []string)`
  - `func envValue(env []string, key string, caseInsensitive bool) string`
  - `func promptYes(in io.Reader, out io.Writer, prompt string) bool`
  - 常量 `envVirtualEnv = "VIRTUAL_ENV"`、`envPath = "PATH"`。

- [ ] **Step 1: 写失败测试**

- `TestServicesShellEnv_SetsVirtualEnvAndPrependsPath`：parent 为 `{"PATH=/usr/bin:/bin", "HOME=/u", "VIRTUAL_ENV=/old/venv"}`，`servicesShellEnv(parent, "/g/services/.venv", "/g/services/.venv/bin", "linux")` 结果中 `VIRTUAL_ENV=/g/services/.venv` 恰好 1 条、`PATH=/g/services/.venv/bin:/usr/bin:/bin` 恰好 1 条、`HOME=/u` 保留、不含 `VIRTUAL_ENV=/old/venv`。
- `TestServicesShellEnv_MissingPath`：parent 为 `{"HOME=/u"}`，linux → `PATH=<venvBin>`。
- `TestServicesShellEnv_WindowsSeparatorAndCase`：parent 为 `{"Path=C:\\Win", "VIRTUAL_ENV=old"}`，goos `windows`，venvBin `C:\\g\\services\\.venv\\Scripts` → `PATH=C:\g\services\.venv\Scripts;C:\Win` 恰好 1 条，无 `Path=`；`VIRTUAL_ENV=C:\g\services\.venv` 恰好 1 条。
- `TestServicesShell_Unix`：`{"SHELL=/bin/zsh"}` → `/bin/zsh`, args 空；无 SHELL → `/bin/sh`。
- `TestServicesShell_Windows`：`{"COMSPEC=C:\\Windows\\system32\\cmd.exe"}` → 该值；无 → `cmd.exe`。
- `TestPromptYes`：表驱动，`"y\n"`/`"Y\n"`/`"yes\n"`/`" YES \n"` 为 true；`"n\n"`/`"\n"`/`""`（EOF）为 false；每种情况断言 out 含提示文本。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/cmd/... -run 'TestServicesShellEnv|TestServicesShell_|TestPromptYes' -v`
Expected: FAIL（函数未定义 / 编译失败）。

- [ ] **Step 3: 实现**

- `servicesShellEnv`：按 `key=value` 切分 parent（无 `=` 的条目原样保留），跳过键为 `VIRTUAL_ENV`/`PATH` 的条目（goos 为 `windows` 时用 `strings.EqualFold` 比较键），先追加 `VIRTUAL_ENV=<venvDir>`，再追加 `PATH=<venvBin><sep><原PATH>`（原 PATH 为空时只写 `venvBin`；`sep` 在 windows 为 `;`，否则 `:`）。
- `servicesShell`：windows 取 `COMSPEC`、回退 `cmd.exe`；其它取 `SHELL`、回退 `/bin/sh`；args 为空切片。
- `envValue`：线性查找第一个键匹配的条目并返回其值。
- `promptYes`：`fmt.Fprint(out, prompt)` 后用 `fmt.Fscan(in, &answer)` 读一个词；`strings.ToLower` 后为 `y`/`yes` 返回 true，读取失败或其它返回 false。提示文本由调用方传入，本函数不改写。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/cmd/... -count=1`
Expected: PASS。

- [ ] **Step 5: 记录工作区快照**

`git add -A && tree=$(git write-tree) && git commit-tree "$tree" -p $(git rev-parse HEAD) -m "snapshot: services shell task1 env"`，hash 写入 `.superpowers/sdd/services-shell/progress.md`。

---

### Task 2: `RunServicesShell` 主流程与命令接入

**Files:**
- Modify: `internal/cmd/services_shell.go`
- Test: `internal/cmd/services_shell_test.go`
- Modify: `cmd/groot/main.go`（`handleServicesCommand`）

**Interfaces:**
- Consumes: Task 1 的 `servicesShellEnv`、`servicesShell`、`promptYes`；现有 `pysvc.ServicesDir`、`pysvc.InterpreterPath`、`RunServicesInit`、`ServicesInitOptions`、`shortenPath`。
- Produces:
  - `type ShellCommand struct { Name string; Args []string; Dir string; Env []string; In io.Reader; Out io.Writer; Err io.Writer }`
  - `type ServicesShellOptions struct { In io.Reader; Out io.Writer; Err io.Writer; Env []string; GOOS string; Interactive func() bool; Run func(cmd ShellCommand) (int, error); Init func(home string, out io.Writer) error }`
  - `func RunServicesShell(home string, opts ServicesShellOptions) (int, error)`
  - `func runShellCommand(cmd ShellCommand) (int, error)`（默认 `Run`）
  - `func interactiveTerminal() bool`（默认 `Interactive`）
  - `cmd/groot/main.go`：`groot services`（无参数）调用 `RunServicesShell(cmd.GetDefaultHome(), cmd.ServicesShellOptions{})`，`err != nil` 打印 `错误: <err>` 退出 1，否则 `os.Exit(code)`。

- [ ] **Step 1: 写失败测试**

辅助：`writeServicesInterpreter(t, home)` 用 `servicesVenvInterpreterRel()` 在 `<home>/services/.venv/<rel>` 写出可执行文件。

- `TestRunServicesShell_NonInteractiveReturnsError`：`Interactive` 返回 false；断言 code 1、error 含 `交互终端`、含 `groot services init`，且 `Run`/`Init` 均未被调用。
- `TestRunServicesShell_EntersActivatedVenv`：建好解释器；`Env` 为 `{"PATH=/usr/bin", "SHELL=/bin/zsh"}`、`GOOS=linux`、`Interactive=true`、`Run` 记录入参并返回 `(0, nil)`；断言 `Name`/`Dir`/`Env`（`VIRTUAL_ENV`、`PATH` 前缀为 `<venv>/bin`）、`In` 是传入的 reader；`Err` 缓冲含 `已进入 Python 服务环境`、venv 路径、`工作目录`、`exit`；code 0、err nil。
- `TestRunServicesShell_PropagatesExitCode`：`Run` 返回 `(7, nil)` → code 7、err nil。
- `TestRunServicesShell_RunFailureReturnsError`：`Run` 返回 `(0, errors.New("boom"))` → code 1、err 含 `boom`。
- `TestRunServicesShell_MissingVenvDeclined`：空 home，`In` 为 `"n\n"`；`Init` 记录调用；断言 Init 未被调用、code 1、err 含 `groot services init`、Err 缓冲含 `共享虚拟环境不存在` 与 `[y/N]`。
- `TestRunServicesShell_MissingVenvInitialized`：空 home，`In` 为 `"y\n"`；`Init` 收到 `home` 与 writer，并写入解释器文件；断言 Init 调用 1 次、随后 `Run` 被调用、code 0。
- `TestRunServicesShell_InitFailureReturnsError`：`Init` 返回含 `rm -rf` 的错误 → code 1、err 含 `rm -rf`、`Run` 未被调用。
- `TestRunServicesShell_BrokenVenvInterpreterIsDir`：把解释器路径建成目录 → code 1、err 含 `缺少解释器` 与 `rm -rf`、`Init` 未被调用。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/cmd/... -run 'TestRunServicesShell' -v`
Expected: FAIL（类型/函数未定义）。

- [ ] **Step 3: 实现**

`RunServicesShell` 顺序固定为：

1. 取默认值（`In`/`Out`/`Err` 为 nil 时用 `os.Stdin`/`os.Stdout`/`os.Stderr`；`Env` 为 nil 时用 `os.Environ()`；`GOOS` 为空时用 `runtime.GOOS`；`Interactive`/`Run`/`Init` 为 nil 时用默认实现；默认 `Init` 调用 `RunServicesInit(home, ServicesInitOptions{Out: out})`）。
2. `!interactive()` → 返回 `(1, errors.New("groot services 需要在交互终端中运行；初始化环境请执行: groot services init"))`。
3. 计算 `servicesDir`、`venvDir`、`interp`；`os.Stat(interp)`：
   - 成功且 `IsDir()` → `(1, fmt.Errorf("共享虚拟环境已存在但缺少解释器: %s；如需重建，请删除该虚拟环境: rm -rf %s", interp, venvDir))`。
   - 成功且非目录 → 继续。
   - `os.IsNotExist` → `promptYes(in, errOut, fmt.Sprintf("共享虚拟环境不存在: %s\n是否现在初始化？[y/N] ", venvDir))`；拒绝 → `(1, errors.New("未初始化服务环境：请先执行 groot services init"))`；同意 → 调 `init(home, out)`，其错误原样返回 `(1, err)`；再 stat 解释器，仍不存在 → `(1, fmt.Errorf("初始化后仍未找到共享解释器: %s（请运行 groot services init 查看详情）", interp))`。
   - 其它错误 → `(1, fmt.Errorf("无法访问共享解释器 %s: %w", interp, err))`。
4. `env = servicesShellEnv(env, venvDir, filepath.Dir(interp), goos)`；`name, args := servicesShell(goos, env)`。
5. 向 `errOut` 打印 `已进入 Python 服务环境: <shortenPath(venvDir, true)>` 与 `工作目录: <shortenPath(servicesDir, true)>（输入 exit 退出）`。
6. 返回 `run(ShellCommand{...})`。

默认 `runShellCommand`：`exec.Command(cmd.Name, cmd.Args...)`，设置 `Dir`/`Env`/`Stdin`/`Stdout`/`Stderr`；`Run()` 成功返回 `(0, nil)`，`*exec.ExitError` 返回 `(ee.ExitCode(), nil)`，其它错误返回 `(1, err)`。

默认 `interactiveTerminal`：`os.Stdin` 与 `os.Stdout` 分别 `Stat()`，二者 `Mode()&os.ModeCharDevice != 0` 才算交互。

`cmd/groot/main.go` 的 `handleServicesCommand`：`len(args)==0` 时按 Interfaces 调用并 `os.Exit(code)`；其余分支（`-h/--help`、`init`、未知子命令）保持不变。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/cmd/... -count=1`
Expected: PASS。

Run: `go build -o dist/groot ./cmd/groot && ./dist/groot services < /dev/null; echo "exit=$?"`
Expected: stderr 打印 `需要在交互终端中运行` 与 `groot services init`，`exit=1`；不创建 `~/.groot/services`。

- [ ] **Step 5: 记录工作区快照**

父提交为 Task 1 快照，消息 `snapshot: services shell task2 run`。

---

### Task 3: 帮助文本、README 与测试用例汇总

**Files:**
- Modify: `internal/cmd/services.go`（`PrintServicesHelp`）
- Modify: `cmd/groot/main.go`（`printHelp`）
- Modify: `README.md`
- Modify: `tests/TEST_CASES.md`

**Interfaces:**
- Consumes: Task 2 的命令形态与退出码语义。
- Produces: 帮助/文档与实现一致。

- [ ] **Step 1: 更新帮助文本**

`PrintServicesHelp` 输出改为（保留既有风格）：

```
用法: groot services [子命令]

进入 Python 子服务环境：不带子命令时在 {GROOT_HOME}/services 下打开已激活共享虚拟环境的交互式 shell，输入 exit 退出。

子命令:
  init            初始化 Python 服务环境（目录布局、共享虚拟环境、基础依赖）

选项:
  -h, --help      显示帮助

示例:
  groot services                     # 进入服务环境（exit 退出）
  groot services init                # 初始化环境
  groot services init --py /usr/local/bin/python3.12
```

`cmd/groot/main.go` 的 `printHelp`：`services` 行说明改为 `进入 Python 服务环境 / 子服务环境管理`；`services 子命令` 段首行插入 `无子命令          进入 Python 服务环境（工作目录 {GROOT_HOME}/services，共享虚拟环境已激活）`；示例段加 `groot services                   # 进入 Python 服务环境`。

- [ ] **Step 2: 更新 README**

- 1168 行引言补充"进入 Python 子服务环境"。
- 6.1 命令总览表在 `groot services init` 前加一行：`groot services` | 进入 Python 子服务环境（工作目录为 `services`，共享虚拟环境已激活，`exit` 退出）。
- 新增 `### 6.5 进入 Python 子服务环境（groot services）`，内容：用法 `groot services`、激活的变量（`VIRTUAL_ENV`、`PATH`）、工作目录、`exit` 退出与退出码透传、`.venv` 缺失时的确认初始化、非交互终端报错；并注明该命令不进入子服务目录、装依赖示例 `groot services` 后 `pip install -r asr-router/requirements.txt`。原有 6.5/6.6/6.7 顺延为 6.6/6.7/6.8（标题编号同步，正文无交叉引用）。
- 7.8 "安装运行环境与依赖"：示例改为先 `groot services init`，再 `groot services` 进入环境执行 `pip install -r asr-router/requirements.txt`；保留原绝对路径 `.venv/bin/pip` 写法作为不进入 shell 的替代。

- [ ] **Step 3: 更新测试用例汇总**

- 在 1.17 后新增 `### 1.18 Python 服务环境进入命令测试`：说明位于 `internal/cmd/services_shell_test.go`，对应实现 `internal/cmd/services_shell.go`，逐条列出 Task 1/2 的测试函数名与覆盖点（环境覆盖与 PATH 前置、Windows 分隔符与键大小写、SHELL/COMSPEC 回退、确认输入、非交互报错、缺失环境确认/初始化、破损环境、退出码透传）。
- 重算 Go 测试数：`grep -rn "^func Test" internal | wc -l`、`grep -rln "^func Test" internal | wc -l`，更新"六、统计汇总"的 Go 行与总计。

- [ ] **Step 4: 验证**

Run: `gofmt -l internal cmd`
Expected: 无输出。

Run: `go test ./internal/... -count=1`
Expected: PASS。

Run: `./dist/groot services -h`（先 `go build -o dist/groot ./cmd/groot`）
Expected: 显示含 `进入 Python 子服务环境`、`groot services                     # 进入服务环境` 与 `init` 的帮助，退出码 0。

Run: `grep -n "groot services" README.md | head -20`
Expected: 6.1 表格、6.5 小节、7.8 示例均出现 `groot services`（无子命令形式）。

- [ ] **Step 5: 记录工作区快照**

父提交为 Task 2 快照，消息 `snapshot: services shell task3 docs`。
