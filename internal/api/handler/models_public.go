package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/api/errbody"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
)

// ModelsPublicHandler 对外模型列表接口（GET /models），响应体遵循 OpenAI 的列表格式。
// 与 Web 端的 ModelsHandler 分开：对外只暴露模型的逻辑名与创建时间，
// 上游地址、密钥、默认类型等部署细节不下发。
type ModelsPublicHandler struct {
	models *llm.ModelService
	log    *logger.Logger
}

func NewModelsPublicHandler(models *llm.ModelService, log *logger.Logger) *ModelsPublicHandler {
	return &ModelsPublicHandler{models: models, log: log}
}

// modelsListResponse 是 GET /models 的响应体。
type modelsListResponse struct {
	Object string      `json:"object"` // 固定 "list"
	Data   []modelCard `json:"data"`
}

// modelCard 是列表中的单个模型。
type modelCard struct {
	ID      string `json:"id"`       // repo.Model.Name（Groot 模型逻辑名）
	Object  string `json:"object"`   // 固定 "model"
	Created int64  `json:"created"`  // repo.Model.CreatedAt.Unix()
	OwnedBy string `json:"owned_by"` // 固定 "groot"
}

// Serve 处理 GET /models，只列出启用的模型。
func (h *ModelsPublicHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	// 服务未装配时按内部错误返回，避免 nil 指针恐慌把连接直接打断
	if h.models == nil {
		h.log.Error("模型列表接口缺少模型服务")
		errbody.Write(rc, 500, "内部错误", errbody.TypeAPI, "", errbody.CodeInternalError)
		return
	}

	list, err := h.models.List(ctx)
	if err != nil {
		h.log.Error("查询模型列表失败: "+err.Error(), zap.Error(err))
		errbody.Write(rc, 500, "内部错误", errbody.TypeAPI, "", errbody.CodeInternalError)
		return
	}

	cards := make([]modelCard, 0, len(list))
	for _, m := range list {
		if !m.Enabled {
			continue
		}
		cards = append(cards, modelCard{
			ID:      m.Name,
			Object:  "model",
			Created: m.CreatedAt.Unix(),
			OwnedBy: "groot",
		})
	}
	rc.JSON(200, modelsListResponse{Object: "list", Data: cards})
}
