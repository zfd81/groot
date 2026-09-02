package handler

import (
	"context"
	"errors"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
)

// ModelsHandler 模型配置管理（/web/models 系列端点）
type ModelsHandler struct {
	models *llm.ModelService
	log    *logger.Logger
}

func NewModelsHandler(models *llm.ModelService, log *logger.Logger) *ModelsHandler {
	return &ModelsHandler{models: models, log: log}
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
		IsDefault:           m.IsDefault,
		Enabled:             m.Enabled,
	}
}

// writeModelError 把 ModelService 错误映射为 HTTP 状态码与错误码。
// 未命中哨兵错误的内部错误只记日志，向客户端返回通用消息。
func (h *ModelsHandler) writeModelError(rc *app.RequestContext, err error) {
	status, code := 500, "internal_error"
	switch {
	case errors.Is(err, llm.ErrModelNotFound):
		status, code = 404, "model_not_found"
	case errors.Is(err, llm.ErrNameExists):
		status, code = 409, "model_name_exists"
	case errors.Is(err, llm.ErrDefaultProtected):
		status, code = 409, "default_model_protected"
	case errors.Is(err, llm.ErrModelDisabled):
		status, code = 400, "model_disabled"
	case errors.Is(err, llm.ErrNoDefaultModel):
		status, code = 400, "no_default_model"
	case errors.Is(err, llm.ErrInvalidModel):
		status, code = 400, "invalid_model_config"
	default:
		h.log.Error("模型管理接口内部错误: " + err.Error())
		rc.JSON(status, utils.H{"status": code, "message": "内部错误"})
		return
	}
	rc.JSON(status, utils.H{"status": code, "message": err.Error()})
}

// List 处理 GET /web/models
func (h *ModelsHandler) List(ctx context.Context, rc *app.RequestContext) {
	list, err := h.models.List(ctx)
	if err != nil {
		h.writeModelError(rc, err)
		return
	}
	models := make([]types.ModelInfo, 0, len(list))
	defaultName := ""
	for _, m := range list {
		if m.IsDefault {
			defaultName = m.Name
		}
		models = append(models, toModelInfo(m))
	}
	rc.JSON(200, types.ModelsResponse{Models: models, Default: defaultName, Total: len(models)})
}

// Create 处理 POST /web/models
func (h *ModelsHandler) Create(ctx context.Context, rc *app.RequestContext) {
	var req ModelRequest
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求参数错误"})
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
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求参数错误"})
		return
	}
	m := req.toModel()
	if err := h.models.Update(ctx, name, m); err != nil {
		h.writeModelError(rc, err)
		return
	}
	rc.JSON(200, utils.H{"status": "ok"})
}

// Delete 处理 DELETE /web/models/:name
func (h *ModelsHandler) Delete(ctx context.Context, rc *app.RequestContext) {
	if err := h.models.Delete(ctx, rc.Param("name")); err != nil {
		h.writeModelError(rc, err)
		return
	}
	rc.JSON(200, utils.H{"status": "ok"})
}

// SetDefault 处理 PUT /web/models/:name/default
func (h *ModelsHandler) SetDefault(ctx context.Context, rc *app.RequestContext) {
	if err := h.models.SetDefault(ctx, rc.Param("name")); err != nil {
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
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求参数错误"})
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
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "base_url 和 api_key 不能为空"})
		return
	}
	status, errMsg := llm.CheckConnection(&repo.Model{
		BaseURL: req.BaseURL,
		APIKey:  apiKey,
		Model:   req.Model,
	})
	rc.JSON(200, utils.H{"status": status, "message": errMsg})
}
