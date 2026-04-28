package llm

import (
	"context"
	"fmt"

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

	// Prepare parameters for API call
	// MaxTokens: maximum output tokens for this single LLM call
	// Temperature: controls randomness of output (0.0-2.0)
	maxTokens := modelCfg.MaxTokens
	temperature := float32(modelCfg.Temperature)

	chatModel, err := openai.NewChatModel(ctx, &openai.ChatModelConfig{
		Model:       modelCfg.Model,
		APIKey:      modelCfg.APIKey,
		BaseURL:     modelCfg.BaseURL,
		MaxTokens:   &maxTokens,     // 限制单次调用输出的最大 token 数
		Temperature: &temperature,    // 控制输出的随机性
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create chat model: %w", err)
	}

	return chatModel, nil
}