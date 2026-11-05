package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/zfd81/groot/internal/repo"
)

// SpeechRequest 语音合成的请求参数。
type SpeechRequest struct {
	Input          string
	Voice          string   // 音色
	Language       string   // 合成语种，空串不下发
	Instructions   string   // 风格指令（OpenAI 规范字段），空串不下发
	ResponseFormat string   // 空串不下发
	Speed          *float64 // 语速，nil 不下发
}

// UpstreamError 是上游非 2xx 的原始响应，由 handler 决定原样转发还是转成统一错误体。
// Body 与 ContentType 均为上游原值，未做解析。
type UpstreamError struct {
	StatusCode  int
	ContentType string
	Body        []byte
}

// Speech 把文字合成为语音。
//
// 请求 {base_url}/audio/speech，JSON 请求体与 OpenAI 的语音合成规范一致，
// 鉴权沿用 repo.Model 中的 APIKey。base_url 缺少 /v1 后缀时自动补齐，
// 与对话、转录的处理一致。
//
// 成功时返回上游的响应体（音频流）与 Content-Type，响应体未读取，
// 调用方可直接转发并负责 Close。上游未设置 Content-Type 时按
// response_format 推断，见 SpeechContentType。
//
// 上游非 2xx 时返回 *UpstreamError（响应体已读入并关闭），错误为 nil；
// 网络或读取失败时返回包装了 ErrUpstream 的错误，调用方主动取消时返回 ctx 的错误。
func Speech(ctx context.Context, m *repo.Model, sr SpeechRequest) (io.ReadCloser, string, *UpstreamError, error) {
	payload, err := json.Marshal(struct {
		Model          string   `json:"model"`
		Input          string   `json:"input"`
		Voice          string   `json:"voice,omitempty"`
		Language       string   `json:"language,omitempty"`
		Instructions   string   `json:"instructions,omitempty"`
		ResponseFormat string   `json:"response_format,omitempty"`
		Speed          *float64 `json:"speed,omitempty"`
	}{
		Model:          m.Model,
		Input:          sr.Input,
		Voice:          sr.Voice,
		Language:       sr.Language,
		Instructions:   sr.Instructions,
		ResponseFormat: sr.ResponseFormat,
		Speed:          sr.Speed,
	})
	if err != nil {
		return nil, "", nil, fmt.Errorf("序列化合成请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, speechURL(m.BaseURL), bytes.NewReader(payload))
	if err != nil {
		return nil, "", nil, fmt.Errorf("构造合成请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.APIKey)

	client := &http.Client{Timeout: upstreamTimeout}
	resp, err := client.Do(req)
	if err != nil {
		// 调用方主动取消或超时不算上游故障，直接返回 ctx 的错误。
		if ctx.Err() != nil {
			return nil, "", nil, ctx.Err()
		}
		return nil, "", nil, fmt.Errorf("%w: %w", ErrUpstream, err)
	}

	ct := resp.Header.Get("Content-Type")
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if ct == "" {
			ct = SpeechContentType(sr.ResponseFormat)
		}
		return resp.Body, ct, nil, nil
	}

	// 非 2xx：读入完整响应体（上限 1 MiB，网关的错误页可能很大）后关闭，
	// 交由 handler 按 OpenAI 信封规则决定转发还是改写。
	body, err := UpstreamBody(resp.Body)
	resp.Body.Close()
	if err != nil {
		return nil, "", nil, fmt.Errorf("%w: 读取响应失败: %w", ErrUpstream, err)
	}
	return nil, "", &UpstreamError{StatusCode: resp.StatusCode, ContentType: ct, Body: body}, nil
}

// speechURL 由 base_url 推出语音合成端点，缺少 /v1 后缀时补齐。
func speechURL(baseURL string) string {
	return openAIBaseURL(baseURL) + "/audio/speech"
}

// SpeechContentType 由 response_format 推断音频的 MIME 类型，
// 未知或空值一律返回 "audio/mpeg"。
func SpeechContentType(format string) string {
	switch format {
	case "opus":
		return "audio/ogg"
	case "aac":
		return "audio/aac"
	case "flac":
		return "audio/flac"
	case "wav":
		return "audio/wav"
	case "pcm":
		return "audio/pcm"
	default:
		return "audio/mpeg"
	}
}
