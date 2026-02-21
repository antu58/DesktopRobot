package memory

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"soul/internal/db"
	"soul/internal/domain"
	"soul/internal/llm"
)

type ServiceConfig struct {
	LLMProvider              llm.Provider
	LLMModel                 string
	Mem0Client               *Mem0Client
	CompressMessageThreshold int
	CompressCharThreshold    int
	CompressScanLimit        int
	IdleTimeout              time.Duration
	IdleSummaryScanInterval  time.Duration
	IdleSummaryBatchSize     int
	Mem0AsyncQueueEnabled    bool
}

type Service struct {
	store                    *db.Store
	llmProvider              llm.Provider
	llmModel                 string
	mem0Client               *Mem0Client
	mem0ReadyMu              sync.Mutex
	mem0Ready                bool
	mem0ReadyCheckedAt       time.Time
	mem0ReadyCheckTTL        time.Duration
	compressMessageThreshold int
	compressCharThreshold    int
	compressScanLimit        int
	idleTimeout              time.Duration
	idleSummaryScanInterval  time.Duration
	idleSummaryBatchSize     int
	mem0AsyncQueueEnabled    bool
	logger                   *slog.Logger
}

func NewService(store *db.Store, cfg ServiceConfig, logger *slog.Logger) (*Service, error) {
	if store == nil {
		return nil, fmt.Errorf("store is required")
	}
	if cfg.LLMProvider == nil {
		return nil, fmt.Errorf("llm provider is required")
	}
	if cfg.LLMModel == "" {
		return nil, fmt.Errorf("llm model is required")
	}
	if cfg.CompressMessageThreshold <= 0 {
		cfg.CompressMessageThreshold = 80
	}
	if cfg.CompressCharThreshold <= 0 {
		cfg.CompressCharThreshold = 12000
	}
	if cfg.CompressScanLimit <= 0 {
		cfg.CompressScanLimit = 200
	}
	if cfg.IdleTimeout <= 0 {
		cfg.IdleTimeout = 3 * time.Minute
	}
	if cfg.IdleSummaryScanInterval <= 0 {
		cfg.IdleSummaryScanInterval = 15 * time.Second
	}
	if cfg.IdleSummaryBatchSize <= 0 {
		cfg.IdleSummaryBatchSize = 50
	}
	if logger == nil {
		logger = slog.Default()
	}

	return &Service{
		store:                    store,
		llmProvider:              cfg.LLMProvider,
		llmModel:                 cfg.LLMModel,
		mem0Client:               cfg.Mem0Client,
		mem0ReadyCheckTTL:        5 * time.Second,
		compressMessageThreshold: cfg.CompressMessageThreshold,
		compressCharThreshold:    cfg.CompressCharThreshold,
		compressScanLimit:        cfg.CompressScanLimit,
		idleTimeout:              cfg.IdleTimeout,
		idleSummaryScanInterval:  cfg.IdleSummaryScanInterval,
		idleSummaryBatchSize:     cfg.IdleSummaryBatchSize,
		mem0AsyncQueueEnabled:    cfg.Mem0AsyncQueueEnabled,
		logger:                   logger,
	}, nil
}

func (s *Service) ResolveOrCreateSoul(ctx context.Context, userID, terminalID, soulHint string) (string, error) {
	return s.store.ResolveOrCreateSoul(ctx, userID, terminalID, soulHint)
}

func (s *Service) PersistMessage(ctx context.Context, sessionID, userID, terminalID, soulID, role, name, toolCallID, content string) error {
	return s.store.SaveMessage(ctx, sessionID, userID, terminalID, soulID, role, name, toolCallID, content)
}

func (s *Service) PersistObservation(ctx context.Context, sessionID, userID, terminalID, soulID, content string) error {
	if strings.TrimSpace(content) == "" {
		return nil
	}
	return s.store.SaveMessage(ctx, sessionID, userID, terminalID, soulID, "observation", "", "", content)
}

func (s *Service) RecentMessages(ctx context.Context, sessionID string, limit int) ([]domain.Message, error) {
	return s.store.GetRecentMessages(ctx, sessionID, limit)
}

func (s *Service) GetSessionSummary(ctx context.Context, sessionID string) (string, error) {
	return s.store.GetSessionSummary(ctx, sessionID)
}

func (s *Service) RecallFromMem0(ctx context.Context, query string, filter ExternalMemoryFilter, topK int) ([]string, error) {
	if s.mem0Client == nil {
		return nil, fmt.Errorf("mem0 recall is not configured")
	}
	return s.mem0Client.Search(ctx, query, filter, topK)
}

func (s *Service) IsMem0RecallReady(ctx context.Context) bool {
	if s.mem0Client == nil {
		return false
	}
	now := time.Now()

	s.mem0ReadyMu.Lock()
	if !s.mem0ReadyCheckedAt.IsZero() && now.Sub(s.mem0ReadyCheckedAt) < s.mem0ReadyCheckTTL {
		ready := s.mem0Ready
		s.mem0ReadyMu.Unlock()
		return ready
	}
	s.mem0ReadyMu.Unlock()

	checkCtx, cancel := context.WithTimeout(ctx, 1200*time.Millisecond)
	defer cancel()
	ready := s.mem0Client.IsReady(checkCtx)

	s.mem0ReadyMu.Lock()
	s.mem0Ready = ready
	s.mem0ReadyCheckedAt = now
	s.mem0ReadyMu.Unlock()
	return ready
}

func (s *Service) BuildContext(ctx context.Context, soulID, sessionID, observationDigest string) (string, string, error) {
	profile, err := s.store.LoadSoulProfilePrompt(ctx, soulID)
	if err != nil {
		return "", "", err
	}

	summary, err := s.store.GetSessionSummary(ctx, sessionID)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(summary) == "" {
		summary = "暂无历史摘要。"
	}

	var sb strings.Builder
	sb.WriteString(profile)
	sb.WriteString("\n历史会话压缩摘要:\n")
	sb.WriteString(summary)

	if strings.TrimSpace(observationDigest) != "" {
		sb.WriteString("\n本轮观测文字化:\n")
		sb.WriteString(strings.TrimSpace(observationDigest))
	}
	return sb.String(), summary, nil
}

func (s *Service) MaybeCompressSession(ctx context.Context, sessionID, userID, terminalID, soulID string, force bool) (string, bool, error) {
	state, err := s.store.GetSessionCompactionState(ctx, sessionID)
	if err != nil {
		return "", false, err
	}

	stats, err := s.store.GetSessionCompactionStats(ctx, sessionID, state.LastCompactedMessageID)
	if err != nil {
		return "", false, err
	}
	if stats.MessageCount == 0 {
		return state.Summary, false, nil
	}
	if !force && stats.MessageCount < s.compressMessageThreshold && stats.CharCount < s.compressCharThreshold {
		return state.Summary, false, nil
	}

	chunks, err := s.store.GetMessagesSince(ctx, sessionID, state.LastCompactedMessageID, s.compressScanLimit)
	if err != nil {
		return "", false, err
	}
	if len(chunks) == 0 {
		return state.Summary, false, nil
	}

	nextSummary, err := s.summarize(ctx, state.Summary, chunks)
	if err != nil {
		return state.Summary, false, err
	}
	nextSummary = strings.TrimSpace(nextSummary)
	if nextSummary == "" {
		nextSummary = state.Summary
	}

	lastCompactedID := chunks[len(chunks)-1].ID
	if err := s.store.UpdateSessionSummary(ctx, sessionID, userID, terminalID, soulID, nextSummary, lastCompactedID); err != nil {
		return "", false, err
	}
	return nextSummary, true, nil
}

func (s *Service) RunIdleSummaryWorker(ctx context.Context) {
	ticker := time.NewTicker(s.idleSummaryScanInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			s.processIdleSummaries(ctx)
		}
	}
}

func (s *Service) processIdleSummaries(ctx context.Context) {
	idleBefore := time.Now().Add(-s.idleTimeout)
	items, err := s.store.ListIdleSessionsForSummary(ctx, idleBefore, s.idleSummaryBatchSize)
	if err != nil {
		s.logger.Warn("list idle sessions failed", "error", err)
		return
	}

	for _, item := range items {
		summary, _, err := s.MaybeCompressSession(ctx, item.SessionID, item.UserID, item.TerminalID, item.SoulID, true)
		if err != nil {
			s.logger.Warn("idle compaction failed", "session_id", item.SessionID, "error", err)
			continue
		}
		summary = strings.TrimSpace(summary)

		if summary != "" {
			if err := s.store.InsertMemoryEpisode(ctx, item.SessionID, item.UserID, item.TerminalID, item.SoulID, summary); err != nil {
				s.logger.Warn("insert memory episode failed", "session_id", item.SessionID, "error", err)
			}
			if s.mem0AsyncQueueEnabled {
				if err := s.store.EnqueueMem0AsyncJob(ctx, item.SessionID, item.UserID, item.TerminalID, item.SoulID, summary, "idle_timeout"); err != nil {
					s.logger.Warn("enqueue mem0 async job failed", "session_id", item.SessionID, "error", err)
				}
			}
		}

		if err := s.store.MarkIdleSummaryProcessed(ctx, item.SessionID, time.Now()); err != nil {
			s.logger.Warn("mark idle summary processed failed", "session_id", item.SessionID, "error", err)
		}
	}
}

func (s *Service) summarize(ctx context.Context, previousSummary string, chunks []db.MessageChunk) (string, error) {
	var transcript strings.Builder
	for _, c := range chunks {
		content := strings.TrimSpace(c.Content)
		if content == "" {
			continue
		}
		transcript.WriteString(c.Role)
		transcript.WriteString(": ")
		transcript.WriteString(content)
		transcript.WriteString("\n")
	}

	userPrompt := fmt.Sprintf(
		"旧摘要:\n%s\n\n新增对话片段:\n%s\n\n请输出新的压缩摘要。",
		strings.TrimSpace(previousSummary),
		strings.TrimSpace(transcript.String()),
	)

	resp, err := s.llmProvider.Complete(ctx, domain.LLMRequest{
		Model:  s.llmModel,
		System: "你是会话压缩器。输出中文摘要，保留用户意图、偏好、约束、关键结论、待办。控制在220字以内，不要输出条目编号。",
		Messages: []domain.Message{
			{Role: "user", Content: userPrompt},
		},
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(resp.Content), nil
}
