package persona

import (
	"fmt"
	"math"
	"strings"
	"time"

	"soul/internal/domain"
)

const ModelVersion = "persona-pad-v2"

type Config struct {
	IdleAfterSeconds        float64
	BoredomTauUpSeconds     float64
	BoredomTauDownSeconds   float64
	ActiveRecoverySeconds   float64
	ImpactBase              float64
	MaxImpactNorm           float64
	NegativeImpactGain      float64
	PositiveImpactGain      float64
	ShockTheta              float64
	ShockTauBaseSeconds     float64
	ShockNegativeGain       float64
	ShockPositiveGain       float64
	RecoveryBaseRate        float64
	ExtremeMemoryTauSeconds float64
	DriftEtaPerSecond       float64
	DriftGammaPerSecond     float64
	DriftMaxAbs             float64
	LockBaseSeconds         float64
	LockRefreshMinSeconds   float64
	LockRefreshMaxSeconds   float64
	PositiveUnlockMinRatio  float64
	PositiveUnlockMaxRatio  float64
	ExtremeEta              float64
	ShockXi                 float64
}

type Engine struct {
	cfg Config
}

type UpdateInput struct {
	Now          time.Time
	UserEmotion  domain.EmotionSignal
	HasUserInput bool
}

type UpdateResult struct {
	State           domain.SoulEmotionState
	Effective       domain.PersonalityVector
	ExecProbability float64
	ExecMode        string
}

func DefaultConfig() Config {
	return Config{
		IdleAfterSeconds:        18,
		BoredomTauUpSeconds:     240,
		BoredomTauDownSeconds:   90,
		ActiveRecoverySeconds:   2,
		ImpactBase:              0.55,
		MaxImpactNorm:           0.42,
		NegativeImpactGain:      1.30,
		PositiveImpactGain:      0.62,
		ShockTheta:              0.08,
		ShockTauBaseSeconds:     120,
		ShockNegativeGain:       1.25,
		ShockPositiveGain:       0.58,
		RecoveryBaseRate:        0.18,
		ExtremeMemoryTauSeconds: 360,
		DriftEtaPerSecond:       0.00009,
		DriftGammaPerSecond:     0.00004,
		DriftMaxAbs:             0.22,
		LockBaseSeconds:         120,
		LockRefreshMinSeconds:   18,
		LockRefreshMaxSeconds:   48,
		PositiveUnlockMinRatio:  0.20,
		PositiveUnlockMaxRatio:  0.75,
		ExtremeEta:              0.95,
		ShockXi:                 0.8,
	}
}

func NewEngine(cfg Config) *Engine {
	if cfg.IdleAfterSeconds <= 0 {
		cfg = DefaultConfig()
	} else {
		defaults := DefaultConfig()
		if cfg.NegativeImpactGain <= 0 {
			cfg.NegativeImpactGain = defaults.NegativeImpactGain
		}
		if cfg.PositiveImpactGain <= 0 {
			cfg.PositiveImpactGain = defaults.PositiveImpactGain
		}
		if cfg.ShockNegativeGain <= 0 {
			cfg.ShockNegativeGain = defaults.ShockNegativeGain
		}
		if cfg.ShockPositiveGain <= 0 {
			cfg.ShockPositiveGain = defaults.ShockPositiveGain
		}
		if cfg.LockBaseSeconds <= 0 {
			cfg.LockBaseSeconds = defaults.LockBaseSeconds
		}
		if cfg.LockRefreshMinSeconds <= 0 {
			cfg.LockRefreshMinSeconds = defaults.LockRefreshMinSeconds
		}
		if cfg.LockRefreshMaxSeconds <= cfg.LockRefreshMinSeconds {
			cfg.LockRefreshMaxSeconds = defaults.LockRefreshMaxSeconds
		}
		if cfg.PositiveUnlockMinRatio <= 0 {
			cfg.PositiveUnlockMinRatio = defaults.PositiveUnlockMinRatio
		}
		if cfg.PositiveUnlockMaxRatio <= cfg.PositiveUnlockMinRatio {
			cfg.PositiveUnlockMaxRatio = defaults.PositiveUnlockMaxRatio
		}
	}
	return &Engine{cfg: cfg}
}

func VectorFromMBTI(raw string) (domain.PersonalityVector, error) {
	mbti := strings.ToUpper(strings.TrimSpace(raw))
	if len(mbti) != 4 {
		return domain.PersonalityVector{}, fmt.Errorf("mbti must be 4 letters")
	}
	chars := []byte(mbti)
	if !contains(chars[0], "EI") || !contains(chars[1], "SN") || !contains(chars[2], "TF") || !contains(chars[3], "JP") {
		return domain.PersonalityVector{}, fmt.Errorf("invalid mbti type: %s", raw)
	}

	v := domain.PersonalityVector{
		Empathy:        0.5,
		Sensitivity:    0.5,
		Stability:      0.5,
		Expressiveness: 0.5,
		Dominance:      0.5,
	}
	apply := func(bias domain.PersonalityVector, positive bool) {
		sign := 1.0
		if !positive {
			sign = -1.0
		}
		v.Empathy = clamp01(v.Empathy + sign*bias.Empathy)
		v.Sensitivity = clamp01(v.Sensitivity + sign*bias.Sensitivity)
		v.Stability = clamp01(v.Stability + sign*bias.Stability)
		v.Expressiveness = clamp01(v.Expressiveness + sign*bias.Expressiveness)
		v.Dominance = clamp01(v.Dominance + sign*bias.Dominance)
	}

	apply(domain.PersonalityVector{
		Empathy:        0.00,
		Sensitivity:    -0.03,
		Stability:      0.00,
		Expressiveness: 0.20,
		Dominance:      0.10,
	}, chars[0] == 'E')
	apply(domain.PersonalityVector{
		Empathy:        -0.02,
		Sensitivity:    0.08,
		Stability:      0.06,
		Expressiveness: -0.03,
		Dominance:      0.03,
	}, chars[1] == 'S')
	apply(domain.PersonalityVector{
		Empathy:        -0.15,
		Sensitivity:    -0.05,
		Stability:      0.08,
		Expressiveness: -0.05,
		Dominance:      0.12,
	}, chars[2] == 'T')
	apply(domain.PersonalityVector{
		Empathy:        0.00,
		Sensitivity:    -0.03,
		Stability:      0.15,
		Expressiveness: -0.02,
		Dominance:      0.08,
	}, chars[3] == 'J')
	return v, nil
}

func InitialEmotionState(now time.Time) domain.SoulEmotionState {
	if now.IsZero() {
		now = time.Now().UTC()
	}
	return domain.SoulEmotionState{
		LastInteractionAt: now.UTC().Format(time.RFC3339Nano),
		LastUpdatedAt:     now.UTC().Format(time.RFC3339Nano),
	}
}

func (e *Engine) EffectiveVector(base, drift domain.PersonalityVector) domain.PersonalityVector {
	return domain.PersonalityVector{
		Empathy:        clamp01(base.Empathy + drift.Empathy),
		Sensitivity:    clamp01(base.Sensitivity + drift.Sensitivity),
		Stability:      clamp01(base.Stability + drift.Stability),
		Expressiveness: clamp01(base.Expressiveness + drift.Expressiveness),
		Dominance:      clamp01(base.Dominance + drift.Dominance),
	}
}

func (e *Engine) Update(base domain.PersonalityVector, prev domain.SoulEmotionState, in UpdateInput, baseExecProbability float64) UpdateResult {
	now := in.Now.UTC()
	if now.IsZero() {
		now = time.Now().UTC()
	}
	if strings.TrimSpace(prev.LastUpdatedAt) == "" {
		prev = InitialEmotionState(now)
	}
	lastUpdated := parseTimeOr(now, prev.LastUpdatedAt)
	if lastUpdated.After(now) {
		lastUpdated = now
	}

	dt := now.Sub(lastUpdated).Seconds()
	if dt < 0 {
		dt = 0
	}
	if dt > 7200 {
		dt = 7200
	}

	eff := e.EffectiveVector(base, prev.Drift)
	updated := prev

	lastInteraction := parseOptionalTime(updated.LastInteractionAt)
	if lastInteraction.IsZero() {
		lastInteraction = lastUpdated
	}
	hasUserInput := in.HasUserInput
	if !hasUserInput {
		emotionLabel := strings.ToLower(strings.TrimSpace(in.UserEmotion.Emotion))
		padAbs := math.Abs(in.UserEmotion.P) + math.Abs(in.UserEmotion.A) + math.Abs(in.UserEmotion.D)
		if in.UserEmotion.Intensity > 0.01 || (emotionLabel != "" && emotionLabel != "neutral") || padAbs > 0.08 {
			hasUserInput = true
		}
	}
	if hasUserInput {
		lastInteraction = now
	}
	idleSeconds := now.Sub(lastInteraction).Seconds()
	if idleSeconds < 0 {
		idleSeconds = 0
	}

	// 1) idle -> boredom and active recovery.
	isIdleGap := idleSeconds >= e.cfg.IdleAfterSeconds
	if isIdleGap {
		tauUp := math.Max(30, e.cfg.BoredomTauUpSeconds*(1+0.6*eff.Stability-0.7*eff.Sensitivity))
		updated.Boredom = 1 - (1-updated.Boredom)*math.Exp(-dt/tauUp)
	}
	if hasUserInput {
		tauDown := math.Max(20, e.cfg.BoredomTauDownSeconds*(1+0.5*eff.Sensitivity-0.7*eff.Stability))
		updated.Boredom = updated.Boredom * math.Exp(-e.cfg.ActiveRecoverySeconds/tauDown)
	}
	updated.Boredom = clamp01(updated.Boredom)

	// 2) user emotion shock.
	targetP, targetA, targetD := neutralPAD(eff)
	targetP = (1-updated.Boredom)*targetP + updated.Boredom*(-0.35)
	targetA = (1-updated.Boredom)*targetA + updated.Boredom*(-0.45)
	targetD = (1-updated.Boredom)*targetD + updated.Boredom*(-0.15)

	intensity := clamp01(in.UserEmotion.Intensity)
	k := e.cfg.ImpactBase * ((0.5 + eff.Empathy) * (0.5 + eff.Sensitivity) / (0.7 + eff.Stability))
	negativePolarity, positivePolarity := emotionPolarity(in.UserEmotion)
	impactGain := e.impactGainByPolarity(negativePolarity, positivePolarity)
	deltaP := intensity * k * impactGain * in.UserEmotion.P
	deltaA := intensity * k * impactGain * in.UserEmotion.A
	deltaD := intensity * k * impactGain * (in.UserEmotion.D + 0.2*(eff.Dominance-0.5))
	dNorm := math.Sqrt((deltaP*deltaP + deltaA*deltaA + deltaD*deltaD) / 3)
	if dNorm > e.cfg.MaxImpactNorm && dNorm > 0 {
		scale := e.cfg.MaxImpactNorm / dNorm
		deltaP *= scale
		deltaA *= scale
		deltaD *= scale
		dNorm = e.cfg.MaxImpactNorm
	}

	tauS := math.Max(12, e.cfg.ShockTauBaseSeconds*(1+0.9*eff.Sensitivity-0.8*eff.Stability))
	shockGain := e.shockGainByPolarity(negativePolarity, positivePolarity)
	updated.ShockLoad = updated.ShockLoad*math.Exp(-dt/tauS) + shockGain*math.Max(0, dNorm-e.cfg.ShockTheta)
	updated.ShockLoad = clamp01(updated.ShockLoad)

	lambda := e.cfg.RecoveryBaseRate * (0.5 + eff.Stability) / (1 + 1.5*updated.ShockLoad)
	// Use exponential gain instead of linear dt scaling to avoid overshooting to +/-1
	// after long gaps (which would force extended blocked mode).
	recoveryGain := 1 - math.Exp(-lambda*dt)
	updated.P = clampSigned(updated.P + deltaP + recoveryGain*(targetP-updated.P))
	updated.A = clampSigned(updated.A + deltaA + recoveryGain*(targetA-updated.A))
	updated.D = clampSigned(updated.D + deltaD + recoveryGain*(targetD-updated.D))

	// 3) long-term PAD features and persona drift.
	alphaLong := 1 - math.Exp(-dt/2400)
	if dt == 0 {
		alphaLong = 0.04
	}
	oldP, oldA, oldD := prev.P, prev.A, prev.D
	updated.LongMuP = lerp(updated.LongMuP, updated.P, alphaLong)
	updated.LongMuA = lerp(updated.LongMuA, math.Abs(updated.A), alphaLong)
	updated.LongMuD = lerp(updated.LongMuD, updated.D, alphaLong)
	vol := (math.Abs(updated.P-oldP) + math.Abs(updated.A-oldA) + math.Abs(updated.D-oldD)) / 3
	updated.LongVolatility = lerp(updated.LongVolatility, vol, alphaLong)

	eta := e.cfg.DriftEtaPerSecond * math.Max(dt, 1)
	gamma := e.cfg.DriftGammaPerSecond * math.Max(dt, 1)
	qEmpathy := clampSigned(0.45*updated.LongMuP - 0.55*updated.LongVolatility - 0.35*updated.ExtremeMemory)
	qSensitivity := clampSigned(0.65*updated.LongMuA + 0.45*updated.ExtremeMemory + 0.35*updated.Boredom)
	qStability := clampSigned(-0.8*updated.LongVolatility - 0.55*updated.ShockLoad + 0.3*(1-updated.LongMuA))
	qExpressiveness := clampSigned(0.35*math.Abs(updated.LongMuP) + 0.45*updated.LongMuA - 0.25*updated.Boredom)
	qDominance := clampSigned(0.7*updated.LongMuD - 0.4*updated.LongMuA + 0.2*eff.Dominance)

	updated.Drift.Empathy = clamp(updated.Drift.Empathy*(1-gamma)+eta*qEmpathy, -e.cfg.DriftMaxAbs, e.cfg.DriftMaxAbs)
	updated.Drift.Sensitivity = clamp(updated.Drift.Sensitivity*(1-gamma)+eta*qSensitivity, -e.cfg.DriftMaxAbs, e.cfg.DriftMaxAbs)
	updated.Drift.Stability = clamp(updated.Drift.Stability*(1-gamma)+eta*qStability, -e.cfg.DriftMaxAbs, e.cfg.DriftMaxAbs)
	updated.Drift.Expressiveness = clamp(updated.Drift.Expressiveness*(1-gamma)+eta*qExpressiveness, -e.cfg.DriftMaxAbs, e.cfg.DriftMaxAbs)
	updated.Drift.Dominance = clamp(updated.Drift.Dominance*(1-gamma)+eta*qDominance, -e.cfg.DriftMaxAbs, e.cfg.DriftMaxAbs)

	// 4) extreme memory and lock.
	z := math.Max(math.Abs(updated.P), math.Max(math.Abs(updated.A), math.Abs(updated.D)))
	traitResilience, traitReactivity := personalityTraits(eff)
	extremeTau := e.extremeMemoryTauSeconds(eff, updated.ShockLoad)
	mAlpha := 1 - math.Exp(-dt/extremeTau)
	if dt == 0 {
		mAlpha = clamp(2.0/extremeTau, 0.02, 0.10)
	}
	updated.ExtremeMemory = lerp(updated.ExtremeMemory, z, mAlpha)
	updated.ExtremeMemory = clamp01(updated.ExtremeMemory)

	lockUntil := parseOptionalTime(updated.LockUntil)
	stableSince := parseOptionalTime(updated.StableSince)
	negativeTrigger := negativePolarity >= 0.35 && negativePolarity >= positivePolarity*1.08
	thresholdTriggered := negativeTrigger && (z >= 0.95 || updated.ShockLoad >= 0.9)
	if thresholdTriggered {
		lockBaseSeconds := math.Max(1, e.cfg.LockBaseSeconds)
		if lockUntil.IsZero() || !lockUntil.After(now) {
			// First threshold trigger: lock to fixed 120s baseline.
			lockUntil = now.Add(time.Duration(lockBaseSeconds * float64(time.Second)))
		} else {
			// Re-trigger while locked: extend lock window with personality-aware refresh seconds.
			refreshSeconds := e.lockRefreshSeconds(z, updated.ShockLoad, traitResilience, traitReactivity)
			lockUntil = lockUntil.Add(time.Duration(refreshSeconds * float64(time.Second)))
		}
		stableSince = time.Time{}
	} else if !lockUntil.IsZero() && lockUntil.After(now) {
		// No reset trigger: positive emotions can reduce remaining lock time.
		sootheStrength := e.positiveSootheStrength(positivePolarity, negativePolarity, in.UserEmotion, eff)
		if sootheStrength > 0.01 {
			remainingSeconds := lockUntil.Sub(now).Seconds()
			reductionRatio := lerp(e.cfg.PositiveUnlockMinRatio, e.cfg.PositiveUnlockMaxRatio, sootheStrength)
			reduceSeconds := remainingSeconds * clamp(reductionRatio, 0.01, 0.95)
			remainingAfter := remainingSeconds - reduceSeconds
			if remainingAfter <= 0.5 {
				lockUntil = time.Time{}
			} else {
				lockUntil = now.Add(time.Duration(remainingAfter * float64(time.Second)))
			}
		}
	}
	if !lockUntil.IsZero() && !lockUntil.After(now) {
		lockUntil = time.Time{}
	}
	if lockUntil.IsZero() {
		if z < 0.7 && updated.ShockLoad < 0.35 {
			if stableSince.IsZero() {
				stableSince = now
			}
		} else {
			stableSince = time.Time{}
		}
	} else {
		stableSince = time.Time{}
	}
	updated.LockUntil = formatOptionalTime(lockUntil)
	updated.StableSince = formatOptionalTime(stableSince)
	updated.LastInteractionAt = formatOptionalTime(lastInteraction)
	updated.LastUpdatedAt = now.Format(time.RFC3339Nano)

	eff = e.EffectiveVector(base, updated.Drift)
	prob, mode := e.ExecutionProbability(eff, updated, baseExecProbability, now)
	return UpdateResult{
		State:           updated,
		Effective:       eff,
		ExecProbability: prob,
		ExecMode:        mode,
	}
}

func (e *Engine) ExecutionProbability(eff domain.PersonalityVector, state domain.SoulEmotionState, base float64, now time.Time) (float64, string) {
	base = clamp01(base)
	z := math.Max(math.Abs(state.P), math.Max(math.Abs(state.A), math.Abs(state.D)))
	traitResilience, traitReactivity := personalityTraits(eff)
	tau := clamp(0.6-0.2*eff.Sensitivity+0.2*eff.Stability, 0.35, 0.85)
	alpha := clamp(2.2+2.8*eff.Sensitivity-1.8*eff.Stability, 0.6, 5.5)
	zn := 0.0
	if tau < 1 {
		zn = clamp((z-tau)/(1-tau), 0, 1)
	}
	extremePenalty := e.cfg.ExtremeEta * clamp(0.55+0.90*traitReactivity-0.45*traitResilience, 0.35, 1.30)
	shockPenalty := e.cfg.ShockXi * clamp(0.55+1.00*traitReactivity-0.35*traitResilience, 0.35, 1.35)
	g := math.Exp(-(alpha*math.Pow(zn, 3) + extremePenalty*state.ExtremeMemory + shockPenalty*state.ShockLoad))
	lockUntil := parseOptionalTime(state.LockUntil)
	if !lockUntil.IsZero() && now.Before(lockUntil) {
		g *= 0.02
	}
	prob := clamp01(base * g)

	// Remove the middle confirm band: mode is now binary (auto_execute / blocked).
	executeThreshold := clamp(0.36-0.12*traitResilience+0.08*traitReactivity, 0.16, 0.48)
	mode := "blocked"
	if prob >= executeThreshold {
		mode = "auto_execute"
	}
	return prob, mode
}

func neutralPAD(v domain.PersonalityVector) (float64, float64, float64) {
	p := clampSigned(0.25*(v.Empathy-0.5) + 0.15*(v.Stability-0.5))
	a := clampSigned(0.20*(v.Expressiveness-0.5) - 0.10*(v.Stability-0.5))
	d := clampSigned(0.50 * (v.Dominance - 0.5))
	return p, a, d
}

func personalityTraits(eff domain.PersonalityVector) (resilience, reactivity float64) {
	resilience = clamp01(0.58*eff.Stability + 0.22*(1-eff.Sensitivity) + 0.20*eff.Dominance)
	reactivity = clamp01(0.55*eff.Sensitivity + 0.25*(1-eff.Stability) + 0.20*(1-eff.Dominance))
	return resilience, reactivity
}

func emotionPolarity(sig domain.EmotionSignal) (negative, positive float64) {
	negative = clamp01(0.70*clamp01(-sig.P) + 0.20*clamp01(sig.A) + 0.10*clamp01(-sig.D))
	positive = clamp01(0.72*clamp01(sig.P) + 0.18*clamp01(-sig.A) + 0.10*clamp01(sig.D))

	switch strings.ToLower(strings.TrimSpace(sig.Emotion)) {
	case "anger", "anxiety", "fear", "frustration", "disgust", "sadness", "disappointment", "boredom":
		negative = clamp01(negative + 0.16)
	case "joy", "gratitude", "relief", "calm", "excitement":
		positive = clamp01(positive + 0.14)
	}

	if negative > positive*1.2 {
		positive *= 0.6
	}
	if positive > negative*1.2 {
		negative *= 0.6
	}
	return clamp01(negative), clamp01(positive)
}

func (e *Engine) impactGainByPolarity(negative, positive float64) float64 {
	negGain := lerp(1.0, e.cfg.NegativeImpactGain, clamp01(negative))
	posGain := lerp(1.0, e.cfg.PositiveImpactGain, clamp01(positive))
	return clamp(negGain*posGain, 0.30, 2.60)
}

func (e *Engine) shockGainByPolarity(negative, positive float64) float64 {
	negGain := lerp(1.0, e.cfg.ShockNegativeGain, clamp01(negative))
	posGain := lerp(1.0, e.cfg.ShockPositiveGain, clamp01(positive))
	return clamp(negGain*posGain, 0.25, 2.80)
}

func (e *Engine) extremeMemoryTauSeconds(eff domain.PersonalityVector, shockLoad float64) float64 {
	base := math.Max(60, e.cfg.ExtremeMemoryTauSeconds)
	factor := 1 + 0.85*eff.Sensitivity - 0.95*eff.Stability + 0.35*clamp01(shockLoad)
	return clamp(base*factor, 90, 1200)
}

func (e *Engine) lockRefreshSeconds(z, shockLoad, traitResilience, traitReactivity float64) float64 {
	minSec := math.Max(1, e.cfg.LockRefreshMinSeconds)
	maxSec := math.Max(minSec+1, e.cfg.LockRefreshMaxSeconds)
	severity := clamp01(0.58*clamp01(z) + 0.42*clamp01(shockLoad))
	traitCurve := clamp01(0.55 + 0.75*traitReactivity - 0.35*traitResilience)
	return lerp(minSec, maxSec, clamp01(0.62*severity+0.38*traitCurve))
}

func (e *Engine) positiveSootheStrength(positivePolarity, negativePolarity float64, sig domain.EmotionSignal, eff domain.PersonalityVector) float64 {
	positive := clamp01(positivePolarity)
	negative := clamp01(negativePolarity)
	base := clamp01(positive * (1 - 0.75*negative))

	switch strings.ToLower(strings.TrimSpace(sig.Emotion)) {
	case "joy", "gratitude", "relief", "calm", "excitement":
		base = clamp01(base + 0.20)
	}

	traitBoost := clamp01(0.55*eff.Empathy + 0.45*eff.Stability)
	return clamp01(base * (0.75 + 0.50*traitBoost))
}

func contains(b byte, chars string) bool {
	return strings.ContainsRune(chars, rune(b))
}

func clamp01(v float64) float64 {
	return clamp(v, 0, 1)
}

func clampSigned(v float64) float64 {
	return clamp(v, -1, 1)
}

func clamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func lerp(cur, next, alpha float64) float64 {
	alpha = clamp01(alpha)
	return cur + alpha*(next-cur)
}

func parseTimeOr(fallback time.Time, raw string) time.Time {
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
	if err != nil {
		return fallback
	}
	return t.UTC()
}

func parseOptionalTime(raw string) time.Time {
	if strings.TrimSpace(raw) == "" {
		return time.Time{}
	}
	t, err := time.Parse(time.RFC3339Nano, strings.TrimSpace(raw))
	if err != nil {
		return time.Time{}
	}
	return t.UTC()
}

func formatOptionalTime(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339Nano)
}
