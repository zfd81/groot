package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/config"
)

// ModelsHandler handles GET /models
type ModelsHandler struct {
	config *config.Config
}

// NewModelsHandler creates a new models handler
func NewModelsHandler(cfg *config.Config) *ModelsHandler {
	return &ModelsHandler{config: cfg}
}

// Serve handles the models request
func (h *ModelsHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	models := make([]types.ModelInfo, 0, len(h.config.LLM.Models))
	for name, m := range h.config.LLM.Models {
		models = append(models, types.ModelInfo{
			Name:    name,
			Model:   m.Model,
			BaseURL: m.BaseURL,
		})
	}

	rc.JSON(200, types.ModelsResponse{
		Models:  models,
		Default: h.config.LLM.DefaultModel,
		Total:   len(models),
	})
}
