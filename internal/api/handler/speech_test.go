package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/cloudwego/hertz/pkg/app"
	"github.com/cloudwego/hertz/pkg/protocol/consts"

	"github.com/zfd81/groot/internal/db"
	"github.com/zfd81/groot/internal/llm"
	"github.com/zfd81/groot/internal/logger"
	"github.com/zfd81/groot/internal/repo"
	"github.com/zfd81/groot/internal/repo/modeldb"
)

// newSpeechHandlerForTest 建一套真实的模型仓库，创建名为 tts-1 的模型
// （库中首个模型，自动成为默认对话模型），其 base_url 指向 httptest 假上游；
// defaultTTS 为 true 时把 tts-1 设为默认 TTS 模型。
func newSpeechHandlerForTest(t *testing.T, upstreamURL string, defaultTTS bool) *SpeechHandler {
	t.Helper()
	sqlxDB, dialect, err := db.Open(nil, t.TempDir())
	if err != nil {
		t.Fatalf("db.Open: %v", err)
	}
	t.Cleanup(func() { sqlxDB.Close() })

	ctx := context.Background()
	models := llm.NewModelService(modeldb.New(sqlxDB, dialect))
	err = models.Create(ctx, &repo.Model{
		Name: "tts-1", Model: "tts-1",
		BaseURL: upstreamURL, APIKey: "sk-test-1234abcd",
		Enabled: true, Stop: []string{},
	})
	if err != nil {
		t.Fatalf("创建测试模型: %v", err)
	}
	if defaultTTS {
		if err := models.SetDefault(ctx, "tts-1", repo.DefaultTTS); err != nil {
			t.Fatalf("设置默认 TTS 模型: %v", err)
		}
	}
	return NewSpeechHandler(models, logger.NewNop())
}

// callJSONWithHeader 与 callJSON 相同，但在调用 handler 前设置请求头。
func callJSONWithHeader(h func(context.Context, *app.RequestContext), method, body string,
	headers map[string]string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(method)
	if body != "" {
		rc.Request.SetBody([]byte(body))
		rc.Request.Header.SetContentTypeBytes([]byte("application/json"))
	}
	for k, v := range headers {
		rc.Request.Header.Set(k, v)
	}
	h(context.Background(), rc)
	return rc
}

var fakeSpeechAudio = []byte("FAKE-MP3-BYTES")

// speechUpstream 记录最近一次收到的 JSON 请求体，返回 fakeSpeechAudio。
// contentType 非空时设置响应 Content-Type。
type speechUpstream struct {
	srv *httptest.Server
	mu  sync.Mutex
	got map[string]any
}

func newSpeechUpstream(t *testing.T, contentType string, status int) *speechUpstream {
	t.Helper()
	u := &speechUpstream{}
	u.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		u.mu.Lock()
		u.got = m
		u.mu.Unlock()
		if status >= 400 {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(status)
			w.Write([]byte(`{"error":{"message":"boom"}}`))
			return
		}
		if contentType != "" {
			w.Header().Set("Content-Type", contentType)
		} else {
			// 置为 nil 阻止 net/http 自动嗅探填入 Content-Type，模拟上游未设该头
			w.Header()["Content-Type"] = nil
		}
		w.Write(fakeSpeechAudio)
	}))
	t.Cleanup(u.srv.Close)
	return u
}

func (u *speechUpstream) received() map[string]any {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.got
}

func TestSpeechHandler_Success(t *testing.T) {
	up := newSpeechUpstream(t, "audio/mpeg", 200)
	h := newSpeechHandlerForTest(t, up.srv.URL, true)

	rc := callJSON(h.Serve, consts.MethodPost, `{"input":"你好"}`, nil)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if !bytes.Equal(rc.Response.Body(), fakeSpeechAudio) {
		t.Errorf("body = %q, want 上游字节", rc.Response.Body())
	}
	if ct := string(rc.Response.Header.ContentType()); ct != "audio/mpeg" {
		t.Errorf("Content-Type = %q, want audio/mpeg", ct)
	}
	if mn := string(rc.Response.Header.Get("X-Model-Name")); mn != "tts-1" {
		t.Errorf("X-Model-Name = %q, want tts-1", mn)
	}
	got := up.received()
	if got["model"] != "tts-1" {
		t.Errorf("上游收到 model = %v, want tts-1", got["model"])
	}
	for _, k := range []string{"voice", "language", "instructions", "response_format"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s 为空时不应下发: %v", k, got)
		}
	}
}

func TestSpeechHandler_PassesLanguageInstructions(t *testing.T) {
	up := newSpeechUpstream(t, "audio/mpeg", 200)
	h := newSpeechHandlerForTest(t, up.srv.URL, true)

	rc := callJSON(h.Serve, consts.MethodPost,
		`{"input":"你好","voice":" vivian ","language":" zh ","instructions":" 用轻快的语气朗读 "}`, nil)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	got := up.received()
	want := map[string]string{
		"voice":        "vivian",
		"language":     "zh",
		"instructions": "用轻快的语气朗读",
	}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("上游收到 %s = %v, want %q（应去掉首尾空白）", k, got[k], v)
		}
	}
}

func TestSpeechHandler_OmitsEmptyLanguageInstructions(t *testing.T) {
	up := newSpeechUpstream(t, "audio/mpeg", 200)
	h := newSpeechHandlerForTest(t, up.srv.URL, true)

	// 显式传空串或仅空白，裁剪后为空，均不下发
	rc := callJSON(h.Serve, consts.MethodPost,
		`{"input":"你好","voice":"alloy","language":"  ","instructions":""}`, nil)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	got := up.received()
	if got["voice"] != "alloy" {
		t.Errorf("上游收到 voice = %v, want alloy", got["voice"])
	}
	for _, k := range []string{"language", "instructions"} {
		if _, ok := got[k]; ok {
			t.Errorf("%s 为空时不应下发: %v", k, got)
		}
	}
}

func TestSpeechHandler_IgnoresLegacyFields(t *testing.T) {
	up := newSpeechUpstream(t, "audio/mpeg", 200)
	h := newSpeechHandlerForTest(t, up.srv.URL, true)

	// 旧键名 speaker、instruct 不再是请求字段：忽略不报错，也不以任何键名下发上游
	rc := callJSON(h.Serve, consts.MethodPost,
		`{"input":"你好","voice":"vivian","speaker":"ryan","instruct":"旧字段"}`, nil)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	got := up.received()
	if got["voice"] != "vivian" {
		t.Errorf("上游收到 voice = %v, want vivian", got["voice"])
	}
	for _, k := range []string{"speaker", "instruct", "instructions"} {
		if _, ok := got[k]; ok {
			t.Errorf("旧字段不应以 %s 下发: %v", k, got)
		}
	}
}

func TestSpeechHandler_OptionalFieldLimits(t *testing.T) {
	cases := []struct {
		field string
		limit int
	}{
		{"voice", 64},
		{"language", 16},
		{"instructions", 500},
	}
	for _, c := range cases {
		t.Run(c.field, func(t *testing.T) {
			up := newSpeechUpstream(t, "audio/mpeg", 200)
			h := newSpeechHandlerForTest(t, up.srv.URL, true)

			// 用多字节字符验证按 rune 计数
			exact := strings.Repeat("字", c.limit)
			rc := callJSON(h.Serve, consts.MethodPost,
				`{"input":"你好","`+c.field+`":"`+exact+`"}`, nil)
			if rc.Response.StatusCode() != 200 {
				t.Fatalf("恰好 %d 字应通过: status=%d body=%s", c.limit,
					rc.Response.StatusCode(), rc.Response.Body())
			}
			if got := up.received(); got[c.field] != exact {
				t.Errorf("上游收到 %s 长度 = %d, want %d", c.field,
					len([]rune(got[c.field].(string))), c.limit)
			}

			up2 := newSpeechUpstream(t, "audio/mpeg", 200)
			h2 := newSpeechHandlerForTest(t, up2.srv.URL, true)
			tooLong := strings.Repeat("字", c.limit+1)
			rc = callJSON(h2.Serve, consts.MethodPost,
				`{"input":"你好","`+c.field+`":"`+tooLong+`"}`, nil)
			assertSpeechError(t, rc, 400, "invalid_request")
			wantMsg := fmt.Sprintf("%s 超过 %d 字符上限", c.field, c.limit)
			if !strings.Contains(string(rc.Response.Body()), wantMsg) {
				t.Errorf("message 应为 %q: %s", wantMsg, rc.Response.Body())
			}
			if up2.received() != nil {
				t.Errorf("%s 超长时不应请求上游", c.field)
			}
		})
	}
}

func TestSpeechHandler_PassesVoiceAndFormat(t *testing.T) {
	// 上游不设 Content-Type，验证按 response_format 推断
	up := newSpeechUpstream(t, "", 200)
	h := newSpeechHandlerForTest(t, up.srv.URL, true)

	rc := callJSON(h.Serve, consts.MethodPost,
		`{"input":"你好","voice":"alloy","response_format":"wav"}`, nil)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	got := up.received()
	if got["voice"] != "alloy" {
		t.Errorf("上游收到 voice = %v, want alloy", got["voice"])
	}
	if got["response_format"] != "wav" {
		t.Errorf("上游收到 response_format = %v, want wav", got["response_format"])
	}
	if ct := string(rc.Response.Header.ContentType()); ct != "audio/wav" {
		t.Errorf("Content-Type = %q, want audio/wav", ct)
	}
}

func assertSpeechError(t *testing.T, rc *app.RequestContext, code int, status string) {
	t.Helper()
	if rc.Response.StatusCode() != code {
		t.Fatalf("status=%d, want %d; body=%s", rc.Response.StatusCode(), code, rc.Response.Body())
	}
	if s := bodyStatus(t, rc); s != status {
		t.Errorf("status = %q, want %s", s, status)
	}
}

func TestSpeechHandler_BadJSON(t *testing.T) {
	up := newSpeechUpstream(t, "audio/mpeg", 200)
	h := newSpeechHandlerForTest(t, up.srv.URL, true)

	rc := callJSON(h.Serve, consts.MethodPost, `{`, nil)
	assertSpeechError(t, rc, 400, "invalid_request")
}

func TestSpeechHandler_EmptyInput(t *testing.T) {
	up := newSpeechUpstream(t, "audio/mpeg", 200)
	h := newSpeechHandlerForTest(t, up.srv.URL, true)

	rc := callJSON(h.Serve, consts.MethodPost, `{"input":"   "}`, nil)
	assertSpeechError(t, rc, 400, "invalid_request")
	if up.received() != nil {
		t.Error("input 为空时不应请求上游")
	}
}

func TestSpeechHandler_InputTooLong(t *testing.T) {
	up := newSpeechUpstream(t, "audio/mpeg", 200)
	h := newSpeechHandlerForTest(t, up.srv.URL, true)

	tooLong := strings.Repeat("字", maxSpeechInputRunes+1)
	rc := callJSON(h.Serve, consts.MethodPost, `{"input":"`+tooLong+`"}`, nil)
	assertSpeechError(t, rc, 400, "invalid_request")
	if up.received() != nil {
		t.Error("input 超长时不应请求上游")
	}

	exact := strings.Repeat("字", maxSpeechInputRunes)
	rc = callJSON(h.Serve, consts.MethodPost, `{"input":"`+exact+`"}`, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("恰好 %d 字应通过: status=%d body=%s", maxSpeechInputRunes,
			rc.Response.StatusCode(), rc.Response.Body())
	}
}

func TestSpeechHandler_BadResponseFormat(t *testing.T) {
	up := newSpeechUpstream(t, "audio/mpeg", 200)
	h := newSpeechHandlerForTest(t, up.srv.URL, true)

	rc := callJSON(h.Serve, consts.MethodPost, `{"input":"你好","response_format":"midi"}`, nil)
	assertSpeechError(t, rc, 400, "invalid_request")
}

func TestSpeechHandler_NoDefaultTTS(t *testing.T) {
	up := newSpeechUpstream(t, "audio/mpeg", 200)
	h := newSpeechHandlerForTest(t, up.srv.URL, false)

	rc := callJSON(h.Serve, consts.MethodPost, `{"input":"你好"}`, nil)
	assertSpeechError(t, rc, 400, "invalid_model")
	if !strings.Contains(string(rc.Response.Body()), "TTS") {
		t.Errorf("message 应提示未配置 TTS 模型: %s", rc.Response.Body())
	}
}

func TestSpeechHandler_ExplicitModelWithoutTTSDefault(t *testing.T) {
	up := newSpeechUpstream(t, "audio/mpeg", 200)
	h := newSpeechHandlerForTest(t, up.srv.URL, false)

	rc := callJSON(h.Serve, consts.MethodPost, `{"input":"你好","model":"tts-1"}`, nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if mn := string(rc.Response.Header.Get("X-Model-Name")); mn != "tts-1" {
		t.Errorf("X-Model-Name = %q, want tts-1", mn)
	}
}

func TestSpeechHandler_HeaderModelUsed(t *testing.T) {
	up := newSpeechUpstream(t, "audio/mpeg", 200)
	h := newSpeechHandlerForTest(t, up.srv.URL, false)

	rc := callJSONWithHeader(h.Serve, consts.MethodPost, `{"input":"你好"}`,
		map[string]string{"X-Model-Name": "tts-1"})
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if got := up.received(); got["model"] != "tts-1" {
		t.Errorf("上游收到 model = %v, want tts-1", got["model"])
	}
}

func TestSpeechHandler_BodyModelWinsOverHeader(t *testing.T) {
	up := newSpeechUpstream(t, "audio/mpeg", 200)
	h := newSpeechHandlerForTest(t, up.srv.URL, true)
	err := h.models.Create(context.Background(), &repo.Model{
		Name: "tts-2", Model: "tts-2",
		BaseURL: up.srv.URL, APIKey: "sk-test-1234abcd",
		Enabled: true, Stop: []string{},
	})
	if err != nil {
		t.Fatalf("创建 tts-2: %v", err)
	}

	rc := callJSONWithHeader(h.Serve, consts.MethodPost, `{"input":"你好","model":"tts-2"}`,
		map[string]string{"X-Model-Name": "tts-1"})
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	if got := up.received(); got["model"] != "tts-2" {
		t.Errorf("上游收到 model = %v, want tts-2（请求体优先于请求头）", got["model"])
	}
	if mn := string(rc.Response.Header.Get("X-Model-Name")); mn != "tts-2" {
		t.Errorf("X-Model-Name = %q, want tts-2", mn)
	}
}

func TestSpeechHandler_ExplicitUnknownNoFallback(t *testing.T) {
	up := newSpeechUpstream(t, "audio/mpeg", 200)
	h := newSpeechHandlerForTest(t, up.srv.URL, true)

	rc := callJSON(h.Serve, consts.MethodPost, `{"input":"你好","model":"nope"}`, nil)
	assertSpeechError(t, rc, 400, "invalid_model")
	if up.received() != nil {
		t.Error("显式模型不存在时不应回落到默认模型请求上游")
	}
}

func TestSpeechHandler_UpstreamError(t *testing.T) {
	up := newSpeechUpstream(t, "", 500)
	h := newSpeechHandlerForTest(t, up.srv.URL, true)

	rc := callJSON(h.Serve, consts.MethodPost, `{"input":"你好"}`, nil)
	assertSpeechError(t, rc, 502, "upstream_error")
}
