package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"

	"github.com/zfd81/groot/internal/repo"
)

var (
	// ErrUpstream 上游服务（转录、合成、图片解析）返回了错误，错误信息中透传上游原文
	ErrUpstream = errors.New("上游服务返回错误")
	// ErrEmptyTranscript 上游返回了空文本，通常是音频过短或无有效语音。
	// 透传语义下转录不再据此报错，保留该值供旧调用方判断。
	ErrEmptyTranscript = errors.New("未识别到有效语音")
)

// upstreamTimeout 转录、合成、图片解析共用的整体超时。这几类请求都比
// 对话补全慢，留足时间，但不能无上限，否则挂起的请求会一直占用连接。
const upstreamTimeout = 120 * time.Second

// TranscriptionParams 是转发给上游的除文件外的表单参数。
type TranscriptionParams struct {
	Model                  string // 上游模型名（repo.Model.Model）
	Language               string
	Prompt                 string
	ResponseFormat         string   // json/text/srt/verbose_json/vtt；空按 json
	Temperature            string   // 以字符串透传，空不下发
	TimestampGranularities []string // 每项一个 timestamp_granularities[] 字段
}

// UpstreamResponse 是上游原始响应，调用方负责 Close。
type UpstreamResponse struct {
	StatusCode  int
	ContentType string
	Body        io.ReadCloser
}

// Transcribe 转发到上游并原样返回响应，不解析、不改写。
// 网络/读失败返回 ErrUpstream 包装的错误；ctx 取消返回 ctx.Err()。
//
// 请求 {base_url}/audio/transcriptions，以 multipart 表单提交，
// 与 OpenAI 的转录规范一致，鉴权沿用 repo.Model 中的 APIKey。
// base_url 缺少 /v1 后缀时自动补齐，与对话、连接测试的处理一致。
//
// file 以流式写入上游请求体，不在内存中完整展开音频。
func Transcribe(ctx context.Context, m *repo.Model, file io.Reader, filename string,
	p TranscriptionParams) (*UpstreamResponse, error) {

	endpoint := transcriptionURL(m.BaseURL)

	// io.Pipe 让 multipart 的写入与 HTTP 请求体的读取并发进行，
	// 音频不必先在内存里拼成完整的 body。
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, pr)
	if err != nil {
		return nil, fmt.Errorf("构造转录请求失败: %w", err)
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
		if err = mw.WriteField("model", p.Model); err != nil {
			return
		}
		// 可选字段为空时不下发，交由上游取默认值；response_format 例外：
		// OpenAI 的缺省是 json，这里显式下发，避免上游实现自行解释缺省。
		optional := []struct{ name, value string }{
			{"language", p.Language},
			{"prompt", p.Prompt},
			{"response_format", responseFormatOrDefault(p.ResponseFormat)},
			{"temperature", p.Temperature},
		}
		for _, f := range optional {
			if f.value == "" {
				continue
			}
			if err = mw.WriteField(f.name, f.value); err != nil {
				return
			}
		}
		for _, g := range p.TimestampGranularities {
			if err = mw.WriteField("timestamp_granularities[]", g); err != nil {
				return
			}
		}
		err = mw.Close()
	}()
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req.Header.Set("Authorization", "Bearer "+m.APIKey)

	client := &http.Client{Timeout: upstreamTimeout}
	resp, err := client.Do(req)
	if err != nil {
		// 调用方主动取消或超时不算上游故障，直接返回 ctx 的错误。
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("%w: %w", ErrUpstream, err)
	}

	// 成功与否都由调用方转发：非 2xx 的 body 也要能原样读出，
	// 因此这里不读 body、不按状态码报错，只在调用方 Close 时释放连接。
	return &UpstreamResponse{
		StatusCode:  resp.StatusCode,
		ContentType: resp.Header.Get("Content-Type"),
		Body:        resp.Body,
	}, nil
}

// responseFormatOrDefault 把空的 response_format 补成 OpenAI 规范的缺省值 json。
func responseFormatOrDefault(format string) string {
	if format == "" {
		return "json"
	}
	return format
}

// transcriptionURL 由 base_url 推出转录端点，缺少 /v1 后缀时补齐。
func transcriptionURL(baseURL string) string {
	return openAIBaseURL(baseURL) + "/audio/transcriptions"
}

// UpstreamBody 读入完整响应体（上限 1 MiB），供 handler 在需要时透传错误体。
// 网关的错误页可能很大，转录结果本身远小于此上限。
func UpstreamBody(r io.Reader) ([]byte, error) {
	return io.ReadAll(io.LimitReader(r, 1<<20))
}

// UpstreamMessage 从上游错误响应中提取可读信息。
// 优先取 OpenAI 规范的 error.message，取不到则退回响应原文。
func UpstreamMessage(body []byte, status int) string {
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

// upstreamMessage 是 UpstreamMessage 的内部别名，合成与图片解析沿用旧名调用。
func upstreamMessage(body []byte, status int) string {
	return UpstreamMessage(body, status)
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
