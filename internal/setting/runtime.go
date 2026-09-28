// internal/setting/runtime.go
// 运行时配置：保存即生效的配置分类。
//
// 取值优先级为「配置表、代码默认值」两层：表内只保存使用者明确改过的项，
// 未设置的项由代码默认值兜底。个别不进表的字段（限流回收周期、调度器构造参数）
// 恒取 bootstrap.yaml 的值。
//
// 能放进这里的前提是「读取点位于请求路径上」：读取点在启动路径上的配置项
// （如服务端口、日志、数据库连接、调度器参数）搬进配置表后
// 仍需重启才生效，因此不在此列。

package setting

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/repo"
)

// 运行时配置在配置表中的键名。点号分层，镜像配置的层级路径。
// security 下 rate_limit 的五项阈值进表，auth 的键见 auth.go。
const (
	KeyMemoryHistoryWindow = "memory.history_window"

	KeyReactMaxIterations = "react.max_iterations"
	KeyReactStepTimeout   = "react.step_timeout"
	KeyReactErrorRetry    = "react.error_retry"

	KeySubAgentMaxConcurrency  = "subagent.max_concurrency"
	KeySubAgentExecTimeout     = "subagent.exec_timeout"
	KeySubAgentMaxTaskLength   = "subagent.max_task_length"
	KeySubAgentMaxResultLength = "subagent.max_result_length"

	KeyAttachmentMaxSize      = "attachment.max_size"
	KeyAttachmentMaxTotalSize = "attachment.max_total_size"
	KeyAttachmentMaxCount     = "attachment.max_count"
	KeyAttachmentAllowedTypes = "attachment.allowed_types"

	KeyRateLimitEnabled            = "security.rate_limit.enabled"
	KeyRateLimitGlobalQPS          = "security.rate_limit.global_qps"
	KeyRateLimitGlobalConcurrency  = "security.rate_limit.global_concurrency"
	KeyRateLimitDefaultQPS         = "security.rate_limit.default_qps"
	KeyRateLimitDefaultConcurrency = "security.rate_limit.default_concurrency"

	KeyScheduleEnabled = "schedule.enabled"
)

// 各配置项的取值边界。写入前校验，避免把会让 Agent 无法工作的值存进表里
// （迭代上限为 0 会使每次对话立刻终止，超时为 0 会使每步 LLM 调用即刻取消）。
const (
	// MinHistoryWindow 为 -1，表示不限制历史轮次
	MinHistoryWindow = -1
	MaxHistoryWindow = 500

	MinReactMaxIterations = 1
	MaxReactMaxIterations = 200
	MinReactStepTimeout   = 1
	MaxReactStepTimeout   = 3600
	MinReactErrorRetry    = 0
	MaxReactErrorRetry    = 10

	MinSubAgentMaxConcurrency = 1
	MaxSubAgentMaxConcurrency = 100
	MinSubAgentExecTimeout    = time.Second
	MaxSubAgentExecTimeout    = 24 * time.Hour
	MinSubAgentTextLength     = 1
	MaxSubAgentTextLength     = 200000

	MinAttachmentSize      = 1
	MaxAttachmentSize      = 1024
	MinAttachmentCount     = 1
	MaxAttachmentCount     = 100
	MaxAttachmentTypeCount = 50

	// QPS 与并发上限为 0 表示该维度不限制，因此下界是 0 而非 1
	MaxRateLimitQPS         = 100000
	MaxRateLimitConcurrency = 100000
)

// RuntimeSettings 汇总全部保存即生效的配置分类。
//
// 分类结构体复用 internal/config 中已有的类型，不重复定义，
// 使调用方从 Settings 取到的值与配置表解码出来的是同一种类型。
//
// SubAgent.MaxConcurrency 的生效语义与其余项不同：它是全局 semaphore 的容量，
// 保存后只对随后新发起的子 Agent 调用生效，正在执行与正在排队的调用按旧上限
// 走完。写入配置表之外还需调用 agent.SubAgentRegistry.SetMaxConcurrency
// 替换信号量，详见该方法的注释。
//
// RateLimit 的生效方式同样需要通知持有方：写表之外还需调用
// ratelimit.RateLimiter.Reconfigure 替换限流参数。其 CleanupInterval
// 不进配置表，恒为 bootstrap.yaml 值——它是后台回收协程的周期，改动需重启。
//
// Schedule 只有 Enabled 进配置表。MaxConcurrentTasks 与 SyncInterval 是调度器
// 的构造参数，改动需重启，恒为 bootstrap.yaml 值。Enabled 的生效载体是
// mcp.Manager 上的内置工具门控：它每次取工具时读一次这里的值，因此写表即生效。
type RuntimeSettings struct {
	Memory     config.MemoryConfig
	React      config.ReactConfig
	SubAgent   config.SubAgentConfig
	Attachment config.AttachmentConfig
	RateLimit  config.RateLimitConfig
	Schedule   config.ScheduleConfig
}

// ErrInvalidSetting 配置值越界。包装它的错误由 handler 映射为 400。
var ErrInvalidSetting = errors.New("setting: 配置值不合法")

// Validate 校验全部字段是否在允许范围内。
// 越界即拒绝整次保存，不做静默截断：使用者在界面上看到的值应当就是生效的值。
func (r RuntimeSettings) Validate() error {
	if err := checkRange("memory.history_window", r.Memory.HistoryWindow,
		MinHistoryWindow, MaxHistoryWindow); err != nil {
		return err
	}

	if err := checkRange("react.max_iterations", r.React.MaxIterations,
		MinReactMaxIterations, MaxReactMaxIterations); err != nil {
		return err
	}
	if err := checkRange("react.step_timeout", r.React.StepTimeout,
		MinReactStepTimeout, MaxReactStepTimeout); err != nil {
		return err
	}
	if err := checkRange("react.error_retry", r.React.ErrorRetry,
		MinReactErrorRetry, MaxReactErrorRetry); err != nil {
		return err
	}

	if err := checkRange("subagent.max_concurrency", r.SubAgent.MaxConcurrency,
		MinSubAgentMaxConcurrency, MaxSubAgentMaxConcurrency); err != nil {
		return err
	}

	d, err := time.ParseDuration(r.SubAgent.ExecTimeout)
	if err != nil {
		return fmt.Errorf("%w: subagent.exec_timeout 不是合法的时长，应形如 5m、30s：%q",
			ErrInvalidSetting, r.SubAgent.ExecTimeout)
	}
	if d < MinSubAgentExecTimeout || d > MaxSubAgentExecTimeout {
		return fmt.Errorf("%w: subagent.exec_timeout 应在 %s 与 %s 之间，当前为 %s",
			ErrInvalidSetting, MinSubAgentExecTimeout, MaxSubAgentExecTimeout, d)
	}
	if err := checkRange("subagent.max_task_length", r.SubAgent.MaxTaskLength,
		MinSubAgentTextLength, MaxSubAgentTextLength); err != nil {
		return err
	}
	if err := checkRange("subagent.max_result_length", r.SubAgent.MaxResultLength,
		MinSubAgentTextLength, MaxSubAgentTextLength); err != nil {
		return err
	}

	if err := checkRange("attachment.max_size", r.Attachment.MaxSize,
		MinAttachmentSize, MaxAttachmentSize); err != nil {
		return err
	}
	if err := checkRange("attachment.max_total_size", r.Attachment.MaxTotalSize,
		MinAttachmentSize, MaxAttachmentSize*4); err != nil {
		return err
	}
	// 总量小于单个上限时，单文件校验永远先被总量卡住，等于单个上限失效
	if r.Attachment.MaxTotalSize < r.Attachment.MaxSize {
		return fmt.Errorf("%w: attachment.max_total_size(%d) 不应小于 attachment.max_size(%d)",
			ErrInvalidSetting, r.Attachment.MaxTotalSize, r.Attachment.MaxSize)
	}
	if err := checkRange("attachment.max_count", r.Attachment.MaxCount,
		MinAttachmentCount, MaxAttachmentCount); err != nil {
		return err
	}
	if len(r.Attachment.AllowedTypes) > MaxAttachmentTypeCount {
		return fmt.Errorf("%w: attachment.allowed_types 最多 %d 项，当前 %d 项",
			ErrInvalidSetting, MaxAttachmentTypeCount, len(r.Attachment.AllowedTypes))
	}
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
	return nil
}

// checkRange 校验整数落在闭区间内，错误信息带上键名与边界，便于界面直接展示。
func checkRange(name string, v, min, max int) error {
	if v < min || v > max {
		return fmt.Errorf("%w: %s 应在 %d 与 %d 之间，当前为 %d",
			ErrInvalidSetting, name, min, max, v)
	}
	return nil
}

// checkFloatRange 校验浮点数落在闭区间内。QPS 是浮点值，
// 转 int 截断会让 100000.5 这类越界值通过、错误信息也显示不出小数。
func checkFloatRange(name string, v, min, max float64) error {
	if math.IsNaN(v) || v < min || v > max {
		return fmt.Errorf("%w: %s 应在 %g 与 %g 之间，当前为 %g",
			ErrInvalidSetting, name, min, max, v)
	}
	return nil
}

// ---- 配置表解码 ----
//
// 解码一律以基准值（代码默认值，个别字段为 bootstrap.yaml 值）叠加表中存在的键：
// 表内只保存使用者明确改过的项，未命中的键保持基准值不变。
// 表中的脏数据不使整次取值失败，单项退回基准值即可，
// 否则一行坏数据会让整个 Agent 无法启动对话。

func memoryFrom(base config.MemoryConfig, vals map[string]string) config.MemoryConfig {
	if raw, ok := vals[KeyMemoryHistoryWindow]; ok {
		base.HistoryWindow = parseInt(raw, base.HistoryWindow)
	}
	return base
}

func reactFrom(base config.ReactConfig, vals map[string]string) config.ReactConfig {
	if raw, ok := vals[KeyReactMaxIterations]; ok {
		base.MaxIterations = parseInt(raw, base.MaxIterations)
	}
	if raw, ok := vals[KeyReactStepTimeout]; ok {
		base.StepTimeout = parseInt(raw, base.StepTimeout)
	}
	if raw, ok := vals[KeyReactErrorRetry]; ok {
		base.ErrorRetry = parseInt(raw, base.ErrorRetry)
	}
	return base
}

func subAgentFrom(base config.SubAgentConfig, vals map[string]string) config.SubAgentConfig {
	if raw, ok := vals[KeySubAgentMaxConcurrency]; ok {
		base.MaxConcurrency = parseInt(raw, base.MaxConcurrency)
	}
	if raw, ok := vals[KeySubAgentExecTimeout]; ok {
		if _, err := time.ParseDuration(raw); err == nil {
			base.ExecTimeout = raw
		}
	}
	if raw, ok := vals[KeySubAgentMaxTaskLength]; ok {
		base.MaxTaskLength = parseInt(raw, base.MaxTaskLength)
	}
	if raw, ok := vals[KeySubAgentMaxResultLength]; ok {
		base.MaxResultLength = parseInt(raw, base.MaxResultLength)
	}
	return base
}

func attachmentFrom(base config.AttachmentConfig, vals map[string]string) config.AttachmentConfig {
	if raw, ok := vals[KeyAttachmentMaxSize]; ok {
		base.MaxSize = parseInt(raw, base.MaxSize)
	}
	if raw, ok := vals[KeyAttachmentMaxTotalSize]; ok {
		base.MaxTotalSize = parseInt(raw, base.MaxTotalSize)
	}
	if raw, ok := vals[KeyAttachmentMaxCount]; ok {
		base.MaxCount = parseInt(raw, base.MaxCount)
	}
	if raw, ok := vals[KeyAttachmentAllowedTypes]; ok {
		var types []string
		if err := json.Unmarshal([]byte(raw), &types); err == nil {
			base.AllowedTypes = types
		}
	}
	return base
}

// scheduleFrom 只叠加 Enabled；其余两个字段保持 base（bootstrap.yaml）值。
func scheduleFrom(base config.ScheduleConfig, vals map[string]string) config.ScheduleConfig {
	if raw, ok := vals[KeyScheduleEnabled]; ok {
		base.Enabled = parseBool(raw, base.Enabled)
	}
	return base
}

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

// ---- 配置表编码 ----

// rows 把运行时配置摊平成配置表的行。
// 全部键一次写入：界面按分类整体保存，表中留下部分键会让「已明确设置」的
// 语义变得难以判断。
func (r RuntimeSettings) rows() []*repo.Setting {
	kv := []struct {
		name  string
		value string
	}{
		{KeyMemoryHistoryWindow, strconv.Itoa(r.Memory.HistoryWindow)},
		{KeyReactMaxIterations, strconv.Itoa(r.React.MaxIterations)},
		{KeyReactStepTimeout, strconv.Itoa(r.React.StepTimeout)},
		{KeyReactErrorRetry, strconv.Itoa(r.React.ErrorRetry)},
		{KeySubAgentMaxConcurrency, strconv.Itoa(r.SubAgent.MaxConcurrency)},
		{KeySubAgentExecTimeout, r.SubAgent.ExecTimeout},
		{KeySubAgentMaxTaskLength, strconv.Itoa(r.SubAgent.MaxTaskLength)},
		{KeySubAgentMaxResultLength, strconv.Itoa(r.SubAgent.MaxResultLength)},
		{KeyAttachmentMaxSize, strconv.Itoa(r.Attachment.MaxSize)},
		{KeyAttachmentMaxTotalSize, strconv.Itoa(r.Attachment.MaxTotalSize)},
		{KeyAttachmentMaxCount, strconv.Itoa(r.Attachment.MaxCount)},
		{KeyAttachmentAllowedTypes, encodeAllowedTypes(r.Attachment.AllowedTypes)},
		{KeyRateLimitEnabled, strconv.FormatBool(r.RateLimit.Enabled)},
		{KeyRateLimitGlobalQPS, formatFloat(r.RateLimit.GlobalQPS)},
		{KeyRateLimitGlobalConcurrency, strconv.Itoa(r.RateLimit.GlobalConcurrency)},
		{KeyRateLimitDefaultQPS, formatFloat(r.RateLimit.DefaultQPS)},
		{KeyRateLimitDefaultConcurrency, strconv.Itoa(r.RateLimit.DefaultConcurrency)},
		{KeyScheduleEnabled, strconv.FormatBool(r.Schedule.Enabled)},
	}
	out := make([]*repo.Setting, 0, len(kv))
	for _, it := range kv {
		out = append(out, &repo.Setting{Scope: repo.ScopeGlobal, Name: it.name, Value: it.value})
	}
	return out
}

// normalizeAllowedTypes 把附件类型白名单归一化为「不带前导点的小写扩展名」。
//
// UI 输入框与 README 提示的格式是 ".png, .jpg"，而校验侧（attachment 包）拿到
// 的扩展名已由 filepath.Ext 去点。此处在写表前统一形式，使入库值与 UI 回读值
// 都是规范形态。消费侧另有一道同样的归一化，负责兜住存量数据。
//
// 归一化后去重并保持首次出现的顺序：".PNG, png" 这类写法归一化后是同一个
// 扩展名，留着重复项会让界面回读显示两个相同条目，还多占 MaxAttachmentTypeCount
// 的额度。
//
// nil 输入返回 nil，由 encodeAllowedTypes 编码为 "[]"，语义是「不限制类型」。
func normalizeAllowedTypes(types []string) []string {
	if types == nil {
		return nil
	}
	out := make([]string, 0, len(types))
	seen := make(map[string]struct{}, len(types))
	for _, t := range types {
		s := strings.ToLower(strings.TrimSpace(t))
		s = strings.TrimPrefix(s, ".")
		if s == "" {
			continue
		}
		if _, dup := seen[s]; dup {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}

// encodeAllowedTypes 把附件类型白名单归一化后编码为 JSON 数组字符串，
// 是 attachment.allowed_types 写表时的唯一编码（rows 与 ImportLegacy 共用）。
// 归一化放在这里而非各调用点：两条写入路径都经过它，其中 ImportLegacy 不走
// Validate，因此只有在此处统一才能保证入库值一律是规范形态。
// nil 切片被 Marshal 成 null，回落为空数组，语义是「不限制类型」。
func encodeAllowedTypes(types []string) string {
	types = normalizeAllowedTypes(types)
	data, err := json.Marshal(types)
	if err != nil || types == nil {
		return "[]"
	}
	return string(data)
}

// parseInt 解析整数；无法解析时返回 fallback。
func parseInt(raw string, fallback int) int {
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

// parseFloat 解析浮点数；无法解析时返回 fallback。
func parseFloat(raw string, fallback float64) float64 {
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return fallback
	}
	return f
}

// formatFloat 用最短往返表示写出浮点值，避免 100 被写成 100.000000。
func formatFloat(f float64) string {
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// RuntimeStatic 返回不查数据库的运行时配置：代码默认值叠加 bootstrap.yaml
// 中的三个构造参数（限流回收周期、调度并发与同步周期）。
// 供调用方在配置表读取失败时降级使用：配置表故障不应阻断对话执行。
func (s *Settings) RuntimeStatic() RuntimeSettings {
	rl := defaultRateLimit()
	rl.CleanupInterval = s.static.Security.RateLimit.CleanupInterval
	return RuntimeSettings{
		Memory:     defaultMemory(),
		React:      defaultReact(),
		SubAgent:   defaultSubAgent(),
		Attachment: defaultAttachment(),
		RateLimit:  rl,
		Schedule: config.ScheduleConfig{
			Enabled:            defaultScheduleEnabled(),
			MaxConcurrentTasks: s.static.Schedule.MaxConcurrentTasks,
			SyncInterval:       s.static.Schedule.SyncInterval,
		},
	}
}
