package handler

import (
	"context"
	"encoding/json"
	"sort"
	"strings"
	"testing"

	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/message"
	"github.com/zfd81/groot/internal/ratelimit"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/modeldb"
	"github.com/zfd81/groot/internal/repo/settingdb"
	"github.com/zfd81/groot/internal/setting"
)

// newSettingHandlerForTest 建 handler，并按需预置一个模型（enabled 决定是否启用）。
func newSettingHandlerForTest(t *testing.T, withModel string, enabled bool) *SettingHandler {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })

	models := llm.NewModelService(modeldb.New(sqlxDB, dialect))
	if withModel != "" {
		// ModelService.Create 会把库中第一个模型强制设为默认且启用，
		// 需要禁用模型时先预置一个启用的占位默认模型，让 enabled=false 得以保留。
		if !enabled {
			err := models.Create(context.Background(), &repo.Model{
				Name: "seed-default", Model: "seed-default",
				BaseURL: "https://api.openai.com/v1", APIKey: "sk-test-1234abcd",
				Enabled: true, Stop: []string{},
			})
			if err != nil {
				t.Fatalf("创建占位默认模型: %v", err)
			}
		}
		err := models.Create(context.Background(), &repo.Model{
			Name: withModel, Model: withModel,
			BaseURL: "https://api.openai.com/v1", APIKey: "sk-test-1234abcd",
			Enabled: enabled, Stop: []string{},
		})
		if err != nil {
			t.Fatalf("创建模型: %v", err)
		}
	}
	settings := setting.New(config.Bootstrap{}, settingdb.New(sqlxDB, dialect))
	return NewSettingHandler(SettingHandlerDeps{
		Settings: settings,
		Models:   models,
		Registry: agent.NewRegistryForTest(4),
		Log:      logger.NewNop(),
	})
}

func TestSettingHandler_GetVoiceDefaults(t *testing.T) {
	h := newSettingHandlerForTest(t, "", false)

	rc := callJSON(h.GetVoice, consts.MethodGet, "", nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var out types.VoiceSettingsResponse
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if out.Enabled || out.Model != "" || out.AutoSend {
		t.Errorf("表为空时应返回默认值，得到 %+v", out)
	}
}

func TestSettingHandler_PutVoiceRoundTrip(t *testing.T) {
	h := newSettingHandlerForTest(t, "whisper-1", true)

	body := `{"enabled":true,"model":"whisper-1","auto_send":true}`
	rc := callJSON(h.PutVoice, consts.MethodPut, body, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("PutVoice status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}

	rc = callJSON(h.GetVoice, consts.MethodGet, "", nil)
	var out types.VoiceSettingsResponse
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if !out.Enabled || out.Model != "whisper-1" || !out.AutoSend {
		t.Errorf("回读 = %+v, want 全部生效", out)
	}
}

func TestSettingHandler_PutVoiceEmptyModelAllowed(t *testing.T) {
	// model 为空串表示「尚未指定」，允许保存：使用者可以先开开关再选模型
	h := newSettingHandlerForTest(t, "", false)

	rc := callJSON(h.PutVoice, consts.MethodPut, `{"enabled":true,"model":"","auto_send":false}`, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
}

func TestSettingHandler_PutVoiceUnknownModel(t *testing.T) {
	h := newSettingHandlerForTest(t, "whisper-1", true)

	rc := callJSON(h.PutVoice, consts.MethodPut, `{"enabled":true,"model":"nope","auto_send":false}`, nil)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "invalid_model" {
		t.Errorf("status = %q, want invalid_model", s)
	}
	if !strings.Contains(string(rc.Response.Body()), "nope") {
		t.Errorf("响应应包含模型名 nope，得到 %s", rc.Response.Body())
	}
}

func TestSettingHandler_PutVoiceDisabledModel(t *testing.T) {
	h := newSettingHandlerForTest(t, "whisper-1", false)

	rc := callJSON(h.PutVoice, consts.MethodPut, `{"enabled":true,"model":"whisper-1","auto_send":false}`, nil)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400（已禁用的模型不应被选为语音模型）", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "invalid_model" {
		t.Errorf("status = %q, want invalid_model", s)
	}
	if !strings.Contains(string(rc.Response.Body()), "whisper-1") {
		t.Errorf("响应应包含模型名 whisper-1，得到 %s", rc.Response.Body())
	}
}

func TestSettingHandler_PutVoiceBadJSON(t *testing.T) {
	h := newSettingHandlerForTest(t, "", false)

	rc := callJSON(h.PutVoice, consts.MethodPut, `{not json`, nil)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "invalid_request" {
		t.Errorf("status = %q, want invalid_request", s)
	}
}

func TestSettingHandler_PutVoiceTrimsModel(t *testing.T) {
	h := newSettingHandlerForTest(t, "whisper-1", true)

	rc := callJSON(h.PutVoice, consts.MethodPut, `{"enabled":true,"model":"  whisper-1  ","auto_send":false}`, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("PutVoice status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}

	rc = callJSON(h.GetVoice, consts.MethodGet, "", nil)
	var out types.VoiceSettingsResponse
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if out.Model != "whisper-1" {
		t.Errorf("Model = %q, want 去除首尾空白后的 whisper-1", out.Model)
	}
}

func TestSettingHandler_DisableWithStaleModel(t *testing.T) {
	// 所选模型已被禁用时，关闭开关不应被拦截
	h := newSettingHandlerForTest(t, "whisper-1", false)

	rc := callJSON(h.PutVoice, consts.MethodPut, `{"enabled":false,"model":"whisper-1","auto_send":false}`, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s, want 200（关闭开关不应校验模型）", rc.Response.StatusCode(), rc.Response.Body())
	}
}

// runtimeBoot 是测试用的 bootstrap 静态层：只承载不进表的构造参数，
// 业务项的基准层是代码默认值。
func runtimeBoot() config.Bootstrap {
	return config.Bootstrap{
		Schedule: config.ScheduleBootstrap{MaxConcurrentTasks: 3, SyncInterval: "30s"},
		Security: config.SecurityBootstrap{
			RateLimit: config.RateLimitBootstrap{CleanupInterval: "1m"},
		},
	}
}

// validRateLimit 返回一份合法的限流分区，供请求体字面量复用。
func validRateLimit() *types.RateLimitSettings {
	return &types.RateLimitSettings{Enabled: true, DefaultQPS: 50, DefaultConcurrency: 20}
}

// validSchedule 返回一份合法的调度分区（关闭），供请求体字面量复用。
func validSchedule() *types.ScheduleSettings {
	return &types.ScheduleSettings{Enabled: false}
}

// newRuntimeHandlerForTest 建一个带 bootstrap 静态层、真实配置表与真实限流器的 handler。
func newRuntimeHandlerForTest(t *testing.T) *SettingHandler {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })

	rl, err := ratelimit.New(config.RateLimitConfig{
		Enabled: true, DefaultQPS: 50, DefaultConcurrency: 20, CleanupInterval: "1m",
	})
	if err != nil {
		t.Fatalf("ratelimit.New: %v", err)
	}
	t.Cleanup(rl.Stop)
	models := llm.NewModelService(modeldb.New(sqlxDB, dialect))
	settings := setting.New(runtimeBoot(), settingdb.New(sqlxDB, dialect))
	return NewSettingHandler(SettingHandlerDeps{
		Settings: settings,
		Models:   models,
		Registry: agent.NewRegistryForTest(4),
		Limiter:  rl,
		Log:      logger.NewNop(),
	})
}

func TestSettingHandler_GetRuntimeFallsBackToDefaults(t *testing.T) {
	h := newRuntimeHandlerForTest(t)

	rc := callJSON(h.GetRuntime, consts.MethodGet, "", nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var out types.RuntimeSettingsPayload
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if out.Memory.HistoryWindow != 20 {
		t.Errorf("history_window = %d, want 默认值 20", out.Memory.HistoryWindow)
	}
	if out.React.MaxIterations != 20 || out.React.StepTimeout != 60 || out.React.ErrorRetry != 2 {
		t.Errorf("react 回落错误: %+v, want 默认值 20/60/2", out.React)
	}
	if out.SubAgent.MaxConcurrency != 5 || out.SubAgent.ExecTimeout != "5m" || out.SubAgent.MaxTaskLength != 16000 {
		t.Errorf("subagent 回落错误: %+v, want 默认值 5/5m/16000", out.SubAgent)
	}
	if len(out.Attachment.AllowedTypes) != 0 {
		t.Errorf("allowed_types = %v, want 默认的空（允许所有）", out.Attachment.AllowedTypes)
	}
}

func TestSettingHandler_PutRuntimeThenGet(t *testing.T) {
	h := newRuntimeHandlerForTest(t)

	body := types.RuntimeSettingsPayload{
		Memory:     types.MemorySettings{HistoryWindow: -1},
		React:      types.ReactSettings{MaxIterations: 50, StepTimeout: 60, ErrorRetry: 0},
		SubAgent:   types.SubAgentSettings{MaxConcurrency: 4, ExecTimeout: "10m", MaxTaskLength: 4000, MaxResultLength: 9000},
		Attachment: types.AttachmentSettings{MaxSize: 20, MaxTotalSize: 80, MaxCount: 8, AllowedTypes: []string{".jpg"}},
		RateLimit:  validRateLimit(),
		Schedule:   validSchedule(),
	}
	raw, _ := json.Marshal(body)
	rc := callJSON(h.PutRuntime, consts.MethodPut, string(raw), nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("PUT status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}

	rc2 := callJSON(h.GetRuntime, consts.MethodGet, "", nil)
	var out types.RuntimeSettingsPayload
	if err := json.Unmarshal(rc2.Response.Body(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if out.Memory.HistoryWindow != -1 {
		t.Errorf("history_window = %d, want -1", out.Memory.HistoryWindow)
	}
	if out.React.MaxIterations != 50 || out.React.ErrorRetry != 0 {
		t.Errorf("react 未持久化: %+v", out.React)
	}
	if out.SubAgent.ExecTimeout != "10m" {
		t.Errorf("exec_timeout = %q, want 10m", out.SubAgent.ExecTimeout)
	}
	if out.Attachment.MaxSize != 20 || len(out.Attachment.AllowedTypes) != 1 {
		t.Errorf("attachment 未持久化: %+v", out.Attachment)
	}
}

// 并发上限随请求体保存，并同步到注册表的信号量。
func TestSettingHandler_PutRuntimeAppliesMaxConcurrency(t *testing.T) {
	h := newRuntimeHandlerForTest(t)

	body := types.RuntimeSettingsPayload{
		Memory:     types.MemorySettings{HistoryWindow: 10},
		React:      types.ReactSettings{MaxIterations: 30, StepTimeout: 120, ErrorRetry: 1},
		SubAgent:   types.SubAgentSettings{MaxConcurrency: 9, ExecTimeout: "5m", MaxTaskLength: 3000, MaxResultLength: 8000},
		Attachment: types.AttachmentSettings{MaxSize: 10, MaxTotalSize: 50, MaxCount: 5, AllowedTypes: []string{}},
		RateLimit:  validRateLimit(),
		Schedule:   validSchedule(),
	}
	raw, _ := json.Marshal(body)
	rc := callJSON(h.PutRuntime, consts.MethodPut, string(raw), nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("PUT status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}

	// 响应体回显新值，界面保存后无需再发一次 GET
	var out types.RuntimeSettingsPayload
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if out.SubAgent.MaxConcurrency != 9 {
		t.Errorf("响应 max_concurrency = %d, want 9", out.SubAgent.MaxConcurrency)
	}

	got, err := h.settings.Runtime(context.Background())
	if err != nil {
		t.Fatalf("Runtime: %v", err)
	}
	if got.SubAgent.MaxConcurrency != 9 {
		t.Errorf("表内 max_concurrency = %d, want 9", got.SubAgent.MaxConcurrency)
	}
	if n := h.registry.MaxConcurrency(); n != 9 {
		t.Errorf("信号量容量 = %d, want 9（保存后应立即生效）", n)
	}
}

// 注册表为 nil 时（未装配子 Agent 能力）保存仍应成功，只是不换信号量。
func TestSettingHandler_PutRuntimeWithoutRegistry(t *testing.T) {
	h := newRuntimeHandlerForTest(t)
	h.registry = nil

	body := types.RuntimeSettingsPayload{
		Memory:     types.MemorySettings{HistoryWindow: 10},
		React:      types.ReactSettings{MaxIterations: 30, StepTimeout: 120, ErrorRetry: 1},
		SubAgent:   types.SubAgentSettings{MaxConcurrency: 6, ExecTimeout: "5m", MaxTaskLength: 3000, MaxResultLength: 8000},
		Attachment: types.AttachmentSettings{MaxSize: 10, MaxTotalSize: 50, MaxCount: 5, AllowedTypes: []string{}},
		RateLimit:  validRateLimit(),
		Schedule:   validSchedule(),
	}
	raw, _ := json.Marshal(body)
	if rc := callJSON(h.PutRuntime, consts.MethodPut, string(raw), nil); rc.Response.StatusCode() != 200 {
		t.Fatalf("PUT status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
}

func TestSettingHandler_PutRuntimeRejectsOutOfRange(t *testing.T) {
	cases := []struct {
		name string
		body types.RuntimeSettingsPayload
	}{
		{"迭代上限为 0", types.RuntimeSettingsPayload{
			Memory: types.MemorySettings{HistoryWindow: 10},
			React:  types.ReactSettings{MaxIterations: 0, StepTimeout: 120, ErrorRetry: 1},
			SubAgent: types.SubAgentSettings{MaxConcurrency: 4, ExecTimeout: "5m",
				MaxTaskLength: 3000, MaxResultLength: 8000},
			Attachment: types.AttachmentSettings{MaxSize: 10, MaxTotalSize: 50, MaxCount: 5},
			RateLimit:  validRateLimit(),
			Schedule:   validSchedule(),
		}},
		{"步超时为 0", types.RuntimeSettingsPayload{
			Memory: types.MemorySettings{HistoryWindow: 10},
			React:  types.ReactSettings{MaxIterations: 30, StepTimeout: 0, ErrorRetry: 1},
			SubAgent: types.SubAgentSettings{MaxConcurrency: 4, ExecTimeout: "5m",
				MaxTaskLength: 3000, MaxResultLength: 8000},
			Attachment: types.AttachmentSettings{MaxSize: 10, MaxTotalSize: 50, MaxCount: 5},
			RateLimit:  validRateLimit(),
			Schedule:   validSchedule(),
		}},
		{"exec_timeout 非法", types.RuntimeSettingsPayload{
			Memory: types.MemorySettings{HistoryWindow: 10},
			React:  types.ReactSettings{MaxIterations: 30, StepTimeout: 120, ErrorRetry: 1},
			SubAgent: types.SubAgentSettings{MaxConcurrency: 4, ExecTimeout: "abc",
				MaxTaskLength: 3000, MaxResultLength: 8000},
			Attachment: types.AttachmentSettings{MaxSize: 10, MaxTotalSize: 50, MaxCount: 5},
			RateLimit:  validRateLimit(),
			Schedule:   validSchedule(),
		}},
		{"附件单文件上限为 0", types.RuntimeSettingsPayload{
			Memory: types.MemorySettings{HistoryWindow: 10},
			React:  types.ReactSettings{MaxIterations: 30, StepTimeout: 120, ErrorRetry: 1},
			SubAgent: types.SubAgentSettings{MaxConcurrency: 4, ExecTimeout: "5m",
				MaxTaskLength: 3000, MaxResultLength: 8000},
			Attachment: types.AttachmentSettings{MaxSize: 0, MaxTotalSize: 50, MaxCount: 5},
			RateLimit:  validRateLimit(),
			Schedule:   validSchedule(),
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h := newRuntimeHandlerForTest(t)
			raw, _ := json.Marshal(tc.body)
			rc := callJSON(h.PutRuntime, consts.MethodPut, string(raw), nil)
			if rc.Response.StatusCode() != 400 {
				t.Fatalf("status=%d, want 400，body=%s",
					rc.Response.StatusCode(), rc.Response.Body())
			}
			if !strings.Contains(string(rc.Response.Body()), "invalid_request") {
				t.Errorf("响应应含 invalid_request，得到 %s", rc.Response.Body())
			}
		})
	}
}

// 并发上限越界应整次拒绝。
func TestSettingHandler_PutRuntimeRejectsMaxConcurrencyOutOfRange(t *testing.T) {
	for _, n := range []int{0, 101} {
		h := newRuntimeHandlerForTest(t)
		body := types.RuntimeSettingsPayload{
			Memory:     types.MemorySettings{HistoryWindow: 10},
			React:      types.ReactSettings{MaxIterations: 30, StepTimeout: 120, ErrorRetry: 1},
			SubAgent:   types.SubAgentSettings{MaxConcurrency: n, ExecTimeout: "5m", MaxTaskLength: 3000, MaxResultLength: 8000},
			Attachment: types.AttachmentSettings{MaxSize: 10, MaxTotalSize: 50, MaxCount: 5, AllowedTypes: []string{}},
			RateLimit:  validRateLimit(),
			Schedule:   validSchedule(),
		}
		raw, _ := json.Marshal(body)
		rc := callJSON(h.PutRuntime, consts.MethodPut, string(raw), nil)
		if rc.Response.StatusCode() != 400 {
			t.Errorf("max_concurrency=%d: status=%d, want 400，body=%s", n, rc.Response.StatusCode(), rc.Response.Body())
		}
		// 拒绝时信号量不应被改动
		if got := h.registry.MaxConcurrency(); got != 4 {
			t.Errorf("max_concurrency=%d: 信号量被改成了 %d，越界请求不应生效", n, got)
		}
	}
}

// TestSettingHandler_PutRuntimeAppliesRateLimit 验证保存后限流器即刻按新参数工作。
func TestSettingHandler_PutRuntimeAppliesRateLimit(t *testing.T) {
	h := newRuntimeHandlerForTest(t)

	body := runtimeBodyWith(`"rate_limit":{"enabled":true,"global_qps":0,"global_concurrency":0,
		"default_qps":7,"default_concurrency":3}`)
	rc := callJSON(h.PutRuntime, consts.MethodPut, body, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}

	got := h.limiter.Config()
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

	// 回读闭环：rows() 写入的值能被 rateLimitFrom 读回
	rc = callJSON(h.GetRuntime, consts.MethodGet, "", nil)
	var out types.RuntimeSettingsPayload
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if out.RateLimit == nil {
		t.Fatal("回读缺少 rate_limit 分区")
	}
	if out.RateLimit.DefaultQPS != 7 || out.RateLimit.DefaultConcurrency != 3 || !out.RateLimit.Enabled {
		t.Errorf("回读限流分区 = %+v, want 7/3/true", *out.RateLimit)
	}
}

// TestSettingHandler_PutRuntimeRejectsMissingRateLimit 验证不带 rate_limit 分区的整包被拒绝，
// 而不是被解码成全零后静默关闭限流。
func TestSettingHandler_PutRuntimeRejectsMissingRateLimit(t *testing.T) {
	h := newRuntimeHandlerForTest(t)

	before := h.limiter.Config()
	rc := callJSON(h.PutRuntime, consts.MethodPut, runtimeBodyWithout("rate_limit"), nil)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400，body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if s := bodyStatus(t, rc); s != "invalid_request" {
		t.Errorf("status = %q, want invalid_request", s)
	}
	// 守卫的 400 与越界的 400 同为 invalid_request，靠消息区分
	if !strings.Contains(string(rc.Response.Body()), "rate_limit") {
		t.Errorf("响应应指明缺少 rate_limit 分区，得到 %s", rc.Response.Body())
	}
	if h.limiter.Config() != before {
		t.Error("缺分区的请求不应改动限流器")
	}
}

// TestSettingHandler_PutRuntimeRejectsBadRateLimit 验证越界请求既不写表也不动限流器。
func TestSettingHandler_PutRuntimeRejectsBadRateLimit(t *testing.T) {
	h := newRuntimeHandlerForTest(t)

	before := h.limiter.Config()
	body := runtimeBodyWith(`"rate_limit":{"enabled":true,"global_qps":0,"global_concurrency":0,
		"default_qps":-5,"default_concurrency":3}`)
	rc := callJSON(h.PutRuntime, consts.MethodPut, body, nil)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "invalid_request" {
		t.Errorf("status = %q, want invalid_request", s)
	}
	if h.limiter.Config() != before {
		t.Error("越界请求不应改动限流器")
	}

	rc = callJSON(h.GetRuntime, consts.MethodGet, "", nil)
	var out types.RuntimeSettingsPayload
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if out.RateLimit == nil || out.RateLimit.DefaultQPS != 10 {
		t.Errorf("rate_limit = %+v, default_qps 应仍为默认值 10", out.RateLimit)
	}
}

// TestSettingHandler_GetRuntimeIncludesRateLimit 验证回读带上限流分区。
func TestSettingHandler_GetRuntimeIncludesRateLimit(t *testing.T) {
	h := newRuntimeHandlerForTest(t)

	rc := callJSON(h.GetRuntime, consts.MethodGet, "", nil)
	var out types.RuntimeSettingsPayload
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if out.RateLimit == nil {
		t.Fatal("回读缺少 rate_limit 分区")
	}
	if out.RateLimit.DefaultQPS != 10 || out.RateLimit.DefaultConcurrency != 5 {
		t.Errorf("限流分区 = %+v, want 默认值 10/5", *out.RateLimit)
	}
}

// runtimeSections 是一份各分区均合法的请求体片段表，键为分区名。
// 缺分区用例从中删掉一项再拼接，其余用例全量拼接。
func runtimeSections() map[string]string {
	return map[string]string{
		"memory":     `"memory":{"history_window":20}`,
		"react":      `"react":{"max_iterations":30,"step_timeout":120,"error_retry":2}`,
		"subagent":   `"subagent":{"max_concurrency":4,"exec_timeout":"5m","max_task_length":3000,"max_result_length":8000}`,
		"attachment": `"attachment":{"max_size":10,"max_total_size":50,"max_count":5,"allowed_types":[]}`,
		"rate_limit": `"rate_limit":{"enabled":true,"global_qps":0,"global_concurrency":0,"default_qps":50,"default_concurrency":20}`,
		"schedule":   `"schedule":{"enabled":false}`,
	}
}

// runtimeBodyWithout 拼一个缺少指定分区、其余分区均合法的请求体。
func runtimeBodyWithout(section string) string {
	secs := runtimeSections()
	delete(secs, section)
	return joinSections(secs)
}

// runtimeBodyWith 拼一个全分区合法的请求体，用 extra 覆盖同名分区。
// extra 必须是一个分区片段（形如 `"rate_limit":{...}`）：越界用例只想改坏一个字段，
// 其余分区必须合法，否则校验会先被别处拦下。缺分区用例用 runtimeBodyWithout。
func runtimeBodyWith(extra string) string {
	secs := runtimeSections()
	name := strings.TrimSpace(strings.SplitN(extra, ":", 2)[0])
	secs[strings.Trim(name, `"`)] = extra
	return joinSections(secs)
}

// joinSections 按分区名排序后拼成一个 JSON 对象，顺序稳定便于排查。
func joinSections(secs map[string]string) string {
	keys := make([]string, 0, len(secs))
	for k := range secs {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		parts = append(parts, secs[k])
	}
	return "{" + strings.Join(parts, ",") + "}"
}

// newMessageHandlerForTest 建一个带真实消息层与配置表的 handler。
// 返回消息层以便断言注册结果。
func newMessageHandlerForTest(t *testing.T) (*SettingHandler, *message.Layer) {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })

	boot := config.Bootstrap{Message: config.MessageBootstrap{QueueSize: 16, Workers: 1}}
	settings := setting.New(boot, settingdb.New(sqlxDB, dialect))
	models := llm.NewModelService(modeldb.New(sqlxDB, dialect))
	layer := message.NewLayer(config.MessageConfig{QueueSize: 16, Workers: 1}, logger.NewNop())
	h := NewSettingHandler(SettingHandlerDeps{
		Settings: settings,
		Models:   models,
		Registry: agent.NewRegistryForTest(4),
		Messages: layer,
		Log:      logger.NewNop(),
	})
	return h, layer
}

// TestSettingHandler_SendersRoundTrip 验证 webhook 参数经接口往返后保持，
// 且响应只含 webhook 这一个渠道。
func TestSettingHandler_SendersRoundTrip(t *testing.T) {
	h, _ := newMessageHandlerForTest(t)

	body := `{"senders":{"webhook":{"enabled":true,"url":"https://hook.test/a"}}}`
	rc := callJSON(h.PutSenders, consts.MethodPut, body, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("PutSenders status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}

	rc = callJSON(h.GetSenders, consts.MethodGet, "", nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("GetSenders status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var out types.SendersPayload
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if len(out.Senders) != 1 {
		t.Fatalf("Senders = %+v, 只应有 webhook 一个渠道", out.Senders)
	}
	got := out.Senders["webhook"]
	if !got.Enabled || got.URL != "https://hook.test/a" {
		t.Errorf("webhook = %+v", got)
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

// TestSettingHandler_PutSendersOverwritesURL 验证二次提交能改掉已存的地址。
func TestSettingHandler_PutSendersOverwritesURL(t *testing.T) {
	h, _ := newMessageHandlerForTest(t)

	first := `{"senders":{"webhook":{"enabled":true,"url":"https://hook.test/a"}}}`
	if rc := callJSON(h.PutSenders, consts.MethodPut, first, nil); rc.Response.StatusCode() != 200 {
		t.Fatalf("首次 status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	second := `{"senders":{"webhook":{"enabled":true,"url":"https://hook.test/b"}}}`
	if rc := callJSON(h.PutSenders, consts.MethodPut, second, nil); rc.Response.StatusCode() != 200 {
		t.Fatalf("二次 status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}

	rc := callJSON(h.GetSenders, consts.MethodGet, "", nil)
	var out types.SendersPayload
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if out.Senders["webhook"].URL != "https://hook.test/b" {
		t.Errorf("url = %q, want https://hook.test/b", out.Senders["webhook"].URL)
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

// TestSettingHandler_PutSendersRejectsUnknownChannel 验证已下线的渠道名被拒，
// 且不会注册进消息层。
func TestSettingHandler_PutSendersRejectsUnknownChannel(t *testing.T) {
	h, layer := newMessageHandlerForTest(t)

	for _, name := range []string{"email", "stdout"} {
		body := `{"senders":{"` + name + `":{"enabled":true,"url":"https://hook.test/a"}}}`
		rc := callJSON(h.PutSenders, consts.MethodPut, body, nil)
		if rc.Response.StatusCode() != 400 {
			t.Errorf("%s status=%d, want 400, body=%s", name, rc.Response.StatusCode(), rc.Response.Body())
		}
		if layer.ChannelEnabled(name) {
			t.Errorf("%s 不应在消息层可用", name)
		}
	}
}

// TestSettingHandler_PutRuntimeScheduleEnabled 验证调度开关经接口往返后保持。
func TestSettingHandler_PutRuntimeScheduleEnabled(t *testing.T) {
	h := newRuntimeHandlerForTest(t)

	rc := callJSON(h.GetRuntime, consts.MethodGet, "", nil)
	var cur types.RuntimeSettingsPayload
	if err := json.Unmarshal(rc.Response.Body(), &cur); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if cur.Schedule == nil {
		t.Fatal("GET 响应应总是携带 schedule 分区")
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
	if out.Schedule == nil || !out.Schedule.Enabled {
		t.Errorf("schedule.enabled 回读应为 true，得到 %+v", out.Schedule)
	}
}

// TestSettingHandler_PutRuntimeRejectsMissingSchedule 验证不带 schedule 分区的请求被拒绝，
// 否则旧客户端保存任意一项都会把调度工具静默关掉。
func TestSettingHandler_PutRuntimeRejectsMissingSchedule(t *testing.T) {
	h := newRuntimeHandlerForTest(t)

	// 带合法 rate_limit、不带 schedule
	body := runtimeBodyWithout("schedule")
	rc := callJSON(h.PutRuntime, consts.MethodPut, body, nil)
	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400，body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if s := bodyStatus(t, rc); s != "invalid_request" {
		t.Errorf("status = %q, want invalid_request", s)
	}
	if !strings.Contains(string(rc.Response.Body()), "schedule") {
		t.Errorf("响应应指明缺少 schedule 分区，得到 %s", rc.Response.Body())
	}
}
