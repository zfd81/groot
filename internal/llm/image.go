package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/zfd81/groot/internal/repo"
)

// ImageRequest 图片解析参数。
type ImageRequest struct {
	Instruction string // 已由 handler 填充默认指令，非空
	MIMEType    string // handler 嗅探或按扩展名判定的图片类型，如 image/png
	Base64      string // 附件原文，纯 Base64，不含 data: 前缀，不解码
}

// Usage 上游返回的 token 用量。
type Usage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// base64Newlines 剔除 Base64 中按列换行留下的 \r\n，见 UnderstandImage。
var base64Newlines = strings.NewReplacer("\r", "", "\n", "")

// imageURL 由 base_url 推出图片解析用的对话补全端点，缺少 /v1 后缀时补齐。
func imageURL(baseURL string) string {
	return openAIBaseURL(baseURL) + "/chat/completions"
}

// UnderstandImage 请求 {base_url}/chat/completions，返回模型输出的文本与用量。
//
// 消息形状与 /chat 发送图片附件时一致：user 消息里文本在前、图片在后。
// image_url 不设 detail 字段、不下发 temperature 与 thinking，
// 由上游默认行为决定；m.MaxCompletionTokens > 0 时下发 max_completion_tokens。
//
// 请求体把 JSON 拆成「前缀 + Base64 原文 + 后缀」三段用 io.MultiReader 拼成，
// Base64 原样嵌入 data URL，不再复制一整份 data URL 或序列化结果。
// 三段长度已知，显式设置 ContentLength，避免退化成 chunked 传输。
//
// Base64 里可能带按列换行的 \r\n（base64 命令行的默认输出即如此），
// JSON 字符串不允许裸控制字符，直接拼接会得到非法报文。这类空白先剔除：
// 解码结果不变（base64 解码器本就忽略它们），且只在确实存在时才新建字符串，
// 常见的不带换行路径不产生额外分配。
//
// 上游返回 >= 400、响应不是合法 JSON、choices 为空或内容为空时，
// 返回包装了 ErrUpstream 的错误；调用方主动取消时直接返回 ctx 的错误。
// 上游报文没有 usage 字段时返回的 *Usage 为 nil。
func UnderstandImage(ctx context.Context, m *repo.Model, req ImageRequest) (string, *Usage, error) {
	modelJSON, err := json.Marshal(m.Model)
	if err != nil {
		return "", nil, fmt.Errorf("序列化模型名失败: %w", err)
	}
	instructionJSON, err := json.Marshal(req.Instruction)
	if err != nil {
		return "", nil, fmt.Errorf("序列化指令失败: %w", err)
	}

	payload := req.Base64
	if strings.ContainsAny(payload, "\r\n") {
		payload = base64Newlines.Replace(payload)
	}

	prefix := `{"model":` + string(modelJSON) +
		`,"messages":[{"role":"user","content":[{"type":"text","text":` + string(instructionJSON) +
		`},{"type":"image_url","image_url":{"url":"data:` + req.MIMEType + `;base64,`
	suffix := `"}}]}]}`
	if m.MaxCompletionTokens > 0 {
		suffix = `"}}]}],"max_completion_tokens":` + strconv.Itoa(m.MaxCompletionTokens) + `}`
	}

	body := io.MultiReader(strings.NewReader(prefix), strings.NewReader(payload), strings.NewReader(suffix))

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, imageURL(m.BaseURL), body)
	if err != nil {
		return "", nil, fmt.Errorf("构造图片解析请求失败: %w", err)
	}
	httpReq.ContentLength = int64(len(prefix) + len(payload) + len(suffix))
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+m.APIKey)

	client := &http.Client{Timeout: upstreamTimeout}
	resp, err := client.Do(httpReq)
	if err != nil {
		// 调用方主动取消或超时不算上游故障，直接返回 ctx 的错误。
		if ctx.Err() != nil {
			return "", nil, ctx.Err()
		}
		return "", nil, fmt.Errorf("%w: %w", ErrUpstream, err)
	}
	defer resp.Body.Close()

	// 限制响应体大小：网关的错误页可能很大，解析结果本身远小于此上限。
	respBody, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return "", nil, fmt.Errorf("%w: 读取响应失败: %w", ErrUpstream, err)
	}

	if resp.StatusCode >= 400 {
		return "", nil, fmt.Errorf("%w: %s", ErrUpstream, upstreamMessage(respBody, resp.StatusCode))
	}

	var out struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
		Usage *struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return "", nil, fmt.Errorf("%w: 响应不是合法 JSON: %s", ErrUpstream, truncate(string(respBody), 200))
	}

	if len(out.Choices) == 0 || strings.TrimSpace(out.Choices[0].Message.Content) == "" {
		return "", nil, fmt.Errorf("%w: 上游未返回内容", ErrUpstream)
	}

	var usage *Usage
	if out.Usage != nil {
		usage = &Usage{
			PromptTokens:     out.Usage.PromptTokens,
			CompletionTokens: out.Usage.CompletionTokens,
			TotalTokens:      out.Usage.TotalTokens,
		}
		// 上游没给 total_tokens 时补齐，保证这项在出现时总是自洽。
		if usage.TotalTokens == 0 && (usage.PromptTokens > 0 || usage.CompletionTokens > 0) {
			usage.TotalTokens = usage.PromptTokens + usage.CompletionTokens
		}
	}

	return out.Choices[0].Message.Content, usage, nil
}
