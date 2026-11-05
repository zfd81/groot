package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/zfd81/groot/internal/api/errbody"
	"github.com/zfd81/groot/internal/config"
	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/modeldb"
	"github.com/zfd81/groot/internal/repo/settingdb"
	"github.com/zfd81/groot/internal/setting"
)

// newTranscriptionHandlerForTest 建一套真实的仓库与配置对象，
// cfg 作为静态配置传入 setting.New；upstreamURL 非空时创建名为 whisper-1 的模型
// （库中首个模型，自动成为默认对话模型），其 base_url 指向 httptest 假上游；
// defaultASR 为 true 时把 whisper-1 设为默认 ASR 模型。
func newTranscriptionHandlerForTest(t *testing.T, cfg config.Bootstrap, upstreamURL string, defaultASR bool) *TranscriptionHandler {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })

	ctx := context.Background()
	models := llm.NewModelService(modeldb.New(sqlxDB, dialect))
	if upstreamURL != "" {
		err := models.Create(ctx, &repo.Model{
			Name: "whisper-1", Model: "whisper-1",
			BaseURL: upstreamURL, APIKey: "sk-test-1234abcd",
			Enabled: true, Stop: []string{},
		})
		if err != nil {
			t.Fatalf("创建测试模型: %v", err)
		}
		if defaultASR {
			if err := models.SetDefault(ctx, "whisper-1", repo.DefaultASR); err != nil {
				t.Fatalf("设置默认 ASR 模型: %v", err)
			}
		}
	}

	settings := setting.New(cfg, settingdb.New(sqlxDB, dialect))
	return NewTranscriptionHandler(settings, models, logger.NewNop())
}

// audioCtx 构造一个 multipart 请求上下文。
// payload 为 nil 时不带 file 字段；model 为空时不带该字段。
func audioCtx(t *testing.T, payload []byte, filename, model string) *app.RequestContext {
	t.Helper()
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	if payload != nil {
		fw, err := w.CreateFormFile("file", filename)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := fw.Write(payload); err != nil {
			t.Fatal(err)
		}
	}
	if model != "" {
		if err := w.WriteField("model", model); err != nil {
			t.Fatal(err)
		}
	}
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodPost)
	rc.Request.Header.SetContentTypeBytes([]byte(w.FormDataContentType()))
	rc.Request.Header.SetContentLength(buf.Len())
	rc.Request.SetBody(buf.Bytes())
	return rc
}

// fakeUpstream 返回一个总是成功转录的假上游。
func fakeUpstream(t *testing.T) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"text":"打开登录日志"}`))
	}))
	t.Cleanup(srv.Close)
	return srv
}

// bodyStatus 解析旧式 {"status":"..."} 响应体，供其它 handler 的测试共用。
func bodyStatus(t *testing.T, rc *app.RequestContext) string {
	t.Helper()
	var out struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("响应不是合法 JSON: %s", rc.Response.Body())
	}
	return out.Status
}

// envelopeOf 解析 OpenAI 错误信封，失败即终止测试。
func envelopeOf(t *testing.T, rc *app.RequestContext) errbody.Envelope {
	t.Helper()
	var envl errbody.Envelope
	if err := json.Unmarshal(rc.Response.Body(), &envl); err != nil {
		t.Fatalf("响应体不是合法错误信封: %v, body=%s", err, rc.Response.Body())
	}
	return envl
}

var fakeAudio = []byte("FAKE-AUDIO-BYTES")

// TestTranscriptionHandler_Success 成功响应原样透传上游的 status、Content-Type 与 body，
// 不再附加 model 字段。
func TestTranscriptionHandler_Success(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, true)

	rc := audioCtx(t, fakeAudio, "rec.webm", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if ct := string(rc.Response.Header.ContentType()); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	if got := string(rc.Response.Body()); got != `{"text":"打开登录日志"}` {
		t.Errorf("body = %q，应原样透传上游响应", got)
	}
}

// TestTranscriptionHandler_StreamsNonJSONBody 上游返回非 JSON（如 SRT 字幕）时，
// 原样转发上游的 Content-Type 与字节。
func TestTranscriptionHandler_StreamsNonJSONBody(t *testing.T) {
	const srt = "1\n00:00:00,000 --> 00:00:02,000\n你好\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte(srt))
	}))
	defer srv.Close()

	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, srv.URL, true)
	rc := audioCtx(t, fakeAudio, "rec.webm", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if ct := string(rc.Response.Header.ContentType()); ct != "text/plain" {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
	if got := string(rc.Response.Body()); got != srt {
		t.Errorf("body = %q, want 原样 srt", got)
	}
}

func TestTranscriptionHandler_MissingFile(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, true)

	rc := audioCtx(t, nil, "", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	if code := envelopeOf(t, rc).Error.Code; code == nil || *code != errbody.CodeInvalidRequest {
		t.Errorf("code = %v, want invalid_request", code)
	}
}

func TestTranscriptionHandler_NoASRModelConfigured(t *testing.T) {
	up := fakeUpstream(t)
	// 建了模型但未设默认 ASR 模型，且请求不带 model 字段
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, false)

	rc := audioCtx(t, fakeAudio, "rec.webm", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	envl := envelopeOf(t, rc)
	if envl.Error.Code == nil || *envl.Error.Code != errbody.CodeModelNotFound {
		t.Errorf("code = %v, want model_not_found", envl.Error.Code)
	}
	if !bytes.Contains(rc.Response.Body(), []byte("未配置默认 ASR 模型")) {
		t.Errorf("错误信息应提示未配置默认 ASR 模型: %s", rc.Response.Body())
	}
}

func TestTranscriptionHandler_ModelNotFound(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, true)

	rc := audioCtx(t, fakeAudio, "rec.webm", "nonexistent-model")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	envl := envelopeOf(t, rc)
	if envl.Error.Code == nil || *envl.Error.Code != errbody.CodeModelNotFound {
		t.Errorf("code = %v, want model_not_found", envl.Error.Code)
	}
	if envl.Error.Param == nil || *envl.Error.Param != "model" {
		t.Errorf("param = %v, want model", envl.Error.Param)
	}
	if !bytes.Contains(rc.Response.Body(), []byte("nonexistent-model")) {
		t.Errorf("错误信息应含模型名: %s", rc.Response.Body())
	}
}

// TestTranscriptionHandler_DefaultASRUsed 请求不指定模型时使用默认 ASR 模型。
func TestTranscriptionHandler_DefaultASRUsed(t *testing.T) {
	var gotModel string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseMultipartForm(1 << 20)
		gotModel = r.FormValue("model")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"text":"ok"}`))
	}))
	defer srv.Close()

	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, srv.URL, true)
	rc := audioCtx(t, fakeAudio, "rec.webm", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if gotModel != "whisper-1" {
		t.Errorf("上游收到 model = %q, want whisper-1（默认 ASR 模型）", gotModel)
	}
}

// TestTranscriptionHandler_FormModelWithoutDefault 未设默认 ASR 模型，表单显式指定时照常转录。
func TestTranscriptionHandler_FormModelWithoutDefault(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, false)

	rc := audioCtx(t, fakeAudio, "rec.webm", "whisper-1")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
}

// TestTranscriptionHandler_ExplicitUnknownNoFallback 显式指定的模型不存在时报错，不回落到默认 ASR 模型。
func TestTranscriptionHandler_ExplicitUnknownNoFallback(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, true)

	rc := audioCtx(t, fakeAudio, "rec.webm", "no-such-model")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400（不应回落到默认 ASR 模型）", rc.Response.StatusCode())
	}
	if code := envelopeOf(t, rc).Error.Code; code == nil || *code != errbody.CodeModelNotFound {
		t.Errorf("code = %v, want model_not_found", code)
	}
}

// TestTranscriptionHandler_IgnoresVoiceSetting 服务端不读取配置表 voice.model：
// 配置表选了 whisper-1 但无默认 ASR 模型、请求也不指定时，仍报未配置。
func TestTranscriptionHandler_IgnoresVoiceSetting(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, false)
	err := h.settings.SetVoice(context.Background(), setting.VoiceSettings{Model: "whisper-1"})
	if err != nil {
		t.Fatalf("SetVoice: %v", err)
	}

	rc := audioCtx(t, fakeAudio, "rec.webm", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400（不应读取配置表 voice.model）", rc.Response.StatusCode())
	}
	if code := envelopeOf(t, rc).Error.Code; code == nil || *code != errbody.CodeModelNotFound {
		t.Errorf("code = %v, want model_not_found", code)
	}
}

// TestTranscriptionHandler_IgnoresModelHeader 模型只读表单 model，请求头不再回退。
func TestTranscriptionHandler_IgnoresModelHeader(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, false)

	rc := audioCtx(t, fakeAudio, "rec.webm", "")
	rc.Request.Header.Set("X-Model-Name", "whisper-1")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400（请求头不应再决定模型）", rc.Response.StatusCode())
	}
	if code := envelopeOf(t, rc).Error.Code; code == nil || *code != errbody.CodeModelNotFound {
		t.Errorf("code = %v, want model_not_found", code)
	}
}

// TestTranscriptionHandler_ForwardsFormFields 除 model 外的表单参数在 handler 层一并转发，
// timestamp_granularities[] 的多项以多个同名字段下发。
func TestTranscriptionHandler_ForwardsFormFields(t *testing.T) {
	var gotLang, gotPrompt, gotFormat, gotTemp string
	var gotGranularities []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseMultipartForm(1 << 20)
		gotLang = r.FormValue("language")
		gotPrompt = r.FormValue("prompt")
		gotFormat = r.FormValue("response_format")
		gotTemp = r.FormValue("temperature")
		gotGranularities = r.MultipartForm.Value["timestamp_granularities[]"]
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"text":"ok"}`))
	}))
	defer srv.Close()

	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, srv.URL, true)

	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	fw, err := w.CreateFormFile("file", "rec.webm")
	if err != nil {
		t.Fatal(err)
	}
	fw.Write(fakeAudio)
	for k, v := range map[string]string{
		"language":        "zh",
		"prompt":          "登录日志",
		"response_format": "verbose_json",
		"temperature":     "0",
	} {
		w.WriteField(k, v)
	}
	w.WriteField("timestamp_granularities[]", "word")
	w.WriteField("timestamp_granularities[]", "segment")
	w.Close()

	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(consts.MethodPost)
	rc.Request.Header.SetContentTypeBytes([]byte(w.FormDataContentType()))
	rc.Request.Header.SetContentLength(buf.Len())
	rc.Request.SetBody(buf.Bytes())

	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if gotLang != "zh" || gotPrompt != "登录日志" || gotFormat != "verbose_json" || gotTemp != "0" {
		t.Errorf("表单字段未透传: language=%q prompt=%q response_format=%q temperature=%q",
			gotLang, gotPrompt, gotFormat, gotTemp)
	}
	if len(gotGranularities) != 2 || gotGranularities[0] != "word" || gotGranularities[1] != "segment" {
		t.Errorf("timestamp_granularities[] = %v, want [word segment]", gotGranularities)
	}
}

func TestTranscriptionHandler_UnsupportedExtension(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, true)

	rc := audioCtx(t, fakeAudio, "notes.txt", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	if code := envelopeOf(t, rc).Error.Code; code == nil || *code != errbody.CodeUnsupportedType {
		t.Errorf("code = %v, want unsupported_type", code)
	}
}

// TestTranscriptionHandler_NoExtension 文件名没有扩展名时应给出「缺少扩展名」的提示。
func TestTranscriptionHandler_NoExtension(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, true)

	rc := audioCtx(t, fakeAudio, "blob", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	if code := envelopeOf(t, rc).Error.Code; code == nil || *code != errbody.CodeUnsupportedType {
		t.Errorf("code = %v, want unsupported_type", code)
	}
	if !bytes.Contains(rc.Response.Body(), []byte("缺少扩展名")) {
		t.Errorf("错误信息应提示缺少扩展名: %s", rc.Response.Body())
	}
}

// TestTranscriptionHandler_UpstreamEnvelopePassthrough 上游非 2xx 且是 OpenAI 信封时，
// 原样转发上游的状态码与响应体（逐字节相同）。
func TestTranscriptionHandler_UpstreamEnvelopePassthrough(t *testing.T) {
	const upstreamBody = `{"error":{"message":"Incorrect API key provided","type":"invalid_request_error","param":null,"code":"invalid_api_key"}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(401)
		w.Write([]byte(upstreamBody))
	}))
	defer srv.Close()

	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, srv.URL, true)
	rc := audioCtx(t, fakeAudio, "rec.webm", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 401 {
		t.Fatalf("status=%d, want 401（原样转发上游状态码）; body=%s",
			rc.Response.StatusCode(), rc.Response.Body())
	}
	if got := string(rc.Response.Body()); got != upstreamBody {
		t.Errorf("body = %q，应与上游响应逐字节相同", got)
	}
}

// TestTranscriptionHandler_UpstreamNonEnvelopeReturns502 上游非 2xx 但不是 OpenAI 信封
// （如网关 HTML 页）时，返回 502 upstream_error，message 带上游原文。
func TestTranscriptionHandler_UpstreamNonEnvelopeReturns502(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		w.WriteHeader(502)
		w.Write([]byte(`<html>Bad Gateway</html>`))
	}))
	defer srv.Close()

	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, srv.URL, true)
	rc := audioCtx(t, fakeAudio, "rec.webm", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 502 {
		t.Fatalf("status=%d, want 502; body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	envl := envelopeOf(t, rc)
	if envl.Error.Code == nil || *envl.Error.Code != errbody.CodeUpstreamError {
		t.Errorf("code = %v, want upstream_error", envl.Error.Code)
	}
	if !bytes.Contains([]byte(envl.Error.Message), []byte("Bad Gateway")) {
		t.Errorf("message 应透传上游原文: %q", envl.Error.Message)
	}
}

// TestTranscriptionHandler_EmptyTextPassthrough 上游返回空 text 也原样透传，不再当成客户端错误。
func TestTranscriptionHandler_EmptyTextPassthrough(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"text":""}`))
	}))
	defer srv.Close()

	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, srv.URL, true)
	rc := audioCtx(t, fakeAudio, "rec.webm", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d, want 200; body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if got := string(rc.Response.Body()); got != `{"text":""}` {
		t.Errorf("body = %q，应原样透传", got)
	}
}

// TestTranscriptionHandler_TooLargeReturns413 超过附件上限时返回 413 file_too_large。
func TestTranscriptionHandler_TooLargeReturns413(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, true)

	// 附件上限的基准层是代码默认值（50MB），把 1MB 写进配置表以触发超限
	rt := h.settings.RuntimeStatic()
	rt.Attachment.MaxSize = 1 // MB
	if err := h.settings.SetRuntime(context.Background(), rt); err != nil {
		t.Fatalf("SetRuntime: %v", err)
	}

	rc := audioCtx(t, bytes.Repeat([]byte{0}, 2<<20), "big.webm", "whisper-1")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 413 {
		t.Fatalf("status=%d, want 413", rc.Response.StatusCode())
	}
	envl := envelopeOf(t, rc)
	if envl.Error.Code == nil || *envl.Error.Code != errbody.CodeFileTooLarge {
		t.Errorf("code = %v, want file_too_large", envl.Error.Code)
	}
}
