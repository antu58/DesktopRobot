package domain

type IntentFilterOptions struct {
	AllowMultiIntent          bool    `json:"allow_multi_intent"`
	MaxIntents                int     `json:"max_intents"`
	MaxIntentsPerSegment      int     `json:"max_intents_per_segment"`
	MinConfidence             float64 `json:"min_confidence"`
	EnableTimeParser          bool    `json:"enable_time_parser"`
	ReturnDebugCandidates     bool    `json:"return_debug_candidates"`
	ReturnDebugEntities       bool    `json:"return_debug_entities"`
	EmitSystemIntentWhenEmpty bool    `json:"emit_system_intent_when_empty"`
}

type IntentFilterRequest struct {
	RequestID     string              `json:"request_id,omitempty"`
	Command       string              `json:"command"`
	IntentCatalog []IntentSpec        `json:"intent_catalog"`
	Options       IntentFilterOptions `json:"options"`
}

type IntentFilterTextSpan struct {
	Text  string `json:"text"`
	Start int    `json:"start"`
	End   int    `json:"end"`
}

type IntentFilterEvidence struct {
	Type  string  `json:"type"`
	Value string  `json:"value"`
	Score float64 `json:"score"`
}

type SelectedIntent struct {
	IntentID          string                 `json:"intent_id"`
	IntentName        string                 `json:"intent_name"`
	Confidence        float64                `json:"confidence"`
	Status            string                 `json:"status"`
	SegmentIndex      int                    `json:"segment_index"`
	Span              IntentFilterTextSpan   `json:"span"`
	Parameters        map[string]any         `json:"parameters"`
	Normalized        map[string]any         `json:"normalized"`
	MissingParameters []string               `json:"missing_parameters"`
	Evidence          []IntentFilterEvidence `json:"evidence"`
}

type IntentFilterDecision struct {
	Action          string `json:"action"`
	TriggerIntentID string `json:"trigger_intent_id"`
	Reason          string `json:"reason"`
}

type IntentFilterResponse struct {
	RequestID string               `json:"request_id"`
	Intents   []SelectedIntent     `json:"intents"`
	Decision  IntentFilterDecision `json:"decision"`
	Meta      map[string]any       `json:"meta"`
}
