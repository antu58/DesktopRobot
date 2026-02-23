package domain

import "encoding/json"

type ChatRequest struct {
	UserID     string      `json:"user_id,omitempty"`
	SessionID  string      `json:"session_id"`
	TerminalID string      `json:"terminal_id"`
	SoulID     string      `json:"soul_id,omitempty"`
	SoulHint   string      `json:"soul_hint,omitempty"`
	Inputs     []ChatInput `json:"inputs"`
}

type ChatResponse struct {
	SessionID       string   `json:"session_id"`
	TerminalID      string   `json:"terminal_id"`
	SoulID          string   `json:"soul_id"`
	Reply           string   `json:"reply"`
	ExecutedSkills  []string `json:"executed_skills,omitempty"`
	ContextSummary  string   `json:"context_summary,omitempty"`
	IntentDecision  string   `json:"intent_decision,omitempty"`
	ExecMode        string   `json:"exec_mode,omitempty"`
	ExecProbability float64  `json:"exec_probability,omitempty"`
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

type IntentMatchRules struct {
	KeywordsAny      []string `json:"keywords_any,omitempty"`
	KeywordsAll      []string `json:"keywords_all,omitempty"`
	NegativeKeywords []string `json:"negative_keywords,omitempty"`
	RegexAny         []string `json:"regex_any,omitempty"`
	RegexAll         []string `json:"regex_all,omitempty"`
	EntityTypesAny   []string `json:"entity_types_any,omitempty"`
	EntityTypesAll   []string `json:"entity_types_all,omitempty"`
	Examples         []string `json:"examples,omitempty"`
	MinConfidence    float64  `json:"min_confidence,omitempty"`
}

type IntentSlotBinding struct {
	Name                string   `json:"name"`
	Required            bool     `json:"required,omitempty"`
	FromEntityTypes     []string `json:"from_entity_types,omitempty"`
	UseNormalizedEntity *bool    `json:"use_normalized_entity,omitempty"`
	Regex               string   `json:"regex,omitempty"`
	RegexGroup          int      `json:"regex_group,omitempty"`
	FromTimeKey         string   `json:"from_time_key,omitempty"`
	TimeKind            string   `json:"time_kind,omitempty"`
	Default             any      `json:"default,omitempty"`
}

type IntentSpec struct {
	ID        string              `json:"id"`
	Name      string              `json:"name,omitempty"`
	Priority  int                 `json:"priority,omitempty"`
	HintScore float64             `json:"hint_score,omitempty"`
	Match     IntentMatchRules    `json:"match,omitempty"`
	Slots     []IntentSlotBinding `json:"slots,omitempty"`
}

type IntentCatalogReport struct {
	TerminalID     string       `json:"terminal_id"`
	CatalogVersion int64        `json:"catalog_version,omitempty"`
	IntentCatalog  []IntentSpec `json:"intent_catalog"`
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

type EmotionSignal struct {
	Emotion    string  `json:"emotion,omitempty"`
	P          float64 `json:"p"`
	A          float64 `json:"a"`
	D          float64 `json:"d"`
	Intensity  float64 `json:"intensity"`
	Confidence float64 `json:"confidence,omitempty"`
}

type PersonalityVector struct {
	Empathy        float64 `json:"empathy"`
	Sensitivity    float64 `json:"sensitivity"`
	Stability      float64 `json:"stability"`
	Expressiveness float64 `json:"expressiveness"`
	Dominance      float64 `json:"dominance"`
}

type SoulEmotionState struct {
	P                 float64           `json:"p"`
	A                 float64           `json:"a"`
	D                 float64           `json:"d"`
	Boredom           float64           `json:"boredom"`
	ShockLoad         float64           `json:"shock_load"`
	ExtremeMemory     float64           `json:"extreme_memory"`
	LongMuP           float64           `json:"long_mu_p"`
	LongMuA           float64           `json:"long_mu_a"`
	LongMuD           float64           `json:"long_mu_d"`
	LongVolatility    float64           `json:"long_volatility"`
	Drift             PersonalityVector `json:"drift"`
	LockUntil         string            `json:"lock_until,omitempty"`
	StableSince       string            `json:"stable_since,omitempty"`
	LastInteractionAt string            `json:"last_interaction_at,omitempty"`
	LastUpdatedAt     string            `json:"last_updated_at"`
}

type SoulProfile struct {
	SoulID            string            `json:"soul_id"`
	UserID            string            `json:"user_id"`
	Name              string            `json:"name"`
	MBTIType          string            `json:"mbti_type"`
	PersonalityVector PersonalityVector `json:"personality_vector"`
	EmotionState      SoulEmotionState  `json:"emotion_state"`
	ModelVersion      string            `json:"model_version"`
	CreatedAt         string            `json:"created_at,omitempty"`
	UpdatedAt         string            `json:"updated_at,omitempty"`
}

type UserProfile struct {
	ID          int64  `json:"id"`
	UserID      string `json:"user_id"`
	UserUUID    string `json:"user_uuid"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
	CreatedAt   string `json:"created_at,omitempty"`
	UpdatedAt   string `json:"updated_at,omitempty"`
}

type CreateUserPayload struct {
	UserID      string `json:"user_id"`
	DisplayName string `json:"display_name,omitempty"`
	Description string `json:"description,omitempty"`
}

type CreateSoulPayload struct {
	UserID   string `json:"user_id,omitempty"`
	Name     string `json:"name"`
	MBTIType string `json:"mbti_type"`
}

type SelectSoulPayload struct {
	UserID     string `json:"user_id,omitempty"`
	TerminalID string `json:"terminal_id"`
	SoulID     string `json:"soul_id"`
}

type SoulUserRelation struct {
	ID               int64              `json:"id"`
	RelationUUID     string             `json:"relation_uuid"`
	SoulID           string             `json:"soul_id"`
	RelatedUserID    string             `json:"related_user_id,omitempty"`
	Appellation      string             `json:"appellation"`
	RelationToOwner  string             `json:"relation_to_owner"`
	UserDescription  string             `json:"user_description,omitempty"`
	PersonalityModel *PersonalityVector `json:"personality_model,omitempty"`
	CreatedAt        string             `json:"created_at,omitempty"`
	UpdatedAt        string             `json:"updated_at,omitempty"`
}

type CreateSoulUserRelationPayload struct {
	RelatedUserID    string             `json:"related_user_id,omitempty"`
	Appellation      string             `json:"appellation"`
	RelationToOwner  string             `json:"relation_to_owner"`
	UserDescription  string             `json:"user_description,omitempty"`
	PersonalityModel *PersonalityVector `json:"personality_model,omitempty"`
}

type EmotionUpdatePayload struct {
	SessionID       string           `json:"session_id"`
	TerminalID      string           `json:"terminal_id"`
	SoulID          string           `json:"soul_id"`
	UserEmotion     EmotionSignal    `json:"user_emotion"`
	SoulEmotion     SoulEmotionState `json:"soul_emotion"`
	ExecProbability float64          `json:"exec_probability"`
	ExecMode        string           `json:"exec_mode"`
	TS              string           `json:"ts"`
}

type IntentActionItem struct {
	IntentID   string         `json:"intent_id"`
	IntentName string         `json:"intent_name,omitempty"`
	Confidence float64        `json:"confidence"`
	Parameters map[string]any `json:"parameters,omitempty"`
	Normalized map[string]any `json:"normalized,omitempty"`
}

type IntentActionPayload struct {
	RequestID       string             `json:"request_id"`
	SessionID       string             `json:"session_id"`
	TerminalID      string             `json:"terminal_id"`
	SoulID          string             `json:"soul_id"`
	Intents         []IntentActionItem `json:"intents"`
	ExecProbability float64            `json:"exec_probability"`
	TS              string             `json:"ts"`
}
