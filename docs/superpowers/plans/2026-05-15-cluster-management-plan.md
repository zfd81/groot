# 集群管理 实现计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 实现基于文件系统的单机多实例集群管理，支持 Leader 选举、心跳存活检测和故障转移。

**Architecture:** 新增 `internal/cluster/` 包，通过文件系统实现注册发现和选举。Cluster 结构体封装全部集群逻辑，通过回调函数与 scheduler 等外部组件交互。`main.go` 是唯一的粘合层。

**Tech Stack:** Go 1.26, 标准库 (`os`, `time`, `path/filepath`, `sort`, `sync`), `go.uber.org/zap`

---

### Task 1: election.go — 选举逻辑

**Files:**
- Create: `internal/cluster/election.go`
- Create: `internal/cluster/election_test.go`

- [ ] **Step 1: 写 election_test.go**

```go
package cluster

import (
	"testing"
	"time"
)

func TestDetermineRole_NoAliveMembers(t *testing.T) {
	role := DetermineRole("20260515143022123", nil, 7*time.Second)
	if role != "leader" {
		t.Errorf("expected leader, got %s", role)
	}
}

func TestDetermineRole_SelfIsSmallest(t *testing.T) {
	members := []MemberInfo{
		{ID: "20260515143022123", Mtime: time.Now()},
		{ID: "20260515143023123", Mtime: time.Now()},
		{ID: "20260515143024123", Mtime: time.Now()},
	}
	role := DetermineRole("20260515143022123", members, 7*time.Second)
	if role != "leader" {
		t.Errorf("expected leader, got %s", role)
	}
}

func TestDetermineRole_SelfIsNotSmallest(t *testing.T) {
	members := []MemberInfo{
		{ID: "20260515143021123", Mtime: time.Now()},
		{ID: "20260515143022123", Mtime: time.Now()},
		{ID: "20260515143023123", Mtime: time.Now()},
	}
	role := DetermineRole("20260515143022123", members, 7*time.Second)
	if role != "follower" {
		t.Errorf("expected follower, got %s", role)
	}
}

func TestDetermineRole_StaleMembersExcluded(t *testing.T) {
	members := []MemberInfo{
		{ID: "20260515143021123", Mtime: time.Now().Add(-10 * time.Second)}, // stale
		{ID: "20260515143022123", Mtime: time.Now()},
	}
	// stale member excluded, self becomes leader among survivors
	role := DetermineRole("20260515143022123", members, 7*time.Second)
	if role != "leader" {
		t.Errorf("expected leader after excluding stale, got %s", role)
	}
}

func TestDetermineRole_AllStale(t *testing.T) {
	members := []MemberInfo{
		{ID: "20260515143021123", Mtime: time.Now().Add(-10 * time.Second)},
		{ID: "20260515143022123", Mtime: time.Now().Add(-10 * time.Second)},
	}
	role := DetermineRole("20260515143025123", members, 7*time.Second)
	if role != "leader" {
		t.Errorf("expected leader when all stale, got %s", role)
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/cluster/ -v -run TestDetermineRole
```
预期：全部 FAIL（类型未定义）

- [ ] **Step 3: 写 election.go**

```go
package cluster

import (
	"sort"
	"time"
)

// MemberInfo represents a cluster member's metadata from its registration file.
type MemberInfo struct {
	ID    string
	Mtime time.Time
}

// DetermineRole determines whether this instance should be leader or follower.
// It filters out stale members (mtime older than timeout), sorts the survivors
// by ID, and returns "leader" if selfID is the smallest or there are no survivors.
func DetermineRole(selfID string, members []MemberInfo, timeout time.Duration) string {
	now := time.Now()
	var alive []MemberInfo
	for _, m := range members {
		if now.Sub(m.Mtime) < timeout {
			alive = append(alive, m)
		}
	}
	if len(alive) == 0 {
		return "leader"
	}
	sort.Slice(alive, func(i, j int) bool {
		return alive[i].ID < alive[j].ID
	})
	if selfID <= alive[0].ID {
		return "leader"
	}
	return "follower"
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/cluster/ -v -run TestDetermineRole
```
预期：全部 PASS

- [ ] **Step 5: 提交**

```bash
git add internal/cluster/election.go internal/cluster/election_test.go
git commit -m "feat(cluster): add role determination logic

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

### Task 2: member.go — 文件操作

**Files:**
- Create: `internal/cluster/member.go`
- Create: `internal/cluster/member_test.go`

- [ ] **Step 1: 写 member_test.go**

```go
package cluster

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestWriteRegistrationFile(t *testing.T) {
	dir := t.TempDir()
	err := WriteRegistration(dir, "20260515143022123", "leader", "127.0.0.1", 8080, 12345)
	if err != nil {
		t.Fatalf("WriteRegistration failed: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dir, "20260515143022123"))
	if err != nil {
		t.Fatalf("failed to read file: %v", err)
	}
	expected := "leader|127.0.0.1:8080|12345"
	if string(content) != expected {
		t.Errorf("expected %q, got %q", expected, string(content))
	}
}

func TestListMembers(t *testing.T) {
	dir := t.TempDir()
	WriteRegistration(dir, "20260515143021123", "leader", "127.0.0.1", 8080, 11111)
	WriteRegistration(dir, "20260515143022123", "follower", "127.0.0.1", 8081, 22222)
	WriteRegistration(dir, "20260515143023123", "follower", "127.0.0.1", 8082, 33333)

	members, err := ListMembers(dir)
	if err != nil {
		t.Fatalf("ListMembers failed: %v", err)
	}
	if len(members) != 3 {
		t.Errorf("expected 3 members, got %d", len(members))
	}
	if members[0].ID != "20260515143021123" {
		t.Errorf("expected smallest ID first, got %s", members[0].ID)
	}
}

func TestListMembers_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	members, err := ListMembers(dir)
	if err != nil {
		t.Fatalf("ListMembers failed: %v", err)
	}
	if len(members) != 0 {
		t.Errorf("expected 0 members, got %d", len(members))
	}
}

func TestRemoveStaleFile(t *testing.T) {
	dir := t.TempDir()
	WriteRegistration(dir, "stale", "follower", "127.0.0.1", 8080, 11111)
	err := RemoveFile(dir, "stale")
	if err != nil {
		t.Fatalf("RemoveFile failed: %v", err)
	}
	_, err = os.Stat(filepath.Join(dir, "stale"))
	if !os.IsNotExist(err) {
		t.Error("expected file to be removed")
	}
}

func TestEnsureMembersDir(t *testing.T) {
	homeDir := t.TempDir()
	membersDir, err := EnsureMembersDir(homeDir)
	if err != nil {
		t.Fatalf("EnsureMembersDir failed: %v", err)
	}
	info, err := os.Stat(membersDir)
	if err != nil {
		t.Fatalf("stat failed: %v", err)
	}
	if !info.IsDir() {
		t.Error("expected a directory")
	}
}

func TestFileMtimeUpdates(t *testing.T) {
	dir := t.TempDir()
	WriteRegistration(dir, "test", "leader", "127.0.0.1", 8080, 12345)
	info1, _ := os.Stat(filepath.Join(dir, "test"))
	mtime1 := info1.ModTime()

	time.Sleep(10 * time.Millisecond)
	WriteRegistration(dir, "test", "leader", "127.0.0.1", 8080, 12345)
	info2, _ := os.Stat(filepath.Join(dir, "test"))
	mtime2 := info2.ModTime()

	if !mtime2.After(mtime1) {
		t.Error("expected mtime to be updated after overwrite")
	}
}
```

- [ ] **Step 2: 运行测试确认失败**

```bash
go test ./internal/cluster/ -v -run "TestWriteRegistration|TestListMembers|TestRemoveStaleFile|TestEnsureMembersDir|TestFileMtimeUpdates"
```
预期：全部 FAIL（函数未定义）

- [ ] **Step 3: 写 member.go**

```go
package cluster

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

func EnsureMembersDir(homeDir string) (string, error) {
	dir := filepath.Join(homeDir, "cluster", "members")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", err
	}
	return dir, nil
}

func WriteRegistration(membersDir, id, role, host string, port, pid int) error {
	content := fmt.Sprintf("%s|%s:%d|%d", role, host, port, pid)
	return os.WriteFile(filepath.Join(membersDir, id), []byte(content), 0644)
}

func ListMembers(membersDir string) ([]MemberInfo, error) {
	entries, err := os.ReadDir(membersDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var members []MemberInfo
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			continue
		}
		members = append(members, MemberInfo{
			ID:    entry.Name(),
			Mtime: info.ModTime(),
		})
	}
	return members, nil
}

func RemoveFile(membersDir, id string) error {
	path := filepath.Join(membersDir, id)
	err := os.Remove(path)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

func GenerateRegID() string {
	return time.Now().Format("20060102150405000")
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/cluster/ -v -run "TestWriteRegistration|TestListMembers|TestRemoveStaleFile|TestEnsureMembersDir|TestFileMtimeUpdates"
```
预期：全部 PASS

- [ ] **Step 5: 提交**

```bash
git add internal/cluster/member.go internal/cluster/member_test.go
git commit -m "feat(cluster): add member file operations

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

### Task 3: cluster.go — Cluster 主结构体

**Files:**
- Create: `internal/cluster/cluster.go`
- Create: `internal/cluster/cluster_test.go`

- [ ] **Step 1: 写 cluster_test.go**

```go
package cluster

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/logger"
)

func TestCluster_JoinAsLeader_NoExistingMembers(t *testing.T) {
	homeDir := t.TempDir()
	log := logger.NewNop()
	c := New(homeDir, "127.0.0.1", 8080, log)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := c.Join(ctx)
	if err != nil {
		t.Fatalf("Join failed: %v", err)
	}
	defer c.Leave()

	if !c.IsLeader() {
		t.Error("expected leader when no other members")
	}
	if c.RegID() == "" {
		t.Error("expected non-empty registration ID")
	}

	// verify file was created
	membersDir := filepath.Join(homeDir, "cluster", "members")
	files, _ := os.ReadDir(membersDir)
	if len(files) != 1 {
		t.Fatalf("expected 1 registration file, got %d", len(files))
	}
	content, _ := os.ReadFile(filepath.Join(membersDir, files[0].Name()))
	expectedPrefix := "leader|127.0.0.1:8080|"
	if string(content)[:len(expectedPrefix)] != expectedPrefix {
		t.Errorf("unexpected file content: %s", string(content))
	}
}

func TestCluster_JoinAsFollower_ExistingLeader(t *testing.T) {
	homeDir := t.TempDir()
	log := logger.NewNop()

	// start first instance (leader)
	leader := New(homeDir, "127.0.0.1", 8080, log)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := leader.Join(ctx)
	if err != nil {
		t.Fatalf("leader Join failed: %v", err)
	}
	defer leader.Leave()

	if !leader.IsLeader() {
		t.Fatal("first instance should be leader")
	}

	// start second instance (follower)
	follower := New(homeDir, "127.0.0.1", 8081, log)
	err = follower.Join(ctx)
	if err != nil {
		t.Fatalf("follower Join failed: %v", err)
	}
	defer follower.Leave()

	if follower.IsLeader() {
		t.Error("second instance should be follower")
	}
}

func TestCluster_Heartbeat_FileLost(t *testing.T) {
	homeDir := t.TempDir()
	log := logger.NewNop()

	c := New(homeDir, "127.0.0.1", 8080, log)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := c.Join(ctx)
	if err != nil {
		t.Fatalf("Join failed: %v", err)
	}
	defer c.Leave()

	oldRegID := c.RegID()

	// simulate file deletion
	membersDir := filepath.Join(homeDir, "cluster", "members")
	RemoveFile(membersDir, oldRegID)

	// wait for heartbeat to re-register
	time.Sleep(3500 * time.Millisecond)

	newRegID := c.RegID()
	if newRegID == oldRegID {
		t.Error("expected new registration ID after file loss")
	}
	if newRegID == "" {
		t.Error("expected non-empty registration ID after re-registration")
	}
}

func TestCluster_Heartbeat_LeaderCleanupStale(t *testing.T) {
	homeDir := t.TempDir()
	log := logger.NewNop()

	leader := New(homeDir, "127.0.0.1", 8080, log)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := leader.Join(ctx)
	if err != nil {
		t.Fatalf("Join failed: %v", err)
	}
	defer leader.Leave()

	// write a stale file manually (simulating dead instance)
	membersDir := filepath.Join(homeDir, "cluster", "members")
	WriteRegistration(membersDir, "20200101000000001", "follower", "127.0.0.1", 9000, 99999)

	// set its mtime to old
	oldTime := time.Now().Add(-10 * time.Second)
	os.Chtimes(filepath.Join(membersDir, "20200101000000001"), oldTime, oldTime)

	// wait for leader heartbeat to clean up
	time.Sleep(3500 * time.Millisecond)

	_, err = os.Stat(filepath.Join(membersDir, "20200101000000001"))
	if !os.IsNotExist(err) {
		t.Error("expected stale file to be cleaned up by leader")
	}
}

func TestCluster_Leave(t *testing.T) {
	homeDir := t.TempDir()
	log := logger.NewNop()

	c := New(homeDir, "127.0.0.1", 8080, log)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	err := c.Join(ctx)
	if err != nil {
		t.Fatalf("Join failed: %v", err)
	}

	c.Leave()

	membersDir := filepath.Join(homeDir, "cluster", "members")
	files, _ := os.ReadDir(membersDir)
	if len(files) != 0 {
		t.Errorf("expected 0 files after Leave, got %d", len(files))
	}
}
```

- [ ] **Step 2: 写 cluster.go**

```go
package cluster

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/logger"
)

const (
	heartbeatInterval = 3 * time.Second
	heartbeatTimeout  = 7 * time.Second
)

// Cluster manages instance registration, heartbeat, and leader election.
type Cluster struct {
	homeDir string
	host    string
	port    int
	regID   string
	role    string
	mu      sync.RWMutex
	log     *logger.Logger

	onBecomeLeader func()
	onLoseLeader   func()

	ctx    context.Context
	cancel context.CancelFunc
}

// New creates a new Cluster instance.
func New(homeDir, host string, port int, log *logger.Logger) *Cluster {
	return &Cluster{
		homeDir: homeDir,
		host:    host,
		port:    port,
		log:     log,
	}
}

// SetCallbacks sets the callbacks for leader role changes.
func (c *Cluster) SetCallbacks(onBecomeLeader, onLoseLeader func()) {
	c.onBecomeLeader = onBecomeLeader
	c.onLoseLeader = onLoseLeader
}

// Join registers this instance in the cluster and starts the heartbeat loop.
func (c *Cluster) Join(ctx context.Context) error {
	c.ctx, c.cancel = context.WithCancel(ctx)

	membersDir, err := EnsureMembersDir(c.homeDir)
	if err != nil {
		return err
	}

	c.register(membersDir)

	go c.run(membersDir)
	return nil
}

// Leave gracefully removes this instance from the cluster.
func (c *Cluster) Leave() {
	if c.cancel != nil {
		c.cancel()
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.regID == "" {
		return
	}

	membersDir, _ := EnsureMembersDir(c.homeDir)
	if membersDir != "" {
		if err := RemoveFile(membersDir, c.regID); err != nil {
			c.log.Error("删除注册文件失败", zap.Error(err))
		}
	}

	c.regID = ""
}

// IsLeader returns true if this instance is currently the leader.
func (c *Cluster) IsLeader() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.role == "leader"
}

// RegID returns the current registration ID.
func (c *Cluster) RegID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.regID
}

// Role returns the current role.
func (c *Cluster) Role() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.role
}

func (c *Cluster) run(membersDir string) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()

	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.heartbeat(membersDir)
		}
	}
}

func (c *Cluster) heartbeat(membersDir string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	// Check if own file still exists
	ownPath := filepath.Join(membersDir, c.regID)
	if _, err := os.Stat(ownPath); os.IsNotExist(err) {
		// File lost -- re-register
		if c.role == "leader" {
			if c.onLoseLeader != nil {
				c.onLoseLeader()
			}
		}
		c.register(membersDir)
		return
	}

	if c.role == "leader" {
		c.leaderHeartbeat(membersDir)
	} else {
		c.followerHeartbeat(membersDir)
	}
}

func (c *Cluster) register(membersDir string) {
	c.regID = GenerateRegID()

	members, err := ListMembers(membersDir)
	if err != nil {
		c.log.Error("列出成员失败", zap.Error(err))
		c.role = "follower"
		return
	}

	c.role = DetermineRole(c.regID, members, heartbeatTimeout)

	pid := os.Getpid()
	if err := WriteRegistration(membersDir, c.regID, c.role, c.host, c.port, pid); err != nil {
		c.log.Error("写入注册文件失败", zap.Error(err))
		return
	}

	c.log.Info("集群注册完成",
		zap.String("reg_id", c.regID),
		zap.String("role", c.role),
		zap.Int("pid", pid),
	)

	if c.role == "leader" && c.onBecomeLeader != nil {
		c.onBecomeLeader()
	}
}

func (c *Cluster) leaderHeartbeat(membersDir string) {
	pid := os.Getpid()
	if err := WriteRegistration(membersDir, c.regID, "leader", c.host, c.port, pid); err != nil {
		c.log.Error("心跳写入失败", zap.Error(err))
		return
	}

	// Clean up stale registration files
	entries, err := os.ReadDir(membersDir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		if entry.Name() == c.regID {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			continue
		}
		if time.Since(info.ModTime()) > heartbeatTimeout {
			if err := RemoveFile(membersDir, entry.Name()); err != nil {
				c.log.Error("清理超时文件失败", zap.String("file", entry.Name()), zap.Error(err))
			} else {
				c.log.Info("清理超时注册文件", zap.String("file", entry.Name()))
			}
		}
	}
}

func (c *Cluster) followerHeartbeat(membersDir string) {
	members, _ := ListMembers(membersDir)

	// Filter alive members
	now := time.Now()
	var alive []MemberInfo
	for _, m := range members {
		if now.Sub(m.Mtime) < heartbeatTimeout {
			alive = append(alive, m)
		}
	}

	if len(alive) > 0 && c.regID == alive[0].ID {
		// Become leader
		c.role = "leader"
		pid := os.Getpid()
		WriteRegistration(membersDir, c.regID, "leader", c.host, c.port, pid)

		c.log.Info("提升为 leader", zap.String("reg_id", c.regID))

		// Clean up stale files
		entries, _ := os.ReadDir(membersDir)
		for _, entry := range entries {
			if entry.Name() == c.regID {
				continue
			}
			info, err := entry.Info()
			if err != nil {
				continue
			}
			if time.Since(info.ModTime()) > heartbeatTimeout {
				RemoveFile(membersDir, entry.Name())
				c.log.Info("清理超时注册文件", zap.String("file", entry.Name()))
			}
		}

		if c.onBecomeLeader != nil {
			c.onBecomeLeader()
		}
	} else {
		pid := os.Getpid()
		WriteRegistration(membersDir, c.regID, "follower", c.host, c.port, pid)
	}
}
```

- [ ] **Step 4: 运行测试确认通过**

```bash
go test ./internal/cluster/ -v -count=1
```
预期：全部 PASS

- [ ] **Step 5: 提交**

```bash
git add internal/cluster/cluster.go internal/cluster/cluster_test.go
git commit -m "feat(cluster): add Cluster struct with heartbeat and election

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

### Task 4: 集成到 main.go

**Files:**
- Modify: `cmd/groot/main.go`

- [ ] **Step 1: 提取 scheduler 初始化到独立函数**

将 `startServer` 中第 327-384 行的 scheduler 相关代码提取为闭包函数，供 cluster 回调使用。

具体改动：

在 `startServer` 中，将 scheduler 创建和任务注册逻辑包裹在 `startLeaderTasks` 和 `stopLeaderTasks` 闭包中。

`startLeaderTasks` 函数：
```go
startLeaderTasks := func() {
    maxConcurrent := cfg.Schedule.MaxConcurrentTasks
    if maxConcurrent <= 0 {
        maxConcurrent = 10
    }
    sched, err := scheduler.New(log, maxConcurrent)
    if err != nil {
        log.Error("无法创建调度器", zap.Error(err))
        return
    }

    scheduleEngine := schedule.NewEngine(sched, scheduleRunner, scheduleStorage, log)
    if err := scheduleEngine.Start(); err != nil {
        log.Error("无法启动调度引擎", zap.Error(err))
        return
    }

    // Register cleanup task
    cleanupHour, cleanupMinute := schedule.ParseCleanupTime(cfg.Memory.CleanupSchedule)
    sched.AddDaily(cleanupHour, cleanupMinute, gocron.NewTask(memory.NewCleanupTask(memMgr, log)), "system-cleanup", "cleanup")

    // Register sync task
    syncInterval, _ := time.ParseDuration(cfg.Schedule.SyncInterval)
    if syncInterval <= 0 {
        syncInterval = 30 * time.Second
    }
    sched.AddDuration(syncInterval, gocron.NewTask(schedule.NewSyncTask(scheduleEngine, scheduleStorage, log)), "system-sync", "sync")

    sched.Start()
    log.Info("统一调度器已启动 (Leader)",
        zap.Int("max_concurrent", maxConcurrent),
        zap.Int("cleanup_hour", cleanupHour),
        zap.Int("cleanup_minute", cleanupMinute),
    )
}
```

`stopLeaderTasks` 函数关闭 sched。注意 sched 需要声明在外层作用域以便两个闭包共享。

- [ ] **Step 2: 声明 cluster 并插入初始化**

在 `startServer` 中，logger 初始化之后插入 cluster 初始化：

```go
// Initialize cluster
clusterInst := cluster.New(homeDir, cfg.Server.Host, cfg.Server.Port, log)
clusterInst.SetCallbacks(startLeaderTasks, stopLeaderTasks)

if err := clusterInst.Join(context.Background()); err != nil {
    log.Error("加入集群失败", zap.Error(err))
}

log.Info("集群状态",
    zap.String("role", clusterInst.Role()),
    zap.String("reg_id", clusterInst.RegID()),
)
```

- [ ] **Step 3: 修改关闭流程**

在关闭 goroutine 中，在 `srv.Stop(ctx)` 之前添加：

```go
// Leave cluster
clusterInst.Leave()
```

移除原来的独立 scheduler shutdown，改为由 stopLeaderTasks 回调处理。

- [ ] **Step 4: 确保 import 正确**

添加 `"github.com/zfd81/groot/internal/cluster"` 到 import 块。

- [ ] **Step 5: 编译验证**

```bash
go build -o bin/groot ./cmd/groot
```
预期：编译成功

- [ ] **Step 6: 运行全部单元测试**

```bash
go test ./internal/cluster/... -v -count=1
```
预期：全部 PASS

- [ ] **Step 7: 提交**

```bash
git add cmd/groot/main.go
git commit -m "feat: integrate cluster management into server startup

Co-Authored-By: Claude Opus 4.7 <noreply@anthropic.com>"
```

---

### Task 5: 系统级验证

- [ ] **Step 1: 单实例启动验证**

```bash
# 设置测试用的 GROOT_HOME
export GROOT_HOME=/tmp/groot-test
mkdir -p $GROOT_HOME

# 配置测试用的 config.yaml（需要有效的 LLM 配置）
# 启动实例 1
./bin/groot -p 8080 &
sleep 2

# 验证注册文件
ls -la $GROOT_HOME/cluster/members/
cat $GROOT_HOME/cluster/members/*
# 预期：一个文件，内容以 leader| 开头
```

- [ ] **Step 2: 双实例验证**

```bash
# 启动实例 2
./bin/groot -p 8081 &
sleep 2

# 验证有两个注册文件，一个 leader，一个 follower
ls -la $GROOT_HOME/cluster/members/
# 预期：2 个文件
```

- [ ] **Step 3: 杀掉 leader 验证故障转移**

```bash
# 找到 leader 的 PID 并 kill
kill <leader-pid>
sleep 10

# 验证原 follower 已提升为 leader
cat $GROOT_HOME/cluster/members/*
# 预期：原 follower 文件内容变为 leader|...
```

- [ ] **Step 4: 清理**

```bash
kill %1 %2 2>/dev/null
rm -rf /tmp/groot-test
```
