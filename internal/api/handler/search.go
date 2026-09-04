package handler

import (
	"context"
	"strconv"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

	"github.com/zfd81/groot/internal/memory"
)

// SearchSessions 处理 GET /sess/search?q=<关键词>&limit=20
// 在历史对话（主 Agent 已完成轮次）的 instruction/result 中模糊搜索，返回轮次级结果。
func (h *SessionHandler) SearchSessions(ctx context.Context, rc *app.RequestContext) {
	q := strings.TrimSpace(rc.Query("q"))

	limit := 20
	if l := rc.Query("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil && parsed > 0 && parsed <= 50 {
			limit = parsed
		}
	}

	// q 为空直接返回空结果（不视为错误）
	if q == "" {
		rc.JSON(200, utils.H{"status": "success", "results": []memory.SearchResult{}})
		return
	}

	// 与 /chat 端点一致：调用方可用 X-User-ID 标识用户；为空时不按用户过滤
	userID := string(rc.GetHeader("X-User-ID"))

	results, err := h.memory.Search(userID, q, limit)
	if err != nil {
		rc.SetContentType("application/json")
		rc.SetStatusCode(500)
		rc.Write([]byte(`{"status":"error","message":"搜索失败"}`))
		return
	}
	rc.JSON(200, utils.H{"status": "success", "results": results})
}
