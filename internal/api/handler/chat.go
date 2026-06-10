package handler

import (
	"context"
	"encoding/base64"
	"fmt"
	"time"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"go.uber.org/zap"

	"io"

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
	subAgentRegistry  *agent.SubAgentRegistry
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
	subAgentReg *agent.SubAgentRegistry,
	attHandler *attachment.Handler,
	cfg config.Config,
	log *logger.Logger,
) *ChatHandler {
	return &ChatHandler{
		memory:            mem,
		runtimeState:      runtime,
		agentExecutor:     executor,
		mcpManager:        mcpMgr,
		subAgentRegistry:  subAgentReg,
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

	// 2.7. 提取 X-Agent-Name header（Solo 模式入口）
	// 不传或传 "groot" → 编排模式（task.AgentName 为空）
	// 传非主 Agent 名 → 校验注册表，未注册则 400
	requestedAgent := string(rc.GetHeader("X-Agent-Name"))
	if requestedAgent == agent.MainAgentName {
		requestedAgent = "" // 标准化：传主 Agent 名等价于不传
	}
	if requestedAgent != "" {
		if h.subAgentRegistry == nil {
			// 配置缺失：服务端 main.go 未注入 SubAgentRegistry。
			// 实际部署中不应发生（main 总会构建至少空 registry），出现即配置异常。
			// 用户视角仍是 400 unknown_agent，运维通过日志区分故障源。
			h.log.Error("X-Agent-Name 校验失败：SubAgentRegistry 未初始化",
				zap.String("requested_agent", requestedAgent))
			rc.JSON(400, utils.H{"status": "unknown_agent", "message": fmt.Sprintf("Unknown agent: %s", requestedAgent)})
			return
		}
		if _, ok := h.subAgentRegistry.Get(requestedAgent); !ok {
			rc.JSON(400, utils.H{"status": "unknown_agent", "message": fmt.Sprintf("Unknown agent: %s", requestedAgent)})
			return
		}
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
	_, err := h.runtimeState.Register(sessionID, chatID)
	if err != nil {
		// Register 返回错误表示有冲突（已有活跃对话）
		rc.JSON(409, utils.H{
			"status":     "chat_limit_exceeded",
			"message":    "该会话已有对话正在执行",
			"session_id": sessionID,
		})
		return
	}
	// 活跃状态在 agent goroutine 结束时清理

	// 9. 注册成功后，再创建 session（如果是新会话）
	if isNew {
		if err := h.memory.CreateSession(sessionID); err != nil {
			rc.JSON(500, utils.H{"status": "error", "message": "创建会话失败"})
			return
		}
	}

	// 10. 处理附件
	var multimodalContents []agent.MultimodalContent
	if len(req.Attachments) > 0 && h.attachmentHandler != nil {
		for _, att := range req.Attachments {
			switch att.Type {
			case "file", "image", "audio", "video":
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

				// 构建 MultimodalContent 传递给 LLM
				mc := agent.MultimodalContent{
					Type:       att.Type,
					Name:       att.Name,
					Base64Data: att.Content,
				}
				// file 类型：服务端先解码，把原文传给 LLM（避免 Base64 乱码）
				if att.Type == "file" {
					mc.DecodedContent = string(content)
				}
				multimodalContents = append(multimodalContents, mc)

			default:
				rc.JSON(400, utils.H{"status": "attachment_invalid_type", "message": "无效的附件类型: " + att.Type})
				return
			}
		}
	}

	// 11. 构建 Task 对象
	task := &agent.Task{
		ID:                 chatID,
		Instruction:        req.Instruction,
		Prompt:             req.Prompt,
		Status:             agent.StatusRunning,
		StartTime:          time.Now(),
		Steps:              []agent.StepRecord{},
		MultiModalContents: multimodalContents,
		Progress:           &agent.ProgressInfo{},
		Round:              round,
		HistoryMessages:    historyMessages,
		ModelName:          modelName,
		AgentName:          requestedAgent,
	}
	// 12. 设置 SSE 响应头
	rc.Response.Header.Set("X-Session-ID", sessionID)
	rc.Response.Header.Set("X-Chat-ID", chatID)
	rc.Response.Header.Set("Content-Type", "text/event-stream")
	rc.Response.Header.Set("Cache-Control", "no-cache")
	rc.Response.Header.Set("Connection", "keep-alive")
	rc.Response.ImmediateHeaderFlush = true

	// 13. 创建 pipe 用于流式 SSE
	pr, pw := io.Pipe()
	rc.SetBodyStream(pr, -1)

	// 14. 异步执行 Agent
	go func() {
		defer h.runtimeState.Delete(sessionID)
		defer pw.Close()

		sseWriter := agent.NewSSEWriter(pipeFlushWriter{pw}, sessionID, chatID, round)

		defer func() {
			if r := recover(); r != nil {
				h.log.Error(fmt.Sprintf("Agent 执行异常(panic): %v", r))
				task.Status = agent.StatusFailed
				sseWriter.WriteDone()
			}
		}()
		h.agentExecutor.Execute(ctx, sessionID, task, sseWriter)

		// 记录日志
		statusText := string(task.Status)
		if task.Status == agent.StatusCompleted {
			statusText = "完成对话"
		} else if task.Status == agent.StatusCancelled {
			statusText = "对话被取消"
		} else if task.Status == agent.StatusFailed {
			statusText = "对话失败"
		}
		h.log.Info(fmt.Sprintf("%s: session=%s, chat=%s, round=%d, isNew=%v", statusText, sessionID, chatID, round, isNew))
	}()
}

// pipeFlushWriter 将 io.PipeWriter 适配为 flushWriter，Flush 为空操作。
// HTTP chunked encoder 会在每个 chunk 后自动 flush。
type pipeFlushWriter struct {
	*io.PipeWriter
}

func (p pipeFlushWriter) Flush() error { return nil }

// Serve 实现 handler 接口，路由到 Handle
func (h *ChatHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	h.Handle(ctx, rc)
}