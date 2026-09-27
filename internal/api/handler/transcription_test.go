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
// cfg 作为静态配置传入 setting.New；upstreamURL 非空时创建名为 whisper-1 的模型，
// 其 base_url 指向 httptest 假上游；voiceModel 非空时写入配置表 voice.model。
func newTranscriptionHandlerForTest(t *testing.T, cfg config.Bootstrap, upstreamURL, voiceModel string) *TranscriptionHandler {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })

	models := llm.NewModelService(modeldb.New(sqlxDB, dialect))
	if upstreamURL != "" {
		err := models.Create(context.Background(), &repo.Model{
			Name: "whisper-1", Model: "whisper-1",
			BaseURL: upstreamURL, APIKey: "sk-test-1234abcd",
			Enabled: true, Stop: []string{},
		})
		if err != nil {
			t.Fatalf("创建测试模型: %v", err)
		}
	}

	settings := setting.New(cfg, settingdb.New(sqlxDB, dialect))
	if voiceModel != "" {
		err := settings.SetVoice(context.Background(), setting.VoiceSettings{
			Enabled: true, Model: voiceModel,
		})
		if err != nil {
			t.Fatalf("SetVoice: %v", err)
		}
	}
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

// bodyModel 解析成功响应中回传的 model 字段。
func bodyModel(t *testing.T, rc *app.RequestContext) string {
	t.Helper()
	var out struct {
		Model string `json:"model"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("响应不是合法 JSON: %s", rc.Response.Body())
	}
	return out.Model
}

var fakeAudio = []byte("FAKE-AUDIO-BYTES")

func TestTranscriptionHandler_Success(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, "whisper-1")

	rc := audioCtx(t, fakeAudio, "rec.webm", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	var out struct {
		Text  string `json:"text"`
		Model string `json:"model"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("解析响应: %v", err)
	}
	if out.Text != "打开登录日志" {
		t.Errorf("text = %q", out.Text)
	}
	if out.Model != "whisper-1" {
		t.Errorf("model = %q, want whisper-1（应回传实际使用的模型）", out.Model)
	}
}

func TestTranscriptionHandler_MissingFile(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, "whisper-1")

	rc := audioCtx(t, nil, "", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "invalid_request" {
		t.Errorf("status = %q, want invalid_request", s)
	}
}

func TestTranscriptionHandler_NoVoiceModelConfigured(t *testing.T) {
	up := fakeUpstream(t)
	// 建了模型但没配 voice.model，且请求不带 model 字段
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, "")

	rc := audioCtx(t, fakeAudio, "rec.webm", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "invalid_model" {
		t.Errorf("status = %q, want invalid_model", s)
	}
}

func TestTranscriptionHandler_ModelNotFound(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, "whisper-1")

	rc := audioCtx(t, fakeAudio, "rec.webm", "nonexistent-model")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "invalid_model" {
		t.Errorf("status = %q, want invalid_model", s)
	}
	if !bytes.Contains(rc.Response.Body(), []byte("nonexistent-model")) {
		t.Errorf("错误信息应含模型名: %s", rc.Response.Body())
	}
}

// TestTranscriptionHandler_FormModelWinsOverSetting 配置表指向一个不存在的模型，
// 表单显式指定 whisper-1，应以表单为准并成功。
func TestTranscriptionHandler_FormModelWinsOverSetting(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, "no-such-model")

	rc := audioCtx(t, fakeAudio, "rec.webm", "whisper-1")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if m := bodyModel(t, rc); m != "whisper-1" {
		t.Errorf("model = %q, want whisper-1（表单应优先于配置表）", m)
	}
}

// TestTranscriptionHandler_HeaderModelWinsOverSetting 配置表指向不存在的模型，
// 请求头 X-Model-Name 指定 whisper-1 且不带表单 model，应以请求头为准。
func TestTranscriptionHandler_HeaderModelWinsOverSetting(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, "no-such-model")

	rc := audioCtx(t, fakeAudio, "rec.webm", "")
	rc.Request.Header.Set("X-Model-Name", "whisper-1")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s（请求头应优先于配置表）", rc.Response.StatusCode(), rc.Response.Body())
	}
	if m := bodyModel(t, rc); m != "whisper-1" {
		t.Errorf("model = %q, want whisper-1", m)
	}
}

// TestTranscriptionHandler_FormWinsOverHeader 表单与请求头同时给出时，表单优先。
func TestTranscriptionHandler_FormWinsOverHeader(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, "")

	rc := audioCtx(t, fakeAudio, "rec.webm", "whisper-1")
	rc.Request.Header.Set("X-Model-Name", "no-such-model")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s（表单应优先于请求头）", rc.Response.StatusCode(), rc.Response.Body())
	}
	if m := bodyModel(t, rc); m != "whisper-1" {
		t.Errorf("model = %q, want whisper-1", m)
	}
}

func TestTranscriptionHandler_HeaderModelUsed(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, "")

	rc := audioCtx(t, fakeAudio, "rec.webm", "")
	rc.Request.Header.Set("X-Model-Name", "whisper-1")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s（请求头应可指定模型）", rc.Response.StatusCode(), rc.Response.Body())
	}
}

func TestTranscriptionHandler_UnsupportedExtension(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, "whisper-1")

	rc := audioCtx(t, fakeAudio, "notes.txt", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "unsupported_type" {
		t.Errorf("status = %q, want unsupported_type", s)
	}
}

// TestTranscriptionHandler_NoExtension 文件名没有扩展名时应给出「缺少扩展名」的提示。
func TestTranscriptionHandler_NoExtension(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, "whisper-1")

	rc := audioCtx(t, fakeAudio, "blob", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "unsupported_type" {
		t.Errorf("status = %q, want unsupported_type", s)
	}
	if !bytes.Contains(rc.Response.Body(), []byte("缺少扩展名")) {
		t.Errorf("错误信息应提示缺少扩展名: %s", rc.Response.Body())
	}
}

func TestTranscriptionHandler_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"Incorrect API key provided"}}`))
	}))
	defer srv.Close()

	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, srv.URL, "whisper-1")
	rc := audioCtx(t, fakeAudio, "rec.webm", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 502 {
		t.Fatalf("status=%d, want 502", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "upstream_error" {
		t.Errorf("status = %q, want upstream_error", s)
	}
	if !bytes.Contains(rc.Response.Body(), []byte("Incorrect API key")) {
		t.Errorf("应透传上游错误原文: %s", rc.Response.Body())
	}
}

// TestTranscriptionHandler_EmptyTranscript 上游返回空文本时应视为客户端请求问题（音频无有效语音）。
func TestTranscriptionHandler_EmptyTranscript(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"text":""}`))
	}))
	defer srv.Close()

	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, srv.URL, "whisper-1")
	rc := audioCtx(t, fakeAudio, "rec.webm", "")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400; body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if s := bodyStatus(t, rc); s != "invalid_request" {
		t.Errorf("status = %q, want invalid_request", s)
	}
}

// TestTranscriptionHandler_FileTooLarge 把附件上限设为 1MB，再上传 2MB 的音频，
// 验证按 MB 换算后的超限判断。
func TestTranscriptionHandler_FileTooLarge(t *testing.T) {
	up := fakeUpstream(t)
	h := newTranscriptionHandlerForTest(t, config.Bootstrap{}, up.URL, "whisper-1")

	// 附件上限的基准层是代码默认值（50MB），把 1MB 写进配置表以触发超限
	rt := h.settings.RuntimeStatic()
	rt.Attachment.MaxSize = 1 // MB
	if err := h.settings.SetRuntime(context.Background(), rt); err != nil {
		t.Fatalf("SetRuntime: %v", err)
	}

	rc := audioCtx(t, bytes.Repeat([]byte{0}, 2<<20), "big.webm", "whisper-1")
	h.Serve(context.Background(), rc)

	if rc.Response.StatusCode() != 400 {
		t.Fatalf("status=%d, want 400", rc.Response.StatusCode())
	}
	if s := bodyStatus(t, rc); s != "file_too_large" {
		t.Errorf("status = %q, want file_too_large", s)
	}
}
