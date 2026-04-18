package handler

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/skill"
	"github.com/zfd81/groot/internal/mcp"
	"github.com/zfd81/groot/internal/attachment"
)

// ChatHandler 对话处理器
type ChatHandler struct {
	memory            *memory.Manager
	runtimeState      *agent.RuntimeState
	agentExecutor     *agent.Executor
	skillRegistry     *skill.Registry
	mcpManager        *mcp.Manager
	attachmentHandler *attachment.Handler
	config            config.Config
	log               *logger.Logger
}

// NewChatHandler 创建对话处理器
func NewChatHandler(
	mem *memory.Manager,
	runtime *agent.RuntimeState,
	executor *agent.Executor,
	skills *skill.Registry,
	mcpMgr *mcp.Manager,
	attHandler *attachment.Handler,
	cfg config.Config,
	log *logger.Logger,
) *ChatHandler {
	return &ChatHandler{
		memory:            mem,
		runtimeState:      runtime,
		agentExecutor:     executor,
		skillRegistry:     skills,
		mcpManager:        mcpMgr,
		attachmentHandler: attHandler,
		config:            cfg,
		log:               log,
	}
}

// ChatRequest 对话请求
type ChatRequest struct {
	Instruction string        `json:"instruction"`
	Prompt      string        `json:"prompt,omitempty"`
	Attachments []Attachment  `json:"attachments,omitempty"`
}

// Attachment 附件
type Attachment struct {
	Type    string `json:"type"`    // file/url
	Name    string `json:"name"`
	Content string `json:"content"` // Base64 content or URL
}

// Handle 处理 POST /chat 请求
func (h *ChatHandler) Handle(ctx context.Context, rc *app.RequestContext) {
	// 1. 解析请求
	var req ChatRequest
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求参数错误"})
		return
	}

	// 2. 检查 instruction 是否为空
	if req.Instruction == "" {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "instruction 不能为空"})
		return
	}

	// 3. 提取 X-Session-ID
	sessionID := string(rc.GetHeader("X-Session-ID"))

	// 4. 会话处理
	var isNew bool
	var round int
	var historyMessages []memory.Message

	if sessionID == "" || !h.memory.ExistsSession(sessionID) {
		// 新会话
		sessionID = memory.GenerateSessionID()
		if err := h.memory.CreateSession(sessionID); err != nil {
			rc.JSON(500, utils.H{"status": "error", "message": "创建会话失败"})
			return
		}
		isNew = true
		round = 1
		historyMessages = []memory.Message{}
	} else {
		// 继续会话 - 检查并发
		if h.runtimeState.IsRunning(sessionID) {
			rc.JSON(409, utils.H{
				"status":  "chat_limit_exceeded",
				"message": "该会话已有对话正在执行",
			})
			return
		}
		isNew = false
		history, err := h.memory.GetHistory(sessionID)
		if err != nil {
			rc.JSON(500, utils.H{"status": "error", "message": "获取历史失败"})
			return
		}
		historyMessages = history.Messages
		round = len(historyMessages) + 1
	}

	// 5. 生成 chat_id
	chatID := memory.GenerateChatID()

	// 6. 注册活跃状态
	activeChat, err := h.runtimeState.Register(sessionID, chatID)
	if err != nil {
		rc.JSON(409, utils.H{"status": "error", "message": err.Error()})
		return
	}

	// 7. 设置响应 Header
	rc.Response.Header.Set("X-Session-ID", sessionID)
	rc.Response.Header.Set("X-Chat-ID", chatID)
	rc.SetContentType("text/event-stream")
	rc.Response.Header.Set("Cache-Control", "no-cache")
	rc.Response.Header.Set("Connection", "keep-alive")

	// 8. 创建 SSE Writer
	sseWriter := agent.NewSSEWriter(rc)

	// 9. 处理附件
	var attachmentPaths []memory.AttachmentPath
	if len(req.Attachments) > 0 && h.attachmentHandler != nil {
		for _, att := range req.Attachments {
			if att.Type == "file" && att.Content != "" {
				// Base64 解码
				content, err := base64.StdEncoding.DecodeString(att.Content)
				if err != nil {
					h.log.Error("附件解码失败: " + att.Name + ", error: " + err.Error())
					continue
				}

				// 保存附件
				path, err := h.memory.SaveAttachment(sessionID, att.Name, content)
				if err != nil {
					h.log.Error("附件保存失败: " + att.Name + ", error: " + err.Error())
					continue
				}

				attachmentPaths = append(attachmentPaths, memory.AttachmentPath{
					OriginalName: att.Name,
					Type:         att.Type,
					FullPath:     path,
					RelativePath: path,
					Size:         int64(len(content)),
					ContentType:  "application/octet-stream",
				})
			}
		}
	}

	// 10. 推送 intent 事件
	sseWriter.WriteIntent(round)

	// 11. 构建 Task 对象
	task := &agent.Task{
		ID:              chatID,
		Instruction:     req.Instruction,
		Prompt:          req.Prompt,
		Status:          agent.StatusRunning,
		StartTime:       time.Now(),
		Steps:           []agent.StepRecord{},
		Progress:        &agent.ProgressInfo{},
		Round:           round,
		HistoryMessages: historyMessages,
	}

	// 转换附件
	if len(attachmentPaths) > 0 {
		for _, ap := range attachmentPaths {
			task.Attachments = append(task.Attachments, agent.Attachment{
				Type:    ap.Type,
				Name:    ap.OriginalName,
				Content: ap.FullPath,
			})
		}
	}

	// 12. 执行 Agent
	go h.agentExecutor.Execute(sessionID, task, sseWriter, activeChat.CancelCh)

	// 记录日志
	h.log.Info(fmt.Sprintf("开始对话: session=%s, chat=%s, round=%d, isNew=%v", sessionID, chatID, round, isNew))
}

// Serve 实现 handler 接口，路由到 Handle
func (h *ChatHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	h.Handle(ctx, rc)
}