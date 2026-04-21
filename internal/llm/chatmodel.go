package llm

import (
	"context"
	"fmt"
	"time"

	openai "github.com/cloudwego/eino-ext/components/model/openai"
	"github.com/cloudwego/eino/components/model"

	"github.com/zfd81/groot/internal/config"
)

// NewChatModel creates an OpenAI-compatible ChatModel using eino-ext
// modelName parameter: if empty, uses default model; otherwise uses specified model
func NewChatModel(ctx context.Context, cfg config.LLMConfig, modelName string) (model.BaseChatModel, error) {
	// Get model config by name
	modelCfg := cfg.GetModelByName(modelName)
	if modelCfg == nil {
		if modelName == "" {
			return nil, fmt.Errorf("default model not found in config")
		}
		return nil, fmt.Errorf("model '%s' not found in config", modelName)
	}

	// Create OpenAI ChatModel with timeout based on max_tokens
	timeout := time.Duration(modelCfg.MaxTokens) * time.Second
	if timeout < 30*time.Second {
		timeout = 30 * time.Second // minimum 30s
	}

	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		Model:   modelCfg.Model,
		APIKey:  modelCfg.APIKey,
		BaseURL: modelCfg.BaseURL,
		Timeout: timeout,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create chat model: %w", err)
	}

	return chatModel, nil
}