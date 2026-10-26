package handler

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"sort"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/attachment"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/setting"
)

// defaultInstruction 调用方未给出指令时下发的默认指令。
const defaultInstruction = "请详细描述这张图片的内容；如果图片中包含文字，请完整识别并逐字输出。"

// statusClientClosedRequest 调用方在拿到响应前放弃请求时返回的状态码（nginx 约定的 499）。
// 与 502 分开是为了让「请求被放弃」不再被读成「上游故障」。
const statusClientClosedRequest = 499

// imageExtensions 文件头无法识别时按扩展名兜底映射的图片类型。
var imageExtensions = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg",
	".webp": "image/webp", ".gif": "image/gif", ".bmp": "image/bmp",
}

func supportedImageExtensions() string {
	exts := make([]string, 0, len(imageExtensions))
	for e := range imageExtensions {
		exts = append(exts, e)
	}
	sort.Strings(exts)
	return strings.Join(exts, ", ")
}

var (
	errDecodeImage = errors.New("图片内容不是合法 Base64")
	errNotImage    = errors.New("内容不是受支持的图片")
)

// imageMIME 判定 content 代表的图片类型：单次流式解码，只保留 512 字节嗅探缓冲。
// 解码失败返回 errDecodeImage；类型不可用返回 errNotImage。
//
// 图片往往是几 MB 的 Base64，完整解码只为了看一眼文件头不划算，因此边解码边丢弃，
// 内存占用与图片大小无关。文件头能嗅探出明确类型时以嗅探结果为准；只有嗅探不出
// （application/octet-stream）时才按扩展名兜底，避免「改个后缀就当成图片」。
func imageMIME(content, name string) (string, error) {
	dec := base64.NewDecoder(base64.StdEncoding, strings.NewReader(content))

	head := make([]byte, 512)
	n, err := io.ReadFull(dec, head)
	if err != nil && err != io.ErrUnexpectedEOF && err != io.EOF {
		return "", errDecodeImage
	}
	// 走完剩余部分才能发现尾部的非法字符，ReadFull 只看到前 512 字节。
	if _, err := io.Copy(io.Discard, dec); err != nil {
		return "", errDecodeImage
	}

	mt := http.DetectContentType(head[:n])
	if strings.HasPrefix(mt, "image/") {
		return mt, nil
	}
	// 嗅探出明确的非图片类型时不兜底：内容本身就是证据，扩展名不足为凭。
	if mt != "application/octet-stream" {
		return "", errNotImage
	}

	mime, ok := imageExtensions[strings.ToLower(filepath.Ext(name))]
	if !ok {
		return "", errNotImage
	}
	return mime, nil
}

// ImageHandler 处理图片解析请求。
// 仅注册在对外路由（apiGroup），鉴权由路由层中间件承担。
type ImageHandler struct {
	settings *setting.Settings
	models   *llm.ModelService
	log      *logger.Logger
}

func NewImageHandler(settings *setting.Settings, models *llm.ModelService,
	log *logger.Logger) *ImageHandler {
	return &ImageHandler{settings: settings, models: models, log: log}
}

// Serve 处理 POST /image/understanding：图片进、文本出，不涉及会话与历史。
func (h *ImageHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	var req types.ImageUnderstandingRequest
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求参数错误"})
		return
	}

	// 本接口一次只解析一张图，多张时无法确定用哪张，直接拒绝而不是只取第一张。
	if len(req.Attachments) != 1 {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "必须提供 1 张图片附件"})
		return
	}
	att := req.Attachments[0]

	// 提前拦住非图片类型，报出的错误比 Validate 的统一类型校验更贴题。
	if att.Type != "image" {
		rc.JSON(400, utils.H{
			"status":  "attachment_invalid_type",
			"message": "无效的附件类型：" + att.Type,
		})
		return
	}

	cfg, err := h.settings.Attachment(ctx)
	if err != nil {
		h.log.Error("读取附件配置失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
		return
	}
	attInput := []attachment.Attachment{{
		Type:    att.Type,
		Name:    att.Name,
		Content: att.Content,
	}}
	if err := attachment.NewHandler(cfg).Validate(attInput); err != nil {
		if attErr, ok := err.(*attachment.AttachmentError); ok {
			rc.JSON(400, utils.H{"status": attErr.Code, "message": attErr.Message})
		} else {
			rc.JSON(400, utils.H{"status": "attachment_validation_error", "message": err.Error()})
		}
		return
	}

	mimeType, err := imageMIME(att.Content, att.Name)
	if err != nil {
		switch {
		case errors.Is(err, errDecodeImage):
			rc.JSON(400, utils.H{
				"status":  "attachment_decode_error",
				"message": fmt.Sprintf("附件 %s 的 Base64 解码失败", att.Name),
			})
		default:
			rc.JSON(400, utils.H{
				"status": "unsupported_type",
				"message": fmt.Sprintf("附件 %s 不是受支持的图片（支持的扩展名：%s）",
					att.Name, supportedImageExtensions()),
			})
		}
		return
	}

	// Resolve 按「显式模型名 → 默认视觉模型」解析并校验 enabled：
	// 显式指定的模型不存在或禁用时直接报错，不回落到默认视觉模型；
	// 三类错误对调用方都是「模型不可用」，统一映射为 invalid_model，错误原文已含模型名或类型。
	m, err := h.models.Resolve(ctx,
		strings.TrimSpace(string(rc.GetHeader("X-Model-Name"))), repo.DefaultVision)
	if err != nil {
		if errors.Is(err, llm.ErrModelNotFound) || errors.Is(err, llm.ErrModelDisabled) ||
			errors.Is(err, llm.ErrNoDefaultModel) {
			rc.JSON(400, utils.H{"status": "invalid_model", "message": err.Error()})
			return
		}
		h.log.Error("查询视觉模型失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
		return
	}

	instruction := strings.TrimSpace(req.Instruction)
	if instruction == "" {
		instruction = defaultInstruction
	}

	// Base64 原样下发，换行清洗在 llm.UnderstandImage 内完成，两处口径只保留一份。
	text, usage, err := llm.UnderstandImage(ctx, m, llm.ImageRequest{
		Instruction: instruction,
		MIMEType:    mimeType,
		Base64:      att.Content,
	})
	if err != nil {
		// 调用方上下文已结束（请求被放弃）不是上游故障：日志与响应都据此分开，
		// 否则一条「图片解析失败」会把「没人等结果了」记成「上游坏了」。
		// 只有错误本身就是该上下文错误时才这样判定，避免上游真出错时恰好赶上取消而被误记。
		if ctxErr := ctx.Err(); ctxErr != nil && errors.Is(err, ctxErr) {
			h.log.Info("图片解析已中断", zap.String("model", m.Name), zap.Error(ctxErr))
			rc.JSON(statusClientClosedRequest, utils.H{"status": "cancelled", "message": ctxErr.Error()})
			return
		}
		h.log.Warn("图片解析失败", zap.String("model", m.Name), zap.Error(err))
		rc.JSON(502, utils.H{"status": "upstream_error", "message": err.Error()})
		return
	}

	resp := utils.H{"text": text, "model": m.Name}
	if usage != nil {
		resp["usage"] = utils.H{
			"prompt_tokens":     usage.PromptTokens,
			"completion_tokens": usage.CompletionTokens,
			"total_tokens":      usage.TotalTokens,
		}
	}
	rc.JSON(200, resp)
}
