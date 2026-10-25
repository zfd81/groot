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
	Voice          string // 音色，空串不下发
	Language       string // 合成语种，空串不下发
	Instructions   string // 风格指令（OpenAI 规范字段），空串不下发
	ResponseFormat string // 空串不下发
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
// 失败时响应体已由本函数关闭，调用方无需处理。
func Speech(ctx context.Context, m *repo.Model, sr SpeechRequest) (io.ReadCloser, string, error) {
	payload, err := json.Marshal(struct {
		Model          string `json:"model"`
		Input          string `json:"input"`
		Voice          string `json:"voice,omitempty"`
		Language       string `json:"language,omitempty"`
		Instructions   string `json:"instructions,omitempty"`
		ResponseFormat string `json:"response_format,omitempty"`
	}{
		Model:          m.Model,
		Input:          sr.Input,
		Voice:          sr.Voice,
		Language:       sr.Language,
		Instructions:   sr.Instructions,
		ResponseFormat: sr.ResponseFormat,
	})
	if err != nil {
		return nil, "", fmt.Errorf("序列化合成请求失败: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, speechURL(m.BaseURL), bytes.NewReader(payload))
	if err != nil {
		return nil, "", fmt.Errorf("构造合成请求失败: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+m.APIKey)

	client := &http.Client{Timeout: transcribeTimeout}
	resp, err := client.Do(req)
	if err != nil {
		// 调用方主动取消或超时不算上游故障，直接返回 ctx 的错误。
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		return nil, "", fmt.Errorf("%w: %w", ErrUpstream, err)
	}

	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		// 限制响应体大小：网关的错误页可能很大。
		body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		if err != nil {
			return nil, "", fmt.Errorf("%w: 读取响应失败: %w", ErrUpstream, err)
		}
		return nil, "", fmt.Errorf("%w: %s", ErrUpstream, upstreamMessage(body, resp.StatusCode))
	}

	ct := resp.Header.Get("Content-Type")
	if ct == "" {
		ct = SpeechContentType(sr.ResponseFormat)
	}
	return resp.Body, ct, nil
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
