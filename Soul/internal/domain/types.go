package domain

import "encoding/json"

type ChatRequest struct {
	UserID     string      `json:"user_id,omitempty"`
	SessionID  string      `json:"session_id"`
	TerminalID string      `json:"terminal_id"`
	SoulHint   string      `json:"soul_hint,omitempty"`
	Inputs     []ChatInput `json:"inputs"`
}

type ChatResponse struct {
	SessionID      string   `json:"session_id"`
	TerminalID     string   `json:"terminal_id"`
	SoulID         string   `json:"soul_id"`
	Reply          string   `json:"reply"`
	ExecutedSkills []string `json:"executed_skills,omitempty"`
	ContextSummary string   `json:"context_summary,omitempty"`
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

type ChatInput struct {
	InputID string          `json:"input_id,omitempty"`
	Type    string          `json:"type"`
	Source  string          `json:"source,omitempty"`
	TS      string          `json:"ts,omitempty"`
	Text    string          `json:"text,omitempty"`
	Media   *InputMedia     `json:"media,omitempty"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type InputMedia struct {
	Provider       string `json:"provider,omitempty"`
	URL            string `json:"url,omitempty"`
	Bucket         string `json:"bucket,omitempty"`
	ObjectKey      string `json:"object_key,omitempty"`
	Mime           string `json:"mime,omitempty"`
	SizeBytes      int64  `json:"size_bytes,omitempty"`
	ChecksumSHA256 string `json:"checksum_sha256,omitempty"`
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
