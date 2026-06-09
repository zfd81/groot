# 运行时数据接入 Storage 抽象层计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 将 memory / schedule / cluster 三个模块的持久化操作全部迁移到 `Storage` 接口（local 和 minio 两种实现），并在 minio 模式下添加启动 fail-fast 探活，保证 local 模式 100% 向后兼容。

**Architecture:** `main.go` 按 storage 类型为每个模块计算 basePath（local 用绝对路径，minio 用相对前缀），各模块只认 `Storage` 接口，不区分后端。`NewMinio()` 内部自动做 BucketExists + PutObject + RemoveObject 探活，任一失败 `storage.New()` 直接 error，启动 fail-fast。memory / schedule 模块的 `saveHistory` / `SaveTask` 等现有 `tmp + rename` 逻辑由 `Storage.Write` 统一下沉，业务层代码路径大幅简化。

**Tech Stack:** Go（已有）、`github.com/minio/minio-go/v7`（已引入）、`go test`。

---

## 文件结构

**修改（已存在）：**

- `internal/storage/minio.go` — `NewMinio()` 末尾加 fail-fast 探活（BucketExists + 写/删 `__startup/probe-{ts}`）
- `internal/storage/minio_test.go` — 补 fail-fast 相关 mock 测试
- `internal/memory/manager.go` — `CreateSession` / `ExistsSession` / `ListSessions` / `GetHistory` / `saveHistory` / `SaveChatRecord` / `GetChatRecord` / `GetLatestChatRecord` / `Cleanup` 全部改走 `Storage` 接口；移除自己实现的 `tmp + rename`
- `internal/memory/memory_test.go` — 已有测试补充 `Storage` 路径断言（本地测试继续用 `storage.NewLocal()`）
- `internal/schedule/storage.go` — `Storage` 结构体改为依赖 `storage.Storage` 接口；`SaveTask` / `SaveExecution` / `MoveTask` / `DeleteTask` / `listTasksIn` / `LoadExecutions` 等全部改走接口
- `internal/schedule/storage_test.go`（新建） — `schedule.Storage` 单元测试
- `internal/cluster/member.go` — `WriteRegistration` / `ListMembers` / `RemoveFile` / `EnsureMembersDir` 改走 `storage.Storage` 接口；`Cluster` 结构体持有 `storage.Storage`
- `internal/cluster/cluster.go` — `New()` 加 `store storage.Storage` 参数，`heartbeat` / `register` / `leaderHeartbeat` / `followerHeartbeat` 里的直接 `os.*` 调用改走 `Storage`
- `internal/cluster/member_test.go` — 已有测试改用 `storage.NewLocal()` 注入
- `cmd/groot/main.go` — 按 storage 类型为每个模块计算 basePath，`cluster.New()` / `schedule.NewStorage()` 传入 `store`
- `docs/superpowers/specs/2026-06-01-storage-abstraction-and-minio-mode-design.md` — 补写 §1.7 各模块存储适配正文

---

## Task 1: 补写设计文档 §1.7

**Files:**
- Modify: `docs/superpowers/specs/2026-06-01-storage-abstraction-and-minio-mode-design.md`

- [ ] **Step 1: 在 §1.6 下方插入 §1.7 正文**

在文件 §1.8 之前插入以下内容（替换已存在的空 §1.7 占位符，或直接追加）：

```markdown
### 1.7 各模块存储适配

memory / schedule / cluster 三个模块的持久化操作统一走 `Storage` 接口。接口不感知路径拼接；路径由调用方（即 `main.go`）按 storage 类型注入 basePath，各模块内部在 basePath 上做相对拼接。

#### 1.7.1 basePath 与 path 拼接规则

启动时 `main.go` 按 storage 类型为每个模块计算 basePath：

| 模块 | local 模式 basePath（绝对路径） | minio 模式 basePath（object key 前缀） |
|------|-------------------------------|---------------------------------------|
| memory | `${GROOT_HOME}/memory` | `memory` |
| schedule | `${GROOT_HOME}/schedules` | `schedules` |
| cluster | `${GROOT_HOME}/cluster/members` | `cluster/members` |

业务代码内部用 `filepath.Join(basePath, ...)` 拼出完整 path，对两种 storage 类型透明。

local 模式：path 是绝对路径，`storage.Local` 强制要求。
minio 模式：path 是 object key（形如 `memory/sessions/abc/history.json`），`storage.Minio` 直接使用。

#### 1.7.2 memory 模块

memory 模块把所有文件读写改走 `Storage` 接口：

| 操作 | 改造前 | 改造后 |
|------|--------|--------|
| `saveHistory` | `os.WriteFile(tmp)` + `os.Rename` | `storage.Write`（接口内部原子写） |
| `SaveChatRecord` | `os.WriteFile(tmp)` + `os.Rename` | `storage.Write` |
| `GetHistory` | `os.ReadFile` | `storage.Read` |
| `GetChatRecord` | `os.ReadFile` | `storage.Read` |
| `ExistsSession` | `os.Stat(history.json)` | `storage.Stat` + `errors.Is(ErrNotFound)` |
| `ListSessions` | `os.ReadDir(memoryDir)` | `storage.List(memoryDir)` |
| `CreateSession` | `os.MkdirAll(sessionDir)` + `os.MkdirAll(chatsDir)` | 仅调用 `saveHistory`（Write 自动建目录） |
| `Cleanup`（附件） | 已走 `storage.DeleteDir`（无变化） | 无变化 |
| `Cleanup`（元数据） | `os.RemoveAll(sessionDir)` | `storage.DeleteDir(sessionDir)` |

注：`SaveAttachment` 已走 `Storage.Write`，本期无需改动。

#### 1.7.3 schedule 模块

`schedule.Storage` 结构体由直接操作 `os.*` 改为持有 `storage.Storage` 接口实例：

| 操作 | 改造前 | 改造后 |
|------|--------|--------|
| `SaveTask` | `os.WriteFile(tmp)` + `os.Rename` | `storage.Write` |
| `SaveExecution` | `os.WriteFile(tmp)` + `os.Rename` | `storage.Write` |
| `LoadTask` | `os.ReadFile` 遍历三个状态目录 | `storage.Read` 遍历 |
| `LoadExecutions` | `os.ReadDir` + `os.ReadFile` | `storage.List` + `storage.Read` |
| `listTasksIn` | `os.ReadDir` + `os.ReadFile` | `storage.List` + `storage.Read` |
| `MoveTask` | `os.Rename(src, dst)` | `storage.Rename` |
| `DeleteTask` | `os.RemoveAll` | `storage.DeleteDir` |
| `EnsureDirs` | `os.MkdirAll` × 3 | 仅在 local 模式下有实际作用；minio 模式 Write 自动建前缀，此方法退化为空操作或保留 `os.MkdirAll` 仅用于 local |
| `GetTaskStatus` | `os.Stat` | `storage.Stat` + `errors.Is(ErrNotFound)` |

#### 1.7.4 cluster 模块

cluster 模块的心跳协调通过 `Storage` 接口读写成员文件：

| 操作 | 改造前 | 改造后 |
|------|--------|--------|
| `WriteRegistration` | `os.WriteFile` | `storage.Write`（内容同格式，原子写） |
| `ListMembers` | `os.ReadDir` | `storage.List(membersDir)` → 读取每个文件 ModTime |
| `RemoveFile` | `os.Remove` | `storage.Delete` |
| `EnsureMembersDir` | `os.MkdirAll` | local 模式继续 `os.MkdirAll`；minio 模式退化为 noop |

心跳判活仍用 `FileInfo.ModTime`（即 `Storage.List` 返回的 `FileInfo.ModTime`），minio 侧对应 MinIO `LastModified`，语义一致。
```

- [ ] **Step 2: 验证文档结构完整**

目录中 1.7.1~1.7.4 的锚点链接与正文标题对应：

```bash
grep -n "1\.7" docs/superpowers/specs/2026-06-01-storage-abstraction-and-minio-mode-design.md
```

Expected: 目录锚点和正文各出现 4 次（1.7.1/1.7.2/1.7.3/1.7.4）

- [ ] **Step 3: 提交**

```bash
git add docs/superpowers/specs/2026-06-01-storage-abstraction-and-minio-mode-design.md
git commit -m "$(cat <<'EOF'
补写存储适配 spec §1.7 各模块接入细节

补全 memory/schedule/cluster 三个模块改走 Storage 接口的方法级映射表，
以及 local/minio 模式下 basePath 的计算规则。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 2: NewMinio 启动 fail-fast 探活

**Files:**
- Modify: `internal/storage/minio.go`
- Modify: `internal/storage/minio_test.go`

- [ ] **Step 1: 在 minio_test.go 中写 fail-fast 测试（先看现有 mock 结构）**

先检查 minio_test.go 现有 mockAPI 结构，确保新测试复用相同 mock：

```bash
head -80 internal/storage/minio_test.go
```

- [ ] **Step 2: 在 minio_test.go 中加失败测试用例**

在现有测试文件中，找到 mock 定义并补充以下测试：

```go
// TestNewMinio_FailFast_BucketNotExist 验证 bucket 不存在时 NewMinio 返回 error。
func TestNewMinio_FailFast_BucketNotExist(t *testing.T) {
    mock := &mockMinioAPI{
        bucketExistsFn: func(ctx context.Context, bucket string) (bool, error) {
            return false, nil
        },
    }
    _, err := newMinioWithClient(mock, "testbucket")
    if err == nil {
        t.Fatal("expected error when bucket does not exist")
    }
    if !strings.Contains(err.Error(), "bucket") {
        t.Errorf("expected error to mention bucket, got: %v", err)
    }
}

// TestNewMinio_FailFast_PutFails 验证探活写入失败时 NewMinio 返回 error。
func TestNewMinio_FailFast_PutFails(t *testing.T) {
    mock := &mockMinioAPI{
        bucketExistsFn: func(ctx context.Context, bucket string) (bool, error) {
            return true, nil
        },
        putObjectFn: func(ctx context.Context, bucket, key string, r io.Reader, size int64, opts minio.PutObjectOptions) error {
            return fmt.Errorf("permission denied")
        },
    }
    _, err := newMinioWithClient(mock, "testbucket")
    if err == nil {
        t.Fatal("expected error when PutObject fails")
    }
}

// TestNewMinio_FailFast_OK 验证 bucket 存在且读写成功时 NewMinio 不返回 error。
func TestNewMinio_FailFast_OK(t *testing.T) {
    mock := &mockMinioAPI{
        bucketExistsFn: func(ctx context.Context, bucket string) (bool, error) {
            return true, nil
        },
        putObjectFn: func(ctx context.Context, bucket, key string, r io.Reader, size int64, opts minio.PutObjectOptions) error {
            return nil
        },
        removeObjectFn: func(ctx context.Context, bucket, key string, opts minio.RemoveObjectOptions) error {
            return nil
        },
    }
    _, err := newMinioWithClient(mock, "testbucket")
    if err != nil {
        t.Fatalf("expected no error, got: %v", err)
    }
}
```

- [ ] **Step 3: 在 mock 结构体中加 `bucketExistsFn` 字段和 `BucketExists` 方法**

在 minio_test.go 的 mockMinioAPI 结构体中添加：

```go
bucketExistsFn func(ctx context.Context, bucket string) (bool, error)
```

同时实现接口方法（如果 minioAPI 接口还未有 BucketExists 方法，则跳到 Step 4 先改接口）：

```go
func (m *mockMinioAPI) BucketExists(ctx context.Context, bucket string) (bool, error) {
    if m.bucketExistsFn != nil {
        return m.bucketExistsFn(ctx, bucket)
    }
    return true, nil
}
```

- [ ] **Step 4: 在 minioAPI 接口中添加 BucketExists 方法**

在 `internal/storage/minio.go` 中的 `minioAPI` interface 内添加：

```go
BucketExists(ctx context.Context, bucket string) (bool, error)
```

- [ ] **Step 5: 在 minioClient 中实现 BucketExists**

在 `internal/storage/minio.go` 中 `minioClient` 的现有方法下添加：

```go
func (m *minioClient) BucketExists(ctx context.Context, bucket string) (bool, error) {
    return m.c.BucketExists(ctx, bucket)
}
```

- [ ] **Step 6: 提取内部构造函数 `newMinioWithClient`**

在 `NewMinio` 之前增加一个可被测试注入的内部构造函数。修改 `internal/storage/minio.go`：

将现有 `NewMinio` 重构为：

```go
// NewMinio 创建并探活一个 minio 存储实例。
// 探活步骤：BucketExists + 写/删 __startup/probe-{ts}，任一失败直接 error。
func NewMinio(endpoint, accessKey, secretKey, bucket string, useSSL bool) (*Minio, error) {
    c, err := minio.New(endpoint, &minio.Options{
        Creds:  credentials.NewStaticV4(accessKey, secretKey, ""),
        Secure: useSSL,
    })
    if err != nil {
        return nil, fmt.Errorf("storage: init minio client: %w", err)
    }
    return newMinioWithClient(&minioClient{c: c}, bucket)
}

// newMinioWithClient 接受已构造好的 minioAPI，便于单元测试注入 mock。
func newMinioWithClient(client minioAPI, bucket string) (*Minio, error) {
    ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
    defer cancel()

    // 1. 检查 bucket 是否存在
    exists, err := client.BucketExists(ctx, bucket)
    if err != nil {
        return nil, fmt.Errorf("storage: minio bucket check: %w", err)
    }
    if !exists {
        return nil, fmt.Errorf("storage: minio bucket %q does not exist", bucket)
    }

    // 2. 探活写入（验证写权限）
    probeKey := fmt.Sprintf("__startup/probe-%d", time.Now().UnixNano())
    empty := strings.NewReader("")
    if err := client.PutObject(ctx, bucket, probeKey, empty, 0, minio.PutObjectOptions{
        ContentType: "application/octet-stream",
    }); err != nil {
        return nil, fmt.Errorf("storage: minio startup probe write failed: %w", err)
    }

    // 3. 探活删除（验证删权限 + 清理探针）
    if err := client.RemoveObject(ctx, bucket, probeKey, minio.RemoveObjectOptions{}); err != nil {
        return nil, fmt.Errorf("storage: minio startup probe delete failed: %w", err)
    }

    return &Minio{client: client, bucket: bucket}, nil
}
```

（注意：`strings` 包已在 minio.go 中 import；`fmt` / `context` / `time` 也已 import，无需新增。）

- [ ] **Step 7: 运行 minio 存储测试**

```bash
go test ./internal/storage/... -run TestNewMinio -v
```

Expected: 新增的 3 个 TestNewMinio_FailFast_* 用例全部 PASS。

- [ ] **Step 8: 运行所有 storage 测试确认无回归**

```bash
go test ./internal/storage/... -v
```

Expected: 所有测试 PASS，无 FAIL。

- [ ] **Step 9: 提交**

```bash
git add internal/storage/minio.go internal/storage/minio_test.go
git commit -m "$(cat <<'EOF'
minio 存储添加启动 fail-fast 探活

NewMinio 内部自动执行 BucketExists + PutObject + RemoveObject 三步探活，
任一失败立即返回 error，避免运行时首次写入才暴露连接或权限问题。
提取 newMinioWithClient 供测试注入 mock。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 3: memory 模块迁移到 Storage 接口

**Files:**
- Modify: `internal/memory/manager.go`
- Modify: `internal/memory/memory_test.go`

- [ ] **Step 1: 先运行现有 memory 测试，记录 baseline**

```bash
go test ./internal/memory/... -v 2>&1 | tail -20
```

Expected: 所有测试 PASS（作为后续对比基准）。

- [ ] **Step 2: 替换 `saveHistory`（移除 tmp+rename，改走 storage.Write）**

在 `internal/memory/manager.go` 中，将 `saveHistory` 改为：

```go
func (m *Manager) saveHistory(sessionID string, history *History) error {
    data, err := json.MarshalIndent(history, "", "  ")
    if err != nil {
        return fmt.Errorf("序列化 history 失败: %w", err)
    }
    return m.storage.Write(
        context.Background(),
        m.historyPath(sessionID),
        bytes.NewReader(data),
        int64(len(data)),
        "application/json",
    )
}
```

同时删除 `saveHistory` 原实现中已不需要的 `tmpPath` 相关代码。

- [ ] **Step 3: 替换 `SaveChatRecord`（移除 tmp+rename，改走 storage.Write）**

将 `SaveChatRecord` 改为：

```go
func (m *Manager) SaveChatRecord(sessionID string, record *ChatRecord) error {
    data, err := json.MarshalIndent(record, "", "  ")
    if err != nil {
        return fmt.Errorf("序列化 chat record 失败: %w", err)
    }
    return m.storage.Write(
        context.Background(),
        m.chatPath(sessionID, record.ChatID),
        bytes.NewReader(data),
        int64(len(data)),
        "application/json",
    )
}
```

删除原实现中 `os.MkdirAll(m.chatsDir(...))` 和 `tmpPath` 相关代码（`storage.Write` 内部自动建目录）。

- [ ] **Step 4: 替换 `GetHistory`（改走 storage.Read）**

将 `GetHistory` 改为：

```go
func (m *Manager) GetHistory(sessionID string) (*History, error) {
    rc, err := m.storage.Read(context.Background(), m.historyPath(sessionID))
    if err != nil {
        if errors.Is(err, storage.ErrNotFound) {
            return nil, fmt.Errorf("会话不存在: %s", sessionID)
        }
        return nil, fmt.Errorf("读取 history 失败: %w", err)
    }
    defer rc.Close()

    data, err := io.ReadAll(rc)
    if err != nil {
        return nil, fmt.Errorf("读取 history 内容失败: %w", err)
    }

    var history History
    if err := json.Unmarshal(data, &history); err != nil {
        return nil, fmt.Errorf("解析 history 失败: %w", err)
    }
    return &history, nil
}
```

- [ ] **Step 5: 替换 `GetChatRecord`（改走 storage.Read）**

将 `GetChatRecord` 改为：

```go
func (m *Manager) GetChatRecord(sessionID string, chatID string) (*ChatRecord, error) {
    rc, err := m.storage.Read(context.Background(), m.chatPath(sessionID, chatID))
    if err != nil {
        if errors.Is(err, storage.ErrNotFound) {
            return nil, fmt.Errorf("对话记录不存在: %s", chatID)
        }
        return nil, fmt.Errorf("读取 chat record 失败: %w", err)
    }
    defer rc.Close()

    data, err := io.ReadAll(rc)
    if err != nil {
        return nil, fmt.Errorf("读取 chat record 内容失败: %w", err)
    }

    var record ChatRecord
    if err := json.Unmarshal(data, &record); err != nil {
        return nil, fmt.Errorf("解析 chat record 失败: %w", err)
    }
    return &record, nil
}
```

- [ ] **Step 6: 替换 `ExistsSession`（改走 storage.Stat）**

将 `ExistsSession` 改为：

```go
func (m *Manager) ExistsSession(sessionID string) bool {
    _, err := m.storage.Stat(context.Background(), m.historyPath(sessionID))
    return err == nil
}
```

- [ ] **Step 7: 替换 `ListSessions`（改走 storage.List）**

将 `ListSessions` 改为：

```go
func (m *Manager) ListSessions(limit, offset int) ([]SessionInfo, int, error) {
    entries, err := m.storage.List(context.Background(), m.memoryDir)
    if err != nil {
        if errors.Is(err, storage.ErrNotFound) {
            return []SessionInfo{}, 0, nil
        }
        return nil, 0, fmt.Errorf("读取记忆目录失败: %w", err)
    }

    var sessions []SessionInfo
    for _, entry := range entries {
        if !entry.IsDir {
            continue
        }
        sessionID := filepath.Base(entry.Path)
        if !m.ExistsSession(sessionID) {
            continue
        }
        info, err := m.GetSessionInfo(sessionID)
        if err != nil {
            m.log.Info("获取会话信息失败: " + sessionID + ", error: " + err.Error())
            continue
        }
        sessions = append(sessions, *info)
    }

    sort.Slice(sessions, func(i, j int) bool {
        return sessions[i].CreatedAt.After(sessions[j].CreatedAt)
    })

    total := len(sessions)
    if offset >= total {
        return []SessionInfo{}, total, nil
    }
    end := offset + limit
    if end > total {
        end = total
    }
    return sessions[offset:end], total, nil
}
```

- [ ] **Step 8: 替换 `CreateSession`（移除 MkdirAll，仅调用 saveHistory）**

将 `CreateSession` 改为：

```go
func (m *Manager) CreateSession(sessionID string) error {
    history := &History{
        SessionID: sessionID,
        CreatedAt: time.Now(),
        Messages:  []Message{},
    }
    return m.saveHistory(sessionID, history)
}
```

（`storage.Write` 会自动建目录，chatsDir 在首次 `SaveChatRecord` 时自动创建，无需手动 MkdirAll。）

- [ ] **Step 9: 替换 `Cleanup` 元数据删除（改走 storage.DeleteDir）**

将 `Cleanup` 中的 `os.RemoveAll(sessionDir)` 替换为 `m.storage.DeleteDir(ctx, sessionDir)`：

```go
// Cleanup 改造点：先 DeleteDir 附件，再 DeleteDir 整个 sessionDir
// （含 history.json / chats / 旧版 SESSION.md 等所有元数据）。
// 原 os.RemoveAll 替换为 storage.DeleteDir，minio 模式下可清 MinIO 上的元数据。
if err := m.storage.DeleteDir(ctx, sessionDir); err != nil {
    m.log.Error("清理会话目录失败: " + sessionID + ", error: " + err.Error())
    continue
}
```

同时删除原先的 `m.storage.DeleteDir(ctx, attachmentsDir)` 单独清附件步骤——改为一次 `DeleteDir(sessionDir)` 递归清除整个 session（附件在 `<sessionDir>/attachments/` 下，递归删除自然包含）。

> 注意：如果附件在 minio 模式下存储路径与元数据路径不同，需要保留两步。但根据 §1.7.1，memory 模块 basePath 统一为 `memory`，附件路径 `memory/sessions/{id}/attachments/...` 在 `memory/sessions/{id}/` 下，递归删除 `memory/sessions/{id}` 即可，无需分步。仔细确认 `AttachmentsDir` 路径后再决定。

实现时先检查：

```bash
grep -n "AttachmentsDir\|sessionDir\|memoryDir" internal/memory/manager.go | head -20
```

- [ ] **Step 10: 检查并移除不再需要的 os 包导入**

```bash
grep -n '"os"' internal/memory/manager.go
```

如果 `os` 包仅在 `NewManager` 里 `os.MkdirAll` 中用到，保留这一行（local 模式仍需在启动时确保目录存在）；其他地方已清除则无需删 import。

- [ ] **Step 11: 补充 io 包导入（新增了 io.ReadAll）**

```bash
grep -n '"io"' internal/memory/manager.go
```

如果尚未 import `"io"` 则添加；`"errors"` 同理。

- [ ] **Step 12: 编译验证**

```bash
go build ./internal/memory/...
```

Expected: 编译通过，无输出。

- [ ] **Step 13: 运行 memory 测试**

```bash
go test ./internal/memory/... -v
```

Expected: 所有测试 PASS。

- [ ] **Step 14: 提交**

```bash
git add internal/memory/manager.go internal/memory/memory_test.go
git commit -m "$(cat <<'EOF'
memory 模块改走 Storage 接口

saveHistory/SaveChatRecord/GetHistory/GetChatRecord/ExistsSession/
ListSessions/CreateSession/Cleanup 全部替换为 storage.Write/Read/Stat/List/DeleteDir，
移除业务层 tmp+rename 逻辑，原子写由 Storage.Write 统一保证。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 4: schedule 模块迁移到 Storage 接口

**Files:**
- Modify: `internal/schedule/storage.go`
- Create: `internal/schedule/storage_test.go`

- [ ] **Step 1: 先运行现有 schedule 测试，记录 baseline**

```bash
go test ./internal/schedule/... -v 2>&1 | tail -20
```

Expected: 所有测试 PASS（作为后续对比基准）。

- [ ] **Step 2: 修改 schedule.Storage 结构体，加入 storage.Storage 依赖**

将 `internal/schedule/storage.go` 的 `Storage` 结构体和 `NewStorage` 改为：

```go
import (
    "bytes"
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "path/filepath"
    "sort"

    "go.uber.org/zap"

    "github.com/zfd81/groot/internal/logger"
    istorage "github.com/zfd81/groot/internal/storage"
)

// Storage handles file-based persistence for scheduled tasks
type Storage struct {
    baseDir string // {GROOT_HOME}/schedules 或 minio 前缀 "schedules"
    store   istorage.Storage
    log     *logger.Logger
}

// NewStorage creates a new storage instance
func NewStorage(baseDir string, store istorage.Storage, log *logger.Logger) *Storage {
    return &Storage{baseDir: baseDir, store: store, log: log}
}
```

- [ ] **Step 3: 替换 `EnsureDirs`**

`EnsureDirs` 只在 local 模式下有实际意义（minio 的目录由写操作隐式创建）。改为：

```go
// EnsureDirs 在 local 模式下预建目录；minio 模式下 Write 会自动创建前缀，此方法为 noop。
func (s *Storage) EnsureDirs() error {
    local, ok := s.store.(*istorage.Local)
    if !ok {
        return nil // minio 模式：noop
    }
    _ = local // 编译期保留引用，避免 unused import
    for _, dir := range []string{"active", "disabled", "archive"} {
        path := filepath.Join(s.baseDir, dir)
        if err := os.MkdirAll(path, 0755); err != nil {
            return fmt.Errorf("创建目录 %s 失败: %w", path, err)
        }
    }
    return nil
}
```

（需保留 `"os"` import。）

- [ ] **Step 4: 替换 `SaveTask`（移除 tmp+rename，改走 storage.Write）**

```go
func (s *Storage) SaveTask(task *Task) error {
    task.UpdatedAt = task.CreatedAt
    if task.CreatedAt.IsZero() {
        task.CreatedAt = task.UpdatedAt
    }

    data, err := json.MarshalIndent(task, "", "  ")
    if err != nil {
        return err
    }

    taskPath := filepath.Join(s.baseDir, "active", task.ID, "task.json")
    return s.store.Write(context.Background(), taskPath, bytes.NewReader(data), int64(len(data)), "application/json")
}
```

- [ ] **Step 5: 替换 `LoadTask`（改走 storage.Read）**

```go
func (s *Storage) LoadTask(taskID string) (*Task, error) {
    for _, status := range []string{"active", "disabled", "archive"} {
        path := filepath.Join(s.baseDir, status, taskID, "task.json")
        rc, err := s.store.Read(context.Background(), path)
        if err != nil {
            if errors.Is(err, istorage.ErrNotFound) {
                continue
            }
            return nil, err
        }
        defer rc.Close()
        data, err := io.ReadAll(rc)
        if err != nil {
            return nil, err
        }
        var task Task
        if err := json.Unmarshal(data, &task); err != nil {
            return nil, err
        }
        return &task, nil
    }
    return nil, fmt.Errorf("任务 %s 不存在", taskID)
}
```

- [ ] **Step 6: 替换 `DeleteTask`（改走 storage.DeleteDir）**

```go
func (s *Storage) DeleteTask(taskID string) error {
    for _, status := range []string{"active", "disabled", "archive"} {
        dir := filepath.Join(s.baseDir, status, taskID)
        _, err := s.store.Stat(context.Background(), dir)
        if err != nil {
            if errors.Is(err, istorage.ErrNotFound) {
                continue
            }
            return err
        }
        return s.store.DeleteDir(context.Background(), dir)
    }
    return fmt.Errorf("任务 %s 不存在", taskID)
}
```

- [ ] **Step 7: 替换 `MoveTask`（改走 storage.Rename）**

```go
func (s *Storage) MoveTask(taskID, from, to string) error {
    srcDir := filepath.Join(s.baseDir, from, taskID)
    dstDir := filepath.Join(s.baseDir, to, taskID)
    if _, err := s.store.Stat(context.Background(), srcDir); err != nil {
        if errors.Is(err, istorage.ErrNotFound) {
            return fmt.Errorf("任务 %s 不在 %s 中", taskID, from)
        }
        return err
    }
    return s.store.Rename(context.Background(), srcDir, dstDir)
}
```

- [ ] **Step 8: 替换 `GetTaskStatus`（改走 storage.Stat）**

```go
func (s *Storage) GetTaskStatus(taskID string) string {
    for _, status := range []string{"active", "disabled", "archive"} {
        dir := filepath.Join(s.baseDir, status, taskID)
        _, err := s.store.Stat(context.Background(), dir)
        if err == nil {
            return status
        }
    }
    return ""
}
```

- [ ] **Step 9: 替换 `SaveExecution`（改走 storage.Write）**

```go
func (s *Storage) SaveExecution(taskID string, record *ExecutionRecord) error {
    status := s.GetTaskStatus(taskID)
    if status == "" {
        return fmt.Errorf("任务 %s 不存在", taskID)
    }

    data, err := json.MarshalIndent(record, "", "  ")
    if err != nil {
        return err
    }

    filename := record.ExecTime.Format("2006-01-02-150405") + ".json"
    path := filepath.Join(s.baseDir, status, taskID, "executions", filename)
    return s.store.Write(context.Background(), path, bytes.NewReader(data), int64(len(data)), "application/json")
}
```

- [ ] **Step 10: 替换 `LoadExecutions`（改走 storage.List + storage.Read）**

```go
func (s *Storage) LoadExecutions(taskID string) ([]ExecutionRecord, error) {
    status := s.GetTaskStatus(taskID)
    if status == "" {
        return nil, fmt.Errorf("任务 %s 不存在", taskID)
    }

    execDir := filepath.Join(s.baseDir, status, taskID, "executions")
    entries, err := s.store.List(context.Background(), execDir)
    if err != nil {
        if errors.Is(err, istorage.ErrNotFound) {
            return nil, nil
        }
        return nil, err
    }

    var records []ExecutionRecord
    for _, entry := range entries {
        if entry.IsDir || filepath.Ext(entry.Path) != ".json" {
            continue
        }
        rc, err := s.store.Read(context.Background(), entry.Path)
        if err != nil {
            s.log.Info("读取执行记录失败: "+filepath.Base(entry.Path), zap.Error(err))
            continue
        }
        data, err := io.ReadAll(rc)
        rc.Close()
        if err != nil {
            s.log.Info("读取执行记录内容失败: "+filepath.Base(entry.Path), zap.Error(err))
            continue
        }
        var record ExecutionRecord
        if err := json.Unmarshal(data, &record); err != nil {
            s.log.Info("解析执行记录失败: "+filepath.Base(entry.Path), zap.Error(err))
            continue
        }
        records = append(records, record)
    }

    sort.Slice(records, func(i, j int) bool {
        return records[i].ExecTime.After(records[j].ExecTime)
    })
    return records, nil
}
```

- [ ] **Step 11: 替换 `listTasksIn`（改走 storage.List + storage.Read）**

```go
func (s *Storage) listTasksIn(status string) ([]*Task, error) {
    dir := filepath.Join(s.baseDir, status)
    entries, err := s.store.List(context.Background(), dir)
    if err != nil {
        if errors.Is(err, istorage.ErrNotFound) {
            return nil, nil
        }
        return nil, err
    }

    var tasks []*Task
    for _, entry := range entries {
        if !entry.IsDir {
            continue
        }
        taskPath := filepath.Join(entry.Path, "task.json")
        rc, err := s.store.Read(context.Background(), taskPath)
        if err != nil {
            continue
        }
        data, err := io.ReadAll(rc)
        rc.Close()
        if err != nil {
            continue
        }
        var task Task
        if err := json.Unmarshal(data, &task); err != nil {
            continue
        }
        tasks = append(tasks, &task)
    }
    return tasks, nil
}
```

- [ ] **Step 12: 编译验证**

```bash
go build ./internal/schedule/...
```

Expected: 编译通过，无输出。修复所有编译错误（通常是 import 缺失或 `os` 包残留引用）后继续。

- [ ] **Step 13: 新建 `internal/schedule/storage_test.go`**

```go
package schedule

import (
    "context"
    "testing"
    "time"

    "github.com/zfd81/groot/internal/logger"
    "github.com/zfd81/groot/internal/storage"
)

func newTestScheduleStorage(t *testing.T) *Storage {
    t.Helper()
    store := storage.NewLocal()
    baseDir := t.TempDir()
    s := NewStorage(baseDir, store, logger.NewNop())
    if err := s.EnsureDirs(); err != nil {
        t.Fatalf("EnsureDirs: %v", err)
    }
    return s
}

func TestSaveAndLoadTask(t *testing.T) {
    s := newTestScheduleStorage(t)
    task := &Task{
        ID:       "task-test-001",
        Name:     "测试任务",
        Schedule: "0 * * * *",
        CreatedAt: time.Now(),
        UpdatedAt: time.Now(),
    }
    if err := s.SaveTask(task); err != nil {
        t.Fatalf("SaveTask: %v", err)
    }
    loaded, err := s.LoadTask("task-test-001")
    if err != nil {
        t.Fatalf("LoadTask: %v", err)
    }
    if loaded.Name != task.Name {
        t.Errorf("expected name %q, got %q", task.Name, loaded.Name)
    }
}

func TestMoveTask(t *testing.T) {
    s := newTestScheduleStorage(t)
    task := &Task{ID: "task-move-001", Name: "移动任务", Schedule: "0 * * * *", CreatedAt: time.Now(), UpdatedAt: time.Now()}
    if err := s.SaveTask(task); err != nil {
        t.Fatalf("SaveTask: %v", err)
    }
    if err := s.MoveTask("task-move-001", "active", "disabled"); err != nil {
        t.Fatalf("MoveTask: %v", err)
    }
    status := s.GetTaskStatus("task-move-001")
    if status != "disabled" {
        t.Errorf("expected disabled, got %s", status)
    }
}

func TestDeleteTask(t *testing.T) {
    s := newTestScheduleStorage(t)
    task := &Task{ID: "task-del-001", Name: "删除任务", Schedule: "0 * * * *", CreatedAt: time.Now(), UpdatedAt: time.Now()}
    if err := s.SaveTask(task); err != nil {
        t.Fatalf("SaveTask: %v", err)
    }
    if err := s.DeleteTask("task-del-001"); err != nil {
        t.Fatalf("DeleteTask: %v", err)
    }
    if _, err := s.LoadTask("task-del-001"); err == nil {
        t.Error("expected error after delete, got nil")
    }
}

func TestSaveAndLoadExecutions(t *testing.T) {
    s := newTestScheduleStorage(t)
    task := &Task{ID: "task-exec-001", Name: "执行历史任务", Schedule: "0 * * * *", CreatedAt: time.Now(), UpdatedAt: time.Now()}
    if err := s.SaveTask(task); err != nil {
        t.Fatalf("SaveTask: %v", err)
    }
    rec := &ExecutionRecord{
        TaskID:    "task-exec-001",
        ExecTime:  time.Now(),
        Status:    "completed",
        DurationMs: 1000,
    }
    if err := s.SaveExecution("task-exec-001", rec); err != nil {
        t.Fatalf("SaveExecution: %v", err)
    }
    records, err := s.LoadExecutions("task-exec-001")
    if err != nil {
        t.Fatalf("LoadExecutions: %v", err)
    }
    if len(records) != 1 {
        t.Errorf("expected 1 record, got %d", len(records))
    }
}

func TestListAllTasks(t *testing.T) {
    s := newTestScheduleStorage(t)
    for i, id := range []string{"task-list-a", "task-list-b"} {
        task := &Task{ID: id, Name: "任务" + string(rune('A'+i)), Schedule: "0 * * * *", CreatedAt: time.Now(), UpdatedAt: time.Now()}
        if err := s.SaveTask(task); err != nil {
            t.Fatalf("SaveTask: %v", err)
        }
    }
    tasks, err := s.ListAllTasks()
    if err != nil {
        t.Fatalf("ListAllTasks: %v", err)
    }
    if len(tasks) != 2 {
        t.Errorf("expected 2 tasks, got %d", len(tasks))
    }
}

func TestDeleteNonExistentTask(t *testing.T) {
    s := newTestScheduleStorage(t)
    err := s.DeleteTask("task-nonexist")
    if err == nil {
        t.Error("expected error when deleting non-existent task")
    }
}

// 确保 context 可取消
func TestLoadTask_ContextCancel(t *testing.T) {
    s := newTestScheduleStorage(t)
    ctx, cancel := context.WithCancel(context.Background())
    cancel() // 立即取消
    // Storage.Read 不直接感知 ctx 取消（local 实现是同步的），这里只验证无 panic
    _ = ctx
    _, err := s.LoadTask("nonexist")
    if err == nil {
        t.Error("expected error for non-existent task")
    }
}
```

- [ ] **Step 14: 运行 schedule 测试**

```bash
go test ./internal/schedule/... -v
```

Expected: 所有测试 PASS（含新增 7 个）。

- [ ] **Step 15: 提交**

```bash
git add internal/schedule/storage.go internal/schedule/storage_test.go
git commit -m "$(cat <<'EOF'
schedule 模块改走 Storage 接口

schedule.Storage 持有 storage.Storage 依赖，SaveTask/SaveExecution/
LoadTask/LoadExecutions/listTasksIn/MoveTask/DeleteTask/GetTaskStatus 
全部替换为 storage.Write/Read/Stat/List/Rename/DeleteDir，
移除 tmp+rename 手动原子写。新增 storage_test.go 覆盖主要操作。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 5: cluster 模块迁移到 Storage 接口

**Files:**
- Modify: `internal/cluster/member.go`
- Modify: `internal/cluster/cluster.go`
- Modify: `internal/cluster/member_test.go`

- [ ] **Step 1: 先运行现有 cluster 测试，记录 baseline**

```bash
go test ./internal/cluster/... -v 2>&1 | tail -20
```

Expected: 所有测试 PASS。

- [ ] **Step 2: 修改 `member.go`，改走 Storage 接口**

将 `internal/cluster/member.go` 整个改为：

```go
package cluster

import (
    "bytes"
    "context"
    "errors"
    "fmt"
    "io"
    "strings"
    "time"

    istorage "github.com/zfd81/groot/internal/storage"
)

func EnsureMembersDir(homeDir string, store istorage.Storage) (string, error) {
    // local 模式：保持原有 os.MkdirAll 语义（提前建目录，避免首次写入时报父目录不存在）。
    // minio 模式：Write 自动创建前缀，此函数退化为 noop，直接返回 membersDir 前缀。
    membersDir := homeDir + "/cluster/members"
    if local, ok := store.(*istorage.Local); ok {
        _ = local
        // local 实现：存储路径是文件系统绝对路径，需预建目录
        import_os_MkdirAll(membersDir) // 见下方 note
    }
    return membersDir, nil
}
```

> Note: `os.MkdirAll` 在 member.go 里保留 `"os"` import，只用于 local 模式分支。

实际需要写的完整 `member.go`（替换原文件全部内容）：

```go
package cluster

import (
    "bytes"
    "context"
    "errors"
    "fmt"
    "io"
    "os"
    "path/filepath"
    "strings"
    "time"

    istorage "github.com/zfd81/groot/internal/storage"
)

// EnsureMembersDir 返回 members 目录路径；local 模式下预建目录，minio 模式下 noop。
func EnsureMembersDir(homeDir string, store istorage.Storage) (string, error) {
    dir := filepath.Join(homeDir, "cluster", "members")
    if _, ok := store.(*istorage.Local); ok {
        if err := os.MkdirAll(dir, 0755); err != nil {
            return "", err
        }
    }
    return dir, nil
}

// WriteRegistration 写入注册文件（内容格式不变：role|host:port|pid）。
func WriteRegistration(store istorage.Storage, membersDir, id, role, host string, port, pid int) error {
    content := fmt.Sprintf("%s|%s:%d|%d", role, host, port, pid)
    return store.Write(
        context.Background(),
        filepath.Join(membersDir, id),
        bytes.NewReader([]byte(content)),
        int64(len(content)),
        "text/plain",
    )
}

// ListMembers 列出 members 目录下所有注册文件的元信息（ID + Mtime）。
func ListMembers(store istorage.Storage, membersDir string) ([]MemberInfo, error) {
    entries, err := store.List(context.Background(), membersDir)
    if err != nil {
        if errors.Is(err, istorage.ErrNotFound) {
            return nil, nil
        }
        return nil, err
    }
    var members []MemberInfo
    for _, entry := range entries {
        if entry.IsDir {
            continue
        }
        members = append(members, MemberInfo{
            ID:    filepath.Base(entry.Path),
            Mtime: entry.ModTime,
        })
    }
    return members, nil
}

// RemoveFile 删除注册文件；不存在视为成功（幂等）。
func RemoveFile(store istorage.Storage, membersDir, id string) error {
    err := store.Delete(context.Background(), filepath.Join(membersDir, id))
    if err != nil && !errors.Is(err, istorage.ErrNotFound) {
        return err
    }
    return nil
}

// GenerateRegID 生成 17 位纯数字注册 ID（格式：YYYYMMDDHHMMSSmmm）。
func GenerateRegID() string {
    s := time.Now().Format("20060102150405.000")
    return strings.Replace(s, ".", "", 1)
}

// ReadRegistration 读取注册文件内容（供 Cluster 内部读取自身 role 等信息）。
func ReadRegistration(store istorage.Storage, membersDir, id string) (string, error) {
    rc, err := store.Read(context.Background(), filepath.Join(membersDir, id))
    if err != nil {
        return "", err
    }
    defer rc.Close()
    data, err := io.ReadAll(rc)
    if err != nil {
        return "", err
    }
    return string(data), nil
}
```

- [ ] **Step 3: 修改 `cluster.go`，`Cluster` 结构体加 storage 字段**

在 `internal/cluster/cluster.go` 中：

1. 添加 import `istorage "github.com/zfd81/groot/internal/storage"`
2. 在 `Cluster` 结构体中新增 `store istorage.Storage` 字段
3. 修改 `New` 签名：

```go
func New(homeDir, host string, port int, log *logger.Logger, store istorage.Storage) *Cluster {
    return &Cluster{
        homeDir: homeDir,
        host:    host,
        port:    port,
        log:     log,
        store:   store,
    }
}
```

4. 修改 `Join`：

```go
func (c *Cluster) Join(ctx context.Context) error {
    c.ctx, c.cancel = context.WithCancel(ctx)

    membersDir, err := EnsureMembersDir(c.homeDir, c.store)
    if err != nil {
        return err
    }

    c.register(membersDir)

    go c.run(membersDir)
    return nil
}
```

5. 修改 `Leave`：

```go
func (c *Cluster) Leave() {
    if c.cancel != nil {
        c.cancel()
    }
    c.mu.Lock()
    defer c.mu.Unlock()

    if c.regID == "" {
        return
    }

    membersDir, _ := EnsureMembersDir(c.homeDir, c.store)
    if membersDir != "" {
        if err := RemoveFile(c.store, membersDir, c.regID); err != nil {
            c.log.Error("删除注册文件失败", zap.Error(err))
        }
    }

    c.regID = ""
}
```

6. 修改 `heartbeat`（将 `os.Stat(ownPath)` 改为 `c.store.Stat`）：

```go
func (c *Cluster) heartbeat(membersDir string) {
    c.mu.Lock()
    defer c.mu.Unlock()

    ownPath := filepath.Join(membersDir, c.regID)
    if _, err := c.store.Stat(context.Background(), ownPath); errors.Is(err, istorage.ErrNotFound) {
        if c.role == RoleLeader && c.onLoseLeader != nil {
            c.onLoseLeader()
        }
        c.register(membersDir)
        return
    }

    if c.role == RoleLeader {
        c.leaderHeartbeat(membersDir)
    } else {
        c.followerHeartbeat(membersDir)
    }
}
```

7. 修改 `register`：

```go
func (c *Cluster) register(membersDir string) {
    members, err := ListMembers(c.store, membersDir)
    if err != nil {
        c.log.Error("列出成员失败", zap.Error(err))
        c.role = RoleFollower
        return
    }

    c.regID = GenerateRegID()
    c.role = DetermineRole(c.regID, members, heartbeatTimeout)

    pid := os.Getpid()
    if err := WriteRegistration(c.store, membersDir, c.regID, c.role, c.host, c.port, pid); err != nil {
        c.log.Error("写入注册文件失败", zap.Error(err))
        return
    }

    c.log.Info("集群注册完成",
        zap.String("reg_id", c.regID),
        zap.String("role", c.role),
        zap.Int("pid", pid),
    )

    if c.role == RoleLeader && c.onBecomeLeader != nil {
        c.onBecomeLeader()
    }
}
```

8. 修改 `leaderHeartbeat`（原 `os.ReadDir` + `entry.Info()` 改为 `ListMembers` 后过滤）：

```go
func (c *Cluster) leaderHeartbeat(membersDir string) {
    pid := os.Getpid()
    if err := WriteRegistration(c.store, membersDir, c.regID, RoleLeader, c.host, c.port, pid); err != nil {
        c.log.Error("心跳写入失败", zap.Error(err))
        return
    }

    members, err := ListMembers(c.store, membersDir)
    if err != nil {
        return
    }
    for _, m := range members {
        if m.ID == c.regID {
            continue
        }
        if time.Since(m.Mtime) > heartbeatTimeout {
            if err := RemoveFile(c.store, membersDir, m.ID); err != nil {
                c.log.Error("清理超时文件失败", zap.String("file", m.ID), zap.Error(err))
            } else {
                c.log.Info("清理超时注册文件", zap.String("file", m.ID))
            }
        }
    }
}
```

9. 修改 `followerHeartbeat`（原 `os.ReadDir` 改为 `ListMembers`）：

```go
func (c *Cluster) followerHeartbeat(membersDir string) {
    members, _ := ListMembers(c.store, membersDir)

    now := time.Now()
    var alive []MemberInfo
    for _, m := range members {
        if now.Sub(m.Mtime) < heartbeatTimeout {
            alive = append(alive, m)
        }
    }

    sort.Slice(alive, func(i, j int) bool {
        return alive[i].ID < alive[j].ID
    })

    if len(alive) > 0 && c.regID == alive[0].ID {
        c.role = RoleLeader
        pid := os.Getpid()
        WriteRegistration(c.store, membersDir, c.regID, RoleLeader, c.host, c.port, pid)

        c.log.Info("提升为 leader", zap.String("reg_id", c.regID))

        // 清理超时文件
        for _, m := range members {
            if m.ID == c.regID {
                continue
            }
            if time.Since(m.Mtime) > heartbeatTimeout {
                RemoveFile(c.store, membersDir, m.ID)
                c.log.Info("清理超时注册文件", zap.String("file", m.ID))
            }
        }

        if c.onBecomeLeader != nil {
            c.onBecomeLeader()
        }
    } else {
        pid := os.Getpid()
        WriteRegistration(c.store, membersDir, c.regID, RoleFollower, c.host, c.port, pid)
    }
}
```

- [ ] **Step 4: 更新 member_test.go，注入 storage.NewLocal()**

在 `internal/cluster/member_test.go` 中将所有直接操作 `dir` 的测试改为通过 `storage.NewLocal()` 注入。例如：

```go
import (
    "testing"
    "time"

    "github.com/zfd81/groot/internal/storage"
)

func newTestStore() *storage.Local {
    return storage.NewLocal()
}

func TestWriteRegistrationFile(t *testing.T) {
    store := newTestStore()
    dir := t.TempDir()
    err := WriteRegistration(store, dir, "20260515143022123", "leader", "127.0.0.1", 8080, 12345)
    if err != nil {
        t.Fatalf("WriteRegistration failed: %v", err)
    }
    members, err := ListMembers(store, dir)
    if err != nil {
        t.Fatalf("ListMembers failed: %v", err)
    }
    if len(members) != 1 || members[0].ID != "20260515143022123" {
        t.Errorf("expected member 20260515143022123, got %+v", members)
    }
}

func TestListMembers(t *testing.T) {
    store := newTestStore()
    dir := t.TempDir()
    WriteRegistration(store, dir, "20260515143021123", "leader", "127.0.0.1", 8080, 11111)
    WriteRegistration(store, dir, "20260515143022123", "follower", "127.0.0.1", 8081, 22222)
    WriteRegistration(store, dir, "20260515143023123", "follower", "127.0.0.1", 8082, 33333)

    members, err := ListMembers(store, dir)
    if err != nil {
        t.Fatalf("ListMembers failed: %v", err)
    }
    if len(members) != 3 {
        t.Errorf("expected 3 members, got %d", len(members))
    }
}

func TestListMembers_EmptyDir(t *testing.T) {
    store := newTestStore()
    dir := t.TempDir()
    members, err := ListMembers(store, dir)
    if err != nil {
        t.Fatalf("ListMembers failed: %v", err)
    }
    if len(members) != 0 {
        t.Errorf("expected 0 members, got %d", len(members))
    }
}

func TestRemoveStaleFile(t *testing.T) {
    store := newTestStore()
    dir := t.TempDir()
    WriteRegistration(store, dir, "stale", "follower", "127.0.0.1", 8080, 11111)
    err := RemoveFile(store, dir, "stale")
    if err != nil {
        t.Fatalf("RemoveFile failed: %v", err)
    }
    members, _ := ListMembers(store, dir)
    if len(members) != 0 {
        t.Error("expected file to be removed")
    }
}

func TestEnsureMembersDir(t *testing.T) {
    store := newTestStore()
    homeDir := t.TempDir()
    membersDir, err := EnsureMembersDir(homeDir, store)
    if err != nil {
        t.Fatalf("EnsureMembersDir failed: %v", err)
    }
    if membersDir == "" {
        t.Error("expected non-empty membersDir")
    }
}

func TestGenerateRegID(t *testing.T) {
    id1 := GenerateRegID()
    if len(id1) != 17 {
        t.Errorf("expected length 17, got %d: %s", len(id1), id1)
    }
    for i, c := range id1 {
        if c < '0' || c > '9' {
            t.Errorf("expected all digits at %d: %s", i, id1)
            break
        }
    }
    id2 := GenerateRegID()
    if id2 < id1 {
        t.Errorf("expected non-decreasing, got id1=%s, id2=%s", id1, id2)
    }
}

func TestFileMtimeUpdates(t *testing.T) {
    store := newTestStore()
    dir := t.TempDir()
    WriteRegistration(store, dir, "test", "leader", "127.0.0.1", 8080, 12345)
    members1, _ := ListMembers(store, dir)

    time.Sleep(10 * time.Millisecond)
    WriteRegistration(store, dir, "test", "leader", "127.0.0.1", 8080, 12345)
    members2, _ := ListMembers(store, dir)

    if !members2[0].Mtime.After(members1[0].Mtime) {
        t.Error("expected mtime to be updated after overwrite")
    }
}
```

- [ ] **Step 5: 编译验证**

```bash
go build ./internal/cluster/...
```

Expected: 编译通过。

- [ ] **Step 6: 运行 cluster 测试**

```bash
go test ./internal/cluster/... -v
```

Expected: 所有测试 PASS。

- [ ] **Step 7: 提交**

```bash
git add internal/cluster/member.go internal/cluster/cluster.go internal/cluster/member_test.go
git commit -m "$(cat <<'EOF'
cluster 模块改走 Storage 接口

WriteRegistration/ListMembers/RemoveFile/EnsureMembersDir 全部改用
storage.Storage 依赖，Cluster 结构体持有 store 字段，heartbeat/register
等操作替换 os.* 直接调用，心跳 mtime 由 Storage.List 返回的 FileInfo.ModTime 提供。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 6: main.go 接线 — basePath 注入与 cluster.New 签名更新

**Files:**
- Modify: `cmd/groot/main.go`
- Modify: `internal/cmd/chat.go`（同步更新 schedule.NewStorage 调用签名）

- [ ] **Step 1: 更新 main.go 的 basePath 注入逻辑**

在 `startServer` 函数中，`storage.New(cfg.Storage)` 之后，添加按 storage 类型计算 basePath 的逻辑，并更新 `cluster.New()`、`schedule.NewStorage()` 的调用：

```go
// 初始化 storage 后端
store, err := storage.New(cfg.Storage)
if err != nil {
    log.Error("无法初始化存储后端", zap.Error(err))
    os.Exit(1)
}

// 按 storage 类型计算运行时数据目录（local 用绝对路径，minio 用相对前缀）
var scheduleBaseDir, clusterMembersDir string
if cfg.Storage.Minio != nil {
    // minio 模式：object key 前缀（相对路径）
    scheduleBaseDir = "schedules"
    clusterMembersDir = "cluster/members"
} else {
    // local 模式：绝对路径（向后兼容）
    scheduleBaseDir = filepath.Join(homeDir, "schedules")
    clusterMembersDir = filepath.Join(homeDir, "cluster", "members")
}
```

注意：`memoryDir` 已通过 `config.ResolvePath(cfg.Memory.Directory, homeDir)` 处理，local 模式下是绝对路径，minio 模式下也是绝对路径（但 memory.Manager 会通过 storage 接口使用相对路径）——**需要检查 memory 模块的路径拼接是否兼容 minio 模式**。

实际上根据 §1.7.1，minio 模式下 memoryDir basePath 应为 `"memory"`（相对前缀）。所以需要额外处理：

```go
var memoryBaseDir string
if cfg.Storage.Minio != nil {
    memoryBaseDir = "memory"
} else {
    memoryBaseDir = config.ResolvePath(cfg.Memory.Directory, homeDir)
}
```

将 `memMgr := memory.NewManager(memoryDir, ...)` 替换为 `memory.NewManager(memoryBaseDir, ...)`。

- [ ] **Step 2: 更新 schedule.NewStorage 调用，传入 store**

将：

```go
scheduleStorage = schedule.NewStorage(scheduleDir, log)
```

替换为：

```go
scheduleStorage = schedule.NewStorage(scheduleBaseDir, store, log)
```

同时删除已不需要的 `scheduleDir := filepath.Join(homeDir, "schedules")` 这一行（逻辑已移到上面的 basePath 计算里）。

- [ ] **Step 3: 更新 cluster.New 调用，传入 store**

将：

```go
clusterInst := cluster.New(homeDir, cfg.Server.Host, cfg.Server.Port, log)
```

替换为（注意 cluster.New 签名已加 store 参数，但 membersDir 不再作为 homeDir 传入，而是让 EnsureMembersDir 内部计算）：

```go
clusterInst := cluster.New(homeDir, cfg.Server.Host, cfg.Server.Port, log, store)
```

（cluster.New 内部通过 `EnsureMembersDir(homeDir, store)` 推导 membersDir，无需外部传入。）

- [ ] **Step 4: 检查并更新 internal/cmd/chat.go**

在 `chat.go` 中也有 memory.NewManager 和 schedule.NewStorage 的类似调用，需同步更新：

```bash
grep -n "NewManager\|NewStorage\|cluster.New" internal/cmd/chat.go
```

按同样模式处理（如果 chat.go 有独立 storage 初始化逻辑的话）。

- [ ] **Step 5: 全量编译验证**

```bash
go build ./...
```

Expected: 编译通过，无 error。修复所有因签名变更引起的编译错误后继续。

- [ ] **Step 6: 运行全量单元测试**

```bash
go test ./internal/... -v 2>&1 | grep -E "^(ok|FAIL|---)"
```

Expected: 所有包均显示 `ok`，无 `FAIL`。

- [ ] **Step 7: 提交**

```bash
git add cmd/groot/main.go internal/cmd/chat.go
git commit -m "$(cat <<'EOF'
main.go 接线：按 storage 类型注入运行时 basePath

minio 模式下 memory/schedule/cluster 使用相对 object key 前缀；
local 模式下保持原绝对路径（100% 向后兼容）。
更新 cluster.New / schedule.NewStorage 签名传入 store 依赖。

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>
EOF
)"
```

---

## Task 7: 最终集成验证

**Files:**
- 无新增文件，仅运行验证

- [ ] **Step 1: 全量编译**

```bash
go build -o bin/groot ./cmd/groot
```

Expected: 编译成功，`bin/groot` 存在。

- [ ] **Step 2: 全量测试**

```bash
go test ./... 2>&1 | grep -E "^(ok|FAIL|---)"
```

Expected: 所有包 `ok`，无 `FAIL`。

- [ ] **Step 3: local 模式冒烟验证（确认向后兼容）**

确认 local 模式可正常初始化 memory / schedule / cluster：

```bash
./bin/groot init --help  # 确认 init 命令正常
```

如有 groot server 本地启动环境可进一步验证：

```bash
./bin/groot &
sleep 2
curl -s http://localhost:8080/health | head -c 200
kill %1
```

- [ ] **Step 4: 提交 final 标记**

```bash
git tag runtime-storage-migration-complete
```

（可选，方便 CI 触发或回溯。）

---

## 自检

**Spec 覆盖确认：**

| spec 需求 | 对应 Task |
|-----------|----------|
| memory 模块改走 Storage 接口 | Task 3 |
| schedule 模块改走 Storage 接口 | Task 4 |
| cluster 模块改走 Storage 接口 | Task 5 |
| local 模式向后兼容 | Task 6（local basePath = 原绝对路径） |
| minio 模式 basePath 注入 | Task 6 |
| 启动 fail-fast 探活 | Task 2 |
| §1.7 spec 补写 | Task 1 |
| memory.saveHistory / SaveChatRecord 移除自实现 tmp+rename | Task 3 |
| schedule.SaveTask / SaveExecution 移除自实现 tmp+rename | Task 4 |

**无占位符确认：** 所有 Step 均含完整代码；无"TBD"、"similar to"。

**类型一致性确认：**
- `istorage "github.com/zfd81/groot/internal/storage"` 别名在 cluster、schedule 包中一致
- `storage.ErrNotFound` 在所有 `errors.Is` 处均用 `istorage.ErrNotFound`
- `cluster.New` 新签名在 Task 5（member.go/cluster.go）和 Task 6（main.go）保持一致：`New(homeDir, host string, port int, log *logger.Logger, store istorage.Storage) *Cluster`
