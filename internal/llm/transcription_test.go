package llm

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/repo"
)

func TestTranscribe_Success(t *testing.T) {
	var gotPath, gotAuth, gotModel, gotLang, gotFilename, gotFileBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		gotModel = r.FormValue("model")
		gotLang = r.FormValue("language")
		f, hdr, err := r.FormFile("file")
		if err != nil {
			t.Errorf("FormFile: %v", err)
		} else {
			defer f.Close()
			gotFilename = hdr.Filename
			b, _ := io.ReadAll(f)
			gotFileBody = string(b)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"text":"帮我看一下登录接口的日志"}`))
	}))
	defer srv.Close()

	m := &repo.Model{BaseURL: srv.URL, APIKey: "sk-test", Model: "whisper-1"}
	text, err := Transcribe(context.Background(), m,
		strings.NewReader("FAKE-AUDIO"), "rec.webm", "zh")
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}

	if text != "帮我看一下登录接口的日志" {
		t.Errorf("text = %q", text)
	}
	if gotPath != "/v1/audio/transcriptions" {
		t.Errorf("path = %q, want /v1/audio/transcriptions（base_url 缺 /v1 时应补齐）", gotPath)
	}
	if gotAuth != "Bearer sk-test" {
		t.Errorf("Authorization = %q", gotAuth)
	}
	if gotModel != "whisper-1" {
		t.Errorf("model = %q, want whisper-1", gotModel)
	}
	if gotLang != "zh" {
		t.Errorf("language = %q, want zh", gotLang)
	}
	if gotFilename != "rec.webm" {
		t.Errorf("filename = %q, want rec.webm", gotFilename)
	}
	if gotFileBody != "FAKE-AUDIO" {
		t.Errorf("file body = %q, want FAKE-AUDIO", gotFileBody)
	}
}

func TestTranscribe_BaseURLAlreadyHasV1(t *testing.T) {
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		w.Write([]byte(`{"text":"ok"}`))
	}))
	defer srv.Close()

	m := &repo.Model{BaseURL: srv.URL + "/v1", APIKey: "k", Model: "whisper-1"}
	if _, err := Transcribe(context.Background(), m, strings.NewReader("x"), "a.webm", ""); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if gotPath != "/v1/audio/transcriptions" {
		t.Errorf("path = %q, want /v1/audio/transcriptions（不应重复补 /v1）", gotPath)
	}
}

func TestTranscribe_LanguageOmittedWhenEmpty(t *testing.T) {
	var hasLang bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseMultipartForm(1 << 20)
		_, hasLang = r.MultipartForm.Value["language"]
		w.Write([]byte(`{"text":"ok"}`))
	}))
	defer srv.Close()

	m := &repo.Model{BaseURL: srv.URL, APIKey: "k", Model: "whisper-1"}
	if _, err := Transcribe(context.Background(), m, strings.NewReader("x"), "a.webm", ""); err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	if hasLang {
		t.Error("language 为空时不应下发该字段")
	}
}

func TestTranscribe_UpstreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		w.Write([]byte(`{"error":{"message":"Incorrect API key provided"}}`))
	}))
	defer srv.Close()

	m := &repo.Model{BaseURL: srv.URL, APIKey: "bad", Model: "whisper-1"}
	_, err := Transcribe(context.Background(), m, strings.NewReader("x"), "a.webm", "")
	if err == nil {
		t.Fatal("上游 401 应返回错误")
	}
	if !errors.Is(err, ErrUpstream) {
		t.Errorf("err 应包装 ErrUpstream，得到 %v", err)
	}
	if !strings.Contains(err.Error(), "Incorrect API key provided") {
		t.Errorf("错误信息应透传上游原文，得到 %q", err.Error())
	}
}

func TestTranscribe_EmptyTextIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"text":""}`))
	}))
	defer srv.Close()

	m := &repo.Model{BaseURL: srv.URL, APIKey: "k", Model: "whisper-1"}
	_, err := Transcribe(context.Background(), m, strings.NewReader("x"), "a.webm", "")
	if !errors.Is(err, ErrEmptyTranscript) {
		t.Errorf("err = %v, want ErrEmptyTranscript", err)
	}
}

func TestTranscriptionURL(t *testing.T) {
	const want = "http://x/v1/audio/transcriptions"
	for _, in := range []string{"http://x", "http://x/", "http://x/v1", "http://x/v1/"} {
		if got := transcriptionURL(in); got != want {
			t.Errorf("transcriptionURL(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestUpstreamMessage(t *testing.T) {
	cases := []struct {
		name   string
		body   string
		status int
		want   string
	}{
		{"html 错误页退回原文", `<html>502</html>`, 502, "<html>"},
		{"空响应体给出状态码", "", 500, "HTTP 500"},
		{"error 非对象时退回原文", `{"error":"str"}`, 400, `{"error":"str"}`},
		{"取 error.message", `{"error":{"message":"m"}}`, 400, "m"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := upstreamMessage([]byte(c.body), c.status)
			if !strings.Contains(got, c.want) {
				t.Errorf("upstreamMessage(%q, %d) = %q, want contains %q", c.body, c.status, got, c.want)
			}
		})
	}
}

func TestTranscribe_NonJSONSuccessBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	m := &repo.Model{BaseURL: srv.URL, APIKey: "k", Model: "whisper-1"}
	_, err := Transcribe(context.Background(), m, strings.NewReader("x"), "a.webm", "")
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("err = %v, want ErrUpstream", err)
	}
	if !strings.Contains(err.Error(), "响应不是合法 JSON") {
		t.Errorf("错误信息应说明响应不是合法 JSON，得到 %q", err.Error())
	}
}

func TestTranscribe_WhitespaceTextIsError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"text":"   "}`))
	}))
	defer srv.Close()

	m := &repo.Model{BaseURL: srv.URL, APIKey: "k", Model: "whisper-1"}
	_, err := Transcribe(context.Background(), m, strings.NewReader("x"), "a.webm", "")
	if !errors.Is(err, ErrEmptyTranscript) {
		t.Errorf("err = %v, want ErrEmptyTranscript", err)
	}
}

func TestTranscribe_ContextCanceled(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 先读完请求体：http.Server 只有在请求体读到 EOF 后才会启动后台读，
		// 进而在客户端断开时取消 r.Context()；否则 srv.Close() 会永远等待本 handler。
		io.Copy(io.Discard, r.Body)
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	m := &repo.Model{BaseURL: srv.URL, APIKey: "k", Model: "whisper-1"}
	_, err := Transcribe(ctx, m, strings.NewReader("x"), "a.webm", "")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
}
