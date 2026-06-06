# 存储抽象层实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 在 `internal/storage/` 下实现统一的文件存储抽象层（local + minio 两种类型），并将 `internal/memory/manager.go` 中现有的附件读写逻辑迁移到该抽象层。

**Architecture:** Storage 接口定义 6 个能力（Write/Read/Delete/DeleteDir/Stat/List），底层使用 `io.Reader`/`io.ReadCloser` 流式 IO；factory 按 `cfg.Storage.Minio` 是否为 nil 选择实现，配了就用 minio，没配就用 local；storage 层不做任何路径拼接，调用方传什么就用什么。

**Tech Stack:** Go 1.26，`github.com/minio/minio-go/v7`（新增），`go test`（已有）。

---

## 文件结构

**新增**：

- `internal/storage/storage.go` — `Storage` 接口、`FileInfo` 类型、`ErrNotFound` / `ErrIsDir` 哨兵错误
- `internal/storage/local.go` — 本地磁盘实现（强制绝对路径）
- `internal/storage/minio.go` — MinIO 实现（path 即 object key）
- `internal/storage/factory.go` — `New(cfg StorageConfig) (Storage, error)` 入口
- `internal/storage/storage_test.go` — 接口共享测试用例（兜底用）
- `internal/storage/local_test.go` — local 实现单元测试
- `internal/storage/minio_test.go` — minio 实现 mock 单元测试
- `internal/storage/factory_test.go` — factory 选择逻辑测试

**修改**：

- `internal/config/config.go` — 新增 `StorageConfig`、`MinioConfig`，`Config` 加 `Storage StorageConfig` 字段
- `internal/config/defaults.go` — `DefaultConfig` 中 `Storage` 字段保持零值（local 模式零配置）
- `internal/config/template.go` — `init` 模板加 storage 块，minio 段以注释形式给出
- `internal/memory/manager.go` — `Manager` 新增 `storage` 字段；`NewManager` 加参数；`SaveAttachment` 改用 storage；`Cleanup` 拆为"先删附件后删元数据"两步；`CreateSession` 移除 `os.MkdirAll(attachments)` 调用
- `internal/memory/memory_test.go` — 调整 `NewManager` 调用，注入 storage 实例
- `cmd/groot/main.go` — 启动时按 `cfg.Storage` 创建 storage 并注入 `memory.NewManager`
- `internal/cmd/chat.go` — 同步调整 `memory.NewManager` 调用
- `go.mod` / `go.sum` — 通过 `go mod tidy` 引入 `github.com/minio/minio-go/v7 v7.2.0`

---

## Task 1: 定义 Storage 接口、FileInfo 类型、哨兵错误

**Files:**
- Create: `internal/storage/storage.go`

- [ ] **Step 1: 创建 `internal/storage/storage.go`**

```go
package storage

import (
	"context"
	"errors"
	"io"
	"time"
)

// ErrNotFound 表示请求的 path 在底层存储不存在。
// 调用方可使用 errors.Is(err, ErrNotFound) 判断。
var ErrNotFound = errors.New("storage: file not found")

// ErrIsDir 表示对目录类型 path 调用了仅文件适用的方法（如 Read）。
var ErrIsDir = errors.New("storage: path is a directory")

// FileInfo 描述文件或目录的元数据。
type FileInfo struct {
	Path        string    // 与调用方传入的 path 一致
	Size        int64     // 字节数
	ContentType string    // MIME 类型
	ModTime     time.Time // 最后修改时间
	IsDir       bool      // 是否为目录
}

// Storage 是统一存储接口。两种类型对 path 的语义不同：
//   - local 实现要求 path 是文件系统绝对路径
//   - minio 实现把 path 直接当 object key
// storage 层不做任何路径拼接，调用方传什么就用什么。
type Storage interface {
	// Write 写入数据流到指定 path。size < 0 表示长度未知（minio 会用分片上传）。
	// contentType 为空时实现可按文件名扩展名推断或留空。
	Write(ctx context.Context, path string, r io.Reader, size int64, contentType string) error

	// Read 返回指定 path 的内容流，调用方负责 Close。
	// 不存在返回 ErrNotFound，path 是目录返回 ErrIsDir。
	Read(ctx context.Context, path string) (io.ReadCloser, error)

	// Delete 删除指定 path 的单个文件。不存在返回 ErrNotFound。
	Delete(ctx context.Context, path string) error

	// DeleteDir 递归删除指定目录及其所有内容。
	// 目录不存在视为已删除，返回 nil（与 os.RemoveAll 一致）。
	DeleteDir(ctx context.Context, path string) error

	// Stat 返回 path 的元信息。不存在返回 ErrNotFound。
	Stat(ctx context.Context, path string) (*FileInfo, error)

	// List 列出指定目录下的文件（不递归）。
	// 目录不存在返回 ErrNotFound，目录为空返回空切片。
	List(ctx context.Context, dir string) ([]*FileInfo, error)
}
```

- [ ] **Step 2: 验证编译通过**

Run: `go build ./internal/storage/...`
Expected: 编译通过，无输出

- [ ] **Step 3: 提交**

```bash
git add internal/storage/storage.go
git commit -m "$(cat <<'EOF'
新增 storage 包接口定义

定义 Storage 接口、FileInfo 类型和 ErrNotFound / ErrIsDir 哨兵错误，作为
local 和 minio 两种存储实现的统一契约。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: 实现 local 存储

**Files:**
- Create: `internal/storage/local.go`
- Test: `internal/storage/local_test.go`

- [ ] **Step 1: 编写失败的单元测试**

```go
// internal/storage/local_test.go
package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLocal_RejectsRelativePath(t *testing.T) {
	ls := NewLocal()
	ctx := context.Background()
	err := ls.Write(ctx, "relative/path.txt", strings.NewReader("x"), 1, "text/plain")
	if err == nil {
		t.Fatal("expected error for relative path, got nil")
	}
	if !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("expected absolute-path error, got: %v", err)
	}
}

func TestLocal_WriteReadDeleteCycle(t *testing.T) {
	dir := t.TempDir()
	ls := NewLocal()
	ctx := context.Background()
	path := filepath.Join(dir, "sub", "hello.txt")
	want := []byte("hello world")

	if err := ls.Write(ctx, path, bytes.NewReader(want), int64(len(want)), "text/plain"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	rc, err := ls.Read(ctx, path)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, want) {
		t.Fatalf("content mismatch: got %q, want %q", got, want)
	}

	if err := ls.Delete(ctx, path); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("file should not exist after Delete, stat err: %v", err)
	}
}

func TestLocal_StatNotFound(t *testing.T) {
	ls := NewLocal()
	ctx := context.Background()
	missing := filepath.Join(t.TempDir(), "nope.txt")
	_, err := ls.Stat(ctx, missing)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

func TestLocal_ReadNotFound(t *testing.T) {
	ls := NewLocal()
	ctx := context.Background()
	missing := filepath.Join(t.TempDir(), "nope.txt")
	_, err := ls.Read(ctx, missing)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

func TestLocal_ReadReturnsErrIsDir(t *testing.T) {
	dir := t.TempDir()
	ls := NewLocal()
	ctx := context.Background()
	_, err := ls.Read(ctx, dir)
	if !errors.Is(err, ErrIsDir) {
		t.Fatalf("expected ErrIsDir, got: %v", err)
	}
}

func TestLocal_DeleteNotFound(t *testing.T) {
	ls := NewLocal()
	ctx := context.Background()
	missing := filepath.Join(t.TempDir(), "nope.txt")
	err := ls.Delete(ctx, missing)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}

func TestLocal_DeleteDirRecursive(t *testing.T) {
	dir := t.TempDir()
	ls := NewLocal()
	ctx := context.Background()

	subFile := filepath.Join(dir, "sub", "a.txt")
	if err := ls.Write(ctx, subFile, strings.NewReader("a"), 1, ""); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if err := ls.DeleteDir(ctx, filepath.Join(dir, "sub")); err != nil {
		t.Fatalf("DeleteDir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "sub")); !os.IsNotExist(err) {
		t.Fatalf("sub dir should not exist, stat err: %v", err)
	}
}

func TestLocal_DeleteDirMissingIsNoop(t *testing.T) {
	ls := NewLocal()
	ctx := context.Background()
	missing := filepath.Join(t.TempDir(), "ghost")
	if err := ls.DeleteDir(ctx, missing); err != nil {
		t.Fatalf("DeleteDir on missing dir should be no-op, got: %v", err)
	}
}

func TestLocal_ListReturnsFiles(t *testing.T) {
	dir := t.TempDir()
	ls := NewLocal()
	ctx := context.Background()

	for _, name := range []string{"a.txt", "b.json"} {
		if err := ls.Write(ctx, filepath.Join(dir, name), strings.NewReader("x"), 1, ""); err != nil {
			t.Fatalf("Write %s: %v", name, err)
		}
	}
	if err := os.MkdirAll(filepath.Join(dir, "child"), 0755); err != nil {
		t.Fatalf("mkdir child: %v", err)
	}

	infos, err := ls.List(ctx, dir)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 3 {
		t.Fatalf("expected 3 entries, got %d", len(infos))
	}
	var sawChild bool
	for _, fi := range infos {
		if fi.IsDir && filepath.Base(fi.Path) == "child" {
			sawChild = true
		}
	}
	if !sawChild {
		t.Fatal("expected child dir entry with IsDir=true")
	}
}

func TestLocal_ListNotFound(t *testing.T) {
	ls := NewLocal()
	ctx := context.Background()
	missing := filepath.Join(t.TempDir(), "ghost")
	_, err := ls.List(ctx, missing)
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got: %v", err)
	}
}
```

- [ ] **Step 2: 运行测试，确认编译失败**

Run: `go test ./internal/storage/... -run TestLocal -v`
Expected: 编译失败，提示 `undefined: NewLocal`

- [ ] **Step 3: 实现 `internal/storage/local.go`**

```go
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
)

// Local 是基于本地文件系统的 Storage 实现。
// 所有 path 必须是绝对路径，否则返回错误。
type Local struct{}

// NewLocal 创建一个本地存储实例。无任何配置参数。
func NewLocal() *Local { return &Local{} }

func (l *Local) ensureAbs(path string) error {
	if !filepath.IsAbs(path) {
		return fmt.Errorf("storage: path must be absolute, got %q", path)
	}
	return nil
}

func (l *Local) Write(ctx context.Context, path string, r io.Reader, size int64, contentType string) error {
	if err := l.ensureAbs(path); err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return fmt.Errorf("storage: mkdir %s: %w", filepath.Dir(path), err)
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return fmt.Errorf("storage: open %s: %w", path, err)
	}
	defer f.Close()

	if _, err := io.Copy(f, r); err != nil {
		return fmt.Errorf("storage: write %s: %w", path, err)
	}
	return nil
}

func (l *Local) Read(ctx context.Context, path string) (io.ReadCloser, error) {
	if err := l.ensureAbs(path); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: stat %s: %w", path, err)
	}
	if info.IsDir() {
		return nil, ErrIsDir
	}
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: open %s: %w", path, err)
	}
	return f, nil
}

func (l *Local) Delete(ctx context.Context, path string) error {
	if err := l.ensureAbs(path); err != nil {
		return err
	}
	if err := os.Remove(path); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return ErrNotFound
		}
		return fmt.Errorf("storage: remove %s: %w", path, err)
	}
	return nil
}

func (l *Local) DeleteDir(ctx context.Context, path string) error {
	if err := l.ensureAbs(path); err != nil {
		return err
	}
	if err := os.RemoveAll(path); err != nil {
		return fmt.Errorf("storage: remove dir %s: %w", path, err)
	}
	return nil
}

func (l *Local) Stat(ctx context.Context, path string) (*FileInfo, error) {
	if err := l.ensureAbs(path); err != nil {
		return nil, err
	}
	info, err := os.Stat(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: stat %s: %w", path, err)
	}
	return &FileInfo{
		Path:        path,
		Size:        info.Size(),
		ContentType: detectContentType(path, info.IsDir()),
		ModTime:     info.ModTime(),
		IsDir:       info.IsDir(),
	}, nil
}

func (l *Local) List(ctx context.Context, dir string) ([]*FileInfo, error) {
	if err := l.ensureAbs(dir); err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: read dir %s: %w", dir, err)
	}
	out := make([]*FileInfo, 0, len(entries))
	for _, e := range entries {
		full := filepath.Join(dir, e.Name())
		info, err := e.Info()
		if err != nil {
			continue
		}
		out = append(out, &FileInfo{
			Path:        full,
			Size:        info.Size(),
			ContentType: detectContentType(full, info.IsDir()),
			ModTime:     info.ModTime(),
			IsDir:       info.IsDir(),
		})
	}
	return out, nil
}

// detectContentType 按文件扩展名推断 MIME，目录返回空串。
func detectContentType(path string, isDir bool) string {
	if isDir {
		return ""
	}
	ext := filepath.Ext(path)
	if ext == "" {
		return ""
	}
	return mime.TypeByExtension(ext)
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./internal/storage/... -run TestLocal -v`
Expected: 全部 PASS

- [ ] **Step 5: 提交**

```bash
git add internal/storage/local.go internal/storage/local_test.go
git commit -m "$(cat <<'EOF'
实现 local 存储类型

实现基于本地文件系统的 Storage，强制要求 path 为绝对路径，
按 os.ErrNotExist 映射 ErrNotFound、目录路径映射 ErrIsDir。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: 引入 minio-go 依赖

**Files:**
- Modify: `go.mod`
- Modify: `go.sum`

- [ ] **Step 1: 拉取 minio-go**

Run: `go get github.com/minio/minio-go/v7@v7.2.0`
Expected: 下载完成，`go.mod` 中新增 `github.com/minio/minio-go/v7 v7.2.0`

- [ ] **Step 2: 整理 go.mod**

Run: `go mod tidy`
Expected: `go.sum` 同步更新

- [ ] **Step 3: 验证编译**

Run: `go build ./...`
Expected: 编译通过

- [ ] **Step 4: 提交**

```bash
git add go.mod go.sum
git commit -m "$(cat <<'EOF'
新增 minio-go v7.2.0 依赖

为 storage 包的 minio 实现引入 MinIO Go SDK。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: 实现 minio 存储

**Files:**
- Create: `internal/storage/minio.go`
- Test: `internal/storage/minio_test.go`

- [ ] **Step 1: 编写失败的单元测试（用真实 minio-go client 类型 + 接口包装）**

minio-go 的官方客户端没有可用 mock，但单元测试不需要真实集群——把客户端调用封装在小接口背后即可。先写测试驱动接口形态。

```go
// internal/storage/minio_test.go
package storage

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/minio/minio-go/v7"
)

// fakeMinioClient 实现 minioAPI 接口，用于单元测试。
type fakeMinioClient struct {
	objects map[string][]byte // key -> body
	stats   map[string]minio.ObjectInfo

	putErr    error
	getErr    error
	statErr   error
	removeErr error
	listErr   error
}

func newFakeClient() *fakeMinioClient {
	return &fakeMinioClient{
		objects: map[string][]byte{},
		stats:   map[string]minio.ObjectInfo{},
	}
}

func (f *fakeMinioClient) PutObject(ctx context.Context, bucket, key string, r io.Reader, size int64, opts minio.PutObjectOptions) error {
	if f.putErr != nil {
		return f.putErr
	}
	body, err := io.ReadAll(r)
	if err != nil {
		return err
	}
	f.objects[key] = body
	f.stats[key] = minio.ObjectInfo{
		Key:          key,
		Size:         int64(len(body)),
		ContentType:  opts.ContentType,
		LastModified: time.Unix(1700000000, 0),
	}
	return nil
}

func (f *fakeMinioClient) GetObject(ctx context.Context, bucket, key string, opts minio.GetObjectOptions) (io.ReadCloser, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	body, ok := f.objects[key]
	if !ok {
		return nil, minio.ErrorResponse{Code: "NoSuchKey"}
	}
	return io.NopCloser(bytes.NewReader(body)), nil
}

func (f *fakeMinioClient) StatObject(ctx context.Context, bucket, key string, opts minio.StatObjectOptions) (minio.ObjectInfo, error) {
	if f.statErr != nil {
		return minio.ObjectInfo{}, f.statErr
	}
	info, ok := f.stats[key]
	if !ok {
		return minio.ObjectInfo{}, minio.ErrorResponse{Code: "NoSuchKey"}
	}
	return info, nil
}

func (f *fakeMinioClient) RemoveObject(ctx context.Context, bucket, key string, opts minio.RemoveObjectOptions) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	if _, ok := f.objects[key]; !ok {
		return minio.ErrorResponse{Code: "NoSuchKey"}
	}
	delete(f.objects, key)
	delete(f.stats, key)
	return nil
}

func (f *fakeMinioClient) ListObjects(ctx context.Context, bucket string, opts minio.ListObjectsOptions) <-chan minio.ObjectInfo {
	ch := make(chan minio.ObjectInfo, len(f.objects)+1)
	if f.listErr != nil {
		ch <- minio.ObjectInfo{Err: f.listErr}
		close(ch)
		return ch
	}
	for key, info := range f.stats {
		if !strings.HasPrefix(key, opts.Prefix) {
			continue
		}
		ch <- info
	}
	close(ch)
	return ch
}

func (f *fakeMinioClient) RemoveObjectsRecursive(ctx context.Context, bucket, prefix string) error {
	if f.removeErr != nil {
		return f.removeErr
	}
	for key := range f.objects {
		if strings.HasPrefix(key, prefix) {
			delete(f.objects, key)
			delete(f.stats, key)
		}
	}
	return nil
}

func newMinioForTest(c minioAPI) *Minio {
	return &Minio{client: c, bucket: "groot"}
}

func TestMinio_WriteReadCycle(t *testing.T) {
	fc := newFakeClient()
	ms := newMinioForTest(fc)
	ctx := context.Background()
	want := []byte("payload")

	if err := ms.Write(ctx, "sessions/abc/x.txt", bytes.NewReader(want), int64(len(want)), "text/plain"); err != nil {
		t.Fatalf("Write: %v", err)
	}

	rc, err := ms.Read(ctx, "sessions/abc/x.txt")
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	got, _ := io.ReadAll(rc)
	rc.Close()
	if !bytes.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestMinio_StatNotFound(t *testing.T) {
	ms := newMinioForTest(newFakeClient())
	_, err := ms.Stat(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMinio_ReadNotFound(t *testing.T) {
	ms := newMinioForTest(newFakeClient())
	_, err := ms.Read(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMinio_DeleteNotFound(t *testing.T) {
	ms := newMinioForTest(newFakeClient())
	err := ms.Delete(context.Background(), "missing")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound, got %v", err)
	}
}

func TestMinio_DeleteDirRemovesPrefix(t *testing.T) {
	fc := newFakeClient()
	ms := newMinioForTest(fc)
	ctx := context.Background()

	for _, k := range []string{"sessions/abc/a", "sessions/abc/sub/b", "sessions/other/c"} {
		_ = ms.Write(ctx, k, strings.NewReader("x"), 1, "")
	}

	if err := ms.DeleteDir(ctx, "sessions/abc"); err != nil {
		t.Fatalf("DeleteDir: %v", err)
	}
	if _, ok := fc.objects["sessions/abc/a"]; ok {
		t.Fatal("sessions/abc/a should have been removed")
	}
	if _, ok := fc.objects["sessions/other/c"]; !ok {
		t.Fatal("sessions/other/c should remain")
	}
}

func TestMinio_ListByPrefix(t *testing.T) {
	fc := newFakeClient()
	ms := newMinioForTest(fc)
	ctx := context.Background()

	for _, k := range []string{"sessions/abc/a", "sessions/abc/b", "sessions/other/c"} {
		_ = ms.Write(ctx, k, strings.NewReader("x"), 1, "")
	}

	infos, err := ms.List(ctx, "sessions/abc")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(infos) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(infos))
	}
}
```

- [ ] **Step 2: 运行测试，确认编译失败**

Run: `go test ./internal/storage/... -run TestMinio -v`
Expected: 编译失败，提示 `undefined: minioAPI`、`undefined: Minio`

- [ ] **Step 3: 实现 `internal/storage/minio.go`**

```go
package storage

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/minio/minio-go/v7"
	"github.com/minio/minio-go/v7/pkg/credentials"
)

// minioAPI 抽象 MinIO 客户端调用，便于单元测试 mock。
type minioAPI interface {
	PutObject(ctx context.Context, bucket, key string, r io.Reader, size int64, opts minio.PutObjectOptions) error
	GetObject(ctx context.Context, bucket, key string, opts minio.GetObjectOptions) (io.ReadCloser, error)
	StatObject(ctx context.Context, bucket, key string, opts minio.StatObjectOptions) (minio.ObjectInfo, error)
	RemoveObject(ctx context.Context, bucket, key string, opts minio.RemoveObjectOptions) error
	ListObjects(ctx context.Context, bucket string, opts minio.ListObjectsOptions) <-chan minio.ObjectInfo
	RemoveObjectsRecursive(ctx context.Context, bucket, prefix string) error
}

// minioClient 是 minioAPI 的真实实现，包装 *minio.Client。
type minioClient struct{ c *minio.Client }

func (m *minioClient) PutObject(ctx context.Context, bucket, key string, r io.Reader, size int64, opts minio.PutObjectOptions) error {
	_, err := m.c.PutObject(ctx, bucket, key, r, size, opts)
	return err
}

func (m *minioClient) GetObject(ctx context.Context, bucket, key string, opts minio.GetObjectOptions) (io.ReadCloser, error) {
	obj, err := m.c.GetObject(ctx, bucket, key, opts)
	if err != nil {
		return nil, err
	}
	return obj, nil
}

func (m *minioClient) StatObject(ctx context.Context, bucket, key string, opts minio.StatObjectOptions) (minio.ObjectInfo, error) {
	return m.c.StatObject(ctx, bucket, key, opts)
}

func (m *minioClient) RemoveObject(ctx context.Context, bucket, key string, opts minio.RemoveObjectOptions) error {
	return m.c.RemoveObject(ctx, bucket, key, opts)
}

func (m *minioClient) ListObjects(ctx context.Context, bucket string, opts minio.ListObjectsOptions) <-chan minio.ObjectInfo {
	return m.c.ListObjects(ctx, bucket, opts)
}

func (m *minioClient) RemoveObjectsRecursive(ctx context.Context, bucket, prefix string) error {
	objCh := make(chan minio.ObjectInfo)
	go func() {
		defer close(objCh)
		for obj := range m.c.ListObjects(ctx, bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: true}) {
			if obj.Err != nil {
				continue
			}
			objCh <- obj
		}
	}()
	for rmErr := range m.c.RemoveObjects(ctx, bucket, objCh, minio.RemoveObjectsOptions{}) {
		if rmErr.Err != nil {
			return rmErr.Err
		}
	}
	return nil
}

// Minio 是基于 MinIO 对象存储的 Storage 实现。
// path 直接作为 object key 使用，不做任何前缀处理。
type Minio struct {
	client minioAPI
	bucket string
}

// NewMinio 用提供的 endpoint / 密钥 / bucket 创建一个 minio 存储实例。
func NewMinio(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*Minio, error) {
	c, err := minio.New(endpoint, &minio.Options{
		Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
		Secure: useSSL,
	})
	if err != nil {
		return nil, fmt.Errorf("storage: init minio client: %w", err)
	}
	return &Minio{client: &minioClient{c: c}, bucket: bucket}, nil
}

func isNotExist(err error) bool {
	if err == nil {
		return false
	}
	var resp minio.ErrorResponse
	if errors.As(err, &resp) {
		return resp.Code == "NoSuchKey" || resp.Code == "NoSuchBucket"
	}
	return false
}

func (m *Minio) Write(ctx context.Context, path string, r io.Reader, size int64, contentType string) error {
	opts := minio.PutObjectOptions{ContentType: contentType}
	if err := m.client.PutObject(ctx, m.bucket, path, r, size, opts); err != nil {
		return fmt.Errorf("storage: minio put %s: %w", path, err)
	}
	return nil
}

func (m *Minio) Read(ctx context.Context, path string) (io.ReadCloser, error) {
	if _, err := m.client.StatObject(ctx, m.bucket, path, minio.StatObjectOptions{}); err != nil {
		if isNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: minio stat %s: %w", path, err)
	}
	rc, err := m.client.GetObject(ctx, m.bucket, path, minio.GetObjectOptions{})
	if err != nil {
		if isNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: minio get %s: %w", path, err)
	}
	return rc, nil
}

func (m *Minio) Delete(ctx context.Context, path string) error {
	if _, err := m.client.StatObject(ctx, m.bucket, path, minio.StatObjectOptions{}); err != nil {
		if isNotExist(err) {
			return ErrNotFound
		}
		return fmt.Errorf("storage: minio stat %s: %w", path, err)
	}
	if err := m.client.RemoveObject(ctx, m.bucket, path, minio.RemoveObjectOptions{}); err != nil {
		return fmt.Errorf("storage: minio remove %s: %w", path, err)
	}
	return nil
}

func (m *Minio) DeleteDir(ctx context.Context, path string) error {
	prefix := strings.TrimSuffix(path, "/") + "/"
	if err := m.client.RemoveObjectsRecursive(ctx, m.bucket, prefix); err != nil {
		return fmt.Errorf("storage: minio remove dir %s: %w", path, err)
	}
	return nil
}

func (m *Minio) Stat(ctx context.Context, path string) (*FileInfo, error) {
	info, err := m.client.StatObject(ctx, m.bucket, path, minio.StatObjectOptions{})
	if err != nil {
		if isNotExist(err) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("storage: minio stat %s: %w", path, err)
	}
	return objectInfoToFileInfo(info, false), nil
}

func (m *Minio) List(ctx context.Context, dir string) ([]*FileInfo, error) {
	prefix := strings.TrimSuffix(dir, "/") + "/"
	out := make([]*FileInfo, 0)
	for obj := range m.client.ListObjects(ctx, m.bucket, minio.ListObjectsOptions{Prefix: prefix, Recursive: false}) {
		if obj.Err != nil {
			return nil, fmt.Errorf("storage: minio list %s: %w", dir, obj.Err)
		}
		isDir := strings.HasSuffix(obj.Key, "/")
		out = append(out, objectInfoToFileInfo(obj, isDir))
	}
	return out, nil
}

func objectInfoToFileInfo(info minio.ObjectInfo, isDir bool) *FileInfo {
	mod := info.LastModified
	if mod.IsZero() {
		mod = time.Time{}
	}
	return &FileInfo{
		Path:        info.Key,
		Size:        info.Size,
		ContentType: info.ContentType,
		ModTime:     mod,
		IsDir:       isDir,
	}
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./internal/storage/... -run TestMinio -v`
Expected: 全部 PASS

- [ ] **Step 5: 提交**

```bash
git add internal/storage/minio.go internal/storage/minio_test.go
git commit -m "$(cat <<'EOF'
实现 minio 存储类型

实现基于 MinIO 对象存储的 Storage，path 直接作为 object key 使用，
通过 minioAPI 内部接口包装客户端调用以支持 mock 测试。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: 新增配置结构

**Files:**
- Modify: `internal/config/config.go`

- [ ] **Step 1: 编写失败的单元测试**

```go
// internal/config/storage_test.go
package config

import (
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestStorageConfig_DefaultIsLocal(t *testing.T) {
	var c Config
	if c.Storage.Minio != nil {
		t.Fatal("default Storage.Minio should be nil (local mode)")
	}
}

func TestStorageConfig_ParsesMinioBlock(t *testing.T) {
	src := `
storage:
  minio:
    endpoint: localhost:9000
    access_key: ak
    secret_key: sk
    bucket: groot
    use_ssl: true
`
	var c Config
	if err := yaml.Unmarshal([]byte(src), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.Storage.Minio == nil {
		t.Fatal("expected Storage.Minio to be set")
	}
	if c.Storage.Minio.Endpoint != "localhost:9000" {
		t.Errorf("endpoint = %q", c.Storage.Minio.Endpoint)
	}
	if !c.Storage.Minio.UseSSL {
		t.Error("UseSSL should be true")
	}
}

func TestStorageConfig_OmittedYieldsLocal(t *testing.T) {
	src := strings.TrimSpace(`
agent:
  name: groot
`)
	var c Config
	if err := yaml.Unmarshal([]byte(src), &c); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if c.Storage.Minio != nil {
		t.Fatal("Storage.Minio should be nil when storage block omitted")
	}
}
```

- [ ] **Step 2: 运行测试，确认编译失败**

Run: `go test ./internal/config/... -run TestStorageConfig -v`
Expected: 编译失败，提示 `c.Storage undefined`

- [ ] **Step 3: 在 `internal/config/config.go` 中添加 `StorageConfig` 与 `MinioConfig`，并在 `Config` 结构体中加 `Storage` 字段**

在 `Config` 结构体的字段列表末尾追加（注意保持现有字段缩进风格）：

```go
	Storage   StorageConfig   `yaml:"storage"`
```

在文件末尾追加新结构体：

```go
// StorageConfig 存储抽象层配置。
// Minio 非 nil 时使用 MinIO 对象存储；nil 表示使用本地磁盘存储（零配置）。
type StorageConfig struct {
	Minio *MinioConfig `yaml:"minio"`
}

// MinioConfig 描述连接 MinIO 集群所需信息。
type MinioConfig struct {
	Endpoint  string `yaml:"endpoint"`
	AccessKey string `yaml:"access_key"`
	SecretKey string `yaml:"secret_key"`
	Bucket    string `yaml:"bucket"`
	UseSSL    bool   `yaml:"use_ssl"`
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./internal/config/... -run TestStorageConfig -v`
Expected: 全部 PASS

- [ ] **Step 5: 验证 `DefaultConfig` 已天然支持新字段**

打开 `internal/config/defaults.go`，确认 `DefaultConfig()` 返回的 `Config` 中 `Storage` 字段保持零值（即 `Storage: StorageConfig{Minio: nil}`，等价于本地磁盘模式）。本任务不需要主动赋值——零值即正确默认值。

如果想加注释让意图更清晰，可在 `DefaultConfig()` 中显式补一行：

```go
		Storage: StorageConfig{}, // local 模式零配置
```

但不是必须；零值已经是想要的行为。

- [ ] **Step 6: 提交**

```bash
git add internal/config/config.go internal/config/storage_test.go internal/config/defaults.go
git commit -m "$(cat <<'EOF'
新增 StorageConfig 配置结构

新增 StorageConfig / MinioConfig，并在 Config 中加入 Storage 字段。
未配置 minio 时 Minio 字段为 nil，对应使用本地磁盘存储模式。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: 实现 factory

**Files:**
- Create: `internal/storage/factory.go`
- Test: `internal/storage/factory_test.go`

- [ ] **Step 1: 编写失败的单元测试**

```go
// internal/storage/factory_test.go
package storage

import (
	"testing"

	"github.com/zfd81/groot/internal/config"
)

func TestFactory_NoMinioYieldsLocal(t *testing.T) {
	s, err := New(config.StorageConfig{})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := s.(*Local); !ok {
		t.Fatalf("expected *Local, got %T", s)
	}
}

func TestFactory_WithMinioYieldsMinio(t *testing.T) {
	cfg := config.StorageConfig{Minio: &config.MinioConfig{
		Endpoint:  "localhost:9000",
		AccessKey: "ak",
		SecretKey: "sk",
		Bucket:    "groot",
	}}
	s, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, ok := s.(*Minio); !ok {
		t.Fatalf("expected *Minio, got %T", s)
	}
}

func TestFactory_MinioMissingFieldsErrors(t *testing.T) {
	cfg := config.StorageConfig{Minio: &config.MinioConfig{
		Endpoint:  "",
		AccessKey: "ak",
		SecretKey: "sk",
		Bucket:    "groot",
	}}
	if _, err := New(cfg); err == nil {
		t.Fatal("expected error for missing endpoint")
	}
}
```

- [ ] **Step 2: 运行测试，确认编译失败**

Run: `go test ./internal/storage/... -run TestFactory -v`
Expected: 编译失败，提示 `undefined: New`

- [ ] **Step 3: 实现 `internal/storage/factory.go`**

```go
package storage

import (
	"fmt"

	"github.com/zfd81/groot/internal/config"
)

// New 根据配置创建合适的 Storage 实现：
// 未配置 minio 时返回 Local；配置 minio 时返回 Minio。
func New(cfg config.StorageConfig) (Storage, error) {
	if cfg.Minio == nil {
		return NewLocal(), nil
	}
	mc := cfg.Minio
	if mc.Endpoint == "" {
		return nil, fmt.Errorf("storage: minio.endpoint is required")
	}
	if mc.Bucket == "" {
		return nil, fmt.Errorf("storage: minio.bucket is required")
	}
	if mc.AccessKey == "" || mc.SecretKey == "" {
		return nil, fmt.Errorf("storage: minio.access_key and secret_key are required")
	}
	return NewMinio(mc.Endpoint, mc.AccessKey, mc.SecretKey, mc.Bucket, mc.UseSSL)
}
```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./internal/storage/... -run TestFactory -v`
Expected: 全部 PASS

- [ ] **Step 5: 提交**

```bash
git add internal/storage/factory.go internal/storage/factory_test.go
git commit -m "$(cat <<'EOF'
新增 storage factory 选择存储类型

未配置 minio 节时返回 Local；配置后校验必填字段并创建 Minio 实例。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: init 配置模板加 storage 块

**Files:**
- Modify: `internal/config/template.go`

- [ ] **Step 1: 编写失败的测试**

```go
// internal/config/template_test.go 末尾追加
func TestGenerateConfigTemplate_HasStorageBlock(t *testing.T) {
	tpl := GenerateConfigTemplate()
	if !strings.Contains(tpl, "# 存储抽象层配置") {
		t.Error("missing storage section header")
	}
	if !strings.Contains(tpl, "storage:") {
		t.Error("missing storage: key")
	}
	if !strings.Contains(tpl, "#   minio:") {
		t.Error("missing commented minio block")
	}
	if !strings.Contains(tpl, "${MINIO_ACCESS_KEY}") {
		t.Error("missing minio access_key env placeholder")
	}
}
```

如果 `template_test.go` 还没有 `import "strings"`，记得在 import 块加入。如果文件不存在，整体新建：

```go
package config

import (
	"strings"
	"testing"
)

func TestGenerateConfigTemplate_HasStorageBlock(t *testing.T) {
	// (同上)
}
```

- [ ] **Step 2: 运行测试，确认失败**

Run: `go test ./internal/config/... -run TestGenerateConfigTemplate_HasStorageBlock -v`
Expected: FAIL，提示 storage 节缺失

- [ ] **Step 3: 在 `internal/config/template.go` 的模板字符串中添加 storage 块**

在 `# 日志配置` 那一节之前插入新的存储块（保持注释解读风格一致）：

```yaml
# 存储抽象层配置
# 默认使用本地磁盘存储（无需任何配置）。如需切换到 MinIO 对象存储，
# 取消以下 minio 块的注释并填入连接信息。
storage:
  # minio:
  #   endpoint: localhost:9000          # MinIO 服务地址（host:port）
  #   access_key: ${MINIO_ACCESS_KEY}   # 访问密钥（建议使用环境变量）
  #   secret_key: ${MINIO_SECRET_KEY}   # 密钥
  #   bucket: groot                     # 存储桶名称
  #   use_ssl: false                    # 是否启用 HTTPS

```

- [ ] **Step 4: 运行测试，确认通过**

Run: `go test ./internal/config/... -run TestGenerateConfigTemplate_HasStorageBlock -v`
Expected: PASS

- [ ] **Step 5: 提交**

```bash
git add internal/config/template.go internal/config/template_test.go
git commit -m "$(cat <<'EOF'
init 配置模板加入 storage 节

模板中默认使用本地磁盘存储，minio 配置以注释形式给出供用户切换。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 8: Memory.Manager 注入 storage 字段

**Files:**
- Modify: `internal/memory/manager.go`
- Modify: `internal/memory/memory_test.go`

- [ ] **Step 1: 修改 `internal/memory/manager.go` 顶部 import 和 `Manager` 结构体**

替换原有 import 块（行 1-14），加上 storage 包：

```go
package memory

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/storage"
)
```

修改 `Manager` 结构体（行 16-22）：

```go
// Manager Memory 接口的实现
type Manager struct {
	memoryDir     string
	retentionDays int
	log           *logger.Logger
	storage       storage.Storage
}
```

- [ ] **Step 2: 修改 `NewManager` 签名（行 23-33）**

```go
// NewManager 创建 Memory Manager
func NewManager(memoryDir string, retentionDays int, log *logger.Logger, store storage.Storage) *Manager {
	// 确保目录存在
	os.MkdirAll(memoryDir, 0755)

	return &Manager{
		memoryDir:     memoryDir,
		retentionDays: retentionDays,
		log:           log,
		storage:       store,
	}
}
```

- [ ] **Step 3: 更新 `internal/memory/memory_test.go` 中所有 `NewManager` 调用**

在文件顶部 import 中加入 `"github.com/zfd81/groot/internal/storage"`，然后将所有 `NewManager(tmpDir, 7, log)` 替换为 `NewManager(tmpDir, 7, log, storage.NewLocal())`。

```bash
# 用 sed 批量替换更稳妥
sed -i '' 's|NewManager(tmpDir, 7, log)|NewManager(tmpDir, 7, log, storage.NewLocal())|g' internal/memory/memory_test.go
```

确认 sed 之后再人工检查 import 是否包含 `storage` 包；如果没有，手动加入。

- [ ] **Step 4: 修改 `cmd/groot/main.go` 中 `memory.NewManager` 调用**

定位到 `cmd/groot/main.go:292`。在调用前先创建 storage：

```go
	// Initialize storage backend
	store, err := storage.New(cfg.Storage)
	if err != nil {
		log.Error("无法初始化存储后端", zap.Error(err))
		os.Exit(1)
	}

	// Initialize memory manager
	memoryDir := config.ResolvePath(cfg.Memory.Directory, homeDir)
	memMgr := memory.NewManager(memoryDir, cfg.Memory.RetentionDays, log, store)
	log.Info("Memory 初始化完成", zap.String("dir", memoryDir))
```

并在 `cmd/groot/main.go` 顶部 import 中加入 `"github.com/zfd81/groot/internal/storage"`。

- [ ] **Step 5: 修改 `internal/cmd/chat.go` 中 `memory.NewManager` 调用**

定位到 `internal/cmd/chat.go:136`。同样改为：

```go
	// Storage backend
	store, err := storage.New(cfg.Storage)
	if err != nil {
		return nil, fmt.Errorf("无法初始化存储后端: %w", err)
	}

	// Memory manager
	memMgr := memory.NewManager(memoryDir, cfg.Memory.RetentionDays, log, store)
```

并在 `internal/cmd/chat.go` 顶部 import 中加入 `"github.com/zfd81/groot/internal/storage"`。

- [ ] **Step 6: 编译验证**

Run: `go build ./...`
Expected: 编译通过

- [ ] **Step 7: 跑现有 memory 测试确保没破坏**

Run: `go test ./internal/memory/... -v`
Expected: 全部 PASS（业务逻辑还没改，只是注入了 storage）

- [ ] **Step 8: 提交**

```bash
git add internal/memory/manager.go internal/memory/memory_test.go cmd/groot/main.go internal/cmd/chat.go
git commit -m "$(cat <<'EOF'
Memory.Manager 注入 storage 字段

NewManager 增加 storage.Storage 参数，启动入口按 cfg.Storage 创建实例并注入。
此提交仅完成依赖注入，业务逻辑迁移在后续提交中进行。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 9: SaveAttachment 改用 storage.Write

**Files:**
- Modify: `internal/memory/manager.go`
- Modify: `internal/memory/memory_test.go`

- [ ] **Step 1: 编写覆盖新行为的测试（如果尚未存在）**

`memory_test.go` 应已有 `TestManager_SaveAttachment` 类测试。打开它确认 / 补充以下用例（如果缺）：

```go
func TestManager_SaveAttachment_WritesViaStorage(t *testing.T) {
	tmpDir := t.TempDir()
	log := initTestLogger()
	mgr := NewManager(tmpDir, 7, log, storage.NewLocal())

	sessionID := "test_save_attach"
	if err := mgr.CreateSession(sessionID); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	content := []byte("hello attachment")
	path, err := mgr.SaveAttachment(sessionID, "report.pdf", content)
	if err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}

	// 验证文件确实落到了预期路径
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if !bytes.Equal(got, content) {
		t.Fatalf("content mismatch: got %q, want %q", got, content)
	}
}
```

如有需要，在文件顶部补充 `"bytes"` import。

- [ ] **Step 2: 修改 `SaveAttachment`（manager.go:291-306）**

将原实现：

```go
func (m *Manager) SaveAttachment(sessionID string, filename string, content []byte) (string, error) {
	// 确保 attachments 目录存在
	os.MkdirAll(m.attachmentsDir(sessionID), 0755)

	safeName := sanitizeFilename(filename)
	fullPath := filepath.Join(m.attachmentsDir(sessionID), safeName)

	if err := os.WriteFile(fullPath, content, 0644); err != nil {
		return "", fmt.Errorf("保存附件失败: %w", err)
	}
	return fullPath, nil
}
```

替换为：

```go
func (m *Manager) SaveAttachment(sessionID string, filename string, content []byte) (string, error) {
	safeName := sanitizeFilename(filename)
	fullPath := filepath.Join(m.attachmentsDir(sessionID), safeName)

	if err := m.storage.Write(
		context.Background(),
		fullPath,
		bytes.NewReader(content),
		int64(len(content)),
		"",
	); err != nil {
		return "", fmt.Errorf("保存附件失败: %w", err)
	}
	return fullPath, nil
}
```

注意：在 `manager.go` 的 import 块中加入 `"bytes"`（如果尚未引入）。

- [ ] **Step 3: 移除 `CreateSession` 中创建 attachments 目录的 `os.MkdirAll`（manager.go:76-78）**

storage.Write 在 local 模式下会自动 mkdir，minio 模式下不需要预创建。删除以下 3 行：

```go
	if err := os.MkdirAll(m.attachmentsDir(sessionID), 0755); err != nil {
		return fmt.Errorf("创建 attachments 目录失败: %w", err)
	}
```

- [ ] **Step 4: 跑测试**

Run: `go test ./internal/memory/... -v`
Expected: 全部 PASS

- [ ] **Step 5: 提交**

```bash
git add internal/memory/manager.go internal/memory/memory_test.go
git commit -m "$(cat <<'EOF'
SaveAttachment 改用 storage.Write 写附件

SaveAttachment 不再直接调用 os.WriteFile，改为通过注入的 storage 实例写入；
CreateSession 也移除显式创建 attachments 目录的逻辑，storage 写入时会自动建目录。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 10: Cleanup 拆分附件与元数据删除

**Files:**
- Modify: `internal/memory/manager.go`
- Modify: `internal/memory/memory_test.go`

- [ ] **Step 1: 编写失败测试**

```go
func TestManager_Cleanup_DeletesAttachmentsViaStorage(t *testing.T) {
	tmpDir := t.TempDir()
	log := initTestLogger()
	mgr := NewManager(tmpDir, 0, log, storage.NewLocal()) // retention=0 → 立即过期

	sessionID := "cleanup_test"
	if err := mgr.CreateSession(sessionID); err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if _, err := mgr.SaveAttachment(sessionID, "x.txt", []byte("hi")); err != nil {
		t.Fatalf("SaveAttachment: %v", err)
	}

	// 把 session 目录的 mtime 调到很久以前以触发清理
	past := time.Now().Add(-24 * time.Hour)
	if err := os.Chtimes(filepath.Join(tmpDir, sessionID), past, past); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	deleted, err := mgr.Cleanup(context.Background())
	if err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 session deleted, got %d", deleted)
	}
	if _, err := os.Stat(filepath.Join(tmpDir, sessionID)); !os.IsNotExist(err) {
		t.Fatalf("session dir should be gone, got: %v", err)
	}
}
```

- [ ] **Step 2: 运行测试，期望编译通过但 Cleanup 行为未变（应仍然 PASS，因为 local 模式下 RemoveAll 已经把附件一起删了）**

Run: `go test ./internal/memory/... -run TestManager_Cleanup_DeletesAttachmentsViaStorage -v`
Expected: PASS

> 此测试在 local 模式下行为不变，但是它锁住了"清理过期 session 后附件目录必须消失"这个不变量；Task 10 重构后此测试仍应通过。

- [ ] **Step 3: 修改 `Cleanup`（manager.go:339-378）**

将原本的：

```go
		if info.ModTime().Before(cutoff) {
			if err := os.RemoveAll(sessionDir); err != nil {
				m.log.Error("清理会话失败: " + sessionID + ", error: " + err.Error())
				continue
			}
			deleted++
			roundCount := m.GetRoundCount(sessionID)
			m.log.Info("清理会话: " + sessionID + ", 最后活跃: " + info.ModTime().Format("2006-01-02") + ", 轮数: " + fmt.Sprintf("%d", roundCount))
		}
```

替换为：

```go
		if info.ModTime().Before(cutoff) {
			roundCount := m.GetRoundCount(sessionID)

			// 步骤 1：通过 storage 抽象删除附件（minio 模式下必须，local 模式下是 no-op）
			if err := m.storage.DeleteDir(ctx, m.attachmentsDir(sessionID)); err != nil {
				m.log.Error("清理会话附件失败: " + sessionID + ", error: " + err.Error())
				continue
			}

			// 步骤 2：删除 session 根目录（含 history.json / chats / SESSION.md，
			// local 模式下 attachments 子目录也会一并删除）
			if err := os.RemoveAll(sessionDir); err != nil {
				m.log.Error("清理会话元数据失败: " + sessionID + ", error: " + err.Error())
				continue
			}
			deleted++
			m.log.Info("清理会话: " + sessionID + ", 最后活跃: " + info.ModTime().Format("2006-01-02") + ", 轮数: " + fmt.Sprintf("%d", roundCount))
		}
```

- [ ] **Step 4: 运行测试**

Run: `go test ./internal/memory/... -v`
Expected: 全部 PASS

- [ ] **Step 5: 编译整个项目**

Run: `go build ./...`
Expected: 通过

- [ ] **Step 6: 跑所有测试**

Run: `go test ./...`
Expected: 全部 PASS

- [ ] **Step 7: 提交**

```bash
git add internal/memory/manager.go internal/memory/memory_test.go
git commit -m "$(cat <<'EOF'
Cleanup 拆分为先删附件后删元数据两步

引入 storage 抽象后附件可能在对象存储里，Cleanup 不能再依赖单一
os.RemoveAll(sessionDir) 一刀切。改为先调 storage.DeleteDir 删附件，
再 os.RemoveAll 删除会话元数据，确保 minio 模式下附件不会泄漏。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 11: 整体回归与 init 模板手测

**Files:**
- 验证：所有

- [ ] **Step 1: 跑完整测试套件**

Run: `go test ./...`
Expected: 全部 PASS

- [ ] **Step 2: 编译 groot 二进制**

Run: `go build -o bin/groot ./cmd/groot`
Expected: 编译通过，`bin/groot` 生成

- [ ] **Step 3: 手测 init 模板**

```bash
mkdir -p /tmp/groot-init-test
cd /tmp/groot-init-test
~/workspace/groot/bin/groot init
grep -A 10 "存储抽象层配置" ~/.groot/config.yaml
```

Expected: 看到 storage 块，并且 minio 段以 `#` 注释形式出现。

- [ ] **Step 4: 验证默认 local 模式启动 OK**

```bash
~/workspace/groot/bin/groot --help
```

Expected: 不会因为 storage 初始化失败而报错。

- [ ] **Step 5: 总结提交（如有微调）**

如果上述手测中发现需要微调，做相应修复并提交单独的 commit。否则跳过。

---

## 自查回顾

**Spec 覆盖**：
- 1.2 能力清单（6 个方法）→ Task 1（接口）+ Task 2（local）+ Task 4（minio）
- 1.3 接口定义 → Task 1
- 1.4 错误约定（ErrNotFound / ErrIsDir / wrap）→ Task 1（定义）+ Task 2（local 映射）+ Task 4（minio 映射）
- 1.5 path 约定（local 必须绝对、minio 直接当 key）→ Task 2（`ensureAbs`）+ Task 4（`PutObject` 直接传 path）
- 1.6 配置结构 → Task 5（结构体）+ Task 6（factory）+ Task 7（init 模板）
- 1.7 目录结构 → Task 1/2/4/6（4 个文件就位）
- 1.8 流式设计 → Task 2（`io.Copy`）+ Task 4（`PutObject` 透传）
- 2.1.1 storage 包本体 → Task 1/2/4/6
- 2.1.2 配置层 → Task 5/7 + Task 3（go.mod）
- 2.1.3 现有附件读写迁移 → Task 8（注入字段、改 main.go/chat.go）+ Task 9（SaveAttachment / CreateSession）+ Task 10（Cleanup 拆分）

**类型一致性**：
- `Storage` 接口签名在 Task 1 定义、Task 2/4 实现，参数与返回类型完全一致
- `config.StorageConfig` 在 Task 5 定义，Task 6（factory）和 Task 8（main.go / chat.go）使用同一类型
- `NewManager(memoryDir, retentionDays, log, store)` 四参签名在 Task 8 定义，Task 9/10 测试中也按此调用

**Placeholder 扫描**：
- 所有代码块都是完整可粘贴的实现
- 所有命令都给了具体可执行的形式
- 没有 "TBD" / "TODO" / "implement later"

---

**Plan complete and saved to `docs/superpowers/plans/2026-06-06-storage-interface.md`. Two execution options:**

**1. Subagent-Driven (recommended)** — I dispatch a fresh subagent per task, review between tasks, fast iteration

**2. Inline Execution** — Execute tasks in this session using executing-plans, batch execution with checkpoints

**Which approach?**
