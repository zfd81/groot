package handler

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/attachment"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/mcp"
	"github.com/zfd81/groot/internal/memory"
)

// ChatHandler 对话处理器
type ChatHandler struct {
	memory            *memory.Manager
	runtimeState      *agent.RuntimeState
	agentExecutor     *agent.Executor
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
	mcpMgr *mcp.Manager,
	attHandler *attachment.Handler,
	cfg config.Config,
	log *logger.Logger,
) *ChatHandler {
	return &ChatHandler{
		memory:            mem,
		runtimeState:      runtime,
		agentExecutor:     executor,
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
	Type    string `json:"type"`    // file/image
	Name    string `json:"name"`
	Content string `json:"content"` // Base64 content (for file/image)
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

	// 2.5. 提取 X-Model-Name header
	modelName := string(rc.GetHeader("X-Model-Name"))

	// 2.6. 验证模型名称
	if modelName != "" && !h.config.LLM.ValidateModel(modelName) {
		rc.JSON(400, utils.H{
			"status":  "invalid_model",
			"message": fmt.Sprintf("模型 '%s' 不存在", modelName),
		})
		return
	}

	// 3. 提取 X-Session-ID
	sessionID := string(rc.GetHeader("X-Session-ID"))

	// 4. 提前检查并发冲突：如果提供了 session_id，先检查是否已有活跃对话
	// 这避免了竞态条件：第二个请求可能在第一个请求 Register 之前到达
	if sessionID != "" && h.runtimeState.IsRunning(sessionID) {
		rc.JSON(409, utils.H{
			"status":     "chat_limit_exceeded",
			"message":    "该会话已有对话正在执行",
			"session_id": sessionID,
		})
		return
	}

	// 5. 附件校验（在会话处理之前）
	if len(req.Attachments) > 0 && h.attachmentHandler != nil {
		// 转换为 attachment.Attachment 格式
		attInput := make([]attachment.Attachment, len(req.Attachments))
		for i, att := range req.Attachments {
			attInput[i] = attachment.Attachment{
				Type:    att.Type,
				Name:    att.Name,
				Content: att.Content,
			}
		}
		if err := h.attachmentHandler.Validate(attInput); err != nil {
			// Check if it's an AttachmentError with specific code
			if attErr, ok := err.(*attachment.AttachmentError); ok {
				rc.JSON(400, utils.H{"status": attErr.Code, "message": attErr.Message})
			} else {
				rc.JSON(400, utils.H{"status": "attachment_validation_error", "message": err.Error()})
			}
			return
		}
	}

	// 6. 会话处理 - 先确定 session_id
	var isNew bool
	var round int
	var historyMessages []memory.Message

	if sessionID == "" || !h.memory.ExistsSession(sessionID) {
		// 新会话
		sessionID = memory.GenerateSessionID()
		isNew = true
		round = 1
		historyMessages = []memory.Message{}
	} else {
		// 继续会话
		isNew = false
		round = h.memory.GetRoundCount(sessionID) + 1
		var err error
		historyMessages, err = h.memory.GetContextMessages(sessionID, h.config.Memory.HistoryWindow)
		if err != nil {
			rc.JSON(500, utils.H{"status": "error", "message": "获取上下文失败"})
			return
		}
	}

	// 7. 生成 chat_id
	chatID := memory.GenerateChatID()

	// 8. 立即注册活跃状态 - 在创建 session 或获取历史之后立即执行
	// 这确保了同一 session 的并发请求只有一个能成功注册
	activeChat, err := h.runtimeState.Register(sessionID, chatID)
	if err != nil {
		// Register 返回错误表示有冲突（已有活跃对话）
		rc.JSON(409, utils.H{
			"status":     "chat_limit_exceeded",
			"message":    "该会话已有对话正在执行",
			"session_id": sessionID,
		})
		return
	}

	// 9. 注册成功后，再创建 session（如果是新会话）
	// 注意：此时 Register 已成功，后续步骤出错时需要 Delete
	if isNew {
		if err := h.memory.CreateSession(sessionID); err != nil {
			h.runtimeState.Delete(sessionID) // 创建失败时清理
			rc.JSON(500, utils.H{"status": "error", "message": "创建会话失败"})
			return
		}
	}

	// 10. 设置响应 Header
	rc.Response.Header.Set("X-Session-ID", sessionID)
	rc.Response.Header.Set("X-Chat-ID", chatID)
	rc.Response.Header.Set("Content-Type", "text/event-stream") // 直接设置，避免框架添加charset
	rc.Response.Header.Set("Cache-Control", "no-cache")
	rc.Response.Header.Set("Connection", "keep-alive")

	// 11. 创建 SSE Writer
	sseWriter := agent.NewSSEWriter(rc, sessionID, chatID, round)

	// 12. 处理附件
	var attachmentNames []string
	if len(req.Attachments) > 0 && h.attachmentHandler != nil {
		for _, att := range req.Attachments {
			switch att.Type {
			case "file", "image":
				if att.Content == "" {
					rc.JSON(400, utils.H{"status": "attachment_missing_content", "message": "附件缺少内容: " + att.Name})
					return
				}
				// Base64 解码
				content, err := base64.StdEncoding.DecodeString(att.Content)
				if err != nil {
					rc.JSON(400, utils.H{"status": "attachment_decode_error", "message": "附件解码失败: " + att.Name + ", error: " + err.Error()})
					return
				}

				// 保存附件
				_, err = h.memory.SaveAttachment(sessionID, att.Name, content)
				if err != nil {
					rc.JSON(500, utils.H{"status": "error", "message": "附件保存失败: " + att.Name})
					return
				}

				attachmentNames = append(attachmentNames, att.Name)

			default:
				rc.JSON(400, utils.H{"status": "attachment_invalid_type", "message": "无效的附件类型: " + att.Type})
				return
			}
		}
	}

	// 13. 构建 Task 对象
	task := &agent.Task{
		ID:              chatID,
		Instruction:     req.Instruction,
		Prompt:          req.Prompt,
		Status:          agent.StatusRunning,
		StartTime:       time.Now(),
		Steps:           []agent.StepRecord{},
		Attachments:     attachmentNames,
		Progress:        &agent.ProgressInfo{},
		Round:           round,
		HistoryMessages: historyMessages,
		ModelName:       modelName,
	}

	// 14. 执行 Agent (同步执行以保持 SSE 连接)
	h.agentExecutor.Execute(sessionID, task, sseWriter, activeChat.CancelCh)

	// 15. 清理活跃状态 (确保后续请求可以继续)
	h.runtimeState.Delete(sessionID)

	// 记录日志
	h.log.Info(fmt.Sprintf("完成对话: session=%s, chat=%s, round=%d, isNew=%v", sessionID, chatID, round, isNew))
}

// Serve 实现 handler 接口，路由到 Handle
func (h *ChatHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	h.Handle(ctx, rc)
}