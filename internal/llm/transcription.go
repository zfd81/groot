package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/zfd81/groot/internal/repo"
)

var (
	// ErrUpstream 上游转录服务返回了错误，错误信息中透传上游原文
	ErrUpstream = errors.New("上游转录服务返回错误")
	// ErrEmptyTranscript 上游返回了空文本，通常是音频过短或无有效语音
	ErrEmptyTranscript = errors.New("未识别到有效语音")
)

// transcribeTimeout 转录请求的整体超时。音频转录比对话补全慢，
// 留足时间，但不能无上限，否则挂起的请求会一直占用连接。
const transcribeTimeout = 120 * time.Second

// Transcribe 把音频转成文字。
//
// 请求 {base_url}/audio/transcriptions，以 multipart 表单提交，
// 与 OpenAI 的转录规范一致，鉴权沿用 repo.Model 中的 APIKey。
// base_url 缺少 /v1 后缀时自动补齐，与 CheckConnection 的处理一致。
//
// file 以流式写入上游请求体，不在内存中完整展开音频。
// language 为空时不下发该字段，交由上游自行判断语种。
func Transcribe(ctx context.Context, m *repo.Model, file io.Reader,
	filename, language string) (string, error) {

	endpoint := transcriptionURL(m.BaseURL)

	// io.Pipe 让 multipart 的写入与 HTTP 请求体的读取并发进行，
	// 音频不必先在内存里拼成完整的 body。
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, pr)
	if err != nil {
		return "", fmt.Errorf("构造转录请求失败: %w", err)
	}

	go func() {
		// 写入侧出错时用 CloseWithError 关闭管道，
		// 读取侧（http.Client）随即拿到同一个错误，不会静默发出残缺请求。
		var err error
		defer func() { pw.CloseWithError(err) }()

		var part io.Writer
		if part, err = mw.CreateFormFile("file", filename); err != nil {
			return
		}
		if _, err = io.Copy(part, file); err != nil {
			return
		}
		if err = mw.WriteField("model", m.Model); err != nil {
			return
		}
		if language != "" {
			if err = mw.WriteField("language", language); err != nil {
				return
			}
		}
		err = mw.Close()
	}()
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+m.APIKey)

	client := &http.Client{Timeout: transcribeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		// 调用方主动取消或超时不算上游故障，直接返回 ctx 的错误。
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%w: %w", ErrUpstream, err)
	}
	defer resp.Body.Close()

	// 限制响应体大小：网关的错误页可能很大，转录结果本身远小于此上限。
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", fmt.Errorf("%w: 读取响应失败: %w", ErrUpstream, err)
	}

	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("%w: %s", ErrUpstream, upstreamMessage(body, resp.StatusCode))
	}

	var out struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(body, &out); err != nil {
		return "", fmt.Errorf("%w: 响应不是合法 JSON: %s", ErrUpstream, truncate(string(body), 200))
	}
	if strings.TrimSpace(out.Text) == "" {
		return "", ErrEmptyTranscript
	}
	return out.Text, nil
}

// transcriptionURL 由 base_url 推出转录端点，缺少 /v1 后缀时补齐。
func transcriptionURL(baseURL string) string {
	b := strings.TrimSuffix(baseURL, "/")
	if !strings.HasSuffix(b, "/v1") {
		b += "/v1"
	}
	return b + "/audio/transcriptions"
}

// upstreamMessage 从上游错误响应中提取可读信息。
// 优先取 OpenAI 规范的 error.message，取不到则退回响应原文。
func upstreamMessage(body []byte, status int) string {
	var e struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err := json.Unmarshal(body, &e); err == nil && e.Error.Message != "" {
		return e.Error.Message
	}
	if len(body) == 0 {
		return fmt.Sprintf("HTTP %d", status)
	}
	return truncate(string(body), 200)
}

// truncate 按字符（rune）截断，上游错误原文可能包含中文，
// 按字节截断会切坏多字节字符。
func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
