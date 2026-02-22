package orchestrator

import (
	"context"
	"strings"
	"time"

	"soul/internal/domain"
	"soul/internal/persona"
)

const emotionDecaySessionID = "system_decay_tick"

func (s *Service) RunEmotionDecayPublisher(ctx context.Context, interval time.Duration) {
	if s == nil || s.personaEngine == nil || s.memoryService == nil || s.skillRegistry == nil {
		return
	}
	publisher, ok := s.invoker.(EmotionPublisher)
	if !ok {
		return
	}

	if interval < 2*time.Second {
		interval = 2 * time.Second
	}
	if interval > 5*time.Second {
		interval = 5 * time.Second
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	s.logger.Info("emotion decay publisher started", "interval", interval)

	for {
		select {
		case <-ctx.Done():
			return
		case tickAt := <-ticker.C:
			s.publishEmotionDecayTick(ctx, publisher, tickAt.UTC())
		}
	}
}

func (s *Service) publishEmotionDecayTick(ctx context.Context, publisher EmotionPublisher, now time.Time) {
	states := s.skillRegistry.ListOnlineStates()
	if len(states) == 0 {
		return
	}

	neutral := domain.EmotionSignal{
		Emotion:    "neutral",
		P:          0,
		A:          0,
		D:          0,
		Intensity:  0,
		Confidence: 1,
	}

	for _, terminal := range states {
		if ctx.Err() != nil {
			return
		}
		terminalID := strings.TrimSpace(terminal.TerminalID)
		soulID := strings.TrimSpace(terminal.SoulID)
		if terminalID == "" || soulID == "" {
			continue
		}

		s.emotionMu.Lock()
		soulProfile, err := s.memoryService.GetSoulProfileByID(ctx, soulID)
		if err != nil {
			s.emotionMu.Unlock()
			s.logger.Warn("emotion decay tick: load soul profile failed", "terminal_id", terminalID, "soul_id", soulID, "error", err)
			continue
		}

		result := s.personaEngine.Update(
			soulProfile.PersonalityVector,
			soulProfile.EmotionState,
			persona.UpdateInput{
				Now:          now,
				UserEmotion:  neutral,
				HasUserInput: false,
			},
			personaBaseExecProb,
		)
		if err := s.memoryService.UpdateSoulEmotionState(ctx, soulID, result.State); err != nil {
			s.emotionMu.Unlock()
			s.logger.Warn("emotion decay tick: update soul emotion state failed", "terminal_id", terminalID, "soul_id", soulID, "error", err)
			continue
		}
		s.emotionMu.Unlock()

		payload := domain.EmotionUpdatePayload{
			SessionID:       emotionDecaySessionID,
			TerminalID:      terminalID,
			SoulID:          soulID,
			UserEmotion:     neutral,
			SoulEmotion:     result.State,
			ExecProbability: result.ExecProbability,
			ExecMode:        result.ExecMode,
			TS:              now.Format(time.RFC3339Nano),
		}
		if err := publisher.PublishEmotionUpdate(ctx, terminalID, payload); err != nil {
			s.logger.Warn("emotion decay tick: publish emotion update failed", "terminal_id", terminalID, "soul_id", soulID, "error", err)
		}
	}
}
