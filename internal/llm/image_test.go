package llm

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/zfd81/groot/internal/repo"
)

// imageRecord 记录伪上游收到的一次图片解析请求。
type imageRecord struct {
	path          string
	auth          string
	contentLength int64 // 取 r.ContentLength：net/http 把 Content-Length 头解析进该字段，头本身已被移除
	body          []byte
}

// newImageServer 启动伪上游：记录请求后交给 respond 应答。
func newImageServer(t *testing.T, respond func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *imageRecord) {
	t.Helper()
	rec := &imageRecord{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.path = r.URL.Path
		rec.auth = r.Header.Get("Authorization")
		rec.contentLength = r.ContentLength
		rec.body, _ = io.ReadAll(r.Body)
		respond(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func imageModel(baseURL string) *repo.Model {
	return &repo.Model{Model: "qwen-vl-max", BaseURL: baseURL, APIKey: "sk-img"}
}

// 成功响应用的上游报文
const imageOKBody = `{"choices":[{"message":{"role":"assistant","content":"图里是一张发票"}}],
    "usage":{"prompt_tokens":1024,"completion_tokens":48,"total_tokens":1072}}`

// imageRequestBody 是测试里还原请求体的结构，只保留断言关心的字段。
type imageRequestBody struct {
	Model    string `json:"model"`
	Messages []struct {
		Role    string `json:"role"`
		Content []struct {
			Type     string         `json:"type"`
			Text     string         `json:"text"`
			ImageURL map[string]any `json:"image_url"`
		} `json:"content"`
	} `json:"messages"`
}

func decodeImageRequestBody(t *testing.T, body []byte) imageRequestBody {
	t.Helper()
	var got imageRequestBody
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("请求体不是合法 JSON: %v; body=%s", err, truncate(string(body), 200))
	}
	return got
}

func TestUnderstandImage_Success(t *testing.T) {
	srv, rec := newImageServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(imageOKBody))
	})

	const (
		instruction   = "这张图里有什么？"
		base64Content = "iVBORw0KGgoAAAANSUhEUg=="
	)
	text, usage, err := UnderstandImage(context.Background(), imageModel(srv.URL), ImageRequest{
		Instruction: instruction,
		MIMEType:    "image/png",
		Base64:      base64Content,
	})
	if err != nil {
		t.Fatalf("UnderstandImage() error = %v", err)
	}

	if rec.path != "/v1/chat/completions" {
		t.Errorf("path = %q, want /v1/chat/completions", rec.path)
	}
	if rec.auth != "Bearer sk-img" {
		t.Errorf("Authorization = %q, want %q", rec.auth, "Bearer sk-img")
	}

	got := decodeImageRequestBody(t, rec.body)
	if got.Model != "qwen-vl-max" {
		t.Errorf("model = %q, want qwen-vl-max", got.Model)
	}
	if len(got.Messages) != 1 {
		t.Fatalf("messages len = %d, want 1", len(got.Messages))
	}
	if got.Messages[0].Role != "user" {
		t.Errorf("messages[0].role = %q, want user", got.Messages[0].Role)
	}
	parts := got.Messages[0].Content
	if len(parts) != 2 {
		t.Fatalf("content len = %d, want 2", len(parts))
	}
	if parts[0].Type != "text" || parts[0].Text != instruction {
		t.Errorf("content[0] = %+v, want text=%q", parts[0], instruction)
	}
	if parts[1].Type != "image_url" {
		t.Errorf("content[1].type = %q, want image_url", parts[1].Type)
	}
	wantURL := "data:image/png;base64," + base64Content
	if parts[1].ImageURL["url"] != wantURL {
		t.Errorf("content[1].image_url.url = %v, want %q", parts[1].ImageURL["url"], wantURL)
	}
	// 不在 image_url 里透传 detail，由上游取默认。
	if _, ok := parts[1].ImageURL["detail"]; ok {
		t.Errorf("image_url should not contain detail, got %v", parts[1].ImageURL)
	}

	// 采样参数不参与图片解析，请求体顶层不出现这两个键。
	var top map[string]any
	if err := json.Unmarshal(rec.body, &top); err != nil {
		t.Fatalf("unmarshal top: %v", err)
	}
	for _, k := range []string{"temperature", "thinking"} {
		if _, ok := top[k]; ok {
			t.Errorf("请求体不应包含 %q, got %v", k, top[k])
		}
	}

	if text != "图里是一张发票" {
		t.Errorf("text = %q, want %q", text, "图里是一张发票")
	}
	if usage == nil {
		t.Fatalf("usage = nil, want non-nil")
	}
	if usage.PromptTokens != 1024 || usage.CompletionTokens != 48 || usage.TotalTokens != 1072 {
		t.Errorf("usage = %+v, want {1024 48 1072}", *usage)
	}
}

func TestUnderstandImage_InstructionEscaped(t *testing.T) {
	srv, rec := newImageServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(imageOKBody))
	})

	// 指令原样编码进 JSON：引号、反斜杠、换行、制表符都必须转义，
	// 否则拼出的请求体不是合法 JSON，或指令被上游读成别的字符。
	instruction := "读取\"金额\"\\与\n二维码\t信息"
	_, _, err := UnderstandImage(context.Background(), imageModel(srv.URL), ImageRequest{
		Instruction: instruction,
		MIMEType:    "image/png",
		Base64:      "QUJD",
	})
	if err != nil {
		t.Fatalf("UnderstandImage() error = %v", err)
	}

	got := decodeImageRequestBody(t, rec.body)
	if len(got.Messages) != 1 {
		t.Fatalf("messages len = %d, want 1", len(got.Messages))
	}
	parts := got.Messages[0].Content
	if len(parts) != 2 {
		t.Fatalf("content len = %d, want 2", len(parts))
	}
	if parts[0].Type != "text" || parts[0].Text != instruction {
		t.Errorf("content[0] = %+v, want text=%q", parts[0], instruction)
	}
}

func TestUnderstandImage_BaseURLAlreadyHasV1(t *testing.T) {
	srv, rec := newImageServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(imageOKBody))
	})

	_, _, err := UnderstandImage(context.Background(), imageModel(srv.URL+"/v1"), ImageRequest{
		Instruction: "看图",
		MIMEType:    "image/png",
		Base64:      "QUJD",
	})
	if err != nil {
		t.Fatalf("UnderstandImage() error = %v", err)
	}
	if rec.path != "/v1/chat/completions" {
		t.Errorf("path = %q, want /v1/chat/completions", rec.path)
	}
}

func TestUnderstandImage_ContentLengthMatchesBody(t *testing.T) {
	srv, rec := newImageServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(imageOKBody))
	})

	_, _, err := UnderstandImage(context.Background(), imageModel(srv.URL), ImageRequest{
		Instruction: "看图",
		MIMEType:    "image/png",
		Base64:      "QUJDRA==",
	})
	if err != nil {
		t.Fatalf("UnderstandImage() error = %v", err)
	}

	if rec.contentLength <= 0 {
		t.Fatalf("Content-Length = %d, want > 0", rec.contentLength)
	}
	if rec.contentLength != int64(len(rec.body)) {
		t.Errorf("Content-Length = %d, body len = %d, want equal", rec.contentLength, len(rec.body))
	}
}

func TestUnderstandImage_LargeBase64(t *testing.T) {
	srv, rec := newImageServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(imageOKBody))
	})

	// 4 MB 的 Base64 原文（"QUJD" 重复 1<<20 次恰好 4 MiB）。
	base64Content := strings.Repeat("QUJD", 1<<20)
	_, _, err := UnderstandImage(context.Background(), imageModel(srv.URL), ImageRequest{
		Instruction: "看图",
		MIMEType:    "image/jpeg",
		Base64:      base64Content,
	})
	if err != nil {
		t.Fatalf("UnderstandImage() error = %v", err)
	}

	if got := strings.Count(string(rec.body), base64Content); got != 1 {
		t.Errorf("Base64 在请求体中出现 %d 次, want 1", got)
	}
	if !strings.Contains(string(rec.body), "data:image/jpeg;base64,"+base64Content) {
		t.Errorf("请求体缺少 data URL 前缀 data:image/jpeg;base64,")
	}
}

func TestUnderstandImage_MaxCompletionTokens(t *testing.T) {
	cases := []struct {
		name        string
		maxTokens   int
		wantPresent bool
	}{
		{"zero omits", 0, false},
		{"positive sends", 2000, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, rec := newImageServer(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(imageOKBody))
			})
			m := imageModel(srv.URL)
			m.MaxCompletionTokens = c.maxTokens

			_, _, err := UnderstandImage(context.Background(), m, ImageRequest{
				Instruction: "看图",
				MIMEType:    "image/png",
				Base64:      "QUJD",
			})
			if err != nil {
				t.Fatalf("UnderstandImage() error = %v", err)
			}

			var top map[string]any
			if err := json.Unmarshal(rec.body, &top); err != nil {
				t.Fatalf("unmarshal top: %v", err)
			}
			got, ok := top["max_completion_tokens"]
			if ok != c.wantPresent {
				t.Fatalf("max_completion_tokens present = %v, want %v; body=%s",
					ok, c.wantPresent, truncate(string(rec.body), 200))
			}
			if c.wantPresent {
				if v, ok := got.(float64); !ok || int(v) != c.maxTokens {
					t.Errorf("max_completion_tokens = %v, want %d", got, c.maxTokens)
				}
			}
		})
	}
}

func TestUnderstandImage_UsageOmitted(t *testing.T) {
	srv, _ := newImageServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"一张图"}}]}`))
	})

	text, usage, err := UnderstandImage(context.Background(), imageModel(srv.URL), ImageRequest{
		Instruction: "看图",
		MIMEType:    "image/png",
		Base64:      "QUJD",
	})
	if err != nil {
		t.Fatalf("UnderstandImage() error = %v", err)
	}
	if text != "一张图" {
		t.Errorf("text = %q, want 一张图", text)
	}
	if usage != nil {
		t.Errorf("usage = %+v, want nil", *usage)
	}
}

func TestUnderstandImage_TotalTokensBackfill(t *testing.T) {
	cases := []struct {
		name       string
		usageJSON  string
		wantTotal  int
		wantPrompt int
		wantCompl  int
	}{
		{
			name:       "missing total backfilled",
			usageJSON:  `{"prompt_tokens":10,"completion_tokens":5}`,
			wantTotal:  15,
			wantPrompt: 10,
			wantCompl:  5,
		},
		{
			name:       "explicit total kept",
			usageJSON:  `{"prompt_tokens":10,"completion_tokens":5,"total_tokens":99}`,
			wantTotal:  99,
			wantPrompt: 10,
			wantCompl:  5,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			srv, _ := newImageServer(t, func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"图"}}],"usage":` + c.usageJSON + `}`))
			})

			_, usage, err := UnderstandImage(context.Background(), imageModel(srv.URL), ImageRequest{
				Instruction: "看图",
				MIMEType:    "image/png",
				Base64:      "QUJD",
			})
			if err != nil {
				t.Fatalf("UnderstandImage() error = %v", err)
			}
			if usage == nil {
				t.Fatalf("usage = nil, want non-nil")
			}
			if usage.TotalTokens != c.wantTotal {
				t.Errorf("TotalTokens = %d, want %d", usage.TotalTokens, c.wantTotal)
			}
			if usage.PromptTokens != c.wantPrompt {
				t.Errorf("PromptTokens = %d, want %d", usage.PromptTokens, c.wantPrompt)
			}
			if usage.CompletionTokens != c.wantCompl {
				t.Errorf("CompletionTokens = %d, want %d", usage.CompletionTokens, c.wantCompl)
			}
		})
	}
}

func TestUnderstandImage_UpstreamError(t *testing.T) {
	srv, _ := newImageServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":{"message":"rate limited"}}`))
	})

	_, _, err := UnderstandImage(context.Background(), imageModel(srv.URL), ImageRequest{
		Instruction: "看图",
		MIMEType:    "image/png",
		Base64:      "QUJD",
	})
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("err = %v, want ErrUpstream", err)
	}
	if !strings.Contains(err.Error(), "rate limited") {
		t.Errorf("err = %q, want to contain %q", err.Error(), "rate limited")
	}
}

func TestUnderstandImage_InvalidJSON(t *testing.T) {
	srv, _ := newImageServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("not json"))
	})

	_, _, err := UnderstandImage(context.Background(), imageModel(srv.URL), ImageRequest{
		Instruction: "看图",
		MIMEType:    "image/png",
		Base64:      "QUJD",
	})
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("err = %v, want ErrUpstream", err)
	}
}

func TestUnderstandImage_EmptyContent(t *testing.T) {
	srv, _ := newImageServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"  "}}]}`))
	})

	text, _, err := UnderstandImage(context.Background(), imageModel(srv.URL), ImageRequest{
		Instruction: "看图",
		MIMEType:    "image/png",
		Base64:      "QUJD",
	})
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("err = %v, want ErrUpstream", err)
	}
	if text != "" {
		t.Errorf("text = %q, want empty on error", text)
	}
}

func TestUnderstandImage_Base64WithNewlines(t *testing.T) {
	srv, rec := newImageServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(imageOKBody))
	})

	// base64 命令行的默认输出按 76 列折行，行间是 \r\n。
	// 请求体必须是合法 JSON：裸控制字符不能出现在 JSON 字符串里。
	const raw = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
	var wrapped strings.Builder
	for i := 0; i < len(raw); i += 76 {
		end := i + 76
		if end > len(raw) {
			end = len(raw)
		}
		wrapped.WriteString(raw[i:end])
		wrapped.WriteString("\r\n")
	}

	_, _, err := UnderstandImage(context.Background(), imageModel(srv.URL), ImageRequest{
		Instruction: "看图",
		MIMEType:    "image/png",
		Base64:      wrapped.String(),
	})
	if err != nil {
		t.Fatalf("UnderstandImage() error = %v", err)
	}

	var top struct {
		Messages []struct {
			Content []struct {
				ImageURL struct {
					URL string `json:"url"`
				} `json:"image_url"`
			} `json:"content"`
		} `json:"messages"`
	}
	if err := json.Unmarshal(rec.body, &top); err != nil {
		t.Fatalf("请求体不是合法 JSON: %v; body=%s", err, rec.body)
	}
	parts := top.Messages[0].Content
	if len(parts) != 2 {
		t.Fatalf("content len = %d, want 2", len(parts))
	}
	wantURL := "data:image/png;base64," + raw
	if parts[1].ImageURL.URL != wantURL {
		t.Errorf("image_url.url = %q, want %q", parts[1].ImageURL.URL, wantURL)
	}
	// 剔除换行后的长度也要与 Content-Length 一致，否则会退化成 chunked 或截断。
	if rec.contentLength != int64(len(rec.body)) {
		t.Errorf("Content-Length = %d, body len = %d, want equal", rec.contentLength, len(rec.body))
	}
}

func TestUnderstandImage_ContextCanceled(t *testing.T) {
	srv, _ := newImageServer(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(imageOKBody))
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, _, err := UnderstandImage(ctx, imageModel(srv.URL), ImageRequest{
		Instruction: "看图",
		MIMEType:    "image/png",
		Base64:      "QUJD",
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if errors.Is(err, ErrUpstream) {
		t.Errorf("err = %v, 调用方取消不应被包装成 ErrUpstream", err)
	}
}
