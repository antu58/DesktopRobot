package domain

import "encoding/json"

type ChatRequest struct {
	UserID     string `json:"user_id,omitempty"`
	SessionID  string `json:"session_id"`
	TerminalID string `json:"terminal_id"`
	SoulHint   string `json:"soul_hint,omitempty"`
	Message    string `json:"message"`
}

type ChatResponse struct {
	SessionID      string   `json:"session_id"`
	TerminalID     string   `json:"terminal_id"`
	SoulID         string   `json:"soul_id"`
	Reply          string   `json:"reply"`
	ExecutedSkills []string `json:"executed_skills,omitempty"`
}

type Message struct {
	Role       string
	Content    string
	Name       string
	ToolCallID string
	ToolCalls  []ToolCall
}

type SkillDefinition struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"input_schema"`
}

type ToolCall struct {
	ID        string
	Name      string
	Arguments json.RawMessage
}

type LLMTool struct {
	Name        string
	Description string
	Schema      json.RawMessage
}

type LLMRequest struct {
	Model    string
	System   string
	Tools    []LLMTool
	Messages []Message
}

type LLMResponse struct {
	Content   string
	ToolCalls []ToolCall
}

// MQTT payloads

type SkillReport struct {
	TerminalID   string            `json:"terminal_id"`
	SoulHint     string            `json:"soul_hint,omitempty"`
	SkillVersion int64             `json:"skill_version,omitempty"`
	Skills       []SkillDefinition `json:"skills"`
}

type InvokeRequest struct {
	RequestID string          `json:"request_id"`
	Skill     string          `json:"skill"`
	Arguments json.RawMessage `json:"arguments"`
}

type InvokeResult struct {
	RequestID string `json:"request_id"`
	OK        bool   `json:"ok"`
	Output    string `json:"output"`
	Error     string `json:"error,omitempty"`
}
