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

func TestLowEmotionInputHasMinimalImpact(t *testing.T) {
	engine := NewEngine(DefaultConfig())
	base, err := VectorFromMBTI("INFJ")
	if err != nil {
		t.Fatalf("vector generation failed: %v", err)
	}

	now := time.Now().UTC()
	baseState := InitialEmotionState(now)
	baseline := engine.Update(base, baseState, UpdateInput{
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

	lowEmotion := engine.Update(base, baseState, UpdateInput{
		Now:          now,
		HasUserInput: true,
		UserEmotion: domain.EmotionSignal{
			Emotion:   "neutral",
			P:         0.14,
			A:         0.08,
			D:         0.03,
			Intensity: 0.20,
		},
	}, 0.95)

	extreme := engine.Update(base, baseState, UpdateInput{
		Now:          now,
		HasUserInput: true,
		UserEmotion: domain.EmotionSignal{
			Emotion:   "anger",
			P:         -1,
			A:         1,
			D:         1,
			Intensity: 1,
		},
	}, 0.95)

	lowImpact := padDeltaNorm(lowEmotion.State, baseline.State)
	extremeImpact := padDeltaNorm(extreme.State, baseline.State)
	if extremeImpact <= 0 {
		t.Fatalf("expected extreme emotion to produce non-zero impact")
	}
	if lowImpact > extremeImpact*0.15 {
		t.Fatalf("expected low-emotion impact to be tiny, low=%.6f extreme=%.6f", lowImpact, extremeImpact)
	}
}

func TestExecutionGateBlocksOnlyWhenLockActive(t *testing.T) {
	engine := NewEngine(DefaultConfig())
	now := time.Now().UTC()

	state := domain.SoulEmotionState{
		P:             -0.96,
		A:             0.98,
		D:             0.72,
		ShockLoad:     0.95,
		ExtremeMemory: 0.90,
	}

	locked := state
	locked.LockUntil = now.Add(90 * time.Second).Format(time.RFC3339Nano)

	unlockedProb, unlockedMode := engine.ExecutionProbability(domain.PersonalityVector{}, state, 0.05, now)
	lockedProb, lockedMode := engine.ExecutionProbability(domain.PersonalityVector{}, locked, 0.95, now)
	expiredProb, expiredMode := engine.ExecutionProbability(domain.PersonalityVector{}, locked, 0.95, now.Add(2*time.Minute))

	if unlockedProb != 1 || unlockedMode != "auto_execute" {
		t.Fatalf("expected unlocked state to always execute, got prob=%.2f mode=%s", unlockedProb, unlockedMode)
	}
	if lockedProb != 0 || lockedMode != "blocked" {
		t.Fatalf("expected locked state to block, got prob=%.2f mode=%s", lockedProb, lockedMode)
	}
	if expiredProb != 1 || expiredMode != "auto_execute" {
		t.Fatalf("expected expired lock to recover execution, got prob=%.2f mode=%s", expiredProb, expiredMode)
	}
}

func padDeltaNorm(a, b domain.SoulEmotionState) float64 {
	dp := a.P - b.P
	da := a.A - b.A
	dd := a.D - b.D
	return math.Sqrt((dp*dp + da*da + dd*dd) / 3)
}

func assertNear(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.0001 {
		t.Fatalf("value mismatch: got=%.6f want=%.6f", got, want)
	}
}
