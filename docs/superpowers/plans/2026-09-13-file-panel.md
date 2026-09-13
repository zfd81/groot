# 文件面板功能实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 Groot Web 对话界面右侧增加文件面板，浏览/预览/编辑/管理 `~/.groot` 目录内容，并支持一键创建 skill/mcp/agent。

**Architecture:** 后端新增 `internal/webfiles` 包（路径安全 Resolver + 文件操作 Service）与 `internal/api/handler/files.go`（HTTP 绑定），路由挂 `/web/files/*`（登录会话保护）。前端新增 `components/files/` 下 4 个组件 + `stores/files.ts`，CodeMirror 6 动态加载。设计规格见 `docs/superpowers/specs/2026-09-13-file-panel-design.md`。

**Tech Stack:** Go + Hertz（后端）、Vue 3 + Element Plus + Pinia + CodeMirror 6（前端）、Go 标准测试 + pytest（测试）。

**⚠️ 项目规则（覆盖本计划模板的默认行为）：**
1. **禁止自动 git commit**——每个任务结束只运行测试/构建并报告结果，提交由用户明确指令触发。
2. Python 系统测试只编写、不运行（用户自行运行）。
3. 编译产物只输出到 `dist/`。

---

## 背景知识（给零上下文的实施者）

- **Groot home 目录**：默认 `~/.groot`（可被 `GROOT_HOME` 覆盖），存放 skills/、subagents/、mcp/、logs/、config.yaml、env.yaml、groot.db 等。`homeDir` 在 `internal/api/server.go` 的 `NewServer` 参数里现成可用。
- **响应约定**：成功 `{"status":"success", ...}`；失败 `{"status":"<错误码>","message":"..."}`（参照 `internal/api/handler/logs.go`）。
- **认证**：`/web/*` 分组路由经 `middleware.WebSession` 保护（见 `internal/api/router.go:43-44`），handler 内无需关心认证。
- **handler 单测模式**：`app.NewContext(0)` 构造 RequestContext，直接调 handler 方法，断言 `rc.Response`（参照 `internal/api/handler/logs_test.go`）。
- **前端**：Vue 3 `<script setup>` + TS。Element Plus 组件由 unplugin 自动导入（无需手动 import el-*）；图标需显式 import（`@element-plus/icons-vue`）。`useI18n` 全局可用（auto-import）。API 封装 `web/src/api/client.ts` 导出 `api.get/post/put/delete`。
- **运行命令**：后端测试 `go test ./internal/... -v`；前端构建（含类型检查）`cd web && npm run build`。

### 核心安全规则（全部在 Task 1 的 Resolver 实现）

| 规则 | 行为 |
|---|---|
| 路径限制在 home 内 | `Clean("/"+rel)` 锚定防穿越 + 符号链接真实路径前缀校验，越界返回 `ErrNotFound` |
| `groot.db*`（根目录）完全隐藏 | 列表过滤 + 直接访问返回 `ErrNotFound`（404，与不存在不可区分） |
| `.DS_Store` | 仅列表过滤（直接访问不拦截） |
| `env.yaml`/`config.yaml`（根目录）只读 | 可列出（readonly 标记）/读/下载；写/改名/删除返回 `ErrReadOnly`（403） |
| 新建文件/子目录白名单 | 仅 `skills/<名称>/` 及更深；其他位置 `ErrForbidden`（403） |
| 上传白名单 | 新建白名单 + `skills/`、`mcp/` 顶层 |
| 删除 | 仅文件和空目录；非空目录 `ErrNotEmpty`（409） |
| 大小限制 | 读/存文本 ≤ 2MB（`ErrTooLarge`→413）；上传 ≤ 20MB |

---

### Task 1: webfiles 包 —— 路径安全 Resolver

**Files:**
- Create: `internal/webfiles/resolver.go`
- Test: `internal/webfiles/resolver_test.go`

- [ ] **Step 1: 写失败的测试**

```go
// internal/webfiles/resolver_test.go
package webfiles

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// newResolverForTest 在临时目录上构造 Resolver。
func newResolverForTest(t *testing.T) (*Resolver, string) {
	t.Helper()
	home := t.TempDir()
	r, err := NewResolver(home)
	if err != nil {
		t.Fatalf("NewResolver: %v", err)
	}
	return r, home
}

// TestResolver_Normalize 验证路径清洗：".." 无法越过根，反斜杠统一为斜杠。
func TestResolver_Normalize(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"skills/foo", "skills/foo"},
		{"/skills/foo/", "skills/foo"},
		{"../../etc/passwd", "etc/passwd"}, // 锚定后穿越被折叠进根
		{"skills/../mcp", "mcp"},
		{"a\\b", "a/b"},
	}
	for _, c := range cases {
		got, err := Normalize(c.in)
		if err != nil || got != c.want {
			t.Errorf("Normalize(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
	if _, err := Normalize("a\x00b"); !errors.Is(err, ErrNotFound) {
		t.Errorf("含 NUL 的路径应返回 ErrNotFound, got %v", err)
	}
}

// TestResolver_Resolve_Basic 验证正常路径解析到 home 内的绝对路径。
func TestResolver_Resolve_Basic(t *testing.T) {
	r, home := newResolverForTest(t)
	abs, n, err := r.Resolve("skills/foo")
	if err != nil {
		t.Fatalf("Resolve: %v", err)
	}
	realHome, _ := filepath.EvalSymlinks(home)
	want := filepath.Join(realHome, "skills", "foo")
	if abs != want || n != "skills/foo" {
		t.Errorf("Resolve = %q, %q; want %q, skills/foo", abs, n, want)
	}
}

// TestResolver_Resolve_SymlinkEscape 验证指向 home 外的符号链接被拒绝。
func TestResolver_Resolve_SymlinkEscape(t *testing.T) {
	r, home := newResolverForTest(t)
	outside := t.TempDir()
	if err := os.Symlink(outside, filepath.Join(home, "evil")); err != nil {
		t.Skipf("无法创建符号链接: %v", err)
	}
	if _, _, err := r.Resolve("evil"); !errors.Is(err, ErrNotFound) {
		t.Errorf("符号链接逃逸应返回 ErrNotFound, got %v", err)
	}
	// 链接下的子路径同样拒绝
	if _, _, err := r.Resolve("evil/sub.txt"); !errors.Is(err, ErrNotFound) {
		t.Errorf("符号链接子路径应返回 ErrNotFound, got %v", err)
	}
}

// TestResolver_Hidden 验证 groot.db* 隐藏规则只作用于根目录。
func TestResolver_Hidden(t *testing.T) {
	r, _ := newResolverForTest(t)
	for _, n := range []string{"groot.db", "groot.db-wal", "groot.db-shm"} {
		if !r.Hidden(n) {
			t.Errorf("Hidden(%q) = false, want true", n)
		}
		if _, _, err := r.Resolve(n); !errors.Is(err, ErrNotFound) {
			t.Errorf("Resolve(%q) 应返回 ErrNotFound, got %v", n, err)
		}
	}
	// 非根目录、非前缀的不隐藏
	for _, n := range []string{"", "skills/groot.db", "groot.dbx-not"} {
		if n == "groot.dbx-not" {
			continue // groot.db 前缀匹配：groot.dbx 也会被隐藏，属可接受的过度匹配
		}
		if n != "" && r.Hidden(n) {
			t.Errorf("Hidden(%q) = true, want false", n)
		}
	}
	if r.Hidden("skills/groot.db") {
		t.Error("子目录中的 groot.db 不应隐藏")
	}
}

// TestResolver_ReadOnly 验证只读规则只覆盖根目录的两个配置文件。
func TestResolver_ReadOnly(t *testing.T) {
	r, _ := newResolverForTest(t)
	if !r.ReadOnly("env.yaml") || !r.ReadOnly("config.yaml") {
		t.Error("env.yaml/config.yaml 应为只读")
	}
	if r.ReadOnly("skills/config.yaml") || r.ReadOnly("GROOT.md") {
		t.Error("非根配置文件不应只读")
	}
}

// TestResolver_Whitelist 验证新建与上传白名单矩阵。
func TestResolver_Whitelist(t *testing.T) {
	r, _ := newResolverForTest(t)
	cases := []struct {
		dir       string
		canCreate bool
		canUpload bool
	}{
		{"skills/my-skill", true, true},
		{"skills/my-skill/scripts", true, true},
		{"skills", false, true},
		{"mcp", false, true},
		{"", false, false},
		{"subagents/weather", false, false},
		{"logs", false, false},
	}
	for _, c := range cases {
		if got := r.CanCreate(c.dir); got != c.canCreate {
			t.Errorf("CanCreate(%q) = %v, want %v", c.dir, got, c.canCreate)
		}
		if got := r.CanUpload(c.dir); got != c.canUpload {
			t.Errorf("CanUpload(%q) = %v, want %v", c.dir, got, c.canUpload)
		}
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/webfiles/... -v`
Expected: FAIL（package 不存在 / 函数未定义）

- [ ] **Step 3: 实现 Resolver**

```go
// internal/webfiles/resolver.go
// Package webfiles 实现 Web 文件面板的文件系统访问层：
// 路径安全解析（防穿越/防符号链接逃逸）、可见性与权限规则、文件操作。
package webfiles

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// 对外统一的错误，handler 依据它们映射 HTTP 状态码。
var (
	ErrNotFound  = errors.New("not found")           // 不存在 / 越界 / 隐藏
	ErrReadOnly  = errors.New("read only")           // 只读文件的写操作
	ErrForbidden = errors.New("forbidden")           // 白名单之外的新建/上传
	ErrExists    = errors.New("already exists")      // 目标已存在
	ErrNotEmpty  = errors.New("directory not empty") // 删除非空目录
	ErrTooLarge  = errors.New("too large")           // 超出大小限制
	ErrInvalid   = errors.New("invalid request")     // 参数非法
)

const (
	// MaxTextSize 在线读取/保存文本的大小上限。
	MaxTextSize = 2 * 1024 * 1024
	// MaxUploadSize 单文件上传大小上限。
	MaxUploadSize = 20 * 1024 * 1024
)

// Resolver 把请求中的相对路径安全解析为 home 内的绝对路径，
// 并承载隐藏、只读、写白名单三类规则。
type Resolver struct {
	home string // 已经过 EvalSymlinks 的绝对路径
}

// NewResolver 构造 Resolver；homeDir 必须存在。
func NewResolver(homeDir string) (*Resolver, error) {
	abs, err := filepath.Abs(homeDir)
	if err != nil {
		return nil, err
	}
	real, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, err
	}
	return &Resolver{home: real}, nil
}

// Home 返回 home 目录的绝对路径（供前端路径栏展示）。
func (r *Resolver) Home() string { return r.home }

// Normalize 清洗请求路径：以 "/" 锚定后 Clean，任何 ".." 都无法越过根。
// 返回统一 "/" 分隔、无前导斜杠的相对路径（"" 表示 home 根）。
func Normalize(rel string) (string, error) {
	if strings.ContainsRune(rel, 0) {
		return "", ErrNotFound
	}
	rel = strings.ReplaceAll(rel, "\\", "/")
	cleaned := filepath.ToSlash(filepath.Clean("/" + rel))
	return strings.TrimPrefix(cleaned, "/"), nil
}

// Resolve 返回 rel 的绝对路径与归一化相对路径。
// 目标无需已存在（供创建类操作使用），但已存在的祖先若经符号链接
// 逃出 home，或路径命中隐藏规则，返回 ErrNotFound。
func (r *Resolver) Resolve(rel string) (abs string, normalized string, err error) {
	n, err := Normalize(rel)
	if err != nil {
		return "", "", err
	}
	if r.Hidden(n) {
		return "", "", ErrNotFound
	}
	abs = filepath.Join(r.home, filepath.FromSlash(n))
	if err := r.verifyReal(abs); err != nil {
		return "", "", err
	}
	return abs, n, nil
}

// verifyReal 自 abs 起向上找到第一个已存在的路径，解析符号链接后
// 校验其真实位置仍在 home 内。
func (r *Resolver) verifyReal(abs string) error {
	p := abs
	for {
		real, err := filepath.EvalSymlinks(p)
		if err == nil {
			if real != r.home && !strings.HasPrefix(real, r.home+string(filepath.Separator)) {
				return ErrNotFound
			}
			return nil
		}
		if !os.IsNotExist(err) {
			return err
		}
		parent := filepath.Dir(p)
		if parent == p {
			return ErrNotFound
		}
		p = parent
	}
}

// Hidden 判定归一化路径 n 是否对外不可见：
// home 根下基名以 groot.db 开头的条目（groot.db 及其 -wal/-shm）。
func (r *Resolver) Hidden(n string) bool {
	return n != "" && !strings.Contains(n, "/") && strings.HasPrefix(n, "groot.db")
}

// ReadOnly 判定 n 是否只读：home 根下的 env.yaml 与 config.yaml。
func (r *Resolver) ReadOnly(n string) bool {
	return n == "env.yaml" || n == "config.yaml"
}

// CanCreate 判定能否在目录 n 内新建文件/子目录：仅 skills/<名称>/ 及更深。
func (r *Resolver) CanCreate(n string) bool {
	parts := strings.Split(n, "/")
	return len(parts) >= 2 && parts[0] == "skills" && parts[1] != ""
}

// CanUpload 判定能否向目录 n 上传：CanCreate 的位置，外加 skills 与 mcp 顶层。
func (r *Resolver) CanUpload(n string) bool {
	return r.CanCreate(n) || n == "skills" || n == "mcp"
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/webfiles/... -v`
Expected: PASS（全部用例）

- [ ] **Step 5: 报告任务完成（不提交，等用户指令）**

---

### Task 2: webfiles 包 —— 读操作（List / Read / DownloadPath）

**Files:**
- Create: `internal/webfiles/service.go`
- Test: `internal/webfiles/service_test.go`

- [ ] **Step 1: 写失败的测试**

```go
// internal/webfiles/service_test.go
package webfiles

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newServiceForTest 构造带典型 home 结构的 Service。
func newServiceForTest(t *testing.T) (*Service, string) {
	t.Helper()
	home := t.TempDir()
	mustMkdir := func(p string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Join(home, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	mustWrite := func(p, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(home, p), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	mustMkdir("skills/my-skill")
	mustMkdir("mcp")
	mustMkdir("logs")
	mustWrite("GROOT.md", "# memo\n")
	mustWrite("config.yaml", "server:\n  port: 8080\n")
	mustWrite("env.yaml", "database:\n")
	mustWrite("groot.db", "SQLite format 3\x00")
	mustWrite("groot.db-wal", "\x00")
	mustWrite(".DS_Store", "junk")
	mustWrite("skills/my-skill/SKILL.md", "---\nname: my-skill\n---\n")
	svc, err := NewService(home)
	if err != nil {
		t.Fatalf("NewService: %v", err)
	}
	return svc, home
}

// entryNames 提取条目名列表。
func entryNames(entries []Entry) []string {
	names := make([]string, len(entries))
	for i, e := range entries {
		names[i] = e.Name
	}
	return names
}

// TestService_List_Root 验证根目录列表：过滤隐藏项、目录在前、只读标记。
func TestService_List_Root(t *testing.T) {
	svc, _ := newServiceForTest(t)
	entries, err := svc.List("")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	names := entryNames(entries)
	for _, banned := range []string{"groot.db", "groot.db-wal", ".DS_Store"} {
		for _, n := range names {
			if n == banned {
				t.Errorf("列表不应包含 %s", banned)
			}
		}
	}
	// 目录在前且按名排序：logs, mcp, skills 之后才是文件
	if len(names) < 6 {
		t.Fatalf("条目数不足: %v", names)
	}
	if names[0] != "logs" || names[1] != "mcp" || names[2] != "skills" {
		t.Errorf("目录排序不对: %v", names)
	}
	for _, e := range entries {
		wantRO := e.Name == "config.yaml" || e.Name == "env.yaml"
		if e.Readonly != wantRO {
			t.Errorf("%s readonly = %v, want %v", e.Name, e.Readonly, wantRO)
		}
	}
}

// TestService_List_NotFound 验证不存在的目录返回 ErrNotFound。
func TestService_List_NotFound(t *testing.T) {
	svc, _ := newServiceForTest(t)
	if _, err := svc.List("no-such-dir"); !errors.Is(err, ErrNotFound) {
		t.Errorf("want ErrNotFound, got %v", err)
	}
}

// TestService_Read 验证文本读取、只读标记、二进制与超限。
func TestService_Read(t *testing.T) {
	svc, home := newServiceForTest(t)

	fc, err := svc.Read("GROOT.md")
	if err != nil || fc.Content != "# memo\n" || fc.Readonly || fc.Binary {
		t.Errorf("Read(GROOT.md) = %+v, %v", fc, err)
	}

	fc, err = svc.Read("config.yaml")
	if err != nil || !fc.Readonly {
		t.Errorf("config.yaml 应可读且 readonly=true, got %+v, %v", fc, err)
	}

	if _, err = svc.Read("groot.db"); !errors.Is(err, ErrNotFound) {
		t.Errorf("读隐藏文件应 ErrNotFound, got %v", err)
	}

	// 二进制探测：含 NUL 字节
	if err := os.WriteFile(filepath.Join(home, "bin.dat"), []byte("ab\x00cd"), 0o644); err != nil {
		t.Fatal(err)
	}
	fc, err = svc.Read("bin.dat")
	if err != nil || !fc.Binary || fc.Content != "" {
		t.Errorf("二进制文件应 Binary=true 且不返回内容, got %+v, %v", fc, err)
	}

	// 超限
	big := strings.Repeat("x", MaxTextSize+1)
	if err := os.WriteFile(filepath.Join(home, "big.txt"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err = svc.Read("big.txt"); !errors.Is(err, ErrTooLarge) {
		t.Errorf("超限读取应 ErrTooLarge, got %v", err)
	}

	// 读目录
	if _, err = svc.Read("skills"); !errors.Is(err, ErrInvalid) {
		t.Errorf("读目录应 ErrInvalid, got %v", err)
	}
}

// TestService_DownloadPath 验证下载路径解析。
func TestService_DownloadPath(t *testing.T) {
	svc, _ := newServiceForTest(t)
	abs, name, err := svc.DownloadPath("skills/my-skill/SKILL.md")
	if err != nil || name != "SKILL.md" || abs == "" {
		t.Errorf("DownloadPath = %q, %q, %v", abs, name, err)
	}
	if _, _, err = svc.DownloadPath("groot.db"); !errors.Is(err, ErrNotFound) {
		t.Errorf("下载隐藏文件应 ErrNotFound, got %v", err)
	}
	if _, _, err = svc.DownloadPath("skills"); !errors.Is(err, ErrNotFound) {
		t.Errorf("下载目录应 ErrNotFound, got %v", err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/webfiles/... -v -run TestService`
Expected: FAIL（Service 未定义）

- [ ] **Step 3: 实现 Service 读操作**

```go
// internal/webfiles/service.go
package webfiles

import (
	"bytes"
	"os"
	"path"
	"path/filepath"
	"sort"
)

// Entry 目录列表条目。
type Entry struct {
	Name     string `json:"name"`
	Type     string `json:"type"` // "dir" | "file"
	Size     int64  `json:"size"`
	Mtime    int64  `json:"mtime"` // 毫秒时间戳
	Readonly bool   `json:"readonly"`
}

// FileContent 文本读取结果。二进制文件 Binary=true 且 Content 为空。
type FileContent struct {
	Path     string `json:"path"`
	Readonly bool   `json:"readonly"`
	Binary   bool   `json:"binary"`
	Content  string `json:"content"`
}

// Service 文件面板的文件操作层，所有路径都经 Resolver 安全解析。
type Service struct {
	res *Resolver
}

// NewService 构造 Service；homeDir 必须存在。
func NewService(homeDir string) (*Service, error) {
	res, err := NewResolver(homeDir)
	if err != nil {
		return nil, err
	}
	return &Service{res: res}, nil
}

// Home 返回 home 目录绝对路径（供前端路径栏展示）。
func (s *Service) Home() string { return s.res.Home() }

// joinRel 拼接归一化相对路径。
func joinRel(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// List 返回目录单层内容：过滤隐藏项与 .DS_Store，目录在前、按名排序。
func (s *Service) List(rel string) ([]Entry, error) {
	abs, n, err := s.res.Resolve(rel)
	if err != nil {
		return nil, err
	}
	des, err := os.ReadDir(abs)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	entries := make([]Entry, 0, len(des))
	for _, de := range des {
		child := joinRel(n, de.Name())
		if s.res.Hidden(child) || de.Name() == ".DS_Store" {
			continue
		}
		info, err := de.Info()
		if err != nil {
			continue // 读取途中被删的条目直接跳过
		}
		typ := "file"
		if de.IsDir() {
			typ = "dir"
		}
		entries = append(entries, Entry{
			Name:     de.Name(),
			Type:     typ,
			Size:     info.Size(),
			Mtime:    info.ModTime().UnixMilli(),
			Readonly: s.res.ReadOnly(child),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Type != entries[j].Type {
			return entries[i].Type == "dir"
		}
		return entries[i].Name < entries[j].Name
	})
	return entries, nil
}

// Read 读取文本文件内容；目录返回 ErrInvalid，超限 ErrTooLarge，
// 二进制文件返回 Binary=true 且不含内容。
func (s *Service) Read(rel string) (*FileContent, error) {
	abs, n, err := s.res.Resolve(rel)
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(abs)
	if err != nil {
		return nil, ErrNotFound
	}
	if info.IsDir() {
		return nil, ErrInvalid
	}
	if info.Size() > MaxTextSize {
		return nil, ErrTooLarge
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		return nil, err
	}
	fc := &FileContent{Path: n, Readonly: s.res.ReadOnly(n)}
	if isBinary(data) {
		fc.Binary = true
		return fc, nil
	}
	fc.Content = string(data)
	return fc, nil
}

// isBinary 以前 8KB 是否含 NUL 字节判定二进制。
func isBinary(data []byte) bool {
	probe := data
	if len(probe) > 8192 {
		probe = probe[:8192]
	}
	return bytes.IndexByte(probe, 0) >= 0
}

// DownloadPath 解析下载目标，返回绝对路径与文件名；目录不可下载。
func (s *Service) DownloadPath(rel string) (abs string, name string, err error) {
	abs, n, err := s.res.Resolve(rel)
	if err != nil {
		return "", "", err
	}
	info, err := os.Stat(abs)
	if err != nil || info.IsDir() {
		return "", "", ErrNotFound
	}
	return abs, path.Base(n), nil
}

// 确保 filepath 引用不因平台差异被裁剪（Resolve 内部使用）。
var _ = filepath.Join
```

注意：最后一行 `var _ = filepath.Join` 只是防未使用导入的占位——实现完 Task 3 后 `filepath` 会被真正使用，届时删除该行。

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/webfiles/... -v`
Expected: PASS

- [ ] **Step 5: 报告任务完成（不提交）**

---

### Task 3: webfiles 包 —— 写操作（Write / Mkdir / Create / Rename / Delete / UploadTarget）

**Files:**
- Modify: `internal/webfiles/service.go`（追加方法；删除 Task 2 的 `var _ = filepath.Join` 占位行）
- Test: `internal/webfiles/service_test.go`（追加用例）

- [ ] **Step 1: 追加失败的测试**

```go
// 追加到 internal/webfiles/service_test.go

// TestService_Write 验证保存：正常写入、只读拒绝、不存在拒绝、超限拒绝。
func TestService_Write(t *testing.T) {
	svc, home := newServiceForTest(t)

	if err := svc.Write("GROOT.md", "updated\n"); err != nil {
		t.Fatalf("Write: %v", err)
	}
	data, _ := os.ReadFile(filepath.Join(home, "GROOT.md"))
	if string(data) != "updated\n" {
		t.Errorf("内容未写入: %q", data)
	}

	if err := svc.Write("config.yaml", "hack"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("写只读文件应 ErrReadOnly, got %v", err)
	}
	if err := svc.Write("env.yaml", "hack"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("写只读文件应 ErrReadOnly, got %v", err)
	}
	if err := svc.Write("not-exist.md", "x"); !errors.Is(err, ErrNotFound) {
		t.Errorf("写不存在文件应 ErrNotFound, got %v", err)
	}
	if err := svc.Write("GROOT.md", strings.Repeat("x", MaxTextSize+1)); !errors.Is(err, ErrTooLarge) {
		t.Errorf("超限保存应 ErrTooLarge, got %v", err)
	}
	if err := svc.Write("skills", "x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("写目录应 ErrInvalid, got %v", err)
	}
}

// TestService_CreateAndMkdir 验证白名单内可新建、白名单外拒绝、重名拒绝。
func TestService_CreateAndMkdir(t *testing.T) {
	svc, home := newServiceForTest(t)

	if err := svc.Create("skills/my-skill/notes.md"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "skills/my-skill/notes.md")); err != nil {
		t.Errorf("文件未创建: %v", err)
	}
	if err := svc.Mkdir("skills/my-skill/scripts"); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	// 白名单外
	if err := svc.Create("logs/hack.txt"); !errors.Is(err, ErrForbidden) {
		t.Errorf("白名单外新建应 ErrForbidden, got %v", err)
	}
	if err := svc.Mkdir("subagents/x"); !errors.Is(err, ErrForbidden) {
		t.Errorf("白名单外建目录应 ErrForbidden, got %v", err)
	}
	if err := svc.Create("skills/direct.md"); !errors.Is(err, ErrForbidden) {
		t.Errorf("skills 顶层新建文件应 ErrForbidden, got %v", err)
	}
	// 重名
	if err := svc.Create("skills/my-skill/SKILL.md"); !errors.Is(err, ErrExists) {
		t.Errorf("重名新建应 ErrExists, got %v", err)
	}
	// 非法名称
	if err := svc.Create("skills/my-skill/.hidden"); !errors.Is(err, ErrInvalid) {
		t.Errorf("点开头文件名应 ErrInvalid, got %v", err)
	}
	// 父目录不存在
	if err := svc.Create("skills/nope/a.md"); !errors.Is(err, ErrNotFound) {
		t.Errorf("父目录不存在应 ErrNotFound, got %v", err)
	}
}

// TestService_Rename 验证同目录改名、只读保护、重名保护。
func TestService_Rename(t *testing.T) {
	svc, home := newServiceForTest(t)

	if err := svc.Rename("GROOT.md", "GROOT2.md"); err != nil {
		t.Fatalf("Rename: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, "GROOT2.md")); err != nil {
		t.Errorf("改名未生效: %v", err)
	}
	if err := svc.Rename("config.yaml", "c2.yaml"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("改名只读文件应 ErrReadOnly, got %v", err)
	}
	if err := svc.Rename("GROOT2.md", "env.yaml"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("改名为只读文件名应 ErrReadOnly, got %v", err)
	}
	if err := svc.Rename("GROOT2.md", "skills/GROOT2.md"); !errors.Is(err, ErrInvalid) {
		t.Errorf("跨目录改名应 ErrInvalid, got %v", err)
	}
	if err := svc.Rename("no.md", "yes.md"); !errors.Is(err, ErrNotFound) {
		t.Errorf("改名不存在文件应 ErrNotFound, got %v", err)
	}
}

// TestService_Delete 验证删除文件、空目录、非空目录拒绝、只读拒绝。
func TestService_Delete(t *testing.T) {
	svc, home := newServiceForTest(t)

	if err := svc.Delete("GROOT.md"); err != nil {
		t.Fatalf("Delete file: %v", err)
	}
	if err := svc.Delete("mcp"); err != nil { // 空目录
		t.Fatalf("Delete empty dir: %v", err)
	}
	if err := svc.Delete("skills"); !errors.Is(err, ErrNotEmpty) {
		t.Errorf("删非空目录应 ErrNotEmpty, got %v", err)
	}
	if err := svc.Delete("config.yaml"); !errors.Is(err, ErrReadOnly) {
		t.Errorf("删只读文件应 ErrReadOnly, got %v", err)
	}
	if err := svc.Delete(""); !errors.Is(err, ErrInvalid) {
		t.Errorf("删 home 根应 ErrInvalid, got %v", err)
	}
	if err := svc.Delete("groot.db"); !errors.Is(err, ErrNotFound) {
		t.Errorf("删隐藏文件应 ErrNotFound, got %v", err)
	}
	_ = home
}

// TestService_UploadTarget 验证上传白名单与限制。
func TestService_UploadTarget(t *testing.T) {
	svc, _ := newServiceForTest(t)

	if _, err := svc.UploadTarget("mcp", "server.json", 100); err != nil {
		t.Errorf("mcp 上传应允许, got %v", err)
	}
	if _, err := svc.UploadTarget("skills", "pack.md", 100); err != nil {
		t.Errorf("skills 顶层上传应允许, got %v", err)
	}
	if _, err := svc.UploadTarget("skills/my-skill", "run.sh", 100); err != nil {
		t.Errorf("skill 目录上传应允许, got %v", err)
	}
	if _, err := svc.UploadTarget("logs", "x.txt", 100); !errors.Is(err, ErrForbidden) {
		t.Errorf("logs 上传应 ErrForbidden, got %v", err)
	}
	if _, err := svc.UploadTarget("mcp", "big.bin", MaxUploadSize+1); !errors.Is(err, ErrTooLarge) {
		t.Errorf("超限上传应 ErrTooLarge, got %v", err)
	}
	if _, err := svc.UploadTarget("mcp", "../evil.sh", 100); !errors.Is(err, ErrInvalid) {
		t.Errorf("带路径的文件名应 ErrInvalid, got %v", err)
	}
	if _, err := svc.UploadTarget("skills/my-skill", "SKILL.md", 100); !errors.Is(err, ErrExists) {
		t.Errorf("上传重名应 ErrExists, got %v", err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/webfiles/... -v -run 'TestService_(Write|CreateAndMkdir|Rename|Delete|UploadTarget)'`
Expected: FAIL（方法未定义）

- [ ] **Step 3: 实现写操作**

追加到 `internal/webfiles/service.go`（同时删除 Task 2 的 `var _ = filepath.Join` 行，并在 import 中加入 `regexp`、`strings`）：

```go
// nameRe 合法名称：字母/数字开头，允许字母数字、空格、括号、点、下划线、连字符，≤64 字符。
var nameRe = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N} ()._-]{0,63}$`)

// validName 校验单段名称：不允许路径分隔、点开头、"."、".."。
func validName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.HasPrefix(name, ".") {
		return false
	}
	return nameRe.MatchString(name)
}

// Write 保存已存在的文本文件；只读文件拒绝，超限拒绝。
func (s *Service) Write(rel, content string) error {
	if len(content) > MaxTextSize {
		return ErrTooLarge
	}
	abs, n, err := s.res.Resolve(rel)
	if err != nil {
		return err
	}
	if s.res.ReadOnly(n) {
		return ErrReadOnly
	}
	info, err := os.Stat(abs)
	if err != nil {
		return ErrNotFound // 只允许保存已存在的文件（新建走 Create/Scaffold）
	}
	if info.IsDir() {
		return ErrInvalid
	}
	return os.WriteFile(abs, []byte(content), 0o644)
}

// Mkdir 在白名单目录内新建子目录。
func (s *Service) Mkdir(rel string) error { return s.createEntry(rel, true) }

// Create 在白名单目录内新建空文件。
func (s *Service) Create(rel string) error { return s.createEntry(rel, false) }

func (s *Service) createEntry(rel string, isDir bool) error {
	abs, n, err := s.res.Resolve(rel)
	if err != nil {
		return err
	}
	parent := path.Dir(n)
	if parent == "." {
		parent = ""
	}
	if !s.res.CanCreate(parent) {
		return ErrForbidden
	}
	if !validName(path.Base(n)) {
		return ErrInvalid
	}
	if info, err := os.Stat(filepath.Dir(abs)); err != nil || !info.IsDir() {
		return ErrNotFound // 父目录必须已存在
	}
	if _, err := os.Lstat(abs); err == nil {
		return ErrExists
	}
	if isDir {
		return os.Mkdir(abs, 0o755)
	}
	return os.WriteFile(abs, nil, 0o644)
}

// Rename 同目录改名；只读文件（或改名为只读文件名）拒绝。
func (s *Service) Rename(fromRel, toRel string) error {
	fromAbs, fromN, err := s.res.Resolve(fromRel)
	if err != nil {
		return err
	}
	if s.res.ReadOnly(fromN) {
		return ErrReadOnly
	}
	if _, err := os.Lstat(fromAbs); err != nil {
		return ErrNotFound
	}
	toAbs, toN, err := s.res.Resolve(toRel)
	if err != nil {
		return err
	}
	if s.res.ReadOnly(toN) {
		return ErrReadOnly
	}
	if path.Dir(fromN) != path.Dir(toN) {
		return ErrInvalid // 仅支持同目录改名
	}
	if !validName(path.Base(toN)) {
		return ErrInvalid
	}
	if _, err := os.Lstat(toAbs); err == nil {
		return ErrExists
	}
	return os.Rename(fromAbs, toAbs)
}

// Delete 删除文件或空目录；home 根、只读、隐藏、非空目录均拒绝。
func (s *Service) Delete(rel string) error {
	abs, n, err := s.res.Resolve(rel)
	if err != nil {
		return err
	}
	if n == "" {
		return ErrInvalid
	}
	if s.res.ReadOnly(n) {
		return ErrReadOnly
	}
	info, err := os.Lstat(abs)
	if err != nil {
		return ErrNotFound
	}
	if info.IsDir() {
		des, err := os.ReadDir(abs)
		if err != nil {
			return err
		}
		if len(des) > 0 {
			return ErrNotEmpty
		}
	}
	return os.Remove(abs)
}

// UploadTarget 校验上传请求（白名单、大小、文件名、重名），
// 返回可直接写入的目标绝对路径；实际落盘由 handler 完成。
func (s *Service) UploadTarget(dirRel, filename string, size int64) (string, error) {
	if size > MaxUploadSize {
		return "", ErrTooLarge
	}
	if !validName(filename) {
		return "", ErrInvalid // 含路径分隔符或非法字符的文件名直接拒绝
	}
	_, dirN, err := s.res.Resolve(dirRel)
	if err != nil {
		return "", err
	}
	if !s.res.CanUpload(dirN) {
		return "", ErrForbidden
	}
	abs, n, err := s.res.Resolve(joinRel(dirN, filename))
	if err != nil {
		return "", err
	}
	if s.res.ReadOnly(n) {
		return "", ErrReadOnly
	}
	if info, err := os.Stat(filepath.Dir(abs)); err != nil || !info.IsDir() {
		return "", ErrNotFound
	}
	if _, err := os.Lstat(abs); err == nil {
		return "", ErrExists
	}
	return abs, nil
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/webfiles/... -v`
Expected: PASS（含 Task 1、2 的全部用例）

- [ ] **Step 5: 报告任务完成（不提交）**

---

### Task 4: webfiles 包 —— Scaffold（创建 skill / mcp / agent）

**Files:**
- Create: `internal/webfiles/scaffold.go`
- Test: `internal/webfiles/scaffold_test.go`

- [ ] **Step 1: 写失败的测试**

```go
// internal/webfiles/scaffold_test.go
package webfiles

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScaffold_Skill 验证生成 skills/<名>/SKILL.md 且 frontmatter 含名称。
func TestScaffold_Skill(t *testing.T) {
	svc, home := newServiceForTest(t)
	rel, err := svc.Scaffold("skill", "code-review")
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	if rel != "skills/code-review/SKILL.md" {
		t.Errorf("rel = %q", rel)
	}
	data, err := os.ReadFile(filepath.Join(home, "skills/code-review/SKILL.md"))
	if err != nil {
		t.Fatalf("模板文件未生成: %v", err)
	}
	if !strings.Contains(string(data), "name: code-review") {
		t.Errorf("frontmatter 缺名称: %s", data)
	}
}

// TestScaffold_Mcp 验证生成 mcp/<名>.json 且为合法 JSON 骨架。
func TestScaffold_Mcp(t *testing.T) {
	svc, home := newServiceForTest(t)
	rel, err := svc.Scaffold("mcp", "my-proxy")
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	if rel != "mcp/my-proxy.json" {
		t.Errorf("rel = %q", rel)
	}
	data, _ := os.ReadFile(filepath.Join(home, "mcp/my-proxy.json"))
	for _, want := range []string{`"name": "my-proxy"`, `"type": "stdio"`, `"isActive": false`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("模板缺少 %s: %s", want, data)
		}
	}
}

// TestScaffold_Agent 验证生成 subagents/<名>/{agent.md, mcp/, skills/}。
func TestScaffold_Agent(t *testing.T) {
	svc, home := newServiceForTest(t)
	rel, err := svc.Scaffold("agent", "helper")
	if err != nil {
		t.Fatalf("Scaffold: %v", err)
	}
	if rel != "subagents/helper/agent.md" {
		t.Errorf("rel = %q", rel)
	}
	for _, p := range []string{"subagents/helper/agent.md", "subagents/helper/mcp", "subagents/helper/skills"} {
		if _, err := os.Stat(filepath.Join(home, p)); err != nil {
			t.Errorf("%s 未生成: %v", p, err)
		}
	}
}

// TestScaffold_Invalid 验证重名、非法名称、未知类型。
func TestScaffold_Invalid(t *testing.T) {
	svc, _ := newServiceForTest(t)
	if _, err := svc.Scaffold("skill", "my-skill"); !errors.Is(err, ErrExists) {
		t.Errorf("重名 skill 应 ErrExists, got %v", err)
	}
	for _, bad := range []string{"", "a/b", "..", "a b", ".hidden"} {
		if _, err := svc.Scaffold("skill", bad); !errors.Is(err, ErrInvalid) {
			t.Errorf("Scaffold(skill, %q) 应 ErrInvalid, got %v", bad, err)
		}
	}
	if _, err := svc.Scaffold("plugin", "x"); !errors.Is(err, ErrInvalid) {
		t.Errorf("未知类型应 ErrInvalid, got %v", err)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/webfiles/... -v -run TestScaffold`
Expected: FAIL（Scaffold 未定义）

- [ ] **Step 3: 实现 Scaffold**

```go
// internal/webfiles/scaffold.go
package webfiles

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
)

// scaffoldNameRe 创建名称：字母/数字开头，仅字母数字、下划线、连字符，≤64 字符。
// 比 validName 更严——名称会成为 skill/agent 的标识符。
var scaffoldNameRe = regexp.MustCompile(`^[\p{L}\p{N}][\p{L}\p{N}_-]{0,63}$`)

const skillTemplate = `---
name: %s
description: ""
---

# %s
`

const mcpTemplate = `{
  "name": %q,
  "type": "stdio",
  "description": "",
  "isActive": false,
  "command": "",
  "args": []
}
`

const agentTemplate = `---
description: ""
---
`

// Scaffold 按模板创建 skill / mcp / agent，返回主文件的相对路径。
// 目标已存在返回 ErrExists；名称或类型非法返回 ErrInvalid。
func (s *Service) Scaffold(kind, name string) (string, error) {
	if !scaffoldNameRe.MatchString(name) {
		return "", ErrInvalid
	}
	switch kind {
	case "skill":
		dir := "skills/" + name
		abs, _, err := s.res.Resolve(dir)
		if err != nil {
			return "", err
		}
		if _, err := os.Lstat(abs); err == nil {
			return "", ErrExists
		}
		if err := os.MkdirAll(abs, 0o755); err != nil {
			return "", err
		}
		content := fmt.Sprintf(skillTemplate, name, name)
		if err := os.WriteFile(filepath.Join(abs, "SKILL.md"), []byte(content), 0o644); err != nil {
			return "", err
		}
		return dir + "/SKILL.md", nil
	case "mcp":
		rel := "mcp/" + name + ".json"
		abs, _, err := s.res.Resolve(rel)
		if err != nil {
			return "", err
		}
		if _, err := os.Lstat(abs); err == nil {
			return "", ErrExists
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return "", err
		}
		if err := os.WriteFile(abs, []byte(fmt.Sprintf(mcpTemplate, name)), 0o644); err != nil {
			return "", err
		}
		return rel, nil
	case "agent":
		dir := "subagents/" + name
		abs, _, err := s.res.Resolve(dir)
		if err != nil {
			return "", err
		}
		if _, err := os.Lstat(abs); err == nil {
			return "", ErrExists
		}
		for _, sub := range []string{"", "mcp", "skills"} {
			if err := os.MkdirAll(filepath.Join(abs, sub), 0o755); err != nil {
				return "", err
			}
		}
		if err := os.WriteFile(filepath.Join(abs, "agent.md"), []byte(agentTemplate), 0o644); err != nil {
			return "", err
		}
		return dir + "/agent.md", nil
	default:
		return "", ErrInvalid
	}
}
```

- [ ] **Step 4: 运行测试确认通过**

Run: `go test ./internal/webfiles/... -v`
Expected: PASS

- [ ] **Step 5: 报告任务完成（不提交）**

---

### Task 5: HTTP handler 与路由装配

**Files:**
- Create: `internal/api/handler/files.go`
- Test: `internal/api/handler/files_test.go`
- Modify: `internal/api/server.go:97-102`（创建 handler 并传入 RegisterRoutes）
- Modify: `internal/api/router.go:29-30, 43-62`（签名加参数 + 注册 /web/files 路由）

- [ ] **Step 1: 写失败的测试**

```go
// internal/api/handler/files_test.go
package handler

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"
)

// newFilesHandlerForTest 在临时 home 上构造 FilesHandler。
func newFilesHandlerForTest(t *testing.T) (*FilesHandler, string) {
	t.Helper()
	home := t.TempDir()
	for _, p := range []string{"skills/demo", "mcp"} {
		if err := os.MkdirAll(filepath.Join(home, p), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(home, "GROOT.md"), []byte("memo"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "config.yaml"), []byte("port: 1"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "groot.db"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	h, err := NewFilesHandler(home)
	if err != nil {
		t.Fatalf("NewFilesHandler: %v", err)
	}
	return h, home
}

// getCtx 构造带 query 参数的 GET 上下文。
func getCtx(query string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodGet)
	rc.Request.SetRequestURI("/web/files/x?" + query)
	return rc
}

// jsonCtx 构造带 JSON body 的上下文。
func jsonCtx(method, body string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(method)
	rc.Request.Header.SetContentTypeBytes([]byte("application/json"))
	rc.Request.SetBody([]byte(body))
	return rc
}

// TestFilesHandler_List 验证列表成功响应与 home 字段。
func TestFilesHandler_List(t *testing.T) {
	h, _ := newFilesHandlerForTest(t)
	rc := getCtx("path=")
	h.List(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status = %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var resp struct {
		Status  string `json:"status"`
		Home    string `json:"home"`
		Entries []struct {
			Name string `json:"name"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &resp); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if resp.Status != "success" || resp.Home == "" {
		t.Errorf("resp = %+v", resp)
	}
	for _, e := range resp.Entries {
		if e.Name == "groot.db" {
			t.Error("列表泄露了 groot.db")
		}
	}
}

// TestFilesHandler_ErrorMapping 验证错误码映射：404/403/400。
func TestFilesHandler_ErrorMapping(t *testing.T) {
	h, _ := newFilesHandlerForTest(t)

	rc := getCtx("path=groot.db")
	h.Content(context.Background(), rc)
	if rc.Response.StatusCode() != 404 {
		t.Errorf("隐藏文件 status = %d, want 404", rc.Response.StatusCode())
	}

	rc = jsonCtx(consts.MethodPut, `{"path":"config.yaml","content":"x"}`)
	h.Save(context.Background(), rc)
	if rc.Response.StatusCode() != 403 {
		t.Errorf("只读保存 status = %d, want 403", rc.Response.StatusCode())
	}

	rc = jsonCtx(consts.MethodPost, `{"path":"logs/new.txt"}`)
	h.Create(context.Background(), rc)
	if rc.Response.StatusCode() != 403 {
		t.Errorf("白名单外新建 status = %d, want 403", rc.Response.StatusCode())
	}

	rc = jsonCtx(consts.MethodPost, `not-json`)
	h.Create(context.Background(), rc)
	if rc.Response.StatusCode() != 400 {
		t.Errorf("非法 JSON status = %d, want 400", rc.Response.StatusCode())
	}
}

// TestFilesHandler_SaveAndScaffold 验证保存与 scaffold 成功路径。
func TestFilesHandler_SaveAndScaffold(t *testing.T) {
	h, home := newFilesHandlerForTest(t)

	rc := jsonCtx(consts.MethodPut, `{"path":"GROOT.md","content":"new"}`)
	h.Save(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("save status = %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	data, _ := os.ReadFile(filepath.Join(home, "GROOT.md"))
	if string(data) != "new" {
		t.Errorf("保存未生效: %q", data)
	}

	rc = jsonCtx(consts.MethodPost, `{"kind":"skill","name":"fresh"}`)
	h.Scaffold(context.Background(), rc)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("scaffold status = %d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var resp struct {
		Path string `json:"path"`
	}
	_ = json.Unmarshal(rc.Response.Body(), &resp)
	if resp.Path != "skills/fresh/SKILL.md" {
		t.Errorf("scaffold path = %q", resp.Path)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

Run: `go test ./internal/api/handler/... -v -run TestFilesHandler`
Expected: FAIL（FilesHandler 未定义）

- [ ] **Step 3: 实现 handler**

```go
// internal/api/handler/files.go
package handler

import (
	"context"
	"errors"
	"net/url"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

	"github.com/zfd81/groot/internal/webfiles"
)

// FilesHandler 文件面板的 HTTP 绑定层；安全与业务规则在 internal/webfiles。
type FilesHandler struct {
	svc *webfiles.Service
}

// NewFilesHandler 构造 FilesHandler；homeDir 无法解析时返回错误（调用方决定降级）。
func NewFilesHandler(homeDir string) (*FilesHandler, error) {
	svc, err := webfiles.NewService(homeDir)
	if err != nil {
		return nil, err
	}
	return &FilesHandler{svc: svc}, nil
}

// writeFilesError 把 webfiles 错误映射为 HTTP 响应。
func writeFilesError(rc *app.RequestContext, err error) {
	switch {
	case errors.Is(err, webfiles.ErrNotFound):
		rc.JSON(404, utils.H{"status": "not_found", "message": "文件或目录不存在"})
	case errors.Is(err, webfiles.ErrReadOnly):
		rc.JSON(403, utils.H{"status": "read_only", "message": "该文件为只读"})
	case errors.Is(err, webfiles.ErrForbidden):
		rc.JSON(403, utils.H{"status": "forbidden", "message": "该位置不允许此操作"})
	case errors.Is(err, webfiles.ErrExists):
		rc.JSON(409, utils.H{"status": "exists", "message": "同名文件或目录已存在"})
	case errors.Is(err, webfiles.ErrNotEmpty):
		rc.JSON(409, utils.H{"status": "not_empty", "message": "目录非空，无法删除"})
	case errors.Is(err, webfiles.ErrTooLarge):
		rc.JSON(413, utils.H{"status": "too_large", "message": "内容超出大小限制"})
	case errors.Is(err, webfiles.ErrInvalid):
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "参数非法"})
	default:
		rc.JSON(500, utils.H{"status": "error", "message": err.Error()})
	}
}

// List 处理 GET /web/files/list?path=
func (h *FilesHandler) List(ctx context.Context, rc *app.RequestContext) {
	rel := rc.Query("path")
	entries, err := h.svc.List(rel)
	if err != nil {
		writeFilesError(rc, err)
		return
	}
	rc.JSON(200, utils.H{"status": "success", "path": rel, "home": h.svc.Home(), "entries": entries})
}

// Content 处理 GET /web/files/content?path=
func (h *FilesHandler) Content(ctx context.Context, rc *app.RequestContext) {
	fc, err := h.svc.Read(rc.Query("path"))
	if err != nil {
		writeFilesError(rc, err)
		return
	}
	rc.JSON(200, utils.H{
		"status": "success", "path": fc.Path,
		"readonly": fc.Readonly, "binary": fc.Binary, "content": fc.Content,
	})
}

// Save 处理 PUT /web/files/content
func (h *FilesHandler) Save(ctx context.Context, rc *app.RequestContext) {
	var req struct {
		Path    string `json:"path"`
		Content string `json:"content"`
	}
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求体解析失败"})
		return
	}
	if err := h.svc.Write(req.Path, req.Content); err != nil {
		writeFilesError(rc, err)
		return
	}
	rc.JSON(200, utils.H{"status": "success"})
}

// pathReq 只含 path 字段的请求体。
type pathReq struct {
	Path string `json:"path"`
}

// Mkdir 处理 POST /web/files/mkdir
func (h *FilesHandler) Mkdir(ctx context.Context, rc *app.RequestContext) {
	var req pathReq
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求体解析失败"})
		return
	}
	if err := h.svc.Mkdir(req.Path); err != nil {
		writeFilesError(rc, err)
		return
	}
	rc.JSON(200, utils.H{"status": "success"})
}

// Create 处理 POST /web/files/create
func (h *FilesHandler) Create(ctx context.Context, rc *app.RequestContext) {
	var req pathReq
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求体解析失败"})
		return
	}
	if err := h.svc.Create(req.Path); err != nil {
		writeFilesError(rc, err)
		return
	}
	rc.JSON(200, utils.H{"status": "success"})
}

// Rename 处理 POST /web/files/rename
func (h *FilesHandler) Rename(ctx context.Context, rc *app.RequestContext) {
	var req struct {
		From string `json:"from"`
		To   string `json:"to"`
	}
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求体解析失败"})
		return
	}
	if err := h.svc.Rename(req.From, req.To); err != nil {
		writeFilesError(rc, err)
		return
	}
	rc.JSON(200, utils.H{"status": "success"})
}

// Delete 处理 DELETE /web/files?path=
func (h *FilesHandler) Delete(ctx context.Context, rc *app.RequestContext) {
	if err := h.svc.Delete(rc.Query("path")); err != nil {
		writeFilesError(rc, err)
		return
	}
	rc.JSON(200, utils.H{"status": "success"})
}

// Upload 处理 POST /web/files/upload（multipart：path=目标目录, file=文件）
func (h *FilesHandler) Upload(ctx context.Context, rc *app.RequestContext) {
	fh, err := rc.FormFile("file")
	if err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "缺少 file 字段"})
		return
	}
	dst, err := h.svc.UploadTarget(rc.PostForm("path"), fh.Filename, fh.Size)
	if err != nil {
		writeFilesError(rc, err)
		return
	}
	if err := rc.SaveUploadedFile(fh, dst); err != nil {
		rc.JSON(500, utils.H{"status": "error", "message": err.Error()})
		return
	}
	rc.JSON(200, utils.H{"status": "success"})
}

// Download 处理 GET /web/files/download?path=
func (h *FilesHandler) Download(ctx context.Context, rc *app.RequestContext) {
	abs, name, err := h.svc.DownloadPath(rc.Query("path"))
	if err != nil {
		writeFilesError(rc, err)
		return
	}
	rc.Response.Header.Set("Content-Disposition",
		`attachment; filename*=UTF-8''`+url.PathEscape(name))
	rc.File(abs)
}

// Scaffold 处理 POST /web/files/scaffold
func (h *FilesHandler) Scaffold(ctx context.Context, rc *app.RequestContext) {
	var req struct {
		Kind string `json:"kind"`
		Name string `json:"name"`
	}
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求体解析失败"})
		return
	}
	rel, err := h.svc.Scaffold(req.Kind, req.Name)
	if err != nil {
		writeFilesError(rc, err)
		return
	}
	rc.JSON(200, utils.H{"status": "success", "path": rel})
}
```

- [ ] **Step 4: 运行 handler 测试确认通过**

Run: `go test ./internal/api/handler/... -v -run TestFilesHandler`
Expected: PASS

- [ ] **Step 5: 装配路由**

`internal/api/server.go` 在 `logsH := handler.NewLogsHandler(cfg.Logging)`（97 行）之后加：

```go
	// 文件面板：home 目录解析失败时禁用该功能（不影响其他路由）
	filesH, err := handler.NewFilesHandler(homeDir)
	if err != nil {
		log.Info("文件面板初始化失败，已禁用", zap.Error(err))
		filesH = nil
	}
```

并把 `RegisterRoutes` 调用（100-102 行）改为：

```go
	RegisterRoutes(h, authMW, rateLimitMW, webStore,
		chatH, statusH, detailH, sessionH,
		healthH, skillsH, agentsH, toolsH, modelsH, scheduleH, webAuthH, apiKeysH, clusterH, logsH, filesH)
```

`internal/api/router.go` 的 `RegisterRoutes` 签名末尾加参数 `filesH *handler.FilesHandler`，并在 `webGroup.GET("/logs/:sid", logsH.Serve)` 之后加：

```go
	// 文件面板端点（filesH 为 nil 表示初始化失败，跳过注册）
	if filesH != nil {
		filesGroup := webGroup.Group("/files")
		filesGroup.GET("/list", filesH.List)
		filesGroup.GET("/content", filesH.Content)
		filesGroup.PUT("/content", filesH.Save)
		filesGroup.POST("/mkdir", filesH.Mkdir)
		filesGroup.POST("/create", filesH.Create)
		filesGroup.POST("/rename", filesH.Rename)
		filesGroup.POST("/upload", filesH.Upload)
		filesGroup.GET("/download", filesH.Download)
		filesGroup.POST("/scaffold", filesH.Scaffold)
		webGroup.DELETE("/files", filesH.Delete)
	}
```

- [ ] **Step 6: 编译与全量后端测试**

Run: `go build -o dist/groot ./cmd && go test ./internal/... 2>&1 | tail -20`
Expected: 编译成功，测试全部 PASS

- [ ] **Step 7: 报告任务完成（不提交）**

---

### Task 6: 前端基础层 —— 类型、API 封装、i18n、store

**Files:**
- Modify: `web/src/api/types.ts`（末尾追加）
- Create: `web/src/api/files.ts`
- Create: `web/src/stores/files.ts`
- Modify: `web/src/i18n/messages/zh-cn.ts`、`web/src/i18n/messages/en.ts`（各追加 `files` 节）

- [ ] **Step 1: 追加类型定义**

`web/src/api/types.ts` 末尾追加：

```ts
// 文件面板（/web/files/*）
export interface FileEntry {
  name: string
  type: 'dir' | 'file'
  size: number
  mtime: number // 毫秒时间戳
  readonly: boolean
}

export interface FileListResp {
  status: string
  path: string
  home: string
  entries: FileEntry[]
}

export interface FileContentResp {
  status: string
  path: string
  readonly: boolean
  binary: boolean
  content: string
}

export interface FileScaffoldResp {
  status: string
  path: string
}
```

- [ ] **Step 2: 创建 API 封装**

```ts
// web/src/api/files.ts
// 文件面板 API 封装。上传走原生 fetch（multipart 与 client.ts 的 JSON 封装不兼容）。
import { api, ApiError, notifyUnauthorized } from './client'
import type { FileListResp, FileContentResp, FileScaffoldResp } from './types'

const enc = encodeURIComponent

export const filesApi = {
  list: (path: string) => api.get<FileListResp>(`/web/files/list?path=${enc(path)}`),
  content: (path: string) => api.get<FileContentResp>(`/web/files/content?path=${enc(path)}`),
  save: (path: string, content: string) =>
    api.put<{ status: string }>('/web/files/content', { path, content }),
  mkdir: (path: string) => api.post<{ status: string }>('/web/files/mkdir', { path }),
  create: (path: string) => api.post<{ status: string }>('/web/files/create', { path }),
  rename: (from: string, to: string) =>
    api.post<{ status: string }>('/web/files/rename', { from, to }),
  remove: (path: string) => api.delete<{ status: string }>(`/web/files?path=${enc(path)}`),
  scaffold: (kind: 'skill' | 'mcp' | 'agent', name: string) =>
    api.post<FileScaffoldResp>('/web/files/scaffold', { kind, name }),
  downloadUrl: (path: string) => `/web/files/download?path=${enc(path)}`,

  async upload(dir: string, file: File): Promise<void> {
    const fd = new FormData()
    fd.append('path', dir)
    fd.append('file', file)
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
}
```

注意：`client.ts` 当前未导出 `ApiError` 之外的 `notifyUnauthorized`——已导出（见 `client.ts` 的 `export function notifyUnauthorized`），直接 import 即可。

- [ ] **Step 3: 创建 store**

```ts
// web/src/stores/files.ts
// 文件面板状态：开关/宽度持久化，树版本号驱动刷新，预览与编辑路径。
import { defineStore } from 'pinia'
import { ref, watch } from 'vue'

const OPEN_KEY = 'groot-files-open'
const WIDTH_KEY = 'groot-files-width'

export const useFilesStore = defineStore('files', () => {
  const open = ref(localStorage.getItem(OPEN_KEY) === '1')
  const width = ref(clampWidth(Number(localStorage.getItem(WIDTH_KEY)) || 340))
  const fullscreen = ref(false)
  const homePath = ref('') // 由首次 list('') 响应回填，供路径栏展示
  const previewPath = ref('') // 非空 = 面板显示预览而非树
  const editorPath = ref('') // 非空 = 编辑弹窗打开
  const treeVersion = ref(0) // +1 触发树重建（保持已展开节点）
  const expandedKeys = ref<string[]>([])

  watch(open, (v) => localStorage.setItem(OPEN_KEY, v ? '1' : '0'))
  watch(width, (v) => localStorage.setItem(WIDTH_KEY, String(v)))

  function toggle() {
    open.value = !open.value
    if (!open.value) fullscreen.value = false
  }

  function refresh() {
    treeVersion.value++
  }

  return {
    open, width, fullscreen, homePath, previewPath, editorPath,
    treeVersion, expandedKeys, toggle, refresh,
  }
})

export function clampWidth(w: number): number {
  return Math.min(600, Math.max(280, w))
}
```

- [ ] **Step 4: 追加 i18n 文案**

`web/src/i18n/messages/zh-cn.ts` 顶层追加 `files` 节（与 `common`、`login` 同级）：

```ts
  files: {
    title: '文件',
    refresh: '刷新',
    fullscreen: '全屏',
    exitFullscreen: '退出全屏',
    collapse: '收起面板',
    newSkill: '创建 Skill',
    newMcp: '创建 MCP',
    newAgent: '创建 Agent',
    namePrompt: '请输入名称',
    nameInvalid: '名称只能包含字母、数字、下划线和连字符',
    rename: '重命名',
    renamePrompt: '请输入新名称',
    download: '下载',
    del: '删除',
    newFile: '新建文件',
    newDir: '新建子目录',
    upload: '上传',
    deleteConfirm: '确定删除 {name} 吗？此操作不可恢复。',
    back: '返回',
    edit: '编辑',
    preview: '预览',
    save: '保存',
    saved: '已保存',
    unsavedConfirm: '有未保存的修改，确定关闭吗？',
    binary: '二进制文件，无法预览',
    tooLarge: '文件过大，无法在线查看',
    loadFailed: '加载失败',
    opFailed: '操作失败',
    readonlyTag: '只读',
  },
```

`web/src/i18n/messages/en.ts` 追加对应英文（key 一一对应）：

```ts
  files: {
    title: 'Files',
    refresh: 'Refresh',
    fullscreen: 'Fullscreen',
    exitFullscreen: 'Exit fullscreen',
    collapse: 'Collapse panel',
    newSkill: 'New Skill',
    newMcp: 'New MCP',
    newAgent: 'New Agent',
    namePrompt: 'Enter a name',
    nameInvalid: 'Only letters, digits, underscores and hyphens are allowed',
    rename: 'Rename',
    renamePrompt: 'Enter a new name',
    download: 'Download',
    del: 'Delete',
    newFile: 'New file',
    newDir: 'New folder',
    upload: 'Upload',
    deleteConfirm: 'Delete {name}? This cannot be undone.',
    back: 'Back',
    edit: 'Edit',
    preview: 'Preview',
    save: 'Save',
    saved: 'Saved',
    unsavedConfirm: 'Discard unsaved changes?',
    binary: 'Binary file, cannot preview',
    tooLarge: 'File too large to view online',
    loadFailed: 'Failed to load',
    opFailed: 'Operation failed',
    readonlyTag: 'read-only',
  },
```

- [ ] **Step 5: 类型检查**

Run: `cd web && npm run build`
Expected: 构建成功、无类型错误

- [ ] **Step 6: 报告任务完成（不提交）**

---

### Task 7: FileTree 组件

**Files:**
- Create: `web/src/components/files/FileTree.vue`

- [ ] **Step 1: 实现组件**

```vue
<!-- web/src/components/files/FileTree.vue -->
<!-- 懒加载文件树：el-tree lazy 模式，行悬停「⋯」菜单，白名单位置提供新建/上传。 -->
<script setup lang="ts">
import { ref } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { Folder, Document, Lock, MoreFilled } from '@element-plus/icons-vue'
import { filesApi } from '../../api/files'
import { useFilesStore } from '../../stores/files'

const { t } = useI18n()
const files = useFilesStore()

// 树节点数据（el-tree data 项）
interface TreeItem {
  name: string
  path: string
  leaf: boolean
  readonly: boolean
}

const treeProps = { label: 'name', isLeaf: 'leaf' }

// el-tree lazy load：level 0 加载 home 根，其余按节点路径加载
async function loadNode(node: any, resolve: (data: TreeItem[]) => void) {
  const dir = node.level === 0 ? '' : (node.data as TreeItem).path
  try {
    const resp = await filesApi.list(dir)
    if (node.level === 0) files.homePath = resp.home
    resolve(
      resp.entries.map((e) => ({
        name: e.name,
        path: dir ? `${dir}/${e.name}` : e.name,
        leaf: e.type === 'file',
        readonly: e.readonly,
      }))
    )
  } catch {
    ElMessage.error(t('files.loadFailed'))
    resolve([])
  }
}

function onNodeClick(data: TreeItem) {
  if (data.leaf) files.previewPath = data.path
}

function onExpand(data: TreeItem) {
  if (!files.expandedKeys.includes(data.path)) files.expandedKeys.push(data.path)
}
function onCollapse(data: TreeItem) {
  files.expandedKeys = files.expandedKeys.filter((k) => k !== data.path)
}

// 白名单（与后端规则一致，仅控制菜单可见性；后端仍强制校验）
function canCreate(dir: string): boolean {
  return /^skills\/[^/]+(\/|$)/.test(dir + '/')
}
function canUpload(dir: string): boolean {
  return canCreate(dir) || dir === 'skills' || dir === 'mcp'
}

function parentDir(p: string): string {
  const i = p.lastIndexOf('/')
  return i < 0 ? '' : p.slice(0, i)
}

// —— 行操作 ——

async function promptName(title: string): Promise<string | null> {
  try {
    const { value } = await ElMessageBox.prompt(t('files.namePrompt'), title, {
      inputPattern: /^[^/\\]+$/,
      inputErrorMessage: t('files.nameInvalid'),
    })
    return value?.trim() || null
  } catch {
    return null // 用户取消
  }
}

async function run(op: () => Promise<unknown>) {
  try {
    await op()
    files.refresh()
  } catch (e: any) {
    ElMessage.error(e?.message || t('files.opFailed'))
  }
}

async function onCommand(cmd: string, data: TreeItem) {
  switch (cmd) {
    case 'rename': {
      const name = await promptName(t('files.rename'))
      if (!name) return
      const to = parentDir(data.path) ? `${parentDir(data.path)}/${name}` : name
      await run(() => filesApi.rename(data.path, to))
      break
    }
    case 'delete': {
      try {
        await ElMessageBox.confirm(t('files.deleteConfirm', { name: data.name }), t('files.del'), {
          type: 'warning',
        })
      } catch {
        return
      }
      await run(() => filesApi.remove(data.path))
      break
    }
    case 'download': {
      const a = document.createElement('a')
      a.href = filesApi.downloadUrl(data.path)
      a.download = data.name
      a.click()
      break
    }
    case 'newFile': {
      const name = await promptName(t('files.newFile'))
      if (!name) return
      await run(async () => {
        await filesApi.create(`${data.path}/${name}`)
        files.editorPath = `${data.path}/${name}` // 新文件直接进编辑
      })
      break
    }
    case 'newDir': {
      const name = await promptName(t('files.newDir'))
      if (!name) return
      await run(() => filesApi.mkdir(`${data.path}/${name}`))
      break
    }
    case 'upload': {
      uploadDir.value = data.path
      uploadInput.value?.click()
      break
    }
  }
}

// 上传：隐藏 input，change 后提交
const uploadInput = ref<HTMLInputElement | null>(null)
const uploadDir = ref('')
async function onUploadChange(e: Event) {
  const input = e.target as HTMLInputElement
  const file = input.files?.[0]
  input.value = '' // 允许连续上传同一个文件
  if (!file) return
  await run(() => filesApi.upload(uploadDir.value, file))
}
</script>

<template>
  <div class="file-tree">
    <el-tree
      :key="files.treeVersion"
      lazy
      :load="loadNode"
      :props="treeProps"
      node-key="path"
      :default-expanded-keys="files.expandedKeys"
      :expand-on-click-node="true"
      @node-click="onNodeClick"
      @node-expand="onExpand"
      @node-collapse="onCollapse"
    >
      <template #default="{ data }">
        <div class="tree-row">
          <el-icon :size="15" class="tree-icon">
            <Document v-if="data.leaf" />
            <Folder v-else />
          </el-icon>
          <span class="tree-name">{{ data.name }}</span>
          <el-icon v-if="data.readonly" :size="12" class="tree-lock" :title="t('files.readonlyTag')">
            <Lock />
          </el-icon>
          <el-dropdown
            trigger="click"
            class="tree-more"
            @command="(cmd: string) => onCommand(cmd, data)"
            @click.stop
          >
            <el-icon :size="14" @click.stop><MoreFilled /></el-icon>
            <template #dropdown>
              <el-dropdown-menu>
                <el-dropdown-item command="rename" :disabled="data.readonly">
                  {{ t('files.rename') }}
                </el-dropdown-item>
                <el-dropdown-item v-if="data.leaf" command="download">
                  {{ t('files.download') }}
                </el-dropdown-item>
                <el-dropdown-item v-if="!data.leaf && canCreate(data.path)" command="newFile">
                  {{ t('files.newFile') }}
                </el-dropdown-item>
                <el-dropdown-item v-if="!data.leaf && canCreate(data.path)" command="newDir">
                  {{ t('files.newDir') }}
                </el-dropdown-item>
                <el-dropdown-item v-if="!data.leaf && canUpload(data.path)" command="upload">
                  {{ t('files.upload') }}
                </el-dropdown-item>
                <el-dropdown-item command="delete" :disabled="data.readonly" divided>
                  {{ t('files.del') }}
                </el-dropdown-item>
              </el-dropdown-menu>
            </template>
          </el-dropdown>
        </div>
      </template>
    </el-tree>
    <input ref="uploadInput" type="file" hidden @change="onUploadChange" />
  </div>
</template>

<style scoped>
.file-tree {
  flex: 1;
  overflow: auto;
  padding: 4px 6px;
}
.tree-row {
  flex: 1;
  min-width: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  padding-right: 4px;
}
.tree-icon {
  flex-shrink: 0;
  opacity: 0.75;
}
.tree-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
.tree-lock {
  flex-shrink: 0;
  opacity: 0.5;
}
/* ⋯ 按钮：默认隐藏，悬停行时浮现 */
.tree-more {
  flex-shrink: 0;
  opacity: 0;
  transition: opacity 0.15s;
}
.tree-row:hover .tree-more {
  opacity: 0.7;
}
</style>
```

- [ ] **Step 2: 类型检查**

Run: `cd web && npm run build`
Expected: 构建成功

- [ ] **Step 3: 报告任务完成（不提交）**

---

### Task 8: FilePreview 组件

**Files:**
- Create: `web/src/components/files/FilePreview.vue`

- [ ] **Step 1: 实现组件**

```vue
<!-- web/src/components/files/FilePreview.vue -->
<!-- 面板内只读预览：md 渲染 / 代码高亮 / 图片 / 二进制提示。 -->
<script setup lang="ts">
import { ref, computed, watch } from 'vue'
import { ArrowLeft, EditPen, Download, Loading } from '@element-plus/icons-vue'
import hljs from 'highlight.js/lib/core'
import MarkdownView from '../chat/MarkdownView.vue'
import { filesApi } from '../../api/files'
import { useFilesStore } from '../../stores/files'
import { ApiError } from '../../api/client'

const { t } = useI18n()
const files = useFilesStore()

const props = defineProps<{ path: string }>()

type Mode = 'markdown' | 'code' | 'image' | 'binary' | 'toolarge' | 'error'
const mode = ref<Mode>('code')
const content = ref('')
const readonly = ref(false)
const loading = ref(false)

const IMG_EXTS = ['png', 'jpg', 'jpeg', 'gif', 'svg', 'webp', 'ico']
const fileName = computed(() => props.path.split('/').pop() || props.path)
const ext = computed(() => (fileName.value.includes('.') ? fileName.value.split('.').pop()!.toLowerCase() : ''))
const imageUrl = computed(() => filesApi.downloadUrl(props.path))
// 可编辑：文本类内容且非只读
const canEdit = computed(
  () => !readonly.value && (mode.value === 'markdown' || mode.value === 'code')
)

// 代码高亮：语言已被 MarkdownView 注册进 hljs 单例（ChatView 必然加载过它），
// 未知语言回退为纯文本。
const highlighted = computed(() => {
  if (mode.value !== 'code') return ''
  const lang = ext.value === 'yml' ? 'yaml' : ext.value
  if (lang && hljs.getLanguage(lang)) {
    return hljs.highlight(content.value, { language: lang }).value
  }
  return escapeHtml(content.value)
})

function escapeHtml(s: string): string {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;')
}

watch(
  () => props.path,
  async (p) => {
    if (!p) return
    content.value = ''
    if (IMG_EXTS.includes(ext.value)) {
      mode.value = 'image'
      return
    }
    loading.value = true
    try {
      const resp = await filesApi.content(p)
      readonly.value = resp.readonly
      if (resp.binary) {
        mode.value = 'binary'
      } else {
        content.value = resp.content
        mode.value = ext.value === 'md' ? 'markdown' : 'code'
      }
    } catch (e) {
      mode.value = e instanceof ApiError && e.status === 413 ? 'toolarge' : 'error'
    } finally {
      loading.value = false
    }
  },
  { immediate: true }
)

function download() {
  const a = document.createElement('a')
  a.href = imageUrl.value
  a.download = fileName.value
  a.click()
}
</script>

<template>
  <div class="file-preview">
    <header class="preview-bar">
      <button class="bar-btn" type="button" :title="t('files.back')" @click="files.previewPath = ''">
        <el-icon :size="15"><ArrowLeft /></el-icon>
      </button>
      <span class="preview-name" :title="path">{{ fileName }}</span>
      <span v-if="readonly" class="preview-ro">{{ t('files.readonlyTag') }}</span>
      <button v-if="canEdit" class="bar-btn" type="button" :title="t('files.edit')"
        @click="files.editorPath = path">
        <el-icon :size="15"><EditPen /></el-icon>
      </button>
      <button class="bar-btn" type="button" :title="t('files.download')" @click="download">
        <el-icon :size="15"><Download /></el-icon>
      </button>
    </header>

    <div class="preview-body">
      <div v-if="loading" class="preview-center">
        <el-icon class="is-loading" :size="20"><Loading /></el-icon>
      </div>
      <MarkdownView v-else-if="mode === 'markdown'" :content="content" />
      <pre v-else-if="mode === 'code'" class="preview-code"><code v-html="highlighted"></code></pre>
      <div v-else-if="mode === 'image'" class="preview-center">
        <img :src="imageUrl" :alt="fileName" class="preview-img" />
      </div>
      <div v-else class="preview-center preview-hint">
        {{ mode === 'binary' ? t('files.binary') : mode === 'toolarge' ? t('files.tooLarge') : t('files.loadFailed') }}
      </div>
    </div>
  </div>
</template>

<style scoped>
.file-preview {
  flex: 1;
  min-height: 0;
  display: flex;
  flex-direction: column;
}
.preview-bar {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 6px 10px;
  border-bottom: 1px solid rgba(127, 127, 127, 0.15);
}
.bar-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 26px;
  height: 26px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: inherit;
  opacity: 0.7;
  cursor: pointer;
}
.bar-btn:hover {
  background: rgba(127, 127, 127, 0.12);
  opacity: 1;
}
.preview-name {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.9em;
  font-weight: 600;
}
.preview-ro {
  flex-shrink: 0;
  font-size: 0.75em;
  opacity: 0.55;
  border: 1px solid rgba(127, 127, 127, 0.35);
  border-radius: 4px;
  padding: 0 4px;
}
.preview-body {
  flex: 1;
  overflow: auto;
  padding: 10px 12px;
}
.preview-code {
  margin: 0;
  font-size: 12px;
  line-height: 1.6;
  white-space: pre-wrap;
  word-break: break-all;
}
.preview-center {
  height: 100%;
  display: flex;
  align-items: center;
  justify-content: center;
}
.preview-hint {
  opacity: 0.55;
  font-size: 0.9em;
}
.preview-img {
  max-width: 100%;
  max-height: 100%;
  object-fit: contain;
}
</style>
```

- [ ] **Step 2: 类型检查**

Run: `cd web && npm run build`
Expected: 构建成功

- [ ] **Step 3: 报告任务完成（不提交）**

---

### Task 9: FileEditorModal 组件（CodeMirror + md 分屏）

**Files:**
- Modify: `web/package.json`（新增依赖）
- Create: `web/src/components/files/FileEditorModal.vue`

- [ ] **Step 1: 安装 CodeMirror 依赖**

Run: `cd web && npm install codemirror@^6.0.1 @codemirror/lang-markdown@^6.2.5 @codemirror/lang-yaml@^6.1.1 @codemirror/lang-json@^6.0.1`
Expected: package.json dependencies 出现四个包

- [ ] **Step 2: 实现组件**

```vue
<!-- web/src/components/files/FileEditorModal.vue -->
<!-- 大弹窗编辑器：CodeMirror 6 动态加载；md 文件左右分屏实时预览（可收起）。 -->
<script setup lang="ts">
import { ref, computed, watch, nextTick, onBeforeUnmount } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import { View, Hide, Loading } from '@element-plus/icons-vue'
import MarkdownView from '../chat/MarkdownView.vue'
import { filesApi } from '../../api/files'
import { useFilesStore } from '../../stores/files'

const { t } = useI18n()
const files = useFilesStore()

const visible = computed(() => !!files.editorPath)
const isMd = computed(() => files.editorPath.toLowerCase().endsWith('.md'))
const fileName = computed(() => files.editorPath.split('/').pop() || '')

const draft = ref('')
const dirty = ref(false)
const loading = ref(false)
const saving = ref(false)
const showPreview = ref(true)
const cmHost = ref<HTMLElement | null>(null)

// CodeMirror EditorView 实例（动态加载，避免类型依赖入主包）
let view: { destroy: () => void } | null = null

watch(
  () => files.editorPath,
  async (p) => {
    destroyEditor()
    if (!p) return
    loading.value = true
    try {
      const resp = await filesApi.content(p)
      draft.value = resp.content
      dirty.value = false
      await nextTick() // 等 el-dialog 渲染出 cmHost
      await initEditor(resp.content, p)
    } catch {
      ElMessage.error(t('files.loadFailed'))
      files.editorPath = ''
    } finally {
      loading.value = false
    }
  }
)

async function initEditor(doc: string, path: string) {
  const [{ EditorView, basicSetup }, langExt] = await Promise.all([
    import('codemirror'),
    languageFor(path),
  ])
  if (!cmHost.value) return
  view = new EditorView({
    doc,
    parent: cmHost.value,
    extensions: [
      basicSetup,
      ...(langExt ? [langExt] : []),
      EditorView.lineWrapping,
      EditorView.updateListener.of((u) => {
        if (u.docChanged) {
          draft.value = u.state.doc.toString()
          dirty.value = true
        }
      }),
    ],
  })
}

// 按扩展名动态加载语言包；未覆盖的扩展名无高亮
async function languageFor(path: string) {
  const ext = path.split('.').pop()?.toLowerCase()
  if (ext === 'md') return (await import('@codemirror/lang-markdown')).markdown()
  if (ext === 'yaml' || ext === 'yml') return (await import('@codemirror/lang-yaml')).yaml()
  if (ext === 'json') return (await import('@codemirror/lang-json')).json()
  return null
}

function destroyEditor() {
  view?.destroy()
  view = null
}
onBeforeUnmount(destroyEditor)

async function save() {
  saving.value = true
  try {
    await filesApi.save(files.editorPath, draft.value)
    dirty.value = false
    ElMessage.success(t('files.saved'))
    files.refresh() // 树上的 size/mtime 变了
  } catch (e: any) {
    ElMessage.error(e?.message || t('files.opFailed'))
  } finally {
    saving.value = false
  }
}

// el-dialog 的 before-close：脏状态需确认
async function handleBeforeClose(done: () => void) {
  if (dirty.value) {
    try {
      await ElMessageBox.confirm(t('files.unsavedConfirm'), fileName.value, { type: 'warning' })
    } catch {
      return // 用户取消关闭
    }
  }
  done()
}

function onClosed() {
  destroyEditor()
  files.editorPath = ''
}
</script>

<template>
  <el-dialog
    :model-value="visible"
    :title="fileName"
    width="82%"
    top="4vh"
    align-center
    append-to-body
    :before-close="handleBeforeClose"
    class="file-editor-dialog"
    @closed="onClosed"
  >
    <template #header>
      <div class="editor-head">
        <span class="editor-title" :title="files.editorPath">{{ fileName }}</span>
        <button
          v-if="isMd"
          class="head-btn"
          type="button"
          :title="showPreview ? t('files.preview') + ' off' : t('files.preview')"
          @click="showPreview = !showPreview"
        >
          <el-icon :size="15"><View v-if="!showPreview" /><Hide v-else /></el-icon>
        </button>
        <el-button size="small" type="primary" :loading="saving" :disabled="!dirty" @click="save">
          {{ t('files.save') }}
        </el-button>
      </div>
    </template>

    <div v-if="loading" class="editor-loading">
      <el-icon class="is-loading" :size="22"><Loading /></el-icon>
    </div>
    <div v-else class="editor-body" :class="{ split: isMd && showPreview }">
      <div ref="cmHost" class="cm-host"></div>
      <div v-if="isMd && showPreview" class="md-preview">
        <MarkdownView :content="draft" />
      </div>
    </div>
  </el-dialog>
</template>

<style scoped>
.editor-head {
  display: flex;
  align-items: center;
  gap: 10px;
  padding-right: 24px;
}
.editor-title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 600;
}
.head-btn {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: inherit;
  opacity: 0.7;
  cursor: pointer;
}
.head-btn:hover {
  background: rgba(127, 127, 127, 0.12);
  opacity: 1;
}
.editor-loading {
  height: 70vh;
  display: flex;
  align-items: center;
  justify-content: center;
}
.editor-body {
  height: 78vh;
  display: flex;
  gap: 0;
}
.cm-host {
  flex: 1;
  min-width: 0;
  overflow: auto;
  font-size: 13px;
}
/* CodeMirror 撑满容器 */
.cm-host :deep(.cm-editor) {
  height: 100%;
}
.editor-body.split .md-preview {
  flex: 1;
  min-width: 0;
  overflow: auto;
  border-left: 1px solid rgba(127, 127, 127, 0.2);
  padding: 0 16px;
}
</style>

<style>
/* 弹窗 body 去 padding，让编辑器贴边 */
.file-editor-dialog .el-dialog__body {
  padding: 0;
}
</style>
```

- [ ] **Step 3: 类型检查**

Run: `cd web && npm run build`
Expected: 构建成功；产物里出现独立的 codemirror chunk（动态 import 生效）

- [ ] **Step 4: 报告任务完成（不提交）**

---

### Task 10: FilePanel 容器组件

**Files:**
- Create: `web/src/components/files/FilePanel.vue`

- [ ] **Step 1: 实现组件**

```vue
<!-- web/src/components/files/FilePanel.vue -->
<!-- 右侧抽屉容器：工具栏（scaffold ×3 / 全屏 / 收起）、路径栏、树/预览切换、宽度拖动。 -->
<script setup lang="ts">
import { onBeforeUnmount } from 'vue'
import { ElMessage, ElMessageBox } from 'element-plus'
import {
  MagicStick, Connection, Avatar, FullScreen, Fold, Refresh,
} from '@element-plus/icons-vue'
import FileTree from './FileTree.vue'
import FilePreview from './FilePreview.vue'
import FileEditorModal from './FileEditorModal.vue'
import { filesApi } from '../../api/files'
import { useFilesStore, clampWidth } from '../../stores/files'

const { t } = useI18n()
const files = useFilesStore()

// —— 宽度拖动 ——
let dragStartX = 0
let dragStartW = 0

function onDragStart(e: MouseEvent) {
  dragStartX = e.clientX
  dragStartW = files.width
  document.addEventListener('mousemove', onDragMove)
  document.addEventListener('mouseup', onDragEnd)
  document.body.style.userSelect = 'none'
}
function onDragMove(e: MouseEvent) {
  files.width = clampWidth(dragStartW + (dragStartX - e.clientX))
}
function onDragEnd() {
  document.removeEventListener('mousemove', onDragMove)
  document.removeEventListener('mouseup', onDragEnd)
  document.body.style.userSelect = ''
}
onBeforeUnmount(onDragEnd)

// —— scaffold 创建 ——
const SCAFFOLD_NAME_RE = /^[\p{L}\p{N}][\p{L}\p{N}_-]{0,63}$/u

async function scaffold(kind: 'skill' | 'mcp' | 'agent', title: string) {
  let name: string
  try {
    const { value } = await ElMessageBox.prompt(t('files.namePrompt'), title, {
      inputPattern: SCAFFOLD_NAME_RE,
      inputErrorMessage: t('files.nameInvalid'),
    })
    name = value.trim()
  } catch {
    return // 取消
  }
  try {
    const resp = await filesApi.scaffold(kind, name)
    files.refresh()
    files.previewPath = ''
    files.editorPath = resp.path // 直接进编辑
  } catch (e: any) {
    ElMessage.error(e?.message || t('files.opFailed'))
  }
}
</script>

<template>
  <aside class="file-panel" :style="files.fullscreen ? undefined : { width: files.width + 'px' }">
    <div class="drag-handle" @mousedown="onDragStart"></div>

    <header class="panel-head">
      <span class="panel-title">{{ t('files.title') }}</span>
      <button class="head-icon" type="button" :title="t('files.newSkill')"
        @click="scaffold('skill', t('files.newSkill'))">
        <el-icon :size="16"><MagicStick /></el-icon>
      </button>
      <button class="head-icon" type="button" :title="t('files.newMcp')"
        @click="scaffold('mcp', t('files.newMcp'))">
        <el-icon :size="16"><Connection /></el-icon>
      </button>
      <button class="head-icon" type="button" :title="t('files.newAgent')"
        @click="scaffold('agent', t('files.newAgent'))">
        <el-icon :size="16"><Avatar /></el-icon>
      </button>
      <span class="head-gap"></span>
      <button class="head-icon" type="button"
        :title="files.fullscreen ? t('files.exitFullscreen') : t('files.fullscreen')"
        @click="files.fullscreen = !files.fullscreen">
        <el-icon :size="15"><FullScreen /></el-icon>
      </button>
      <button class="head-icon" type="button" :title="t('files.collapse')" @click="files.toggle()">
        <el-icon :size="16"><Fold /></el-icon>
      </button>
    </header>

    <div class="path-bar">
      <span class="path-text" :title="files.homePath">{{ files.homePath || '~/.groot' }}</span>
      <button class="head-icon" type="button" :title="t('files.refresh')" @click="files.refresh()">
        <el-icon :size="14"><Refresh /></el-icon>
      </button>
    </div>

    <FileTree v-show="!files.previewPath" />
    <FilePreview v-if="files.previewPath" :path="files.previewPath" />
    <FileEditorModal />
  </aside>
</template>

<style scoped>
.file-panel {
  flex-shrink: 0;
  height: 100vh;
  display: flex;
  flex-direction: column;
  position: relative;
  border-left: 1px solid rgba(127, 127, 127, 0.15);
  background: var(--el-bg-color);
}
.drag-handle {
  position: absolute;
  left: -3px;
  top: 0;
  bottom: 0;
  width: 6px;
  cursor: col-resize;
  z-index: 2;
}
.panel-head {
  flex-shrink: 0;
  height: 56px;
  display: flex;
  align-items: center;
  gap: 2px;
  padding: 0 10px;
  border-bottom: 1px solid rgba(127, 127, 127, 0.15);
}
.panel-title {
  font-weight: 600;
  margin-right: 8px;
}
.head-gap {
  flex: 1;
}
.head-icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 28px;
  height: 28px;
  border: none;
  border-radius: 6px;
  background: transparent;
  color: inherit;
  opacity: 0.65;
  cursor: pointer;
  transition: background 0.15s, opacity 0.15s;
}
.head-icon:hover {
  background: rgba(127, 127, 127, 0.12);
  opacity: 0.95;
}
.path-bar {
  flex-shrink: 0;
  display: flex;
  align-items: center;
  gap: 6px;
  padding: 4px 10px;
  border-bottom: 1px solid rgba(127, 127, 127, 0.12);
}
.path-text {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-size: 0.78em;
  opacity: 0.6;
  direction: rtl; /* 路径过长时保留结尾（目录名）可见 */
  text-align: left;
}
</style>
```

- [ ] **Step 2: 类型检查**

Run: `cd web && npm run build`
Expected: 构建成功

- [ ] **Step 3: 报告任务完成（不提交）**

---

### Task 11: ChatView 集成与端到端验证

**Files:**
- Modify: `web/src/views/ChatView.vue`（顶栏按钮 + 布局挂载 + 全屏样式）

- [ ] **Step 1: 修改 ChatView**

`<script setup>` 部分（15 行附近）：

```ts
// import 行修改：
import { Monitor, FolderOpened } from '@element-plus/icons-vue'
// 新增 import（与其他组件 import 并列）：
import FilePanel from '../components/files/FilePanel.vue'
import { useFilesStore } from '../stores/files'
```

在 `const meta = useMetaStore()`（21 行）之后加：

```ts
const files = useFilesStore()
```

模板部分：`.chat-layout` 根元素（128 行）加全屏 class：

```html
  <div class="chat-layout" :class="{ 'files-full': files.open && files.fullscreen }">
```

顶栏日志按钮（151-159 行）之后并列加：

```html
        <button
          class="topbar-logs"
          type="button"
          :title="t('files.title')"
          @click="files.toggle()"
        >
          <el-icon :size="17"><FolderOpened /></el-icon>
        </button>
```

`.main` 的闭合 `</div>`（187 行，`</div>` 对应 `<div class="main">`）之后、`.chat-layout` 闭合之前加：

```html
    <FilePanel v-if="files.open" />
```

样式部分追加：

```css
/* 文件面板全屏：隐藏对话主区（组件保持挂载，状态不丢） */
.chat-layout.files-full .main {
  display: none;
}
```

- [ ] **Step 2: 构建前端并同步到 dist**

Run: `cd web && npm run build`
Expected: 构建成功

- [ ] **Step 3: 编译后端并手工冒烟**

Run: `cd /Users/zhangfengda/workspace/groot && go build -o dist/groot ./cmd && go test ./internal/... 2>&1 | tail -5`
Expected: 编译成功、测试 PASS

手工冒烟（可选，需本地启动 groot 后浏览器验证）：
1. 顶栏出现文件夹图标，点击右侧滑出面板
2. 树能展开 skills/，单击 GROOT.md 出预览，点编辑弹 CodeMirror
3. config.yaml 显示锁标记，无编辑按钮
4. 面板列表中看不到 groot.db
5. 创建 skill 按钮 → 输入名称 → 树中出现新目录并弹出编辑器

- [ ] **Step 4: 检查 README 是否需要更新**

依据项目记忆 `readme-api-scope`：README 只写对外 API，`/web/files/*` 是 Web 自用端点，**不新增 API 文档**。检查 README 中是否有「Web 界面」功能介绍章节：

Run: `grep -n "Web 界面\|Web UI\|web 界面" README.md | head -5`

若存在功能清单式章节，追加一行文件面板功能简介（一句话即可）；若无此类章节，跳过。

- [ ] **Step 5: 报告任务完成（不提交）**

---

### Task 12: Python 系统测试用例与测试文档

**Files:**
- Create: `tests/python/test_files_api.py`
- Modify: `tests/TEST_CASES.md`（追加用例点）

- [ ] **Step 1: 编写系统测试（只编写，不运行——用户自行运行）**

```python
# tests/python/test_files_api.py
"""
文件面板 API（/web/files/*）系统测试

覆盖：认证要求、路径安全、隐藏/只读规则、白名单 CRUD、scaffold。
运行方式（用户执行）：cd tests/python && pytest test_files_api.py -v
"""

import os
import requests
import pytest

from conftest import BASE_URL, TEST_WEB_USER, TEST_WEB_PASS

WEB_USER = os.environ.get("GROOT_WEB_USER", TEST_WEB_USER)
WEB_PASS = os.environ.get("GROOT_WEB_PASS", TEST_WEB_PASS)

FILES = f"{BASE_URL}/web/files"


@pytest.fixture(scope="module")
def web(server):
    """已登录的 Web 会话（Cookie 认证）；登录失败时跳过整个模块"""
    s = requests.Session()
    try:
        resp = s.post(f"{BASE_URL}/web/login", json={
            "username": WEB_USER,
            "password": WEB_PASS,
        }, timeout=10)
    except requests.RequestException as e:
        pytest.skip(f"groot 服务不可达: {e}")
    if resp.status_code != 200:
        pytest.skip(f"Web 登录失败 ({resp.status_code}): {resp.text}")
    return s


class TestFilesAuth:
    """未登录访问一律 401"""

    def test_list_without_cookie(self, server):
        r = requests.get(f"{FILES}/list", params={"path": ""}, timeout=10)
        assert r.status_code == 401

    def test_save_without_cookie(self, server):
        r = requests.put(f"{FILES}/content",
                         json={"path": "GROOT.md", "content": "x"}, timeout=10)
        assert r.status_code == 401


class TestFilesSecurity:
    """路径安全与可见性规则"""

    def test_list_root(self, web):
        r = web.get(f"{FILES}/list", params={"path": ""}, timeout=10)
        assert r.status_code == 200
        body = r.json()
        assert body["status"] == "success"
        assert body["home"]

    def test_groot_db_hidden_in_list(self, web):
        r = web.get(f"{FILES}/list", params={"path": ""}, timeout=10)
        names = [e["name"] for e in r.json()["entries"]]
        assert not any(n.startswith("groot.db") for n in names)
        assert ".DS_Store" not in names

    def test_groot_db_direct_access_404(self, web):
        for path in ["groot.db", "groot.db-wal", "groot.db-shm"]:
            r = web.get(f"{FILES}/content", params={"path": path}, timeout=10)
            assert r.status_code == 404, f"{path} 应 404, got {r.status_code}"

    def test_traversal_normalized(self, web):
        # "../" 被锚定归一化到根，不产生越界（返回根目录列表）
        r = web.get(f"{FILES}/list", params={"path": "../../etc"}, timeout=10)
        assert r.status_code in (200, 404)  # 归一化为 etc/，通常不存在 → 404

    def test_config_readonly(self, web):
        r = web.get(f"{FILES}/content", params={"path": "config.yaml"}, timeout=10)
        assert r.status_code == 200
        assert r.json()["readonly"] is True
        r = web.put(f"{FILES}/content",
                    json={"path": "config.yaml", "content": "x"}, timeout=10)
        assert r.status_code == 403
        r = web.delete(f"{FILES}", params={"path": "config.yaml"}, timeout=10)
        assert r.status_code == 403


class TestFilesCrud:
    """白名单内的创建/编辑/改名/删除全流程（skills/_pytest-skill 沙箱）"""

    SKILL = "_pytest-skill"

    def test_full_lifecycle(self, web):
        # 1. scaffold 创建 skill
        r = web.post(f"{FILES}/scaffold",
                     json={"kind": "skill", "name": self.SKILL}, timeout=10)
        assert r.status_code == 200, r.text
        skill_md = r.json()["path"]
        assert skill_md == f"skills/{self.SKILL}/SKILL.md"

        # 2. 重名 scaffold → 409
        r = web.post(f"{FILES}/scaffold",
                     json={"kind": "skill", "name": self.SKILL}, timeout=10)
        assert r.status_code == 409

        # 3. skill 目录内新建文件 + 保存内容
        note = f"skills/{self.SKILL}/note.md"
        r = web.post(f"{FILES}/create", json={"path": note}, timeout=10)
        assert r.status_code == 200, r.text
        r = web.put(f"{FILES}/content",
                    json={"path": note, "content": "# note\n"}, timeout=10)
        assert r.status_code == 200, r.text
        r = web.get(f"{FILES}/content", params={"path": note}, timeout=10)
        assert r.json()["content"] == "# note\n"

        # 4. 白名单外新建 → 403
        r = web.post(f"{FILES}/create", json={"path": "logs/hack.txt"}, timeout=10)
        assert r.status_code == 403

        # 5. 改名
        note2 = f"skills/{self.SKILL}/note2.md"
        r = web.post(f"{FILES}/rename", json={"from": note, "to": note2}, timeout=10)
        assert r.status_code == 200, r.text

        # 6. 删除非空目录 → 409
        r = web.delete(f"{FILES}", params={"path": f"skills/{self.SKILL}"}, timeout=10)
        assert r.status_code == 409

        # 7. 清理：先删文件再删目录
        for p in [note2, skill_md]:
            r = web.delete(f"{FILES}", params={"path": p}, timeout=10)
            assert r.status_code == 200, f"删除 {p} 失败: {r.text}"
        r = web.delete(f"{FILES}", params={"path": f"skills/{self.SKILL}"}, timeout=10)
        assert r.status_code == 200, r.text

    def test_upload_to_mcp(self, web):
        r = web.post(f"{FILES}/upload",
                     data={"path": "mcp"},
                     files={"file": ("_pytest-upload.json", b'{"name":"t"}')},
                     timeout=10)
        assert r.status_code == 200, r.text
        # 清理
        r = web.delete(f"{FILES}", params={"path": "mcp/_pytest-upload.json"}, timeout=10)
        assert r.status_code == 200

    def test_upload_to_logs_forbidden(self, web):
        r = web.post(f"{FILES}/upload",
                     data={"path": "logs"},
                     files={"file": ("x.txt", b"data")},
                     timeout=10)
        assert r.status_code == 403
```

- [ ] **Step 2: 更新测试用例汇总**

`tests/TEST_CASES.md` 追加章节：

```markdown
## 文件面板（/web/files/*）

### Go 单元测试（internal/webfiles + internal/api/handler/files_test.go）
- 路径清洗：".." 穿越折叠、反斜杠归一、NUL 拒绝
- 符号链接逃逸：链接与链接子路径均 404
- 隐藏规则：根目录 groot.db* 列表过滤 + 直接访问 404；子目录同名不隐藏
- 只读规则：根目录 env.yaml/config.yaml 写/改名/删除 403，可读可下载
- 白名单矩阵：新建（仅 skill 目录内）/上传（skill 目录 + skills/ + mcp/）
- 读取：文本内容、二进制探测（NUL 字节）、2MB 超限、读目录 400
- 保存：正常写入、只读 403、不存在 404、超限 413
- 新建：文件/目录、重名 409、点开头名称 400、父目录不存在 404
- 改名：同目录成功、跨目录 400、只读 403、目标重名 409
- 删除：文件/空目录成功、非空 409、home 根 400、隐藏 404
- 上传目标：白名单、20MB 超限、带路径文件名 400、重名 409
- Scaffold：skill/mcp/agent 模板生成、重名 409、非法名称/类型 400
- Handler：错误码映射（404/403/400/409/413）、list 响应含 home 字段

### Python 系统测试（tests/python/test_files_api.py）
- 认证：未登录 401
- 安全：groot.db* 列表隐藏 + 直接访问 404；穿越归一化；config.yaml 只读 403
- CRUD 全流程：scaffold → 新建 → 保存 → 读取 → 改名 → 删除（含非空目录 409）
- 上传：mcp/ 允许、logs/ 403
```

- [ ] **Step 3: 语法检查（不运行测试）**

Run: `cd tests/python && python3 -m py_compile test_files_api.py && echo OK`
Expected: OK

- [ ] **Step 4: 报告任务完成（不提交）**

---

## 完成标准

1. `go test ./internal/...` 全部通过
2. `go build -o dist/groot ./cmd` 编译成功
3. `cd web && npm run build` 构建成功、无类型错误
4. 手工冒烟（Task 11 Step 3 清单）通过
5. Python 系统测试已就位（由用户运行验证）
6. 所有提交动作等待用户明确指令
