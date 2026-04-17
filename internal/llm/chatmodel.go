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
func NewChatModel(ctx context.Context, cfg config.LLMConfig) (model.BaseChatModel, error) {
	// Get active model config
	modelCfg := cfg.GetActiveModel()
	if modelCfg == nil {
		return nil, fmt.Errorf("model %s not found in config", cfg.ActiveModel)
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