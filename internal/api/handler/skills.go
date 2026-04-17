package handler

import (
	"context"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/api"
	"github.com/zfd81/groot/internal/skill"
)

// SkillsHandler handles GET /skills
type SkillsHandler struct {
	skillRegistry *skill.Registry
}

// NewSkillsHandler creates a new skills handler
func NewSkillsHandler(skills *skill.Registry) *SkillsHandler {
	return &SkillsHandler{skillRegistry: skills}
}

// Serve handles the skills request
func (h *SkillsHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	skills := h.skillRegistry.List()

	skillInfos := make([]api.SkillInfo, len(skills))
	for i, s := range skills {
		skillInfos[i] = api.SkillInfo{
			Name:        s.Name,
			Description: s.Description,
		}
	}

	resp := api.SkillsResponse{
		Skills: skillInfos,
		Total:  len(skillInfos),
	}

	rc.JSON(200, resp)
}
