package orchestrator

import (
	"strings"
	"testing"

	"soul/internal/domain"
)

func TestNormalizeAssistantReply(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		wantReply  string
		wantSilent bool
	}{
		{name: "marker angle", input: "<NO_REPLY>", wantReply: "", wantSilent: true},
		{name: "marker plain", input: "NO_REPLY", wantReply: "", wantSilent: true},
		{name: "marker bracket", input: "[NO_REPLY]", wantReply: "", wantSilent: true},
		{name: "normal text", input: "  好的  ", wantReply: "好的", wantSilent: false},
		{name: "empty", input: "   ", wantReply: "", wantSilent: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotReply, gotSilent := normalizeAssistantReply(tt.input)
			if gotReply != tt.wantReply || gotSilent != tt.wantSilent {
				t.Fatalf("normalizeAssistantReply(%q) = (%q,%v), want (%q,%v)", tt.input, gotReply, gotSilent, tt.wantReply, tt.wantSilent)
			}
		})
	}
}

func TestInferTargetPersonaHintFromMBTI(t *testing.T) {
	hint := inferTargetPersonaHint("目标人物是 intj，比较理性")
	if !hint.Known {
		t.Fatalf("expected known target persona")
	}
	if hint.Source != "mbti_mention" {
		t.Fatalf("unexpected source: %s", hint.Source)
	}
	if hint.Label != "INTJ" {
		t.Fatalf("unexpected label: %s", hint.Label)
	}
}

func TestBuildSystemPromptContainsRelationAndNoReplyRule(t *testing.T) {
	prompt := buildSystemPrompt(
		"历史会话压缩摘要：\n无",
		nil,
		false,
		llmEmotionPromptSnapshot{
			ExecMode:        "auto_execute",
			ExecProbability: 0.9,
			UserEmotion:     domain.EmotionSignal{Emotion: "neutral", Intensity: 0.2},
		},
		"- target_persona: INTJ\n- relation_strategy: 先给结论。",
	)
	if !strings.Contains(prompt, "人格关系快照") {
		t.Fatalf("prompt missing relation snapshot section")
	}
	if !strings.Contains(prompt, "<NO_REPLY>") {
		t.Fatalf("prompt missing NO_REPLY rule")
	}
}
