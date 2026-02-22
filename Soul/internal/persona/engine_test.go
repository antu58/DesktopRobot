package persona

import (
	"math"
	"testing"
	"time"

	"soul/internal/domain"
)

func TestVectorFromMBTI_INFJ(t *testing.T) {
	v, err := VectorFromMBTI("INFJ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	assertNear(t, v.Empathy, 0.67)
	assertNear(t, v.Sensitivity, 0.47)
	assertNear(t, v.Stability, 0.51)
	assertNear(t, v.Expressiveness, 0.36)
	assertNear(t, v.Dominance, 0.33)
}

func TestExtremeEmotionLowersExecutionProbability(t *testing.T) {
	engine := NewEngine(DefaultConfig())
	base, err := VectorFromMBTI("INFJ")
	if err != nil {
		t.Fatalf("vector generation failed: %v", err)
	}

	now := time.Now().UTC()
	baseState := InitialEmotionState(now.Add(-30 * time.Second))
	neutral := engine.Update(base, baseState, UpdateInput{
		Now:          now,
		HasUserInput: true,
		UserEmotion: domain.EmotionSignal{
			Emotion:   "neutral",
			P:         0,
			A:         0,
			D:         0,
			Intensity: 0,
		},
	}, 0.95)

	extreme := engine.Update(base, neutral.State, UpdateInput{
		Now:          now.Add(20 * time.Second),
		HasUserInput: true,
		UserEmotion: domain.EmotionSignal{
			Emotion:   "anger",
			P:         -1,
			A:         1,
			D:         1,
			Intensity: 1,
		},
	}, 0.95)

	if extreme.ExecProbability >= neutral.ExecProbability {
		t.Fatalf("expected lower exec probability on extreme emotion, neutral=%.4f extreme=%.4f", neutral.ExecProbability, extreme.ExecProbability)
	}
	if extreme.ExecMode == "confirm_required" || neutral.ExecMode == "confirm_required" {
		t.Fatalf("confirm_required mode should be disabled, got neutral=%s extreme=%s", neutral.ExecMode, extreme.ExecMode)
	}
}

func TestExecutionProbabilityDiffersByPersonality(t *testing.T) {
	engine := NewEngine(DefaultConfig())
	now := time.Now().UTC()

	state := domain.SoulEmotionState{
		P:             -0.42,
		A:             -0.35,
		D:             -0.16,
		ShockLoad:     0.10,
		ExtremeMemory: 0.34,
	}

	resilient := domain.PersonalityVector{
		Empathy:        0.55,
		Sensitivity:    0.22,
		Stability:      0.84,
		Expressiveness: 0.48,
		Dominance:      0.74,
	}
	reactive := domain.PersonalityVector{
		Empathy:        0.55,
		Sensitivity:    0.86,
		Stability:      0.28,
		Expressiveness: 0.48,
		Dominance:      0.26,
	}

	probResilient, modeResilient := engine.ExecutionProbability(resilient, state, 0.95, now)
	probReactive, modeReactive := engine.ExecutionProbability(reactive, state, 0.95, now)

	if probResilient <= probReactive {
		t.Fatalf("expected resilient personality to have higher execution probability, resilient=%.4f reactive=%.4f", probResilient, probReactive)
	}
	if modeResilient == "confirm_required" || modeReactive == "confirm_required" {
		t.Fatalf("confirm_required mode should be disabled, resilient=%s reactive=%s", modeResilient, modeReactive)
	}
}

func assertNear(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.0001 {
		t.Fatalf("value mismatch: got=%.6f want=%.6f", got, want)
	}
}
