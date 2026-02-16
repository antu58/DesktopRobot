package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"soul/internal/domain"
)

type ClaudeProvider struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

func NewClaudeProvider(client *http.Client, baseURL, apiKey string) *ClaudeProvider {
	return &ClaudeProvider{client: client, baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey}
}

type claudeRequest struct {
	Model     string          `json:"model"`
	System    string          `json:"system,omitempty"`
	MaxTokens int             `json:"max_tokens"`
	Messages  []claudeMessage `json:"messages"`
	Tools     []claudeTool    `json:"tools,omitempty"`
}

type claudeMessage struct {
	Role    string        `json:"role"`
	Content []claudeBlock `json:"content"`
}

type claudeBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
}

type claudeTool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type claudeResponse struct {
	Content []claudeBlock `json:"content"`
	Error   *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

func (p *ClaudeProvider) Complete(ctx context.Context, req domain.LLMRequest) (domain.LLMResponse, error) {
	payload := claudeRequest{
		Model:     req.Model,
		System:    req.System,
		MaxTokens: 1024,
		Messages:  make([]claudeMessage, 0, len(req.Messages)),
	}
	for _, m := range req.Messages {
		switch m.Role {
		case "user", "assistant":
			cm := claudeMessage{Role: m.Role}
			if m.Content != "" {
				cm.Content = append(cm.Content, claudeBlock{Type: "text", Text: m.Content})
			}
			for _, tc := range m.ToolCalls {
				cm.Content = append(cm.Content, claudeBlock{
					Type:  "tool_use",
					ID:    tc.ID,
					Name:  tc.Name,
					Input: normalizeSchema(tc.Arguments),
				})
			}
			if len(cm.Content) == 0 {
				cm.Content = []claudeBlock{{Type: "text", Text: ""}}
			}
			payload.Messages = append(payload.Messages, cm)
		case "tool":
			payload.Messages = append(payload.Messages, claudeMessage{
				Role: "user",
				Content: []claudeBlock{{
					Type:      "tool_result",
					ToolUseID: m.ToolCallID,
					Content:   m.Content,
				}},
			})
		}
	}

	if len(req.Tools) > 0 {
		payload.Tools = make([]claudeTool, 0, len(req.Tools))
		for _, t := range req.Tools {
			payload.Tools = append(payload.Tools, claudeTool{
				Name:        t.Name,
				Description: t.Description,
				InputSchema: normalizeSchema(t.Schema),
			})
		}
	}

	buf, err := json.Marshal(payload)
	if err != nil {
		return domain.LLMResponse{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/v1/messages", bytes.NewReader(buf))
	if err != nil {
		return domain.LLMResponse{}, err
	}
	httpReq.Header.Set("x-api-key", p.apiKey)
	httpReq.Header.Set("anthropic-version", "2023-06-01")
	httpReq.Header.Set("content-type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return domain.LLMResponse{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return domain.LLMResponse{}, fmt.Errorf("claude status %d: %s", resp.StatusCode, string(body))
	}

	var parsed claudeResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return domain.LLMResponse{}, err
	}
	if parsed.Error != nil {
		return domain.LLMResponse{}, fmt.Errorf("claude error: %s", parsed.Error.Message)
	}

	out := domain.LLMResponse{}
	for _, block := range parsed.Content {
		switch block.Type {
		case "text":
			if block.Text != "" {
				if out.Content == "" {
					out.Content = block.Text
				} else {
					out.Content += "\n" + block.Text
				}
			}
		case "tool_use":
			out.ToolCalls = append(out.ToolCalls, domain.ToolCall{
				ID:        block.ID,
				Name:      block.Name,
				Arguments: normalizeSchema(block.Input),
			})
		}
	}
	return out, nil
}
