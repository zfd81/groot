package llm

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudwego/eino/schema"

	"github.com/zfd81/groot/internal/repo"
)

func TestOpenAIBaseURL(t *testing.T) {
	cases := map[string]string{
		"http://x":        "http://x/v1",
		"http://x/":       "http://x/v1",
		"http://x/v1":     "http://x/v1",
		"http://x/v1/":    "http://x/v1",
		"http://x:8111":   "http://x:8111/v1",
		"http://x/api/v1": "http://x/api/v1",
	}
	for in, want := range cases {
		if got := openAIBaseURL(in); got != want {
			t.Errorf("openAIBaseURL(%q) = %q, want %q", in, got, want)
		}
	}
}

// 对话请求的实际路径：base_url 带不带 /v1 都应落到 /v1/chat/completions
func TestNewChatModel_BaseURLV1(t *testing.T) {
	for _, suffix := range []string{"", "/", "/v1", "/v1/"} {
		var gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"id":"c1","object":"chat.completion","created":1,"model":"m",` +
				`"choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}]}`))
		}))

		m := &repo.Model{BaseURL: srv.URL + suffix, APIKey: "k", Model: "m"}
		cm, err := NewChatModel(context.Background(), m, 0)
		if err != nil {
			srv.Close()
			t.Fatalf("NewChatModel(%q): %v", suffix, err)
		}
		if _, err = cm.Generate(context.Background(), []*schema.Message{schema.UserMessage("hi")}); err != nil {
			t.Errorf("Generate(base_url 后缀 %q): %v", suffix, err)
		}
		if gotPath != "/v1/chat/completions" {
			t.Errorf("base_url 后缀 %q：path = %q, want /v1/chat/completions", suffix, gotPath)
		}
		srv.Close()
	}
}

// 连接测试与对话使用同一前缀：base_url 缺 /v1 时探测 /v1/models
func TestCheckConnection_BaseURLV1(t *testing.T) {
	for _, suffix := range []string{"", "/v1"} {
		var gotPath string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			gotPath = r.URL.Path
			w.Write([]byte(`{"data":[]}`))
		}))
		status, msg := CheckConnection(&repo.Model{BaseURL: srv.URL + suffix, APIKey: "k"})
		srv.Close()
		if status != "healthy" {
			t.Errorf("base_url 后缀 %q：status = %q (%s)", suffix, status, msg)
		}
		if gotPath != "/v1/models" {
			t.Errorf("base_url 后缀 %q：path = %q, want /v1/models", suffix, gotPath)
		}
	}
}
