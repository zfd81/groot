package handler

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/cloudwego/hertz/pkg/app"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/api/errbody"
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
		errbody.Write(rc, 400, "请求参数错误", errbody.TypeInvalidRequest, "", errbody.CodeInvalidRequest)
		return
	}

	input := strings.TrimSpace(req.Input)
	if input == "" {
		errbody.Write(rc, 400, "input 不能为空", errbody.TypeInvalidRequest, "input", errbody.CodeInvalidRequest)
		return
	}
	if utf8.RuneCountInString(input) > maxSpeechInputRunes {
		errbody.Write(rc, 400, fmt.Sprintf("input 超过 %d 字符上限", maxSpeechInputRunes),
			errbody.TypeInvalidRequest, "input", errbody.CodeInvalidRequest)
		return
	}
	// 对外接口不提供默认音色，调用方必须显式给出。
	voice := strings.TrimSpace(req.Voice)
	if voice == "" {
		errbody.Write(rc, 400, "缺少 voice", errbody.TypeInvalidRequest, "voice", errbody.CodeInvalidRequest)
		return
	}
	if req.ResponseFormat != "" && !speechFormats[req.ResponseFormat] {
		errbody.Write(rc, 400, "不支持的 response_format",
			errbody.TypeInvalidRequest, "response_format", errbody.CodeInvalidRequest)
		return
	}

	language := strings.TrimSpace(req.Language)
	instructions := strings.TrimSpace(req.Instructions)
	if msg, ok := checkSpeechFieldLimits(voice, language, instructions); !ok {
		errbody.Write(rc, 400, msg, errbody.TypeInvalidRequest, "", errbody.CodeInvalidRequest)
		return
	}

	// 模型名取请求体 model，为空时由 Resolve 回落到默认 TTS 模型。
	// 显式指定的模型不要求持有 tts 默认；不存在或禁用时直接报错，不回落。
	name := strings.TrimSpace(req.Model)
	m, err := h.models.Resolve(ctx, name, repo.DefaultTTS)
	if err != nil {
		if errors.Is(err, llm.ErrModelNotFound) || errors.Is(err, llm.ErrModelDisabled) ||
			errors.Is(err, llm.ErrNoDefaultModel) {
			errbody.Write(rc, 400, err.Error(), errbody.TypeInvalidRequest, "model", errbody.CodeModelNotFound)
			return
		}
		h.log.Error("查询 TTS 模型失败", zap.Error(err))
		errbody.Write(rc, 500, "内部错误", errbody.TypeAPI, "", errbody.CodeInternalError)
		return
	}

	body, ct, upErr, err := llm.Speech(ctx, m, llm.SpeechRequest{
		Input:          input,
		Voice:          voice,
		Language:       language,
		Instructions:   instructions,
		ResponseFormat: req.ResponseFormat,
		Speed:          req.Speed,
	})
	if err != nil {
		h.log.Warn("语音合成失败", zap.String("model", m.Name), zap.Error(err))
		errbody.Write(rc, 502, err.Error(), errbody.TypeAPI, "", errbody.CodeUpstreamError)
		return
	}
	if upErr != nil {
		// 与转录接口同一规则：OpenAI 信封原样转发，其它（网关 HTML 页、纯文本）转成 502。
		if isOpenAIEnvelope(upErr.Body) {
			contentType := upErr.ContentType
			if contentType == "" {
				contentType = "application/json"
			}
			forwardUpstream(rc, upErr.StatusCode, contentType, bytes.NewReader(upErr.Body), len(upErr.Body))
			return
		}
		errbody.Write(rc, 502, llm.UpstreamMessage(upErr.Body, upErr.StatusCode),
			errbody.TypeAPI, "", errbody.CodeUpstreamError)
		return
	}

	// 上游音频流直接转发，不读入内存；body 实现 io.Closer，
	// Hertz 写完响应后在 CloseBodyStream 中关闭它。
	forwardUpstream(rc, 200, ct, body, -1)
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
