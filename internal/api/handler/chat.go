package handler

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"go.uber.org/zap"

	"github.com/cloudwego/hertz/pkg/app"

	"github.com/zfd81/groot/internal/agent"
	"github.com/zfd81/groot/internal/api/errbody"
	"github.com/zfd81/groot/internal/api/middleware"
	"github.com/zfd81/groot/internal/attachment"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/mcp"
	"github.com/zfd81/groot/internal/memory"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/setting"
)

// taskExecutor 是对话执行依赖，*agent.Executor 满足它。
// 抽成接口是为了 handler 单元测试能注入替身。做法与 schedule.Runner 的 taskExecutor 一致。
type taskExecutor interface {
	Execute(ctx context.Context, sessionID string, task *agent.Task, events agent.EventWriter)
}

// ChatHandler 对话处理器
type ChatHandler struct {
	memory           *memory.Manager
	runtimeState     *agent.RuntimeState
	agentExecutor    taskExecutor
	mcpManager       *mcp.Manager
	subAgentRegistry *agent.SubAgentRegistry
	models           *llm.ModelService
	settings         *setting.Settings
	log              *logger.Logger
}

// NewChatHandler 创建对话处理器
func NewChatHandler(
	mem *memory.Manager,
	runtime *agent.RuntimeState,
	executor *agent.Executor,
	mcpMgr *mcp.Manager,
	subAgentReg *agent.SubAgentRegistry,
	models *llm.ModelService,
	settings *setting.Settings,
	log *logger.Logger,
) *ChatHandler {
	return &ChatHandler{
		memory:           mem,
		runtimeState:     runtime,
		agentExecutor:    executor,
		mcpManager:       mcpMgr,
		subAgentRegistry: subAgentReg,
		models:           models,
		settings:         settings,
		log:              log,
	}
}

// Handle 处理 POST /chat/completions 请求。步骤顺序固定：请求校验先于模型解析，
// 模型解析先于 Agent 校验，身份与归属检查先于会话状态读写。
func (h *ChatHandler) Handle(ctx context.Context, rc *app.RequestContext) {
	// 1. 解析请求体：JSON 结构、消息角色、内容片段等静态校验都在 parseChatRequest 内完成
	body, err := rc.Body()
	if err != nil {
		errbody.Write(rc, 400, "读取请求体失败："+err.Error(), errbody.TypeInvalidRequest, "", errbody.CodeInvalidRequest)
		return
	}
	p, rerr := parseChatRequest(body)
	if rerr != nil {
		errbody.Write(rc, rerr.Status, rerr.Message, rerr.Type, rerr.Param, rerr.Code)
		return
	}

	// 2. 解析模型：显式模型名优先，缺省取默认对话模型。
	// 只有「请求要用的模型不可用」（不存在、已禁用、未配置默认模型）才是调用方的 400；
	// 其它错误（如配置库故障）是服务端故障，返回 500 且不回传原始错误串。
	mdl, err := h.models.Resolve(ctx, p.Model, repo.DefaultChat)
	if err != nil {
		if errors.Is(err, llm.ErrModelNotFound) ||
			errors.Is(err, llm.ErrModelDisabled) || errors.Is(err, llm.ErrNoDefaultModel) {
			errbody.Write(rc, 400, err.Error(), errbody.TypeInvalidRequest, "model", errbody.CodeModelNotFound)
			return
		}
		h.log.Error("解析对话模型失败", zap.String("model", p.Model), zap.Error(err))
		errbody.Write(rc, 500, "解析模型配置失败", errbody.TypeAPI, "", errbody.CodeInternalError)
		return
	}

	// 3. 子 Agent 校验：agent_name 等于主 Agent 名时归一为空（等价于不指定，走编排模式）；
	// 非空时必须在注册表中存在。
	agentName := p.AgentName
	if agentName == agent.MainAgentName {
		agentName = ""
	}
	if agentName != "" {
		if h.subAgentRegistry == nil {
			// 配置缺失：main 总会注入至少空 registry，走到这里说明服务端配置异常。
			// 对调用方仍是 400 unknown_agent，运维通过日志区分故障源。
			h.log.Error("agent_name 校验失败：SubAgentRegistry 未初始化", zap.String("agent_name", agentName))
			errbody.Write(rc, 400, "Unknown agent: "+agentName,
				errbody.TypeInvalidRequest, "agent_name", errbody.CodeUnknownAgent)
			return
		}
		if _, ok := h.subAgentRegistry.Get(agentName); !ok {
			errbody.Write(rc, 400, "Unknown agent: "+agentName,
				errbody.TypeInvalidRequest, "agent_name", errbody.CodeUnknownAgent)
			return
		}
	}

	// 4. 归属解析：Web 调用者为管理员（owner 为 nil），API Key 调用者必须带租户与用户。
	// 本接口的 user 按 OpenAI 标准放在请求体里，因此不能复用要求请求头必须带 user 的
	// requireOwner，而是在这里按「请求体优先、请求头回落」的顺序解析。
	owner, tenantID, userID, ok := resolveChatOwner(rc, p.User)
	if !ok {
		return
	}

	// 5. 续聊归属校验：会话不存在或不属于当前调用者时视为未传 session_id，静默新建会话，
	// 不返回 404，避免非归属方借此探知会话是否存在。
	sessionID := p.SessionID
	if sessionID != "" && !ownsOrAdmin(h.memory, owner, sessionID) {
		sessionID = ""
	}

	// 6. 并发预检：先于 Register 拦一次，缩小并发请求的竞态窗口。
	// 响应体不带 session_id：归属校验之后这里的会话一定属于调用方，无需回显。
	if sessionID != "" && h.runtimeState.IsRunning(sessionID) {
		errbody.Write(rc, 409, "该会话已有对话正在执行",
			errbody.TypeInvalidRequest, "", errbody.CodeChatLimitExceeded)
		return
	}

	// 7. 运行时配置：本次请求读一次，附件校验与历史窗口共用同一份快照。
	// 读取失败退回 YAML 值，配置表故障不阻断对话。
	rt, rtErr := h.settings.Runtime(ctx)
	if rtErr != nil {
		h.log.Error("读取运行时配置失败，本次请求使用 YAML 默认值", zap.Error(rtErr))
		rt = h.settings.RuntimeStatic()
	}

	// 8. 附件校验：数量、体积、类型白名单依赖运行时配置，必须在会话状态变更之前完成。
	if len(p.Attachments) > 0 {
		atts := make([]attachment.Attachment, 0, len(p.Attachments))
		for _, a := range p.Attachments {
			// agent.MultimodalContent 的正文是 Base64Data，附件校验的「内容」即它的原文
			atts = append(atts, attachment.Attachment{Type: a.Type, Name: a.Name, Content: a.Base64Data})
		}
		if err := attachment.NewHandler(rt.Attachment).Validate(atts); err != nil {
			var attErr *attachment.AttachmentError
			if errors.As(err, &attErr) {
				code := attachmentErrorCode(attErr.Code)
				errbody.Write(rc, attachmentStatus(code), attErr.Message, errbody.TypeInvalidRequest, "messages", code)
			} else {
				errbody.Write(rc, 400, err.Error(), errbody.TypeInvalidRequest, "messages", errbody.CodeInvalidRequest)
			}
			return
		}
	}

	// 9. 会话与历史：新建会话轮次为 1；续接时取服务端历史，受历史窗口与上下文 token 预算约束。
	var (
		isNew           bool
		round           int
		historyMessages []memory.Message
	)
	if sessionID == "" {
		sessionID = memory.GenerateSessionID()
		isNew = true
		round = 1
		historyMessages = []memory.Message{}
	} else {
		round = h.memory.GetRoundCount(sessionID) + 1
		historyMessages, err = h.memory.GetContextMessagesWithTokenLimit(
			sessionID,
			rt.Memory.HistoryWindow,
			mdl.MaxContextTokens,
		)
		if err != nil {
			h.log.Error("获取会话上下文失败", zap.String("session_id", sessionID), zap.Error(err))
			errbody.Write(rc, 500, "获取上下文失败", errbody.TypeAPI, "", errbody.CodeInternalError)
			return
		}
	}

	// 10. 注册活跃状态：同一 session 的并发请求只有一个能注册成功。
	chatID := memory.GenerateChatID()
	active, err := h.runtimeState.Register(ctx, sessionID, chatID)
	if err != nil {
		errbody.Write(rc, 409, "该会话已有对话正在执行",
			errbody.TypeInvalidRequest, "", errbody.CodeChatLimitExceeded)
		return
	}
	// 活跃状态在 agent goroutine 结束时清理；从这里到启动执行之间的每条失败返回
	// 都必须先 Delete，否则该会话会一直表现为「正在运行」，后续 Wait 只能等到超时。

	// 11. 新建会话落库。注册成功后再创建：CreateSession 失败时状态已注册，需回滚。
	if isNew {
		if err := h.memory.CreateSession(sessionID, tenantID, userID, memory.TruncateTitle(p.Title)); err != nil {
			h.runtimeState.Delete(sessionID)
			h.log.Error("创建会话失败", zap.String("session_id", sessionID), zap.Error(err))
			errbody.Write(rc, 500, "创建会话失败", errbody.TypeAPI, "", errbody.CodeInternalError)
			return
		}
	}

	// 12. 组装任务
	task := &agent.Task{
		ID:                 chatID,
		Instruction:        p.Instruction,
		Prompt:             p.SystemPrompt,
		Status:             agent.StatusRunning,
		StartTime:          time.Now(),
		Steps:              []agent.StepRecord{},
		MultiModalContents: p.Attachments,
		Caller:             middleware.GetCaller(rc),
		Progress:           &agent.ProgressInfo{},
		Round:              round,
		HistoryMessages:    historyMessages,
		CallerHistory:      p.CallerHistory,
		ModelName:          mdl.Name,
		AgentName:          agentName,
	}

	sessionLog := h.log.With(
		zap.String("session_id", sessionID),
		zap.String("tenant_id", tenantID),
		zap.String("user_id", userID),
	)

	if p.Stream {
		// 13. 流式：SSE 直出。响应头不设置 X-Session-ID / X-Chat-ID，
		// 会话标识由每个 chunk 的 session_id 字段承载。
		rc.Response.Header.Set("Content-Type", "text/event-stream")
		rc.Response.Header.Set("Cache-Control", "no-cache")
		rc.Response.Header.Set("Connection", "keep-alive")
		rc.Response.ImmediateHeaderFlush = true

		pr, pw := io.Pipe()
		rc.SetBodyStream(pr, -1)

		go func() {
			defer h.runtimeState.Delete(sessionID)
			defer pw.Close()

			events := agent.NewSSEWriter(pipeFlushWriter{pw}, sessionID, chatID, round,
				mdl.Name, task.StartTime.Unix(), p.IncludeUsage)

			defer func() {
				if r := recover(); r != nil {
					sessionLog.Error(fmt.Sprintf("Agent 执行异常(panic): %v", r))
					task.Status = agent.StatusFailed
					// panic 也必须以 error 事件到达客户端，再紧跟 [DONE]，否则表现为静默空回复
					events.WriteError("", fmt.Sprintf("Agent 执行异常: %v", r))
					events.WriteDone()
				}
			}()
			h.agentExecutor.Execute(active.Context(), sessionID, task, events)

			sessionLog.Info(fmt.Sprintf("%s: chat=%s, round=%d, isNew=%v",
				taskStatusText(task.Status), chatID, round, isNew))
		}()
		return
	}

	// 14. 非流式：事件聚合成一次性响应。执行放在 goroutine 中、主协程等待，
	// 保留 active.Context() 的取消语义（调用方停止生成时执行可被中止）。
	agg := agent.NewAggregator()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer h.runtimeState.Delete(sessionID)
		defer func() {
			if r := recover(); r != nil {
				task.Status = agent.StatusFailed
				// panic 同样要落到响应的 message.error，否则调用方只能看到空回复
				agg.WriteError("", fmt.Sprintf("Agent 执行异常: %v", r))
			}
		}()
		h.agentExecutor.Execute(active.Context(), sessionID, task, agg)
	}()
	<-done

	res := agg.Result()
	content := res.Content
	finishReason := res.FinishReason
	// 失败与取消都按 error 结束：取消时执行器不再写出 error 事件，
	// 但调用方需要从 finish_reason 读出「这一轮没有正常完成」。
	failed := res.Error != "" || task.Status == agent.StatusFailed || task.Status == agent.StatusCancelled
	if failed {
		finishReason = "error"
	} else {
		// WriteFinish 只在 reason 非空时覆盖，正常结束但没有任何 finish 事件时补 stop
		if finishReason == "" {
			finishReason = "stop"
		}
		if content == "" {
			content = "任务执行完成，但未获得明确结果"
		}
	}

	resp := chatCompletion{
		ID:        chatID,
		Object:    "chat.completion",
		Created:   task.StartTime.Unix(),
		Model:     mdl.Name,
		SessionID: sessionID,
		Choices: []completionChoice{{
			Index: 0,
			Message: completionMessage{
				Role:             "assistant",
				Content:          content,
				ReasoningContent: res.ReasoningContent,
				Error:            completionErrorOf(res.Error),
			},
			FinishReason: finishReason,
		}},
		Usage: completionUsage{
			PromptTokens:     res.Usage.Prompt,
			CompletionTokens: res.Usage.Completion,
			TotalTokens:      res.Usage.Total,
		},
	}
	rc.JSON(200, resp)

	sessionLog.Info(fmt.Sprintf("%s: chat=%s, round=%d, isNew=%v",
		taskStatusText(task.Status), chatID, round, isNew))
}

// resolveChatOwner 解析 /chat/completions 的会话归属。
// Web 调用者（Cookie）视为管理员，返回 owner=nil、租户与用户为空串；
// API Key 调用者要求租户非空，用户标识优先取请求体 user，未传时回落到请求头注入的 user_id。
// 失败时已写出 OpenAI 错误信封，调用方据 ok 决定是否 return。
// 请求体 user 无论哪种调用方都要做与请求头相同的格式校验：它会写进会话归属。
func resolveChatOwner(rc *app.RequestContext, bodyUser string) (owner *repo.SessionOwner, tenantID, userID string, ok bool) {
	webCaller := middleware.IsWebCaller(rc)
	if !webCaller {
		tenantID = middleware.GetTenant(rc)
		if tenantID == "" {
			errbody.Write(rc, 403, "API Key 未绑定租户",
				errbody.TypePermission, "", errbody.CodeTenantNotBound)
			return nil, "", "", false
		}
	}

	if bodyUser != "" {
		u, valid := middleware.ValidIdentifier(bodyUser)
		if !valid {
			errbody.Write(rc, 400, "user 字段非法：只允许字母、数字、下划线与连字符，长度 1..64",
				errbody.TypeInvalidRequest, "user", errbody.CodeInvalidRequest)
			return nil, "", "", false
		}
		userID = u
	} else if !webCaller {
		userID = middleware.GetUserID(rc)
		if userID == "" {
			errbody.Write(rc, 400, "缺少或非法的 user",
				errbody.TypeInvalidRequest, "user", errbody.CodeInvalidRequest)
			return nil, "", "", false
		}
	}

	if webCaller {
		return nil, "", userID, true
	}
	return &repo.SessionOwner{TenantID: tenantID, UserID: userID}, tenantID, userID, true
}

// chatCompletion 是 POST /chat/completions 非流式响应的完整结构（OpenAI chat.completion）。
// session_id 是项目扩展字段，供调用方在续聊与查询详情时使用。
type chatCompletion struct {
	ID        string             `json:"id"`
	Object    string             `json:"object"`
	Created   int64              `json:"created"`
	Model     string             `json:"model"`
	SessionID string             `json:"session_id"`
	Choices   []completionChoice `json:"choices"`
	Usage     completionUsage    `json:"usage"`
}

// completionChoice 只有一个候选：本接口不支持 n>1。
type completionChoice struct {
	Index        int               `json:"index"`
	Message      completionMessage `json:"message"`
	FinishReason string            `json:"finish_reason"`
}

// completionMessage 是助手回复；reasoning_content 与 error 无值时省略。
type completionMessage struct {
	Role             string           `json:"role"`
	Content          string           `json:"content"`
	ReasoningContent string           `json:"reasoning_content,omitempty"`
	Error            *completionError `json:"error,omitempty"`
}

// completionError 是 message 内的错误对象，只承载错误文案。
type completionError struct {
	Message string `json:"message"`
}

// completionErrorOf 把聚合结果中的错误串转成响应对象；空串返回 nil 以便序列化时省略。
func completionErrorOf(msg string) *completionError {
	if msg == "" {
		return nil
	}
	return &completionError{Message: msg}
}

// completionUsage 是本次请求的 token 用量汇总。
type completionUsage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// taskStatusText 把任务状态转成日志用的中文描述。
func taskStatusText(status agent.TaskStatus) string {
	switch status {
	case agent.StatusCompleted:
		return "完成对话"
	case agent.StatusCancelled:
		return "对话被取消"
	case agent.StatusFailed:
		return "对话失败"
	default:
		return string(status)
	}
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
