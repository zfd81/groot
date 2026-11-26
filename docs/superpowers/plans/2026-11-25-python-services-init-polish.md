# Python 服务初始化命令收尾打磨 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 收掉 `groot services init` 评审遗留的用户可见错误文案、关键降级输出测试与少量重复代码，并同步设计文档。

**Architecture:** 不改命令形态与主流程；只细化 `internal/pysvc` 的错误分支与测试、给 `internal/cmd.RunServicesInit` 增加可注入输出并锁定降级文案、统一展示路径。

**Tech Stack:** Go 1.26、标准 `testing`。

**Spec:** `docs/superpowers/specs/2026-11-24-python-services-init-command-design.md`

## Global Constraints

- 直接在 `master` 工作区开发，不创建分支或 worktree；不自动提交；每个任务结束记录悬空快照。
- 不派生子代理；实现者只做自己的任务。
- Python 最低 3.9；`--py` 指定后不回退。
- 基础依赖安装失败仍退出 0，只提示手动安装；Python/venv/布局失败非 0。
- 只写 Go 单元测试；错误文案用中文（flag 解析错误沿用既有英文）；gofmt 干净。
- 改了用户可见行为必须同步设计文档 `docs/superpowers/specs/2026-11-24-python-services-init-command-design.md`。

## Review Focus

- `--py` 显式指定失败时仍提示“用 --py 指定” → 必须只报告该解释器原因，不回退、不追加安装指引。测试落在 Task 1。
- `services/` 是普通文件或不可访问时，停用文案不能误报“不存在” → 区分不存在/不是目录/无法访问。测试落在 Task 1。
- 基础依赖安装失败的降级输出（Tail、联网/离线命令、pip 缺失提示、结尾文案）必须有断言，不能只靠肉眼。测试落在 Task 2。
- 版本查询失败、版本输出无法解析、`services/.venv` 路径是文件三个 venv 错误分支必须有测试。测试落在 Task 1。
- 设计文档与 README 必须反映 `--py` 失败语义与停用文案细分。测试/验证落在 Task 3。

---

### Task 1: pysvc 错误分支与测试补齐

**Files:**
- Modify: `internal/pysvc/venv.go`
- Modify: `internal/pysvc/venv_test.go`
- Modify: `internal/pysvc/manager.go`
- Modify: `internal/pysvc/manager_test.go`

**Interfaces:**
- Consumes: 现有 `EnsureVenv`/`VenvOptions`/`disabledReason`。
- Produces:
  - `pythonUnavailableError(goos string, explicit bool, failures []string) error`
  - `queryPythonVersion(runner CommandRunner, python string, prefix []string) (major, minor int, version string, err error)`
  - `disabledReason()` 区分三种情况。

- [ ] **Step 1: 写失败测试**

- `TestEnsureVenv_ExplicitPythonNoFallback` 追加断言：错误含 `指定的 Python 解释器不可用`，不含 `也可通过 --py` 与 `brew install`/`apt install`。
- 新增 `TestEnsureVenv_VersionQueryFails`：`python3` LookPath 成功、`-c` 返回错误，`python` 不存在；error 含 `无法获取版本`。
- 新增 `TestEnsureVenv_VersionUnparseable`：`-c` 返回 `abc`；error 含 `无法解析 Python 版本`。
- 新增 `TestEnsureVenv_VenvPathIsFile`：把 `<services>/.venv` 建成普通文件；error 含 `已存在` 与 `rm -rf`。
- 新增 `TestManager_ServicesDirNotDirectory`：`home/services` 建成普通文件；`NewManager` + `Start` 后 `Process.Error` 含 `不是目录`，不含 `不存在`。
- `TestManager_UninitializedServicesDisabled` 追加：`NewManager` 后、`Start` 前 `Process.Error` 含 `未初始化`；`Start` 后同样含 `未初始化`。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/pysvc/... -run 'TestEnsureVenv|TestManager_' -v`
Expected: FAIL（新断言/新用例未实现）。

- [ ] **Step 3: 实现**

- `resolvePython` 判定 `opts.Python != ""` 时调用 `pythonUnavailableError(goos, true, failures)`；错误只包含 `指定的 Python 解释器不可用: <name>` 与失败原因，不追加安装指引/`--py` 提示。自动探测仍用 `explicit=false` 的现有聚合文案。
- `EnsureVenv` 只解析一次 `runner := opts.runner()` 并传入 `resolvePython(goos, opts, runner)`（签名可加参数）；`queryPythonVersion` 返回 `(major, minor, version, err)`，`resolvePython` 直接使用，删除二次 `parsePythonVersion`。
- `disabledReason()`：`os.Stat(servicesDir)` 返回 `IsNotExist` → `Python 服务未初始化: 目录 <dir> 不存在（请运行 groot services init）`；其它错误 → `无法访问 Python 服务目录 <dir>: <err>（请检查权限，或运行 groot services init）`；存在但不是目录 → `Python 服务目录 <dir> 不是目录（请检查配置，或运行 groot services init）`；是目录 → `noServiceReason`。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/pysvc/... -count=1`
Expected: PASS。

- [ ] **Step 5: 记录工作区快照**

复用悬空快照命令，父提交为开工基线，消息 `snapshot: polish task1 pysvc errors`。

---

### Task 2: 命令输出可注入与文案锁定

**Files:**
- Modify: `internal/cmd/services.go`
- Modify: `internal/cmd/services_test.go`
- Modify: `internal/cmd/init_test.go`

**Interfaces:**
- Consumes: Task 1 的 pysvc 行为。
- Produces:
  - `type ServicesInitOptions struct { Python string; Runner pysvc.CommandRunner; Out io.Writer }`（`Out` 为空时用 `os.Stdout`）
  - `RunServicesInit` 全部输出写入 `Out`。

- [ ] **Step 1: 写失败测试**

- 新增 `TestRunServicesInit_DepsFailureWritesManualCommands`：`Out` 用 `bytes.Buffer`；pip 返回错误且输出含 `No module named pip`。断言 buffer 含 `基础依赖未安装成功`、pip 输出尾行、`requirements.txt` 的 `install -r`、`--no-index --find-links`、`apt install python3-venv`、`初始化完成（基础依赖需手动安装）`。
- `TestRunInit_NoServicesLayout` 改为三分支断言消息：目录存在、stat 其它错误、路径存在（可只验证前两种的文案分支）。
- 现有 `RunServicesInit` 测试补 `Out: io.Discard`，避免污染测试输出。

- [ ] **Step 2: 运行确认失败**

Run: `go test ./internal/cmd/... -run 'TestRunServicesInit|TestRunInit_NoServicesLayout' -v`
Expected: FAIL（`Out` 字段/断言未实现）。

- [ ] **Step 3: 实现**

- `ServicesInitOptions` 增加 `Out io.Writer`；`RunServicesInit` 开头 `out := opts.Out; if out == nil { out = os.Stdout }`，所有 `fmt.Println/Printf` 改为写入 `out`。
- venv 创建成功消息改为 `shortenPath(venvDir, true)`；子服务业务依赖提示用 `path.Join(servicesDir, "<子服务名>", "requirements.txt")`（导入 `path`），Windows 下也显示 `/`。
- `TestRunInit_NoServicesLayout` 的失败分支按 `os.IsNotExist`/目录存在/其它错误分别给出清楚消息（测试辅助逻辑，不改变 product）。

- [ ] **Step 4: 运行确认通过**

Run: `go test ./internal/cmd/... -count=1`
Expected: PASS。

- [ ] **Step 5: 记录工作区快照**

父提交为 Task 1 快照，消息 `snapshot: polish task2 cmd output`。

---

### Task 3: 设计文档、README 与测试汇总同步

**Files:**
- Modify: `docs/superpowers/specs/2026-11-24-python-services-init-command-design.md`
- Modify: `README.md`
- Modify: `tests/TEST_CASES.md`

**Interfaces:**
- Consumes: Task 1/2 的最终行为与测试名。
- Produces: 文档与实现一致。

- [ ] **Step 1: 更新设计文档**

- 1.3.3 末尾：明确 `--py` 指定失败时只报告该解释器原因，不回落、不追加安装指引或 `--py` 提示。
- 1.3.6：停用原因细分——目录不存在 → 未初始化；路径不是目录 → 不是目录；其它访问错误 → 无法访问并附原因；三者都提示 `groot services init`。
- 如 Task 2 改了输出路径形态（`shortenPath`、`path.Join`），在 1.3.2 对应条目同步。

- [ ] **Step 2: 更新 README**

- 6.4 补一句：`--py` 指定后只使用该解释器，失败直接报错、不回退。
- 不改变其它章节。

- [ ] **Step 3: 更新测试用例汇总**

- 1.16 增加 Task 1 新增测试覆盖点；1.17 增加 `TestRunServicesInit_DepsFailureWritesManualCommands`。
- 重新统计 Go 测试函数/文件数并更新「六、统计汇总」的 Go 行与总计（Python 两项不变）。

- [ ] **Step 4: 验证**

Run: `go test ./internal/... -count=1`
Expected: PASS。

Run: `grep -n "也可通过 --py\|目录 .* 不存在" docs/superpowers/specs/2026-11-24-python-services-init-command-design.md README.md`
Expected: 仅保留“目录不存在 → 未初始化”的说明；不得有“指定失败仍提示 --py”的表述。

- [ ] **Step 5: 记录工作区快照**

父提交为 Task 2 快照，消息 `snapshot: polish task3 docs`。
