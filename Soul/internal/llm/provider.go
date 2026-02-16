package llm

import (
	"context"
	"fmt"
	"net/http"
	"time"

	"soul/internal/domain"
)

type Provider interface {
	Complete(ctx context.Context, req domain.LLMRequest) (domain.LLMResponse, error)
}

type Config struct {
	Provider         string
	Model            string
	OpenAIBaseURL    string
	OpenAIAPIKey     string
	AnthropicBaseURL string
	AnthropicAPIKey  string
}

func NewProvider(cfg Config) (Provider, error) {
	client := &http.Client{Timeout: 60 * time.Second}

	switch cfg.Provider {
	case "openai":
		return NewOpenAIProvider(client, cfg.OpenAIBaseURL, cfg.OpenAIAPIKey), nil
	case "claude":
		return NewClaudeProvider(client, cfg.AnthropicBaseURL, cfg.AnthropicAPIKey), nil
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %s", cfg.Provider)
	}
}
