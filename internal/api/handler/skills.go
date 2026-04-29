package handler

import (
	"context"

	"github.com/cloudwego/eino/adk/middlewares/skill"
	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/api/types"
)

// SkillsHandler handles GET /skills
type SkillsHandler struct {
	backend skill.Backend
}

// NewSkillsHandler creates a new skills handler
func NewSkillsHandler(backend skill.Backend) *SkillsHandler {
	return &SkillsHandler{backend: backend}
}

// Serve handles the skills request
func (h *SkillsHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	if h.backend == nil {
		rc.JSON(200, types.SkillsResponse{Skills: []types.SkillInfo{}, Total: 0})
		return
	}

	matters, err := h.backend.List(ctx)
	if err != nil {
		rc.JSON(500, types.SkillsResponse{Skills: []types.SkillInfo{}, Total: 0})
		return
	}

	skillInfos := make([]types.SkillInfo, len(matters))
	for i, m := range matters {
		skillInfos[i] = types.SkillInfo{
			Name:        m.Name,
			Description: m.Description,
		}
	}

	rc.JSON(200, types.SkillsResponse{
		Skills: skillInfos,
		Total:  len(skillInfos),
	})
}
