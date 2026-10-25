package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/zfd81/groot/internal/repo"
)

// speechRecord 记录伪上游收到的一次请求，供断言。
type speechRecord struct {
	path   string
	auth   string
	ctype  string
	fields map[string]any
}

// newSpeechServer 启动伪上游：记录请求、按 handler 响应。
func newSpeechServer(t *testing.T, respond func(w http.ResponseWriter, r *http.Request)) (*httptest.Server, *speechRecord) {
	t.Helper()
	rec := &speechRecord{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec.path = r.URL.Path
		rec.auth = r.Header.Get("Authorization")
		rec.ctype = r.Header.Get("Content-Type")
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &rec.fields)
		respond(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv, rec
}

func speechModel(baseURL string) *repo.Model {
	return &repo.Model{Model: "tts-1", BaseURL: baseURL, APIKey: "sk-x"}
}

func TestSpeech_Success(t *testing.T) {
	audio := []byte{0xFF, 0xFB, 0x90, 0x00, 'a', 'u', 'd', 'i', 'o'}
	srv, rec := newSpeechServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "audio/mpeg")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(audio)
	})

	body, ct, err := Speech(context.Background(), speechModel(srv.URL), SpeechRequest{
		Input:          "你好，世界",
		Voice:          "alloy",
		Language:       "zh",
		Instructions:   "用轻快的语气朗读",
		ResponseFormat: "mp3",
	})
	if err != nil {
		t.Fatalf("Speech() error = %v", err)
	}
	defer body.Close()

	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if !bytes.Equal(got, audio) {
		t.Errorf("body = %v, want %v", got, audio)
	}
	if ct != "audio/mpeg" {
		t.Errorf("contentType = %q, want %q", ct, "audio/mpeg")
	}

	if rec.path != "/v1/audio/speech" {
		t.Errorf("path = %q, want /v1/audio/speech", rec.path)
	}
	if rec.auth != "Bearer sk-x" {
		t.Errorf("Authorization = %q, want %q", rec.auth, "Bearer sk-x")
	}
	if !strings.HasPrefix(rec.ctype, "application/json") {
		t.Errorf("Content-Type = %q, want application/json", rec.ctype)
	}
	want := map[string]any{
		"model":           "tts-1",
		"input":           "你好，世界",
		"voice":           "alloy",
		"language":        "zh",
		"instructions":    "用轻快的语气朗读",
		"response_format": "mp3",
	}
	for k, v := range want {
		if rec.fields[k] != v {
			t.Errorf("json[%q] = %v, want %v", k, rec.fields[k], v)
		}
	}
	// 上游只认 OpenAI 规范字段，旧键名一律不得出现
	for _, k := range []string{"speaker", "instruct"} {
		if _, ok := rec.fields[k]; ok {
			t.Errorf("json should not contain legacy key %q, got %v", k, rec.fields[k])
		}
	}
}

func TestSpeech_OmitsEmptyOptionalFields(t *testing.T) {
	srv, rec := newSpeechServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	body, _, err := Speech(context.Background(), speechModel(srv.URL), SpeechRequest{Input: "hi"})
	if err != nil {
		t.Fatalf("Speech() error = %v", err)
	}
	body.Close()

	for _, k := range []string{"voice", "language", "instructions", "response_format"} {
		if _, ok := rec.fields[k]; ok {
			t.Errorf("json should not contain %q, got %v", k, rec.fields[k])
		}
	}
	if rec.fields["model"] != "tts-1" || rec.fields["input"] != "hi" {
		t.Errorf("json = %v, want model=tts-1 input=hi", rec.fields)
	}
}

func TestSpeech_ContextCanceled(t *testing.T) {
	srv, _ := newSpeechServer(t, func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(5 * time.Second):
		}
	})

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	body, _, err := Speech(ctx, speechModel(srv.URL), SpeechRequest{Input: "hi"})
	if body != nil {
		body.Close()
	}
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestSpeech_ContentTypeFallback(t *testing.T) {
	cases := []struct {
		format string
		want   string
	}{
		{"wav", "audio/wav"},
		{"", "audio/mpeg"},
	}
	for _, c := range cases {
		t.Run("format="+c.format, func(t *testing.T) {
			srv, _ := newSpeechServer(t, func(w http.ResponseWriter, r *http.Request) {
				// 不设置 Content-Type；显式写 header 后再 WriteHeader，
				// 避免 net/http 在首次 Write 时自动嗅探并补上 Content-Type。
				w.Header()["Content-Type"] = nil
				w.WriteHeader(http.StatusOK)
				_, _ = w.Write([]byte("x"))
			})

			body, ct, err := Speech(context.Background(), speechModel(srv.URL), SpeechRequest{
				Input:          "hi",
				ResponseFormat: c.format,
			})
			if err != nil {
				t.Fatalf("Speech() error = %v", err)
			}
			body.Close()
			if ct != c.want {
				t.Errorf("contentType = %q, want %q", ct, c.want)
			}
		})
	}
}

func TestSpeech_UpstreamError(t *testing.T) {
	srv, _ := newSpeechServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":{"message":"bad voice"}}`))
	})

	body, _, err := Speech(context.Background(), speechModel(srv.URL), SpeechRequest{Input: "hi", Voice: "nope"})
	if body != nil {
		t.Errorf("body should be nil on error")
		body.Close()
	}
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("err = %v, want ErrUpstream", err)
	}
	if !strings.Contains(err.Error(), "bad voice") {
		t.Errorf("err = %q, want to contain %q", err.Error(), "bad voice")
	}
}

func TestSpeech_BaseURLWithV1(t *testing.T) {
	srv, rec := newSpeechServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	body, _, err := Speech(context.Background(), speechModel(srv.URL+"/v1"), SpeechRequest{Input: "hi"})
	if err != nil {
		t.Fatalf("Speech() error = %v", err)
	}
	body.Close()

	if rec.path != "/v1/audio/speech" {
		t.Errorf("path = %q, want /v1/audio/speech", rec.path)
	}
}

func TestSpeechContentType(t *testing.T) {
	cases := map[string]string{
		"mp3":  "audio/mpeg",
		"opus": "audio/ogg",
		"aac":  "audio/aac",
		"flac": "audio/flac",
		"wav":  "audio/wav",
		"pcm":  "audio/pcm",
		"":     "audio/mpeg",
		"xyz":  "audio/mpeg",
	}
	for format, want := range cases {
		if got := SpeechContentType(format); got != want {
			t.Errorf("SpeechContentType(%q) = %q, want %q", format, got, want)
		}
	}
}
