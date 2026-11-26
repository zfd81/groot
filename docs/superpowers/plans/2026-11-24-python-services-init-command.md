# Python 服务初始化命令 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增 `groot services init`，创建 `{GROOT_HOME}/services/` 固定布局与共享虚拟环境 `.venv`，安装基础依赖；`groot init` 不再创建 `services/`。

**Architecture:** 在 `internal/pysvc` 新增 `EnsureVenv`（解释器探测、版本校验、venv 创建）与 `InstallBaseDeps`（pip 安装，失败降级）；在 `internal/cmd` 新增 `services init` 的解析与编排，`cmd/groot/main.go` 增加两级命令分发；启动时 `Manager` 的停用文案指向新命令。

**Tech Stack:** Go 1.26、`os/exec`、标准 `testing`；无新增第三方依赖。

**Spec:** `docs/superpowers/specs/2026-11-24-python-services-init-command-design.md`

## Global Constraints

- 一律直接在 `master` 工作区开发，不创建分支或 worktree。
- 不自动提交；每个任务结束用悬空快照对象记录工作区状态，全部改动留在工作区。
- 进度账本写在 `.superpowers/sdd/python-services-init-command/progress.md`（已 gitignore），整体评审通过后删除。
- Python 最低版本 3.9；非 Windows 候选顺序 `python3`、`python`；Windows 为 `py -3`、`python3`、`python`。
- `--py <路径>` 指定后只使用该解释器，失败直接报错，不回落自动探测。
- 共享环境固定为 `{GROOT_HOME}/services/.venv`，解释器路径由 `pysvc.InterpreterPath` 决定。
- 基础依赖安装命令固定为 `<venv python> -m pip install --retries 1 --timeout 10 -r <services>/requirements.txt`。
- Python 缺失/版本过低、布局创建失败、虚拟环境创建或校验失败、`.venv` 损坏：命令非 0 退出。
- 基础依赖安装失败：打印 pip 输出末尾与手动命令，命令仍成功结束（退出码 0），不删除、不回滚已建内容。
- 不自动删除损坏的 `.venv`，只提示手动 `rm -rf`。
- 不新增 Python 测试；只写 Go 单元测试，运行 `go test ./internal/... -v`。
- 文档遵守 `CLAUDE.md` 的两段式结构：功能设计用正面陈述，差异只写在迭代说明。

## Review Focus

- 已存在 `.venv` 但解释器缺失，或解释器路径被建成目录：必须硬失败、不删除、不重建，错误里给出手动修复命令。测试落在 Task 1。
- `--py` 指向不存在或不可执行的解释器：必须报错，不能回落到 `python3`/`python`。测试落在 Task 1。
- 基础依赖安装失败（断网、坏 requirements、源异常）：命令仍成功，布局与 `.venv` 保留，打印 pip 输出末尾与联网/离线两种手动命令。测试落在 Task 2 与 Task 3。
- 重复执行：用户自定义的 `service.yaml` 不被覆盖，`.venv` 不重建，但基础依赖安装仍执行。测试落在 Task 3。
- 版本边界 3.8 与 3.9、Windows `py -3` 候选：3.8 被跳过并尝试下一个，全部不满足时报错并带安装指引。测试落在 Task 1。

---

### Task 1: 共享虚拟环境探测与创建

**Files:**
- Create: `internal/pysvc/venv.go`
- Create: `internal/pysvc/venv_test.go`
- Modify: `internal/pysvc/discover.go`（`InterpreterPath` 改为 `interpreterPath(servicesDir, runtime.GOOS)` 的薄封装）

**Interfaces:**
- Consumes: `InterpreterPath(servicesDir string) string`（`internal/pysvc/discover.go`）。
- Produces:
  - `const MinPythonMajor = 3`、`const MinPythonMinor = 9`
  - `type CommandRunner interface { LookPath(file string) (string, error); Run(ctx context.Context, name string, args ...string) ([]byte, error) }`
  - `type VenvOptions struct { Python string; GOOS string; Runner CommandRunner }`（`GOOS`/`Runner` 为空时分别取 `runtime.GOOS` 与真实执行）
  - `type VenvResult struct { Interpreter string; Version string; Created bool }`
  - `func EnsureVenv(servicesDir string, opts VenvOptions) (VenvResult, error)`
  - `func VenvPipPath(servicesDir string) string`（非 Windows `<services>/.venv/bin/pip`，Windows `<services>/.venv/Scripts/pip.exe`）
  - 包内 `func interpreterPath(servicesDir, goos string) string` 与 `func venvPipPath(servicesDir, goos string) string`（venv.go）

- [ ] **Step 1: 写失败测试**

`venv_test.go` 使用包内假执行器，记录调用并按测试需要返回输出：

```go
type venvFakeRunner struct {
	paths map[string]string
	calls []string
	lookups []string
	run   func(name string, args ...string) ([]byte, error)
}

func (f *venvFakeRunner) LookPath(file string) (string, error) {
	f.lookups = append(f.lookups, file)
	if p, ok := f.paths[file]; ok {
		return p, nil
	}
	return "", fmt.Errorf("exec: %q: executable file not found in $PATH", file)
}

func (f *venvFakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if f.run != nil {
		return f.run(name, args...)
	}
	return nil, nil
}
```

测试与断言：

- `TestEnsureVenv_ExistingVenv`：在 `InterpreterPath(servicesDir)` 写入文件；版本查询返回 `3.12`；断言 `Created == false`、`Interpreter` 等于该路径、`Version == "3.12"`，且没有任何调用包含 `-m venv`。
- `TestEnsureVenv_ExistingVenvVersionUnknown`：解释器文件存在、版本查询报错；断言 error 为 nil、`Created == false`、`Version == ""`。
- `TestEnsureVenv_Creates`：`LookPath("python3")` 返回 `/usr/bin/python3`，版本返回 `3.12`；`Run` 遇到 `-m venv` 时创建 `InterpreterPath` 文件。断言 `Created == true`、`Version == "3.12"`、文件存在，`calls` 含 `/usr/bin/python3 -m venv <services>/.venv`。
- `TestEnsureVenv_BrokenVenv`：只创建 `<services>/.venv` 目录；断言 error 含 `已存在` 与 `rm -rf`，且 `lookups` 为空。
- `TestEnsureVenv_InterpreterIsDirectory`：把 `InterpreterPath` 建成目录；断言 error 含 `已存在` 与 `rm -rf`。
- `TestEnsureVenv_NoPython`：所有 `LookPath` 失败；断言 error 同时含 `python3`、`python`、`3.9` 与安装指引关键字（`brew install` 或 `python.org` 或 `apt install`）。
- `TestEnsureVenv_VersionTooLow`：`python3` 版本 `3.8`、`python` 不存在；断言 error 含 `3.8` 与 `3.9`。
- `TestEnsureVenv_FallbackToSecondCandidate`：`python3` 版本 `3.8`、`python` 版本 `3.11`；断言创建成功，venv 命令使用 `/usr/local/bin/python`。
- `TestEnsureVenv_ExplicitPythonNoFallback`：`Python` 设为 `/opt/py3` 且 `LookPath` 失败；断言 error 含 `/opt/py3`，且没有查询 `python3`/`python`。
- `TestEnsureVenv_VenvCreationFails`：venv 命令返回输出 `Error: ensurepip is not available` 与 error；断言 error 含该输出与 `apt install python3-venv`。
- `TestEnsureVenv_MissingInterpreterAfterCreate`：venv 命令返回 nil 但不创建文件；断言 error 含 `创建后未找到解释器`。
- `TestEnsureVenv_WindowsCandidates`：`GOOS: "windows"`，`py` 解析为 `C:\py.exe`、版本 `3.12`，venv 命令创建 `filepath.Join(servicesDir, ".venv", "Scripts", "python.exe")`；断言 `Created`、`Interpreter` 等于该路径，调用含 `C:\py.exe -3 -m venv`。
- `TestVenvPipPath`：用内部 `venvPipPath(servicesDir, goos)` 表驱动：`darwin`/`linux` → `.venv/bin/pip`，`windows` → `.venv/Scripts/pip.exe`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/pysvc/... -run 'TestEnsureVenv|TestVenvPipPath' -v`
Expected: FAIL，`undefined: EnsureVenv`、`undefined: VenvPipPath`。

- [ ] **Step 3: 实现 `internal/pysvc/venv.go`**

语义：

1. 默认执行器 `execRunner` 用 `exec.LookPath` 与 `exec.CommandContext(...).CombinedOutput()`。
2. `EnsureVenv` 用 `interpreterPath(servicesDir, opts.goos())` 定位解释器；`Run` 调用内部使用 `context.Background()`。先看该路径：存在且不是目录 → 版本查询尽力而为（失败时 `Version=""`），直接返回 `Created=false`。
3. 解释器 stat 返回非 `IsNotExist` 错误、解释器路径是目录、或 `<services>/.venv` 已存在但解释器缺失 → 返回错误，文案含 `已存在但缺少解释器`、目标路径与 `rm -rf <venvDir>`，不删除任何内容。
4. 候选生成：`opts.Python` 非空时只有它；否则按 `opts.goos()` 返回平台候选。每个候选 `LookPath` 后执行 `<path> <prefix> -c "import sys; print('%d.%d' % sys.version_info[:2])"`，解析 `major.minor`；`(major, minor) < (3, 9)` 记为跳过原因。
5. 全部候选不可用时返回聚合错误：逐条列出候选与原因，追加按平台区分的安装指引（macOS `brew install python@3.12` / python.org；Ubuntu/Debian `sudo apt install python3 python3-venv`；Windows python.org 并勾选 PATH），并提示 `--py <路径>`。
6. 执行 `<selected> <prefix> -m venv <venvDir>`；失败时错误包含命令输出；输出含 `ensurepip` 时追加 `apt install python3-venv` 提示。
7. 创建后再次 stat 解释器；缺失时返回 `创建后未找到解释器: <path>`。
8. 返回 `VenvResult{Interpreter, Version, Created: true}`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/pysvc/... -run 'TestEnsureVenv|TestVenvPipPath' -v`
Expected: PASS。

- [ ] **Step 5: 记录工作区快照**

```bash
cd /Users/bonc/workspace/groot
snap=$(mktemp -u /tmp/groot-index.XXXXXX)
GIT_INDEX_FILE="$snap" git read-tree HEAD
GIT_INDEX_FILE="$snap" git add -A
tree=$(GIT_INDEX_FILE="$snap" git write-tree)
git commit-tree "$tree" -p HEAD -m "snapshot: task1 pysvc venv"
rm -f "$snap"
```

---

### Task 2: 基础依赖安装与失败降级

**Files:**
- Modify: `internal/pysvc/venv.go`
- Modify: `internal/pysvc/venv_test.go`

**Interfaces:**
- Consumes: Task 1 的 `CommandRunner`。
- Produces:
  - `type DepsResult struct { OK bool; Tail string }`
  - `func InstallBaseDeps(ctx context.Context, venvPython, requirementsPath string, runner CommandRunner) DepsResult`
  - 包内 `func tailLines(s string, n int) string`

- [ ] **Step 1: 写失败测试**

- `TestInstallBaseDeps_Success`：假 `Run` 返回 nil；断言 `OK == true`、`Tail == ""`；调用记录恰为 `/venv/bin/python -m pip install --retries 1 --timeout 10 -r /services/requirements.txt`。
- `TestInstallBaseDeps_FailureKeepsTail`：输出 25 行 `line 1`…`line 25` 并以换行结尾，返回 error；断言 `OK == false`，`Tail` 含 `line 6` 与 `line 25`，不含 `line 5`。
- `TestInstallBaseDeps_EmptyOutput`：输出为空、返回 error；断言 `OK == false`、`Tail == ""`。
- `TestTailLines`：表驱动覆盖 0 行、少于 n、恰好 n、多于 n、尾部换行；n 固定 20。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/pysvc/... -run 'TestInstallBaseDeps|TestTailLines' -v`
Expected: FAIL，`undefined: InstallBaseDeps`、`undefined: tailLines`。

- [ ] **Step 3: 实现 `InstallBaseDeps` 与 `tailLines`**

`InstallBaseDeps`：runner 为 nil 时用 `execRunner{}`；参数固定 `-m pip install --retries 1 --timeout 10 -r <requirementsPath>`；成功返回 `DepsResult{OK: true}`；失败返回 `DepsResult{OK: false, Tail: tailLines(string(out), 20)}`，不返回 error。

`tailLines`：按 `\n` 切分，去掉尾部空行，超过 n 行时只保留最后 n 行，重新用 `\n` 连接。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/pysvc/... -run 'TestInstallBaseDeps|TestTailLines' -v`
Expected: PASS。

- [ ] **Step 5: 记录工作区快照**

复用 Task 1 的快照命令，消息改为 `snapshot: task2 pysvc deps install`。

---

### Task 3: `groot services init` 命令与分发

**Files:**
- Create: `internal/cmd/services.go`
- Create: `internal/cmd/services_test.go`
- Modify: `cmd/groot/main.go`（命令分发、`handleServicesCommand`、`printHelp`）

**Interfaces:**
- Consumes: `pysvc.EnsureLayout`、`pysvc.ServicesDir`、`pysvc.EnsureVenv`、`pysvc.InstallBaseDeps`、`pysvc.VenvPipPath`、`pysvc.CommandRunner`；同包 `createDir`、`shortenPath`。
- Produces:
  - `type ServicesInitFlags struct { Python string }`
  - `type ServicesInitOptions struct { Python string; Runner pysvc.CommandRunner }`
  - `func ParseServicesInitFlags(args []string) (*ServicesInitFlags, error)`
  - `func RunServicesInit(homeDir string, opts ServicesInitOptions) error`
  - `func PrintServicesHelp()`、`func PrintServicesInitHelp()`

- [ ] **Step 1: 写失败测试**

`services_test.go` 使用包内假执行器（记录调用；`Run` 可按参数创建解释器或返回指定错误）：

```go
type servicesFakeRunner struct {
	paths map[string]string
	calls []string
	lookups []string
	run   func(name string, args ...string) ([]byte, error)
}

func (f *servicesFakeRunner) LookPath(file string) (string, error) {
	f.lookups = append(f.lookups, file)
	if p, ok := f.paths[file]; ok {
		return p, nil
	}
	return "", fmt.Errorf("exec: %q: executable file not found in $PATH", file)
}
func (f *servicesFakeRunner) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	return f.run(name, args...)
}
```

测试与断言：

- `TestParseServicesInitFlags`：`[]` → Python 为空；`["--py","/opt/python3.12"]` → 该值；`["--py"]` → `--py requires a value`；`["--invalid"]` → `unknown flag: --invalid`；`["extra"]` → `unexpected argument: extra`。
- `TestRunServicesInit_CreatesLayoutVenvAndInstallsDeps`：临时 home；`python3` 版本 `3.12`，venv 调用创建解释器，pip 成功。断言 `services/service.yaml`、`services/requirements.txt`、`services/_template/app/main.py`、`services/.sdk`、解释器均存在；调用记录含 `-m venv` 与 `-m pip install`；返回 nil。
- `TestRunServicesInit_CreatesHomeWhenMissing`：home 用 `filepath.Join(t.TempDir(), "groot")`；断言执行后 home 与 `services/` 存在。
- `TestRunServicesInit_Idempotent`：执行两次；第二次前把 `service.yaml` 改为 `# user-customized-marker\n`。断言 marker 保留、venv 创建调用只有 1 次、pip 调用 2 次。
- `TestRunServicesInit_DepsFailureReturnsNil`：pip 调用返回 error 与多行输出；断言返回 nil，布局与解释器仍存在。
- `TestRunServicesInit_PythonMissingReturnsError`：所有 `LookPath` 失败；断言 error 含 `python` 与 `3.9`。
- `TestRunServicesInit_BrokenVenvReturnsError`：预建 `services/.venv` 目录；断言 error 含 `rm -rf`。
- `TestRunServicesInit_UsesExplicitPython`：`Python: "/opt/py3"`；断言 venv 调用使用该路径，且未查询 `python3`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/cmd/... -run 'TestParseServicesInitFlags|TestRunServicesInit' -v`
Expected: FAIL，`undefined: ParseServicesInitFlags`、`undefined: RunServicesInit`。

- [ ] **Step 3: 实现 `internal/cmd/services.go`**

- `ParseServicesInitFlags`：支持 `--py <值>`、`-h/--help`（打印帮助并 `os.Exit(0)`，与其它命令一致）；缺值、未知 flag、多余位置参数返回中文错误。
- `RunServicesInit` 顺序：
  1. 打印 `初始化 Python 服务环境...` 与空行。
  2. home 不存在时用 `createDir(homeDir, "工作目录", true)` 创建，并记下“本次新建”；已存在时不要打印跳过提示（调用 `os.Stat` 判断，避免 `createDir` 的“已存在”噪音）。
  3. `pysvc.EnsureLayout(homeDir)`；逐项打印 `Python 服务 <路径> 创建成功`，全部存在时打印 `Python 服务目录布局已存在，跳过创建`；失败直接返回错误。
  4. `pysvc.EnsureVenv(servicesDir, pysvc.VenvOptions{Python: opts.Python, Runner: opts.Runner})`；失败返回错误。成功时 `Created` 为 true 打印 `共享虚拟环境 <venvDir> 创建成功`，否则打印 `共享虚拟环境 .venv 已存在，跳过创建`。
  5. 打印 `正在安装基础依赖（第一次可能需要几分钟）...`，调用 `pysvc.InstallBaseDeps(context.Background(), venv.Interpreter, filepath.Join(servicesDir, "requirements.txt"), opts.Runner)`。
  6. `DepsResult.OK` 为 true → 打印 `基础依赖安装完成`；否则依次打印 `基础依赖未安装成功，请手动安装`、`Tail`（非空时原样打印）、联网命令 `<venvPip> install -r <requirements>`、离线命令 `<venvPip> install --no-index --find-links <wheel 目录> -r <requirements>`。`Tail` 含 `No module named pip` 时追加 `缺少 pip 时可执行 apt install python3-venv（Debian/Ubuntu）`。
  7. 打印 `解释器: <Interpreter>`（`Version` 非空时追加 ` (<Version>)`）与子服务业务依赖提示 `<venvPip> install -r <services>/<子服务名>/requirements.txt`。
  8. home 本次新建时打印 `工作目录其余内容由 groot init 准备（skills/mcp/subagents/logs、bootstrap.yaml、GROOT.md）`。
  9. 最后一行：依赖成功 `初始化完成`；失败 `初始化完成（基础依赖需手动安装）`。
- `PrintServicesHelp`：用法 `groot services <子命令>`；子命令 `init`；示例 `groot services init`、`groot services init --py /usr/local/bin/python3.12`。
- `PrintServicesInitHelp`：用法 `groot services init [选项]`；说明 Python 要求 3.9+、基础依赖失败只提示手动安装；选项 `--py <路径>`、`-h, --help`。
- `cmd/groot/main.go`：`switch` 增加 `case "services": handleServicesCommand(args[1:])`；`handleServicesCommand` 在无参数、未知子命令时打印 `PrintServicesHelp` 并 `os.Exit(1)`，`init` 子命令解析后调用 `RunServicesInit(GetDefaultHome(), ...)`，错误打印 `错误: ...` 并 `os.Exit(1)`；`-h/--help` 打印帮助。
- `printHelp` 增加子命令行 `services          Python 子服务环境管理`、`services 子命令` 段（`init`、`--py`）与示例 `groot services init`。

- [ ] **Step 4: 运行单元测试确认通过**

Run: `go test ./internal/cmd/... -run 'TestParseServicesInitFlags|TestRunServicesInit' -v`
Expected: PASS。

- [ ] **Step 5: 验证命令分发**

Run: `go run ./cmd/groot services init -h` → 打印 init 帮助，退出码 0。
Run: `go run ./cmd/groot services` → 打印 services 帮助，退出码 1（shell 中 `echo $?` 验证）。
Run: `go run ./cmd/groot --help | grep services` → 含 `groot services init` 示例。

- [ ] **Step 6: 记录工作区快照**

复用快照命令，消息 `snapshot: task3 services init command`。

---

### Task 4: `groot init` 不再创建 services

**Files:**
- Modify: `internal/cmd/init.go`
- Modify: `internal/cmd/init_test.go`
- Modify: `internal/pysvc/assets.go`（注释）
- Modify: `internal/pysvc/config.go`（注释）

**Interfaces:**
- Consumes: 无。
- Produces: `RunInit(homeDir string) error` 行为变更为不创建 `services/`；签名不变。

- [ ] **Step 1: 调整测试（先失败）**

- 删除 `TestRunInit_CreatesServicesLayout`、`TestRunInit_SkipsExistingServicesFile`（对应覆盖迁到 `services_test.go`）。
- 新增 `TestRunInit_NoServicesLayout`：`RunInit(home)` 后断言 `os.Stat(filepath.Join(home, "services"))` 返回 `IsNotExist`。
- 保留其余 init 测试不动。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/cmd/... -run TestRunInit_NoServicesLayout -v`
Expected: FAIL，`init 不应创建 services 目录`。

- [ ] **Step 3: 修改 `RunInit` 与注释**

- 删除 `RunInit` 中 `pysvc.EnsureLayout` 代码块与 `pysvc` import。
- `printNextSteps` 在现有 4 条后增加第 5 条：`（可选）启用 Python 子服务：groot services init`。
- `internal/pysvc/assets.go` 的 `layoutDirs`/`layoutFiles` 注释把 `groot init` 改为 `groot services init`。
- `internal/pysvc/config.go` 中 `全注释模板（groot init 写入）` 改为 `全注释模板（groot services init 写入）`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/cmd/... -run TestRunInit -v`
Expected: PASS。

- [ ] **Step 5: 记录工作区快照**

复用快照命令，消息 `snapshot: task4 init stops services layout`。

---

### Task 5: 启动停用文案指向新命令

**Files:**
- Modify: `internal/pysvc/manager.go`
- Modify: `internal/pysvc/manager_test.go`

**Interfaces:**
- Consumes: `ServicesDir` 与 `Manager.servicesDir`。
- Produces: 包内 `func (m *Manager) disabledReason() string`；`ProcessStatus.Error` 文案变化。

- [ ] **Step 1: 写失败测试**

- 新增 `TestManager_UninitializedServicesDisabled`：`home := t.TempDir()`（不建 services 目录），`NewManager(managerOptions(home))` 后 `Start`；断言 `Process.Status == "disabled"`、`Process.Error` 含 `groot services init`、`Services` 为空、runner 未启动。
- 修改 `TestManager_InterpreterMissingDisables`：追加断言 `strings.Contains(status.Process.Error, "groot services init")`。

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/pysvc/... -run 'TestManager_UninitializedServicesDisabled|TestManager_InterpreterMissingDisables' -v`
Expected: FAIL，错误文案不含 `groot services init`。

- [ ] **Step 3: 实现文案**

- 新增 `disabledReason()`：`servicesDir` 不存在时返回 `Python 服务未初始化: 目录 <dir> 不存在（请运行 groot services init）`，否则返回 `noServiceReason`。
- `NewManager` 与 `Start` 设置 `disabled` 时统一使用 `m.disabledReason()`。
- 解释器缺失文案改为 `共享解释器缺失: <path>（请运行 groot services init 创建虚拟环境并安装依赖）`。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/pysvc/... -run 'TestManager_UninitializedServicesDisabled|TestManager_InterpreterMissingDisables' -v`
Expected: PASS。

- [ ] **Step 5: 记录工作区快照**

复用快照命令，消息 `snapshot: task5 manager uninitialized reason`。

---

### Task 6: 文档与测试用例汇总同步

**Files:**
- Modify: `README.md`
- Modify: `docs/superpowers/specs/2026-11-23-python-service-hosting-design.md`
- Modify: `tests/TEST_CASES.md`

**Interfaces:**
- Consumes: 前五个任务确定的命令行为与文案。
- Produces: 文档与实现一致。

- [ ] **Step 1: 更新 README**

- 2.3 目录树注释：`services/` 改为 `# Python 子服务根目录（groot services init 创建布局）`，`.venv/` 改为 `# 共享虚拟环境，由 groot services init 创建`。
- 2.4 固定目录表：`services/` 行的“`groot init` 创建布局”改为“`groot services init` 创建布局”。
- 3.1 初始化：追加一句“如需 Python 子服务能力，执行 `groot services init` 创建布局、共享虚拟环境并安装基础依赖（可选）。”
- 6.1 命令总览表：`groot init` 行后插入 `| groot services init | 初始化 Python 子服务环境（布局、共享虚拟环境、基础依赖） |`。
- 6.3 init 表格：删除 `services/` 行；表格后加注“Python 子服务环境由 `groot services init` 单独初始化。”
- 新增 `### 6.4 初始化 Python 子服务环境（groot services init）`：命令用法、`--py`、创建内容、Python 3.9+ 要求、基础依赖失败降级与手动命令、退出码；原 6.4/6.5/6.6 依次改为 6.5/6.6/6.7。
- 7.8 开头：`groot init` 改 `groot services init`；目录树 `.venv/` 注释改为“由 groot services init 创建”；“`groot init` **不会**创建 `services/asr-router`”中的命令名同步改为 `groot services init`。
- 7.8「安装运行环境与依赖」整段替换为：
  - 执行 `groot services init` 创建布局与共享环境并安装基础依赖；
  - 基础依赖失败只提示手动安装，命令仍成功；
  - 子服务业务依赖由使用者手动安装（保留 asr-router 的 pip 命令示例）；
  - 缺少 `.venv` 时服务停用，原因提示运行 `groot services init`；
  - 删除 `python3 -m venv ...` 手动建环境示例。

- [ ] **Step 2: 更新旧设计文档**

`docs/superpowers/specs/2026-11-23-python-service-hosting-design.md`：

- 1.1 第 13 行：`groot init` 改为 `groot services init`。
- 1.4.1 表格（`service.yaml`、`requirements.txt`、`.sdk/`、`_template/` 四行）：创建者改为 `groot services init`；`.venv/` 行注明“`groot services init` 创建并安装基础依赖”。
- 1.4.3：改为“使用者执行 `groot services init` 创建共享环境并安装基础依赖；子服务业务依赖由使用者安装；Groot 运行期不安装或修复依赖”。删除“Groot 不执行 pip install，不创建或修复虚拟环境”。
- 1.11 的“`groot init` 写出的 `_template/`…”改为“`groot services init` 写出的…”。
- 1.12 非目标第二条改为：“运行期不自动安装依赖、不自动下载模型权重、不创建或修复虚拟环境；初始化命令 `groot services init` 创建虚拟环境并尝试安装基础依赖，失败时只提示手动安装。”
- 二、迭代说明末尾追加 `### 2.1 Python 服务初始化命令调整`：新增 `groot services init`；`groot init` 不再创建 `services/`；基础依赖由初始化命令安装（不保证成功）；启动停用文案指向新命令。

- [ ] **Step 3: 更新测试用例汇总**

`tests/TEST_CASES.md`：

- 1.16 覆盖点：嵌入资源一行保持；新增 `- 虚拟环境（venv_test.go）：解释器已存在跳过；正常创建；`.venv` 损坏或解释器为目录；无可用解释器与版本过低；候选回落；`--py` 不回退；venv 创建失败；Windows `py -3` 候选；创建后解释器缺失；基础依赖安装成功/失败尾行；PipPath 平台差异`；Manager 一行追加“services 目录缺失时的未初始化原因”。
- 新增 `### 1.17 Python 服务初始化命令测试`：位于 `internal/cmd/services_test.go`，列出 `TestParseServicesInitFlags`、`TestRunServicesInit_CreatesLayoutVenvAndInstallsDeps`、`TestRunServicesInit_CreatesHomeWhenMissing`、`TestRunServicesInit_Idempotent`、`TestRunServicesInit_DepsFailureReturnsNil`、`TestRunServicesInit_PythonMissingReturnsError`、`TestRunServicesInit_BrokenVenvReturnsError`、`TestRunServicesInit_UsesExplicitPython`；并注明 init 测试改为断言不创建 `services/`。

- [ ] **Step 4: 验证文档一致性**

Run: `grep -rn "groot init 创建布局\|由使用者创建\|Groot 不执行 pip install" README.md docs/superpowers/specs/2026-11-23-python-service-hosting-design.md`
Expected: 无输出（新设计文档与迭代说明中的历史对比措辞除外；如命中，确认是“上一版行为”描述后保留）。

Run: `go test ./internal/... -v`
Expected: 全部 PASS。

- [ ] **Step 5: 记录工作区快照**

复用快照命令，消息 `snapshot: task6 docs`。
