# plugins init 输出精简实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让 `groot plugins init` 只输出它实际完成的事项，删除带字面 `<插件名>` 占位符的插件业务依赖 pip 提示行。

**Architecture:** 纯删除型改动。`internal/cmd/plugins.go` 的 `RunPluginsInit` 去掉一行无条件输出及因此不再使用的 `path` 导入；单元测试改为「成功路径的完整输出不含占位符、仍含解释器与收尾状态」，失败分支的既有断言保持；设计文档 `2026-11-24-python-plugins-init-command-design.md` 同步执行流程第 6 步。

**Tech Stack:** Go 1.x（仅标准库）、Go 标准测试框架、pytest 无关。

**Spec:** `docs/superpowers/specs/2026-11-26-plugins-init-output-cleanup-design.md`

## Global Constraints

- 所有开发直接在 `master` 工作区进行，不建分支、不建 worktree。
- 不自动 `git commit`；每个任务结束用悬空快照记录工作区状态（见各任务「记录快照」步）。
- Go 代码用 `gofmt` 格式化；测试只用 Go 标准测试框架，不引入新工具。
- 标识符命名沿用现有：`RunPluginsInit`、`PluginsInitOptions.Out`、`venvPip`、`requirements`。
- 文档遵守项目结构规范：`docs/` 下的设计文档分「一、功能设计」（正面陈述）与「二、迭代说明」（可比较）两部分。
- 编译产物只放 `dist/`。

## Review Focus

- 基础依赖安装**成功**时，输出不得再出现 `<插件名>` 占位符或任何需要使用者当下执行的 pip 命令 — Task 1 的新测试覆盖。
- 基础依赖安装**失败**时，pip 输出末尾、基础依赖手动命令、`--no-index --find-links` 离线命令、缺 pip 提示必须全部保留 — 现有 `TestRunPluginsInit_DepsFailureWritesManualCommands` 覆盖。
- 删除导入后是否仍被其它位置使用（`path` 包）：`plugins.go` 与 `plugins_test.go` 各只剩该提示一处使用 — Task 1 的 `go build` 与测试编译覆盖。
- 删除后成功路径的解释器行与收尾行不得缺失 — Task 1 新测试断言 `解释器: ` 与 `初始化完成`。
- 文档与实现不能脱节：`2026-11-24` 设计文档不得再描述该提示 — Task 2 的 grep 验证覆盖。

---

### Task 1: 删除提示行并更新单元测试

**Files:**
- Modify: `internal/cmd/plugins.go`（删除第 166 行附近的无条件 pip 提示，移除 `"path"` 导入）
- Test: `internal/cmd/plugins_test.go`（新增成功路径输出测试；失败路径测试删掉提示断言与 `subPluginRequirements`；移除 `"path"` 导入）

**Interfaces:**
- Consumes: `RunPluginsInit(homeDir string, opts PluginsInitOptions) error`、`PluginsInitOptions{Runner, Out, Python}`、测试辅助 `newPluginsFakeRunner()`、`pluginsTestInterpreterPath(homeDir)`。
- Produces: 无新增导出符号；`RunPluginsInit` 的输出契约变为「不含 `<插件名>` 提示」。

- [ ] **Step 1: 写失败测试（成功路径输出断言）**

在 `internal/cmd/plugins_test.go` 中 `TestRunPluginsInit_DepsFailureReturnsNil` 之前插入：

```go
// TestRunPluginsInit_SuccessOutputHasNoSubPluginHint 验证基础依赖安装成功时，
// 输出不含插件业务依赖提示（占位符 <插件名>），且解释器信息与收尾状态仍在。
func TestRunPluginsInit_SuccessOutputHasNoSubPluginHint(t *testing.T) {
	homeDir := t.TempDir()
	runner := newPluginsFakeRunner()

	var out bytes.Buffer
	if err := RunPluginsInit(homeDir, PluginsInitOptions{Runner: runner, Out: &out}); err != nil {
		t.Fatalf("expected nil error, got: %v", err)
	}

	got := out.String()
	if strings.Contains(got, "<插件名>") {
		t.Errorf("输出不应包含插件依赖提示，实际输出:\n%s", got)
	}
	if !strings.Contains(got, "解释器: ") {
		t.Errorf("输出缺少解释器信息，实际输出:\n%s", got)
	}
	if !strings.Contains(got, "初始化完成") {
		t.Errorf("输出缺少收尾状态，实际输出:\n%s", got)
	}
}
```

同时删除 `TestRunPluginsInit_DepsFailureWritesManualCommands` 中的两处：

```go
	subPluginRequirements := path.Join(filepath.ToSlash(pluginsDir), "<插件名>", "requirements.txt")
```
```go
		venvPip + " install -r " + subPluginRequirements,
```

- [ ] **Step 2: 运行测试确认失败**

Run: `cd /Users/bonc/workspace/groot && go test ./internal/cmd/... -run 'TestRunPluginsInit_SuccessOutputHasNoSubPluginHint' -v`
Expected: FAIL，报 `输出不应包含插件依赖提示`（提示行仍在）。

- [ ] **Step 3: 删除提示行与 `path` 导入**

在 `internal/cmd/plugins.go` 的 `RunPluginsInit` 中删除打印解释器信息之后的那一行：

```go
	fmt.Fprintf(out, "%s install -r %s\n", venvPip, path.Join(filepath.ToSlash(pluginsDir), "<插件名>", "requirements.txt"))
```

删除后 `pluginsDir` 仍被 `EnsureVenv`、`requirements` 等使用，`venvPip` 仍被失败分支使用，均保留。随后从 `plugins.go` 的导入块删除 `"path"`；从 `plugins_test.go` 的导入块删除 `"path"`（该文件中 `path` 仅被刚删掉的这一处使用）。

- [ ] **Step 4: 运行测试确认通过**

Run: `cd /Users/bonc/workspace/groot && go test ./internal/cmd/... -v`
Expected: PASS，全部用例通过（含 `TestRunPluginsInit_DepsFailureWritesManualCommands`）。

Run: `cd /Users/bonc/workspace/groot && go build -o dist/groot ./cmd/groot && gofmt -l internal/cmd`
Expected: 构建成功；`gofmt -l` 无输出。

- [ ] **Step 5: 记录快照（不提交）**

```bash
cd /Users/bonc/workspace/groot
export GIT_INDEX_FILE=.git/sdd-index
git read-tree HEAD && git add -A && T=$(git write-tree)
git commit-tree "$T" -p HEAD -m "task1: 删除 plugins init 插件依赖提示行"
unset GIT_INDEX_FILE; rm -f .git/sdd-index
```

---

### Task 2: 同步设计文档

**Files:**
- Modify: `docs/superpowers/specs/2026-11-24-python-plugins-init-command-design.md`（1.3.2 执行流程第 6 步相关条目、2.1 迭代说明、2.2 方案选择）

**Interfaces:**
- Consumes: Task 1 确定的输出契约（成功路径无插件提示）。
- Produces: 文档层面的最终表述；无代码符号。

- [ ] **Step 1: 改写执行流程第 6 步**

将 1.3.2 第 6 步中关于插件业务依赖的两条项目符号与该步的说明改为只描述实际输出：

```markdown
6. 打印使用中的解释器路径与版本，并打印收尾状态：
   - `解释器: <路径> (<版本>)`；版本未知时省略括号部分。
   - `{venv}/bin/pip install -r {plugins}/requirements.txt`（仅在第 5 步失败时出现，手动补装基础依赖；Windows 使用 `Scripts\pip.exe`）。
   - 基础依赖安装成功时以 `初始化完成` 结尾；失败时以 `初始化完成（基础依赖需手动安装）` 结尾。
```

删除原有的「各插件业务依赖」条目与「插件业务依赖提示中的路径用正斜杠拼接」条目。

- [ ] **Step 2: 在 2.1 追加本次迭代条目**

在 2.1 末尾追加：

```markdown
- 移除：`groot plugins init` 不再输出插件业务依赖的 pip 提示；该信息由 README §6.4、§6.5、§7.8 说明（`groot plugins` 内相对路径安装，或直接调用共享环境 pip 的绝对路径写法）。
```

并在 2.2 末尾追加本次选择的比较：

```markdown
- 插件依赖提示：移除。它在插件目录尚未就位时不可执行，且在成功与失败两条路径上都出现，容易被误读为失败提示；README 已完整覆盖两种安装方式。备选「加引导语并改相对路径」仍会在初始化阶段给出通常无法立即执行的命令；「检测到插件时才打印」让 init 承担插件发现职责，收益不足以抵消复杂度。
```

- [ ] **Step 3: 验证文档与实现一致**

Run: `cd /Users/bonc/workspace/groot && grep -rn "插件名.*requirements.txt" docs/superpowers/specs/2026-11-24-python-plugins-init-command-design.md`
Expected: 无输出（该文档不再描述提示行）。

Run: `cd /Users/bonc/workspace/groot && sed -n '55,70p' docs/superpowers/specs/2026-11-24-python-plugins-init-command-design.md`
Expected: 第 6 步只含解释器、基础依赖失败手动命令与收尾状态三项。

- [ ] **Step 4: 记录快照（不提交）**

```bash
cd /Users/bonc/workspace/groot
export GIT_INDEX_FILE=.git/sdd-index
git read-tree HEAD && git add -A && T=$(git write-tree)
git commit-tree "$T" -p HEAD -m "task2: 同步 plugins init 设计文档"
unset GIT_INDEX_FILE; rm -f .git/sdd-index
```

---

### Task 3: 完成前验证

**Files:** 无改动（只验证）。

**Interfaces:**
- Consumes: Task 1、Task 2 的全部改动。
- Produces: 验证结论。

- [ ] **Step 1: 运行相关单元测试**

Run: `cd /Users/bonc/workspace/groot && go test ./internal/cmd/... -v -run 'TestRunPluginsInit'`
Expected: PASS，全部 `TestRunPluginsInit_*` 通过。

- [ ] **Step 2: 运行整个 cmd 包与 plugin 包测试**

Run: `cd /Users/bonc/workspace/groot && go test ./internal/cmd/... ./internal/plugin/... -count=1`
Expected: PASS。

- [ ] **Step 3: 构建与格式检查**

Run: `cd /Users/bonc/workspace/groot && go build -o dist/groot ./cmd/groot && gofmt -l internal/`
Expected: 构建成功；`gofmt -l` 无输出。

- [ ] **Step 4: 核对工作区差异只含预期文件**

Run: `cd /Users/bonc/workspace/groot && git status --short`
Expected: 仅 `internal/cmd/plugins.go`、`internal/cmd/plugins_test.go`、`docs/superpowers/specs/2026-11-24-python-plugins-init-command-design.md`、两份新增文档（`docs/superpowers/specs/2026-11-26-*`、`docs/superpowers/plans/2026-11-26-*`）。
