package handler

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/setting"
)

// audioExtensions 受支持的音频扩展名。
// 浏览器录音产出 wav，其余为对外接口常见的上传格式。
var audioExtensions = map[string]bool{
	".webm": true, ".mp3": true, ".mp4": true, ".mpeg": true,
	".mpga": true, ".m4a": true, ".wav": true, ".ogg": true, ".flac": true,
}

func supportedAudioExtensions() string {
	exts := make([]string, 0, len(audioExtensions))
	for e := range audioExtensions {
		exts = append(exts, e)
	}
	sort.Strings(exts)
	return strings.Join(exts, ", ")
}

// TranscriptionHandler 处理音频转录请求。
// 对外路由与 Web 路由共用同一个 Serve 方法，鉴权差异由路由层的中间件承担。
type TranscriptionHandler struct {
	settings *setting.Settings
	models   *llm.ModelService
	log      *logger.Logger
}

func NewTranscriptionHandler(settings *setting.Settings, models *llm.ModelService,
	log *logger.Logger) *TranscriptionHandler {
	return &TranscriptionHandler{settings: settings, models: models, log: log}
}

// Serve 处理 POST /audio/transcriptions 与 POST /web/audio/transcriptions。
func (h *TranscriptionHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	fh, err := rc.FormFile("file")
	if err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "缺少 file 字段"})
		return
	}

	ext := strings.ToLower(path.Ext(fh.Filename))
	if !audioExtensions[ext] {
		msg := fmt.Sprintf("不支持的音频格式 %q，受支持的扩展名: %s", ext, supportedAudioExtensions())
		if ext == "" {
			msg = fmt.Sprintf("文件名缺少扩展名，受支持的扩展名: %s", supportedAudioExtensions())
		}
		rc.JSON(400, utils.H{"status": "unsupported_type", "message": msg})
		return
	}

	// 附件配置的 MaxSize 以 MB 计（见 internal/attachment/handler.go 的换算），
	// 这里同样换算成字节后再与 fh.Size 比较，两处口径保持一致。
	att, err := h.settings.Attachment(ctx)
	if err != nil {
		h.log.Error("读取附件配置失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
		return
	}
	if att.MaxSize > 0 && fh.Size > int64(att.MaxSize)*1024*1024 {
		rc.JSON(400, utils.H{
			"status":  "file_too_large",
			"message": fmt.Sprintf("音频大小 %d 字节超过上限 %d MB", fh.Size, att.MaxSize),
		})
		return
	}

	// Resolve 按「显式模型名 → 默认语音模型」解析并校验 enabled：
	// 显式指定的模型不存在或禁用时直接报错，不回落到默认语音模型；
	// 三类错误对调用方都是「模型不可用」，统一映射为 invalid_model，错误原文已含模型名或类型。
	m, err := h.models.Resolve(ctx, explicitModelName(rc), repo.DefaultVoice)
	if err != nil {
		if errors.Is(err, llm.ErrModelNotFound) || errors.Is(err, llm.ErrModelDisabled) ||
			errors.Is(err, llm.ErrNoDefaultModel) {
			rc.JSON(400, utils.H{"status": "invalid_model", "message": err.Error()})
			return
		}
		h.log.Error("查询语音模型失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
		return
	}

	f, err := fh.Open()
	if err != nil {
		h.log.Error("打开上传音频失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
		return
	}
	defer f.Close()

	text, err := llm.Transcribe(ctx, m, f, fh.Filename, rc.PostForm("language"))
	if err != nil {
		if errors.Is(err, llm.ErrEmptyTranscript) {
			rc.JSON(400, utils.H{"status": "invalid_request", "message": err.Error()})
			return
		}
		h.log.Warn("转录失败", zap.String("model", m.Name), zap.Error(err))
		rc.JSON(502, utils.H{"status": "upstream_error", "message": err.Error()})
		return
	}

	rc.JSON(200, utils.H{"text": text, "model": m.Name})
}

// explicitModelName 按「表单 model → 请求头 X-Model-Name」取调用方显式指定的模型名，
// 均未给出返回空串。请求头形式与 /chat 的既有约定一致。
func explicitModelName(rc *app.RequestContext) string {
	if v := strings.TrimSpace(rc.PostForm("model")); v != "" {
		return v
	}
	return strings.TrimSpace(string(rc.GetHeader("X-Model-Name")))
}
