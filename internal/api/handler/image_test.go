package handler

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
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

// newImageHandlerForTest 建一套真实的仓库与配置对象；upstreamURL 非空时创建名为 qwen-vl-max 的模型
// （库中首个模型，自动成为默认对话模型且启用），其 base_url 指向 httptest 假上游；
// defaultVision 为 true 时把它设为默认视觉模型。
func newImageHandlerForTest(t *testing.T, upstreamURL string, defaultVision bool) *ImageHandler {
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
			Name: "qwen-vl-max", Model: "qwen-vl-max",
			BaseURL: upstreamURL, APIKey: "sk-test-1234abcd",
			Enabled: true, Stop: []string{},
		})
		if err != nil {
			t.Fatalf("创建测试模型: %v", err)
		}
		if defaultVision {
			if err := models.SetDefault(ctx, "qwen-vl-max", repo.DefaultVision); err != nil {
				t.Fatalf("设置默认视觉模型: %v", err)
			}
		}
	}

	settings := setting.New(config.Bootstrap{}, settingdb.New(sqlxDB, dialect))
	return NewImageHandler(settings, models, logger.NewNop())
}

// imageUpstreamText 假上游成功时报文里的 text，供成功用例逐字比对。
const imageUpstreamText = "图里是一张发票"

// imageUpstream 记录假上游收到的请求体（供断言 instruction 与 data URL）。
type imageUpstream struct {
	mu   sync.Mutex
	body []byte
}

// newImageUpstream 启动假上游：记录请求体，返回 text 与 usage 固定的成功报文。
func newImageUpstream(t *testing.T) (*httptest.Server, *imageUpstream) {
	t.Helper()
	up := &imageUpstream{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		up.mu.Lock()
		up.body = raw
		up.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"` + imageUpstreamText + `"}}],` +
			`"usage":{"prompt_tokens":1024,"completion_tokens":48,"total_tokens":1072}}`))
	}))
	t.Cleanup(srv.Close)
	return srv, up
}

// received 返回假上游收到的请求体副本。
func (u *imageUpstream) received() []byte {
	u.mu.Lock()
	defer u.mu.Unlock()
	return u.body
}

// imageBody 拼一个单图请求体；content 直接嵌入（不转义），便于构造非法 JSON 之外的用例。
// instruction 为空串时省略该字段，代表调用方未传指令。
func imageBody(instruction, typ, name, content string) string {
	ins := ""
	if instruction != "" {
		ins = `"instruction":"` + instruction + `",`
	}
	return `{` + ins + `"attachments":[{"type":"` + typ + `","name":"` + name +
		`","content":"` + content + `"}]}`
}

// 可用的测试图片 Base64（均已实测）
const (
	pngBase64   = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAIAAACQd1PeAAAADElEQVR4nGP4z8AAAAMBAQDJ/pLvAAAAAElFTkSuQmCC" // 嗅探 image/png
	icoBase64   = "AAABAAEA"                                                                                     // 嗅探 image/x-icon
	octetBase64 = "AQIDBAUGBwg="                                                                                 // 嗅探 application/octet-stream
	textBase64  = "aGVsbG8gd29ybGQ="                                                                             // 嗅探 text/plain; charset=utf-8
)

// postImage 以 POST JSON 调用图片解析 handler，headers 可为 nil。
func postImage(h *ImageHandler, body string, headers map[string]string) *app.RequestContext {
	return callJSONWithHeader(h.Serve, consts.MethodPost, body, headers)
}

// callJSONWithContext 与 callJSONWithHeader 相同，但用调用方给定的 ctx 调 handler，
// 供「调用方中断请求」这类需要取消上下文的用例使用（callJSONWithHeader 固定用 Background）。
func callJSONWithContext(ctx context.Context, h func(context.Context, *app.RequestContext),
	method, body string, headers map[string]string) *app.RequestContext {
	rc := app.NewContext(0)
	rc.Request.Header.SetMethod(method)
	if body != "" {
		rc.Request.SetBody([]byte(body))
		rc.Request.Header.SetContentTypeBytes([]byte("application/json"))
	}
	for k, v := range headers {
		rc.Request.Header.Set(k, v)
	}
	h(ctx, rc)
	return rc
}

// bodyMessage 解析错误响应中的 message 字段。
func bodyMessage(t *testing.T, rc *app.RequestContext) string {
	t.Helper()
	var out struct {
		Message string `json:"message"`
	}
	if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
		t.Fatalf("响应不是合法 JSON: %s", rc.Response.Body())
	}
	return out.Message
}

// assertImageError 断言响应状态码与 status 字段。
func assertImageError(t *testing.T, rc *app.RequestContext, code int, status string) {
	t.Helper()
	if rc.Response.StatusCode() != code {
		t.Fatalf("status=%d, want %d; body=%s", rc.Response.StatusCode(), code, rc.Response.Body())
	}
	if s := bodyStatus(t, rc); s != status {
		t.Errorf("status = %q, want %s", s, status)
	}
}

// upstreamImageRequest 取出假上游收到的请求里的 user 消息文本与图片 data URL。
func upstreamImageRequest(t *testing.T, raw []byte) (text, dataURL string) {
	t.Helper()
	var out struct {
		Messages []struct {
			Role    string `json:"role"`
			Content []struct {
				Type     string `json:"type"`
				Text     string `json:"text"`
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("上游请求体不是合法 JSON: %v; body=%s", err, raw)
	}
	if len(out.Messages) != 1 || len(out.Messages[0].Content) != 2 {
		t.Fatalf("上游请求体消息形状不符: %s", raw)
	}
	return out.Messages[0].Content[0].Text, out.Messages[0].Content[1].ImageURL.URL
}

func TestImageHandler_BadJSON(t *testing.T) {
	srv, _ := newImageUpstream(t)
	h := newImageHandlerForTest(t, srv.URL, true)

	rc := postImage(h, `{not json`, nil)
	assertImageError(t, rc, 400, "invalid_request")
}

func TestImageHandler_AttachmentCount(t *testing.T) {
	srv, _ := newImageUpstream(t)
	h := newImageHandlerForTest(t, srv.URL, true)

	bodies := map[string]string{
		"缺省": `{}`,
		"两张": `{"attachments":[` +
			`{"type":"image","name":"a.png","content":"` + pngBase64 + `"},` +
			`{"type":"image","name":"b.png","content":"` + pngBase64 + `"}]}`,
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			rc := postImage(h, body, nil)
			assertImageError(t, rc, 400, "invalid_request")
			if msg := bodyMessage(t, rc); !strings.Contains(msg, "必须提供 1 张图片附件") {
				t.Errorf("message = %q, 应含「必须提供 1 张图片附件」", msg)
			}
		})
	}
}

func TestImageHandler_NotImageType(t *testing.T) {
	srv, _ := newImageUpstream(t)
	h := newImageHandlerForTest(t, srv.URL, true)

	for _, typ := range []string{"audio", "file"} {
		t.Run(typ, func(t *testing.T) {
			rc := postImage(h, imageBody("", typ, "a.png", pngBase64), nil)
			assertImageError(t, rc, 400, "attachment_invalid_type")
		})
	}
}

func TestImageHandler_MissingName(t *testing.T) {
	srv, _ := newImageUpstream(t)
	h := newImageHandlerForTest(t, srv.URL, true)

	rc := postImage(h, imageBody("", "image", "", pngBase64), nil)
	assertImageError(t, rc, 400, "attachment_missing_name")
}

func TestImageHandler_DecodeError(t *testing.T) {
	srv, _ := newImageUpstream(t)
	h := newImageHandlerForTest(t, srv.URL, true)

	rc := postImage(h, imageBody("", "image", "a.png", "aGVs bG8="), nil)
	assertImageError(t, rc, 400, "attachment_decode_error")
}

// TestImageHandler_Base64WithNewlines 按 76 列换行的 Base64 与 /chat 同口径，应被接受。
func TestImageHandler_Base64WithNewlines(t *testing.T) {
	srv, up := newImageUpstream(t)
	h := newImageHandlerForTest(t, srv.URL, true)

	// JSON 字符串里换行必须转义，故写入 \r\n 的转义形式，解出的 content 含真实 \r\n
	wrapped := pngBase64[:76] + `\r\n` + pngBase64[76:]
	rc := postImage(h, imageBody("", "image", "a.png", wrapped), nil)

	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	_, dataURL := upstreamImageRequest(t, up.received())
	if !strings.HasPrefix(dataURL, "data:image/png;base64,") {
		t.Errorf("data URL = %.40q, want 前缀 data:image/png;base64,", dataURL)
	}
}

// TestImageHandler_TextRenamedPNG 扩展名兜底只服务于「嗅探不出类型」，
// 把明确是文本的内容改名成 .png 仍应被拒。
func TestImageHandler_TextRenamedPNG(t *testing.T) {
	srv, _ := newImageUpstream(t)
	h := newImageHandlerForTest(t, srv.URL, true)

	rc := postImage(h, imageBody("", "image", "a.png", textBase64), nil)
	assertImageError(t, rc, 400, "unsupported_type")
}

// TestImageHandler_OctetStreamExtensionFallback 嗅探不出类型时按扩展名兜底映射。
func TestImageHandler_OctetStreamExtensionFallback(t *testing.T) {
	srv, up := newImageUpstream(t)
	h := newImageHandlerForTest(t, srv.URL, true)

	rc := postImage(h, imageBody("", "image", "a.png", octetBase64), nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	_, dataURL := upstreamImageRequest(t, up.received())
	if !strings.HasPrefix(dataURL, "data:image/png;base64,") {
		t.Errorf("data URL = %.40q, want 前缀 data:image/png;base64,", dataURL)
	}
}

// TestImageHandler_OctetStreamUnknownExtension 嗅探不出类型且扩展名映射不到时拒绝，并列出支持的扩展名。
func TestImageHandler_OctetStreamUnknownExtension(t *testing.T) {
	srv, _ := newImageUpstream(t)
	h := newImageHandlerForTest(t, srv.URL, true)

	rc := postImage(h, imageBody("", "image", "a.tiff", octetBase64), nil)
	assertImageError(t, rc, 400, "unsupported_type")
	if msg := bodyMessage(t, rc); !strings.Contains(msg, ".png") {
		t.Errorf("message = %q, 应列出支持的扩展名（含 .png）", msg)
	}
}

// TestImageHandler_ICOSniffed 嗅探出 image/x-icon 即接受，不套白名单。
func TestImageHandler_ICOSniffed(t *testing.T) {
	srv, up := newImageUpstream(t)
	h := newImageHandlerForTest(t, srv.URL, true)

	rc := postImage(h, imageBody("", "image", "a.ico", icoBase64), nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	_, dataURL := upstreamImageRequest(t, up.received())
	if !strings.HasPrefix(dataURL, "data:image/x-icon;base64,") {
		t.Errorf("data URL = %.40q, want 前缀 data:image/x-icon;base64,", dataURL)
	}
}

// TestImageHandler_SizeExceeded 附件上限设为 1 MB 时提交约 2 MB Base64，应触发单文件超限。
func TestImageHandler_SizeExceeded(t *testing.T) {
	srv, _ := newImageUpstream(t)
	h := newImageHandlerForTest(t, srv.URL, true)

	rt := h.settings.RuntimeStatic()
	rt.Attachment.MaxSize = 1 // MB
	if err := h.settings.SetRuntime(context.Background(), rt); err != nil {
		t.Fatalf("SetRuntime: %v", err)
	}

	big := strings.Repeat("A", 2<<20)
	rc := postImage(h, imageBody("", "image", "a.png", big), nil)
	assertImageError(t, rc, 400, "attachment_size_exceeded")
}

// TestImageHandler_InvalidModel 模型不可用的三种来源统一 400 invalid_model，且不回落到默认视觉模型。
func TestImageHandler_InvalidModel(t *testing.T) {
	srv, _ := newImageUpstream(t)

	cases := []struct {
		name    string
		header  string
		handler func(t *testing.T) *ImageHandler
	}{
		{
			name:   "显式模型不存在",
			header: "__nope__",
			handler: func(t *testing.T) *ImageHandler {
				return newImageHandlerForTest(t, srv.URL, true)
			},
		},
		{
			name:   "显式模型已禁用",
			header: "qwen-vl-max",
			handler: func(t *testing.T) *ImageHandler {
				h := newImageHandlerForTest(t, "", false)
				ctx := context.Background()
				// 先建 seed-default（首个模型自动成为默认对话模型并启用），
				// 再建禁用的 qwen-vl-max，否则「首个模型强制启用」会覆盖 Enabled: false。
				for _, m := range []*repo.Model{
					{Name: "seed-default", Model: "seed-default",
						BaseURL: srv.URL, APIKey: "sk-test-1234abcd", Enabled: true, Stop: []string{}},
					{Name: "qwen-vl-max", Model: "qwen-vl-max",
						BaseURL: srv.URL, APIKey: "sk-test-1234abcd", Enabled: false, Stop: []string{}},
				} {
					if err := h.models.Create(ctx, m); err != nil {
						t.Fatalf("创建模型 %s: %v", m.Name, err)
					}
				}
				return h
			},
		},
		{
			name:   "未配置默认视觉模型",
			header: "",
			handler: func(t *testing.T) *ImageHandler {
				return newImageHandlerForTest(t, srv.URL, false)
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := c.handler(t)
			var headers map[string]string
			if c.header != "" {
				headers = map[string]string{"X-Model-Name": c.header}
			}
			rc := postImage(h, imageBody("", "image", "a.png", pngBase64), headers)
			assertImageError(t, rc, 400, "invalid_model")
			if msg := bodyMessage(t, rc); msg == "" {
				t.Error("message 不应为空")
			}
		})
	}
}

// TestImageHandler_ModelHeader 显式指定模型不要求它持有 vision 默认。
func TestImageHandler_ModelHeader(t *testing.T) {
	srv, _ := newImageUpstream(t)
	h := newImageHandlerForTest(t, srv.URL, false)

	rc := postImage(h, imageBody("", "image", "a.png", pngBase64),
		map[string]string{"X-Model-Name": "qwen-vl-max"})
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
}

// TestImageHandler_DefaultInstruction 未传指令或只传空白时下发默认指令。
func TestImageHandler_DefaultInstruction(t *testing.T) {
	bodies := map[string]string{
		"未传":  imageBody("", "image", "a.png", pngBase64),
		"全空白": imageBody("   ", "image", "a.png", pngBase64),
	}
	for name, body := range bodies {
		t.Run(name, func(t *testing.T) {
			srv, up := newImageUpstream(t)
			h := newImageHandlerForTest(t, srv.URL, true)

			rc := postImage(h, body, nil)
			if rc.Response.StatusCode() != 200 {
				t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
			}
			text, _ := upstreamImageRequest(t, up.received())
			if text != defaultInstruction {
				t.Errorf("上游收到 text = %q, want 默认指令", text)
			}
		})
	}
}

// TestImageHandler_CustomInstruction 传入指令时原样下发，不叠加默认指令。
func TestImageHandler_CustomInstruction(t *testing.T) {
	srv, up := newImageUpstream(t)
	h := newImageHandlerForTest(t, srv.URL, true)

	rc := postImage(h, imageBody("提取发票字段", "image", "a.png", pngBase64), nil)
	if rc.Response.StatusCode() != 200 {
		t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
	}
	text, _ := upstreamImageRequest(t, up.received())
	if text != "提取发票字段" {
		t.Errorf("上游收到 text = %q, want 提取发票字段", text)
	}
	if strings.Contains(text, "请详细描述") {
		t.Errorf("text = %q, 不应叠加默认指令", text)
	}
}

func TestImageHandler_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(500)
		w.Write([]byte(`{"error":{"message":"boom"}}`))
	}))
	defer srv.Close()

	h := newImageHandlerForTest(t, srv.URL, true)
	rc := postImage(h, imageBody("", "image", "a.png", pngBase64), nil)

	assertImageError(t, rc, 502, "upstream_error")
	if msg := bodyMessage(t, rc); !strings.Contains(msg, "boom") {
		t.Errorf("message = %q, 应透传上游原文", msg)
	}
}

// TestImageHandler_CallerCancelled 调用方中断请求时，响应必须与「上游故障」分开：
// 不能报成 502 upstream_error，否则日志与响应都会把「请求被放弃」误读成「上游坏了」。
func TestImageHandler_CallerCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	started := make(chan struct{})
	unblock := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(started)
		<-unblock
	}))
	defer srv.Close()
	defer close(unblock) // 保证上游协程一定能退出，测试不会卡在 srv.Close

	h := newImageHandlerForTest(t, srv.URL, true)

	// 上游确实收到请求之后再取消，模拟调用方在解析过程中离开。
	go func() {
		<-started
		cancel()
	}()

	rc := callJSONWithContext(ctx, h.Serve, consts.MethodPost,
		imageBody("", "image", "a.png", pngBase64), nil)
	assertImageError(t, rc, 499, "cancelled")
}

func TestImageHandler_Success(t *testing.T) {
	t.Run("含 usage", func(t *testing.T) {
		srv, _ := newImageUpstream(t)
		h := newImageHandlerForTest(t, srv.URL, true)

		rc := postImage(h, imageBody("", "image", "a.png", pngBase64), nil)
		if rc.Response.StatusCode() != 200 {
			t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
		}
		var out struct {
			Text  string `json:"text"`
			Model string `json:"model"`
			Usage *struct {
				PromptTokens     int `json:"prompt_tokens"`
				CompletionTokens int `json:"completion_tokens"`
				TotalTokens      int `json:"total_tokens"`
			} `json:"usage"`
		}
		if err := json.Unmarshal(rc.Response.Body(), &out); err != nil {
			t.Fatalf("解析响应: %v", err)
		}
		if out.Text != imageUpstreamText {
			t.Errorf("text = %q, want %q（应逐字返回上游原文）", out.Text, imageUpstreamText)
		}
		if out.Model != "qwen-vl-max" {
			t.Errorf("model = %q, want qwen-vl-max", out.Model)
		}
		if out.Usage == nil || out.Usage.TotalTokens != 1072 {
			t.Errorf("usage = %+v, want total_tokens=1072", out.Usage)
		}
	})

	t.Run("上游无 usage", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"` + imageUpstreamText + `"}}]}`))
		}))
		defer srv.Close()

		h := newImageHandlerForTest(t, srv.URL, true)
		rc := postImage(h, imageBody("", "image", "a.png", pngBase64), nil)
		if rc.Response.StatusCode() != 200 {
			t.Fatalf("status=%d body=%s", rc.Response.StatusCode(), rc.Response.Body())
		}
		if strings.Contains(string(rc.Response.Body()), `"usage"`) {
			t.Errorf("上游未返回 usage 时响应不应含该键: %s", rc.Response.Body())
		}
	})
}
