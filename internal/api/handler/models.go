package handler

import (
	"context"
	"errors"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/api/errbody"
	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/setting"
)

// ModelsHandler 模型配置管理（/web/models 系列端点）
type ModelsHandler struct {
	models   *llm.ModelService
	settings *setting.Settings
	log      *logger.Logger
}

func NewModelsHandler(models *llm.ModelService, settings *setting.Settings, log *logger.Logger) *ModelsHandler {
	return &ModelsHandler{models: models, settings: settings, log: log}
}

// ModelRequest 创建/更新模型的请求体（api_key 为空表示更新时保持原值）
type ModelRequest struct {
	Name                string   `json:"name"`
	Model               string   `json:"model"`
	BaseURL             string   `json:"base_url"`
	APIKey              string   `json:"api_key"`
	MaxCompletionTokens int      `json:"max_completion_tokens"`
	MaxContextTokens    int      `json:"max_context_tokens"`
	Temperature         float64  `json:"temperature"`
	TopP                float64  `json:"top_p"`
	FrequencyPenalty    float64  `json:"frequency_penalty"`
	PresencePenalty     float64  `json:"presence_penalty"`
	Seed                int      `json:"seed"`
	Stop                []string `json:"stop"`
	Thinking            bool     `json:"thinking"`
	Enabled             *bool    `json:"enabled"` // 省略视为 true，显式 false 才禁用
}

func (r *ModelRequest) toModel() *repo.Model {
	return &repo.Model{
		Name:                r.Name,
		Model:               r.Model,
		BaseURL:             r.BaseURL,
		APIKey:              r.APIKey,
		MaxCompletionTokens: r.MaxCompletionTokens,
		MaxContextTokens:    r.MaxContextTokens,
		Temperature:         r.Temperature,
		TopP:                r.TopP,
		FrequencyPenalty:    r.FrequencyPenalty,
		PresencePenalty:     r.PresencePenalty,
		Seed:                r.Seed,
		Stop:                r.Stop,
		Thinking:            r.Thinking,
		Enabled:             r.Enabled == nil || *r.Enabled,
	}
}

func toModelInfo(m *repo.Model) types.ModelInfo {
	stop := m.Stop
	if stop == nil {
		stop = []string{}
	}
	return types.ModelInfo{
		Name:                m.Name,
		Model:               m.Model,
		BaseURL:             m.BaseURL,
		APIKey:              llm.MaskAPIKey(m.APIKey),
		MaxCompletionTokens: m.MaxCompletionTokens,
		MaxContextTokens:    m.MaxContextTokens,
		Temperature:         m.Temperature,
		TopP:                m.TopP,
		FrequencyPenalty:    m.FrequencyPenalty,
		PresencePenalty:     m.PresencePenalty,
		Seed:                m.Seed,
		Stop:                stop,
		Thinking:            m.Thinking,
		DefaultTypes:        defaultTypes(m),
		Enabled:             m.Enabled,
	}
}

// defaultTypes 返回模型持有的默认类型标识，未持有时返回空切片（序列化为 []）
func defaultTypes(m *repo.Model) []string {
	out := []string{}
	for _, f := range repo.AllDefaultFlags {
		if m.Has(f) {
			out = append(out, f.String())
		}
	}
	return out
}

// parseDefaultType 解析查询参数 type；省略时为 chat，非法取值返回 false
func parseDefaultType(rc *app.RequestContext) (repo.DefaultFlag, bool) {
	s := strings.TrimSpace(rc.Query("type"))
	if s == "" {
		return repo.DefaultChat, true
	}
	return repo.ParseDefaultFlag(s)
}

func writeInvalidDefaultType(rc *app.RequestContext) {
	names := make([]string, 0, len(repo.AllDefaultFlags))
	for _, f := range repo.AllDefaultFlags {
		names = append(names, f.String())
	}
	errbody.Write(rc, 400, "type 取值无效，可选 "+strings.Join(names, "、"),
		errbody.TypeInvalidRequest, "type", errbody.CodeInvalidRequest)
}

// writeModelError 把 ModelService 错误映射为 HTTP 状态码与 OpenAI 错误信封。
// 未命中哨兵错误的内部错误只记日志，向客户端返回通用消息。
// 模型管理不在 OpenAI 标准内，未覆盖的细分错误保留原 code 字符串，type 归一为 invalid_request_error。
func (h *ModelsHandler) writeModelError(rc *app.RequestContext, err error) {
	status, code := 500, errbody.CodeInternalError
	typ := errbody.TypeAPI
	switch {
	case errors.Is(err, llm.ErrModelNotFound):
		status, code = 404, errbody.CodeModelNotFound
		typ = errbody.TypeInvalidRequest
	case errors.Is(err, llm.ErrNameExists):
		status, code = 409, "model_name_exists"
		typ = errbody.TypeInvalidRequest
	case errors.Is(err, llm.ErrDefaultProtected):
		status, code = 409, "default_model_protected"
		typ = errbody.TypeInvalidRequest
	case errors.Is(err, llm.ErrChatDefaultRequired):
		status, code = 400, "default_chat_required"
		typ = errbody.TypeInvalidRequest
	case errors.Is(err, llm.ErrModelDisabled):
		status, code = 400, "model_disabled"
		typ = errbody.TypeInvalidRequest
	case errors.Is(err, llm.ErrNoDefaultModel):
		status, code = 400, "no_default_model"
		typ = errbody.TypeInvalidRequest
	case errors.Is(err, llm.ErrInvalidModel):
		status, code = 400, "invalid_model_config"
		typ = errbody.TypeInvalidRequest
	default:
		h.log.Error("模型管理接口内部错误: " + err.Error())
		errbody.Write(rc, status, "内部错误", typ, "", code)
		return
	}
	errbody.Write(rc, status, err.Error(), typ, "", code)
}

// List 处理 GET /web/models
func (h *ModelsHandler) List(ctx context.Context, rc *app.RequestContext) {
	list, err := h.models.List(ctx)
	if err != nil {
		h.writeModelError(rc, err)
		return
	}
	models := make([]types.ModelInfo, 0, len(list))
	var defaults types.DefaultModels
	for _, m := range list {
		if m.Has(repo.DefaultChat) {
			defaults.Chat = m.Name
		}
		if m.Has(repo.DefaultASR) {
			defaults.ASR = m.Name
		}
		if m.Has(repo.DefaultVision) {
			defaults.Vision = m.Name
		}
		if m.Has(repo.DefaultTTS) {
			defaults.TTS = m.Name
		}
		models = append(models, toModelInfo(m))
	}
	rc.JSON(200, types.ModelsResponse{Models: models, Defaults: defaults, Total: len(models)})
}

// Create 处理 POST /web/models
func (h *ModelsHandler) Create(ctx context.Context, rc *app.RequestContext) {
	var req ModelRequest
	if err := rc.BindJSON(&req); err != nil {
		errbody.Write(rc, 400, "请求参数错误", errbody.TypeInvalidRequest, "", errbody.CodeInvalidRequest)
		return
	}
	m := req.toModel()
	if err := h.models.Create(ctx, m); err != nil {
		h.writeModelError(rc, err)
		return
	}
	// 回读库中实际状态（首个模型会被自动设为默认并强制启用，请求快照 m 不含这些信息）
	stored, err := h.models.GetStored(ctx, m.Name)
	if err != nil {
		h.log.Error("创建模型后回读失败: " + err.Error())
		rc.JSON(200, utils.H{"status": "ok"})
		return
	}
	rc.JSON(200, toModelInfo(stored))
}

// Update 处理 PUT /web/models/:name
func (h *ModelsHandler) Update(ctx context.Context, rc *app.RequestContext) {
	name := rc.Param("name")
	var req ModelRequest
	if err := rc.BindJSON(&req); err != nil {
		errbody.Write(rc, 400, "请求参数错误", errbody.TypeInvalidRequest, "", errbody.CodeInvalidRequest)
		return
	}
	m := req.toModel()
	if err := h.models.Update(ctx, name, m); err != nil {
		h.writeModelError(rc, err)
		return
	}
	h.syncVoiceModel(ctx, name, m)
	rc.JSON(200, utils.H{"status": "ok"})
}

// syncVoiceModel 在模型更新后让语音识别模型配置跟随变化：
// 模型被禁用或改名时，仍指向该模型的 voice.model 相应清空或改名；
// 同步失败只记录日志，模型变更已落库，不回滚也不改变成功响应。
func (h *ModelsHandler) syncVoiceModel(ctx context.Context, name string, m *repo.Model) {
	var err error
	switch {
	case !m.Enabled:
		err = h.settings.ClearVoiceModelIf(ctx, name)
	case m.Name != name:
		err = h.settings.RenameVoiceModelIf(ctx, name, m.Name)
	default:
		return
	}
	if err != nil {
		h.logVoiceSyncError(name, err)
	}
}

// logVoiceSyncError 记录语音识别模型配置同步失败。模型变更已落库，
// 同步失败不影响本次请求的结果，仅留日志供排查。
func (h *ModelsHandler) logVoiceSyncError(name string, err error) {
	h.log.Error("同步识别模型配置失败", zap.String("model", name), zap.Error(err))
}

// Delete 处理 DELETE /web/models/:name
func (h *ModelsHandler) Delete(ctx context.Context, rc *app.RequestContext) {
	name := rc.Param("name")
	if err := h.models.Delete(ctx, name); err != nil {
		h.writeModelError(rc, err)
		return
	}
	if err := h.settings.ClearVoiceModelIf(ctx, name); err != nil {
		h.logVoiceSyncError(name, err)
	}
	rc.JSON(200, utils.H{"status": "ok"})
}

// SetDefault 处理 PUT /web/models/:name/default?type=chat|asr|vision|tts（type 省略为 chat）
func (h *ModelsHandler) SetDefault(ctx context.Context, rc *app.RequestContext) {
	flag, ok := parseDefaultType(rc)
	if !ok {
		writeInvalidDefaultType(rc)
		return
	}
	if err := h.models.SetDefault(ctx, rc.Param("name"), flag); err != nil {
		h.writeModelError(rc, err)
		return
	}
	rc.JSON(200, utils.H{"status": "ok"})
}

// ClearDefault 处理 DELETE /web/models/:name/default?type=asr|vision|tts。
// type 为 chat（或省略）时由业务层返回 default_chat_required。
func (h *ModelsHandler) ClearDefault(ctx context.Context, rc *app.RequestContext) {
	flag, ok := parseDefaultType(rc)
	if !ok {
		writeInvalidDefaultType(rc)
		return
	}
	if err := h.models.ClearDefault(ctx, rc.Param("name"), flag); err != nil {
		h.writeModelError(rc, err)
		return
	}
	rc.JSON(200, utils.H{"status": "ok"})
}

// ModelTestRequest 连接测试请求体。api_key 为空且 name 非空时取库中已存 key。
type ModelTestRequest struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	APIKey  string `json:"api_key"`
	Model   string `json:"model"`
}

// Test 处理 POST /web/models/test
func (h *ModelsHandler) Test(ctx context.Context, rc *app.RequestContext) {
	var req ModelTestRequest
	if err := rc.BindJSON(&req); err != nil {
		errbody.Write(rc, 400, "请求参数错误", errbody.TypeInvalidRequest, "", errbody.CodeInvalidRequest)
		return
	}
	apiKey := config.ExpandEnv(req.APIKey)
	if apiKey == "" && req.Name != "" {
		stored, err := h.models.GetStored(ctx, req.Name)
		if err != nil {
			h.writeModelError(rc, err)
			return
		}
		apiKey = stored.APIKey
		if req.BaseURL == "" {
			req.BaseURL = stored.BaseURL
		}
		if req.Model == "" {
			req.Model = stored.Model
		}
	}
	if req.BaseURL == "" || apiKey == "" {
		errbody.Write(rc, 400, "base_url 和 api_key 不能为空", errbody.TypeInvalidRequest, "", errbody.CodeInvalidRequest)
		return
	}
	status, errMsg := llm.CheckConnection(&repo.Model{
		BaseURL: req.BaseURL,
		APIKey:  apiKey,
		Model:   req.Model,
	})
	rc.JSON(200, utils.H{"status": status, "message": errMsg})
}
