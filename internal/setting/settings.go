// Package setting 是程序读取配置的唯一入口。
// Settings 私有地持有 bootstrap.yaml 解析结果与配置表仓库，对外只按分类暴露方法，
// 调用方从方法返回的结构体上取属性，不关心某一项配置来自 bootstrap.yaml 还是数据库。
//
// 配置表中缺失的键由代码默认值填充，因此表在创建后为空即可工作，
// 新增配置项也无需为已有数据库补写初始数据。
//
// 本包独立于 internal/config 而非并入其中：config 处于依赖链底层，
// 而配置表仓库需要引用它完成环境变量展开，配置对象置于两者之上可避免循环导入。
package setting

import (
	"context"
	"errors"
	"strconv"

	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/repo"
)

// ErrNoSettingStore 配置表仓库未装配，无法写入配置。
var ErrNoSettingStore = errors.New("setting: 配置存储不可用")

// Settings 配置对象。
//
// 全部分类方法统一签名 Xxx(ctx) (T, error)：来自 bootstrap.yaml 的分类不使用 ctx 且
// error 恒为 nil，但保持签名一致，使某一分类日后迁移到配置表时调用方无需改动。
//
// 来自配置表的分类每次调用查询数据库，不做内存缓存：设置面板保存后当次请求
// 即可读到新值，多节点共享同一数据库时各节点取值一致。语音配置的读取时机
// （打开聊天页、点击话筒、转录接口被调用）均为单次按作用域的范围查询，不在热路径。
// 限流参数由中间件在启动时取一次实例，在线改动经 Reconfigure 推送，
// Security() 不在请求路径上。
// Schedule() 位于每轮对话的取工具路径上，每轮一次范围查询；门控每组只求值一次。
type Settings struct {
	static config.Bootstrap
	repo   repo.SettingRepo
}

// New 构造配置对象。settingRepo 为 nil 时，来自配置表的分类一律返回代码默认值，
// SetVoice 等写入方法返回 ErrNoSettingStore，
// 便于在尚未接入数据库的场景（如部分单元测试）中使用。
func New(static config.Bootstrap, settingRepo repo.SettingRepo) *Settings {
	return &Settings{static: static, repo: settingRepo}
}

// ---- 来自 bootstrap.yaml 的分类 ----

// Agent 返回 agent 元信息配置，来自 bootstrap.yaml。
func (s *Settings) Agent(ctx context.Context) (config.AgentConfig, error) {
	return s.static.Agent, nil
}

// Server 返回 HTTP 服务配置，来自 bootstrap.yaml。
func (s *Settings) Server(ctx context.Context) (config.ServerConfig, error) {
	return s.static.Server, nil
}

// Logging 返回日志配置，来自 bootstrap.yaml。
func (s *Settings) Logging(ctx context.Context) (config.LoggingConfig, error) {
	return s.static.Logging, nil
}

// Database 返回数据库配置，来自 bootstrap.yaml。
func (s *Settings) Database(ctx context.Context) (*config.DatabaseConfig, error) {
	return s.static.Database, nil
}

// ---- 来自配置表的分类 ----

// Memory 读取记忆配置。
func (s *Settings) Memory(ctx context.Context) (config.MemoryConfig, error) {
	base := defaultMemory()
	if s.repo == nil {
		return base, nil
	}
	vals, err := s.globalValues(ctx)
	if err != nil {
		return config.MemoryConfig{}, err
	}
	return memoryFrom(base, vals), nil
}

// React 读取 ReAct 循环配置。
//
// 主 Agent 的 Engine 每次执行时构造，改动对随后的对话即刻生效。
// 子 Agent 的迭代与超时在加载 subagents 目录时就固化进了注册表条目，
// 改动要等注册表重建才会跟上。
func (s *Settings) React(ctx context.Context) (config.ReactConfig, error) {
	base := defaultReact()
	if s.repo == nil {
		return base, nil
	}
	vals, err := s.globalValues(ctx)
	if err != nil {
		return config.ReactConfig{}, err
	}
	return reactFrom(base, vals), nil
}

// SubAgent 读取子 agent 配置。MaxConcurrency 的生效方式见 RuntimeSettings 注释。
func (s *Settings) SubAgent(ctx context.Context) (config.SubAgentConfig, error) {
	base := defaultSubAgent()
	if s.repo == nil {
		return base, nil
	}
	vals, err := s.globalValues(ctx)
	if err != nil {
		return config.SubAgentConfig{}, err
	}
	return subAgentFrom(base, vals), nil
}

// Attachment 读取附件配置。
func (s *Settings) Attachment(ctx context.Context) (config.AttachmentConfig, error) {
	base := defaultAttachment()
	if s.repo == nil {
		return base, nil
	}
	vals, err := s.globalValues(ctx)
	if err != nil {
		return config.AttachmentConfig{}, err
	}
	return attachmentFrom(base, vals), nil
}

// Security 返回安全配置。Auth 与限流阈值来自配置表，
// 限流的 CleanupInterval 来自 bootstrap.yaml（后台回收协程的周期）。
// 中间件在启动时拿到限流器实例，限流参数的在线改动由
// ratelimit.RateLimiter.Reconfigure 承接，这里的返回值供接口回读与启动应用。
func (s *Settings) Security(ctx context.Context) (config.SecurityConfig, error) {
	auth, err := s.Auth(ctx)
	if err != nil {
		return config.SecurityConfig{}, err
	}
	base := defaultRateLimit()
	base.CleanupInterval = s.static.Security.RateLimit.CleanupInterval
	out := config.SecurityConfig{Auth: auth}
	if s.repo == nil {
		out.RateLimit = base
		return out, nil
	}
	vals, err := s.globalValues(ctx)
	if err != nil {
		return config.SecurityConfig{}, err
	}
	out.RateLimit = rateLimitFrom(base, vals)
	return out, nil
}

// Schedule 返回定时任务配置。Enabled 来自配置表，
// MaxConcurrentTasks 与 SyncInterval 来自 bootstrap.yaml（调度器构造参数）。
// 内置工具门控每次取工具时调用它一次（每组一次，不是每工具一次），
// 这是一次按作用域的范围查询，与 Executor 每次执行开始时读 Runtime 的代价相同。
func (s *Settings) Schedule(ctx context.Context) (config.ScheduleConfig, error) {
	base := config.ScheduleConfig{
		Enabled:            defaultScheduleEnabled(),
		MaxConcurrentTasks: s.static.Schedule.MaxConcurrentTasks,
		SyncInterval:       s.static.Schedule.SyncInterval,
	}
	if s.repo == nil {
		return base, nil
	}
	vals, err := s.globalValues(ctx)
	if err != nil {
		return config.ScheduleConfig{}, err
	}
	return scheduleFrom(base, vals), nil
}

// Runtime 一次取出全部运行时配置，供设置面板加载。
// 设置面板需要多个分类，逐个调用会多次打数据库，这里合并为一次范围查询。
func (s *Settings) Runtime(ctx context.Context) (RuntimeSettings, error) {
	base := s.RuntimeStatic()
	if s.repo == nil {
		return base, nil
	}
	vals, err := s.globalValues(ctx)
	if err != nil {
		return RuntimeSettings{}, err
	}
	return RuntimeSettings{
		Memory:     memoryFrom(base.Memory, vals),
		React:      reactFrom(base.React, vals),
		SubAgent:   subAgentFrom(base.SubAgent, vals),
		Attachment: attachmentFrom(base.Attachment, vals),
		RateLimit:  rateLimitFrom(base.RateLimit, vals),
		Schedule:   scheduleFrom(base.Schedule, vals),
	}, nil
}

// SetRuntime 校验并整体保存全部运行时配置。
// 越界即整次拒绝，不做部分写入：半套生效的配置比拒绝更难排查。
func (s *Settings) SetRuntime(ctx context.Context, r RuntimeSettings) error {
	if err := r.Validate(); err != nil {
		return err
	}
	if s.repo == nil {
		return ErrNoSettingStore
	}
	return s.repo.Upsert(ctx, r.rows()...)
}

// Voice 读取语音配置。表内缺失的字段由代码默认值填充。
// 出错时返回值无意义，调用方须先检查 error。
func (s *Settings) Voice(ctx context.Context) (VoiceSettings, error) {
	v := defaultVoice()
	if s.repo == nil {
		return v, nil
	}
	vals, err := s.globalValues(ctx)
	if err != nil {
		return VoiceSettings{}, err
	}
	if raw, ok := vals[KeyVoiceEnabled]; ok {
		v.Enabled = parseBool(raw, v.Enabled)
	}
	if raw, ok := vals[KeyVoiceModel]; ok {
		v.Model = raw
	}
	if raw, ok := vals[KeyVoiceAutoSend]; ok {
		v.AutoSend = parseBool(raw, v.AutoSend)
	}
	return v, nil
}

// SetVoice 整体保存语音配置的三个字段。
// 三个键写入后即视为明确设置，之后不再跟随代码默认值变化；恢复默认需删除对应行。
func (s *Settings) SetVoice(ctx context.Context, v VoiceSettings) error {
	if s.repo == nil {
		return ErrNoSettingStore
	}
	return s.repo.Upsert(ctx,
		&repo.Setting{Scope: repo.ScopeGlobal, Name: KeyVoiceEnabled, Value: strconv.FormatBool(v.Enabled)},
		&repo.Setting{Scope: repo.ScopeGlobal, Name: KeyVoiceModel, Value: v.Model},
		&repo.Setting{Scope: repo.ScopeGlobal, Name: KeyVoiceAutoSend, Value: strconv.FormatBool(v.AutoSend)},
	)
}

// globalValues 一次取出全局作用域的全部配置，避免一个分类内逐键查询。
func (s *Settings) globalValues(ctx context.Context) (map[string]string, error) {
	items, err := s.repo.ListByScope(ctx, repo.ScopeGlobal, "")
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(items))
	for _, it := range items {
		out[it.Name] = it.Value
	}
	return out, nil
}

// parseBool 解析布尔值；无法解析时返回 fallback。
// 表中的脏数据不应导致整次取值失败，退回默认值即可。
func parseBool(raw string, fallback bool) bool {
	b, err := strconv.ParseBool(raw)
	if err != nil {
		return fallback
	}
	return b
}
