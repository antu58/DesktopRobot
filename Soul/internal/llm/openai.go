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

type OpenAIProvider struct {
	client  *http.Client
	baseURL string
	apiKey  string
}

func NewOpenAIProvider(client *http.Client, baseURL, apiKey string) *OpenAIProvider {
	return &OpenAIProvider{client: client, baseURL: strings.TrimRight(baseURL, "/"), apiKey: apiKey}
}

type openAIRequest struct {
	Model      string          `json:"model"`
	Messages   []openAIMessage `json:"messages"`
	Tools      []openAITool    `json:"tools,omitempty"`
	ToolChoice string          `json:"tool_choice,omitempty"`
}

type openAIMessage struct {
	Role       string           `json:"role"`
	Content    string           `json:"content,omitempty"`
	Name       string           `json:"name,omitempty"`
	ToolCallID string           `json:"tool_call_id,omitempty"`
	ToolCalls  []openAIToolCall `json:"tool_calls,omitempty"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters"`
}

type openAIResponse struct {
	Choices []struct {
		Message openAIMessage `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type openAIToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

func (p *OpenAIProvider) Complete(ctx context.Context, req domain.LLMRequest) (domain.LLMResponse, error) {
	payload := openAIRequest{
		Model:    req.Model,
		Messages: make([]openAIMessage, 0, len(req.Messages)+1),
	}
	if req.System != "" {
		payload.Messages = append(payload.Messages, openAIMessage{Role: "system", Content: req.System})
	}
	for _, m := range req.Messages {
		om := openAIMessage{
			Role:       m.Role,
			Content:    m.Content,
			Name:       m.Name,
			ToolCallID: m.ToolCallID,
		}
		if len(m.ToolCalls) > 0 {
			om.ToolCalls = make([]openAIToolCall, 0, len(m.ToolCalls))
			for _, tc := range m.ToolCalls {
				item := openAIToolCall{ID: tc.ID, Type: "function"}
				item.Function.Name = tc.Name
				item.Function.Arguments = string(tc.Arguments)
				om.ToolCalls = append(om.ToolCalls, item)
			}
		}
		payload.Messages = append(payload.Messages, om)
	}

	if len(req.Tools) > 0 {
		payload.Tools = make([]openAITool, 0, len(req.Tools))
		for _, t := range req.Tools {
			payload.Tools = append(payload.Tools, openAITool{
				Type: "function",
				Function: openAIFunction{
					Name:        t.Name,
					Description: t.Description,
					Parameters:  normalizeSchema(t.Schema),
				},
			})
		}
		payload.ToolChoice = "auto"
	}

	buf, err := json.Marshal(payload)
	if err != nil {
		return domain.LLMResponse{}, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.baseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return domain.LLMResponse{}, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+p.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(httpReq)
	if err != nil {
		return domain.LLMResponse{}, err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return domain.LLMResponse{}, fmt.Errorf("openai status %d: %s", resp.StatusCode, string(body))
	}

	var parsed openAIResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return domain.LLMResponse{}, err
	}
	if parsed.Error != nil {
		return domain.LLMResponse{}, fmt.Errorf("openai error: %s", parsed.Error.Message)
	}
	if len(parsed.Choices) == 0 {
		return domain.LLMResponse{}, fmt.Errorf("empty openai response")
	}

	msg := parsed.Choices[0].Message
	out := domain.LLMResponse{Content: msg.Content}
	for _, tc := range msg.ToolCalls {
		out.ToolCalls = append(out.ToolCalls, domain.ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: json.RawMessage(tc.Function.Arguments),
		})
	}
	return out, nil
}

func normalizeSchema(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return json.RawMessage(`{"type":"object","properties":{},"required":[]}`)
	}
	return raw
}
