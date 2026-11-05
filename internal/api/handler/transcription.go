package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"sort"
	"strings"

	"github.com/cloudwego/hertz/pkg/app"
	"go.uber.org/zap"

	"github.com/zfd81/groot/internal/api/errbody"
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
// 成功后原样转发上游的 status、Content-Type 与响应体：不同 response_format 的
// 响应形态各异（JSON、纯文本、SRT、VTT），服务端解析再重组会丢失信息。
func (h *TranscriptionHandler) Serve(ctx context.Context, rc *app.RequestContext) {
	fh, err := rc.FormFile("file")
	if err != nil {
		errbody.Write(rc, 400, "缺少 file 字段", errbody.TypeInvalidRequest, "file", errbody.CodeInvalidRequest)
		return
	}

	ext := strings.ToLower(path.Ext(fh.Filename))
	if !audioExtensions[ext] {
		msg := fmt.Sprintf("不支持的音频格式 %q，受支持的扩展名: %s", ext, supportedAudioExtensions())
		if ext == "" {
			msg = fmt.Sprintf("文件名缺少扩展名，受支持的扩展名: %s", supportedAudioExtensions())
		}
		errbody.Write(rc, 400, msg, errbody.TypeInvalidRequest, "file", errbody.CodeUnsupportedType)
		return
	}

	// 附件配置的 MaxSize 以 MB 计（见 internal/attachment/handler.go 的换算），
	// 这里同样换算成字节后再与 fh.Size 比较，两处口径保持一致。
	att, err := h.settings.Attachment(ctx)
	if err != nil {
		h.log.Error("读取附件配置失败", zap.Error(err))
		errbody.Write(rc, 500, "内部错误", errbody.TypeAPI, "", errbody.CodeInternalError)
		return
	}
	if att.MaxSize > 0 && fh.Size > int64(att.MaxSize)*1024*1024 {
		msg := fmt.Sprintf("音频大小 %d 字节超过上限 %d MB", fh.Size, att.MaxSize)
		errbody.Write(rc, 413, msg, errbody.TypeInvalidRequest, "file", errbody.CodeFileTooLarge)
		return
	}

	// Resolve 按「显式模型名 → 默认 ASR 模型」解析并校验 enabled：
	// 显式指定的模型不存在或禁用时直接报错，不回落到默认 ASR 模型；
	// 三类错误对调用方都是「模型不可用」，统一映射为 model_not_found，错误原文已含模型名或类型。
	m, err := h.models.Resolve(ctx, explicitModelName(rc), repo.DefaultASR)
	if err != nil {
		if errors.Is(err, llm.ErrModelNotFound) || errors.Is(err, llm.ErrModelDisabled) ||
			errors.Is(err, llm.ErrNoDefaultModel) {
			errbody.Write(rc, 400, err.Error(), errbody.TypeInvalidRequest, "model", errbody.CodeModelNotFound)
			return
		}
		h.log.Error("查询 ASR 模型失败", zap.Error(err))
		errbody.Write(rc, 500, "内部错误", errbody.TypeAPI, "", errbody.CodeInternalError)
		return
	}

	f, err := fh.Open()
	if err != nil {
		h.log.Error("打开上传音频失败", zap.Error(err))
		errbody.Write(rc, 500, "内部错误", errbody.TypeAPI, "", errbody.CodeInternalError)
		return
	}
	defer f.Close()

	resp, err := llm.Transcribe(ctx, m, f, fh.Filename, llm.TranscriptionParams{
		Model:                  m.Model,
		Language:               rc.PostForm("language"),
		Prompt:                 rc.PostForm("prompt"),
		ResponseFormat:         rc.PostForm("response_format"),
		Temperature:            rc.PostForm("temperature"),
		TimestampGranularities: rc.PostFormArray("timestamp_granularities[]"),
	})
	if err != nil {
		h.log.Warn("转录失败", zap.String("model", m.Name), zap.Error(err))
		errbody.Write(rc, 502, err.Error(), errbody.TypeAPI, "", errbody.CodeUpstreamError)
		return
	}
	ct := resp.ContentType
	if ct == "" {
		ct = "application/json"
	}

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		// 响应体交给 Hertz 边读边写，写完后由 CloseBodyStream 关闭，
		// 因此这里不能 defer Close：handler 返回时 body 还没被写出。
		forwardUpstream(rc, resp.StatusCode, ct, resp.Body, -1)
		return
	}

	// 非 2xx：OpenAI 信封（error.message 非空）原样转发，让调用方拿到上游的真实错误；
	// 其它（网关 HTML 页、纯文本）读入后转成 502，避免把非信封内容冒充成错误信封。
	body, err := llm.UpstreamBody(resp.Body)
	resp.Body.Close()
	if err != nil {
		h.log.Warn("读取上游错误响应失败", zap.String("model", m.Name), zap.Error(err))
		errbody.Write(rc, 502, err.Error(), errbody.TypeAPI, "", errbody.CodeUpstreamError)
		return
	}
	if isOpenAIEnvelope(body) {
		forwardUpstream(rc, resp.StatusCode, ct, bytes.NewReader(body), len(body))
		return
	}
	errbody.Write(rc, 502, llm.UpstreamMessage(body, resp.StatusCode),
		errbody.TypeAPI, "", errbody.CodeUpstreamError)
}

// forwardUpstream 把上游响应的状态码、Content-Type 与 body 原样写出。
// size < 0 表示长度未知，由 Hertz 以 chunked 方式边读边写；
// body 实现 io.Closer 时由 Hertz 在写完后关闭。
func forwardUpstream(rc *app.RequestContext, status int, contentType string, body io.Reader, size int) {
	rc.SetStatusCode(status)
	rc.SetContentType(contentType)
	rc.SetBodyStream(body, size)
}

// isOpenAIEnvelope 判断响应体是否为 OpenAI 错误信封：顶层 error 是对象且 message 非空。
// 只有这种结构才能原样转发，否则客户端会用统一的错误解析逻辑读出空 message。
func isOpenAIEnvelope(body []byte) bool {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err != nil {
		return false
	}
	return e.Error.Message != ""
}

// explicitModelName 读取表单 model 字段，未给出返回空串。
func explicitModelName(rc *app.RequestContext) string {
	return strings.TrimSpace(rc.PostForm("model"))
}
