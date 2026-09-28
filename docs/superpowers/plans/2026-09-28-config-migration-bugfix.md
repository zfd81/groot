# 配置迁移遗留缺陷修复 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 修复配置迁移到数据库 + UI 后暴露的 4 个功能缺陷和 2 处一致性问题，不改变任何现有正确行为。

**Architecture:** 按包分 4 批独立改动。附件包修白名单匹配与体积校验覆盖面；setting 包补白名单入库归一化并导出可配置渠道列表；agent 包解除并发下限篡改并让子 Agent 跟随推理参数热更新；web 包对齐占位默认值。每批自带 Go 单测，互不依赖，可独立验证。

**Tech Stack:** Go 1.x（标准 `testing`）、Vue 3 + TypeScript、Element Plus。

**本项目约定（覆盖 skill 默认）：**
- 单测用 Go 标准框架，`*_test.go` 与源码同目录，命令 `go test ./internal/xxx/... -v`
- **禁止自动 commit。** 所有任务的「提交」步骤只做 `git diff` 自查，实际 commit 等用户明确指示
- 编译产物输出到 `dist/`，命令 `go build -o dist/groot ./cmd`

---

## 前置背景（实现者必读）

四个缺陷的成因都与「配置从 YAML 搬到数据库 + UI」这次重构有关，但根因各不相同：

**附件扩展名白名单失效。** [internal/attachment/handler.go:70-81](../../../internal/attachment/handler.go#L70-L81) 从文件名取扩展名时用 `ext = ext[1:]` 去掉了点号，得到 `png`；而 `isTypeAllowed` 拿它和用户配置的原值比较。UI 输入框提示和 README 都写「如 `.png, .jpg, .pdf`」，前端只 trim 不去点，后端不归一化，于是按文档填写的白名单永远匹配不上，报错信息自相矛盾：`附件类型不允许：png (允许的类型：.png, .pdf)`。旧版默认空数组且无 UI，没人会填带点的值，所以是搬上 UI 之后才变得可触发。

**并发上限被静默篡改。** [internal/agent/subagent_registry.go:301](../../../internal/agent/subagent_registry.go#L301) 的 `max(subCfg.MaxConcurrency, 5)` 来自子 Agent 能力初版，当时只有 YAML 一个来源，这行是给未设置的零值兜底。现在 [runtime.go:69-70](../../../internal/setting/runtime.go#L69-L70) 把合法区间定为 1..100，`SetMaxConcurrency` 又没有同样的下限，于是填 2 立即按 2 生效、重启后变 5，UI 仍显示 2。

**非 file 类附件不受体积限制。** [handler.go:82](../../../internal/attachment/handler.go#L82) 的体积校验条件是 `att.Type == "file"`，image/audio/video 三类既不查单文件上限也不累加进总量。

**子 Agent 不跟随推理参数热更新。** `MaxIterations`/`RetryConfig`/`StepTimeout` 在 `buildSubAgentEntry` 里固化进 `SubAgentEntry`，启动后不再变。注意启动时取的**已经是数据库值**（[assemble.go:36](../../../internal/setting/assemble.go#L36) 的 `React: rt.React`），所以问题只是「冻结」而非「取错来源」。Solo 模式走 [executor.go:268](../../../internal/agent/executor.go#L268) 的 `rt.React`，本来就正确，不受影响。

## 明确不改的部分（守住现有正确逻辑）

- **白名单适用范围保持 `file || image`**，audio/video 继续不查扩展名。用户已确认：只有体积限制需要覆盖全部类型。
- **UI 提示与 README 不改。** 归一化后填 `.png` 和 `png` 都有效，是纯放宽，不让任何现有正确配置失效。
- **`isTypeAllowed` 函数体不改**，它已对两侧做 `ToLower`。
- **并发上限的零值兜底保留**，只是不再篡改用户设的 1..4。
- **`MinSubAgentMaxConcurrency` 保持 1**，不上调为 5。
- **请求体上限不动。** 附件校验发生在 [chat.go:161](../../../internal/api/handler/chat.go#L161)，已在框架读完请求体之后，放开体积判断只影响「超限是否报错」。

## 已知的行为收紧（需在 PR 说明中告知）

Task 3 修完后，原先能通过的大图片会开始返回 400，且图片计入总量。README 第 4.6 节本来就把单文件上限写成对附件普遍生效，属代码对齐文档，不改文档。

## File Structure

| 文件 | 改动 | 职责 |
|---|---|---|
| `internal/attachment/handler.go` | 修改 | 消费侧归一化白名单（兜住存量数据）；体积校验覆盖四类 |
| `internal/attachment/handler_test.go` | 修改 | 补带点/大小写/四类超限/图片计入总量用例 |
| `internal/setting/runtime.go` | 修改 | 新增 `normalizeAllowedTypes`，写表前调用 |
| `internal/setting/runtime_test.go` | 修改 | 归一化单测 |
| `internal/setting/message.go` | 修改 | 导出 `ConfigurableSenders()` |
| `internal/api/handler/setting.go` | 修改 | 三处 webhook 硬编码改为遍历 |
| `cmd/groot/main.go` | 修改 | 渠道注册改为遍历；去掉 `cfg.React` 实参 |
| `internal/agent/subagent_registry.go` | 修改 | 解除并发篡改；摘除三个 react 字段 |
| `internal/agent/subagent_registry_test.go` | 修改 | 并发初始化 + react 派生 helper 单测 |
| `internal/agent/call_agent.go` | 修改 | 透传 React 配置 |
| `internal/agent/call_agent_test.go` | 修改 | 适配签名变更 |
| `internal/agent/executor.go` | 修改 | 传入 `rt.React` |
| `web/src/api/runtime.ts` | 修改 | 对齐 7 个占位默认值 |

---

## Task 1: 附件扩展名白名单归一化（消费侧）

修 Bug 1 的关键一半。消费侧归一化覆盖存量带点数据和老 config.yaml 迁移进来的值，不用写数据迁移脚本。

**Files:**
- Modify: `internal/attachment/handler.go:40-47`（`NewHandler`）
- Modify: `internal/attachment/handler.go:96-106`（新增归一化函数）
- Test: `internal/attachment/handler_test.go`

- [ ] **Step 1: 写失败测试**

追加到 `internal/attachment/handler_test.go` 末尾：

```go
// TestHandler_Validate_DottedAllowedTypes 验证带点号的白名单配置（UI 提示与
// README 的格式）能正确匹配。归一化前这里必然失败：ext 已去点，配置未去点。
func TestHandler_Validate_DottedAllowedTypes(t *testing.T) {
	handler := NewHandler(config.AttachmentConfig{
		MaxSize:      50,
		MaxTotalSize: 100,
		MaxCount:     10,
		AllowedTypes: []string{".png", ".pdf"},
	})

	err := handler.Validate([]Attachment{
		{Type: "file", Name: "doc.pdf", Content: "abc"},
	})
	if err != nil {
		t.Errorf("带点白名单应放行同类型附件，却报错: %v", err)
	}
}

// TestHandler_Validate_MixedCaseAllowedTypes 验证白名单大小写与前导点混用时
// 仍能匹配，且不匹配的类型依然被拒。
func TestHandler_Validate_MixedCaseAllowedTypes(t *testing.T) {
	handler := NewHandler(config.AttachmentConfig{
		MaxSize:      50,
		MaxTotalSize: 100,
		MaxCount:     10,
		AllowedTypes: []string{".PNG", "PDF", "  .Txt  "},
	})

	for _, name := range []string{"a.png", "b.pdf", "c.txt", "d.PNG"} {
		if err := handler.Validate([]Attachment{
			{Type: "file", Name: name, Content: "abc"},
		}); err != nil {
			t.Errorf("%s 应被放行，却报错: %v", name, err)
		}
	}

	err := handler.Validate([]Attachment{
		{Type: "file", Name: "e.exe", Content: "abc"},
	})
	if err == nil {
		t.Error("exe 不在白名单内，应被拒绝")
	}
}

// TestNormalizeTypes 验证归一化：去空白、去前导点、转小写、丢弃空项。
func TestNormalizeTypes(t *testing.T) {
	got := normalizeTypes([]string{" .PNG ", "PDF", ".", "", "  ", ".tar.gz"})
	want := []string{"png", "pdf", "tar.gz"}
	if len(got) != len(want) {
		t.Fatalf("归一化结果 = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("归一化结果[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestNormalizeTypes_NilStaysNil 验证 nil 输入返回 nil，保持「不限制类型」语义。
func TestNormalizeTypes_NilStaysNil(t *testing.T) {
	if got := normalizeTypes(nil); got != nil {
		t.Errorf("normalizeTypes(nil) = %v, want nil", got)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/attachment/... -run 'TestHandler_Validate_DottedAllowedTypes|TestHandler_Validate_MixedCaseAllowedTypes|TestNormalizeTypes' -v`

Expected: 编译失败 `undefined: normalizeTypes`。这是预期的，函数还没写。

- [ ] **Step 3: 实现归一化函数**

在 `internal/attachment/handler.go` 的 `isTypeAllowed` 函数之后追加：

```go
// normalizeTypes 把白名单配置归一化为「不带前导点的小写扩展名」，与 Validate
// 中 filepath.Ext 去点后的形式对齐。
//
// 存在两个归一化落点：写入侧（setting 包）保证入库值规范，本函数则兜住存量
// 数据——老 config.yaml 迁移进来的、或此前经 UI 写入的带点值，无需数据迁移
// 脚本即可正确匹配。
//
// nil 输入返回 nil，保持「白名单为空 = 不限制类型」的语义。
func normalizeTypes(types []string) []string {
	if types == nil {
		return nil
	}
	out := make([]string, 0, len(types))
	for _, t := range types {
		s := strings.ToLower(strings.TrimSpace(t))
		s = strings.TrimPrefix(s, ".")
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}
```

- [ ] **Step 4: 在 NewHandler 中调用**

把 `internal/attachment/handler.go:40-47` 的 `NewHandler` 改为：

```go
// NewHandler creates a new attachment handler
func NewHandler(cfg config.AttachmentConfig) *Handler {
	return &Handler{
		maxSize:      int64(cfg.MaxSize) * 1024 * 1024,
		maxTotalSize: int64(cfg.MaxTotalSize) * 1024 * 1024,
		maxCount:     cfg.MaxCount,
		allowedTypes: normalizeTypes(cfg.AllowedTypes),
	}
}
```

其余字段与原实现逐字一致，只有 `allowedTypes` 一行有变化。

- [ ] **Step 5: 修正自相矛盾的报错信息**

`internal/attachment/handler.go:76-79` 的报错拼的是 `h.allowedTypes`，归一化后它已是去点小写形式，报错信息自然与 `ext` 同形，原「png 不在 .png 里」的矛盾消失。**此步无需改代码**，只需确认第 78 行仍为：

```go
Message: fmt.Sprintf("附件类型不允许：%s (允许的类型：%s)", ext, strings.Join(h.allowedTypes, ", ")),
```

- [ ] **Step 6: 运行测试确认通过**

Run: `go test ./internal/attachment/... -v`

Expected: 全部 PASS，包含原有 11 个用例。特别确认 `TestNewHandler`（用不带点的 `pdf txt json`）和 `TestHandler_Validate_TypeNotAllowed` 仍通过——归一化对不带点的输入是恒等变换，现有正确行为不受影响。

- [ ] **Step 7: 自查 diff**

```bash
git diff internal/attachment/
```

确认只动了 `NewHandler` 一行 + 新增一个函数 + 新增测试。**不要 commit**，等用户指示。

---

## Task 2: 附件体积限制覆盖全部类型

修 Bug 3。用户已明确：除文本内容外，上传的文件不管什么类型都要限制大小。

**Files:**
- Modify: `internal/attachment/handler.go:82`
- Test: `internal/attachment/handler_test.go`

- [ ] **Step 1: 写失败测试**

追加到 `internal/attachment/handler_test.go` 末尾：

```go
// TestHandler_Validate_NonFileSizeExceeded 验证 image/audio/video 三类附件
// 同样受单文件上限约束。修复前它们完全绕过体积校验。
func TestHandler_Validate_NonFileSizeExceeded(t *testing.T) {
	handler := NewHandler(config.AttachmentConfig{
		MaxSize:      1,
		MaxTotalSize: 100,
		MaxCount:     10,
	})

	// base64 长度 * 3/4 约等于原始字节数；取 3MB 确保超过 1MB 上限
	big := strings.Repeat("A", 4*1024*1024)

	for _, typ := range []string{"image", "audio", "video"} {
		err := handler.Validate([]Attachment{
			{Type: typ, Name: "big.dat", Content: big},
		})
		if err == nil {
			t.Errorf("%s 类型超过单文件上限，应被拒绝", typ)
			continue
		}
		var attErr *AttachmentError
		if !errors.As(err, &attErr) || attErr.Code != ErrCodeSizeExceeded {
			t.Errorf("%s 类型应返回 %s，实际: %v", typ, ErrCodeSizeExceeded, err)
		}
	}
}

// TestHandler_Validate_NonFileCountsTowardTotal 验证非 file 类附件计入总量。
// 三个各约 3MB 的图片，单个都不超 5MB 上限，合计超过 6MB 总量上限。
func TestHandler_Validate_NonFileCountsTowardTotal(t *testing.T) {
	handler := NewHandler(config.AttachmentConfig{
		MaxSize:      5,
		MaxTotalSize: 6,
		MaxCount:     10,
	})

	img := strings.Repeat("A", 4*1024*1024) // 约 3MB
	err := handler.Validate([]Attachment{
		{Type: "image", Name: "a.png", Content: img},
		{Type: "image", Name: "b.png", Content: img},
		{Type: "image", Name: "c.png", Content: img},
	})
	if err == nil {
		t.Fatal("三个图片合计超过总量上限，应被拒绝")
	}
	var attErr *AttachmentError
	if !errors.As(err, &attErr) || attErr.Code != ErrCodeTotalSizeExceeded {
		t.Errorf("应返回 %s，实际: %v", ErrCodeTotalSizeExceeded, err)
	}
}

// TestHandler_Validate_NonFileWithinLimit 验证限内的非 file 附件仍放行，
// 确认修复没有误伤正常路径。
func TestHandler_Validate_NonFileWithinLimit(t *testing.T) {
	handler := NewHandler(config.AttachmentConfig{
		MaxSize:      50,
		MaxTotalSize: 100,
		MaxCount:     10,
	})

	for _, typ := range []string{"image", "audio", "video", "file"} {
		if err := handler.Validate([]Attachment{
			{Type: typ, Name: "small.dat", Content: "abcd"},
		}); err != nil {
			t.Errorf("%s 类型在限内应放行，却报错: %v", typ, err)
		}
	}
}
```

测试文件的 import 块需要加 `errors` 和 `strings`：

```go
import (
	"errors"
	"strings"
	"testing"

	"github.com/zfd81/groot/internal/config"
)
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/attachment/... -run 'TestHandler_Validate_NonFile' -v`

Expected: `TestHandler_Validate_NonFileSizeExceeded` 和 `TestHandler_Validate_NonFileCountsTowardTotal` 均 FAIL，报 `应被拒绝`。`TestHandler_Validate_NonFileWithinLimit` 此时已 PASS（它验证的是不该误伤的路径）。

- [ ] **Step 3: 放开体积校验的类型限制**

把 `internal/attachment/handler.go:82` 一行：

```go
		if att.Type == "file" && att.Content != "" {
```

改为：

```go
		// 体积限制对全部类型生效：image/audio/video 同样是上传的文件内容
		if att.Content != "" {
```

`if` 块内部的四行（估算、比较、报错、累加）**逐字不动**。注意第 70 行的白名单条件 `att.Type == "file" || att.Type == "image"` 保持原样，不在本任务范围。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/attachment/... -v`

Expected: 全部 PASS。特别确认 `TestHandler_Validate_SizeExceeded`（原有的 file 类超限用例）仍通过——放宽条件是 file 路径的超集，原行为不变。

- [ ] **Step 5: 自查 diff**

```bash
git diff internal/attachment/handler.go
```

确认本任务只改了一行判断条件加一行注释。**不要 commit**。

---

## Task 3: 白名单写入侧归一化

修 Bug 1 的另一半。保证入库值规范，UI 回读到的也是规范值。

**Files:**
- Modify: `internal/setting/runtime.go:337`（rows 写表）
- Modify: `internal/setting/runtime.go:352-361`（`encodeAllowedTypes` 附近新增函数）
- Test: `internal/setting/runtime_test.go`

- [ ] **Step 1: 写失败测试**

追加到 `internal/setting/runtime_test.go` 末尾：

```go
// TestNormalizeAllowedTypes 验证写入侧归一化：去空白、去前导点、转小写、丢空项。
func TestNormalizeAllowedTypes(t *testing.T) {
	got := normalizeAllowedTypes([]string{" .PNG ", "PDF", ".", "", ".tar.gz"})
	want := []string{"png", "pdf", "tar.gz"}
	if len(got) != len(want) {
		t.Fatalf("归一化结果 = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("结果[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestNormalizeAllowedTypes_NilStaysNil 验证 nil 保持 nil，
// 交由 encodeAllowedTypes 编码为 "[]"（语义：不限制类型）。
func TestNormalizeAllowedTypes_NilStaysNil(t *testing.T) {
	if got := normalizeAllowedTypes(nil); got != nil {
		t.Errorf("normalizeAllowedTypes(nil) = %v, want nil", got)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/setting/... -run TestNormalizeAllowedTypes -v`

Expected: 编译失败 `undefined: normalizeAllowedTypes`。

- [ ] **Step 3: 实现归一化函数**

在 `internal/setting/runtime.go` 的 `encodeAllowedTypes` 函数**之前**插入：

```go
// normalizeAllowedTypes 把附件类型白名单归一化为「不带前导点的小写扩展名」。
//
// UI 输入框与 README 提示的格式是 ".png, .jpg"，而校验侧（attachment 包）拿到
// 的扩展名已由 filepath.Ext 去点。此处在写表前统一形式，使入库值与 UI 回读值
// 都是规范形态。消费侧另有一道同样的归一化，负责兜住存量数据。
//
// nil 输入返回 nil，由 encodeAllowedTypes 编码为 "[]"，语义是「不限制类型」。
func normalizeAllowedTypes(types []string) []string {
	if types == nil {
		return nil
	}
	out := make([]string, 0, len(types))
	for _, t := range types {
		s := strings.ToLower(strings.TrimSpace(t))
		s = strings.TrimPrefix(s, ".")
		if s == "" {
			continue
		}
		out = append(out, s)
	}
	return out
}
```

`internal/setting/runtime.go` 已 import `strings`（第 183 行 `strings.TrimSpace` 在用），无需改 import 块。

- [ ] **Step 4: 在写表处调用**

把 `internal/setting/runtime.go:337` 一行：

```go
		{KeyAttachmentAllowedTypes, encodeAllowedTypes(r.Attachment.AllowedTypes)},
```

改为：

```go
		{KeyAttachmentAllowedTypes, encodeAllowedTypes(normalizeAllowedTypes(r.Attachment.AllowedTypes))},
```

kv 列表其余各行不动。

- [ ] **Step 5: 收紧 Validate，把「归一化后为空」的项挡在保存环节**

这一步来自 Task 1 的代码审查发现，是 Task 1 消费侧兜底的配套上游修复。

现有校验（`internal/setting/runtime.go:181-186`）只拒绝 `strings.TrimSpace(t) == ""`，所以 `.`、`..`、`. ` 这类「只剩点号」的项能通过校验入库。它们归一化后为空，会被丢弃。消费侧 Task 1 已用 `Handler.restrictTypes` 保证这种情况按「拒绝所有附件」处理而非反转成「不限制」，但让这种值入库本身就是错的：用户在界面上看到一个自己填的白名单，实际效果是全部拒绝，且回读时那一项已消失。

把第 181-186 行的循环改为：

```go
	for _, t := range r.Attachment.AllowedTypes {
		if strings.TrimSpace(t) == "" {
			return fmt.Errorf("%w: attachment.allowed_types 不允许空白项", ErrInvalidSetting)
		}
		// 「.」「..」这类只剩点号的项归一化后为空，会被静默丢弃，导致界面显示的
		// 白名单与实际生效的不一致。在保存环节直接报错，而不是落库后再兜。
		if strings.Trim(strings.TrimSpace(t), ".") == "" {
			return fmt.Errorf("%w: attachment.allowed_types 项 %q 无效，需包含扩展名", ErrInvalidSetting, t)
		}
	}
```

原来的空白项检查保留在前，报错信息更具体，不要合并成一个分支。

配套测试追加到 `internal/setting/runtime_test.go`：

```go
// TestValidateRuntime_RejectsDotOnlyAllowedType 验证只含点号的白名单项在保存
// 环节就被拒绝，不会落库后归一化为空再被丢弃。
func TestValidateRuntime_RejectsDotOnlyAllowedType(t *testing.T) {
	for _, bad := range []string{".", "..", " . "} {
		rt := validRuntimeSettings()
		rt.Attachment.AllowedTypes = []string{bad}
		err := rt.Validate()
		if err == nil {
			t.Errorf("白名单项 %q 应被拒绝，实际通过校验", bad)
			continue
		}
		if !errors.Is(err, ErrInvalidSetting) {
			t.Errorf("白名单项 %q 的错误应包装 ErrInvalidSetting，实际 %v", bad, err)
		}
	}
}

// TestValidateRuntime_AcceptsDottedAllowedType 验证带点的正常扩展名（UI 提示
// 的格式）仍然通过校验，收紧校验没有误伤。
func TestValidateRuntime_AcceptsDottedAllowedType(t *testing.T) {
	rt := validRuntimeSettings()
	rt.Attachment.AllowedTypes = []string{".png", "pdf", ".tar.gz"}
	if err := rt.Validate(); err != nil {
		t.Errorf("带点扩展名应通过校验，实际报错: %v", err)
	}
}
```

`validRuntimeSettings()` 是否已存在于 `runtime_test.go` 需要先确认：如果没有这个 helper，改用该文件中既有的构造合法 `RuntimeSettings` 的方式（读一下文件顶部的既有测试怎么造的，照它的模式来），不要新建 helper。`errors` 包若未 import 需补上。

- [ ] **Step 6: 运行测试确认通过**

Run: `go test ./internal/setting/... -v -count=1`

Expected: 全部 PASS，含新增 4 个用例。

- [ ] **Step 7: 自查 diff**

```bash
git diff internal/setting/runtime.go internal/setting/runtime_test.go
```

确认只动了 kv 写表一行、Validate 循环、新增一个函数、新增测试。**不要 commit**。

---

## Task 4: 导出可配置渠道列表

修轻微问题 2。消除三处 webhook 硬编码，将来加渠道只改一处。

**Files:**
- Modify: `internal/setting/message.go:27-28`
- Modify: `internal/api/handler/setting.go:249`
- Modify: `internal/api/handler/setting.go:309-310`
- Modify: `cmd/groot/main.go:370-371`
- Test: `internal/setting/message_test.go`

- [ ] **Step 1: 写失败测试**

追加到 `internal/setting/message_test.go` 末尾：

```go
// TestConfigurableSenders 验证导出的渠道列表内容正确，且返回副本——
// 调用方修改返回值不应污染包内状态。
func TestConfigurableSenders(t *testing.T) {
	got := ConfigurableSenders()
	if len(got) != 1 || got[0] != SenderWebhook {
		t.Fatalf("ConfigurableSenders() = %v, want [%s]", got, SenderWebhook)
	}

	got[0] = "tampered"
	again := ConfigurableSenders()
	if again[0] != SenderWebhook {
		t.Errorf("返回值被外部修改后污染了包内状态: %v", again)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/setting/... -run TestConfigurableSenders -v`

Expected: 编译失败 `undefined: ConfigurableSenders`。

- [ ] **Step 3: 新增导出函数**

在 `internal/setting/message.go:28` 的 `configurableSenders` 变量声明之后插入：

```go
// ConfigurableSenders 返回可配置渠道名的副本，供包外遍历。
// 返回副本而非切片本身，避免调用方意外修改包内状态。
func ConfigurableSenders() []string {
	out := make([]string, len(configurableSenders))
	copy(out, configurableSenders)
	return out
}
```

包内已有的 4 处 `configurableSenders` 用法（第 58、59、91、108、116 行）**全部不动**，它们在包内直接用切片更直接。

- [ ] **Step 4: 改 setting handler 的遍历**

把 `internal/api/handler/setting.go:249` 一行：

```go
	for _, name := range []string{setting.SenderWebhook} {
```

改为：

```go
	for _, name := range setting.ConfigurableSenders() {
```

- [ ] **Step 5: 改 setting handler 的渠道注册**

`internal/api/handler/setting.go:309-310` 现为：

```go
	w := m.Senders[setting.SenderWebhook]
	h.messages.SetSender(setting.SenderWebhook, senders.NewWebhook(w.URL), w)
```

改为：

```go
	for _, name := range setting.ConfigurableSenders() {
		conf := m.Senders[name]
		if name == setting.SenderWebhook {
			h.messages.SetSender(name, senders.NewWebhook(conf.URL), conf)
		}
	}
```

保留 `name == SenderWebhook` 的分支判断：每种渠道的构造器签名不同，遍历只统一了「有哪些渠道」，构造仍需按名分派。将来加渠道时在此加一个分支。

- [ ] **Step 6: 改 main.go 的启动期注册**

`cmd/groot/main.go:370-371` 现为：

```go
	webhookConf := msgCfg.Senders[setting.SenderWebhook]
	msgLayer.Register(setting.SenderWebhook, senders.NewWebhook(webhookConf.URL), webhookConf)
```

改为：

```go
	for _, name := range setting.ConfigurableSenders() {
		conf := msgCfg.Senders[name]
		if name == setting.SenderWebhook {
			msgLayer.Register(name, senders.NewWebhook(conf.URL), conf)
		}
	}
```

- [ ] **Step 7: 运行测试并编译**

Run: `go test ./internal/setting/... ./internal/api/... -v && go build -o dist/groot ./cmd`

Expected: 测试全部 PASS，编译成功。当前只有 webhook 一种渠道，所以行为与改动前完全一致。

- [ ] **Step 8: 自查 diff**

```bash
git diff internal/setting/message.go internal/api/handler/setting.go cmd/groot/main.go
```

**不要 commit**。

---

## Task 5: 解除子 Agent 并发上限篡改

修 Bug 2。保留零值兜底，不再篡改用户设的 1..4。

**Files:**
- Modify: `internal/agent/subagent_registry.go:301`
- Test: `internal/agent/subagent_registry_test.go`

- [ ] **Step 1: 写失败测试**

先看 `internal/agent/subagent_registry_test.go` 里已有的 `NewRegistryForTest` 辅助函数，它直接构造 registry，绕过了 `BuildSubAgentRegistry`。本任务要测的正是 `BuildSubAgentRegistry` 的初始化逻辑，所以需要抽出被测逻辑。

先把 `internal/agent/subagent_registry.go:301` 的表达式抽成可测函数。在 `BuildSubAgentRegistry` 函数**之前**插入：

```go
// resolveMaxConcurrency 决定 semaphore 的初始容量。
//
// 合法区间由 setting.MinSubAgentMaxConcurrency..MaxSubAgentMaxConcurrency（1..100）
// 定义，校验层已把关，此处只兜住「配置缺失」这一种情况：零值或负数回落到默认 5。
// 不对区间内的值做任何抬升——用户在 UI 里设的 2 必须在重启后仍是 2，否则界面
// 显示与实际行为分叉。
func resolveMaxConcurrency(n int) int {
	if n <= 0 {
		return 5
	}
	return n
}
```

然后追加测试到 `internal/agent/subagent_registry_test.go` 末尾：

```go
// TestResolveMaxConcurrency 验证并发上限的初始化语义：区间内的值原样保留，
// 缺失（零值/负数）才回落默认 5。
func TestResolveMaxConcurrency(t *testing.T) {
	cases := []struct {
		in   int
		want int
		desc string
	}{
		{0, 5, "零值回落默认"},
		{-1, 5, "负数回落默认"},
		{1, 1, "下限值原样保留"},
		{2, 2, "小于默认值的合法配置不被抬升"},
		{4, 4, "小于默认值的合法配置不被抬升"},
		{5, 5, "等于默认值"},
		{100, 100, "上限值原样保留"},
	}
	for _, c := range cases {
		if got := resolveMaxConcurrency(c.in); got != c.want {
			t.Errorf("resolveMaxConcurrency(%d) = %d, want %d（%s）",
				c.in, got, c.want, c.desc)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agent/... -run TestResolveMaxConcurrency -v`

Expected: 如果 Step 1 里的函数还没加就是编译失败；加了函数但还没改第 301 行调用点时测试会 PASS（函数本身是对的）。真正的验证在 Step 4。

- [ ] **Step 3: 替换 BuildSubAgentRegistry 里的表达式**

把 `internal/agent/subagent_registry.go:301` 一行：

```go
	maxConc := max(subCfg.MaxConcurrency, 5)
```

改为：

```go
	maxConc := resolveMaxConcurrency(subCfg.MaxConcurrency)
```

- [ ] **Step 4: 验证与 SetMaxConcurrency 语义一致**

追加测试到 `internal/agent/subagent_registry_test.go` 末尾：

```go
// TestBuildSubAgentRegistry_RespectsSmallConcurrency 验证启动期取的并发上限
// 与运行期 SetMaxConcurrency 语义一致：都保留小于 5 的合法值。
// 修复前启动期会把 2 抬成 5，导致重启后界面显示 2 而实际跑 5。
func TestBuildSubAgentRegistry_RespectsSmallConcurrency(t *testing.T) {
	dir := t.TempDir() // 空目录：不加载任何子 Agent，只验证并发初始化
	reg := BuildSubAgentRegistry(
		context.Background(),
		dir,
		config.ReactConfig{}, // Task 6 会移除此参数，届时同步删掉这一行
		config.SubAgentConfig{MaxConcurrency: 2},
		nil,
		logger.NewNop(),
	)
	if got := reg.MaxConcurrency(); got != 2 {
		t.Errorf("启动期并发上限 = %d, want 2（不应被抬升到 5）", got)
	}

	reg.SetMaxConcurrency(2)
	if got := reg.MaxConcurrency(); got != 2 {
		t.Errorf("SetMaxConcurrency(2) 后 = %d, want 2", got)
	}
}
```

`internal/agent/subagent_registry_test.go` 的 import 块已含 `context`、`github.com/zfd81/groot/internal/config`、`github.com/zfd81/groot/internal/logger`，无需改动。

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/agent/... -v`

Expected: 全部 PASS。特别确认 `TestSubAgentRegistry_SetMaxConcurrency` 和 `TestSubAgentRegistry_SetMaxConcurrencyIgnoresNonPositive` 仍通过——`SetMaxConcurrency` 本身没动。

- [ ] **Step 6: 自查 diff**

```bash
git diff internal/agent/subagent_registry.go
```

**不要 commit**。

---

## Task 6: 子 Agent 跟随推理循环参数热更新

修 Bug 4。把 `MaxIterations`/`RetryConfig`/`StepTimeout` 从启动期固化改为每次 `call_agent` 调用现场从 `rt.React` 派生。`BuildAgentTool` 本就是每次调用现场执行的（为了跟随 model），react 参数只是漏了同样处理。

**Files:**
- Modify: `internal/agent/subagent_registry.go:41-59`（`SubAgentEntry` 摘字段）
- Modify: `internal/agent/subagent_registry.go:293-317`（`BuildSubAgentRegistry` 去参）
- Modify: `internal/agent/subagent_registry.go:331-394`（`buildSubAgentEntry` 去参与派生逻辑）
- Modify: `internal/agent/subagent_registry.go:408-467`（`BuildAgentTool` 加参）
- Modify: `internal/agent/call_agent.go:44-88`（结构体与构造透传）
- Modify: `internal/agent/call_agent.go:142`（调用点传参）
- Modify: `internal/agent/executor.go:203-214`（传入 `rt.React`）
- Modify: `cmd/groot/main.go:377`（去掉 `cfg.React` 实参）
- Test: `internal/agent/subagent_registry_test.go`、`internal/agent/call_agent_test.go`

- [ ] **Step 1: 写派生 helper 的失败测试**

追加到 `internal/agent/subagent_registry_test.go` 末尾：

```go
// TestDeriveReactRuntime 验证推理参数派生：迭代零值回落 20、重试为 0 时不建
// RetryConfig、超时按秒换算。这段逻辑原先内联在 buildSubAgentEntry 中，
// 抽出后每次 call_agent 调用现场执行。
func TestDeriveReactRuntime(t *testing.T) {
	// 正常值
	r := deriveReactRuntime(config.ReactConfig{MaxIterations: 30, StepTimeout: 90, ErrorRetry: 3})
	if r.maxIterations != 30 {
		t.Errorf("maxIterations = %d, want 30", r.maxIterations)
	}
	if r.stepTimeout != 90*time.Second {
		t.Errorf("stepTimeout = %v, want 90s", r.stepTimeout)
	}
	if r.retryConfig == nil || r.retryConfig.MaxRetries != 3 {
		t.Errorf("retryConfig = %+v, want MaxRetries=3", r.retryConfig)
	}

	// 迭代零值回落默认，重试为 0 时不建 RetryConfig
	z := deriveReactRuntime(config.ReactConfig{})
	if z.maxIterations != 20 {
		t.Errorf("零值 maxIterations = %d, want 20", z.maxIterations)
	}
	if z.retryConfig != nil {
		t.Errorf("ErrorRetry=0 时 retryConfig 应为 nil，实际 %+v", z.retryConfig)
	}
	if z.stepTimeout != 0 {
		t.Errorf("零值 stepTimeout = %v, want 0", z.stepTimeout)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/agent/... -run TestDeriveReactRuntime -v`

Expected: 编译失败 `undefined: deriveReactRuntime`。

- [ ] **Step 3: 新增派生 helper 与结果类型**

在 `internal/agent/subagent_registry.go` 的 `SubAgentEntry` 结构体定义**之前**插入：

```go
// reactRuntime 是从 config.ReactConfig 派生出的、可直接喂给 adk 的推理参数。
type reactRuntime struct {
	maxIterations int                   // 已应用默认值（>=1）
	retryConfig   *adk.ModelRetryConfig // ErrorRetry 为 0 时为 nil
	stepTimeout   time.Duration         // 单步 LLM 调用超时
}

// deriveReactRuntime 把配置值换算成 adk 需要的形式。
//
// 每次 call_agent 调用现场执行，与 BuildAgentTool 跟随父 Agent model 的做法
// 一致：设置面板改动推理参数后，下一次子 Agent 调用即用新值，不必重启。
// Solo 模式（直接以某个子 Agent 身份对话）走 Executor 的 rt.React，
// 两条路径改动后取值一致。
func deriveReactRuntime(cfg config.ReactConfig) reactRuntime {
	maxIter := cfg.MaxIterations
	if maxIter <= 0 {
		maxIter = 20
	}
	var retryCfg *adk.ModelRetryConfig
	if cfg.ErrorRetry > 0 {
		retryCfg = &adk.ModelRetryConfig{MaxRetries: cfg.ErrorRetry}
	}
	return reactRuntime{
		maxIterations: maxIter,
		retryConfig:   retryCfg,
		stepTimeout:   time.Duration(cfg.StepTimeout) * time.Second,
	}
}
```

三个分支的逻辑与原 `buildSubAgentEntry` 第 372-380 行逐字等价，只是搬了位置。

- [ ] **Step 4: 运行 helper 测试确认通过**

Run: `go test ./internal/agent/... -run TestDeriveReactRuntime -v`

Expected: PASS。

- [ ] **Step 5: 从 SubAgentEntry 摘除三个字段**

把 `internal/agent/subagent_registry.go:41-59` 的结构体改为：

```go
type SubAgentEntry struct {
	Name        string
	Description string
	Instruction string            // agent.md 正文，Solo 模式 + BuildAgentTool 都使用
	MCPManager  *mcp.Manager      // 持有 MCP 连接生命周期
	SkillBK     einoskill.Backend // 供 /agents、/skills 查询；Watcher 热更新入口

	// 构建子 ChatModelAgent 所需的纯配置；BuildAgentTool 每次现场用这些拼装。
	// 推理循环参数（迭代/重试/超时）不在此列：它们随每次调用从运行时配置派生，
	// 见 deriveReactRuntime。
	AgentMdModel string                       // agent.md 中显式声明的 model；空字符串表示跟随父 Agent
	SkillMW      adk.ChatModelAgentMiddleware // 已构建的 skill middleware；可空
	Models       *llm.ModelService            // 模型配置读取入口

	// testTool 仅供 _test.go 注入预制 InvokableTool 跳过 BuildAgentTool 的真实
	// LLM dial。生产路径绝不写入；BuildAgentTool 在非 nil 时直接返回它。
	testTool tool.InvokableTool
}
```

删掉的是 `MaxIterations`、`RetryConfig`、`StepTimeout` 三行。结构体上方 20-40 行的注释块不动。

- [ ] **Step 6: 改 buildSubAgentEntry 去掉 reactCfg 参数**

`internal/agent/subagent_registry.go:331-337` 的函数签名改为：

```go
func buildSubAgentEntry(
	ctx context.Context,
	p parsedSubAgent,
	models *llm.ModelService,
	log *logger.Logger,
) (*SubAgentEntry, error) {
```

第 371-394 行（注释「3. ChatModelAgent 装配材料」到函数末尾）整段替换为：

```go
	// 3. ChatModelAgent 装配材料（不立即构建——见 SubAgentEntry 注释）。
	// 推理循环参数不在此固化，每次 BuildAgentTool 现场从运行时配置派生。
	return &SubAgentEntry{
		Name:         p.name,
		Description:  p.md.Description,
		Instruction:  p.md.Content,
		MCPManager:   mcpMgr,
		SkillBK:      skillBK,
		AgentMdModel: p.md.Model,
		SkillMW:      skillMW,
		Models:       models,
	}, nil
}
```

第 338-370 行（MCP Manager 与 Skill Backend 装配）**逐字不动**。

- [ ] **Step 7: 改 BuildSubAgentRegistry 去掉 reactCfg 参数**

`internal/agent/subagent_registry.go:293-300` 的签名改为：

```go
func BuildSubAgentRegistry(
	ctx context.Context,
	dir string,
	subCfg config.SubAgentConfig,
	models *llm.ModelService,
	log *logger.Logger,
) *SubAgentRegistry {
```

函数体内第 310 行的调用改为：

```go
		entry, err := buildSubAgentEntry(ctx, p, models, log)
```

其余行不动（Task 5 已把第 301 行改成 `resolveMaxConcurrency`）。

- [ ] **Step 8: 改 BuildAgentTool 接收 ReactConfig**

`internal/agent/subagent_registry.go:408` 的签名改为：

```go
func (e *SubAgentEntry) BuildAgentTool(ctx context.Context, parentModelName string, react config.ReactConfig, extraTools ...tool.BaseTool) (tool.InvokableTool, string, error) {
```

函数体第 409-418 行（modelName 计算与 testTool 短路）不动。在 `mdl, err := e.Models.GetByName(...)` 之前插入一行：

```go
	rr := deriveReactRuntime(react)
```

然后把函数体内三处对已删字段的引用替换为：

| 原代码 | 改为 |
|---|---|
| `llm.NewChatModel(ctx, mdl, e.StepTimeout)` | `llm.NewChatModel(ctx, mdl, rr.stepTimeout)` |
| `MaxIterations: e.MaxIterations,` | `MaxIterations: rr.maxIterations,` |
| `if e.RetryConfig != nil {`<br>`    agentCfg.ModelRetryConfig = e.RetryConfig` | `if rr.retryConfig != nil {`<br>`    agentCfg.ModelRetryConfig = rr.retryConfig` |

同时把第 397-407 行的函数注释里加一句：

```go
// react 是本次调用的推理循环参数（迭代/重试/超时），来自调用方当次读取的
// 运行时配置，因此设置面板改动后下一次 call_agent 即生效。
```

- [ ] **Step 9: CallAgentTool 透传 ReactConfig**

`internal/agent/call_agent.go:44-56` 的结构体加一个字段：

```go
type CallAgentTool struct {
	registry          *SubAgentRegistry
	parentChatID      string
	sessionID         string
	maxTaskLen        int
	maxResultLen      int
	execTimeout       time.Duration
	react             config.ReactConfig // 本次请求的推理循环参数，透传给 BuildAgentTool
	memory            *memory.Manager
	runtimeState      *RuntimeState
	tokenAccumulators *TokenAccumulators
	log               *logger.Logger
	parentRound       int
}
```

`internal/agent/call_agent.go:59-71` 的 `CallAgentToolConfig` 加同名导出字段：

```go
type CallAgentToolConfig struct {
	Registry          *SubAgentRegistry
	ParentChatID      string
	SessionID         string
	MaxTaskLen        int
	MaxResultLen      int
	ExecTimeout       time.Duration
	React             config.ReactConfig
	Memory            *memory.Manager
	RuntimeState      *RuntimeState
	TokenAccumulators *TokenAccumulators
	Log               *logger.Logger
	ParentRound       int
}
```

`internal/agent/call_agent.go:74-88` 的 `NewCallAgentTool` 加一行赋值 `react: cfg.React,`（放在 `execTimeout:` 之后）。

`internal/agent/call_agent.go:142` 的调用改为：

```go
	subTool, resolvedModel, buildErr := entry.BuildAgentTool(execCtx, parentModel, t.react)
```

`call_agent.go` 当前**没有** import `config` 包（import 块只有 `logger` 与 `memory` 两个项目内包）。在 `internal/agent/call_agent.go:13` 之前加一行：

```go
	"github.com/zfd81/groot/internal/config"
```

`executor.go` 无需改 import：它只是把 `rt.React` 透传进 `CallAgentToolConfig.React`，类型由字段声明决定，不需要在 executor.go 里引用 `config` 标识符。

- [ ] **Step 10: Executor 传入 rt.React**

`internal/agent/executor.go:203-214` 的构造加一个字段：

```go
			callAgent := NewCallAgentTool(CallAgentToolConfig{
				Registry:          e.subAgentRegistry,
				ParentChatID:      task.ID,
				SessionID:         sessionID,
				MaxTaskLen:        rt.SubAgent.MaxTaskLength,
				MaxResultLen:      rt.SubAgent.MaxResultLength,
				ExecTimeout:       execTimeout,
				React:             rt.React,
				Memory:            e.memoryManager,
				RuntimeState:      e.runtimeState,
				TokenAccumulators: e.tokenAccumulators,
				Log:               sessionLog,
				ParentRound:       task.Round,
			})
```

只新增 `React: rt.React,` 一行。`rt` 在第 135 行已读好，与 `MaxTaskLen`/`ExecTimeout` 用的是同一份快照。

- [ ] **Step 11: main.go 去掉 cfg.React 实参**

`cmd/groot/main.go:377` 改为：

```go
	subAgentReg := agent.BuildSubAgentRegistry(context.Background(), subAgentDir, cfg.SubAgent, modelService, log)
```

- [ ] **Step 12: 更新 Task 5 的测试，删掉临时的 ReactConfig 实参**

把 `internal/agent/subagent_registry_test.go` 中 `TestBuildSubAgentRegistry_RespectsSmallConcurrency` 的调用改为：

```go
	reg := BuildSubAgentRegistry(
		context.Background(),
		dir,
		config.SubAgentConfig{MaxConcurrency: 2},
		nil,
		logger.NewNop(),
	)
```

`call_agent_test.go` 里 `newTestCallAgentTool` 用具名字段构造结构体，新增的 `react` 字段取零值即可，**无需改动**；`newFakeEntry` 走 `testTool` 短路，也无需改动。

- [ ] **Step 13: 编译并跑 agent 包全部测试**

Run: `go build ./... && go test ./internal/agent/... -v`

Expected: 编译成功，全部 PASS。如果编译报 `too many arguments` 或 `undefined field`，说明 Step 6/7/8 某处漏改，按报错行号回查。

- [ ] **Step 14: 自查 diff**

```bash
git diff internal/agent/ cmd/groot/main.go
```

重点确认：`buildSubAgentEntry` 第 338-370 行的 MCP/Skill 装配逻辑没有被误动；`BuildAgentTool` 的 model 优先级三行（409-413）没动。**不要 commit**。

---

## Task 7: 前端占位默认值对齐后端

修轻微问题 1。纯数值对齐，无逻辑改动。`loaded` 守卫已挡住把占位值写回服务端的路径（`web/src/components/settings/SettingsModal.vue:101-104`），本任务只消除首屏一瞬的错误显示。

**Files:**
- Modify: `web/src/api/runtime.ts:67-87`

- [ ] **Step 1: 对照后端默认值改 7 个数**

后端权威来源是 `internal/setting/defaults.go:27-43`。把 `web/src/api/runtime.ts:67-87` 的 `defaultRuntimeSettings` 整个函数体替换为：

```ts
// defaultRuntimeSettings 仅用于首屏渲染的占位，真实值由接口返回后覆盖。
// 数值与 internal/setting/defaults.go 保持一致，避免首屏闪现与服务端不同的值。
export function defaultRuntimeSettings(): RuntimeSettings {
  return {
    memory: { history_window: 20 },
    react: { max_iterations: 20, step_timeout: 60, error_retry: 2 },
    subagent: {
      max_concurrency: 5,
      exec_timeout: '5m',
      max_task_length: 16000,
      max_result_length: 8000,
    },
    attachment: { max_size: 50, max_total_size: 100, max_count: 10, allowed_types: [] },
    rate_limit: {
      enabled: false,
      global_qps: 0,
      global_concurrency: 0,
      default_qps: 10,
      default_concurrency: 5,
    },
    schedule: { enabled: false },
  }
}
```

改动的 7 个值：

| 字段 | 原值 | 新值 |
|---|---|---|
| `react.max_iterations` | 30 | 20 |
| `react.step_timeout` | 120 | 60 |
| `subagent.max_concurrency` | 4 | 5 |
| `subagent.max_task_length` | 3000 | 16000 |
| `attachment.max_size` | 10 | 50 |
| `attachment.max_total_size` | 50 | 100 |
| `attachment.max_count` | 5 | 10 |

`memory`、`rate_limit`、`schedule`、`exec_timeout`、`max_result_length` 原本就一致，保持不动。

- [ ] **Step 2: 类型检查与构建**

`web/node_modules` 已安装，直接：

Run: `cd web && npm run build`

Expected: `vue-tsc -b` 无类型错误，`vite build` 成功输出到 `web/dist/`。这一步同时刷新了 `go:embed` 嵌入的前端产物，Task 9 的 Go 编译会带上新值。

- [ ] **Step 3: 自查 diff**

```bash
git diff web/src/api/runtime.ts
```

确认只有 7 个数字和一行注释变化。**不要 commit**。`web/dist/` 是否纳入版本控制按现有 `.gitignore` 规则，不额外处理。

---

## Task 8: 在设计文档中补迭代说明

按 CLAUDE.md 流程，缺陷修复不新建设计文档，在现有 spec 的「迭代说明」章节追加。对比性语言（修复、调整）只允许出现在这一章。

**Files:**
- Modify: `docs/superpowers/specs/2026-09-25-runtime-config-migration-design.md`（2.1 节末尾）

- [ ] **Step 1: 追加迭代条目**

在 `### 2.1 与上一版差异` 的最后一个条目（以 `- 保持不变：` 开头那行）之后、`### 2.2 后续独立迭代` 之前，追加：

```markdown

**2026-09-28 缺陷修复：**

- 修复：附件类型白名单按界面提示格式（`.png, .jpg`）填写时无法匹配任何附件。校验侧取扩展名已去点，而配置值未归一化。现在写入侧（`setting.normalizeAllowedTypes`）与消费侧（`attachment.normalizeTypes`）各做一次「去空白、去前导点、转小写」归一化；消费侧那一道兜住老 config.yaml 迁移进来的存量带点值，无需数据迁移。界面提示与手册不变，带点与不带点两种写法均有效。
- 修复：子 Agent 并发上限在启动期被 `max(n, 5)` 抬升，界面设 2 重启后实际跑 5 而界面仍显示 2。现改为 `resolveMaxConcurrency`：只在零值或负数时回落默认 5，区间内的值原样保留，与 `SetMaxConcurrency` 语义一致。
- 修复：`image`/`audio`/`video` 三类附件此前完全绕过单文件上限与总量累计，只有 `file` 受限。现在四类统一受体积限制。白名单适用范围保持 `file || image` 不变。
- 调整：子 Agent 的推理循环参数（迭代上限、重试、单步超时）从启动期固化进 `SubAgentEntry` 改为每次 `call_agent` 调用现场由 `deriveReactRuntime` 从运行时配置派生，与子 Agent 跟随父 Agent model 的做法一致。此前设置面板改动这三项后主 Agent 即时生效而子 Agent 要等重启；现在两者一致。`BuildAgentTool` 增加 `react config.ReactConfig` 参数；`BuildSubAgentRegistry` 与 `buildSubAgentEntry` 不再接收 `reactCfg`。
- 调整：前端首屏占位默认值（`web/src/api/runtime.ts`）7 项与 `internal/setting/defaults.go` 对齐；此前 `max_iterations` 等值不一致，只影响接口返回前一瞬的显示。
- 调整：`setting.ConfigurableSenders()` 导出可配置渠道列表；`api/handler/setting.go` 与 `main.go` 中三处对 webhook 的硬编码遍历改为使用它。当前只有一种渠道，行为无差异。
```

- [ ] **Step 2: 检查文档结构未被破坏**

Run: `grep -n "^## \|^### " docs/superpowers/specs/2026-09-25-runtime-config-migration-design.md`

Expected: 章节顺序仍为 一、功能设计 → 二、迭代说明（2.1、2.2）→ 三、测试，新内容落在 2.1 内。**不要 commit**。

---

## Task 9: 全量验证

所有任务完成后的最终关卡。任何一步失败都要回到对应任务修，不要在此处打补丁。

**Files:** 无新增改动。

- [ ] **Step 1: 格式检查**

Run: `gofmt -l internal/ cmd/`

Expected: **无输出**。若列出文件，对该文件执行 `gofmt -w <file>` 后重跑。

- [ ] **Step 2: 静态检查**

Run: `go vet ./...`

Expected: 无输出。

- [ ] **Step 3: 编译到 dist/**

Run: `go build -o dist/groot ./cmd`

Expected: 成功，`dist/groot` 更新。注意是 `./cmd` 而不是项目根目录。

- [ ] **Step 4: 全量单测（含竞争检测）**

Run: `go test ./... 2>&1 | grep -vE "^ok|no test files"`

Expected: **无输出**（所有包 ok 或无测试文件）。

再对本次触及并发的三个包加 `-race`：

Run: `go test -race ./internal/agent/... ./internal/attachment/... ./internal/setting/... -v 2>&1 | tail -5`

Expected: 末尾三行均为 `ok`。`subagent_registry` 的信号量替换与 `resolveMaxConcurrency` 都在这里覆盖。

- [ ] **Step 5: 回归确认「守住的逻辑」**

以下用例是本次明确不改的行为，逐个点名确认仍 PASS：

Run: `go test ./internal/attachment/... -run 'TestNewHandler|TestHandler_Validate_TypeNotAllowed|TestHandler_Validate_AllowedType|TestHandler_Validate_SizeExceeded|TestHandler_Validate_NoRestriction' -v`

Expected: 5 个全 PASS。它们分别覆盖：不带点白名单仍工作、白名单拒绝仍工作、白名单放行仍工作、file 类超限仍被拒、空白名单仍不限制。

Run: `go test ./internal/agent/... -run 'TestSubAgentRegistry_SetMaxConcurrency|TestCallAgentTool' -v`

Expected: 全 PASS。`SetMaxConcurrency` 与 `CallAgentTool` 的既有行为未变。

- [ ] **Step 6: 前端已在 Task 7 验证过；若中途重装依赖，重跑一次**

Run: `cd web && npm run build`

Expected: 成功。

- [ ] **Step 7: 汇总 diff 供用户审阅**

```bash
git status --short
git diff --stat
```

Expected 触及文件（共 12 个源文件 + 1 个 spec + 1 个 plan）：

```
 cmd/groot/main.go
 docs/superpowers/plans/2026-09-28-config-migration-bugfix.md
 docs/superpowers/specs/2026-09-25-runtime-config-migration-design.md
 internal/agent/call_agent.go
 internal/agent/executor.go
 internal/agent/subagent_registry.go
 internal/agent/subagent_registry_test.go
 internal/api/handler/setting.go
 internal/attachment/handler.go
 internal/attachment/handler_test.go
 internal/setting/message.go
 internal/setting/message_test.go
 internal/setting/runtime.go
 internal/setting/runtime_test.go
 web/src/api/runtime.ts
```

若 `web/dist/` 出现在 status 里，按现有 `.gitignore` 规则处理，不要手动加进提交。

**到此为止不 commit。** 向用户报告验证结果，等用户明确说「提交」再执行 git 操作。

---

## 执行顺序与依赖

```
Task 1 ─┐
Task 2 ─┤ attachment 包，可任意顺序，建议 1→2
Task 3 ─┤ setting 包，与 1/2 独立
Task 4 ─┘ setting + handler + main，与 1/2/3 独立
Task 5 ──→ Task 6   agent 包，5 必须在 6 之前（6 会删掉 5 测试里的临时实参）
Task 7    web，完全独立
Task 8    文档，建议所有代码任务完成后再写
Task 9    最后
```

Task 1–4、5–6、7 三组之间没有编译依赖，可并行分派给不同子 agent；Task 4 与 Task 6 都改 `cmd/groot/main.go` 但改的是不同行（370-371 vs 377），合并时不会冲突。

---

## Self-Review

**Spec coverage.** 逐条对照用户确认的最终方案：

| 方案条目 | 对应任务 |
|---|---|
| Bug 1 白名单：两处归一化，UI/README 不动 | Task 1（消费侧）+ Task 3（写入侧） |
| Bug 2 并发：去掉 `max(...,5)`，保留零值兜底，`Min` 不上调 | Task 5 |
| Bug 3 体积：四类统一受限，白名单范围保持 `file \|\| image` | Task 2 |
| Bug 4 热更新：三字段改为 `BuildAgentTool` 入参，`rt.React` 现场传入，去掉 registry 对 react 的依赖 | Task 6 |
| 轻微 1：7 个前端占位值对齐 | Task 7 |
| 轻微 2：导出 `ConfigurableSenders`，三处硬编码改遍历（handler 两处 + main 一处） | Task 4 |
| 「不影响现有正确逻辑」 | 每任务的「明确不改」说明 + Task 9 Step 5 点名回归 |
| 补 spec 说明而非新建设计文档 | Task 8 |

无遗漏。

**Placeholder scan.** 全文搜索「TBD / TODO / 类似 Task N / 适当处理 / 补充测试」：无。每个代码步骤都给出了完整代码；Task 6 Step 8 用表格列出三处替换是因为原函数体较长且只改三行，表格比重贴 60 行更不易出错，且每一格都是可直接粘贴的完整代码行。

**Type consistency.**
- `normalizeTypes`（attachment 包）与 `normalizeAllowedTypes`（setting 包）是两个不同包里的同逻辑函数，故意分开命名以免读者以为跨包共享；Task 8 的 spec 条目里两个名字都提到了。
- `reactRuntime` 的三个小写字段 `maxIterations`/`retryConfig`/`stepTimeout` 在 Task 6 Step 1 测试、Step 3 定义、Step 8 替换表中拼写一致。
- `BuildAgentTool(ctx, parentModelName string, react config.ReactConfig, extraTools ...tool.BaseTool)` 的参数顺序在 Step 8 签名与 Step 9 调用 `entry.BuildAgentTool(execCtx, parentModel, t.react)` 一致。
- `CallAgentToolConfig.React` 与 `CallAgentTool.react` 在 Step 9 定义、Step 10 executor 赋值中一致。
- `resolveMaxConcurrency` 在 Task 5 Step 1 定义、Step 3 调用、Task 8 spec 条目中一致。
- `ConfigurableSenders()` 在 Task 4 Step 1 测试、Step 3 定义、Step 4/5/6 三处调用中一致。
- Task 5 Step 4 测试里带 `config.ReactConfig{}` 临时实参，Task 6 Step 12 明确删除它，前后呼应。
