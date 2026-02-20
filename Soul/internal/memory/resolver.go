package memory

import (
	"context"
)

type TerminalSoulResolver struct {
	userID string
	svc    *Service
}

func NewTerminalSoulResolver(userID string, svc *Service) *TerminalSoulResolver {
	return &TerminalSoulResolver{userID: userID, svc: svc}
}

func (r *TerminalSoulResolver) ResolveOrCreateSoul(ctx context.Context, terminalID, soulHint string) (string, error) {
	return r.svc.ResolveOrCreateSoul(ctx, r.userID, terminalID, soulHint)
}
