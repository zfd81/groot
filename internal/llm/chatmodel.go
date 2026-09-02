package llm

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	openai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"

	"github.com/zfd81/groot/internal/repo"
)

// NewChatModel creates an OpenAI-compatible ChatModel using eino-ext.
// m 为已解析的模型配置（APIKey 已展开环境变量，由 ModelService 保证）。
// timeout: per-call timeout for LLM API requests (0 means no timeout)
// 采样参数只透传 temperature、max_completion_tokens、thinking 三项；
// 其余字段（top_p、penalty、seed、stop 等）保留在数据模型中供以后扩展，暂不下发。
func NewChatModel(ctx context.Context, m *repo.Model, timeout time.Duration) (model.BaseChatModel, error) {
	temperature := float32(m.Temperature)

	chatCfg := &openai.ChatModelConfig{
		Model:       m.Model,
		APIKey:      m.APIKey,
		BaseURL:     m.BaseURL,
		Temperature: &temperature,
		Timeout:     timeout,
	}
	// MaxCompletionTokens: only set when > 0 (0 means not specified;
	// 显式传 0 会被多数 OpenAI 兼容后端拒绝)
	if m.MaxCompletionTokens > 0 {
		maxTokens := m.MaxCompletionTokens
		chatCfg.MaxCompletionTokens = &maxTokens
	}
	if m.Thinking {
		chatCfg.ExtraFields = map[string]any{
			"thinking": map[string]any{"type": "enabled"},
		}
	}

	chatModel, err := openai.NewChatModel(ctx, chatCfg)
	if err != nil {
		return nil, fmt.Errorf("failed to create chat model: %w", err)
	}
	return chatModel, nil
}

// CheckConnection checks if LLM API is reachable and properly configured
// Returns (status, errorMessage) where status is "healthy" or "unhealthy"
func CheckConnection(m *repo.Model) (status string, errorMsg string) {
	// Ensure base_url ends with /v1 for OpenAI-compatible APIs
	baseURL := m.BaseURL
	if !strings.HasSuffix(baseURL, "/v1") && !strings.HasSuffix(baseURL, "/v1/") {
		baseURL = strings.TrimSuffix(baseURL, "/") + "/v1"
	}

	// Test connection by calling the models endpoint (lightweight, no token cost)
	modelsURL := strings.TrimSuffix(baseURL, "/") + "/models"

	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequest("GET", modelsURL, nil)
	if err != nil {
		return "unhealthy", "failed to create request: " + err.Error()
	}

	req.Header.Set("Authorization", "Bearer "+m.APIKey)

	resp, err := client.Do(req)
	if err != nil {
		return "unhealthy", "connection failed: " + err.Error()
	}
	defer resp.Body.Close()

	// Read response body to check for auth errors
	body, _ := io.ReadAll(resp.Body)

	if resp.StatusCode == 401 || resp.StatusCode == 403 {
		return "unhealthy", "authentication failed (invalid API key)"
	}

	if resp.StatusCode >= 500 {
		return "unhealthy", fmt.Sprintf("server error: status %d", resp.StatusCode)
	}

	if resp.StatusCode >= 400 {
		// Some APIs don't support /models endpoint, but if we get a 404 with valid auth,
		// the connection is likely OK. Check for auth errors in response body.
		if strings.Contains(string(body), "invalid") || strings.Contains(string(body), "unauthorized") {
			return "unhealthy", "authentication failed"
		}
		// For other 4xx errors, assume connection is OK (API may not support models endpoint)
		return "healthy", ""
	}

	return "healthy", ""
}
