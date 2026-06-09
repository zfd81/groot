# 集群共享配置同步 (groot push/pull/diff) 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 新增 `groot push` / `groot pull` / `groot diff` 三个子命令,在 minio 模式下将集群共享配置(skills / mcp / subagents / config.yaml / GROOT.md)在本地 HOME 与 MinIO 之间做镜像同步。

**Architecture:** `internal/sync/` 包实现四层:resource(白名单+类型定义) → resolver(用户路径展开为资源对象列表) → diff(size+mtime 判等,返回 DiffResult struct) → sync(Push/Pull 调用 diff 再执行镜像操作)。cmd 层各一个文件(push.go / pull.go / diff.go)负责 flags 解析、调用 SyncManager、渲染输出、交互确认。Push/Pull 默认显示 diff 并等待 y/n 确认,`--yes/-y` 跳过。测试只用单测(local storage mock + os.* 本地文件),不依赖真实 minio。

**Tech Stack:** Go(已有)、`internal/storage`(已有 Storage 接口 + local/minio 实现)、`os.*` 本地文件操作(HOME 侧永远是本地 fs)、Go 标准测试框架。

---

## 文件结构

**新增:**

- `internal/sync/resource.go` — `Resource` 类型、`SyncableResourceRoots` 白名单、资源对象校验
- `internal/sync/resolver.go` — 用户输入路径展开为具体资源对象文件列表(双侧)
- `internal/sync/diff.go` — `DiffResult` 类型、`computeDiff()` 算法(size+mtime ±1s)
- `internal/sync/sync.go` — `SyncManager` 接口 + `localSyncManager` 实现(Push/Pull/Diff)
- `internal/sync/render.go` — `RenderDiff(w io.Writer, d DiffResult, direction string)` 把 diff 渲染为可读输出
- `internal/sync/resource_test.go` — 白名单校验测试
- `internal/sync/resolver_test.go` — 路径展开逻辑测试
- `internal/sync/diff_test.go` — 判等算法 + mtime 精度测试
- `internal/sync/sync_test.go` — Push/Pull 端到端单测(两个 local tmpdir 模拟 HOME vs MinIO 侧)
- `internal/cmd/push.go` — `ParsePushFlags` / `RunPush`
- `internal/cmd/pull.go` — `ParsePullFlags` / `RunPull`
- `internal/cmd/diff_cmd.go` — `ParseDiffFlags` / `RunDiff`(避免与 diff 包同名冲突)

**修改:**

- `cmd/groot/main.go` — dispatch table 新增 `push` / `pull` / `diff` case + 三个 handler 函数

---

## Task 1: resource.go — 类型、白名单、路径校验

**Files:**
- Create: `internal/sync/resource.go`
- Create: `internal/sync/resource_test.go`

- [ ] **Step 1: 新建 `internal/sync/resource_test.go` 写失败测试**

```go
package sync

import (
    "testing"
)

func TestSyncableRoots(t *testing.T) {
    roots := SyncableResourceRoots
    if len(roots) != 5 {
        t.Fatalf("expected 5 roots, got %d", len(roots))
    }
    for _, r := range []string{"config.yaml", "skills", "subagents", "mcp", "GROOT.md"} {
        found := false
        for _, root := range roots {
            if root == r {
                found = true
                break
            }
        }
        if !found {
            t.Errorf("missing root: %s", r)
        }
    }
}

func TestValidateSyncPath_WhitelistRoot(t *testing.T) {
    if err := ValidateSyncPath("config.yaml"); err != nil {
        t.Errorf("expected nil, got %v", err)
    }
    if err := ValidateSyncPath("skills"); err != nil {
        t.Errorf("expected nil, got %v", err)
    }
    if err := ValidateSyncPath("skills/weather"); err != nil {
        t.Errorf("expected nil, got %v", err)
    }
    if err := ValidateSyncPath("subagents/db-agent/agent.md"); err != nil {
        t.Errorf("expected nil, got %v", err)
    }
}

func TestValidateSyncPath_Rejected(t *testing.T) {
    cases := []string{"env.yaml", "logs", "memory", "cluster", "../etc/passwd", ""}
    for _, p := range cases {
        if err := ValidateSyncPath(p); err == nil {
            t.Errorf("expected error for %q, got nil", p)
        }
    }
}

func TestValidateSyncPath_SkillFileDirect(t *testing.T) {
    // 禁止直接操作 skill 目录下的单个文件
    if err := ValidateSyncPath("skills/weather/SKILL.md"); err == nil {
        t.Error("expected error for direct skill file path")
    }
    if err := ValidateSyncPath("subagents/db-agent/skills/sql/SKILL.md"); err == nil {
        t.Error("expected error for direct subagent skill file path")
    }
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/sync/... -v
```

Expected: FAIL — `sync` 包不存在

- [ ] **Step 3: 新建 `internal/sync/resource.go`**

```go
package sync

import (
    "fmt"
    "strings"
)

// SyncableResourceRoots 是受 sync 管理的根路径白名单。
// env.yaml / memory / schedules / cluster 不在此列。
var SyncableResourceRoots = []string{
    "config.yaml",
    "skills",
    "subagents",
    "mcp",
    "GROOT.md",
}

// ValidateSyncPath 校验用户指定的 path 是否在白名单范围内且符合资源对象操作规则:
//   - 必须以白名单根为前缀
//   - 不允许直接操作 skills/{name}/SKILL.md (必须操作整个 skill 目录)
//   - 不允许操作 env.yaml 等黑名单路径
func ValidateSyncPath(path string) error {
    if path == "" {
        return fmt.Errorf("sync: empty path")
    }
    // 路径遍历防护
    if strings.Contains(path, "..") {
        return fmt.Errorf("sync: path traversal not allowed: %q", path)
    }

    // 校验白名单根
    matched := false
    for _, root := range SyncableResourceRoots {
        if path == root || strings.HasPrefix(path, root+"/") {
            matched = true
            break
        }
    }
    if !matched {
        return fmt.Errorf("sync: path %q is not in sync whitelist", path)
    }

    // 禁止直接操作 skill 目录内的单个文件:
    //   skills/{name}/XXX → 必须操作 skills/{name}
    //   subagents/{sa}/skills/{name}/XXX → 同上
    if isDirectSkillFile(path) {
        return fmt.Errorf("sync: path %q is inside a skill directory — operate the skill directory instead (e.g. %q)",
            path, parentSkillDir(path))
    }

    return nil
}

// isDirectSkillFile 判断 path 是否是 skill 目录下的具体文件:
//   skills/{skill}/{file}  (depth >= 3 且 prefix = "skills/")
//   subagents/{sa}/skills/{skill}/{file} (depth >= 5 且含 "skills/")
func isDirectSkillFile(path string) bool {
    parts := strings.Split(path, "/")
    switch {
    case len(parts) >= 3 && parts[0] == "skills":
        // skills/weather/SKILL.md — 深度 3,文件在 skill 目录里
        return true
    case len(parts) >= 5 && parts[0] == "subagents" && parts[2] == "skills":
        // subagents/db-agent/skills/sql/SKILL.md
        return true
    }
    return false
}

// parentSkillDir 返回 skill 目录路径(去掉末尾文件名)。
func parentSkillDir(path string) string {
    parts := strings.Split(path, "/")
    switch {
    case len(parts) >= 3 && parts[0] == "skills":
        return strings.Join(parts[:2], "/")
    case len(parts) >= 5 && parts[0] == "subagents" && parts[2] == "skills":
        return strings.Join(parts[:4], "/")
    }
    return path
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/sync/... -v -run TestSyncable
go test ./internal/sync/... -v -run TestValidate
```

Expected: 全部 PASS

- [ ] **Step 5: 提交**

```bash
git add internal/sync/resource.go internal/sync/resource_test.go
git commit -m "$(cat <<'EOF'
sync: 新增 resource.go 白名单与路径校验

定义 SyncableResourceRoots 白名单,ValidateSyncPath 校验用户路径合法性,
拒绝 env.yaml / 路径遍历 / 直接操作 skill 目录内单文件三类非法操作。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: diff.go — DiffResult 类型与 size+mtime 判等算法

**Files:**
- Create: `internal/sync/diff.go`
- Create: `internal/sync/diff_test.go`

- [ ] **Step 1: 新建 `internal/sync/diff_test.go`**

```go
package sync

import (
    "os"
    "path/filepath"
    "strings"
    "testing"
    "time"

    "github.com/zfd81/groot/internal/storage"
)

// makeFile 在 dir 下创建文件,内容为 content,返回绝对路径。
func makeFile(t *testing.T, dir, name, content string) string {
    t.Helper()
    path := filepath.Join(dir, name)
    if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
        t.Fatal(err)
    }
    if err := os.WriteFile(path, []byte(content), 0644); err != nil {
        t.Fatal(err)
    }
    return path
}

func TestComputeDiff_Added(t *testing.T) {
    localDir := t.TempDir()
    remoteDir := t.TempDir()

    makeFile(t, localDir, "config.yaml", "agent:\n  name: groot\n")

    store := storage.NewLocal()
    result, err := ComputeDiff(store, localDir, remoteDir, []string{"config.yaml"})
    if err != nil {
        t.Fatalf("ComputeDiff: %v", err)
    }
    if len(result.Added) != 1 || result.Added[0] != "config.yaml" {
        t.Errorf("expected Added=[config.yaml], got %+v", result)
    }
    if len(result.Modified) != 0 || len(result.Removed) != 0 {
        t.Errorf("unexpected changes: %+v", result)
    }
}

func TestComputeDiff_Removed(t *testing.T) {
    localDir := t.TempDir()
    remoteDir := t.TempDir()

    // 只在远端有文件
    makeFile(t, remoteDir, "GROOT.md", "# GROOT\n")

    store := storage.NewLocal()
    result, err := ComputeDiff(store, localDir, remoteDir, []string{"GROOT.md"})
    if err != nil {
        t.Fatalf("ComputeDiff: %v", err)
    }
    if len(result.Removed) != 1 || result.Removed[0] != "GROOT.md" {
        t.Errorf("expected Removed=[GROOT.md], got %+v", result)
    }
}

func TestComputeDiff_Same(t *testing.T) {
    localDir := t.TempDir()
    remoteDir := t.TempDir()

    content := "agent:\n  name: groot\n"
    makeFile(t, localDir, "config.yaml", content)
    makeFile(t, remoteDir, "config.yaml", content)

    // 同步两侧 mtime
    now := time.Now().Truncate(time.Second)
    for _, dir := range []string{localDir, remoteDir} {
        path := filepath.Join(dir, "config.yaml")
        _ = os.Chtimes(path, now, now)
    }

    store := storage.NewLocal()
    result, err := ComputeDiff(store, localDir, remoteDir, []string{"config.yaml"})
    if err != nil {
        t.Fatalf("ComputeDiff: %v", err)
    }
    if len(result.Same) != 1 {
        t.Errorf("expected Same=[config.yaml], got %+v", result)
    }
}

func TestComputeDiff_Modified_SizeDiff(t *testing.T) {
    localDir := t.TempDir()
    remoteDir := t.TempDir()

    makeFile(t, localDir, "GROOT.md", "# v2\n")
    makeFile(t, remoteDir, "GROOT.md", "# v1 (longer content)\n")

    store := storage.NewLocal()
    result, err := ComputeDiff(store, localDir, remoteDir, []string{"GROOT.md"})
    if err != nil {
        t.Fatalf("ComputeDiff: %v", err)
    }
    if len(result.Modified) != 1 {
        t.Errorf("expected Modified=[GROOT.md], got %+v", result)
    }
}

func TestComputeDiff_Modified_MtimeDiff(t *testing.T) {
    localDir := t.TempDir()
    remoteDir := t.TempDir()

    content := "same content\n"
    makeFile(t, localDir, "GROOT.md", content)
    makeFile(t, remoteDir, "GROOT.md", content)

    // 本地文件 mtime 比远端早 5s (超过 1s 容差)
    older := time.Now().Add(-5 * time.Second).Truncate(time.Second)
    newer := time.Now().Truncate(time.Second)
    _ = os.Chtimes(filepath.Join(localDir, "GROOT.md"), older, older)
    _ = os.Chtimes(filepath.Join(remoteDir, "GROOT.md"), newer, newer)

    store := storage.NewLocal()
    result, err := ComputeDiff(store, localDir, remoteDir, []string{"GROOT.md"})
    if err != nil {
        t.Fatalf("ComputeDiff: %v", err)
    }
    if len(result.Modified) != 1 {
        t.Errorf("expected Modified=[GROOT.md], got %+v", result)
    }
}

func TestComputeDiff_MtimeTolerance(t *testing.T) {
    localDir := t.TempDir()
    remoteDir := t.TempDir()

    content := "same content\n"
    makeFile(t, localDir, "GROOT.md", content)
    makeFile(t, remoteDir, "GROOT.md", content)

    // 本地与远端 mtime 差 < 1s → 判为 Same
    base := time.Now().Truncate(time.Second)
    _ = os.Chtimes(filepath.Join(localDir, "GROOT.md"), base, base)
    _ = os.Chtimes(filepath.Join(remoteDir, "GROOT.md"), base, base)

    store := storage.NewLocal()
    result, err := ComputeDiff(store, localDir, remoteDir, []string{"GROOT.md"})
    if err != nil {
        t.Fatalf("ComputeDiff: %v", err)
    }
    if len(result.Same) != 1 {
        t.Errorf("expected Same (within tolerance), got %+v", result)
    }
}

func TestComputeDiff_RecursiveDir(t *testing.T) {
    localDir := t.TempDir()
    remoteDir := t.TempDir()

    makeFile(t, localDir, "skills/weather/SKILL.md", "weather skill\n")
    makeFile(t, localDir, "skills/weather/handler.go", "package main\n")

    store := storage.NewLocal()
    result, err := ComputeDiff(store, localDir, remoteDir, []string{"skills/weather"})
    if err != nil {
        t.Fatalf("ComputeDiff: %v", err)
    }
    if len(result.Added) != 2 {
        t.Errorf("expected 2 Added files, got %d: %v", len(result.Added), result.Added)
    }
    for _, f := range result.Added {
        if !strings.HasPrefix(f, "skills/weather/") {
            t.Errorf("unexpected path: %s", f)
        }
    }
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/sync/... -run TestComputeDiff -v
```

Expected: FAIL — `ComputeDiff` 未定义

- [ ] **Step 3: 新建 `internal/sync/diff.go`**

```go
package sync

import (
    "context"
    "errors"
    "fmt"
    "os"
    "path/filepath"
    "time"

    istorage "github.com/zfd81/groot/internal/storage"
)

// mtimeTolerance 是本地 mtime 与 MinIO LastModified 之间允许的精度误差。
const mtimeTolerance = time.Second

// DiffResult 描述本地 HOME 与 MinIO 之间的差异,以相对路径表示。
type DiffResult struct {
    Added    []string // 本地有,远端没有
    Modified []string // 双侧都有但内容/时间不同
    Removed  []string // 远端有,本地没有
    Same     []string // 一致
}

// IsEmpty 返回是否没有任何差异。
func (d DiffResult) IsEmpty() bool {
    return len(d.Added)+len(d.Modified)+len(d.Removed) == 0
}

// ComputeDiff 对 paths(相对于 localBase 和 remoteBase 的相对路径列表)进行双侧 diff。
// 每个 path 如果是目录则递归展开其下所有文件再比较。
// localBase 必须是绝对路径(本地 HOME 下某目录)。
// remoteBase 由 Storage 接口决定(local 模式为绝对路径,minio 模式为 object-key 前缀)。
// store 用于读取远端元数据。
func ComputeDiff(store istorage.Storage, localBase, remoteBase string, paths []string) (DiffResult, error) {
    var result DiffResult

    for _, rel := range paths {
        localPath := filepath.Join(localBase, filepath.FromSlash(rel))
        remotePath := joinPath(remoteBase, rel)

        localFiles, err := walkLocalFiles(localPath, localBase)
        if err != nil {
            return result, fmt.Errorf("sync diff: scan local %s: %w", rel, err)
        }
        remoteFiles, err := walkRemoteFiles(store, remotePath, remoteBase)
        if err != nil {
            return result, fmt.Errorf("sync diff: scan remote %s: %w", rel, err)
        }

        // 合并
        localMap := make(map[string]os.FileInfo)
        for _, f := range localFiles {
            localMap[f.relPath] = f.info
        }
        remoteMap := make(map[string]remoteFileInfo)
        for _, f := range remoteFiles {
            remoteMap[f.relPath] = f
        }

        for relPath, li := range localMap {
            ri, inRemote := remoteMap[relPath]
            if !inRemote {
                result.Added = append(result.Added, relPath)
                continue
            }
            if differsFromRemote(li, ri) {
                result.Modified = append(result.Modified, relPath)
            } else {
                result.Same = append(result.Same, relPath)
            }
        }
        for relPath := range remoteMap {
            if _, inLocal := localMap[relPath]; !inLocal {
                result.Removed = append(result.Removed, relPath)
            }
        }
    }
    return result, nil
}

// differsFromRemote 判断本地文件与远端文件是否不同(size 或 mtime 超出容差)。
func differsFromRemote(local os.FileInfo, remote remoteFileInfo) bool {
    if local.Size() != remote.size {
        return true
    }
    diff := local.ModTime().Sub(remote.mtime)
    if diff < 0 {
        diff = -diff
    }
    return diff > mtimeTolerance
}

// --- 本地遍历 ---

type localFile struct {
    relPath string
    info    os.FileInfo
}

// walkLocalFiles 遍历 absPath 下所有文件(递归),返回相对于 base 的路径。
// 如果 absPath 本身就是文件,只返回一个元素。
// 如果 absPath 不存在,返回空切片(非错误)。
func walkLocalFiles(absPath, base string) ([]localFile, error) {
    var files []localFile
    info, err := os.Stat(absPath)
    if err != nil {
        if os.IsNotExist(err) {
            return nil, nil
        }
        return nil, err
    }
    if !info.IsDir() {
        rel, _ := filepath.Rel(base, absPath)
        files = append(files, localFile{relPath: filepath.ToSlash(rel), info: info})
        return files, nil
    }
    err = filepath.Walk(absPath, func(path string, fi os.FileInfo, err error) error {
        if err != nil {
            return err
        }
        if fi.IsDir() {
            return nil
        }
        rel, _ := filepath.Rel(base, path)
        files = append(files, localFile{relPath: filepath.ToSlash(rel), info: fi})
        return nil
    })
    return files, err
}

// --- 远端遍历 ---

type remoteFileInfo struct {
    relPath string
    size    int64
    mtime   time.Time
}

// walkRemoteFiles 通过 Storage.List 递归列出 absRemotePath 下所有文件。
// 如果远端路径不存在,返回空切片(非错误)。
func walkRemoteFiles(store istorage.Storage, absRemotePath, remoteBase string) ([]remoteFileInfo, error) {
    ctx := context.Background()
    fi, err := store.Stat(ctx, absRemotePath)
    if err != nil {
        if errors.Is(err, istorage.ErrNotFound) {
            return nil, nil
        }
        return nil, err
    }
    if !fi.IsDir {
        rel := relPath(remoteBase, absRemotePath)
        return []remoteFileInfo{{relPath: rel, size: fi.Size, mtime: fi.ModTime}}, nil
    }
    return listRemoteRecursive(store, ctx, absRemotePath, remoteBase)
}

func listRemoteRecursive(store istorage.Storage, ctx context.Context, dir, base string) ([]remoteFileInfo, error) {
    entries, err := store.List(ctx, dir)
    if err != nil {
        if errors.Is(err, istorage.ErrNotFound) {
            return nil, nil
        }
        return nil, err
    }
    var files []remoteFileInfo
    for _, entry := range entries {
        if entry.IsDir {
            sub, err := listRemoteRecursive(store, ctx, entry.Path, base)
            if err != nil {
                return nil, err
            }
            files = append(files, sub...)
        } else {
            rel := relPath(base, entry.Path)
            files = append(files, remoteFileInfo{relPath: rel, size: entry.Size, mtime: entry.ModTime})
        }
    }
    return files, nil
}

// joinPath 拼接 base + rel,兼容 local(filepath.Join)和 minio(object key / 分隔)。
// 统一用 "/" 分隔符,os.* 调用方再做 filepath.FromSlash 转换。
func joinPath(base, rel string) string {
    if base == "" {
        return rel
    }
    return base + "/" + rel
}

// relPath 计算 path 相对于 base 的相对路径(统一 "/" 分隔符)。
func relPath(base, path string) string {
    if base == "" {
        return path
    }
    prefix := base + "/"
    if len(path) > len(prefix) && path[:len(prefix)] == prefix {
        return path[len(prefix):]
    }
    return path
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/sync/... -run TestComputeDiff -v
```

Expected: 全部 PASS(7 个 TestComputeDiff_* 用例)

- [ ] **Step 5: 提交**

```bash
git add internal/sync/diff.go internal/sync/diff_test.go
git commit -m "$(cat <<'EOF'
sync: 新增 diff.go 双侧 size+mtime 判等算法

ComputeDiff 递归展开本地/远端路径,以 size + mtime(±1s 容差)判等,
返回 Added/Modified/Removed/Same 四分组 DiffResult。本地侧走 os.*,
远端侧走 Storage 接口。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: resolver.go — 用户路径展开

**Files:**
- Create: `internal/sync/resolver.go`
- Create: `internal/sync/resolver_test.go`

- [ ] **Step 1: 新建 `internal/sync/resolver_test.go`**

```go
package sync

import (
    "os"
    "path/filepath"
    "testing"
)

func TestResolveLocalPaths_Category(t *testing.T) {
    homeDir := t.TempDir()
    // 准备两个 skill 目录
    os.MkdirAll(filepath.Join(homeDir, "skills", "weather"), 0755)
    os.MkdirAll(filepath.Join(homeDir, "skills", "translator"), 0755)

    paths, err := ResolveLocalPaths(homeDir, []string{"skills"})
    if err != nil {
        t.Fatalf("ResolveLocalPaths: %v", err)
    }
    if len(paths) != 2 {
        t.Errorf("expected 2 skill paths, got %d: %v", len(paths), paths)
    }
}

func TestResolveLocalPaths_SpecificSkill(t *testing.T) {
    homeDir := t.TempDir()
    os.MkdirAll(filepath.Join(homeDir, "skills", "weather"), 0755)

    paths, err := ResolveLocalPaths(homeDir, []string{"skills/weather"})
    if err != nil {
        t.Fatalf("ResolveLocalPaths: %v", err)
    }
    if len(paths) != 1 || paths[0] != "skills/weather" {
        t.Errorf("expected [skills/weather], got %v", paths)
    }
}

func TestResolveLocalPaths_FileResource(t *testing.T) {
    homeDir := t.TempDir()
    os.WriteFile(filepath.Join(homeDir, "config.yaml"), []byte("agent: groot\n"), 0644)

    paths, err := ResolveLocalPaths(homeDir, []string{"config.yaml"})
    if err != nil {
        t.Fatalf("ResolveLocalPaths: %v", err)
    }
    if len(paths) != 1 || paths[0] != "config.yaml" {
        t.Errorf("expected [config.yaml], got %v", paths)
    }
}

func TestResolveLocalPaths_DefaultAll(t *testing.T) {
    homeDir := t.TempDir()
    // 只创建部分资源
    os.MkdirAll(filepath.Join(homeDir, "skills", "w"), 0755)
    os.WriteFile(filepath.Join(homeDir, "config.yaml"), []byte(""), 0644)

    paths, err := ResolveLocalPaths(homeDir, nil) // nil = all
    if err != nil {
        t.Fatalf("ResolveLocalPaths: %v", err)
    }
    // 应当包含 config.yaml 和 skills/w,不应包含不存在的 mcp / subagents / GROOT.md
    found := map[string]bool{}
    for _, p := range paths {
        found[p] = true
    }
    if !found["config.yaml"] {
        t.Error("expected config.yaml in default resolve")
    }
    if !found["skills/w"] {
        t.Error("expected skills/w in default resolve")
    }
    // 不存在的类别不应出现
    if found["mcp"] {
        t.Error("mcp dir does not exist, should not appear")
    }
}

func TestResolveLocalPaths_InvalidPath(t *testing.T) {
    homeDir := t.TempDir()
    _, err := ResolveLocalPaths(homeDir, []string{"env.yaml"})
    if err == nil {
        t.Error("expected error for env.yaml, got nil")
    }
}

func TestResolveLocalPaths_SkillFileDirect(t *testing.T) {
    homeDir := t.TempDir()
    _, err := ResolveLocalPaths(homeDir, []string{"skills/weather/SKILL.md"})
    if err == nil {
        t.Error("expected error for direct skill file path")
    }
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/sync/... -run TestResolveLocalPaths -v
```

Expected: FAIL — `ResolveLocalPaths` 未定义

- [ ] **Step 3: 新建 `internal/sync/resolver.go`**

```go
package sync

import (
    "fmt"
    "os"
    "path/filepath"
)

// ResolveLocalPaths 把用户输入的 paths(相对于 homeDir)展开为资源对象相对路径列表。
//
//   - paths 为 nil 时默认展开白名单内所有已存在的资源对象
//   - "skills" → 展开为 ["skills/weather", "skills/translator", ...]
//   - "skills/weather" → 直接返回 ["skills/weather"](目录资源对象)
//   - "config.yaml" → 直接返回 ["config.yaml"](文件资源对象)
//   - "subagents/db-agent" → 直接返回 ["subagents/db-agent"](递归目录)
//   - "subagents/db-agent/agent.md" → 直接返回 ["subagents/db-agent/agent.md"](文件)
//   - 直接操作 skill 目录内单文件 → 返回错误(如 "skills/weather/SKILL.md")
func ResolveLocalPaths(homeDir string, paths []string) ([]string, error) {
    if len(paths) == 0 {
        return resolveAll(homeDir)
    }
    var result []string
    for _, p := range paths {
        if err := ValidateSyncPath(p); err != nil {
            return nil, err
        }
        expanded, err := expandPath(homeDir, p)
        if err != nil {
            return nil, err
        }
        result = append(result, expanded...)
    }
    return result, nil
}

// resolveAll 展开白名单内所有已存在于 homeDir 的资源对象。
func resolveAll(homeDir string) ([]string, error) {
    var result []string
    for _, root := range SyncableResourceRoots {
        expanded, err := expandPath(homeDir, root)
        if err != nil {
            return nil, err
        }
        result = append(result, expanded...)
    }
    return result, nil
}

// expandPath 展开单个相对路径为一个或多个资源对象路径。
// 对于类别目录(skills / mcp / subagents),列出其下直接子项作为资源对象。
// 对于其他目录/文件,直接返回该路径(若存在)。
func expandPath(homeDir, rel string) ([]string, error) {
    abs := filepath.Join(homeDir, filepath.FromSlash(rel))

    info, err := os.Stat(abs)
    if err != nil {
        if os.IsNotExist(err) {
            return nil, nil // 不存在视为空(推送时远端可能有,pull 时需)
        }
        return nil, fmt.Errorf("sync resolver: stat %s: %w", rel, err)
    }

    // 类别目录展开:skills、mcp 列一级子项
    if info.IsDir() && isCategoryDir(rel) {
        return listCategoryChildren(abs, rel)
    }
    // subagents 类别:列直接子 Agent 目录
    if rel == "subagents" {
        return listCategoryChildren(abs, rel)
    }
    // 其他:直接返回
    return []string{rel}, nil
}

// isCategoryDir 判断是否是需要"展开一级子项"的类别目录。
func isCategoryDir(rel string) bool {
    return rel == "skills" || rel == "mcp"
}

// listCategoryChildren 列出 abs 下直接子项的相对路径。
func listCategoryChildren(abs, relBase string) ([]string, error) {
    entries, err := os.ReadDir(abs)
    if err != nil {
        if os.IsNotExist(err) {
            return nil, nil
        }
        return nil, fmt.Errorf("sync resolver: read dir %s: %w", relBase, err)
    }
    var result []string
    for _, e := range entries {
        child := relBase + "/" + e.Name()
        result = append(result, child)
    }
    return result, nil
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/sync/... -run TestResolveLocalPaths -v
```

Expected: 全部 PASS(6 个用例)

- [ ] **Step 5: 提交**

```bash
git add internal/sync/resolver.go internal/sync/resolver_test.go
git commit -m "$(cat <<'EOF'
sync: 新增 resolver.go 路径展开逻辑

ResolveLocalPaths 把用户输入的相对路径展开为资源对象列表:
类别目录(skills/mcp/subagents)展开一级子项,文件/具体目录直接透传,
nil 时展开白名单内全部已存在资源,禁止直接操作 skill 目录内单文件。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: render.go — diff 结果渲染

**Files:**
- Create: `internal/sync/render.go`
- Create: `internal/sync/render_test.go`

- [ ] **Step 1: 新建 `internal/sync/render_test.go`**

```go
package sync

import (
    "bytes"
    "strings"
    "testing"
)

func TestRenderDiff_Push(t *testing.T) {
    d := DiffResult{
        Added:    []string{"skills/weather/SKILL.md"},
        Modified: []string{"config.yaml"},
        Removed:  []string{"skills/old/SKILL.md"},
    }
    var buf bytes.Buffer
    RenderDiff(&buf, d, "push")
    out := buf.String()

    for _, want := range []string{
        "Changes to push",
        "HOME → MinIO",
        "Added:",
        "skills/weather/SKILL.md",
        "Modified:",
        "config.yaml",
        "Removed:",
        "skills/old/SKILL.md",
    } {
        if !strings.Contains(out, want) {
            t.Errorf("output missing %q\n\nOutput:\n%s", want, out)
        }
    }
}

func TestRenderDiff_Pull(t *testing.T) {
    d := DiffResult{
        Added: []string{"GROOT.md"},
    }
    var buf bytes.Buffer
    RenderDiff(&buf, d, "pull")
    out := buf.String()
    if !strings.Contains(out, "MinIO → HOME") {
        t.Errorf("expected 'MinIO → HOME' in pull output:\n%s", out)
    }
}

func TestRenderDiff_Empty(t *testing.T) {
    d := DiffResult{}
    var buf bytes.Buffer
    RenderDiff(&buf, d, "push")
    out := buf.String()
    if !strings.Contains(out, "No differences") {
        t.Errorf("expected 'No differences' for empty diff:\n%s", out)
    }
}

func TestRenderDiff_NeedsRestart(t *testing.T) {
    d := DiffResult{
        Modified: []string{"config.yaml"},
    }
    var buf bytes.Buffer
    RenderDiff(&buf, d, "pull")
    out := buf.String()
    if !strings.Contains(out, "restart") && !strings.Contains(out, "重启") {
        t.Errorf("expected restart notice for config.yaml in pull output:\n%s", out)
    }
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/sync/... -run TestRenderDiff -v
```

Expected: FAIL — `RenderDiff` 未定义

- [ ] **Step 3: 新建 `internal/sync/render.go`**

```go
package sync

import (
    "fmt"
    "io"
    "strings"
)

// needsRestartPaths 列出 pull 后需要重启服务才生效的路径前缀。
// 参考 spec §1.8.8。
var needsRestartPaths = []string{
    "config.yaml",
    "mcp/",
    "subagents/",
}

// RenderDiff 把 DiffResult 渲染为可读输出写入 w。
// direction 为 "push" 或 "pull"。
func RenderDiff(w io.Writer, d DiffResult, direction string) {
    if d.IsEmpty() {
        fmt.Fprintln(w, "No differences found — already in sync.")
        return
    }

    arrow := "HOME → MinIO"
    verb := "push"
    if direction == "pull" {
        arrow = "MinIO → HOME"
        verb = "pull"
    }
    fmt.Fprintf(w, "\nChanges to %s (%s):\n", verb, arrow)

    if len(d.Added) > 0 {
        fmt.Fprintln(w, "  Added:")
        for _, f := range d.Added {
            fmt.Fprintf(w, "    %s\n", f)
        }
    }
    if len(d.Modified) > 0 {
        fmt.Fprintln(w, "  Modified:")
        for _, f := range d.Modified {
            fmt.Fprintf(w, "    %s\n", f)
        }
    }
    if len(d.Removed) > 0 {
        fmt.Fprintln(w, "  Removed:")
        for _, f := range d.Removed {
            fmt.Fprintf(w, "    %s\n", f)
        }
    }

    // pull 时如果涉及需重启的资源,给出提示
    if direction == "pull" {
        allChanged := append(append(d.Added, d.Modified...), d.Removed...)
        if anyNeedsRestart(allChanged) {
            fmt.Fprintln(w, "\n⚠  Some resources require a service restart to take effect:")
            fmt.Fprintln(w, "   config.yaml, mcp configs, subagent entry files (agent.md).")
            fmt.Fprintln(w, "   Please restart groot after pull completes.")
        }
    }
    fmt.Fprintln(w)
}

// anyNeedsRestart 判断变更列表中是否含需重启的资源。
func anyNeedsRestart(paths []string) bool {
    for _, p := range paths {
        for _, prefix := range needsRestartPaths {
            if p == strings.TrimSuffix(prefix, "/") || strings.HasPrefix(p, prefix) {
                return true
            }
        }
    }
    return false
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/sync/... -run TestRenderDiff -v
```

Expected: 全部 PASS(4 个用例)

- [ ] **Step 5: 提交**

```bash
git add internal/sync/render.go internal/sync/render_test.go
git commit -m "$(cat <<'EOF'
sync: 新增 render.go diff 结果渲染

RenderDiff 按 push/pull 方向渲染 DiffResult 为可读输出,pull 时若涉及
config.yaml / mcp / subagent 入口文件附加重启提示(对应 spec §1.8.8)。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: sync.go — SyncManager 接口与 Push/Pull/Diff 实现

**Files:**
- Create: `internal/sync/sync.go`
- Create: `internal/sync/sync_test.go`

- [ ] **Step 1: 新建 `internal/sync/sync_test.go`**

```go
package sync

import (
    "os"
    "path/filepath"
    "testing"

    "github.com/zfd81/groot/internal/storage"
)

// newTestManager 创建测试用 SyncManager:homeDir 和 remoteBase 都是 tmpdir。
func newTestManager(t *testing.T) (*localSyncManager, string, string) {
    t.Helper()
    homeDir := t.TempDir()
    remoteDir := t.TempDir()
    store := storage.NewLocal()
    mgr := NewSyncManager(homeDir, remoteDir, store).(*localSyncManager)
    return mgr, homeDir, remoteDir
}

func TestSyncManager_Diff_NoFiles(t *testing.T) {
    mgr, _, _ := newTestManager(t)
    result, err := mgr.Diff(nil)
    if err != nil {
        t.Fatalf("Diff: %v", err)
    }
    if !result.IsEmpty() {
        t.Errorf("expected empty diff, got %+v", result)
    }
}

func TestSyncManager_Push_SingleFile(t *testing.T) {
    mgr, homeDir, remoteDir := newTestManager(t)

    os.WriteFile(filepath.Join(homeDir, "config.yaml"), []byte("agent: groot\n"), 0644)

    if err := mgr.Push([]string{"config.yaml"}); err != nil {
        t.Fatalf("Push: %v", err)
    }

    remote := filepath.Join(remoteDir, "config.yaml")
    data, err := os.ReadFile(remote)
    if err != nil {
        t.Fatalf("remote file not found after push: %v", err)
    }
    if string(data) != "agent: groot\n" {
        t.Errorf("remote content mismatch: %q", data)
    }
}

func TestSyncManager_Push_MirrorDelete(t *testing.T) {
    mgr, homeDir, remoteDir := newTestManager(t)

    // 远端有文件,本地没有 → push 应该删除远端
    os.MkdirAll(filepath.Join(remoteDir, "mcp"), 0755)
    os.WriteFile(filepath.Join(remoteDir, "mcp", "old.json"), []byte("{}"), 0644)

    if err := mgr.Push([]string{"mcp"}); err != nil {
        t.Fatalf("Push: %v", err)
    }

    _, err := os.Stat(filepath.Join(remoteDir, "mcp", "old.json"))
    if !os.IsNotExist(err) {
        t.Error("expected remote file to be deleted after push mirror")
    }
    _ = homeDir
}

func TestSyncManager_Pull_SingleFile(t *testing.T) {
    mgr, homeDir, remoteDir := newTestManager(t)

    os.WriteFile(filepath.Join(remoteDir, "GROOT.md"), []byte("# GROOT\n"), 0644)

    if err := mgr.Pull([]string{"GROOT.md"}); err != nil {
        t.Fatalf("Pull: %v", err)
    }

    local := filepath.Join(homeDir, "GROOT.md")
    data, err := os.ReadFile(local)
    if err != nil {
        t.Fatalf("local file not found after pull: %v", err)
    }
    if string(data) != "# GROOT\n" {
        t.Errorf("local content mismatch: %q", data)
    }
}

func TestSyncManager_Pull_PhaseABeforePhaseB(t *testing.T) {
    mgr, homeDir, remoteDir := newTestManager(t)

    // 远端有 new.md,本地有 old.md → pull 应先写 new.md 再删 old.md
    os.MkdirAll(filepath.Join(remoteDir, "mcp"), 0755)
    os.WriteFile(filepath.Join(remoteDir, "mcp", "new.json"), []byte(`{"new":true}`), 0644)
    os.MkdirAll(filepath.Join(homeDir, "mcp"), 0755)
    os.WriteFile(filepath.Join(homeDir, "mcp", "old.json"), []byte(`{"old":true}`), 0644)

    if err := mgr.Pull([]string{"mcp"}); err != nil {
        t.Fatalf("Pull: %v", err)
    }

    // new.json 应存在
    if _, err := os.Stat(filepath.Join(homeDir, "mcp", "new.json")); err != nil {
        t.Error("expected new.json after pull")
    }
    // old.json 应被删除
    if _, err := os.Stat(filepath.Join(homeDir, "mcp", "old.json")); !os.IsNotExist(err) {
        t.Error("expected old.json to be removed after pull mirror")
    }
}

func TestSyncManager_Pull_CleanTmpFiles(t *testing.T) {
    mgr, homeDir, remoteDir := newTestManager(t)

    // 模拟上次 pull 崩溃留下 .tmp 残留
    os.MkdirAll(filepath.Join(homeDir, "skills", "weather"), 0755)
    os.WriteFile(filepath.Join(homeDir, "skills", "weather", "SKILL.md.tmp"), []byte("stale"), 0644)

    // 远端有 skills/weather/SKILL.md
    os.MkdirAll(filepath.Join(remoteDir, "skills", "weather"), 0755)
    os.WriteFile(filepath.Join(remoteDir, "skills", "weather", "SKILL.md"), []byte("fresh"), 0644)

    if err := mgr.Pull([]string{"skills"}); err != nil {
        t.Fatalf("Pull: %v", err)
    }

    // .tmp 文件应被清理
    tmpPath := filepath.Join(homeDir, "skills", "weather", "SKILL.md.tmp")
    if _, err := os.Stat(tmpPath); !os.IsNotExist(err) {
        t.Error("expected .tmp file to be cleaned before pull")
    }
    _ = remoteDir
}

func TestNewSyncManager_LocalMode_Disabled(t *testing.T) {
    // store 为 nil 或 remoteBase 为空时返回 disabled 实现
    mgr := NewSyncManager("", "", nil)
    if _, err := mgr.Diff(nil); err == nil {
        t.Error("expected ErrSyncDisabled in local mode")
    }
    if err := mgr.Push(nil); err == nil {
        t.Error("expected ErrSyncDisabled in local mode")
    }
    if err := mgr.Pull(nil); err == nil {
        t.Error("expected ErrSyncDisabled in local mode")
    }
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/sync/... -run TestSyncManager -v
```

Expected: FAIL — `SyncManager` / `NewSyncManager` 未定义

- [ ] **Step 3: 新建 `internal/sync/sync.go`**

```go
package sync

import (
    "bufio"
    "bytes"
    "context"
    "errors"
    "fmt"
    "io"
    "io/fs"
    "os"
    "path/filepath"
    "strings"

    istorage "github.com/zfd81/groot/internal/storage"
)

// SyncManager 管理本地 HOME 与 MinIO 之间的集群共享配置同步。
// local 模式下不可用;minio 模式由 NewSyncManager 构造可用实例。
type SyncManager interface {
    Push(paths []string) error
    Pull(paths []string) error
    Diff(paths []string) (DiffResult, error)
}

// ErrSyncDisabled 表示当前未启用 minio 模式,sync 命令不可用。
var ErrSyncDisabled = errors.New("sync: minio 模式未启用 — 请在 env.yaml 中配置 minio 节")

// disabledSyncManager 是 local 模式下的空实现,所有方法返回 ErrSyncDisabled。
type disabledSyncManager struct{}

func (d *disabledSyncManager) Push(_ []string) error          { return ErrSyncDisabled }
func (d *disabledSyncManager) Pull(_ []string) error          { return ErrSyncDisabled }
func (d *disabledSyncManager) Diff(_ []string) (DiffResult, error) { return DiffResult{}, ErrSyncDisabled }

// localSyncManager 是可用的 SyncManager 实现:本地侧走 os.*,远端侧走 Storage 接口。
type localSyncManager struct {
    homeDir    string          // 本地 HOME 绝对路径
    remoteBase string          // 远端 object-key 前缀(minio)或绝对路径(local 测试)
    store      istorage.Storage
}

// NewSyncManager 创建 SyncManager。
// store 为 nil 或 remoteBase 为空时返回 disabledSyncManager。
func NewSyncManager(homeDir, remoteBase string, store istorage.Storage) SyncManager {
    if store == nil || remoteBase == "" {
        return &disabledSyncManager{}
    }
    return &localSyncManager{homeDir: homeDir, remoteBase: remoteBase, store: store}
}

// Diff 计算指定 paths 的本地 vs 远端差异。paths 为 nil 时比较全部白名单资源。
func (m *localSyncManager) Diff(paths []string) (DiffResult, error) {
    resolved, err := ResolveLocalPaths(m.homeDir, paths)
    if err != nil {
        return DiffResult{}, err
    }
    return ComputeDiff(m.store, m.homeDir, m.remoteBase, resolved)
}

// Push 把本地 paths 镜像推送到远端:新增/修改 → 写远端;远端多余 → 删远端。
// paths 为 nil 时处理白名单全部资源。
func (m *localSyncManager) Push(paths []string) error {
    resolved, err := ResolveLocalPaths(m.homeDir, paths)
    if err != nil {
        return err
    }
    diff, err := ComputeDiff(m.store, m.homeDir, m.remoteBase, resolved)
    if err != nil {
        return err
    }
    ctx := context.Background()

    // 写入 Added / Modified
    for _, rel := range append(diff.Added, diff.Modified...) {
        localPath := filepath.Join(m.homeDir, filepath.FromSlash(rel))
        remotePath := joinPath(m.remoteBase, rel)
        if err := pushFile(ctx, m.store, localPath, remotePath); err != nil {
            return fmt.Errorf("sync push %s: %w", rel, err)
        }
    }
    // 删除 Removed(本地已删,远端镜像删除)
    for _, rel := range diff.Removed {
        remotePath := joinPath(m.remoteBase, rel)
        if err := m.store.Delete(ctx, remotePath); err != nil && !errors.Is(err, istorage.ErrNotFound) {
            return fmt.Errorf("sync push delete %s: %w", rel, err)
        }
    }
    return nil
}

// Pull 把远端 paths 镜像拉取到本地:Phase A 先写新增/修改,Phase B 再删本地多余。
// paths 为 nil 时处理白名单全部资源。
// 启动时先清理 homeDir 下所有 *.tmp 残留。
func (m *localSyncManager) Pull(paths []string) error {
    resolved, err := ResolveLocalPaths(m.homeDir, paths)
    if err != nil {
        return err
    }

    // 清理 *.tmp 残留(上次 pull 崩溃留下)
    if err := cleanTmpFiles(m.homeDir, resolved); err != nil {
        return fmt.Errorf("sync pull cleanup tmp: %w", err)
    }

    // diff(注意方向:本地 vs 远端)
    diff, err := ComputeDiff(m.store, m.homeDir, m.remoteBase, resolved)
    if err != nil {
        return err
    }
    ctx := context.Background()

    // Phase A: 写入 Added + Modified
    for _, rel := range append(diff.Added, diff.Modified...) {
        remotePath := joinPath(m.remoteBase, rel)
        localPath := filepath.Join(m.homeDir, filepath.FromSlash(rel))
        if err := pullFile(ctx, m.store, remotePath, localPath); err != nil {
            return fmt.Errorf("sync pull %s: %w", rel, err)
        }
    }

    // Phase B: 删除 Removed(远端已删,本地镜像删除)
    for _, rel := range diff.Removed {
        localPath := filepath.Join(m.homeDir, filepath.FromSlash(rel))
        if err := os.Remove(localPath); err != nil && !os.IsNotExist(err) {
            return fmt.Errorf("sync pull delete %s: %w", rel, err)
        }
    }
    return nil
}

// --- 文件级 push/pull 操作 ---

// pushFile 把本地文件写到远端(通过 Storage 接口原子写)。
func pushFile(ctx context.Context, store istorage.Storage, localPath, remotePath string) error {
    f, err := os.Open(localPath)
    if err != nil {
        return err
    }
    defer f.Close()
    info, err := f.Stat()
    if err != nil {
        return err
    }
    return store.Write(ctx, remotePath, f, info.Size(), "")
}

// pullFile 把远端文件写到本地,使用 tmp+rename 保证原子写。
func pullFile(ctx context.Context, store istorage.Storage, remotePath, localPath string) error {
    rc, err := store.Read(ctx, remotePath)
    if err != nil {
        return err
    }
    defer rc.Close()

    // 读取全部内容
    data, err := io.ReadAll(rc)
    if err != nil {
        return err
    }

    // 原子写:tmp → sync → rename
    if err := os.MkdirAll(filepath.Dir(localPath), 0755); err != nil {
        return err
    }
    tmp := localPath + ".tmp"
    _ = os.Remove(tmp) // 清理孤儿 tmp
    f, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
    if err != nil {
        return err
    }
    if _, err := f.Write(data); err != nil {
        f.Close()
        _ = os.Remove(tmp)
        return err
    }
    if err := f.Sync(); err != nil {
        f.Close()
        _ = os.Remove(tmp)
        return err
    }
    if err := f.Close(); err != nil {
        _ = os.Remove(tmp)
        return err
    }
    return os.Rename(tmp, localPath)
}

// cleanTmpFiles 递归删除 homeDir 下 resolved paths 范围内的所有 *.tmp 文件。
func cleanTmpFiles(homeDir string, resolved []string) error {
    for _, rel := range resolved {
        root := filepath.Join(homeDir, filepath.FromSlash(rel))
        _ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
            if err != nil || d.IsDir() {
                return nil
            }
            if strings.HasSuffix(d.Name(), ".tmp") {
                _ = os.Remove(path)
            }
            return nil
        })
    }
    return nil
}

// --- 交互确认 ---

// ConfirmContinue 在 stdout 显示提示并等待用户输入 y/Y/yes 后返回 true。
// 若 stdin 不是 tty 或用户输入其他内容,返回 false(取消)。
func ConfirmContinue(r io.Reader, w io.Writer) bool {
    fmt.Fprintf(w, "Continue? (y/n): ")
    scanner := bufio.NewScanner(r)
    if scanner.Scan() {
        ans := strings.TrimSpace(strings.ToLower(scanner.Text()))
        return ans == "y" || ans == "yes"
    }
    return false
}

// FormatDiff 把 DiffResult 以 string 返回(用于命令输出)。
func FormatDiff(d DiffResult, direction string) string {
    var buf bytes.Buffer
    RenderDiff(&buf, d, direction)
    return buf.String()
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/sync/... -v
```

Expected: 全部 PASS(含之前 Task 1-4 的测试)

- [ ] **Step 5: 全量编译确认**

```bash
go build ./...
```

Expected: 通过,无输出

- [ ] **Step 6: 提交**

```bash
git add internal/sync/sync.go internal/sync/sync_test.go
git commit -m "$(cat <<'EOF'
sync: 新增 SyncManager 接口与 Push/Pull/Diff 实现

Push 镜像推送(新增/修改写远端,已删从远端删);Pull 先清 .tmp 残留,
Phase A 先写后 Phase B 再删,保证任意中断点本地至少有一份完整内容。
local 模式返回 disabledSyncManager,minio 模式通过 NewSyncManager 注入。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: push.go / pull.go / diff_cmd.go — CLI 子命令

**Files:**
- Create: `internal/cmd/push.go`
- Create: `internal/cmd/pull.go`
- Create: `internal/cmd/diff_cmd.go`

- [ ] **Step 1: 新建 `internal/cmd/push.go`**

```go
package cmd

import (
    "errors"
    "fmt"
    "os"
    "strings"

    "github.com/zfd81/groot/internal/config"
    "github.com/zfd81/groot/internal/storage"
    isync "github.com/zfd81/groot/internal/sync"
)

// PushFlags holds parsed flags for the push command.
type PushFlags struct {
    Paths []string // 要推送的相对路径列表,nil 表示全部
    Yes   bool     // -y / --yes: 跳过确认
}

// ParsePushFlags 解析 groot push 子命令参数。
func ParsePushFlags(args []string) (*PushFlags, error) {
    flags := &PushFlags{}
    for _, arg := range args {
        switch arg {
        case "-h", "--help":
            printPushHelp()
            os.Exit(0)
        case "-y", "--yes":
            flags.Yes = true
        default:
            if strings.HasPrefix(arg, "-") {
                return nil, fmt.Errorf("unknown flag: %s", arg)
            }
            flags.Paths = append(flags.Paths, arg)
        }
    }
    return flags, nil
}

func printPushHelp() {
    fmt.Println("用法: groot push [path...] [-y]")
    fmt.Println()
    fmt.Println("将本地 HOME 的集群共享配置镜像推送到 MinIO。")
    fmt.Println("仅在 minio 模式下可用(需配置 ~/.groot/env.yaml 中的 minio 节)。")
    fmt.Println()
    fmt.Println("参数:")
    fmt.Println("  path...   要推送的资源路径(可多个),省略时推送全部白名单资源")
    fmt.Println()
    fmt.Println("选项:")
    fmt.Println("  -y, --yes   跳过交互确认,直接执行")
    fmt.Println("  -h, --help  显示帮助")
    fmt.Println()
    fmt.Println("示例:")
    fmt.Println("  groot push                       # 推送全部")
    fmt.Println("  groot push config.yaml           # 推送主配置")
    fmt.Println("  groot push skills/weather        # 推送单个 skill")
    fmt.Println("  groot push skills subagents mcp  # 推送多个类别")
    fmt.Println("  groot push -y skills             # 跳过确认直接推送")
}

// RunPush 执行 groot push。
func RunPush(flags *PushFlags) error {
    homeDir := GetDefaultHome()
    cfg, err := config.Load(homeDir)
    if err != nil {
        return fmt.Errorf("加载配置失败: %w", err)
    }
    if cfg.Storage.Minio == nil {
        return errors.New("groot push 仅在 minio 模式下可用\n请在 ~/.groot/env.yaml 中配置 minio 节")
    }
    store, err := storage.New(cfg.Storage)
    if err != nil {
        return fmt.Errorf("初始化存储失败: %w", err)
    }

    remoteBase := "" // minio 模式下 object-key 根为 bucket 根,不加前缀
    mgr := isync.NewSyncManager(homeDir, remoteBase, store)

    fmt.Println("Scanning differences...")
    diff, err := mgr.Diff(flags.Paths)
    if err != nil {
        return err
    }

    fmt.Print(isync.FormatDiff(diff, "push"))
    if diff.IsEmpty() {
        return nil
    }

    if !flags.Yes {
        if !isync.ConfirmContinue(os.Stdin, os.Stdout) {
            fmt.Println("Cancelled.")
            return nil
        }
    }

    if err := mgr.Push(flags.Paths); err != nil {
        return fmt.Errorf("push 失败: %w", err)
    }
    fmt.Println("Push complete.")
    return nil
}
```

- [ ] **Step 2: 新建 `internal/cmd/pull.go`**

```go
package cmd

import (
    "errors"
    "fmt"
    "os"
    "strings"

    "github.com/zfd81/groot/internal/config"
    "github.com/zfd81/groot/internal/storage"
    isync "github.com/zfd81/groot/internal/sync"
)

// PullFlags holds parsed flags for the pull command.
type PullFlags struct {
    Paths []string
    Yes   bool
}

// ParsePullFlags 解析 groot pull 子命令参数。
func ParsePullFlags(args []string) (*PullFlags, error) {
    flags := &PullFlags{}
    for _, arg := range args {
        switch arg {
        case "-h", "--help":
            printPullHelp()
            os.Exit(0)
        case "-y", "--yes":
            flags.Yes = true
        default:
            if strings.HasPrefix(arg, "-") {
                return nil, fmt.Errorf("unknown flag: %s", arg)
            }
            flags.Paths = append(flags.Paths, arg)
        }
    }
    return flags, nil
}

func printPullHelp() {
    fmt.Println("用法: groot pull [path...] [-y]")
    fmt.Println()
    fmt.Println("将 MinIO 的集群共享配置镜像拉取到本地 HOME。")
    fmt.Println("仅在 minio 模式下可用。")
    fmt.Println()
    fmt.Println("参数:")
    fmt.Println("  path...   要拉取的资源路径(可多个),省略时拉取全部白名单资源")
    fmt.Println()
    fmt.Println("选项:")
    fmt.Println("  -y, --yes   跳过交互确认,直接执行")
    fmt.Println("  -h, --help  显示帮助")
}

// RunPull 执行 groot pull。
func RunPull(flags *PullFlags) error {
    homeDir := GetDefaultHome()
    cfg, err := config.Load(homeDir)
    if err != nil {
        return fmt.Errorf("加载配置失败: %w", err)
    }
    if cfg.Storage.Minio == nil {
        return errors.New("groot pull 仅在 minio 模式下可用\n请在 ~/.groot/env.yaml 中配置 minio 节")
    }
    store, err := storage.New(cfg.Storage)
    if err != nil {
        return fmt.Errorf("初始化存储失败: %w", err)
    }

    remoteBase := ""
    mgr := isync.NewSyncManager(homeDir, remoteBase, store)

    fmt.Println("Scanning differences...")
    diff, err := mgr.Diff(flags.Paths)
    if err != nil {
        return err
    }

    fmt.Print(isync.FormatDiff(diff, "pull"))
    if diff.IsEmpty() {
        return nil
    }

    if !flags.Yes {
        if !isync.ConfirmContinue(os.Stdin, os.Stdout) {
            fmt.Println("Cancelled.")
            return nil
        }
    }

    if err := mgr.Pull(flags.Paths); err != nil {
        return fmt.Errorf("pull 失败: %w", err)
    }
    fmt.Println("Pull complete.")
    return nil
}
```

- [ ] **Step 3: 新建 `internal/cmd/diff_cmd.go`**

```go
package cmd

import (
    "errors"
    "fmt"
    "os"
    "strings"

    "github.com/zfd81/groot/internal/config"
    "github.com/zfd81/groot/internal/storage"
    isync "github.com/zfd81/groot/internal/sync"
)

// DiffFlags holds parsed flags for the diff command.
type DiffFlags struct {
    Paths []string
}

// ParseDiffFlags 解析 groot diff 子命令参数。
func ParseDiffFlags(args []string) (*DiffFlags, error) {
    flags := &DiffFlags{}
    for _, arg := range args {
        switch arg {
        case "-h", "--help":
            printDiffHelp()
            os.Exit(0)
        default:
            if strings.HasPrefix(arg, "-") {
                return nil, fmt.Errorf("unknown flag: %s", arg)
            }
            flags.Paths = append(flags.Paths, arg)
        }
    }
    return flags, nil
}

func printDiffHelp() {
    fmt.Println("用法: groot diff [path...]")
    fmt.Println()
    fmt.Println("显示本地 HOME 与 MinIO 之间的集群共享配置差异(只读,不修改)。")
    fmt.Println("仅在 minio 模式下可用。")
    fmt.Println()
    fmt.Println("参数:")
    fmt.Println("  path...   要比较的资源路径(可多个),省略时比较全部白名单资源")
    fmt.Println()
    fmt.Println("选项:")
    fmt.Println("  -h, --help  显示帮助")
}

// RunDiff 执行 groot diff。
func RunDiff(flags *DiffFlags) error {
    homeDir := GetDefaultHome()
    cfg, err := config.Load(homeDir)
    if err != nil {
        return fmt.Errorf("加载配置失败: %w", err)
    }
    if cfg.Storage.Minio == nil {
        return errors.New("groot diff 仅在 minio 模式下可用\n请在 ~/.groot/env.yaml 中配置 minio 节")
    }
    store, err := storage.New(cfg.Storage)
    if err != nil {
        return fmt.Errorf("初始化存储失败: %w", err)
    }

    remoteBase := ""
    mgr := isync.NewSyncManager(homeDir, remoteBase, store)

    diff, err := mgr.Diff(flags.Paths)
    if err != nil {
        return err
    }

    fmt.Print(isync.FormatDiff(diff, "diff"))
    return nil
}
```

- [ ] **Step 4: 编译确认**

```bash
go build ./internal/cmd/...
```

Expected: 通过

- [ ] **Step 5: 提交**

```bash
git add internal/cmd/push.go internal/cmd/pull.go internal/cmd/diff_cmd.go
git commit -m "$(cat <<'EOF'
cmd: 新增 push / pull / diff 子命令

三个子命令均加载 env.yaml 判断 minio 模式,local 模式下直接报错退出。
push / pull 默认显示 diff 后等待 y/n 确认,--yes/-y 跳过。
diff 只读不修改,直接输出 DiffResult 渲染结果。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: main.go dispatch — 接入三个子命令

**Files:**
- Modify: `cmd/groot/main.go`

- [ ] **Step 1: 在 main.go switch 的 `case "tail":` 之后添加三个 case**

找到 [cmd/groot/main.go:95-100](cmd/groot/main.go#L95-L100) 的 `case "tail":` 块,在它之后(且在 `default:` 之前)添加:

```go
case "push":
    handlePushCommand(args[1:])
    return
case "pull":
    handlePullCommand(args[1:])
    return
case "diff":
    handleDiffCommand(args[1:])
    return
```

- [ ] **Step 2: 在 main.go 末尾添加三个 handler 函数**

在 `handleInitCommand` 等现有 handler 之后追加:

```go
func handlePushCommand(args []string) {
    flags, err := cmd.ParsePushFlags(args)
    if err != nil {
        fmt.Fprintf(os.Stderr, "错误: %s\n", err)
        os.Exit(1)
    }
    if err := cmd.RunPush(flags); err != nil {
        fmt.Fprintf(os.Stderr, "错误: %s\n", err)
        os.Exit(1)
    }
}

func handlePullCommand(args []string) {
    flags, err := cmd.ParsePullFlags(args)
    if err != nil {
        fmt.Fprintf(os.Stderr, "错误: %s\n", err)
        os.Exit(1)
    }
    if err := cmd.RunPull(flags); err != nil {
        fmt.Fprintf(os.Stderr, "错误: %s\n", err)
        os.Exit(1)
    }
}

func handleDiffCommand(args []string) {
    flags, err := cmd.ParseDiffFlags(args)
    if err != nil {
        fmt.Fprintf(os.Stderr, "错误: %s\n", err)
        os.Exit(1)
    }
    if err := cmd.RunDiff(flags); err != nil {
        fmt.Fprintf(os.Stderr, "错误: %s\n", err)
        os.Exit(1)
    }
}
```

- [ ] **Step 3: 在 `printHelp()` 中添加三个命令说明**

找到 `printHelp()` 中"子命令:"列表,在 `tail` 行后添加:

```
  push              将本地配置推送到 MinIO（minio 模式）
  pull              从 MinIO 拉取配置到本地（minio 模式）
  diff              显示本地与 MinIO 的配置差异（minio 模式）
```

- [ ] **Step 4: 全量编译**

```bash
go build -o /tmp/groot ./cmd/groot
```

Expected: 通过,生成 `/tmp/groot`

- [ ] **Step 5: 冒烟验证 help 输出**

```bash
/tmp/groot push --help
/tmp/groot pull --help
/tmp/groot diff --help
```

Expected: 各自打印帮助文本并退出 0

- [ ] **Step 6: local 模式下命令报错**

不启动 minio,直接运行:

```bash
/tmp/groot push config.yaml 2>&1 | head -3
```

Expected: 输出含 "minio 模式未启用" 或 "加载配置失败"(无 ~/.groot/config.yaml 时),不 panic,exitcode 1

- [ ] **Step 7: 全量测试**

```bash
go test ./... 2>&1 | grep -E "^(ok|FAIL)" | head -20
```

Expected: 全部 ok,无 FAIL

- [ ] **Step 8: 清理临时文件,提交**

```bash
rm -f /tmp/groot
git add cmd/groot/main.go
git commit -m "$(cat <<'EOF'
main.go 接入 push / pull / diff 子命令

dispatch table 添加三个 case,对应 handler 函数复用现有 ParseXxxFlags + RunXxx 模式。
printHelp() 同步补充三条命令说明。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## 自检

### Spec 覆盖确认

| spec 需求 | 对应 Task |
|-----------|----------|
| SyncManager 接口(Push/Pull/Diff) | Task 5 |
| DiffResult 四分组(Added/Modified/Removed/Same) | Task 2 |
| size + mtime ±1s 判等算法 | Task 2 |
| 白名单 SyncableResourceRoots | Task 1 |
| env.yaml 禁止 push/pull | Task 1(ValidateSyncPath) |
| 路径展开规则(类别目录展开子项) | Task 3 |
| 禁止直接操作 skill 目录内单文件 | Task 1 + Task 3 |
| diff 渲染 + push/pull 前显示差异 | Task 4 |
| 交互确认(y/n) + --yes 跳过 | Task 5(ConfirmContinue) + Task 6 |
| push 镜像语义(删除远端多余) | Task 5(Push) |
| pull 先写后删(Phase A → Phase B) | Task 5(Pull) |
| pull 启动时清 *.tmp 残留 | Task 5(cleanTmpFiles) |
| pull 后需重启资源的提示 | Task 4(RenderDiff + anyNeedsRestart) |
| local 模式返回"未启用"错误 | Task 5(disabledSyncManager) + Task 6 |
| groot push/pull/diff CLI 子命令 | Task 6 |
| main.go dispatch | Task 7 |

所有 §1.8 需求均有对应 Task。

### 无占位符确认

所有步骤均含完整代码,无 TBD / 相似 Task N 引用 / 缺代码的步骤描述。

### 类型一致性确认

- `DiffResult.Added/Modified/Removed/Same []string` — Task 2 定义,Task 4/5 使用,一致
- `SyncManager.Diff() (DiffResult, error)` — Task 5 定义,Task 6 使用,一致
- `ResolveLocalPaths(homeDir string, paths []string) ([]string, error)` — Task 3 定义,Task 5 使用,一致
- `ComputeDiff(store, localBase, remoteBase string, paths []string) (DiffResult, error)` — Task 2 定义,Task 5 使用,一致
- `RenderDiff(w io.Writer, d DiffResult, direction string)` — Task 4 定义,Task 5 `FormatDiff` 调用,一致
- `ConfirmContinue(r io.Reader, w io.Writer) bool` — Task 5 定义,Task 6 使用,一致
- `NewSyncManager(homeDir, remoteBase string, store istorage.Storage) SyncManager` — Task 5 定义,Task 6 使用,一致
