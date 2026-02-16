package memory

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"soul/internal/db"
	"soul/internal/domain"
)

type ExternalMemory interface {
	Add(ctx context.Context, entry ExternalMemoryEntry) error
	Search(ctx context.Context, query string, filter ExternalMemoryFilter, topK int) ([]string, error)
}

type ExternalMemoryEntry struct {
	Text       string
	Role       string
	UserID     string
	SoulID     string
	SessionID  string
	TerminalID string
}

type ExternalMemoryFilter struct {
	UserID     string
	SoulID     string
	SessionID  string
	TerminalID string
}

type Service struct {
	store          *db.Store
	externalMemory ExternalMemory
	externalTopK   int
}

func NewService(store *db.Store, externalMemory ExternalMemory, externalTopK int) (*Service, error) {
	if externalTopK <= 0 {
		externalTopK = 5
	}
	if externalMemory == nil {
		return nil, errors.New("external memory is required")
	}
	return &Service{
		store:          store,
		externalMemory: externalMemory,
		externalTopK:   externalTopK,
	}, nil
}

func (s *Service) ResolveOrCreateSoul(ctx context.Context, userID, terminalID, soulHint string) (string, error) {
	return s.store.ResolveOrCreateSoul(ctx, userID, terminalID, soulHint)
}

func (s *Service) PersistMessage(ctx context.Context, sessionID, userID, terminalID, soulID, role, name, toolCallID, content string) error {
	if err := s.store.SaveMessage(ctx, sessionID, userID, terminalID, soulID, role, name, toolCallID, content); err != nil {
		return err
	}

	if role != "user" && role != "assistant" {
		return nil
	}

	entry := ExternalMemoryEntry{
		Text:       content,
		Role:       role,
		UserID:     userID,
		SoulID:     soulID,
		SessionID:  sessionID,
		TerminalID: terminalID,
	}
	return s.externalMemory.Add(ctx, entry)
}

func (s *Service) RecentMessages(ctx context.Context, sessionID string, limit int) ([]domain.Message, error) {
	return s.store.GetRecentMessages(ctx, sessionID, limit)
}

func (s *Service) BuildContext(ctx context.Context, userID, soulID, sessionID, terminalID, query string) (string, error) {
	base, err := s.store.BuildMemoryContext(ctx, soulID)
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(query) == "" {
		return base, nil
	}

	items, err := s.externalMemory.Search(ctx, query, ExternalMemoryFilter{
		UserID:     userID,
		SoulID:     soulID,
		SessionID:  sessionID,
		TerminalID: terminalID,
	}, s.externalTopK)
	if err != nil {
		return "", err
	}
	if len(items) == 0 {
		return base, nil
	}

	return fmt.Sprintf("%s\n语义记忆检索:\n- %s", base, strings.Join(items, "\n- ")), nil
}
