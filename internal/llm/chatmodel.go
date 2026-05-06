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

	"github.com/zfd81/groot/internal/config"
)

// NewChatModel creates an OpenAI-compatible ChatModel using eino-ext
// modelName parameter: if empty, uses default model; otherwise uses specified model
// timeout parameter: per-call timeout for LLM API requests (0 means no timeout)
func NewChatModel(ctx context.Context, cfg config.LLMConfig, modelName string, timeout time.Duration) (model.BaseChatModel, error) {
	// Get model config by name
	modelCfg := cfg.GetModelByName(modelName)
	if modelCfg == nil {
		if modelName == "" {
			return nil, fmt.Errorf("default model not found in config")
		}
		return nil, fmt.Errorf("model '%s' not found in config", modelName)
	}

	// Prepare parameters for API call
	maxTokens := modelCfg.MaxCompletionTokens
	temperature := float32(modelCfg.Temperature)
	topP := float32(modelCfg.TopP)
	frequencyPenalty := float32(modelCfg.FrequencyPenalty)
	presencePenalty := float32(modelCfg.PresencePenalty)

	chatCfg := &openai.ChatModelConfig{
		Model:              modelCfg.Model,
		APIKey:             modelCfg.APIKey,
		BaseURL:            modelCfg.BaseURL,
		MaxCompletionTokens: &maxTokens,
		Temperature:        &temperature,
		TopP:               &topP,
		FrequencyPenalty:   &frequencyPenalty,
		PresencePenalty:    &presencePenalty,
		Timeout:            timeout,
	}

	// Seed: only set when > 0 (0 means not specified)
	if modelCfg.Seed > 0 {
		chatCfg.Seed = &modelCfg.Seed
	}

	// Stop: only set when non-empty
	if len(modelCfg.Stop) > 0 {
		chatCfg.Stop = modelCfg.Stop
	}

	// Thinking: pass via extra_fields for models like Qwen/DeepSeek
	if modelCfg.Thinking {
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
func CheckConnection(cfg config.LLMConfig) (status string, errorMsg string) {
	modelCfg := cfg.GetDefaultModel()
	if modelCfg == nil {
		return "unhealthy", "default model not configured"
	}

	// Ensure base_url ends with /v1 for OpenAI-compatible APIs
	baseURL := modelCfg.BaseURL
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

	req.Header.Set("Authorization", "Bearer "+modelCfg.APIKey)

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