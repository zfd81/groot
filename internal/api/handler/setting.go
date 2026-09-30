package handler

import (
	"context"
	"errors"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/ratelimit"
	"github.com/zfd81/groot/internal/setting"
)

// SettingHandler 处理配置的读写。接口按分类而非按单键暴露，
// 与设置面板的分区一一对应。
//
// registry 与 limiter 是两个生效载体：配置表是持久来源，它们是运行中的实例。
// 表与实例都要更新——只写表则要等重启才生效，只改实例则重启后丢失。
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

// GetVoice 处理 GET /web/settings/voice。
func (h *SettingHandler) GetVoice(ctx context.Context, rc *app.RequestContext) {
	v, err := h.settings.Voice(ctx)
	if err != nil {
		h.log.Error("读取语音配置失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
		return
	}
	rc.JSON(200, types.VoiceSettingsResponse{
		Enabled:  v.Enabled,
		Model:    v.Model,
		AutoSend: v.AutoSend,
	})
}

// PutVoice 处理 PUT /web/settings/voice，整体保存三个字段。
func (h *SettingHandler) PutVoice(ctx context.Context, rc *app.RequestContext) {
	var req types.VoiceSettingsRequest
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求参数错误"})
		return
	}

	// 仅在开关打开且 model 非空时校验：模型必须已存在且启用，否则话筒一按就报错。
	// 关闭开关时不校验，这样所选模型被删除或禁用后仍能关闭语音输入。
	name := strings.TrimSpace(req.Model)
	if req.Enabled && name != "" {
		if _, err := h.models.GetByName(ctx, name); err != nil {
			if errors.Is(err, llm.ErrModelNotFound) || errors.Is(err, llm.ErrModelDisabled) {
				rc.JSON(400, utils.H{"status": "invalid_model", "message": err.Error()})
				return
			}
			h.log.Error("校验语音模型失败", zap.String("model", name), zap.Error(err))
			rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
			return
		}
	}

	err := h.settings.SetVoice(ctx, setting.VoiceSettings{
		Enabled:  req.Enabled,
		Model:    name,
		AutoSend: req.AutoSend,
	})
	if err != nil {
		h.log.Error("保存语音配置失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
		return
	}
	rc.JSON(200, utils.H{"status": "ok"})
}

// GetRuntime 处理 GET /web/settings/runtime。
// 返回的是当次生效值：表内没有的项已由配置对象回落到 YAML 或代码默认值，
// 界面上看到的即为服务端正在使用的值。
func (h *SettingHandler) GetRuntime(ctx context.Context, rc *app.RequestContext) {
	r, err := h.settings.Runtime(ctx)
	if err != nil {
		h.log.Error("读取运行时配置失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
		return
	}
	rc.JSON(200, runtimeToPayload(r))
}

// PutRuntime 处理 PUT /web/settings/runtime，整体保存全部分区。
// 任一项越界即整次拒绝，不做部分写入：半套生效的配置比拒绝更难排查。
//
// 子 Agent 并发上限在写表成功后同步到注册表的信号量。新上限只对随后新发起的
// 调用生效：已经拿到名额的调用继续执行，已在排队的调用仍等旧信号量，
// 因为提前打断正在跑的子 Agent 会丢掉它未返回的工作。
//
// 限流参数在写表成功后同步到限流器。已建桶的调用方保留旧容量，
// 随后首次出现的调用方按新参数建桶，正在进行的请求不被打断。
func (h *SettingHandler) PutRuntime(ctx context.Context, rc *app.RequestContext) {
	var req types.RuntimeSettingsPayload
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求参数错误"})
		return
	}
	if req.RateLimit == nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "缺少 rate_limit 分区"})
		return
	}
	if req.Schedule == nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "缺少 schedule 分区"})
		return
	}

	next := payloadToRuntime(req, h.settings.RuntimeStatic())
	if err := h.settings.SetRuntime(ctx, next); err != nil {
		if errors.Is(err, setting.ErrInvalidSetting) {
			rc.JSON(400, utils.H{"status": "invalid_request", "message": err.Error()})
			return
		}
		h.log.Error("保存运行时配置失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
		return
	}

	// 写表在前、通知持有方在后：写表失败时上面已返回，不会留下
	// 「实例已改、表里还是旧值」的状态
	if h.registry != nil {
		h.registry.SetMaxConcurrency(next.SubAgent.MaxConcurrency)
	}
	if h.limiter != nil {
		h.limiter.Reconfigure(next.RateLimit)
	}
	rc.JSON(200, runtimeToPayload(next))
}

// runtimeToPayload 把配置对象的结构摊平成接口结构。
func runtimeToPayload(r setting.RuntimeSettings) types.RuntimeSettingsPayload {
	allowed := r.Attachment.AllowedTypes
	if allowed == nil {
		// nil 会被序列化成 null，前端的下拉与标签列表需要的是空数组
		allowed = []string{}
	}
	return types.RuntimeSettingsPayload{
		Memory: types.MemorySettings{HistoryWindow: r.Memory.HistoryWindow},
		React: types.ReactSettings{
			MaxIterations: r.React.MaxIterations,
			StepTimeout:   r.React.StepTimeout,
			ErrorRetry:    r.React.ErrorRetry,
		},
		SubAgent: types.SubAgentSettings{
			MaxConcurrency:  r.SubAgent.MaxConcurrency,
			ExecTimeout:     r.SubAgent.ExecTimeout,
			MaxTaskLength:   r.SubAgent.MaxTaskLength,
			MaxResultLength: r.SubAgent.MaxResultLength,
		},
		Attachment: types.AttachmentSettings{
			MaxSize:      r.Attachment.MaxSize,
			MaxTotalSize: r.Attachment.MaxTotalSize,
			MaxCount:     r.Attachment.MaxCount,
			AllowedTypes: allowed,
		},
		RateLimit: &types.RateLimitSettings{
			Enabled:            r.RateLimit.Enabled,
			GlobalQPS:          r.RateLimit.GlobalQPS,
			GlobalConcurrency:  r.RateLimit.GlobalConcurrency,
			DefaultQPS:         r.RateLimit.DefaultQPS,
			DefaultConcurrency: r.RateLimit.DefaultConcurrency,
		},
		Schedule: &types.ScheduleSettings{Enabled: r.Schedule.Enabled},
	}
}

// payloadToRuntime 把接口结构还原成配置对象的结构。
// base 提供请求体不携带的字段：RateLimit.CleanupInterval、Schedule.MaxConcurrentTasks
// 与 Schedule.SyncInterval；它们不进配置表，用 YAML 值即可。
// 调用方须先保证 p.RateLimit 与 p.Schedule 非 nil。
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
	out.Schedule.Enabled = p.Schedule.Enabled
	return out
}
