package agent

import (
	"context"
	"fmt"

	ollamaembed "github.com/cloudwego/eino-ext/components/embedding/ollama"
	ollamachat "github.com/cloudwego/eino-ext/components/model/ollama"
)

const (
	OllamaBaseURL  = "http://localhost:11434"
	ChatModel      = "qwen3.6:35b-a3b-coding-nvfp4"
	EmbeddingModel = "nomic-embed-text:latest"
)

func NewChatModel(ctx context.Context, cfg ollamachat.ChatModelConfig) (*ollamachat.ChatModel, error) {
	out, err := ollamachat.NewChatModel(ctx, &cfg)
	if err != nil {
		return nil, fmt.Errorf("ollama chat model: %w", err)
	}

	return out, nil
}

func NewEmbedder(ctx context.Context, cfg ollamaembed.EmbeddingConfig) (*ollamaembed.Embedder, error) {
	out, err := ollamaembed.NewEmbedder(ctx, &cfg)
	if err != nil {
		return nil, fmt.Errorf("ollama embedder: %w", err)
	}

	return out, nil
}
