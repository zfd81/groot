package handler

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/common/utils"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/api/types"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
)

// maxSpeechInputRunes 单次合成文本的字符上限（按 Unicode 字符计），与 OpenAI 规范一致。
const maxSpeechInputRunes = 4096

// 可选透传字段的长度上限（按 Unicode 字符计）。这些值原样下发上游，
// 服务端无法校验其含义，设上限只为挡住误把整段文本填进来的情况。
const (
	maxSpeechVoiceRunes        = 64
	maxSpeechLanguageRunes     = 16
	maxSpeechInstructionsRunes = 500
)

// speechFormats 受支持的 response_format 取值。
var speechFormats = map[string]bool{
	"mp3": true, "opus": true, "aac": true, "flac": true, "wav": true, "pcm": true,
}

// SpeechHandler 处理语音合成请求。
// 对外路由与 Web 路由共用同一个 Serve 方法，鉴权差异由路由层的中间件承担。
type SpeechHandler struct {
	models *llm.ModelService
	log    *logger.Logger
}

func NewSpeechHandler(models *llm.ModelService, log *logger.Logger) *SpeechHandler {
	return &SpeechHandler{models: models, log: log}
}

// Serve 处理 POST /audio/speech 与 POST /web/audio/speech。
func (h *SpeechHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	var req types.SpeechRequest
	if err := rc.BindJSON(&req); err != nil {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "请求参数错误"})
		return
	}

	input := strings.TrimSpace(req.Input)
	if input == "" {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "input 不能为空"})
		return
	}
	if utf8.RuneCountInString(input) > maxSpeechInputRunes {
		rc.JSON(400, utils.H{
			"status":  "invalid_request",
			"message": fmt.Sprintf("input 超过 %d 字符上限", maxSpeechInputRunes),
		})
		return
	}
	if req.ResponseFormat != "" && !speechFormats[req.ResponseFormat] {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": "不支持的 response_format"})
		return
	}

	voice := strings.TrimSpace(req.Voice)
	language := strings.TrimSpace(req.Language)
	instructions := strings.TrimSpace(req.Instructions)
	if msg, ok := checkSpeechFieldLimits(voice, language, instructions); !ok {
		rc.JSON(400, utils.H{"status": "invalid_request", "message": msg})
		return
	}

	// 模型名按「请求体 model → 请求头 X-Model-Name」取显式指定值，均为空时由 Resolve 回落到默认 TTS 模型。
	// 显式指定的模型不要求持有 tts 默认；不存在或禁用时直接报错，不回落。
	name := strings.TrimSpace(req.Model)
	if name == "" {
		name = strings.TrimSpace(string(rc.GetHeader("X-Model-Name")))
	}
	m, err := h.models.Resolve(ctx, name, repo.DefaultTTS)
	if err != nil {
		if errors.Is(err, llm.ErrModelNotFound) || errors.Is(err, llm.ErrModelDisabled) ||
			errors.Is(err, llm.ErrNoDefaultModel) {
			rc.JSON(400, utils.H{"status": "invalid_model", "message": err.Error()})
			return
		}
		h.log.Error("查询 TTS 模型失败", zap.Error(err))
		rc.JSON(500, utils.H{"status": "error", "message": "内部错误"})
		return
	}

	body, ct, err := llm.Speech(ctx, m, llm.SpeechRequest{
		Input:          input,
		Voice:          voice,
		Language:       language,
		Instructions:   instructions,
		ResponseFormat: req.ResponseFormat,
	})
	if err != nil {
		h.log.Warn("语音合成失败", zap.String("model", m.Name), zap.Error(err))
		rc.JSON(502, utils.H{"status": "upstream_error", "message": err.Error()})
		return
	}

	// 上游音频流直接转发，不读入内存；body 实现 io.Closer，
	// Hertz 写完响应后在 CloseBodyStream 中关闭它。
	rc.Response.Header.Set("X-Model-Name", m.Name)
	rc.SetContentType(ct)
	rc.SetBodyStream(body, -1)
	rc.SetStatusCode(200)
}

// checkSpeechFieldLimits 逐个检查已裁剪空白的可选透传字段是否超过长度上限。
// 超限时返回面向调用方的提示与 false；合成接口与朗读配置接口共用同一套上限与文案。
func checkSpeechFieldLimits(voice, language, instructions string) (string, bool) {
	checks := []struct {
		name  string
		value string
		limit int
	}{
		{"voice", voice, maxSpeechVoiceRunes},
		{"language", language, maxSpeechLanguageRunes},
		{"instructions", instructions, maxSpeechInstructionsRunes},
	}
	for _, c := range checks {
		if utf8.RuneCountInString(c.value) > c.limit {
			return fmt.Sprintf("%s 超过 %d 字符上限", c.name, c.limit), false
		}
	}
	return "", true
}
