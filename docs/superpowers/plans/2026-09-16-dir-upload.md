# 目录上传 Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 让工作空间文件面板支持把本地整个目录上传到 home 内任意已存在目录。

**Architecture:** 两步流程。第一步前端调 `POST /web/files/upload/prepare`，服务层用 `os.Mkdir`（非 `MkdirAll`）在目标目录下建出本地目录的同名目录——创建成功即证明原先不存在，占位与冲突检测一次完成、无先查后建竞态；已存在则返回 409，前端一个文件都不传。第二步前端按每个文件的相对路径逐个调用既有上传接口，multipart 新增可选 `relpath` 字段，服务层按 `/` 切段逐段过 `validName`，再按需创建中间目录。`relpath` 缺省时行为与单文件上传完全一致。

**Tech Stack:** Go（hertz + 标准库 `os`）、Vue 3 `<script setup>` + TypeScript + Element Plus。后端单元测试用 Go 标准测试框架，前端验证门槛为 `npx vue-tsc -b` 与 `npm run build`（项目无前端测试框架，不引入）。

**Spec:** `docs/superpowers/specs/2026-09-13-file-panel-design.md` §1.4.4 / §1.4.5 / §2.3

---

## 文件结构

| 文件 | 责任 |
|---|---|
| `internal/webfiles/service.go` | 修改：新增 `Mkdir`（占位原语）；`UploadTarget` 第二参数由文件名改为相对路径 |
| `internal/webfiles/service_test.go` | 修改：新增 `Mkdir` 与嵌套 `relpath` 的单元测试 |
| `internal/api/handler/files.go` | 修改：新增 `UploadPrepare` handler；`Upload` 读取可选 `relpath` 表单字段 |
| `internal/api/handler/files_test.go` | 修改：新增 `UploadPrepare` 的端点测试 |
| `internal/api/router.go` | 修改：注册 `POST /web/files/upload/prepare` |
| `web/src/api/files.ts` | 修改：新增 `uploadPrepare`；`upload` 增加可选 `relpath` 参数 |
| `web/src/components/files/FileTree.vue` | 修改：菜单拆为「上传文件」「上传目录」，新增目录 input 与批量上传编排、进度显示 |
| `web/src/i18n/messages/{zh-cn,en}.ts` | 修改：`upload` 拆成两条，新增进度与结果提示词条 |

**任务顺序：** Task 1 服务层（纯 Go、可独立测试）→ Task 2 端点与路由 → Task 3 前端 API 封装 → Task 4 i18n 与界面编排。每个 Task 自带测试与提交。

## 关键背景（实现者必读）

1. **`validName` 是全套路径防线的唯一入口**（`internal/webfiles/service.go:160`）：拒绝空串、`.`、`..`、以 `.` 开头、尾点、尾空格，并要求匹配 `nameRe`（字母/数字开头，允许字母数字空格括号点下划线连字符，≤64 字符）。逐段校验相对路径就是对每一段调用它，防线不放宽只是从一段变多段。
2. **`Resolver.Resolve` 允许目标不存在**（`internal/webfiles/resolver.go:83`），它只保证已存在的祖先没有经符号链接逃出 home。所以"目录必须已存在"必须由调用方显式 `os.Stat` 检查。
3. **`UploadTarget` 现有六项检查顺序不能乱**：大小 → 名字 → 基准目录可上传 → 目标只读 → 父目录存在 → 目标不存在。改造后要在**创建中间目录之前**确认基准目录已存在，否则 `MkdirAll` 会把不存在的基准目录一起造出来，`UploadTarget("nope", "A/x.md", ...)` 就不再返回 404 了。
4. **错误映射已就绪**（`internal/api/handler/files.go:29` 的 `writeFilesError`）：`ErrExists`→409、`ErrNotFound`→404、`ErrForbidden`→403、`ErrInvalid`→400。新端点直接复用，不要自己写状态码。
5. **前端 `run()` 辅助函数**（`web/src/components/files/FileTree.vue:144`）会在成功后刷新文件树、失败时弹 `ElMessage.error`。目录上传是多次请求的编排，**不要**套在 `run()` 里逐个调用（会刷新 N 次树、弹 N 次错误），要自己写编排并在结束时刷新一次。
6. **进度显示用 `v-loading`**，项目既有写法见 `web/src/components/settings/ApiKeysPanel.vue:223`（`<div v-loading="loading">`）。配 `element-loading-text` 传响应式文案即可，不引入新的加载组件。
7. **浏览器目录选择的行为**：`<input type="file" webkitdirectory multiple>` 只上报文件、不上报空目录，每个 `File` 的 `webkitRelativePath` 形如 `A/sub/note.md`（首段即所选目录名，始终用 `/` 分隔）。不支持该属性的浏览器要隐藏「上传目录」入口。

---

## Task 1: 服务层——占位原语与相对路径上传

**Files:**
- Modify: `internal/webfiles/service.go:293`（`UploadTarget`），并在其前面新增 `Mkdir`
- Test: `internal/webfiles/service_test.go`（在 `TestService_UploadTarget` 后追加）

- [ ] **Step 1: 写失败的测试**

在 `internal/webfiles/service_test.go` 中 `TestService_UploadTarget`（第 342 行结束）之后追加两个测试函数。夹具 `newServiceForTest` 已提供 `skills/my-skill/`、`mcp/`、`logs/`、`GROOT.md`、`config.yaml`。

```go
// TestService_Mkdir 验证目录上传的占位原语：正常创建、冲突、越界与非法名字。
func TestService_Mkdir(t *testing.T) {
	svc, home := newServiceForTest(t)

	if err := svc.Mkdir("skills", "brand-new"); err != nil {
		t.Fatalf("在已存在目录下建目录应成功, got %v", err)
	}
	if info, err := os.Stat(filepath.Join(home, "skills", "brand-new")); err != nil || !info.IsDir() {
		t.Errorf("目录未真正创建: %v", err)
	}
	// 第二次同名 → 冲突（占位即冲突检测，无需先查后建）
	if err := svc.Mkdir("skills", "brand-new"); !errors.Is(err, ErrExists) {
		t.Errorf("同名目录应 ErrExists, got %v", err)
	}
	// 与已存在的文件同名也算冲突
	if err := svc.Mkdir("", "GROOT.md"); !errors.Is(err, ErrExists) {
		t.Errorf("与已存在文件同名应 ErrExists, got %v", err)
	}
	// 父目录不存在
	if err := svc.Mkdir("skills/nope", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("父目录不存在应 ErrNotFound, got %v", err)
	}
	// 非法名字：空、点、双点、点开头、含分隔符、尾点、尾空格
	for _, name := range []string{"", ".", "..", ".hidden", "a/b", "trail.", "trail "} {
		if err := svc.Mkdir("skills", name); !errors.Is(err, ErrInvalid) {
			t.Errorf("非法名字 %q 应 ErrInvalid, got %v", name, err)
		}
	}
	// home 根下允许建目录
	if err := svc.Mkdir("", "toplevel"); err != nil {
		t.Errorf("home 根下建目录应成功, got %v", err)
	}
}

// TestService_UploadTargetRelpath 验证嵌套相对路径的逐段校验与中间目录创建。
func TestService_UploadTargetRelpath(t *testing.T) {
	svc, home := newServiceForTest(t)

	abs, err := svc.UploadTarget("skills", "A/sub/note.md", 100)
	if err != nil {
		t.Fatalf("嵌套上传应成功, got %v", err)
	}
	if want := filepath.Join(home, "skills", "A", "sub", "note.md"); abs != want {
		t.Errorf("目标路径 = %q, want %q", abs, want)
	}
	if info, err := os.Stat(filepath.Join(home, "skills", "A", "sub")); err != nil || !info.IsDir() {
		t.Errorf("中间目录应已按需创建: %v", err)
	}
	// 逐段校验：任一段非法即整体拒绝
	for _, rp := range []string{"../evil.sh", "A/../evil.sh", ".git/config", "A/.hidden/x.md", "A//x.md", ""} {
		if _, err := svc.UploadTarget("skills", rp, 100); !errors.Is(err, ErrInvalid) {
			t.Errorf("非法 relpath %q 应 ErrInvalid, got %v", rp, err)
		}
	}
	// 基准目录不存在时必须 404，且不得顺手把它创建出来
	if _, err := svc.UploadTarget("nope", "A/x.md", 100); !errors.Is(err, ErrNotFound) {
		t.Errorf("基准目录不存在应 ErrNotFound, got %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "nope")); !os.IsNotExist(err) {
		t.Error("基准目录不存在时不应创建任何目录")
	}
	// 目标文件已存在
	if err := os.WriteFile(filepath.Join(home, "skills", "A", "sub", "exists.md"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.UploadTarget("skills", "A/sub/exists.md", 100); !errors.Is(err, ErrExists) {
		t.Errorf("目标已存在应 ErrExists, got %v", err)
	}
	// 中间某段已作为文件存在 → 冲突而非内部错误
	if _, err := svc.UploadTarget("", "GROOT.md/x.md", 100); !errors.Is(err, ErrExists) {
		t.Errorf("中间段是文件应 ErrExists, got %v", err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/webfiles/ -run 'TestService_Mkdir|TestService_UploadTargetRelpath' -v`
Expected: 编译失败，`svc.Mkdir undefined (type *Service has no field or method Mkdir)`

- [ ] **Step 3: 实现 Mkdir**

在 `internal/webfiles/service.go` 的 `UploadTarget` 之前插入：

```go
// Mkdir 在 dirRel 下创建单层目录 name，服务于目录上传的占位步骤。
// 用 os.Mkdir 而非 MkdirAll：创建成功即证明 name 原先不存在，占位与冲突
// 检测一次完成，没有先查后建的竞态。不提供通用建目录能力（无菜单入口）。
func (s *Service) Mkdir(dirRel, name string) error {
	if !validName(name) {
		return ErrInvalid
	}
	dirAbs, dirN, err := s.res.Resolve(dirRel)
	if err != nil {
		return err
	}
	if !s.res.CanUpload(dirN) {
		return ErrForbidden
	}
	if info, err := os.Stat(dirAbs); err != nil || !info.IsDir() {
		return ErrNotFound
	}
	abs, n, err := s.res.Resolve(joinRel(dirN, name))
	if err != nil {
		return err
	}
	if s.res.ReadOnly(n) {
		return ErrReadOnly
	}
	if err := os.Mkdir(abs, 0o755); err != nil {
		if os.IsExist(err) {
			return ErrExists
		}
		return err
	}
	return nil
}
```

- [ ] **Step 4: 改造 UploadTarget 接受相对路径**

把 `internal/webfiles/service.go` 中整个 `UploadTarget` 函数（含其上方的注释）替换为：

```go
// UploadTarget 校验上传请求（白名单、大小、路径各段、重名），按需创建
// relpath 的中间目录，返回可直接写入的目标绝对路径；落盘由 handler 完成。
// relpath 是以 "/" 分隔的相对路径（目录上传时形如 "A/sub/note.md"），
// 单文件上传时就是文件名。每段独立过 validName——路径防线不放宽，
// 只是从校验一个名字变为逐段校验一串名字。
func (s *Service) UploadTarget(dirRel, relpath string, size int64) (string, error) {
	if size > MaxUploadSize {
		return "", ErrTooLarge
	}
	segs := strings.Split(relpath, "/")
	for _, seg := range segs {
		if !validName(seg) {
			return "", ErrInvalid
		}
	}
	dirAbs, dirN, err := s.res.Resolve(dirRel)
	if err != nil {
		return "", err
	}
	if !s.res.CanUpload(dirN) {
		return "", ErrForbidden
	}
	// 基准目录必须已存在：只有 relpath 内部的中间目录才按需创建，
	// 否则 MkdirAll 会把不存在的基准目录一起造出来，掩盖 404。
	if info, err := os.Stat(dirAbs); err != nil || !info.IsDir() {
		return "", ErrNotFound
	}
	abs, n, err := s.res.Resolve(joinRel(dirN, relpath))
	if err != nil {
		return "", err
	}
	if s.res.ReadOnly(n) {
		return "", ErrReadOnly
	}
	if _, err := os.Lstat(abs); err == nil {
		return "", ErrExists
	}
	if len(segs) > 1 {
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			// 中间某段已作为文件存在等情况，按冲突处理而非内部错误
			return "", ErrExists
		}
	}
	return abs, nil
}
```

单段场景（`len(segs) == 1`）不再单独 stat 父目录——它就是刚校验过的 `dirAbs`。

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/webfiles/ -v && gofmt -l internal/webfiles/`
Expected: 全部 PASS（含既有的 `TestService_UploadTarget`，它传的仍是单段文件名，行为不变）；`gofmt -l` 无输出。

- [ ] **Step 6: 提交**

```bash
git add internal/webfiles/service.go internal/webfiles/service_test.go
git commit -m "feat(webfiles): 新增 Mkdir 占位原语，UploadTarget 支持相对路径"
```

---

## Task 2: 端点与路由

**Files:**
- Modify: `internal/api/handler/files.go:119-135`（`Upload`），并在其前面新增 `UploadPrepare`
- Modify: `internal/api/router.go:71`（在 `/upload` 注册之后加一行）
- Test: `internal/api/handler/files_test.go`（文件末尾追加）

- [ ] **Step 1: 写失败的测试**

在 `internal/api/handler/files_test.go` 末尾追加。夹具 `newFilesHandlerForTest` 提供 `skills/demo/`、`mcp/`、`GROOT.md`、`config.yaml`、`groot.db`；`jsonCtx` 构造 JSON body 上下文。

```go
// TestFilesHandler_UploadPrepare 验证目录上传占位端点的成功与各错误状态码。
func TestFilesHandler_UploadPrepare(t *testing.T) {
	h, home := newFilesHandlerForTest(t)

	rc := jsonCtx(consts.MethodPost, `{"path":"skills","name":"uploaded-dir"}`)
	h.UploadPrepare(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("占位 status = %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if info, err := os.Stat(filepath.Join(home, "skills", "uploaded-dir")); err != nil || !info.IsDir() {
		t.Errorf("占位目录未创建: %v", err)
	}

	// 重复占位 → 409，前端据此终止整批上传
	rc = jsonCtx(consts.MethodPost, `{"path":"skills","name":"uploaded-dir"}`)
	h.UploadPrepare(context.Background(), rc)
	if rc.Response.StatusCode() != 409 {
		t.Errorf("重复占位 status = %d, want 409", rc.Response.StatusCode())
	}

	// 父目录不存在 → 404
	rc = jsonCtx(consts.MethodPost, `{"path":"nope","name":"x"}`)
	h.UploadPrepare(context.Background(), rc)
	if rc.Response.StatusCode() != 404 {
		t.Errorf("父目录不存在 status = %d, want 404", rc.Response.StatusCode())
	}

	// 非法名字 → 400
	for _, body := range []string{
		`{"path":"skills","name":"../evil"}`,
		`{"path":"skills","name":".hidden"}`,
		`{"path":"skills","name":""}`,
	} {
		rc = jsonCtx(consts.MethodPost, body)
		h.UploadPrepare(context.Background(), rc)
		if rc.Response.StatusCode() != 400 {
			t.Errorf("body %s status = %d, want 400", body, rc.Response.StatusCode())
		}
	}

	// 非法 JSON → 400
	rc = jsonCtx(consts.MethodPost, `{"path":`)
	h.UploadPrepare(context.Background(), rc)
	if rc.Response.StatusCode() != 400 {
		t.Errorf("非法 JSON status = %d, want 400", rc.Response.StatusCode())
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/api/handler/ -run TestFilesHandler_UploadPrepare -v`
Expected: 编译失败，`h.UploadPrepare undefined`

- [ ] **Step 3: 实现 UploadPrepare 并让 Upload 读取 relpath**

在 `internal/api/handler/files.go` 的 `Upload` 之前插入：

```go
// UploadPrepare 处理 POST /web/files/upload/prepare：为目录上传创建目标根目录。
// 目录已存在时返回 409，前端据此终止整批上传。它是上传流程的内部原语，
// 不在界面上提供通用「新建目录」入口。
func (h *FilesHandler) UploadPrepare(ctx context.Context, rc *app.RequestContext) {
	var req struct {
		Path string `json:"path"`
		Name string `json:"name"`
	}
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求体解析失败"})
		return
	}
	if err := h.svc.Mkdir(req.Path, req.Name); err != nil {
		writeFilesError(rc, err)
		return
	}
	rc.JSON(200, utils.H{"status": "success"})
}
```

再把 `Upload` 中取目标路径的那一行（`internal/api/handler/files.go:125`）替换为：

```go
	// relpath 承载目录上传的相对路径（如 "A/sub/note.md"）；缺省时退化为
	// 单文件上传，等价于文件名本身。
	relpath := rc.PostForm("relpath")
	if relpath == "" {
		relpath = fh.Filename
	}
	dst, err := h.svc.UploadTarget(rc.PostForm("path"), relpath, fh.Size)
```

- [ ] **Step 4: 注册路由**

在 `internal/api/router.go:71`（`filesGroup.POST("/upload", filesH.Upload)`）之后插入一行：

```go
		filesGroup.POST("/upload/prepare", filesH.UploadPrepare)
```

- [ ] **Step 5: 运行测试确认通过**

Run: `go test ./internal/api/... ./internal/webfiles/... -count=1 && go build ./... && gofmt -l internal/api/handler/files.go internal/api/router.go`
Expected: 测试全部 ok（既有的上传测试不受影响——`relpath` 缺省时行为不变），构建通过，`gofmt -l` 无输出。

- [ ] **Step 6: 提交**

```bash
git add internal/api/handler/files.go internal/api/handler/files_test.go internal/api/router.go
git commit -m "feat(api): 新增目录上传占位端点，上传接口支持 relpath"
```

---

## Task 3: 前端 API 封装

**Files:**
- Modify: `web/src/api/files.ts:19-36`

- [ ] **Step 1: 新增 uploadPrepare 并给 upload 加 relpath**

把 `web/src/api/files.ts` 中的 `upload` 方法（第 19-36 行）整段替换为下面两个方法。`uploadPrepare` 走 `api.post`（JSON 请求，`client.ts` 已封装 401 与 `ApiError`，其 `code` 取自 `data.code || data.status`，因此调用方可用 `err.code === 'exists'` 判断冲突）；`upload` 仍走原生 fetch，因为 multipart 与 `client.ts` 的 JSON 封装不兼容。

```ts
  // 目录上传第一步：在 dir 下创建目标根目录 name。已存在时抛 409（code 为 'exists'）。
  uploadPrepare: (dir: string, name: string) =>
    api.post<{ status: string }>('/web/files/upload/prepare', { path: dir, name }),

  // relpath 供目录上传携带文件在所选目录内的相对路径（如 "A/sub/note.md"）；
  // 省略时后端按文件名处理，即单文件上传。
  async upload(dir: string, file: File, relpath?: string): Promise<void> {
    const fd = new FormData()
    fd.append('path', dir)
    fd.append('file', file)
    if (relpath) fd.append('relpath', relpath)
    const resp = await fetch('/web/files/upload', {
      method: 'POST',
      body: fd,
      credentials: 'same-origin',
    })
    if (resp.status === 401) {
      notifyUnauthorized()
      throw new ApiError(401, 'unauthorized')
    }
    if (!resp.ok) {
      const data = await resp.json().catch(() => null)
      throw new ApiError(resp.status, data?.message || `upload failed: ${resp.status}`, data?.status)
    }
  },
```

- [ ] **Step 2: 验证**

Run: `cd web && npx vue-tsc -b`
Expected: 零错误。既有单文件调用 `filesApi.upload(uploadDir.value, file)` 因第三参可选而无需改动。

- [ ] **Step 3: 提交**

```bash
git add web/src/api/files.ts
git commit -m "feat(web): 上传 API 支持目录占位与 relpath"
```

---

## Task 4: 界面——菜单拆分与批量上传编排

**Files:**
- Modify: `web/src/i18n/messages/zh-cn.ts:252`、`web/src/i18n/messages/en.ts:252`
- Modify: `web/src/components/files/FileTree.vue`（菜单项、脚本、模板、样式）

- [ ] **Step 1: 加 i18n 词条**

`web/src/i18n/messages/zh-cn.ts` 第 252 行 `upload: '上传',` 替换为：

```ts
    uploadFile: '上传文件',
    uploadDir: '上传目录',
    uploadDirProgress: '正在上传 {done}/{total}',
    uploadDirDone: '已上传 {total} 个文件',
    uploadDirTooMany: '所选目录包含 {count} 个文件，超出单批 {limit} 个的上限',
    uploadDirEmpty: '所选目录没有可上传的文件',
    uploadDirExists: '目标位置已存在同名目录 {name}，请先删除后重试',
    uploadDirFailed: '上传中断于 {path}（已成功 {done}/{total}）；已上传的部分保留，请删除目标目录后重试',
```

`web/src/i18n/messages/en.ts` 第 252 行 `upload: 'Upload',` 替换为同一组 key：

```ts
    uploadFile: 'Upload file',
    uploadDir: 'Upload folder',
    uploadDirProgress: 'Uploading {done}/{total}',
    uploadDirDone: 'Uploaded {total} files',
    uploadDirTooMany: 'The selected folder has {count} files, over the per-batch limit of {limit}',
    uploadDirEmpty: 'The selected folder has no uploadable files',
    uploadDirExists: 'A folder named {name} already exists at the target; delete it and retry',
    uploadDirFailed: 'Upload stopped at {path} ({done}/{total} succeeded); uploaded files are kept — delete the target folder and retry',
```

原 `files.upload` 被移除。改完后确认无残留引用：`grep -rn "files\.upload'\|t('files.upload')" web/src` 应无输出（模板中的引用在 Step 3 一并替换）。

- [ ] **Step 2: 改脚本——菜单分支与上传编排**

在 `web/src/components/files/FileTree.vue` 中，把 `handleCommand` 的 `case 'upload'` 分支（第 189-192 行）替换为两个分支：

```ts
    case 'uploadFile': {
      uploadDir.value = data.path
      uploadInput.value?.click()
      break
    }
    case 'uploadDir': {
      uploadDir.value = data.path
      dirInput.value?.click()
      break
    }
```

再把「上传：隐藏 input，change 后提交」那一段（第 201-210 行，`uploadInput` 声明到 `onUploadChange` 结束）整段替换为：

```ts
// 上传：隐藏 input，change 后提交
const uploadInput = ref<HTMLInputElement | null>(null)
const dirInput = ref<HTMLInputElement | null>(null)
const uploadDir = ref('')

// 目录上传单批上限，与设计文档 §1.4.5 一致（服务端逐文件受理，看不到整批）
const MAX_DIR_FILES = 500

// 目录上传进度：非空表示正在上传，用于 v-loading 与文案
const uploading = ref(false)
const progress = ref({ done: 0, total: 0 })
const progressText = computed(() =>
  t('files.uploadDirProgress', { done: progress.value.done, total: progress.value.total }),
)

// 浏览器不支持目录选择时隐藏「上传目录」入口
const dirUploadSupported = 'webkitdirectory' in HTMLInputElement.prototype

async function onUploadChange(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = '' // 允许连续上传同一个文件
  if (!file) return
  await run(() => filesApi.upload(uploadDir.value, file))
}

// 目录上传：占位建目标目录 → 逐文件上传。任一环节失败即停，
// 已上传部分保留（设计文档 §1.4.5），由用户删除目标目录后重试。
async function onDirChange(e: Event) {
  const input = e.target as HTMLInputElement
  const picked = Array.from(input.files ?? [])
  input.value = ''
  if (!picked.length) return

  // 相对路径任一段以 "." 开头的一律跳过，与列表接口过滤隐藏项一致。
  // 局部变量不能叫 files——组件顶层已有 const files = useFilesStore()。
  const items = picked.filter((f) => {
    const rel = f.webkitRelativePath
    return rel !== '' && !rel.split('/').some((seg) => seg.startsWith('.'))
  })
  if (!items.length) {
    ElMessage.warning(t('files.uploadDirEmpty'))
    return
  }
  if (items.length > MAX_DIR_FILES) {
    ElMessage.error(t('files.uploadDirTooMany', { count: items.length, limit: MAX_DIR_FILES }))
    return
  }

  const dir = uploadDir.value
  const rootName = items[0].webkitRelativePath.split('/')[0]
  uploading.value = true
  progress.value = { done: 0, total: items.length }
  try {
    try {
      await filesApi.uploadPrepare(dir, rootName)
    } catch (err: any) {
      // 目标同名目录已存在：一个文件都不传
      if (err?.code === 'exists') {
        ElMessage.error(t('files.uploadDirExists', { name: rootName }))
      } else {
        ElMessage.error(err?.message || t('files.opFailed'))
      }
      return
    }
    for (const f of items) {
      const rel = f.webkitRelativePath
      try {
        await filesApi.upload(dir, f, rel)
      } catch {
        ElMessage.error(
          t('files.uploadDirFailed', {
            path: rel,
            done: progress.value.done,
            total: items.length,
          }),
        )
        return
      }
      progress.value.done++
    }
    ElMessage.success(t('files.uploadDirDone', { total: items.length }))
  } finally {
    uploading.value = false
    // 编排自己刷新一次树，不套 run()——否则每个文件都会刷新一次、弹一次错
    files.refresh()
  }
}
```

`computed` 需要加入第 3 行的 vue import：`import { computed, ref } from 'vue'`。

- [ ] **Step 3: 改模板——菜单项、目录 input、进度遮罩**

把 `web/src/components/files/FileTree.vue` 模板中的上传菜单项（第 250-252 行）替换为：

```vue
                <el-dropdown-item v-if="!data.leaf" command="uploadFile">
                  {{ t('files.uploadFile') }}
                </el-dropdown-item>
                <el-dropdown-item v-if="!data.leaf && dirUploadSupported" command="uploadDir">
                  {{ t('files.uploadDir') }}
                </el-dropdown-item>
```

把根节点 `<div class="file-tree">` 改为带遮罩（`v-loading` 项目既有写法见 `ApiKeysPanel.vue:223`）：

```vue
  <div class="file-tree" v-loading="uploading" :element-loading-text="progressText">
```

把末尾的隐藏 input（第 265 行）替换为两个：

```vue
    <input ref="uploadInput" type="file" hidden @change="onUploadChange" />
    <input ref="dirInput" type="file" webkitdirectory multiple hidden @change="onDirChange" />
```

- [ ] **Step 4: 验证**

Run: `cd web && npx vue-tsc -b && npm run build`
Expected: 零类型错误，构建通过。再确认词条无残留与新 input 已就位：

```bash
cd web
grep -rn "files\.upload'" src/          # 应无输出（旧词条已移除）
grep -n "webkitdirectory" src/components/files/FileTree.vue   # 应有一行
diff <(grep -o "uploadDir[A-Za-z]*:" src/i18n/messages/zh-cn.ts | sort) \
     <(grep -o "uploadDir[A-Za-z]*:" src/i18n/messages/en.ts | sort)   # 应无差异
```

- [ ] **Step 5: 手工验收**

启动服务后在浏览器 `/ui` 打开工作空间面板：

1. 任意目录行 `⋯` → 「上传目录」→ 选一个含子目录的本地目录 → 确认选择器的「打开」按钮此时可点（这正是本次要修的现象）
2. 上传过程中出现「正在上传 N/M」遮罩，结束后树中出现完整目录结构
3. 对同一目标目录重复上传同一个目录 → 提示同名目录已存在，且**没有**新文件被写入
4. 「上传文件」入口行为不变

- [ ] **Step 6: 提交**

```bash
git add web/src/components/files/FileTree.vue web/src/i18n/messages/zh-cn.ts web/src/i18n/messages/en.ts
git commit -m "feat(web): 工作空间支持目录上传"
```

---

## 完成标准

- `go test ./internal/... -count=1` 全绿，`go build ./...` 通过，`gofmt -l` 对改动文件无输出
- `cd web && npx vue-tsc -b && npm run build` 通过
- 手工验收 4 项全部符合预期
- 未新增测试目录或测试工具，未在根目录留下产物（项目规范）
