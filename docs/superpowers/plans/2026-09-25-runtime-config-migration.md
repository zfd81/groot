# 限流、消息发送器、调度开关迁移配置表 实施计划

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** 把限流参数、消息发送器配置、`schedule.enabled` 三项从启动期固化值改造成保存即生效的界面配置。

**Architecture:** 三项的共同障碍是「值被固化进长生命周期对象」。改造统一走两步：给持有方加一个在线重建方法（`RateLimiter.Reconfigure`、`Layer.Reconfigure`、`Manager.SetBuiltinToolsEnabled`），再由配置接口在写表成功后调用它。生效语义沿用子 Agent 并发上限已确立的约定：已发起的调用按旧配置走完，新发起的调用看新配置。

**Tech Stack:** Go（hertz、sqlx、golang.org/x/time/rate）、Vue 3 + TypeScript + Element Plus。

---

## 范围说明

三项彼此独立，按 A、B、C 三个阶段推进，每个阶段单独可上线、可测试。阶段内的任务有先后依赖，阶段之间没有。

阶段 A 与 C 的配置项并入既有的 `/web/settings/runtime`（读写同构，无敏感字段）。阶段 B 单独开 `/web/settings/senders`，因为 SMTP 密码需要「回读时脱敏、提交空值表示不改」的语义，与 runtime 接口的读写同构约定冲突。

不迁移的项及原因：`message.queue_size`、`message.workers` 是启动时建好的 channel 容量与协程数；`security.rate_limit.cleanup_interval` 是后台 ticker 周期；`schedule.max_concurrent_tasks`、`schedule.sync_interval` 是调度器构造参数。它们的持有方重建代价远大于收益，留在 YAML。

## 两处并发缺陷

改造会引入新的写入方，暴露出两处既有的数据竞争，必须在同一阶段内修掉：

1. `internal/ratelimit/limiter.go` 的 `rl.cfg` 目前被 `Allow`、`Acquire`、`Release` 无锁读取。`Reconfigure` 一旦写它就是竞争。
2. `internal/message/layer.go` 的 `senders`、`senderConfigs` 两个 map 目前被 worker 协程无锁读取。`Reconfigure` 一旦写它们就是竞争。

两处都用已在结构体里或新增的 `sync.RWMutex` 保护。阶段 A 的任务 1、阶段 B 的任务 6 分别处理。

## 文件结构

**阶段 A：限流参数**

| 文件 | 职责 |
| --- | --- |
| `internal/ratelimit/limiter.go` | 新增 `Reconfigure`，`cfg` 读取改为持锁 |
| `internal/setting/runtime.go` | 新增 5 个限流键、边界常量、校验、编解码 |
| `internal/setting/settings.go` | `Runtime`/`RuntimeStatic` 带上 `RateLimit` 分类 |
| `internal/api/handler/setting.go` | `PutRuntime` 写表后调用 `Reconfigure` |
| `internal/api/types/types.go` | `RuntimeSettingsPayload` 新增 `rate_limit` |
| `internal/api/server.go` | 把 `rateLimiter` 传进 `NewSettingHandler` |
| `cmd/groot/main.go` | 启动时把表内限流值应用到限流器 |

**阶段 B：消息发送器**

| 文件 | 职责 |
| --- | --- |
| `internal/message/sender.go` | 新增 `SenderFactory` 函数类型 |
| `internal/message/senders/factory.go` | 按名字与配置构造 sender，实现 `SenderFactory` |
| `internal/message/layer.go` | 新增 `Reconfigure`，两个 map 加锁 |
| `internal/setting/message.go` | 新建：消息配置的键、校验、编解码 |
| `internal/setting/settings.go` | 新增 `MessageSettings`/`SetMessageSettings` |
| `internal/api/handler/setting.go` | `GET`/`PUT /web/settings/senders` 两个 handler 并入既有文件（实施时未另建 `message_setting.go`） |
| `internal/api/types/types.go` | 新增消息配置请求与响应体 |
| `internal/api/server.go` | 装配消息配置 handler 与路由 |
| `cmd/groot/main.go` | 注册改为走工厂，启动时应用表内值 |

**阶段 C：调度开关**

| 文件 | 职责 |
| --- | --- |
| `internal/mcp/manager.go` | 新增内置工具开关，`GetTools` 按开关过滤 |
| `internal/setting/runtime.go` | 新增 `schedule.enabled` 键与编解码 |
| `internal/setting/settings.go` | `Runtime`/`RuntimeStatic` 带上 `Schedule` 分类 |
| `internal/api/handler/setting.go` | `PutRuntime` 写表后翻转开关 |
| `internal/api/types/types.go` | `RuntimeSettingsPayload` 新增 `schedule` |
| `cmd/groot/main.go` | 调度工具改为无条件注册，开关决定暴露 |

**三阶段共用的前端改动**

| 文件 | 职责 |
| --- | --- |
| `web/src/api/runtime.ts` | 类型、默认值、克隆、取值边界 |
| `web/src/api/messageSetting.ts` | 新建：消息配置接口层 |
| `web/src/components/settings/SettingsModal.vue` | 三个新分组的控件 |
| `web/src/i18n/messages/zh-cn.ts` | 中文文案 |
| `web/src/i18n/messages/en.ts` | 英文文案，键与中文一一对应 |

**设计文档**

`docs/superpowers/specs/2026-09-25-runtime-config-migration-design.md` 新建。功能设计只作正面陈述，全部对比语句集中在迭代说明章节。

---
## 阶段 A：限流参数

### Task 1: 限流器支持在线重建

**Files:**
- Modify: `internal/ratelimit/limiter.go:20-27`（`RateLimiter` 结构体）、`:58-71`（`Allow`）、`:76-118`（`Acquire`）、`:122-142`（`Release`）、`:151-172`（`getOrCreateLimiter`）
- Test: `internal/ratelimit/limiter_test.go`

`cfg` 目前被四个方法无锁读取。加一个 `Reconfigure` 写它就构成竞争，所以这一步把 `cfg` 的全部读取收进 `snapshot()`，由已有的 `rl.mu` 保护。

- [x] **Step 1: 写失败的测试**

追加到 `internal/ratelimit/limiter_test.go` 末尾：

```go
// TestReconfigure_AppliesToNewKeys 验证收紧并发上限对随后首次出现的 key 生效。
func TestReconfigure_AppliesToNewKeys(t *testing.T) {
	cfg := newTestConfig()
	cfg.DefaultConcurrency = 3
	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rl.Stop()

	next := cfg
	next.DefaultConcurrency = 1
	rl.Reconfigure(next)

	if !rl.Acquire("fresh-key") {
		t.Fatal("first acquire on a fresh key should succeed")
	}
	if rl.Acquire("fresh-key") {
		t.Error("second acquire should be blocked by the new limit of 1")
	}
	rl.Release("fresh-key")
}

// TestReconfigure_KeepsExistingBuckets 验证已建桶的 key 保留旧容量。
// 这是「已发起的调用按旧配置走完」的直接体现：取名额与归还名额
// 落在同一个桶上，收紧上限不会让一个已放行的请求在归还时对不上账。
func TestReconfigure_KeepsExistingBuckets(t *testing.T) {
	cfg := newTestConfig()
	cfg.DefaultConcurrency = 2
	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rl.Stop()

	if !rl.Acquire("old-key") {
		t.Fatal("acquire before reconfigure should succeed")
	}

	next := cfg
	next.DefaultConcurrency = 1
	rl.Reconfigure(next)

	// old-key 的桶容量仍是 2，第二个名额应能取到
	if !rl.Acquire("old-key") {
		t.Error("existing key should keep its old capacity of 2")
	}
	// 同时新 key 已按新上限建桶，取第二个应被挡住
	if !rl.Acquire("new-key") {
		t.Fatal("first acquire on new-key should succeed")
	}
	if rl.Acquire("new-key") {
		t.Error("new-key should be capped at the new limit of 1")
	}

	rl.Release("old-key")
	rl.Release("old-key")
	rl.Release("new-key")
}

// TestReconfigure_TogglesEnabled 验证开关可在线关停与恢复。
func TestReconfigure_TogglesEnabled(t *testing.T) {
	cfg := newTestConfig()
	cfg.DefaultQPS = 1
	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rl.Stop()

	// 先把 key 的令牌耗干
	rl.Allow("drained")
	if rl.Allow("drained") {
		t.Fatal("second call should be rate limited")
	}

	off := cfg
	off.Enabled = false
	rl.Reconfigure(off)
	if !rl.Allow("drained") {
		t.Error("disabled limiter must allow everything")
	}

	// 恢复开关后限流重新起作用：换一个 key 观察，
	// drained 的桶尚未补满令牌，用它判定会依赖计时
	rl.Reconfigure(cfg)
	if !rl.Allow("fresh") {
		t.Fatal("first call on a fresh key should pass")
	}
	if rl.Allow("fresh") {
		t.Error("re-enabled limiter should rate limit the second call")
	}
}

// TestReconfigure_RebuildsGlobalLimiter 验证全局桶按新配置重建。
func TestReconfigure_RebuildsGlobalLimiter(t *testing.T) {
	cfg := newTestConfig()
	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rl.Stop()

	next := cfg
	next.GlobalConcurrency = 1
	rl.Reconfigure(next)

	if !rl.Acquire("a") {
		t.Fatal("first acquire should take the only global slot")
	}
	if rl.Acquire("b") {
		t.Error("second acquire should be blocked by global concurrency 1")
	}
	rl.Release("a")
}

// TestReconfigure_ConcurrentWithTraffic 在 -race 下验证重建与请求并发无竞争。
func TestReconfigure_ConcurrentWithTraffic(t *testing.T) {
	cfg := newTestConfig()
	rl, err := New(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer rl.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				key := "k" + strconv.Itoa(n)
				if rl.Acquire(key) {
					rl.Release(key)
				}
				rl.Allow(key)
			}
		}(i)
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			next := cfg
			next.DefaultConcurrency = n + 1
			for j := 0; j < 20; j++ {
				rl.Reconfigure(next)
			}
		}(i)
	}
	wg.Wait()
}
```

把 `strconv` 加进该文件的 import 块，它目前只有 `sync`、`testing`、`time` 与 config：

```go
import (
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/config"
)
```

- [x] **Step 2: 运行测试确认失败**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/ratelimit/ -run 'TestReconfigure' -count=1`
Expected: FAIL，编译错误 `rl.Reconfigure undefined (type *RateLimiter has no field or method Reconfigure)`

- [x] **Step 3: 把 cfg 读取收进 snapshot**

`internal/ratelimit/limiter.go`。四个方法目前直接读 `rl.cfg`，逐个替换。

`Allow` 整体替换为：

```go
// Allow checks if a request is allowed for the given key (QPS only).
// Returns true if allowed, false if rate limited.
func (rl *RateLimiter) Allow(key string) bool {
	cfg, global := rl.snapshot()
	if !cfg.Enabled {
		return true
	}

	// Global QPS check
	if global != nil && global.qps != nil && !global.qps.Allow() {
		return false
	}

	// Per-key QPS check
	kl := rl.getOrCreateLimiter(key, cfg)
	return kl.qps == nil || kl.qps.Allow()
}
```

`Acquire` 整体替换为：

```go
// Acquire checks QPS and acquires a concurrency slot for the given key.
// Returns true if both checks pass, false if rate limited.
// Caller must call Release when done.
func (rl *RateLimiter) Acquire(key string) bool {
	cfg, global := rl.snapshot()
	if !cfg.Enabled {
		return true
	}

	// Global QPS check
	if global != nil && global.qps != nil && !global.qps.Allow() {
		return false
	}

	// Global concurrency check
	if global != nil && global.sem != nil {
		select {
		case global.sem <- struct{}{}:
		default:
			return false
		}
	}

	// Per-key QPS + concurrency check
	kl := rl.getOrCreateLimiter(key, cfg)
	if kl.qps != nil && !kl.qps.Allow() {
		// Release global sem if we acquired it
		if global != nil && global.sem != nil {
			<-global.sem
		}
		return false
	}
	if kl.sem != nil {
		select {
		case kl.sem <- struct{}{}:
		default:
			// Release global sem if we acquired it
			if global != nil && global.sem != nil {
				<-global.sem
			}
			return false
		}
	}

	kl.touch()
	return true
}
```

`Release` 整体替换为：

```go
// Release releases a concurrency slot for the given key.
// Must be called after Acquire returns true.
//
// 归还时重新取快照而非沿用获取时的：重建只换全局桶的实例，
// 旧实例被丢弃后不再有人读它的计数，少还一次不影响新桶的容量。
func (rl *RateLimiter) Release(key string) {
	cfg, global := rl.snapshot()
	if !cfg.Enabled {
		return
	}

	// Release per-key sem
	if kl := rl.getLimiter(key); kl != nil && kl.sem != nil {
		select {
		case <-kl.sem:
		default:
		}
	}

	// Release global sem
	if global != nil && global.sem != nil {
		select {
		case <-global.sem:
		default:
		}
	}
}
```

- [x] **Step 4: 新增 Reconfigure 与 snapshot**

紧跟在 `Release` 之后插入：

```go
// Reconfigure 用新配置替换限流参数。
//
// 生效范围与子 Agent 并发上限一致：已建桶的 key 保留旧容量，随后首次出现的
// key 按新配置建桶。正在进行的请求不被打断——中途抽走名额会让一个已放行的
// 请求在归还时对不上账。旧桶在空闲超过回收窗口后由 cleanup 清掉，
// 此后该 key 再来即按新配置建桶。
//
// per-key 桶不清空：Release 是按 key 查当前桶来归还的，清空后一个
// 重建前取到名额的请求会把名额还进新桶，凭空放宽新上限。
//
// 全局桶无条件重建：它只有一个实例，没有「逐个 key 过渡」的说法。
// 代价是重建瞬间正在占用全局名额的请求，其归还会落到新桶上——
// 全局桶的容量通常远大于瞬时并发，这点偏差不影响限流意图。
func (rl *RateLimiter) Reconfigure(cfg config.RateLimitConfig) {
	var global *keyLimiter
	if cfg.GlobalQPS > 0 || cfg.GlobalConcurrency > 0 {
		global = newKeyLimiter(cfg.GlobalQPS, cfg.GlobalConcurrency)
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()
	rl.cfg = cfg
	rl.global = global
}

// Config 返回当前生效的限流配置，供接口回读与启动日志使用。
func (rl *RateLimiter) Config() config.RateLimitConfig {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return rl.cfg
}

// snapshot 一次取出配置与全局桶，避免调用方在一次判定内多次加锁，
// 也避免中途被 Reconfigure 换掉造成前后不一致。
func (rl *RateLimiter) snapshot() (config.RateLimitConfig, *keyLimiter) {
	rl.mu.RLock()
	defer rl.mu.RUnlock()
	return rl.cfg, rl.global
}
```

- [x] **Step 5: 给 keyLimiter 的 lastUsed 加锁**

`lastUsed` 被 `getOrCreateLimiter` 与 `cleanup` 并发读写，`-race` 会报。`keyLimiter` 结构体替换为：

```go
// keyLimiter tracks rate limits for a single key (API key or client IP)
type keyLimiter struct {
	qps *rate.Limiter
	sem chan struct{}

	// mu 只保护 lastUsed。qps 与 sem 自身并发安全，建桶后不再改写。
	mu       sync.Mutex
	lastUsed time.Time
}

// touch 记录本次使用时间，供空闲回收判定。
func (kl *keyLimiter) touch() {
	kl.mu.Lock()
	kl.lastUsed = time.Now()
	kl.mu.Unlock()
}

// idleFor 返回距上次使用经过的时长。
func (kl *keyLimiter) idleFor(now time.Time) time.Duration {
	kl.mu.Lock()
	defer kl.mu.Unlock()
	return now.Sub(kl.lastUsed)
}
```

`getOrCreateLimiter` 整体替换为（签名多收一个 cfg，避免在持锁期间再读 `rl.cfg`）：

```go
// getOrCreateLimiter returns an existing limiter for the key or creates a new one
func (rl *RateLimiter) getOrCreateLimiter(key string, cfg config.RateLimitConfig) *keyLimiter {
	rl.mu.RLock()
	kl, ok := rl.limiters[key]
	rl.mu.RUnlock()
	if ok {
		kl.touch()
		return kl
	}

	rl.mu.Lock()
	defer rl.mu.Unlock()

	kl, ok = rl.limiters[key]
	if ok {
		kl.touch()
		return kl
	}

	kl = newKeyLimiter(cfg.DefaultQPS, cfg.DefaultConcurrency)
	rl.limiters[key] = kl
	return kl
}
```

`cleanup` 里的时间比较替换为：

```go
	now := time.Now()
	for key, kl := range rl.limiters {
		if kl.idleFor(now) > maxAge {
			delete(rl.limiters, key)
		}
	}
```

`newKeyLimiter` 里的 `&keyLimiter{lastUsed: time.Now()}` 保持不变——此时桶尚未发布给其他协程，无需持锁。

`cleanupLoop` 不读 `rl.cfg`，只用形参 `interval`，无需改动。

- [x] **Step 6: 运行测试确认通过**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/ratelimit/ -race -count=1`
Expected: PASS，全部用例通过且无 `DATA RACE` 输出

- [ ] **Step 7: 提交**（未执行：按项目规范，提交由使用者发起）

```bash
cd /Users/zhangfengda/workspace/groot
git add internal/ratelimit/limiter.go internal/ratelimit/limiter_test.go
git commit -m "feat(ratelimit): 支持在线重建限流参数"
```

**审查后修订（以此为准，覆盖上文 Step 3、4 的对应片段）：**

1. `Acquire` 里两处回滚全局名额的 `<-global.sem` 改为非阻塞（`select { case <-global.sem: default: }`）。全局桶被替换后新桶可能出现「持有者多于令牌」的欠账，阻塞接收会让请求永久挂起。
2. `Release` 不再检查 `cfg.Enabled`，无条件做非阻塞归还。关停后早退会让关停前取到的 per-key 名额永不归还，热 key 的并发上限永久缩水。
3. `Reconfigure` 只在 `GlobalQPS` 或 `GlobalConcurrency` 变化时重建全局桶，未变则保留桶及其令牌状态，减少欠账的触发面。比较与重建都在写锁内完成。
4. 测试补强：新增 `TestReconfigure_DisableMidFlightDoesNotLeak`；`TestReconfigure_ConcurrentWithTraffic` 设 `GlobalConcurrency: 4` 并加一个循环调 `rl.cleanup(time.Hour)` 的 goroutine；`TestReconfigure_RebuildsGlobalLimiter` 追加「参数归零后 `rl.global` 为 nil」与「参数未变时指针不变」两组断言。

### Task 2: 限流参数进配置表

**Files:**
- Modify: `internal/setting/runtime.go:26-42`（键常量）、`:46-70`（边界常量）、`:81-86`（`RuntimeSettings`）、`:93-162`（`Validate`）、`:180-235`（解码）、`:242-270`（`rows`）
- Modify: `internal/setting/settings.go:66-69`（`Security`）、`:137-157`（`Runtime`）、`:283-290`（`RuntimeStatic`）
- Test: `internal/setting/runtime_test.go`

`RateLimitConfig` 的六个字段里 `CleanupInterval` 留在 YAML，其余五个进表。

- [x] **Step 1: 写失败的测试**

追加到 `internal/setting/runtime_test.go` 末尾：

```go
// TestRuntimeSettings_ValidateRateLimit 验证限流字段的边界。
func TestRuntimeSettings_ValidateRateLimit(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(*RuntimeSettings)
		wantErr bool
	}{
		{"合法", func(r *RuntimeSettings) {}, false},
		{"QPS 为零表示不限速", func(r *RuntimeSettings) { r.RateLimit.DefaultQPS = 0 }, false},
		{"QPS 为负", func(r *RuntimeSettings) { r.RateLimit.DefaultQPS = -1 }, true},
		{"QPS 超上限", func(r *RuntimeSettings) { r.RateLimit.DefaultQPS = MaxRateLimitQPS + 1 }, true},
		{"并发为零表示不限并发", func(r *RuntimeSettings) { r.RateLimit.DefaultConcurrency = 0 }, false},
		{"并发为负", func(r *RuntimeSettings) { r.RateLimit.DefaultConcurrency = -1 }, true},
		{"并发超上限", func(r *RuntimeSettings) {
			r.RateLimit.DefaultConcurrency = MaxRateLimitConcurrency + 1
		}, true},
		{"全局 QPS 为负", func(r *RuntimeSettings) { r.RateLimit.GlobalQPS = -1 }, true},
		{"全局并发超上限", func(r *RuntimeSettings) {
			r.RateLimit.GlobalConcurrency = MaxRateLimitConcurrency + 1
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			r := validCfg()
			c.mutate(&r)
			err := r.Validate()
			if c.wantErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !c.wantErr && err != nil {
				t.Fatalf("expected nil, got %v", err)
			}
			if c.wantErr && !errors.Is(err, ErrInvalidSetting) {
				t.Errorf("error should wrap ErrInvalidSetting, got %v", err)
			}
		})
	}
}

// TestRateLimitFrom_FallsBackToBase 验证表内缺键时保持 YAML 值，脏数据退回基准值。
func TestRateLimitFrom_FallsBackToBase(t *testing.T) {
	base := config.RateLimitConfig{
		Enabled: true, GlobalQPS: 100, GlobalConcurrency: 50,
		DefaultQPS: 10, DefaultConcurrency: 5, CleanupInterval: "5m",
	}

	got := rateLimitFrom(base, map[string]string{})
	if got != base {
		t.Errorf("空表应保持基准值，得到 %+v", got)
	}

	got = rateLimitFrom(base, map[string]string{
		KeyRateLimitEnabled:    "false",
		KeyRateLimitDefaultQPS: "不是数字",
	})
	if got.Enabled {
		t.Error("Enabled 应被表内的 false 覆盖")
	}
	if got.DefaultQPS != 10 {
		t.Errorf("DefaultQPS = %v, 脏数据应退回基准值 10", got.DefaultQPS)
	}
	if got.CleanupInterval != "5m" {
		t.Errorf("CleanupInterval 不在表内，应保持 %q", base.CleanupInterval)
	}
}

// TestRuntimeSettings_RowsIncludeRateLimit 验证限流五键随整体保存写入。
func TestRuntimeSettings_RowsIncludeRateLimit(t *testing.T) {
	r := validCfg()
	r.RateLimit.DefaultQPS = 12.5
	r.RateLimit.Enabled = true

	found := map[string]string{}
	for _, row := range r.rows() {
		found[row.Name] = row.Value
	}
	if found[KeyRateLimitEnabled] != "true" {
		t.Errorf("%s = %q, want true", KeyRateLimitEnabled, found[KeyRateLimitEnabled])
	}
	if found[KeyRateLimitDefaultQPS] != "12.5" {
		t.Errorf("%s = %q, want 12.5", KeyRateLimitDefaultQPS, found[KeyRateLimitDefaultQPS])
	}
	for _, k := range []string{
		KeyRateLimitGlobalQPS, KeyRateLimitGlobalConcurrency, KeyRateLimitDefaultConcurrency,
	} {
		if _, ok := found[k]; !ok {
			t.Errorf("缺少键 %s", k)
		}
	}
	if _, ok := found["security.rate_limit.cleanup_interval"]; ok {
		t.Error("cleanup_interval 留在 YAML，不应写表")
	}
}
```

该文件已有 `validCfg`（约在 `:140`）与 `staticCfg`（约在 `:13`）两个辅助函数。两者都要补上限流分类，否则新用例读到零值。

`validCfg` 的返回值里加一行：

```go
		RateLimit:  config.RateLimitConfig{Enabled: true, DefaultQPS: 100, DefaultConcurrency: 10},
```

`staticCfg` 的返回值里加一行（注意它返回 `config.Config`，限流位于 `Security` 之下）：

```go
		Security: config.SecurityConfig{
			RateLimit: config.RateLimitConfig{
				Enabled: true, GlobalQPS: 200, GlobalConcurrency: 100,
				DefaultQPS: 50, DefaultConcurrency: 20, CleanupInterval: "5m",
			},
		},
```

- [x] **Step 2: 运行测试确认失败**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/setting/ -run 'RateLimit' -count=1`
Expected: FAIL，编译错误 `r.RateLimit undefined` 与 `undefined: KeyRateLimitEnabled`

- [x] **Step 3: 加键常量与边界常量**

`internal/setting/runtime.go`，在 `KeyAttachmentAllowedTypes` 之后、`)` 之前插入：

```go

	KeyRateLimitEnabled            = "security.rate_limit.enabled"
	KeyRateLimitGlobalQPS          = "security.rate_limit.global_qps"
	KeyRateLimitGlobalConcurrency  = "security.rate_limit.global_concurrency"
	KeyRateLimitDefaultQPS         = "security.rate_limit.default_qps"
	KeyRateLimitDefaultConcurrency = "security.rate_limit.default_concurrency"
```

在 `MaxAttachmentTypeCount` 之后、`)` 之前插入：

```go

	// QPS 与并发上限为 0 表示该维度不限制，因此下界是 0 而非 1
	MaxRateLimitQPS         = 100000
	MaxRateLimitConcurrency = 100000
```

- [x] **Step 4: 结构体加分类**

`RuntimeSettings` 加一个字段，并在其文档注释末尾追加一段：

```go
type RuntimeSettings struct {
	Memory     config.MemoryConfig
	React      config.ReactConfig
	SubAgent   config.SubAgentConfig
	Attachment config.AttachmentConfig
	RateLimit  config.RateLimitConfig
}
```

追加到该结构体上方注释的末尾：

```go
// RateLimit 的生效方式同样需要通知持有方：写表之外还需调用
// ratelimit.RateLimiter.Reconfigure 替换限流参数。其 CleanupInterval
// 不进配置表，恒为 YAML 值——它是后台回收协程的周期，改动需重启。
```

- [x] **Step 5: 加校验**

在 `Validate` 里 `attachment.allowed_types` 的循环之后、`return nil` 之前插入：

```go
	if err := checkFloatRange("security.rate_limit.global_qps", r.RateLimit.GlobalQPS,
		0, MaxRateLimitQPS); err != nil {
		return err
	}
	if err := checkFloatRange("security.rate_limit.default_qps", r.RateLimit.DefaultQPS,
		0, MaxRateLimitQPS); err != nil {
		return err
	}
	if err := checkRange("security.rate_limit.global_concurrency", r.RateLimit.GlobalConcurrency,
		0, MaxRateLimitConcurrency); err != nil {
		return err
	}
	if err := checkRange("security.rate_limit.default_concurrency", r.RateLimit.DefaultConcurrency,
		0, MaxRateLimitConcurrency); err != nil {
		return err
	}
```

在 `checkRange` 之后新增浮点版本：

```go
// checkFloatRange 校验浮点数落在闭区间内。QPS 是浮点值，
// 复用 checkRange 会在取整时把 0.5 这类合法值判成 0。
func checkFloatRange(name string, v, min, max float64) error {
	if v < min || v > max {
		return fmt.Errorf("%w: %s 应在 %g 与 %g 之间，当前为 %g",
			ErrInvalidSetting, name, min, max, v)
	}
	return nil
}
```

- [x] **Step 6: 加解码与编码**

在 `attachmentFrom` 之后插入：

```go
func rateLimitFrom(base config.RateLimitConfig, vals map[string]string) config.RateLimitConfig {
	if raw, ok := vals[KeyRateLimitEnabled]; ok {
		base.Enabled = parseBool(raw, base.Enabled)
	}
	if raw, ok := vals[KeyRateLimitGlobalQPS]; ok {
		base.GlobalQPS = parseFloat(raw, base.GlobalQPS)
	}
	if raw, ok := vals[KeyRateLimitGlobalConcurrency]; ok {
		base.GlobalConcurrency = parseInt(raw, base.GlobalConcurrency)
	}
	if raw, ok := vals[KeyRateLimitDefaultQPS]; ok {
		base.DefaultQPS = parseFloat(raw, base.DefaultQPS)
	}
	if raw, ok := vals[KeyRateLimitDefaultConcurrency]; ok {
		base.DefaultConcurrency = parseInt(raw, base.DefaultConcurrency)
	}
	return base
}
```

在 `parseInt` 之后插入：

```go
// parseFloat 解析浮点数；无法解析时返回 fallback。
func parseFloat(raw string, fallback float64) float64 {
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return fallback
	}
	return f
}
```

在 `rows()` 的 `kv` 切片里，`KeyAttachmentAllowedTypes` 那一行之后插入：

```go
		{KeyRateLimitEnabled, strconv.FormatBool(r.RateLimit.Enabled)},
		{KeyRateLimitGlobalQPS, formatFloat(r.RateLimit.GlobalQPS)},
		{KeyRateLimitGlobalConcurrency, strconv.Itoa(r.RateLimit.GlobalConcurrency)},
		{KeyRateLimitDefaultQPS, formatFloat(r.RateLimit.DefaultQPS)},
		{KeyRateLimitDefaultConcurrency, strconv.Itoa(r.RateLimit.DefaultConcurrency)},
```

在 `parseFloat` 之后插入格式化函数：

```go
// formatFloat 用最短往返表示写出浮点值，避免 100 被写成 100.000000。
func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}
```

- [x] **Step 7: 配置对象带上新分类**

`internal/setting/settings.go`。`Runtime` 的 `base` 与返回值各加一行：

```go
	base := RuntimeSettings{
		Memory:     s.static.Memory,
		React:      s.static.React,
		SubAgent:   s.static.SubAgent,
		Attachment: s.static.Attachment,
		RateLimit:  s.static.Security.RateLimit,
	}
```

```go
	return RuntimeSettings{
		Memory:     memoryFrom(base.Memory, vals),
		React:      reactFrom(base.React, vals),
		SubAgent:   subAgentFrom(base.SubAgent, vals),
		Attachment: attachmentFrom(base.Attachment, vals),
		RateLimit:  rateLimitFrom(base.RateLimit, vals),
	}, nil
```

`RuntimeStatic` 加一行：

```go
		RateLimit:  s.static.Security.RateLimit,
```

`Security` 整体替换，让限流子结构走配置表，认证部分仍来自 YAML：

```go
// Security 返回安全配置。Auth 部分来自 YAML，RateLimit 部分叠加配置表。
// 中间件在启动时拿到限流器实例，限流参数的在线改动由
// ratelimit.RateLimiter.Reconfigure 承接，这里的返回值供接口回读与启动应用。
func (s *Settings) Security(ctx context.Context) (config.SecurityConfig, error) {
	out := s.static.Security
	if s.repo == nil {
		return out, nil
	}
	vals, err := s.globalValues(ctx)
	if err != nil {
		return config.SecurityConfig{}, err
	}
	out.RateLimit = rateLimitFrom(out.RateLimit, vals)
	return out, nil
}
```

- [x] **Step 8: 运行测试确认通过**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/setting/ -count=1`
Expected: PASS，全部用例通过

- [ ] **Step 9: 提交**（未执行：按项目规范，提交由使用者发起）

```bash
cd /Users/zhangfengda/workspace/groot
git add internal/setting/runtime.go internal/setting/settings.go internal/setting/runtime_test.go
git commit -m "feat(setting): 限流参数纳入配置表"
```

### Task 3: 限流参数接入配置接口与启动路径

**Files:**
- Modify: `internal/api/types/types.go:296-301`（`RuntimeSettingsPayload`）、`:329`（末尾追加类型）
- Modify: `internal/api/handler/setting.go:25-35`（结构体与构造）、`:107-131`（`PutRuntime`）、`:134-179`（两个转换函数）
- Modify: `internal/api/server.go:77-83`（限流器构造）、`NewSettingHandler` 调用处
- Modify: `cmd/groot/main.go`（服务启动前应用表内限流值）
- Test: `internal/api/handler/setting_test.go`

- [x] **Step 1: 写失败的测试**

追加到 `internal/api/handler/setting_test.go` 末尾：

```go
// TestSettingHandler_PutRuntimeAppliesRateLimit 验证保存后限流器即刻按新参数工作。
func TestSettingHandler_PutRuntimeAppliesRateLimit(t *testing.T) {
	h, rl := newRuntimeHandlerWithLimiter(t)
	defer rl.Stop()

	body := runtimeBodyWith(`"rate_limit":{"enabled":true,"global_qps":0,"global_concurrency":0,
		"default_qps":7,"default_concurrency":3}`)
	rc := callJSON(h.PutRuntime, consts.MethodPut, body, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}

	got := rl.Config()
	if got.DefaultQPS != 7 {
		t.Errorf("DefaultQPS = %v, want 7", got.DefaultQPS)
	}
	if got.DefaultConcurrency != 3 {
		t.Errorf("DefaultConcurrency = %d, want 3", got.DefaultConcurrency)
	}
	if !got.Enabled {
		t.Error("Enabled 应为 true")
	}
	// CleanupInterval 不在请求体内，应保持 YAML 值
	if got.CleanupInterval != "1m" {
		t.Errorf("CleanupInterval = %q, want 1m（不随接口改动）", got.CleanupInterval)
	}
}

// TestSettingHandler_PutRuntimeRejectsBadRateLimit 验证越界请求既不写表也不动限流器。
func TestSettingHandler_PutRuntimeRejectsBadRateLimit(t *testing.T) {
	h, rl := newRuntimeHandlerWithLimiter(t)
	defer rl.Stop()

	before := rl.Config()
	body := runtimeBodyWith(`"rate_limit":{"enabled":true,"global_qps":0,"global_concurrency":0,
		"default_qps":-5,"default_concurrency":3}`)
	rc := callJSON(h.PutRuntime, consts.MethodPut, body, nil)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "invalid_request" {
		t.Errorf("status = %q, want invalid_request", s)
	}
	if rl.Config() != before {
		t.Error("越界请求不应改动限流器")
	}

	rc = callJSON(h.GetRuntime, consts.MethodGet, "", nil)
	var out types.RuntimeSettingsPayload
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if out.RateLimit.DefaultQPS != 50 {
		t.Errorf("default_qps = %v, 应仍为 YAML 值 50", out.RateLimit.DefaultQPS)
	}
}

// TestSettingHandler_GetRuntimeIncludesRateLimit 验证回读带上限流分区。
func TestSettingHandler_GetRuntimeIncludesRateLimit(t *testing.T) {
	h, rl := newRuntimeHandlerWithLimiter(t)
	defer rl.Stop()

	rc := callJSON(h.GetRuntime, consts.MethodGet, "", nil)
	var out types.RuntimeSettingsPayload
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if out.RateLimit.DefaultQPS != 50 || out.RateLimit.DefaultConcurrency != 20 {
		t.Errorf("限流分区 = %+v, want YAML 的 50/20", out.RateLimit)
	}
}

// newRuntimeHandlerWithLimiter 建一个带真实限流器的 handler。
func newRuntimeHandlerWithLimiter(t *testing.T) (*SettingHandler, *ratelimit.RateLimiter) {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })

	cfg := runtimeYAML()
	cfg.Security.RateLimit = config.RateLimitConfig{
		Enabled: true, DefaultQPS: 50, DefaultConcurrency: 20, CleanupInterval: "1m",
	}
	rl, err := ratelimit.New(cfg.Security.RateLimit)
	if err != nil {
		t.Fatalf("ratelimit.New: %v", err)
	}
	models := llm.NewModelService(modeldb.New(sqlxDB, dialect))
	settings := setting.New(cfg, settingdb.New(sqlxDB, dialect))
	h := NewSettingHandler(SettingHandlerDeps{
		Settings: settings,
		Models:   models,
		Registry: agent.NewRegistryForTest(4),
		Limiter:  rl,
		Log:      logger.NewNop(),
	})
	return h, rl
}

// runtimeBodyWith 拼一个合法的运行时配置请求体，用 extra 覆盖或补充分区。
// 越界用例只想改坏一个字段，其余分区必须合法，否则校验会先被别处拦下。
func runtimeBodyWith(extra string) string {
	return `{
		"memory":{"history_window":20},
		"react":{"max_iterations":30,"step_timeout":120,"error_retry":2},
		"subagent":{"max_concurrency":4,"exec_timeout":"5m",
			"max_task_length":3000,"max_result_length":8000},
		"attachment":{"max_size":10,"max_total_size":50,"max_count":5,"allowed_types":[]},
		` + extra + `}`
}
```

把 `ratelimit` 与 `config` 加进该文件的 import 块（`config` 已在，只需补 `ratelimit`）：

```go
	"github.com/zfd81/groot/internal/ratelimit"
```

该文件已有的 `newSettingHandlerForTest` 与 `newRuntimeHandlerForTest` 两处 `NewSettingHandler` 调用改为命名参数写法。限流器对它们的用例无关，省略即为 nil，handler 会跳过：

```go
	return NewSettingHandler(SettingHandlerDeps{
		Settings: settings,
		Models:   models,
		Registry: agent.NewRegistryForTest(4),
		Log:      logger.NewNop(),
	})
```

`runtimeYAML` 也补上限流分类，让 `newRuntimeHandlerForTest` 的回读有确定基准：

```go
		Security: config.SecurityConfig{
			RateLimit: config.RateLimitConfig{
				Enabled: true, DefaultQPS: 50, DefaultConcurrency: 20, CleanupInterval: "1m",
			},
		},
```

- [x] **Step 2: 运行测试确认失败**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/api/handler/ -run 'RateLimit' -count=1`
Expected: FAIL，编译错误 `out.RateLimit undefined` 与 `undefined: SettingHandlerDeps`

- [x] **Step 3: 接口结构加分区**

`internal/api/types/types.go`。`RuntimeSettingsPayload` 加一个字段并改注释——它当前的注释声明不含 `subagent.max_concurrency`，那句话已经过时：

```go
// RuntimeSettingsPayload 是 /web/settings/runtime 的请求与响应体。
// 读写同构：界面按分区整体提交，回读的字段与提交的字段一一对应，
// 前端无需为两个方向维护两套结构。
//
// 不含 security.rate_limit.cleanup_interval：它是后台回收协程的周期，改动需重启才生效。
type RuntimeSettingsPayload struct {
	Memory     MemorySettings     `json:"memory"`
	React      ReactSettings      `json:"react"`
	SubAgent   SubAgentSettings   `json:"subagent"`
	Attachment AttachmentSettings `json:"attachment"`
	RateLimit  RateLimitSettings  `json:"rate_limit"`
}
```

在 `AttachmentSettings` 之后追加：

```go
// RateLimitSettings 限流参数。QPS 与并发上限为 0 表示该维度不限制。
type RateLimitSettings struct {
	Enabled            bool    `json:"enabled"`
	GlobalQPS          float64 `json:"global_qps"`
	GlobalConcurrency  int     `json:"global_concurrency"`
	DefaultQPS         float64 `json:"default_qps"`
	DefaultConcurrency int     `json:"default_concurrency"`
}
```

- [x] **Step 4: handler 收下限流器并在保存后重建**

`internal/api/handler/setting.go`。结构体与构造函数替换为：

```go
// SettingHandler 处理配置的读写。接口按分类而非按单键暴露，
// 与设置面板的分区一一对应。
//
// registry 与 limiter 是两个生效载体：配置表是持久来源，它们是运行中的实例。
// 两者都要更新——只写表则要等重启才生效，只改实例则重启后丢失。
type SettingHandler struct {
	settings *setting.Settings
	models   *llm.ModelService
	registry *agent.SubAgentRegistry
	limiter  *ratelimit.RateLimiter
	log      *logger.Logger
}

// SettingHandlerDeps 是 NewSettingHandler 的命名参数集合。
// 生效载体会随配置项迁移逐个增加，位置参数到第五个已难以辨认谁是谁，
// 与本仓库 CallAgentToolConfig 的做法一致。
type SettingHandlerDeps struct {
	Settings *setting.Settings
	Models   *llm.ModelService
	Registry *agent.SubAgentRegistry
	Limiter  *ratelimit.RateLimiter
	Log      *logger.Logger
}

func NewSettingHandler(deps SettingHandlerDeps) *SettingHandler {
	return &SettingHandler{
		settings: deps.Settings,
		models:   deps.Models,
		registry: deps.Registry,
		limiter:  deps.Limiter,
		log:      deps.Log,
	}
}
```

import 块补一行：

```go
	"github.com/zfd81/groot/internal/ratelimit"
```

`PutRuntime` 里写表成功之后的那段替换为：

```go
	// 写表在前、通知持有方在后：写表失败时上面已返回，不会留下
	// 「实例已改、表里还是旧值」的状态
	if h.registry != nil {
		h.registry.SetMaxConcurrency(next.SubAgent.MaxConcurrency)
	}
	if h.limiter != nil {
		h.limiter.Reconfigure(next.RateLimit)
	}
	rc.JSON(200, runtimeToPayload(next))
```

同时把 `PutRuntime` 的函数注释末尾追加一段：

```go
// 限流参数在写表成功后同步到限流器。已建桶的调用方保留旧容量，
// 随后首次出现的调用方按新参数建桶，正在进行的请求不被打断。
```

- [x] **Step 5: 两个转换函数带上限流**

`runtimeToPayload` 的返回值里，`Attachment` 之后加一段：

```go
		RateLimit: types.RateLimitSettings{
			Enabled:            r.RateLimit.Enabled,
			GlobalQPS:          r.RateLimit.GlobalQPS,
			GlobalConcurrency:  r.RateLimit.GlobalConcurrency,
			DefaultQPS:         r.RateLimit.DefaultQPS,
			DefaultConcurrency: r.RateLimit.DefaultConcurrency,
		},
```

`payloadToRuntime` 需要一个 base 参数来保住不在请求体内的 `CleanupInterval`。整体替换为：

```go
// payloadToRuntime 把接口结构还原成配置对象的结构。
// base 提供请求体不携带的字段，当前是 RateLimit.CleanupInterval：
// 它留在 YAML，整体写入时必须从当次生效值取回，否则会被清空。
func payloadToRuntime(p types.RuntimeSettingsPayload, base setting.RuntimeSettings) setting.RuntimeSettings {
	out := base
	out.Memory.HistoryWindow = p.Memory.HistoryWindow
	out.React.MaxIterations = p.React.MaxIterations
	out.React.StepTimeout = p.React.StepTimeout
	out.React.ErrorRetry = p.React.ErrorRetry
	out.SubAgent.MaxConcurrency = p.SubAgent.MaxConcurrency
	out.SubAgent.ExecTimeout = strings.TrimSpace(p.SubAgent.ExecTimeout)
	out.SubAgent.MaxTaskLength = p.SubAgent.MaxTaskLength
	out.SubAgent.MaxResultLength = p.SubAgent.MaxResultLength
	out.Attachment.MaxSize = p.Attachment.MaxSize
	out.Attachment.MaxTotalSize = p.Attachment.MaxTotalSize
	out.Attachment.MaxCount = p.Attachment.MaxCount
	out.Attachment.AllowedTypes = p.Attachment.AllowedTypes
	out.RateLimit.Enabled = p.RateLimit.Enabled
	out.RateLimit.GlobalQPS = p.RateLimit.GlobalQPS
	out.RateLimit.GlobalConcurrency = p.RateLimit.GlobalConcurrency
	out.RateLimit.DefaultQPS = p.RateLimit.DefaultQPS
	out.RateLimit.DefaultConcurrency = p.RateLimit.DefaultConcurrency
	return out
}
```

`PutRuntime` 里构造 `next` 的那行随之改为先取当次生效值。把 `BindJSON` 之后、`next :=` 那一行替换为：

```go
	// 取一次当次生效值，为请求体不携带的字段提供底值
	current, err := h.settings.Runtime(ctx)
	if err != nil {
		h.log.Error("读取运行时配置失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
		return
	}

	next := payloadToRuntime(req, current)
```

`PutRuntime` 原本用 `:=` 声明 `err`，现在 `err` 已由上面的 `current, err :=` 声明，把 `if err := h.settings.SetRuntime(ctx, next); err != nil {` 保持原样即可——它是新作用域里的变量，不冲突。

- [x] **Step 6: server 与 main 装配**

`internal/api/server.go`，`NewSettingHandler` 调用处改为命名参数并补入限流器。该行目前形如 `settingH := handler.NewSettingHandler(settings, models, subAgentReg, log)`，改为：

```go
	settingH := handler.NewSettingHandler(handler.SettingHandlerDeps{
		Settings: settings,
		Models:   models,
		Registry: subAgentReg,
		Limiter:  rateLimiter,
		Log:      log,
	})
```

`rateLimiter` 在同一函数内、`NewRateLimitMiddleware` 之前已构造，直接可用。

`cmd/groot/main.go`，在 `api.NewServer(...)` 调用之前插入一段，把表内限流值应用到 YAML 构造出的限流器。找到创建 `settings` 之后、启动 HTTP 服务之前的位置：

```go
	// 启动时把配置表里的限流值补到 cfg 上：限流器在 NewServer 内按 cfg 构造，
	// 不在这里覆盖的话，重启后限流会退回 YAML 值，界面上的改动看似丢失
	if sec, err := settings.Security(context.Background()); err != nil {
		log.Error("读取限流配置失败，本次启动使用 YAML 值", zap.Error(err))
	} else {
		cfg.Security.RateLimit = sec.RateLimit
	}
```

- [x] **Step 7: 运行测试确认通过**

Run: `cd /Users/zhangfengda/workspace/groot && go build ./... && go test ./internal/api/handler/ ./internal/setting/ ./internal/ratelimit/ -race -count=1`
Expected: 构建无输出，三个包全部 PASS

- [ ] **Step 8: 提交**（未执行：按项目规范，提交由使用者发起）

```bash
cd /Users/zhangfengda/workspace/groot
git add internal/api/types/types.go internal/api/handler/setting.go \
	internal/api/handler/setting_test.go internal/api/server.go cmd/groot/main.go
git commit -m "feat(api): 限流参数保存即生效"
```

**审查后修订（以此为准，覆盖上文 Step 3、4、5 的对应片段）：**

1. `RuntimeSettingsPayload.RateLimit` 改为指针 `*RateLimitSettings`。全零对该分区是合法值（0 表示不限制），必须区分「未携带」与「显式关闭」，否则一个不带该分区的旧客户端保存任意一项都会静默关掉限流。`PutRuntime` 在 `BindJSON` 后对 `nil` 返回 400 `invalid_request`「缺少 rate_limit 分区」；`runtimeToPayload` 总是填充指针，JSON 形状不变。
2. `payloadToRuntime` 的 base 改用 `h.settings.RuntimeStatic()`，不再先 `Runtime()` 读表：唯一需要保留的 `CleanupInterval` 不进表，两者永远相同。少一次 DB 往返，无读改写窗口，DB 故障时坏请求仍得 400。
3. 测试 helper 合并：`newRuntimeHandlerForTest` 总是建真实限流器并传入，`newRuntimeHandlerWithLimiter` 移除；限流用例经 `h.limiter.Config()` 断言。
4. 新增 `TestSettingHandler_PutRuntimeRejectsMissingRateLimit`；`TestSettingHandler_PutRuntimeAppliesRateLimit` 末尾补 GET 回读断言；所有既有 PUT 用例请求体补上合法的 `rate_limit` 分区。
5. 记入设计文档：`Reconfigure` 只作用于处理该请求的节点，多节点部署下其他节点到重启才跟上。

## 阶段 B：消息发送器支持动态注册

### Task 4: 消息层支持在线换发送器

**Files:**
- Modify: `internal/message/layer.go:16-57`（结构体、`Register`、`isSenderEnabled`）、`:130-178`（`processJob`）
- Test: `internal/message/layer_test.go`

`senders` 与 `senderConfigs` 两个 map 目前在启动时无锁写入，之后只被 worker 协程读取。要支持在线换发送器就得加锁，否则 `-race` 会报数据竞争。

- [x] **Step 1: 写失败的测试**

追加到 `internal/message/layer_test.go` 末尾：

```go
// TestLayer_SetSenderReplacesInPlace 验证替换发送器后新消息走新实例。
func TestLayer_SetSenderReplacesInPlace(t *testing.T) {
	l := NewLayer(config.MessageConfig{QueueSize: 4, Workers: 1}, logger.NewNop())
	first := &countingSender{name: "webhook"}
	second := &countingSender{name: "webhook"}
	l.SetSender("webhook", first, config.SenderConf{Enabled: true})
	l.Start()
	defer l.Stop()

	if err := publishAndWait(t, l, "webhook"); err != nil {
		t.Fatalf("first publish: %v", err)
	}
	if first.count() != 1 {
		t.Fatalf("first sender count = %d, want 1", first.count())
	}

	l.SetSender("webhook", second, config.SenderConf{Enabled: true})
	if err := publishAndWait(t, l, "webhook"); err != nil {
		t.Fatalf("second publish: %v", err)
	}
	if second.count() != 1 {
		t.Errorf("replacement sender count = %d, want 1", second.count())
	}
	if first.count() != 1 {
		t.Errorf("old sender count = %d, 替换后不应再收到消息", first.count())
	}
}

// TestLayer_RemoveSenderStopsDelivery 验证注销后该渠道不再投递。
func TestLayer_RemoveSenderStopsDelivery(t *testing.T) {
	l := NewLayer(config.MessageConfig{QueueSize: 4, Workers: 1}, logger.NewNop())
	s := &countingSender{name: "webhook"}
	l.SetSender("webhook", s, config.SenderConf{Enabled: true})
	l.Start()
	defer l.Stop()

	l.RemoveSender("webhook")
	results, err := publishCollect(t, l, "webhook")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("注销后应无可用渠道，得到 %d 条结果", len(results))
	}
	if s.count() != 0 {
		t.Errorf("已注销的发送器仍被调用 %d 次", s.count())
	}
}

// TestLayer_SetSenderDisabledSkipsDelivery 验证 Enabled=false 的渠道不投递，
// 但仍留在注册表里：界面上把它关掉再打开不需要重启。
func TestLayer_SetSenderDisabledSkipsDelivery(t *testing.T) {
	l := NewLayer(config.MessageConfig{QueueSize: 4, Workers: 1}, logger.NewNop())
	s := &countingSender{name: "webhook"}
	l.SetSender("webhook", s, config.SenderConf{Enabled: false})
	l.Start()
	defer l.Stop()

	results, err := publishCollect(t, l, "webhook")
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if len(results) != 0 {
		t.Errorf("已禁用渠道不应投递，得到 %d 条结果", len(results))
	}

	l.SetSender("webhook", s, config.SenderConf{Enabled: true})
	if err := publishAndWait(t, l, "webhook"); err != nil {
		t.Fatalf("publish after enabling: %v", err)
	}
	if s.count() != 1 {
		t.Errorf("重新启用后应投递一次，实际 %d 次", s.count())
	}
}

// TestLayer_SetSenderConcurrentWithDelivery 在投递过程中反复换发送器，
// 由 -race 判定读写是否有竞争。
func TestLayer_SetSenderConcurrentWithDelivery(t *testing.T) {
	l := NewLayer(config.MessageConfig{QueueSize: 64, Workers: 4}, logger.NewNop())
	l.SetSender("webhook", &countingSender{name: "webhook"}, config.SenderConf{Enabled: true})
	l.Start()
	defer l.Stop()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			l.SetSender("webhook", &countingSender{name: "webhook"},
				config.SenderConf{Enabled: true})
		}
	}()
	for i := 0; i < 50; i++ {
		ch, err := l.Publish(context.Background(), Event{Title: "t"}, []string{"webhook"})
		if err == nil {
			<-ch
		}
	}
	<-done
}

// countingSender 记录被调用次数的测试发送器。
type countingSender struct {
	name string
	mu   sync.Mutex
	n    int
}

func (s *countingSender) Name() string { return s.name }

func (s *countingSender) Send(_ context.Context, _ Event) SendResult {
	s.mu.Lock()
	s.n++
	s.mu.Unlock()
	return SendResult{Channel: s.name, Success: true, Timestamp: time.Now()}
}

func (s *countingSender) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.n
}

// publishCollect 发一条消息并取回结果，超时即失败。
func publishCollect(t *testing.T, l *Layer, channel string) ([]SendResult, error) {
	t.Helper()
	ch, err := l.Publish(context.Background(), Event{Title: "t"}, []string{channel})
	if err != nil {
		return nil, err
	}
	select {
	case results := <-ch:
		return results, nil
	case <-time.After(2 * time.Second):
		t.Fatal("等待发送结果超时")
		return nil, nil
	}
}

// publishAndWait 发一条消息并要求全部渠道成功。
func publishAndWait(t *testing.T, l *Layer, channel string) error {
	t.Helper()
	results, err := publishCollect(t, l, channel)
	if err != nil {
		return err
	}
	if len(results) != 1 {
		t.Fatalf("结果条数 = %d, want 1", len(results))
	}
	if !results[0].Success {
		t.Fatalf("发送失败: %s", results[0].Message)
	}
	return nil
}
```

该文件的 import 块已含 `context`、`testing`、`time`、`internal/config`、`internal/logger`，只缺一个。在 `"context"` 之后补上：

```go
	"sync"
```

- [x] **Step 2: 运行测试确认失败**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/message/ -run 'SetSender|RemoveSender' -count=1`
Expected: FAIL，编译错误 `l.SetSender undefined` 与 `l.RemoveSender undefined`

- [x] **Step 3: 给两个 map 加锁并新增三个方法**

`internal/message/layer.go`。结构体加一把锁：

```go
// Layer is the message notification layer
//
// senders 与 senderConfigs 可在运行期被替换（设置面板改完即生效），
// 由 mu 保护。取发送器与投递分成两步：先在锁内拿到实例，
// 再在锁外调用 Send——发送是网络操作，持锁会把整个消息层卡住。
type Layer struct {
	mu            sync.RWMutex
	queue         chan *sendJob
	queueSize     int
	senders       map[string]Sender
	senderConfigs map[string]config.SenderConf
	workers       int
	stopCh        chan struct{}
	wg            sync.WaitGroup
	log           *logger.Logger
}
```

`Register` 与 `isSenderEnabled` 整体替换为：

```go
// Register registers a sender with its config.
// 启动路径使用；运行期改动走 SetSender。
func (l *Layer) Register(name string, sender Sender, cfg config.SenderConf) {
	l.SetSender(name, sender, cfg)
}

// SetSender 注册或替换一个发送器，随后入队的消息即走新实例。
// 正在投递中的消息握着旧实例的指针，按旧配置发完——
// 中途换掉会让一次已开始的网络请求结果无处归属。
func (l *Layer) SetSender(name string, sender Sender, cfg config.SenderConf) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.senders[name] = sender
	l.senderConfigs[name] = cfg
}

// RemoveSender 注销一个发送器，此后该渠道被视为不可用。
func (l *Layer) RemoveSender(name string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	delete(l.senders, name)
	delete(l.senderConfigs, name)
}

// resolve 在锁内取出一个可用渠道的发送器实例。
// 第二个返回值为 false 表示未注册或已禁用。
func (l *Layer) resolve(name string) (Sender, bool) {
	l.mu.RLock()
	defer l.mu.RUnlock()
	cfg, ok := l.senderConfigs[name]
	if !ok || !cfg.Enabled {
		return nil, false
	}
	s, ok := l.senders[name]
	return s, ok
}
```

- [x] **Step 4: processJob 走 resolve**

把 `processJob` 里筛选渠道那段与调用 `Send` 那行改成一次解析、后续复用。筛选段替换为：

```go
	var enabledChannels []string
	var targets []Sender
	for _, name := range job.channels {
		if s, ok := l.resolve(name); ok {
			enabledChannels = append(enabledChannels, name)
			targets = append(targets, s)
		}
	}
```

`go func(idx int, channelName string)` 那段改为把发送器一并传进去：

```go
	results := make([]SendResult, len(enabledChannels))
	var wg sync.WaitGroup
	for i, name := range enabledChannels {
		wg.Add(1)
		go func(idx int, channelName string, sender Sender) {
			defer wg.Done()
			defer func() {
				if r := recover(); r != nil {
					results[idx] = SendResult{
						Channel: channelName,
						Success: false,
						Message: fmt.Sprintf("panic: %v", r),
					}
					l.log.Error("Sender panic",
						zap.String("channel", channelName),
						zap.String("title", job.event.Title),
						zap.Any("panic", r),
					)
				}
			}()
			results[idx] = sender.Send(ctx, job.event)
		}(i, name, targets[i])
	}
	wg.Wait()
```

这样 `l.senders[channelName]` 的无锁读取被消除，发送器实例在解析时就已确定。

- [x] **Step 5: 运行测试确认通过**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/message/ -race -count=1`
Expected: PASS，无 `DATA RACE` 报告

- [ ] **Step 6: 提交**（未执行：按项目规范，提交由使用者发起）

```bash
cd /Users/zhangfengda/workspace/groot
git add internal/message/layer.go internal/message/layer_test.go
git commit -m "feat(message): 消息层支持在线注册与注销发送器"
```

**审查后修订（以此为准）：** `senders` 与 `senderConfigs` 两个 map 合并为 `senders map[string]registration`，`registration{sender Sender; cfg config.SenderConf}` 为包内类型。单 map 使 `SetSender`/`RemoveSender` 成为单次写入、`resolve` 成为单次查找，不存在两表漂移的可能；Task 6 的 `ChannelEnabled` 直接复用 `resolve`。`SetSender` 注释措辞为「随后开始处理的消息即走新实例」：解析发生在 worker 取出消息时，已在队列里的消息同样走新实例。并发测试改为先发后收、替换与投递交错。

### Task 5: 发送器配置进配置表

**Files:**
- Create: `internal/setting/message.go`
- Create: `internal/setting/message_test.go`
- Modify: `internal/setting/settings.go`（新增 `Message` 与 `SetMessage`）

发送器配置是一组「每渠道多字段」的值，键名带渠道名这一层，与运行时配置的扁平键形状不同，所以单独成文件而非塞进 `runtime.go`。

只有 webhook 与 email 两个渠道进表。stdout 无参数可配，恒为启用，不进表。

- [x] **Step 1: 写失败的测试**

创建 `internal/setting/message_test.go`：

```go
// internal/setting/message_test.go
package setting

import (
	"context"
	"errors"
	"testing"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/repo/settingdb"
)

// messageStaticCfg 构造带两个发送器的 YAML 基准值。
func messageStaticCfg() config.Config {
	return config.Config{
		Message: config.MessageConfig{
			QueueSize: 100, Workers: 4,
			Senders: map[string]config.SenderConf{
				"webhook": {Enabled: true, URL: "https://yaml.example.com/hook"},
				"email": {
					Enabled: false, SMTPHost: "smtp.yaml.test", SMTPPort: 587,
					Username: "yaml-user", Password: "yaml-pass", From: "yaml@test",
				},
			},
		},
	}
}

// newMessageSettings 建一个带真实配置表的配置对象。
func newMessageSettings(t *testing.T) *Settings {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })
	return New(messageStaticCfg(), settingdb.New(sqlxDB, dialect))
}

func TestMessage_EmptyTableKeepsYAML(t *testing.T) {
	s := newMessageSettings(t)

	got, err := s.Message(context.Background())
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if got.Senders["webhook"].URL != "https://yaml.example.com/hook" {
		t.Errorf("webhook URL = %q, 应保持 YAML 值", got.Senders["webhook"].URL)
	}
	if got.Senders["email"].SMTPPort != 587 {
		t.Errorf("email SMTPPort = %d, want 587", got.Senders["email"].SMTPPort)
	}
	if got.QueueSize != 100 || got.Workers != 4 {
		t.Errorf("队列参数不进表，应保持 YAML: %+v", got)
	}
}

func TestSetMessage_RoundTrip(t *testing.T) {
	s := newMessageSettings(t)
	ctx := context.Background()

	err := s.SetMessage(ctx, map[string]config.SenderConf{
		"webhook": {Enabled: false, URL: "https://table.example.com/hook"},
		"email": {
			Enabled: true, SMTPHost: "smtp.table.test", SMTPPort: 465,
			Username: "table-user", Password: "table-pass", From: "table@test",
		},
	})
	if err != nil {
		t.Fatalf("SetMessage: %v", err)
	}

	got, err := s.Message(ctx)
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	w := got.Senders["webhook"]
	if w.Enabled || w.URL != "https://table.example.com/hook" {
		t.Errorf("webhook 回读 = %+v", w)
	}
	e := got.Senders["email"]
	if !e.Enabled || e.SMTPHost != "smtp.table.test" || e.SMTPPort != 465 {
		t.Errorf("email 回读 = %+v", e)
	}
	if e.Password != "table-pass" {
		t.Errorf("Password = %q, want table-pass", e.Password)
	}
	if got.QueueSize != 100 {
		t.Errorf("QueueSize = %d, 不应被 SetMessage 改动", got.QueueSize)
	}
}

func TestSetMessage_EmptyPasswordKeepsStored(t *testing.T) {
	// 空串表示「不改密码」：界面回读时密码是脱敏值，
	// 原样提交会把星号存成真密码
	s := newMessageSettings(t)
	ctx := context.Background()

	err := s.SetMessage(ctx, map[string]config.SenderConf{
		"email": {Enabled: true, SMTPHost: "smtp.a.test", SMTPPort: 25, Password: "first-pass"},
	})
	if err != nil {
		t.Fatalf("首次保存: %v", err)
	}
	err = s.SetMessage(ctx, map[string]config.SenderConf{
		"email": {Enabled: true, SMTPHost: "smtp.b.test", SMTPPort: 25, Password: ""},
	})
	if err != nil {
		t.Fatalf("二次保存: %v", err)
	}

	got, err := s.Message(ctx)
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if got.Senders["email"].Password != "first-pass" {
		t.Errorf("Password = %q, 空串应保留原密码", got.Senders["email"].Password)
	}
	if got.Senders["email"].SMTPHost != "smtp.b.test" {
		t.Errorf("SMTPHost = %q, 其余字段应已更新", got.Senders["email"].SMTPHost)
	}
}

func TestSetMessage_Validates(t *testing.T) {
	s := newMessageSettings(t)
	ctx := context.Background()

	cases := []struct {
		name  string
		confs map[string]config.SenderConf
	}{
		{"未知渠道", map[string]config.SenderConf{
			"telegram": {Enabled: true},
		}},
		{"启用 webhook 但地址为空", map[string]config.SenderConf{
			"webhook": {Enabled: true, URL: "   "},
		}},
		{"webhook 地址不是 http", map[string]config.SenderConf{
			"webhook": {Enabled: true, URL: "ftp://example.com/hook"},
		}},
		{"启用 email 但主机为空", map[string]config.SenderConf{
			"email": {Enabled: true, SMTPHost: "", SMTPPort: 587},
		}},
		{"email 端口越界", map[string]config.SenderConf{
			"email": {Enabled: true, SMTPHost: "smtp.test", SMTPPort: 70000},
		}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := s.SetMessage(ctx, c.confs)
			if err == nil {
				t.Fatal("应当被拒绝，却通过了校验")
			}
			if !errors.Is(err, ErrInvalidSetting) {
				t.Errorf("错误未包装 ErrInvalidSetting: %v", err)
			}
		})
	}
}

func TestSetMessage_DisabledSkipsFieldChecks(t *testing.T) {
	// 关闭的渠道不校验其参数：使用者要能在参数不全时先把渠道关掉
	s := newMessageSettings(t)

	err := s.SetMessage(context.Background(), map[string]config.SenderConf{
		"webhook": {Enabled: false, URL: ""},
		"email":   {Enabled: false, SMTPHost: "", SMTPPort: 0},
	})
	if err != nil {
		t.Fatalf("关闭渠道不应校验参数: %v", err)
	}
}

func TestMessage_NoStore(t *testing.T) {
	s := New(messageStaticCfg(), nil)

	got, err := s.Message(context.Background())
	if err != nil {
		t.Fatalf("Message: %v", err)
	}
	if got.Senders["webhook"].URL != "https://yaml.example.com/hook" {
		t.Errorf("无配置表时应返回 YAML 值，得到 %+v", got.Senders["webhook"])
	}
}
```

- [x] **Step 2: 运行测试确认失败**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/setting/ -run 'Message' -count=1`
Expected: FAIL，编译错误 `s.Message undefined` 与 `s.SetMessage undefined`

- [x] **Step 3: 新建消息配置文件**

创建 `internal/setting/message.go`：

```go
// internal/setting/message.go
// 消息发送器配置：保存即生效的发送渠道参数。
//
// 键名多一层渠道名：message.senders.<渠道>.<字段>。渠道是可枚举的固定集合，
// 不接受任意名字——注册表里没有对应实现的渠道，存进表也无从投递。
//
// 队列容量与工作协程数不进表：它们决定启动时建好的 channel 大小与协程数量，
// 改动需重启才生效。

package setting

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/repo"
)

// SenderWebhook 与 SenderEmail 是允许配置的渠道名。
// stdout 无参数可配且恒为启用，不在此列。
const (
	SenderWebhook = "webhook"
	SenderEmail   = "email"
)

// configurableSenders 限定可配置的渠道，顺序固定以便接口回读稳定。
var configurableSenders = []string{SenderWebhook, SenderEmail}

// senderKey 拼出某渠道某字段在配置表中的键名。
func senderKey(sender, field string) string {
	return "message.senders." + sender + "." + field
}

// 发送器配置的字段名。
const (
	fieldEnabled  = "enabled"
	fieldURL      = "url"
	fieldSMTPHost = "smtp_host"
	fieldSMTPPort = "smtp_port"
	fieldUsername = "username"
	fieldPassword = "password"
	fieldFrom     = "from"
)

// MinSMTPPort 与 MaxSMTPPort 是 SMTP 端口的取值范围。
const (
	MinSMTPPort = 1
	MaxSMTPPort = 65535
)

// Message 返回消息层配置：队列参数来自 YAML，发送器参数叠加配置表。
func (s *Settings) Message(ctx context.Context) (config.MessageConfig, error) {
	out := s.static.Message
	if s.repo == nil {
		return out, nil
	}
	vals, err := s.globalValues(ctx)
	if err != nil {
		return config.MessageConfig{}, err
	}

	// 复制一份 map：直接改 s.static.Message.Senders 会污染 YAML 基准值，
	// 下一次读取就拿不到原始值了
	merged := make(map[string]config.SenderConf, len(out.Senders)+len(configurableSenders))
	for name, conf := range out.Senders {
		merged[name] = conf
	}
	for _, name := range configurableSenders {
		merged[name] = senderConfFrom(merged[name], name, vals)
	}
	out.Senders = merged
	return out, nil
}

// senderConfFrom 以 base 为基准叠加表中存在的字段。
// 脏数据退回基准值，不使整次取值失败：一行坏数据不应让消息层无法投递。
func senderConfFrom(base config.SenderConf, sender string, vals map[string]string) config.SenderConf {
	if raw, ok := vals[senderKey(sender, fieldEnabled)]; ok {
		base.Enabled = parseBool(raw, base.Enabled)
	}
	if raw, ok := vals[senderKey(sender, fieldURL)]; ok {
		base.URL = raw
	}
	if raw, ok := vals[senderKey(sender, fieldSMTPHost)]; ok {
		base.SMTPHost = raw
	}
	if raw, ok := vals[senderKey(sender, fieldSMTPPort)]; ok {
		base.SMTPPort = parseInt(raw, base.SMTPPort)
	}
	if raw, ok := vals[senderKey(sender, fieldUsername)]; ok {
		base.Username = raw
	}
	if raw, ok := vals[senderKey(sender, fieldPassword)]; ok {
		base.Password = raw
	}
	if raw, ok := vals[senderKey(sender, fieldFrom)]; ok {
		base.From = raw
	}
	return base
}

// SetMessage 保存发送器配置。校验不通过即整次拒绝，不做部分写入。
//
// 只写入 confs 中出现的渠道：界面可以只提交被改动的那一个。
// 某渠道的 Password 为空串时保留表内原值——界面回读到的是脱敏值，
// 原样提交会把星号存成真密码。
func (s *Settings) SetMessage(ctx context.Context, confs map[string]config.SenderConf) error {
	for name, conf := range confs {
		if err := validateSenderConf(name, conf); err != nil {
			return err
		}
	}
	if s.repo == nil {
		return ErrNoSettingStore
	}

	current, err := s.Message(ctx)
	if err != nil {
		return err
	}

	var rows []*repo.Setting
	for _, name := range configurableSenders {
		conf, ok := confs[name]
		if !ok {
			continue
		}
		if conf.Password == "" {
			conf.Password = current.Senders[name].Password
		}
		rows = append(rows, senderRows(name, conf)...)
	}
	if len(rows) == 0 {
		return nil
	}
	return s.repo.Upsert(ctx, rows...)
}

// validateSenderConf 校验单个渠道的参数。
// 渠道关闭时只校验渠道名：参数不全也要能先把它关掉。
func validateSenderConf(name string, conf config.SenderConf) error {
	known := false
	for _, n := range configurableSenders {
		if n == name {
			known = true
			break
		}
	}
	if !known {
		return fmt.Errorf("%w: 未知的发送渠道 %q，可配置的为 %v",
			ErrInvalidSetting, name, configurableSenders)
	}
	if !conf.Enabled {
		return nil
	}

	switch name {
	case SenderWebhook:
		addr := strings.TrimSpace(conf.URL)
		if addr == "" {
			return fmt.Errorf("%w: 启用 webhook 需填写推送地址", ErrInvalidSetting)
		}
		u, err := url.Parse(addr)
		if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
			return fmt.Errorf("%w: webhook 地址应是完整的 http 或 https 链接，当前为 %q",
				ErrInvalidSetting, conf.URL)
		}
	case SenderEmail:
		if strings.TrimSpace(conf.SMTPHost) == "" {
			return fmt.Errorf("%w: 启用邮件需填写 SMTP 主机", ErrInvalidSetting)
		}
		if conf.SMTPPort < MinSMTPPort || conf.SMTPPort > MaxSMTPPort {
			return fmt.Errorf("%w: SMTP 端口应在 %d 与 %d 之间，当前为 %d",
				ErrInvalidSetting, MinSMTPPort, MaxSMTPPort, conf.SMTPPort)
		}
	}
	return nil
}

// senderRows 把单个渠道摊平成配置表的行。
// 该渠道的全部字段一次写入，与运行时配置按分类整体保存的做法一致。
func senderRows(name string, conf config.SenderConf) []*repo.Setting {
	kv := []struct {
		field string
		value string
	}{
		{fieldEnabled, strconv.FormatBool(conf.Enabled)},
		{fieldURL, strings.TrimSpace(conf.URL)},
		{fieldSMTPHost, strings.TrimSpace(conf.SMTPHost)},
		{fieldSMTPPort, strconv.Itoa(conf.SMTPPort)},
		{fieldUsername, conf.Username},
		{fieldPassword, conf.Password},
		{fieldFrom, strings.TrimSpace(conf.From)},
	}
	out := make([]*repo.Setting, 0, len(kv))
	for _, it := range kv {
		out = append(out, &repo.Setting{
			Scope: repo.ScopeGlobal,
			Name:  senderKey(name, it.field),
			Value: it.value,
		})
	}
	return out
}
```

`parseBool` 与 `parseInt` 已分别定义在 `internal/setting/settings.go:222` 与 `internal/setting/runtime.go`，同包可直接调用，无需重复定义。写入方法为 `repo.Upsert`，与 `SetRuntime` 一致。

- [x] **Step 4: 运行测试确认通过**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/setting/ -count=1`
Expected: PASS，全部用例通过

- [ ] **Step 5: 提交**（未执行：按项目规范，提交由使用者发起）

```bash
cd /Users/zhangfengda/workspace/groot
git add internal/setting/message.go internal/setting/message_test.go
git commit -m "feat(setting): 发送器配置纳入配置表"
```

**审查后修订（以此为准）：** 键前缀为 `message.senders.<渠道>.<字段>`，与 YAML 的 `yaml:"senders"` 一致。启用邮件时 `From` 必填：`senders/email.go` 把它同时用作 MAIL FROM、RCPT TO 与收件人，空值必然在发送时失败，与空 SMTP 主机同性质。`runtime.go` 包头注释不再把消息发送器列为「需重启」的例子。测试补充：脏数据回退（`smtp_port = "abc"`）、无仓库时 `SetMessage` 返回 `ErrNoSettingStore`、只提交 email 时不写 webhook 行、空 map 不写入；`newMessageSettings` 同时返回仓库供这些用例查表。

### Task 6: 发送器接口与启动装配

**Files:**
- Modify: `internal/api/types/types.go`（新增发送器接口结构）
- Modify: `internal/api/handler/setting.go`（新增两个 handler 与消息层字段）
- Modify: `internal/api/handler/setting_test.go`
- Modify: `internal/api/server.go`（构造参数与 `NewSettingHandler` 装配）
- Modify: `internal/api/router.go:71-72`（两条路由）
- Modify: `cmd/groot/main.go:330-341`（注册改为无条件、参数取自配置对象）

- [x] **Step 1: 写失败的测试**

追加到 `internal/api/handler/setting_test.go` 末尾：

```go
// newMessageHandlerForTest 建一个带真实消息层与配置表的 handler。
// 返回消息层以便断言注册结果。
func newMessageHandlerForTest(t *testing.T) (*SettingHandler, *message.Layer) {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })

	cfg := config.Config{
		Message: config.MessageConfig{
			QueueSize: 16, Workers: 1,
			Senders: map[string]config.SenderConf{
				"webhook": {Enabled: false, URL: ""},
				"email":   {Enabled: false, SMTPHost: "", SMTPPort: 587},
			},
		},
	}
	settings := setting.New(cfg, settingdb.New(sqlxDB, dialect))
	models := llm.NewModelService(modeldb.New(sqlxDB, dialect))
	layer := message.NewLayer(cfg.Message, logger.NewNop())
	h := NewSettingHandler(SettingHandlerDeps{
		Settings: settings,
		Models:   models,
		Registry: agent.NewRegistryForTest(4),
		Messages: layer,
		Log:      logger.NewNop(),
	})
	return h, layer
}

func TestSettingHandler_GetSendersMasksPassword(t *testing.T) {
	h, _ := newMessageHandlerForTest(t)

	body := `{"senders":{"email":{"enabled":true,"smtp_host":"smtp.test","smtp_port":587,` +
		`"username":"u","password":"super-secret","from":"a@test"}}}`
	rc := callJSON(h.PutSenders, consts.MethodPut, body, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("PutSenders status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}

	rc = callJSON(h.GetSenders, consts.MethodGet, "", nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("GetSenders status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if strings.Contains(string(rc.Response.Body()), "super-secret") {
		t.Fatalf("响应泄露了 SMTP 密码: %s", rc.Response.Body())
	}
	var out types.SendersPayload
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	// MaskAPIKey 保留尾 4 位，12 位的 super-secret 脱敏为 ****cret
	if out.Senders["email"].Password != "****cret" {
		t.Errorf("password = %q, want ****cret", out.Senders["email"].Password)
	}
	if out.Senders["email"].SMTPHost != "smtp.test" {
		t.Errorf("smtp_host = %q, want smtp.test", out.Senders["email"].SMTPHost)
	}
}

func TestSettingHandler_PutSendersRegistersIntoLayer(t *testing.T) {
	h, layer := newMessageHandlerForTest(t)

	body := `{"senders":{"webhook":{"enabled":true,"url":"https://hook.test/a"}}}`
	rc := callJSON(h.PutSenders, consts.MethodPut, body, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if !layer.ChannelEnabled("webhook") {
		t.Error("保存后 webhook 应在消息层可用")
	}
}

func TestSettingHandler_PutSendersDisableKeepsChannelUnavailable(t *testing.T) {
	h, layer := newMessageHandlerForTest(t)

	rc := callJSON(h.PutSenders, consts.MethodPut,
		`{"senders":{"webhook":{"enabled":true,"url":"https://hook.test/a"}}}`, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("启用 status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	rc = callJSON(h.PutSenders, consts.MethodPut,
		`{"senders":{"webhook":{"enabled":false,"url":"https://hook.test/a"}}}`, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("关闭 status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if layer.ChannelEnabled("webhook") {
		t.Error("关闭后 webhook 不应可用")
	}
}

func TestSettingHandler_PutSendersRejectsBadConfig(t *testing.T) {
	h, layer := newMessageHandlerForTest(t)

	rc := callJSON(h.PutSenders, consts.MethodPut,
		`{"senders":{"webhook":{"enabled":true,"url":"not-a-url"}}}`, nil)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "invalid_request" {
		t.Errorf("status = %q, want invalid_request", s)
	}
	if layer.ChannelEnabled("webhook") {
		t.Error("校验失败不应改动消息层")
	}
}

func TestSettingHandler_PutSendersEmptyPasswordKeepsStored(t *testing.T) {
	h, _ := newMessageHandlerForTest(t)

	first := `{"senders":{"email":{"enabled":true,"smtp_host":"smtp.a","smtp_port":25,` +
		`"username":"u","password":"please-keep-me","from":"a@test"}}}`
	if rc := callJSON(h.PutSenders, consts.MethodPut, first, nil); rc.Response.StatusCode() != 200 {
		t.Fatalf("首次 status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	second := `{"senders":{"email":{"enabled":true,"smtp_host":"smtp.b","smtp_port":25,` +
		`"username":"u","password":"","from":"a@test"}}}`
	if rc := callJSON(h.PutSenders, consts.MethodPut, second, nil); rc.Response.StatusCode() != 200 {
		t.Fatalf("二次 status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}

	rc := callJSON(h.GetSenders, consts.MethodGet, "", nil)
	var out types.SendersPayload
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	// please-keep-me 脱敏为 ****p-me；若密码被空串覆盖，这里会是空串
	if out.Senders["email"].Password != "****p-me" {
		t.Errorf("password = %q, 空串提交应保留原密码", out.Senders["email"].Password)
	}
	if out.Senders["email"].SMTPHost != "smtp.b" {
		t.Errorf("smtp_host = %q, want smtp.b", out.Senders["email"].SMTPHost)
	}
}

func TestSettingHandler_PutSendersBadJSON(t *testing.T) {
	h, _ := newMessageHandlerForTest(t)

	rc := callJSON(h.PutSenders, consts.MethodPut, `{not json`, nil)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "invalid_request" {
		t.Errorf("status = %q, want invalid_request", s)
	}
}
```

该文件的 import 块需补上 `internal/message`（`internal/ratelimit` 已在 Task 3 中加入）：

```go
	"github.com/zfd81/groot/internal/message"
```

- [x] **Step 2: 运行测试确认失败**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/api/handler/ -run 'Senders' -count=1`
Expected: FAIL，编译错误 `h.PutSenders undefined`、`types.SendersPayload undefined`、`layer.ChannelEnabled undefined`

- [x] **Step 3: 消息层补一个查询方法**

`internal/message/layer.go`，加在 `resolve` 之后：

```go
// ChannelEnabled 报告某渠道当前是否可投递：已注册且已启用。
func (l *Layer) ChannelEnabled(name string) bool {
	_, ok := l.resolve(name)
	return ok
}
```

- [x] **Step 4: 接口结构**

`internal/api/types/types.go` 末尾追加：

```go
// SendersPayload 是 /web/settings/senders 的请求与响应体。
// 读写同构，但 password 字段方向不同：响应中是脱敏值，
// 请求中空串表示不改密码。
type SendersPayload struct {
	Senders map[string]SenderSettings `json:"senders"`
}

// SenderSettings 单个发送渠道的参数。
// 字段是两个渠道的并集：webhook 只用 url，email 用其余几项。
type SenderSettings struct {
	Enabled  bool   `json:"enabled"`
	URL      string `json:"url"`
	SMTPHost string `json:"smtp_host"`
	SMTPPort int    `json:"smtp_port"`
	Username string `json:"username"`
	Password string `json:"password"`
	From     string `json:"from"`
}
```

- [x] **Step 5: handler 收下消息层并实现两个接口**

`internal/api/handler/setting.go`。依赖结构体加一个字段：

```go
	// Messages 用于在保存后把发送器注册进运行中的消息层。
	Messages *message.Layer
```

`SettingHandler` 结构体同步加 `messages *message.Layer`，`NewSettingHandler` 里加 `messages: deps.Messages`。

import 补 `"github.com/zfd81/groot/internal/message"`。

文件末尾追加两个 handler 与转换函数：

```go
// GetSenders 处理 GET /web/settings/senders。
// SMTP 密码以脱敏形式返回：界面需要知道密码已设置，但不该拿到原文。
func (h *SettingHandler) GetSenders(ctx context.Context, rc *app.RequestContext) {
	m, err := h.settings.Message(ctx)
	if err != nil {
		h.log.Error("读取发送器配置失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
		return
	}
	out := types.SendersPayload{Senders: map[string]types.SenderSettings{}}
	for _, name := range []string{setting.SenderWebhook, setting.SenderEmail} {
		c := m.Senders[name]
		out.Senders[name] = types.SenderSettings{
			Enabled:  c.Enabled,
			URL:      c.URL,
			SMTPHost: c.SMTPHost,
			SMTPPort: c.SMTPPort,
			Username: c.Username,
			Password: llm.MaskAPIKey(c.Password),
			From:     c.From,
		}
	}
	rc.JSON(200, out)
}

// PutSenders 处理 PUT /web/settings/senders。
//
// 写表在前、注册进消息层在后：写表失败时直接返回，
// 不会留下「消息层已改、表里还是旧值」的状态。
// 注册后新入队的消息即走新参数，正在投递的消息按旧参数发完。
func (h *SettingHandler) PutSenders(ctx context.Context, rc *app.RequestContext) {
	var req types.SendersPayload
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求参数错误"})
		return
	}

	confs := make(map[string]config.SenderConf, len(req.Senders))
	for name, s := range req.Senders {
		confs[name] = config.SenderConf{
			Enabled:  s.Enabled,
			URL:      strings.TrimSpace(s.URL),
			SMTPHost: strings.TrimSpace(s.SMTPHost),
			SMTPPort: s.SMTPPort,
			Username: s.Username,
			Password: s.Password,
			From:     strings.TrimSpace(s.From),
		}
	}

	if err := h.settings.SetMessage(ctx, confs); err != nil {
		if errors.Is(err, setting.ErrInvalidSetting) {
			rc.JSON(400, utils.H{"status": "invalid_request", "message": err.Error()})
			return
		}
		h.log.Error("保存发送器配置失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
		return
	}

	// 从配置表回读：请求体里的空密码已在 SetMessage 中补成原值，
	// 注册用的必须是补齐后的参数，否则换成空密码会让邮件发送失败
	saved, err := h.settings.Message(ctx)
	if err != nil {
		h.log.Error("回读发送器配置失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
		return
	}
	h.applySenders(saved)
	h.GetSenders(ctx, rc)
}

// applySenders 把配置表中的发送器参数注册进消息层。
// 每个可配置渠道都注册，启用与否交给 SenderConf.Enabled 判定——
// 注销再注册会让一次配置改动产生两次 map 写入，中间态下渠道短暂不可用。
func (h *SettingHandler) applySenders(m config.MessageConfig) {
	if h.messages == nil {
		return
	}
	w := m.Senders[setting.SenderWebhook]
	h.messages.SetSender(setting.SenderWebhook, senders.NewWebhook(w.URL), w)
	e := m.Senders[setting.SenderEmail]
	h.messages.SetSender(setting.SenderEmail,
		senders.NewEmail(e.SMTPHost, e.SMTPPort, e.Username, e.Password, e.From), e)
}
```

import 再补三项：

```go
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/message/senders"
```

`llm` 与 `strings` 已在该文件的 import 中。

- [x] **Step 6: 路由与 server 装配**

`internal/api/server.go`。`NewServer` 的参数列表加上消息层：

```go
	msgLayer *message.Layer,
```

`NewSettingHandler` 的调用处补一项：

```go
		Messages: msgLayer,
```

路由在 `internal/api/router.go`，紧随 [router.go:71-72](internal/api/router.go#L71-L72) 的 runtime 两条之后加两条：

```go
	webGroup.GET("/settings/senders", settingH.GetSenders)
	webGroup.PUT("/settings/senders", settingH.PutSenders)
```

`cmd/groot/main.go` 里 `api.NewServer(...)` 的调用处按新参数位置传入 `msgLayer`。

- [x] **Step 7: 启动改为无条件注册**

`cmd/groot/main.go:330-341`。整段替换为：

```go
	// Initialize message layer
	// 队列容量与协程数来自 YAML（改动需重启）；发送器参数来自配置对象，
	// 保存即生效。两个可配置渠道无条件注册，是否投递由 Enabled 决定——
	// 启动时按 enabled 决定注册与否的话，在界面上打开渠道就得重启。
	msgCfg, err := settings.Message(context.Background())
	if err != nil {
		log.Error("读取发送器配置失败，改用 YAML 值", zap.Error(err))
		msgCfg = cfg.Message
	}
	msgLayer := message.NewLayer(msgCfg, log)
	webhookConf := msgCfg.Senders["webhook"]
	msgLayer.Register("webhook", senders.NewWebhook(webhookConf.URL), webhookConf)
	emailConf := msgCfg.Senders["email"]
	msgLayer.Register("email", senders.NewEmail(emailConf.SMTPHost, emailConf.SMTPPort,
		emailConf.Username, emailConf.Password, emailConf.From), emailConf)
	msgLayer.Register("stdout", senders.NewStdout(), config.SenderConf{Enabled: true})
	msgLayer.Start()
	log.Info("消息层已启动")
```

- [x] **Step 8: 运行测试确认通过**

Run: `cd /Users/zhangfengda/workspace/groot && go build ./... && go test ./internal/api/handler/ ./internal/message/ -race -count=1`
Expected: 构建无输出，两个包 PASS

- [ ] **Step 9: 提交**（未执行：按项目规范，提交由使用者发起）

```bash
cd /Users/zhangfengda/workspace/groot
git add internal/api internal/message cmd/groot/main.go
git commit -m "feat(api): 发送器配置支持接口读写与在线生效"
```

**审查后修订（以此为准）：** 抽出 `sendersToPayload(m config.MessageConfig) types.SendersPayload` 供 `GetSenders` 与 `PutSenders` 共用；`PutSenders` 用已回读的 `saved` 直接构造响应，不再调用 `GetSenders` 二次读表——保存已成功后再读表失败会返回误导性的 500。`applySenders` 注释说明只更新本节点。`PutSenders` 对 `Username` 一并 `TrimSpace`，密码不 trim。`SendersPayload` 注释注明密码无法经接口清空。新增 `TestSettingHandler_PutSendersKeepsOtherSenderApplied`：只提交 webhook 后，email 在消息层仍保持已启用，证明注册用的是表值而非请求体。

## 阶段 C：调度开关下移判断点

### Task 7: 调度工具始终注册，按开关过滤

`cfg.Schedule.Enabled` 当前在 [main.go:403](cmd/groot/main.go#L403) 决定「是否调用 `RegisterBuiltinTools`」。改成启动时无条件注册，把「是否可见」的判断挪到 `GetTools` 每次取用时，开关即变成读一次配置表的策略判断。

判断点下移后，`Manager` 需要一个「取值函数」而不是一个布尔值：布尔值在注册时就被定死，取值函数则每次调用时才求值。用函数而非 `*Settings` 是为了让 `internal/mcp` 不依赖 `internal/setting`，避免反向依赖。

**Files:**
- Modify: `internal/mcp/manager.go`
- Modify: `cmd/groot/main.go`
- Test: `internal/mcp/manager_builtin_test.go`（新建）

- [x] **Step 1: 写失败的测试**

创建 `internal/mcp/manager_builtin_test.go`：

```go
// Package mcp 内置工具门控测试。
package mcp

import (
	"context"
	"testing"

	"github.com/cloudwego/eino/components/tool"
	"github.com/cloudwego/eino/schema"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
)

// fakeTool 只实现 Info 与 InvokableRun，供注册与取用测试使用。
type fakeTool struct{ name string }

func (f *fakeTool) Info(_ context.Context) (*schema.ToolInfo, error) {
	return &schema.ToolInfo{Name: f.name, Desc: "测试工具"}, nil
}

func (f *fakeTool) InvokableRun(_ context.Context, _ string, _ ...tool.Option) (string, error) {
	return "", nil
}

// newGateTestManager 建一个只输出 error 级别的 Manager。
// 名字带 Gate 前缀：既有 manager_test.go 已定义了无参的 newTestManager。
func newGateTestManager(t *testing.T) *Manager {
	t.Helper()
	return NewManager(logger.New(config.LoggingConfig{
		Level: "error", Format: "console", Output: []string{"stdout"},
	}))
}

// countBuiltin 数 GetTools 结果里名为 name 的工具数量。
func countBuiltin(tools []tool.BaseTool, name string) int {
	n := 0
	for _, tl := range tools {
		info, err := tl.Info(context.Background())
		if err == nil && info != nil && info.Name == name {
			n++
		}
	}
	return n
}

// TestGetTools_GateBlocksBuiltin 验证门控关闭时内置工具不出现在 GetTools 结果里，
// 但仍留在注册表中：开关重新打开后无需再注册即可恢复。
func TestGetTools_GateBlocksBuiltin(t *testing.T) {
	m := newGateTestManager(t)
	allow := false
	m.SetBuiltinGate("schedule", func() bool { return allow })
	m.RegisterBuiltinTools(map[string]tool.BaseTool{
		"schedule_create": &fakeTool{name: "schedule_create"},
	})

	if n := countBuiltin(m.GetTools(), "schedule_create"); n != 0 {
		t.Fatalf("门控关闭时 GetTools 返回了 %d 个 schedule_create，want 0", n)
	}

	allow = true
	if n := countBuiltin(m.GetTools(), "schedule_create"); n != 1 {
		t.Fatalf("门控打开后 GetTools 返回了 %d 个 schedule_create，want 1", n)
	}
}

// TestGetTools_NoGateAllowsBuiltin 验证未设门控的内置工具一律可见，
// 保证既有内置工具不受本次改动影响。
func TestGetTools_NoGateAllowsBuiltin(t *testing.T) {
	m := newGateTestManager(t)
	m.RegisterBuiltinTools(map[string]tool.BaseTool{
		"other_tool": &fakeTool{name: "other_tool"},
	})
	if n := countBuiltin(m.GetTools(), "other_tool"); n != 1 {
		t.Fatalf("无门控工具返回 %d 个，want 1", n)
	}
}

// TestListTools_GateBlocksBuiltin 验证门控关闭时工具清单接口也不列出该工具，
// 使界面看到的与模型看到的一致。
func TestListTools_GateBlocksBuiltin(t *testing.T) {
	m := newGateTestManager(t)
	m.SetBuiltinGate("schedule", func() bool { return false })
	m.RegisterBuiltinTools(map[string]tool.BaseTool{
		"schedule_create": &fakeTool{name: "schedule_create"},
	})
	for _, info := range m.ListTools() {
		if info.Name == "schedule_create" {
			t.Fatal("门控关闭时 ListTools 仍列出了 schedule_create")
		}
	}
	if got := m.ToolCount(); got != 0 {
		t.Errorf("ToolCount() = %d, want 0", got)
	}
}
```

- [x] **Step 2: 运行测试确认失败**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/mcp/ -run 'TestGetTools|TestListTools' -count=1`
Expected: FAIL，编译错误 `m.SetBuiltinGate undefined (type *Manager has no field or method SetBuiltinGate)`

- [x] **Step 3: Manager 加门控字段与设置方法**

`internal/mcp/manager.go`，在 `Manager` 结构体的 `toolInfos` 之后加一行字段：

```go
	builtinGates map[string]func() bool        // 内置工具组的可见性门控，每次取用时求值
```

`NewManager` 的返回字面量里同步初始化：

```go
		builtinGates: make(map[string]func() bool),
```

在 `GetTools` 之前插入门控的设置与求值两个方法：

```go
// SetBuiltinGate 给一组内置工具挂上可见性门控。group 与 RegisterBuiltinTools
// 写入 ToolInfo.MCP 的值一致（调度工具为 "schedule"）。
//
// 传函数而非布尔值，是为了把「是否可用」的求值推迟到每次取工具时：
// 布尔值在挂载那一刻就被定死，改配置要重启才生效；函数则每次读到当前值，
// 于是配置表一改，下一次对话就按新值走。
//
// 未挂门控的组一律可见，因此既有内置工具不受影响。
func (m *Manager) SetBuiltinGate(group string, allow func() bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.builtinGates[group] = allow
}

// builtinAllowed 判断某个工具名当前是否可见。调用方需持有读锁。
// 门控求值放在锁内：门控函数只读配置表，不会回调 Manager，不存在重入。
func (m *Manager) builtinAllowed(name string) bool {
	info, ok := m.toolInfos[name]
	if !ok {
		return true
	}
	gate, ok := m.builtinGates[info.MCP]
	if !ok {
		return true
	}
	return gate()
}
```

- [x] **Step 4: 三处取用点接入门控**

同文件，`GetTools` 的内置工具循环加一行判断：

```go
	for name, t := range m.builtinTools {
		if !m.builtinAllowed(name) {
			continue
		}
		result = append(result, t)
	}
```

`ListTools` 改为逐项过滤，使界面与模型看到同一份清单：

```go
// ListTools returns all registered tools metadata.
// 被门控挡住的内置工具不出现在结果里：清单是给使用者看的，
// 列出一个模型拿不到的工具只会造成误解。
func (m *Manager) ListTools() []*ToolInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*ToolInfo, 0, len(m.toolInfos))
	for name, tool := range m.toolInfos {
		if !m.builtinAllowed(name) {
			continue
		}
		result = append(result, tool)
	}
	return result
}
```

`ToolCount` 与 `ListTools` 保持一致，否则界面上的数量与列表长度对不上：

```go
// ToolCount returns the number of tools currently available（与 ListTools 同口径）
func (m *Manager) ToolCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for name := range m.toolInfos {
		if m.builtinAllowed(name) {
			n++
		}
	}
	return n
}
```

`GetTool` 也要挡住，避免按名取到一个当前不可用的工具：

```go
// GetTool retrieves a tool by name。被门控挡住的内置工具按「不存在」返回。
func (m *Manager) GetTool(name string) (*ToolInfo, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	tool, ok := m.toolInfos[name]
	if !ok || !m.builtinAllowed(name) {
		return nil, false
	}
	return tool, true
}
```

- [x] **Step 5: 运行测试确认通过**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/mcp/ -race -count=1`
Expected: PASS

- [x] **Step 6: 启动处改为无条件注册加挂门控**

`cmd/groot/main.go`，把 [main.go:402-408](cmd/groot/main.go#L402-L408) 那段 `if cfg.Schedule.Enabled { ... }` 整体替换为：

```go
		// 调度工具一律注册，可见性交给门控每次求值：
		// 开关是「是否允许在对话中创建定时任务」的策略判断，改完下一次对话即生效，
		// 不必重启。注册与否若跟着开关走，就又变成了启动期决定。
		scheduleMgr = schedule.NewManager(scheduleStorage, scheduleEngine, scheduleRunner, log)
		mcpMgr.SetBuiltinGate("schedule", func() bool {
			sc, err := settings.Schedule(context.Background())
			if err != nil {
				// 配置表读不到时按 YAML 值走，不让一次查询失败关掉整组工具
				log.Warn("读取调度开关失败,按静态配置处理", zap.Error(err))
				return cfg.Schedule.Enabled
			}
			return sc.Enabled
		})
		scheduleTools := schedule.NewScheduleTools(scheduleMgr)
		mcpMgr.RegisterBuiltinTools(scheduleTools)
		log.Info("调度工具已注册", zap.Int("count", len(scheduleTools)))
```

这段位于 Leader 分支内，`settings`、`cfg`、`log`、`mcpMgr`、`scheduleMgr` 均已在作用域中。`context` 与 `zap` 两个包在该文件已导入。

- [x] **Step 7: 运行测试确认通过**

Run: `cd /Users/zhangfengda/workspace/groot && go build ./... && go vet ./internal/mcp/ ./cmd/...`
Expected: 两条命令均无输出

- [ ] **Step 8: 提交**（未执行：按项目规范，提交由使用者发起）

```bash
cd /Users/zhangfengda/workspace/groot
git add internal/mcp cmd/groot/main.go
git commit -m "refactor(mcp): 内置工具可见性改为取用时求值"
```

**审查后修订（以此为准，覆盖上文 Step 3、4 的对应片段）：**

1. 门控每组每次方法调用只求值一次，且在锁外求值：新增 `hiddenGroups()` 在读锁内快照门控函数、锁外求值、返回被挡住的组名集合；四个取用点先取 `hidden` 再进读锁，用 `builtinHidden(name, hidden)` 过滤。`builtinAllowed` 删除。门控读配置表，逐工具求值会让一次 `GetTools` 变成 8 次库读且占着锁。
2. 门控只作用于 `builtinTools` 成员：`builtinHidden` 先判断工具是否为内置工具，MCP 工具不受同名门控影响，避免清单与模型口径不一致。
3. 导出 `mcp.BuiltinGroupSchedule = "schedule"`，`RegisterBuiltinTools`、`main.go` 与测试统一使用。
4. 测试补充：`GetTool` 尊重门控；同名 MCP 的工具不被挡；一次 `GetTools` 门控只求值一次。

### Task 8: 调度开关进配置表与接口

门控已就位，这一任务把 `schedule.enabled` 变成可写项：配置表存值、接口读写、界面开关。

**Files:**
- Modify: `internal/setting/runtime.go`
- Modify: `internal/api/types/types.go`
- Modify: `internal/api/handler/setting.go`
- Test: `internal/setting/runtime_test.go`
- Test: `internal/api/handler/setting_test.go`

- [x] **Step 1: 写失败的测试**

先给 `internal/setting/runtime_test.go` 的 `staticCfg` 补一个 Schedule 基准值，在 `Attachment` 字段之后加入：

```go
		Schedule: config.ScheduleConfig{
			Enabled: true, MaxConcurrentTasks: 5, SyncInterval: "30s",
		},
```

YAML 基准取 `true`，这样「写 false 后回读仍是 false」才说明值真落进了表，而不是恰好撞上 YAML 值。

在同文件末尾追加：

```go
// TestRuntime_ScheduleEnabledRoundTrip 验证调度开关经配置表往返后保持，
// 尤其是关闭：布尔零值必须能写进表，不能被当成「未设置」而回落到 YAML 的 true。
func TestRuntime_ScheduleEnabledRoundTrip(t *testing.T) {
	s := New(staticCfg(), newFakeRepo())
	ctx := context.Background()

	cur, err := s.Runtime(ctx)
	if err != nil {
		t.Fatalf("Runtime: %v", err)
	}
	if !cur.Schedule.Enabled {
		t.Fatalf("表为空时应回落到 YAML 的 true，得到 %v", cur.Schedule.Enabled)
	}

	cur.Schedule.Enabled = false
	if err := s.SetRuntime(ctx, cur); err != nil {
		t.Fatalf("SetRuntime 关闭: %v", err)
	}
	got, err := s.Runtime(ctx)
	if err != nil {
		t.Fatalf("Runtime 回读: %v", err)
	}
	if got.Schedule.Enabled {
		t.Error("schedule.enabled 应已关闭：false 必须能写进表")
	}

	got.Schedule.Enabled = true
	if err := s.SetRuntime(ctx, got); err != nil {
		t.Fatalf("SetRuntime 打开: %v", err)
	}
	again, err := s.Runtime(ctx)
	if err != nil {
		t.Fatalf("Runtime 二次回读: %v", err)
	}
	if !again.Schedule.Enabled {
		t.Error("schedule.enabled 应已打开")
	}
}
```

在 `internal/api/handler/setting_test.go` 末尾追加：

```go
// TestSettingHandler_PutRuntimeScheduleEnabled 验证调度开关经接口往返后保持。
func TestSettingHandler_PutRuntimeScheduleEnabled(t *testing.T) {
	h := newRuntimeHandlerForTest(t)

	rc := callJSON(h.GetRuntime, consts.MethodGet, "", nil)
	var cur types.RuntimeSettingsPayload
	if err := json.Unmarshal(rc.Response.Body(), &cur); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	cur.Schedule.Enabled = true
	body, err := json.Marshal(cur)
	if err != nil {
		t.Fatalf("序列化请求: %v", err)
	}

	rc = callJSON(h.PutRuntime, consts.MethodPut, string(body), nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("PutRuntime status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}

	rc = callJSON(h.GetRuntime, consts.MethodGet, "", nil)
	var out types.RuntimeSettingsPayload
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("解析回读响应: %v", err)
	}
	if !out.Schedule.Enabled {
		t.Error("schedule.enabled 回读应为 true")
	}
}
```

- [x] **Step 2: 运行测试确认失败**

Run: `cd /Users/zhangfengda/workspace/groot && go test ./internal/setting/ ./internal/api/handler/ -run 'ScheduleEnabled' -count=1`
Expected: FAIL，编译错误 `cur.Schedule undefined`

- [x] **Step 3: 配置表层加键、分类、编解码**

`internal/setting/runtime.go`。键常量块（`KeyAttachmentAllowedTypes` 所在的 `const` 组）末尾加一行：

```go
	KeyScheduleEnabled = "schedule.enabled"
```

`RuntimeSettings` 结构体加一个字段：

```go
	Schedule config.ScheduleConfig
```

在 `RuntimeSettings` 的文档注释末尾追加：

```go
// Schedule 只有 Enabled 进配置表。MaxConcurrentTasks 与 SyncInterval 是调度器
// 的构造参数，改动需重启，恒为 YAML 值。Enabled 的生效载体是 mcp.Manager 上的
// 内置工具门控：它每次取工具时读一次这里的值，因此写表即生效。
```

`Validate` 无需为它加校验：布尔值没有越界一说。

在 `attachmentFrom` 之后加解码函数：

```go
// scheduleFrom 只叠加 Enabled；其余两个字段保持 base（即 YAML）值。
func scheduleFrom(base config.ScheduleConfig, vals map[string]string) config.ScheduleConfig {
	if raw, ok := vals[KeyScheduleEnabled]; ok {
		base.Enabled = parseBool(raw, base.Enabled)
	}
	return base
}
```

`rows()` 的 `kv` 切片里，`KeyAttachmentAllowedTypes` 那行之后加一行：

```go
		{KeyScheduleEnabled, strconv.FormatBool(r.Schedule.Enabled)},
```

`strconv.FormatBool(false)` 写出 `"false"` 字符串，不是空值，所以关闭状态能真正落表。

`internal/setting/settings.go`。`Runtime` 方法的 `base` 字面量加一行：

```go
		Schedule:   s.static.Schedule,
```

返回字面量加一行：

```go
		Schedule:   scheduleFrom(base.Schedule, vals),
```

`RuntimeStatic` 加一行：

```go
		Schedule:   s.static.Schedule,
```

`Schedule` 方法整体替换，让门控读到的就是配置表值：

```go
// Schedule 返回定时任务配置。Enabled 叠加配置表，其余两项来自 YAML。
// 内置工具门控每次取工具时调用它，这是一次按作用域的范围查询，
// 与 Executor 每次执行开始时读 Runtime 的代价相同。
func (s *Settings) Schedule(ctx context.Context) (config.ScheduleConfig, error) {
	if s.repo == nil {
		return s.static.Schedule, nil
	}
	vals, err := s.globalValues(ctx)
	if err != nil {
		return config.ScheduleConfig{}, err
	}
	return scheduleFrom(s.static.Schedule, vals), nil
}
```

- [x] **Step 4: 接口结构加分区**

`internal/api/types/types.go`。`RuntimeSettingsPayload` 加一个字段：

```go
	Schedule   ScheduleSettings   `json:"schedule"`
```

紧随 `AttachmentSettings` 之后追加类型：

```go
// ScheduleSettings 定时任务相关的可配置项。
// 只有开关：并发数与同步间隔是调度器构造参数，改动需重启，不在此列。
type ScheduleSettings struct {
	Enabled bool `json:"enabled"` // 是否允许在对话中创建定时任务
}
```

- [x] **Step 5: handler 两个转换函数带上分区**

`internal/api/handler/setting.go`。`runtimeToPayload` 的返回字面量加一段：

```go
		Schedule: types.ScheduleSettings{Enabled: r.Schedule.Enabled},
```

`payloadToRuntime` 加一行赋值。该函数在 Task 3 中已改为接收 `base`，未做 Task 3 时它没有 `base` 参数，此时 `out` 是零值起步——两种形态下都是在 `out.Attachment.AllowedTypes = ...` 之后加：

```go
	out.Schedule.Enabled = p.Schedule.Enabled
```

`PutRuntime` 不需要额外改动：门控是读配置表的函数，写表成功即生效，无需像限流器与信号量那样通知一个实例。

- [x] **Step 6: 运行测试确认通过**

Run: `cd /Users/zhangfengda/workspace/groot && go build ./... && go test ./internal/setting/ ./internal/api/handler/ ./internal/mcp/ -race -count=1`
Expected: 构建无输出，三个包 PASS

- [ ] **Step 7: 提交**（未执行：按项目规范，提交由使用者发起）

```bash
cd /Users/zhangfengda/workspace/groot
git add internal/setting internal/api/types/types.go internal/api/handler/setting.go internal/api/handler/setting_test.go
git commit -m "feat(setting): 调度开关纳入配置表并暴露到接口"
```

**审查后修订（承 Task 3 的同类问题）：** `RuntimeSettingsPayload.Schedule` 同样改为指针 `*ScheduleSettings`。`{enabled:false}` 是合法值，不带该分区的客户端会被解码成 `false` 并静默关掉调度工具。`PutRuntime` 对 `nil` 返回 400 `invalid_request`「缺少 schedule 分区」；`runtimeToPayload` 总是填充指针；`payloadToRuntime` 解引用。新增 `TestSettingHandler_PutRuntimeRejectsMissingSchedule`，既有 PUT 用例请求体补上 `schedule` 分区。

**审查后修订（二）：** `main.go` 门控闭包读表用 2 秒超时的 context，DB 挂起时超时走 YAML 回退，不让取工具无限等待。`RuntimeSettingsPayload` 类型注释写明指针规则：分区字段为指针当且仅当其全零值是合法配置。`Settings` 类型注释补充 `Schedule()` 位于每轮对话的取工具路径上、每轮一次范围查询。测试 helper 重构为 `runtimeSections()`/`runtimeBodyWithout(section)`/`runtimeBodyWith(sectionFragment)`/`joinSections`，缺分区用例从表中删一项再拼接；脏值与空表用例补上 Schedule 断言。

## 阶段 D：前端

前端没有单元测试框架（项目规范禁止引入 Jest 等），验证手段是类型检查与构建：`cd web && npx vue-tsc --noEmit && npm run build`。

### Task 9: 运行时配置接口层加限流与调度分区

**Files:**
- Modify: `web/src/api/runtime.ts`

- [x] **Step 1: 加类型**

在 `AttachmentSettings` 接口之后追加：

```ts
export interface RateLimitSettings {
  enabled: boolean
  global_qps: number // 0 表示不限制
  global_concurrency: number // 0 表示不限制
  default_qps: number // 每个调用方，0 表示不限制
  default_concurrency: number // 每个调用方，0 表示不限制
}

export interface ScheduleSettings {
  enabled: boolean // 是否允许在对话中创建定时任务
}
```

`RuntimeSettings` 接口加两个字段：

```ts
export interface RuntimeSettings {
  memory: MemorySettings
  react: ReactSettings
  subagent: SubAgentSettings
  attachment: AttachmentSettings
  rate_limit: RateLimitSettings
  schedule: ScheduleSettings
}
```

- [x] **Step 2: 加取值边界**

`runtimeLimits` 里 `attachmentCount` 那行之后加两行，与 `internal/setting/runtime.go` 的 `MaxRateLimitQPS`、`MaxRateLimitConcurrency` 一致：

```ts
  rateLimitQPS: { min: 0, max: 100000 },
  rateLimitConcurrency: { min: 0, max: 100000 },
```

- [x] **Step 3: 默认值与克隆**

`defaultRuntimeSettings` 返回值里 `attachment` 之后加：

```ts
    rate_limit: {
      enabled: false,
      global_qps: 0,
      global_concurrency: 0,
      default_qps: 10,
      default_concurrency: 5,
    },
    schedule: { enabled: false },
```

`cloneRuntimeSettings` 返回值里 `attachment` 之后加：

```ts
    rate_limit: {
      enabled: s.rate_limit.enabled,
      global_qps: s.rate_limit.global_qps,
      global_concurrency: s.rate_limit.global_concurrency,
      default_qps: s.rate_limit.default_qps,
      default_concurrency: s.rate_limit.default_concurrency,
    },
    schedule: { enabled: s.schedule.enabled },
```

- [x] **Step 4: 类型检查**

Run: `cd /Users/zhangfengda/workspace/groot/web && npx vue-tsc --noEmit`
Expected: 无输出。`SettingsModal.vue` 只通过 `defaultRuntimeSettings()` 与 `cloneRuntimeSettings()` 构造该类型，不含字面量，因此新增字段不会让既有代码报错。

- [ ] **Step 5: 提交**（未执行：按项目规范，提交由使用者发起）

```bash
cd /Users/zhangfengda/workspace/groot
git add web/src/api/runtime.ts
git commit -m "feat(web): 运行时配置类型加限流与调度分区"
```

### Task 10: 发送器接口层与 store

**Files:**
- Create: `web/src/api/senders.ts`
- Create: `web/src/stores/senders.ts`

密码字段两个方向语义不同：回读是脱敏值，提交时空串表示不改。界面上的可编辑副本必须把密码清空再显示，否则使用者不动密码直接保存，脱敏串会被当成新密码存进去。

- [x] **Step 1: 接口层**

创建 `web/src/api/senders.ts`：

```ts
// web/src/api/senders.ts
// 消息发送器配置：webhook 与 email 两个渠道的参数，保存即生效。
//
// 密码字段的读写方向不同：服务端回读的是脱敏值（形如 ****cret），
// 提交时空串表示「保持不变」。界面上的可编辑副本要先把密码清空，
// 见 editableSenders。
import { api } from './client'

export type SenderName = 'webhook' | 'email'

export interface SenderSettings {
  enabled: boolean
  url: string // webhook 专用
  smtp_host: string // 以下为 email 专用
  smtp_port: number
  username: string
  password: string
  from: string
}

export interface SendersSettings {
  senders: Record<SenderName, SenderSettings>
}

// 取值边界，与 internal/setting/message.go 的 MinSMTPPort / MaxSMTPPort 一致
export const senderLimits = {
  smtpPort: { min: 1, max: 65535 },
} as const

function emptySender(): SenderSettings {
  return {
    enabled: false,
    url: '',
    smtp_host: '',
    smtp_port: 587,
    username: '',
    password: '',
    from: '',
  }
}

// defaultSendersSettings 仅用于首屏占位，真实值由接口返回后覆盖。
export function defaultSendersSettings(): SendersSettings {
  return { senders: { webhook: emptySender(), email: emptySender() } }
}

// cloneSendersSettings 按字段深拷贝。不用 structuredClone：
// store 里的值是 Vue 响应式 Proxy，结构化克隆会抛 DataCloneError。
export function cloneSendersSettings(s: SendersSettings): SendersSettings {
  const copy = (x: SenderSettings): SenderSettings => ({
    enabled: x.enabled,
    url: x.url,
    smtp_host: x.smtp_host,
    smtp_port: x.smtp_port,
    username: x.username,
    password: x.password,
    from: x.from,
  })
  return { senders: { webhook: copy(s.senders.webhook), email: copy(s.senders.email) } }
}

// editableSenders 生成给界面编辑的副本：密码清空。
// 服务端回读的密码是脱敏串，原样提交会被存成真密码；
// 清空后不改密码直接保存即提交空串，服务端据此保留原值。
export function editableSenders(s: SendersSettings): SendersSettings {
  const out = cloneSendersSettings(s)
  out.senders.webhook.password = ''
  out.senders.email.password = ''
  return out
}

export const sendersApi = {
  getSettings: () => api.get<SendersSettings>('/web/settings/senders'),

  // 保存成功后服务端回传生效值（密码已脱敏），直接用它覆盖本地状态
  saveSettings: (s: SendersSettings) => api.put<SendersSettings>('/web/settings/senders', s),
}
```

- [x] **Step 2: store**

创建 `web/src/stores/senders.ts`：

```ts
import { defineStore } from 'pinia'
import { ref } from 'vue'
import { sendersApi, defaultSendersSettings, type SendersSettings } from '../api/senders'

// 发送器配置：只服务于设置面板，与运行时配置同构地单独建 store。
// settings 里保存的是服务端回传值，其中密码为脱敏串。
export const useSendersStore = defineStore('senders', () => {
  const settings = ref<SendersSettings>(defaultSendersSettings())
  const loaded = ref(false)

  async function load() {
    if (loaded.value) return
    await reload()
  }

  // reload 强制回源。保存失败后用它把界面恢复成服务端的真实值。
  async function reload() {
    settings.value = await sendersApi.getSettings()
    loaded.value = true
  }

  // save 写入服务端，并用服务端回传的生效值覆盖本地状态
  async function save(next: SendersSettings) {
    settings.value = await sendersApi.saveSettings(next)
    loaded.value = true
  }

  return { settings, loaded, load, reload, save }
})
```

- [x] **Step 3: 类型检查**

Run: `cd /Users/zhangfengda/workspace/groot/web && npx vue-tsc --noEmit`
Expected: 无输出

- [ ] **Step 4: 提交**（未执行：按项目规范，提交由使用者发起）

```bash
cd /Users/zhangfengda/workspace/groot
git add web/src/api/senders.ts web/src/stores/senders.ts
git commit -m "feat(web): 发送器配置接口层与 store"
```

### Task 11: 设置面板加四个分组

**Files:**
- Modify: `web/src/components/settings/SettingsModal.vue`（import 块、脚本、`config` 分区模板、`watch` 钩子）

四个分组都放在「配置」分区：限流、定时任务随运行时配置一起走 `saveRuntime`；Webhook、邮件走各自的 `saveSenders`。

- [x] **Step 1: import**

在 `import { useRuntimeStore } from '../../stores/runtime'` 之后加：

```ts
import { senderLimits, editableSenders, type SendersSettings } from '../../api/senders'
import { useSendersStore } from '../../stores/senders'
```

- [x] **Step 2: 脚本**

在 `allowedTypesText` 的 `computed` 定义之后、`async function saveVoice()` 之前插入：

```ts
// ---- 发送器配置 ----
const sendersStore = useSendersStore()
// 本地副本用 editableSenders 生成：密码清空，不动密码直接保存即提交空串
const senders = ref<SendersSettings>(editableSenders(sendersStore.settings))
const sendersSaving = ref(false)
// 服务端回读的密码非空即表示已设置；输入框用它决定提示文案
const smtpPasswordSet = computed(() => sendersStore.settings.senders.email.password !== '')

async function loadSenders() {
  try {
    await sendersStore.reload()
    senders.value = editableSenders(sendersStore.settings)
  } catch (e: any) {
    ElMessage.error(t('settings.sendersLoadFailed', { msg: e?.message || '' }))
  }
}

// saveSenders 整体保存两个渠道。启用了渠道但参数不全由服务端拒绝，
// 失败即回源，避免界面上留下一个并未生效的值。
async function saveSenders() {
  sendersSaving.value = true
  try {
    await sendersStore.save(senders.value)
    senders.value = editableSenders(sendersStore.settings)
    ElMessage.success(t('settings.sendersSaved'))
  } catch (e: any) {
    ElMessage.error(t('settings.sendersSaveFailed', { msg: e?.message || '' }))
    await loadSenders()
  } finally {
    sendersSaving.value = false
  }
}
```

`watch(() => props.show, ...)` 里 `void loadRuntime()` 之后加一行：

```ts
      void loadSenders()
```

- [x] **Step 3: 模板**

在「配置」分区的附件分组末尾——`allowedTypesText` 输入框所在 `.row` 关闭后紧跟的 `.config-group` 收尾 `</div>`——之后、`config-panel` 的收尾 `</div>` 之前插入四个分组。地址与开关的排列遵循语音分组的做法：开关依赖的字段排在开关之前，让前置条件在界面上直接可见。

```vue
          <div class="config-group">
            <div class="group-title">{{ t('settings.configRateLimit') }}</div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.rateLimitEnabled') }}</div>
                <div class="label-desc">{{ t('settings.rateLimitEnabledDesc') }}</div>
              </div>
              <el-switch v-model="runtime.rate_limit.enabled" :loading="runtimeSaving" @change="saveRuntime" />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.rateLimitDefaultQps') }}</div>
                <div class="label-desc">{{ t('settings.rateLimitDefaultQpsDesc') }}</div>
              </div>
              <el-input-number
                v-model="runtime.rate_limit.default_qps"
                :min="runtimeLimits.rateLimitQPS.min"
                :max="runtimeLimits.rateLimitQPS.max"
                :precision="1"
                controls-position="right"
                style="width: 140px"
                @change="saveRuntime"
              />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.rateLimitDefaultConcurrency') }}</div>
                <div class="label-desc">{{ t('settings.rateLimitDefaultConcurrencyDesc') }}</div>
              </div>
              <el-input-number
                v-model="runtime.rate_limit.default_concurrency"
                :min="runtimeLimits.rateLimitConcurrency.min"
                :max="runtimeLimits.rateLimitConcurrency.max"
                controls-position="right"
                style="width: 140px"
                @change="saveRuntime"
              />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.rateLimitGlobalQps') }}</div>
                <div class="label-desc">{{ t('settings.rateLimitGlobalQpsDesc') }}</div>
              </div>
              <el-input-number
                v-model="runtime.rate_limit.global_qps"
                :min="runtimeLimits.rateLimitQPS.min"
                :max="runtimeLimits.rateLimitQPS.max"
                :precision="1"
                controls-position="right"
                style="width: 140px"
                @change="saveRuntime"
              />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.rateLimitGlobalConcurrency') }}</div>
                <div class="label-desc">{{ t('settings.rateLimitGlobalConcurrencyDesc') }}</div>
              </div>
              <el-input-number
                v-model="runtime.rate_limit.global_concurrency"
                :min="runtimeLimits.rateLimitConcurrency.min"
                :max="runtimeLimits.rateLimitConcurrency.max"
                controls-position="right"
                style="width: 140px"
                @change="saveRuntime"
              />
            </div>
          </div>

          <div class="config-group">
            <div class="group-title">{{ t('settings.configSchedule') }}</div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.scheduleEnabled') }}</div>
                <div class="label-desc">{{ t('settings.scheduleEnabledDesc') }}</div>
              </div>
              <el-switch v-model="runtime.schedule.enabled" :loading="runtimeSaving" @change="saveRuntime" />
            </div>
          </div>

          <div class="config-group">
            <div class="group-title">{{ t('settings.configWebhook') }}</div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.webhookUrl') }}</div>
                <div class="label-desc">{{ t('settings.webhookUrlDesc') }}</div>
              </div>
              <el-input
                v-model="senders.senders.webhook.url"
                style="width: 320px"
                placeholder="https://"
                @change="saveSenders"
              />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.webhookEnabled') }}</div>
                <div class="label-desc">{{ t('settings.webhookEnabledDesc') }}</div>
              </div>
              <el-switch v-model="senders.senders.webhook.enabled" :loading="sendersSaving" @change="saveSenders" />
            </div>
          </div>

          <div class="config-group">
            <div class="group-title">{{ t('settings.configEmail') }}</div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.smtpHost') }}</div>
                <div class="label-desc">{{ t('settings.smtpHostDesc') }}</div>
              </div>
              <el-input v-model="senders.senders.email.smtp_host" style="width: 220px" @change="saveSenders" />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.smtpPort') }}</div>
                <div class="label-desc">{{ t('settings.smtpPortDesc') }}</div>
              </div>
              <el-input-number
                v-model="senders.senders.email.smtp_port"
                :min="senderLimits.smtpPort.min"
                :max="senderLimits.smtpPort.max"
                controls-position="right"
                style="width: 140px"
                @change="saveSenders"
              />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.smtpUsername') }}</div>
                <div class="label-desc">{{ t('settings.smtpUsernameDesc') }}</div>
              </div>
              <el-input v-model="senders.senders.email.username" style="width: 220px" @change="saveSenders" />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.smtpPassword') }}</div>
                <div class="label-desc">{{ t('settings.smtpPasswordDesc') }}</div>
              </div>
              <el-input
                v-model="senders.senders.email.password"
                type="password"
                show-password
                style="width: 220px"
                :placeholder="smtpPasswordSet ? t('settings.smtpPasswordKeepHint') : t('settings.smtpPasswordUnsetHint')"
                @change="saveSenders"
              />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.smtpFrom') }}</div>
                <div class="label-desc">{{ t('settings.smtpFromDesc') }}</div>
              </div>
              <el-input v-model="senders.senders.email.from" style="width: 220px" @change="saveSenders" />
            </div>
            <div class="row">
              <div class="row-label">
                <div class="label-title">{{ t('settings.emailEnabled') }}</div>
                <div class="label-desc">{{ t('settings.emailEnabledDesc') }}</div>
              </div>
              <el-switch v-model="senders.senders.email.enabled" :loading="sendersSaving" @change="saveSenders" />
            </div>
          </div>
```

- [x] **Step 4: 类型检查**

Run: `cd /Users/zhangfengda/workspace/groot/web && npx vue-tsc --noEmit`
Expected: 无输出。此时 i18n 键尚未加入，但 `t()` 接受任意字符串，类型检查不会拦下缺失的键；缺失的键在 Task 12 补齐。

- [ ] **Step 5: 提交**（未执行：按项目规范，提交由使用者发起）

```bash
cd /Users/zhangfengda/workspace/groot
git add web/src/components/settings/SettingsModal.vue
git commit -m "feat(web): 设置面板加限流、定时任务、通知渠道分组"
```

**审查后修订（以此为准）：** `saveRuntime` 与 `saveSenders` 用脏标记循环串行化：保存进行中再触发只记脏，由正在跑的那次收尾时再发一轮，只在最后一轮成功后覆盖本地副本。blur 触发的 `change` 紧接 input-number 按钮点击会并发两个 PUT，先发后到的响应会用服务端回传值冲掉使用者后来的改动。SMTP 密码输入框加 `autocomplete="new-password"`，避免浏览器自动填入其他站点凭据并因即改即存落库。失败提示文案统一带冒号。

### Task 12: 两语言文案

**Files:**
- Modify: `web/src/i18n/messages/zh-cn.ts`
- Modify: `web/src/i18n/messages/en.ts`

两个文件的 `settings` 块里都有 `attachAllowedTypesPlaceholder` 这一行，新键插在它之后、`runtimeSaved` 之前。英文文件头部注明键必须与中文一一对应，两边加同样的键名。

- [x] **Step 1: 中文**

`web/src/i18n/messages/zh-cn.ts`，在 `attachAllowedTypesPlaceholder: '.png, .jpg, .pdf',` 之后插入：

```ts
    configRateLimit: '限流',
    rateLimitEnabled: '启用限流',
    rateLimitEnabledDesc: '按调用方限制接口的请求速率与并发；已建立的调用方保留原上限，新调用方按新值',
    rateLimitDefaultQps: '默认 QPS',
    rateLimitDefaultQpsDesc: '每个调用方每秒允许的请求数，0 表示不限制',
    rateLimitDefaultConcurrency: '默认并发',
    rateLimitDefaultConcurrencyDesc: '每个调用方同时进行的对话数上限，0 表示不限制',
    rateLimitGlobalQps: '全局 QPS',
    rateLimitGlobalQpsDesc: '所有调用方合计的每秒请求数上限，0 表示不限制',
    rateLimitGlobalConcurrency: '全局并发',
    rateLimitGlobalConcurrencyDesc: '所有调用方合计的同时对话数上限，0 表示不限制',
    configSchedule: '定时任务',
    scheduleEnabled: '允许创建定时任务',
    scheduleEnabledDesc: '开启后模型可在对话中创建与管理定时任务，保存后下一次对话生效',
    configWebhook: 'Webhook 通知',
    webhookUrl: '推送地址',
    webhookUrlDesc: '接收通知的 HTTP 地址，启用前必须填写',
    webhookEnabled: '启用 Webhook',
    webhookEnabledDesc: '定时任务结果等事件将推送到上述地址',
    configEmail: '邮件通知',
    smtpHost: 'SMTP 主机',
    smtpHostDesc: '邮件服务器地址，启用前必须填写',
    smtpPort: 'SMTP 端口',
    smtpPortDesc: '常用 25、465、587',
    smtpUsername: '用户名',
    smtpUsernameDesc: '登录邮件服务器的账号',
    smtpPassword: '密码',
    smtpPasswordDesc: '登录邮件服务器的密码，只在需要更换时填写',
    smtpPasswordKeepHint: '已设置，留空保持不变',
    smtpPasswordUnsetHint: '尚未设置',
    smtpFrom: '发件人',
    smtpFromDesc: '通知邮件的发件地址',
    emailEnabled: '启用邮件',
    emailEnabledDesc: '定时任务结果等事件将发送到配置的邮箱',
    sendersSaved: '通知配置已保存',
    sendersSaveFailed: '保存失败 {msg}',
    sendersLoadFailed: '通知配置读取失败 {msg}',
```

- [x] **Step 2: 英文**

`web/src/i18n/messages/en.ts`，在 `attachAllowedTypesPlaceholder: '.png, .jpg, .pdf',` 之后插入：

```ts
    configRateLimit: 'Rate limiting',
    rateLimitEnabled: 'Enable rate limiting',
    rateLimitEnabledDesc: 'Limit request rate and concurrency per caller; existing callers keep their limits, new callers get the new values',
    rateLimitDefaultQps: 'Default QPS',
    rateLimitDefaultQpsDesc: 'Requests per second allowed for each caller, 0 for unlimited',
    rateLimitDefaultConcurrency: 'Default concurrency',
    rateLimitDefaultConcurrencyDesc: 'Simultaneous chats allowed for each caller, 0 for unlimited',
    rateLimitGlobalQps: 'Global QPS',
    rateLimitGlobalQpsDesc: 'Requests per second across all callers, 0 for unlimited',
    rateLimitGlobalConcurrency: 'Global concurrency',
    rateLimitGlobalConcurrencyDesc: 'Simultaneous chats across all callers, 0 for unlimited',
    configSchedule: 'Scheduled tasks',
    scheduleEnabled: 'Allow scheduled tasks',
    scheduleEnabledDesc: 'Let the model create and manage scheduled tasks in chat; applies from the next chat after saving',
    configWebhook: 'Webhook notifications',
    webhookUrl: 'Endpoint URL',
    webhookUrlDesc: 'HTTP address that receives notifications, required before enabling',
    webhookEnabled: 'Enable webhook',
    webhookEnabledDesc: 'Events such as scheduled task results are posted to the address above',
    configEmail: 'Email notifications',
    smtpHost: 'SMTP host',
    smtpHostDesc: 'Mail server address, required before enabling',
    smtpPort: 'SMTP port',
    smtpPortDesc: 'Commonly 25, 465 or 587',
    smtpUsername: 'Username',
    smtpUsernameDesc: 'Account used to sign in to the mail server',
    smtpPassword: 'Password',
    smtpPasswordDesc: 'Password for the mail server, fill in only to change it',
    smtpPasswordKeepHint: 'Set, leave empty to keep',
    smtpPasswordUnsetHint: 'Not set',
    smtpFrom: 'From address',
    smtpFromDesc: 'Sender address on notification emails',
    emailEnabled: 'Enable email',
    emailEnabledDesc: 'Events such as scheduled task results are sent to the configured mailbox',
    sendersSaved: 'Notification settings saved',
    sendersSaveFailed: 'Save failed {msg}',
    sendersLoadFailed: 'Failed to load notification settings {msg}',
```

- [x] **Step 3: 类型检查与构建**

Run: `cd /Users/zhangfengda/workspace/groot/web && npx vue-tsc --noEmit && npm run build`
Expected: 类型检查无输出，构建以 `✓ built in` 结尾

- [ ] **Step 4: 提交**（未执行：按项目规范，提交由使用者发起）

```bash
cd /Users/zhangfengda/workspace/groot
git add web/src/i18n/messages/zh-cn.ts web/src/i18n/messages/en.ts
git commit -m "feat(web): 限流、定时任务、通知渠道文案"
```

## 阶段 E：文档

### Task 13: 设计文档与测试索引

**Files:**
- Create: `docs/superpowers/specs/2026-09-25-runtime-config-migration-design.md`
- Modify: `tests/TEST_CASES.md`（在 `## 二、系统测试（Python）` 之前插入 `### 1.6`）

设计文档遵循项目规范：功能设计部分只作正面陈述，不出现「相比」「原来」「新增了」「改为」等对比措辞；全部对比集中在迭代说明章节。

- [x] **Step 1: 写设计文档**

创建 `docs/superpowers/specs/2026-09-25-runtime-config-migration-design.md`：

````markdown
# 限流、消息发送器与调度开关的运行时配置设计文档

## 一、功能设计

### 1.1 功能概述

接口限流参数、消息发送渠道参数、定时任务开关三项配置由设置面板维护，保存后即刻生效，无需重启服务。

三项配置各自有一个运行中的持有对象：限流参数的持有对象是限流器，发送渠道参数的持有对象是消息层，定时任务开关的持有对象是内置工具门控。配置表是持久来源，持有对象是生效载体，一次保存同时更新两者。

生效语义统一：已发起的调用按保存前的参数走完，保存后新发起的调用按新参数。正在进行的请求不被中途打断，避免一个已放行的请求在归还名额时对不上账，或一次已开始的网络投递结果无处归属。

解决的问题：

- 限流阈值需要根据实际流量调整，每次调整都重启会中断正在进行的对话。
- Webhook 地址与 SMTP 参数属于部署环境信息，由运维人员在界面上填写比编辑 YAML 更直接。
- 是否允许模型创建定时任务是一项策略判断，应当能随时切换。

### 1.2 能力清单

| 能力 | 说明 |
|---|---|
| 限流参数在线调整 | 开关、默认 QPS、默认并发、全局 QPS、全局并发五项，保存后对新调用方生效 |
| 发送渠道在线配置 | Webhook 地址与开关，SMTP 主机、端口、账号、密码、发件人与开关，保存后对新入队的消息生效 |
| 密码脱敏回读 | SMTP 密码回读时只显示尾四位，提交空串表示保持不变 |
| 定时任务开关 | 决定模型在对话中是否能看到调度工具，保存后下一次对话生效 |
| 参数校验 | 越界或不完整的参数整次拒绝，不做部分写入 |
| 关闭时免校验 | 关闭的渠道不校验其参数，使用者可在参数不全时先把渠道关掉 |

### 1.3 配置项

#### 1.3.1 限流

| 键 | 类型 | 取值范围 | 含义 |
|---|---|---|---|
| `security.rate_limit.enabled` | bool | | 是否启用限流 |
| `security.rate_limit.global_qps` | float | 0 ~ 100000 | 全部调用方合计的每秒请求数上限，0 表示不限制 |
| `security.rate_limit.global_concurrency` | int | 0 ~ 100000 | 全部调用方合计的同时对话数上限，0 表示不限制 |
| `security.rate_limit.default_qps` | float | 0 ~ 100000 | 每个调用方的每秒请求数上限，0 表示不限制 |
| `security.rate_limit.default_concurrency` | int | 0 ~ 100000 | 每个调用方的同时对话数上限，0 表示不限制 |

空闲桶的回收周期 `cleanup_interval` 是后台协程的定时参数，恒为 YAML 值。

QPS 是浮点数，校验与写表都按浮点处理；写表采用最短往返表示，`100` 写成 `100` 而非 `100.000000`。

#### 1.3.2 发送渠道

键名带一层渠道名：`message.senders.<渠道>.<字段>`。可配置的渠道是 `webhook` 与 `email`，标准输出渠道无参数可配且恒为启用。

| 字段 | 适用渠道 | 含义 |
|---|---|---|
| `enabled` | 两者 | 是否投递到该渠道 |
| `url` | webhook | 推送地址，须为完整的 http 或 https 链接 |
| `smtp_host` | email | SMTP 主机 |
| `smtp_port` | email | SMTP 端口，1 ~ 65535 |
| `username` | email | SMTP 账号 |
| `password` | email | SMTP 密码 |
| `from` | email | 发件人地址，启用时必填 |

队列容量与工作协程数是消息层构造参数，恒为 YAML 值。

#### 1.3.3 定时任务

| 键 | 类型 | 含义 |
|---|---|---|
| `schedule.enabled` | bool | 是否允许模型在对话中创建与管理定时任务 |

调度器的并发数与目录同步间隔是构造参数，恒为 YAML 值。

### 1.4 生效机制

#### 1.4.1 限流器

限流器持有一份配置与一个全局桶，按调用方各建一个桶。全部读取路径先在读锁下取一次配置与全局桶的快照，再据此判定，一次判定内不会看到前后不一致的配置。

保存新参数时替换配置与全局桶。已建的调用方桶保留原容量，直到空闲超过回收窗口被清掉；随后首次出现的调用方按新参数建桶。归还名额按调用方名查当前桶，与取名额落在同一个桶上，收紧上限不会让已放行的请求对不上账。

全局桶只有一个实例。`GlobalQPS` 或 `GlobalConcurrency` 变化时才重建，未变则保留桶及其令牌状态。重建瞬间正在占用全局名额的请求，其归还落到新桶上，新桶因此出现「持有者多于令牌」的欠账；名额的归还一律非阻塞，欠账不会挂住任何请求，并在新桶的通道下一次排空时被吸收。

限流器是每个节点各一份的实例，保存后的替换只作用于处理该请求的节点。多节点共享同一数据库时，其他节点在下一次启动时从配置表取到新值。

#### 1.4.2 消息层

消息层的发送器注册表由读写锁保护。投递一条消息时先在锁内解析出目标渠道的发送器实例，再在锁外调用它发送——发送是网络操作，持锁会把整个消息层卡住。

保存新参数时按渠道替换发送器实例并更新其启用状态。已解析出实例、正在投递的消息按旧实例发完；随后开始处理的消息走新实例，包括保存时已在队列中等待的消息。两个可配置渠道在启动时无条件注册，投递与否由启用状态决定，界面上打开渠道无需重启。

消息层是每个节点各一份的实例，保存后的替换只作用于处理该请求的节点。多节点共享同一数据库时，其他节点在下一次启动时从配置表取到新值。

SMTP 密码一旦设置，无法经接口清空：提交空串表示保持不变。需要停用凭据时换新密码或关闭渠道。

#### 1.4.3 内置工具门控

内置工具按组挂门控，门控是一个返回布尔值的函数。每次取工具时每个组的门控求值一次，求值在管理器的锁之外进行，门控读配置表不会占着锁。被挡住的工具不出现在交给模型的工具列表里，也不出现在界面的工具清单与计数中，两处口径一致。门控只作用于内置工具，外部 MCP 服务的工具不受影响，即便其名称与某个组名相同。

调度工具在服务成为 Leader 时无条件注册，挂上读取 `schedule.enabled` 的门控。开关是每次取工具时读一次配置表，保存后下一次对话即按新值。这次读取带 2 秒超时；配置表读取失败或超时时门控按 YAML 值判定，一次查询失败不会关掉整组工具，也不会让取工具无限等待。

### 1.5 接口

| 路由 | 说明 |
|---|---|
| `GET /web/settings/runtime` | 读取运行时配置，含 `rate_limit` 与 `schedule` 分区 |
| `PUT /web/settings/runtime` | 整体保存，成功后同步限流器 |
| `GET /web/settings/senders` | 读取两个渠道的参数，密码脱敏 |
| `PUT /web/settings/senders` | 保存提交的渠道，成功后注册进消息层 |

运行时配置读写同构。发送渠道接口的 `password` 字段方向不同：响应中是脱敏值，请求中空串表示保持不变。

写表在前、通知持有对象在后。写表失败直接返回，不留下「持有对象已改、表内仍是旧值」的状态。

### 1.6 设置面板

四个分组位于「配置」分区，在附件之后：限流、定时任务、Webhook 通知、邮件通知。每组一项一行，标题下附说明。

限流与定时任务随运行时配置整体保存。Webhook 与邮件各自整体保存。开关依赖的字段排在开关上方：推送地址在 Webhook 开关上方，SMTP 各项在邮件开关上方，使前置条件在界面上直接可见。

SMTP 密码输入框不回显已存密码。密码已设置时占位文字提示「已设置，留空保持不变」，未设置时提示「尚未设置」。

### 1.7 改动清单

| 文件 | 说明 |
|---|---|
| `internal/ratelimit/limiter.go` | 配置快照、在线重建、按调用方桶的空闲时间加锁 |
| `internal/message/layer.go` | 注册表加锁、替换与注销发送器、渠道可用性查询 |
| `internal/mcp/manager.go` | 内置工具组门控，取工具、列清单、计数、按名取一律经门控 |
| `internal/setting/runtime.go` | 限流五键与调度开关的键、边界、校验、编解码 |
| `internal/setting/message.go` | 发送渠道的键、校验、编解码，空密码保留原值 |
| `internal/setting/settings.go` | `Security`、`Message`、`Schedule` 叠加配置表 |
| `internal/api/handler/setting.go` | 命名参数构造、写表后同步持有对象、发送渠道两个接口 |
| `internal/api/types/types.go` | 限流、调度、发送渠道的接口结构 |
| `internal/api/router.go` | 发送渠道两条路由 |
| `internal/api/server.go` | 把限流器与消息层交给设置 handler |
| `cmd/groot/main.go` | 启动时应用表内限流值，发送渠道无条件注册，调度工具挂门控 |
| `web/src/api/runtime.ts` | 限流与调度分区的类型、默认值、克隆、取值边界 |
| `web/src/api/senders.ts` | 发送渠道的类型、克隆、可编辑副本、接口 |
| `web/src/stores/senders.ts` | 发送渠道状态 |
| `web/src/components/settings/SettingsModal.vue` | 四个分组 |
| `web/src/i18n/messages/*` | 两语言文案 |

## 二、迭代说明

### 2.1 与上一版差异

- 新增：限流五项、发送渠道参数、调度开关进配置表，保存即生效。此前这三项在启动时读取 YAML 并固化进限流器、消息层与调度工具注册判断，改动需重启。
- 新增：`RateLimiter.Reconfigure` 与 `Config`，限流器支持在线替换参数。
- 新增：`Layer.SetSender`、`RemoveSender`、`ChannelEnabled`，消息层支持在线替换与注销发送器。`Register` 保留为启动路径的别名。
- 新增：`Manager.SetBuiltinGate`，内置工具按组挂可见性门控；`GetTools`、`ListTools`、`ToolCount`、`GetTool` 一律经门控。
- 新增：`GET`、`PUT /web/settings/senders` 两条路由。
- 调整：`NewSettingHandler` 从五个位置参数改为 `SettingHandlerDeps` 命名参数。
- 调整：`payloadToRuntime` 增加 `base` 参数，为请求体不携带的 `CleanupInterval` 提供底值。
- 调整：`Settings.Security`、`Message`、`Schedule` 从纯 YAML 透传改为叠加配置表。
- 调整：启动时发送渠道从「按 enabled 决定是否注册」改为无条件注册；调度工具从「按 `cfg.Schedule.Enabled` 决定是否注册」改为无条件注册加门控。
- 修复：限流器的 `cfg` 字段与调用方桶的 `lastUsed` 此前无锁读写，`-race` 可报数据竞争；消息层的两个注册表 map 同样如此。本次一并加锁。
- 保持不变：`security.rate_limit.cleanup_interval`、`message.queue_size`、`message.workers`、`schedule.max_concurrent_tasks`、`schedule.sync_interval` 留在 YAML。

### 2.2 后续独立迭代

限流的全局桶在替换瞬间存在名额归还错位，偏差不超过瞬时并发数。若日后需要严格计数，可把全局桶也改为按取得时的实例归还，做法同子 Agent 并发上限的信号量替换。

## 三、测试

### 3.1 单元测试（Go）

| 测试对象 | 覆盖点 |
|---|---|
| `internal/ratelimit` | 重建后新调用方按新上限、已建桶保留旧容量、开关在线关停与恢复、全局桶重建、重建与请求并发无竞争 |
| `internal/message` | 替换发送器后新消息走新实例、注销后不投递、禁用渠道不投递且重新启用无需重注册、替换与投递并发无竞争 |
| `internal/mcp` | 门控关闭时取工具与列清单均不含该组、未挂门控的组一律可见、计数与清单同口径 |
| `internal/setting` | 限流五项边界、浮点 QPS 解析与写出、脏数据回落、`cleanup_interval` 不写表；发送渠道往返、空密码保留原值、未知渠道与不完整参数拒绝、关闭渠道免校验、无仓库时读 YAML；调度开关 `false` 能写进表 |
| `internal/api/handler` | 限流保存后限流器即刻生效、越界不动限流器、回读含限流分区；发送渠道密码脱敏且不含原文、保存后消息层可用、关闭后不可用、校验失败不动消息层、空密码保留、非法 JSON；调度开关经接口往返 |

运行：`go test ./internal/ratelimit/ ./internal/message/ ./internal/mcp/ ./internal/setting/ ./internal/api/handler/ -race -v`

### 3.2 系统测试（Python）

`tests/python/` 下可补充：保存限流参数后以高于阈值的速率请求收到 429、关闭限流后恢复、发送渠道接口回读密码脱敏。由使用者自行运行。
````

- [x] **Step 2: 更新测试索引**

`tests/TEST_CASES.md`，在 `## 二、系统测试（Python）` 那一行之前插入：

```markdown
### 1.6 运行时配置迁移测试

位于 `internal/ratelimit/limiter_test.go`、`internal/message/layer_test.go`、`internal/mcp/manager_builtin_test.go`、
`internal/setting/runtime_test.go`、`internal/setting/message_test.go` 与 `internal/api/handler/setting_test.go`。

覆盖点：

- 限流器：重建后新调用方按新上限、已建桶保留旧容量（取与还落在同一桶）、开关在线关停与恢复、全局桶重建、重建与请求并发（-race）
- 消息层：替换发送器后新消息走新实例且旧实例不再收到、注销后无可用渠道、禁用渠道不投递且重新启用无需重注册、替换与投递并发（-race）
- 内置工具门控：门控关闭时 GetTools 与 ListTools 均不含该组、ToolCount 同口径、未挂门控的组一律可见
- 配置对象（限流）：五项边界表驱动（0 表示不限制、负数与超上限拒绝）、表内缺键保持 YAML、脏数据回落、rows 含五键且不含 cleanup_interval
- 配置对象（发送渠道）：表为空回落 YAML、往返一致且队列参数不受影响、空密码保留原值、未知渠道 / 启用但地址空 / 非 http 地址 / 启用但主机空 / 端口越界表驱动拒绝、关闭渠道免校验、无仓库时读 YAML
- 配置对象（调度）：开关往返，尤其 false 能写进表而非回落 YAML 的 true
- 设置 handler：限流保存后限流器 Config 即刻更新且 CleanupInterval 保持、越界 400 且限流器不变、回读含限流分区；发送渠道回读脱敏且响应体不含原文、保存后 ChannelEnabled 为真、关闭后为假、校验失败 400 且消息层不变、空密码保留、非法 JSON 400；调度开关经接口往返

---

```

- [ ] **Step 3: 提交**（未执行：按项目规范，提交由使用者发起）

```bash
cd /Users/zhangfengda/workspace/groot
git add docs/superpowers/specs/2026-09-25-runtime-config-migration-design.md tests/TEST_CASES.md
git commit -m "docs: 限流、发送渠道、调度开关的运行时配置设计文档与测试索引"
```

---

## 收尾验证

全部任务完成后，在仓库根目录依次执行：

```bash
cd /Users/zhangfengda/workspace/groot
gofmt -l internal/ratelimit internal/message internal/mcp internal/setting internal/api cmd
go build ./...
go vet ./internal/... ./cmd/...
go test ./internal/... -race -count=1
cd web && npx vue-tsc --noEmit && npm run build
```

Expected：`gofmt -l` 无输出；`go build` 与 `go vet` 无输出；`go test` 每个包一行 `ok`，无 `FAIL` 与 `DATA RACE`；类型检查无输出，构建以 `✓ built in` 结尾。

手工验证（需启动服务）：

1. 设置面板「配置」分区把默认 QPS 改为 1，保存；用一个新 API Key 连发两次请求，第二次收到 429。
2. 关闭限流开关，保存；同一 Key 连发请求均 200。
3. 填写 Webhook 地址并启用，保存；创建一个通知渠道含 webhook 的定时任务，到点后地址收到 POST。
4. 关闭 Webhook，保存；再次触发，地址不再收到请求，服务无需重启。
5. 打开定时任务开关，保存；新开对话，让模型列出可用工具，能看到 `schedule_create`；关闭开关后新开对话，看不到。
6. 填写 SMTP 密码保存后刷新面板，密码框为空且占位提示「已设置，留空保持不变」；不动密码改发件人再保存，邮件仍能用原密码发出。
