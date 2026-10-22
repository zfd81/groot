package handler

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/repo"
)

// 删除会话时的等待时间；包级变量以便测试缩短。
var (
	// deleteWaitTimeout 本机有活跃对话时，取消后等待执行协程退出的最长时间
	deleteWaitTimeout = 10 * time.Second
	// deleteBroadcastGrace 向其他存活节点广播中止后，留给它们退出对话的宽限时间
	deleteBroadcastGrace = 5 * time.Second
)

// SessionHandler 会话处理器
type SessionHandler struct {
	memory    *memory.Manager
	runtime   *agent.RuntimeState
	canceller agent.ChatCanceller
}

// NewSessionHandler 创建会话处理器
func NewSessionHandler(mem *memory.Manager, rt *agent.RuntimeState, canceller agent.ChatCanceller) *SessionHandler {
	return &SessionHandler{memory: mem, runtime: rt, canceller: canceller}
}

// GetSession 处理 GET /sess/{sid}
func (h *SessionHandler) GetSession(ctx context.Context, rc *app.RequestContext) {
	sessionID := rc.Param("sid")

	if sessionID == "" {
		rc.SetContentType("application/json")
		rc.SetStatusCode(400)
		rc.Write([]byte(`{"status":"invalid_request","message":"session_id 参数缺失"}`))
		return
	}

	owner, ok := requireOwner(rc)
	if !ok {
		return
	}
	// 非本人（他租户/他用户）的会话与不存在的会话一律 404，响应体保持一致，不可区分
	if !ownsOrAdmin(h.memory, owner, sessionID) {
		rc.SetContentType("application/json")
		rc.SetStatusCode(404)
		rc.Write([]byte(`{"status":"session_not_found","message":"会话不存在"}`))
		return
	}

	info, err := h.memory.GetSessionInfo(sessionID)
	if err != nil {
		rc.SetContentType("application/json")
		rc.SetStatusCode(404)
		rc.Write([]byte(`{"status":"session_not_found","message":"会话不存在"}`))
		return
	}

	history, err := h.memory.GetHistory(sessionID)
	if err != nil {
		rc.SetContentType("application/json")
		rc.SetStatusCode(500)
		rc.Write([]byte(`{"status":"error","message":"获取历史失败"}`))
		return
	}

	rc.JSON(200, utils.H{
		"status":     "success",
		"session_id": sessionID,
		"session": utils.H{
			"session_id":     info.SessionID,
			"title":          info.Title,
			"created_at":     info.CreatedAt,
			"round_count":    info.RoundCount,
			"path":           info.Path,
			"last_active_at": getLastActiveTime(history),
		},
		"history": history,
	})
}

// getLastActiveTime returns the timestamp of the last message
func getLastActiveTime(history *memory.History) string {
	if len(history.Messages) == 0 {
		return ""
	}
	return history.Messages[len(history.Messages)-1].Timestamp.Format("2006-01-02T15:04:05Z")
}

// DeleteSession 处理 DELETE /sess/{sid}
//
// 流程：归属校验 → 若本机有活跃对话则取消并等待其退出（超时 409）→ 否则在集群存在
// 其他存活节点时广播中止并等待宽限期 → 以 chatID "delete" 占位（阻止并发续聊，失败即 409）→ 删除会话。
func (h *SessionHandler) DeleteSession(ctx context.Context, rc *app.RequestContext) {
	sessionID := rc.Param("sid")

	if sessionID == "" {
		rc.SetContentType("application/json")
		rc.SetStatusCode(400)
		rc.Write([]byte(`{"status":"invalid_request","message":"session_id 参数缺失"}`))
		return
	}

	owner, ok := requireOwner(rc)
	if !ok {
		return
	}
	// 非本人的会话与不存在的会话一律 404，响应体与 GetSession 保持一致
	if !ownsOrAdmin(h.memory, owner, sessionID) {
		rc.SetContentType("application/json")
		rc.SetStatusCode(404)
		rc.Write([]byte(`{"status":"session_not_found","message":"会话不存在"}`))
		return
	}

	log := logger.FromContext(ctx)
	if active, running := h.runtime.Get(sessionID); running {
		// 本机正在执行：取消该会话下当前活跃的任意对话（chatID 为空匹配任意对话，
		// 避免 Get 观察到的对话已结束、续聊刚注册时取消落空而白等 Wait 超时），再等待执行协程退出
		h.runtime.Cancel(sessionID, "")
		if !h.runtime.Wait(sessionID, deleteWaitTimeout) {
			log.Warn("删除会话：等待活跃对话退出超时",
				zap.String("session_id", sessionID), zap.String("chat_id", active.ChatID))
			writeChatStillRunning(rc, sessionID)
			return
		}
	} else if h.canceller.OthersAlive(ctx) {
		// 本机无活跃对话且集群中有其他存活节点：可能在那里执行，广播中止后等待宽限期
		if _, err := h.canceller.Cancel(ctx, sessionID, ""); err != nil {
			log.Warn("删除会话：广播中止请求失败",
				zap.String("session_id", sessionID), zap.Error(err))
		}
		time.Sleep(deleteBroadcastGrace)
	}

	// 占位：阻止删除期间续聊；若续聊已抢先注册则视为仍在执行
	if _, err := h.runtime.Register(ctx, sessionID, "delete"); err != nil {
		writeChatStillRunning(rc, sessionID)
		return
	}
	defer h.runtime.Delete(sessionID)

	if err := h.memory.DeleteSession(sessionID); err != nil {
		log.Error("删除会话失败", zap.String("session_id", sessionID), zap.Error(err))
		rc.SetContentType("application/json")
		rc.SetStatusCode(500)
		rc.Write([]byte(`{"status":"error","message":"删除会话失败"}`))
		return
	}

	rc.JSON(200, utils.H{
		"status":     "success",
		"session_id": sessionID,
	})
}

// renameSessionRequest 是 PATCH /sess/{sid} 的请求体。
type renameSessionRequest struct {
	Title string `json:"title"`
}

// RenameSession 处理 PATCH /sess/{sid}
//
// 流程：参数校验（sid、请求体、标题去首尾空白后长度 1..MaxTitleRunes）→ 归属校验 → 更新标题。
// 只更新 title，不影响会话的 updated_at 与列表排序。
func (h *SessionHandler) RenameSession(ctx context.Context, rc *app.RequestContext) {
	sessionID := rc.Param("sid")

	if sessionID == "" {
		rc.SetContentType("application/json")
		rc.SetStatusCode(400)
		rc.Write([]byte(`{"status":"invalid_request","message":"session_id 参数缺失"}`))
		return
	}

	var req renameSessionRequest
	if err := rc.BindJSON(&req); err != nil {
		rc.SetContentType("application/json")
		rc.SetStatusCode(400)
		rc.Write([]byte(`{"status":"invalid_request","message":"请求参数错误"}`))
		return
	}

	title := strings.TrimSpace(req.Title)
	if n := utf8.RuneCountInString(title); n < 1 || n > memory.MaxTitleRunes {
		rc.SetContentType("application/json")
		rc.SetStatusCode(400)
		rc.Write([]byte(`{"status":"invalid_request","message":"标题不能为空，且长度不能超过 100 个字符"}`))
		return
	}

	owner, ok := requireOwner(rc)
	if !ok {
		return
	}
	// 非本人的会话与不存在的会话一律 404，响应体与 GetSession 保持一致
	if !ownsOrAdmin(h.memory, owner, sessionID) {
		rc.SetContentType("application/json")
		rc.SetStatusCode(404)
		rc.Write([]byte(`{"status":"session_not_found","message":"会话不存在"}`))
		return
	}

	if err := h.memory.RenameSession(sessionID, title); err != nil {
		if errors.Is(err, repo.ErrNotFound) {
			rc.SetContentType("application/json")
			rc.SetStatusCode(404)
			rc.Write([]byte(`{"status":"session_not_found","message":"会话不存在"}`))
			return
		}
		logger.FromContext(ctx).Error("重命名会话失败", zap.String("session_id", sessionID), zap.Error(err))
		rc.SetContentType("application/json")
		rc.SetStatusCode(500)
		rc.Write([]byte(`{"status":"error","message":"重命名失败"}`))
		return
	}

	rc.JSON(200, utils.H{
		"status":     "success",
		"session_id": sessionID,
		"title":      title,
	})
}

// writeChatStillRunning 写出 409 chat_still_running 响应
func writeChatStillRunning(rc *app.RequestContext, sessionID string) {
	rc.JSON(409, utils.H{
		"status":     "chat_still_running",
		"session_id": sessionID,
		"message":    "对话仍在执行，请稍后重试",
	})
}

// ListSessions 处理 GET /sess/history
func (h *SessionHandler) ListSessions(ctx context.Context, rc *app.RequestContext) {
	limit := rc.Query("limit")
	offset := rc.Query("offset")

	limitInt := 20
	offsetInt := 0

	if limit != "" {
		if parsed, err := strconv.Atoi(limit); err == nil && parsed > 0 && parsed <= 100 {
			limitInt = parsed
		}
	}
	if offset != "" {
		if parsed, err := strconv.Atoi(offset); err == nil && parsed >= 0 {
			offsetInt = parsed
		}
	}

	owner, ok := requireOwner(rc)
	if !ok {
		return
	}

	sessions, total, err := h.memory.ListSessions(owner, limitInt, offsetInt)
	if err != nil {
		rc.SetContentType("application/json")
		rc.SetStatusCode(500)
		rc.Write([]byte(`{"status":"error","message":"查询失败"}`))
		return
	}

	rc.JSON(200, utils.H{
		"status":   "success",
		"total":    total,
		"limit":    limitInt,
		"offset":   offsetInt,
		"sessions": sessions,
	})
}

// Serve 实现 handler 接口 - 默认路由到 ListSessions
func (h *SessionHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	h.ListSessions(ctx, rc)
}
