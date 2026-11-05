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

// TestTranscribe_ForwardsAllFields 验证除文件外的表单参数逐项转发，
// timestamp_granularities[] 每项写一个同名字段。
func TestTranscribe_ForwardsAllFields(t *testing.T) {
	var gotPath, gotAuth, gotModel, gotLang, gotPrompt, gotFormat, gotTemp, gotFilename, gotFileBody string
	var gotGranularities []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
		}
		gotModel = r.FormValue("model")
		gotLang = r.FormValue("language")
		gotPrompt = r.FormValue("prompt")
		gotFormat = r.FormValue("response_format")
		gotTemp = r.FormValue("temperature")
		gotGranularities = r.MultipartForm.Value["timestamp_granularities[]"]
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
		w.Write([]byte(`{"text":"ok"}`))
	}))
	defer srv.Close()

	m := &repo.Model{BaseURL: srv.URL, APIKey: "sk-test", Model: "whisper-1"}
	resp, err := Transcribe(context.Background(), m, strings.NewReader("FAKE-AUDIO"), "rec.webm",
		TranscriptionParams{
			Model:                  "whisper-1",
			Language:               "zh",
			Prompt:                 "这是一段登录日志的录音",
			ResponseFormat:         "verbose_json",
			Temperature:            "0.2",
			TimestampGranularities: []string{"word", "segment"},
		})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	defer resp.Body.Close()

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
	if gotPrompt != "这是一段登录日志的录音" {
		t.Errorf("prompt = %q", gotPrompt)
	}
	if gotFormat != "verbose_json" {
		t.Errorf("response_format = %q, want verbose_json", gotFormat)
	}
	if gotTemp != "0.2" {
		t.Errorf("temperature = %q, want 0.2", gotTemp)
	}
	if strings.Join(gotGranularities, ",") != "word,segment" {
		t.Errorf("timestamp_granularities[] = %v, want [word segment]", gotGranularities)
	}
	if gotFilename != "rec.webm" {
		t.Errorf("filename = %q, want rec.webm", gotFilename)
	}
	if gotFileBody != "FAKE-AUDIO" {
		t.Errorf("file body = %q, want FAKE-AUDIO", gotFileBody)
	}
}

// TestTranscribe_ReturnsRawBodyAndStatus 上游返回什么就原样交回，不解析、不改写。
func TestTranscribe_ReturnsRawBodyAndStatus(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		w.Write([]byte("hi"))
	}))
	defer srv.Close()

	m := &repo.Model{BaseURL: srv.URL, APIKey: "k", Model: "whisper-1"}
	resp, err := Transcribe(context.Background(), m, strings.NewReader("x"), "a.webm", TranscriptionParams{Model: "whisper-1"})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		t.Errorf("StatusCode = %d, want 200", resp.StatusCode)
	}
	if resp.ContentType != "text/plain" {
		t.Errorf("ContentType = %q, want text/plain", resp.ContentType)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("读取响应体: %v", err)
	}
	if string(body) != "hi" {
		t.Errorf("body = %q, want hi", body)
	}
}

// TestTranscribe_Non2xxReturnedNotError 非 2xx 也原样返回，由调用方决定如何转发。
func TestTranscribe_Non2xxReturnedNotError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		w.Write([]byte(`{"error":{"message":"bad audio"}}`))
	}))
	defer srv.Close()

	m := &repo.Model{BaseURL: srv.URL, APIKey: "k", Model: "whisper-1"}
	resp, err := Transcribe(context.Background(), m, strings.NewReader("x"), "a.webm", TranscriptionParams{Model: "whisper-1"})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != 400 {
		t.Errorf("StatusCode = %d, want 400", resp.StatusCode)
	}
	body, err := UpstreamBody(resp.Body)
	if err != nil {
		t.Fatalf("UpstreamBody: %v", err)
	}
	if string(body) != `{"error":{"message":"bad audio"}}` {
		t.Errorf("body = %q", body)
	}
}

// TestTranscribe_ResponseFormatDefaultsToJSON response_format 为空时按 json 下发。
func TestTranscribe_ResponseFormatDefaultsToJSON(t *testing.T) {
	var gotFormat string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseMultipartForm(1 << 20)
		gotFormat = r.FormValue("response_format")
		w.Write([]byte(`{"text":"ok"}`))
	}))
	defer srv.Close()

	m := &repo.Model{BaseURL: srv.URL, APIKey: "k", Model: "whisper-1"}
	resp, err := Transcribe(context.Background(), m, strings.NewReader("x"), "a.webm", TranscriptionParams{Model: "whisper-1"})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	resp.Body.Close()

	if gotFormat != "json" {
		t.Errorf("response_format = %q, want json", gotFormat)
	}
}

// TestTranscribe_LanguageOmittedWhenEmpty 可选字段为空时不下发。
func TestTranscribe_LanguageOmittedWhenEmpty(t *testing.T) {
	var hasLang, hasPrompt, hasTemp bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseMultipartForm(1 << 20)
		_, hasLang = r.MultipartForm.Value["language"]
		_, hasPrompt = r.MultipartForm.Value["prompt"]
		_, hasTemp = r.MultipartForm.Value["temperature"]
		w.Write([]byte(`{"text":"ok"}`))
	}))
	defer srv.Close()

	m := &repo.Model{BaseURL: srv.URL, APIKey: "k", Model: "whisper-1"}
	resp, err := Transcribe(context.Background(), m, strings.NewReader("x"), "a.webm", TranscriptionParams{Model: "whisper-1"})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	resp.Body.Close()

	if hasLang || hasPrompt || hasTemp {
		t.Errorf("空字段不应下发，language=%v prompt=%v temperature=%v", hasLang, hasPrompt, hasTemp)
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
	resp, err := Transcribe(context.Background(), m, strings.NewReader("x"), "a.webm", TranscriptionParams{Model: "whisper-1"})
	if err != nil {
		t.Fatalf("Transcribe: %v", err)
	}
	resp.Body.Close()
	if gotPath != "/v1/audio/transcriptions" {
		t.Errorf("path = %q, want /v1/audio/transcriptions（不应重复补 /v1）", gotPath)
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
			got := UpstreamMessage([]byte(c.body), c.status)
			if !strings.Contains(got, c.want) {
				t.Errorf("UpstreamMessage(%q, %d) = %q, want contains %q", c.body, c.status, got, c.want)
			}
		})
	}
}

// TestUpstreamBody_LimitAt1MiB 响应体读取上限为 1 MiB，避免网关的错误页占用过多内存。
func TestUpstreamBody_LimitAt1MiB(t *testing.T) {
	got, err := UpstreamBody(strings.NewReader(strings.Repeat("a", 1<<20+1)))
	if err != nil {
		t.Fatalf("UpstreamBody: %v", err)
	}
	if len(got) != 1<<20 {
		t.Errorf("len = %d, want %d", len(got), 1<<20)
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
	_, err := Transcribe(ctx, m, strings.NewReader("x"), "a.webm", TranscriptionParams{Model: "whisper-1"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
}
