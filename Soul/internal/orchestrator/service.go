package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"soul/internal/domain"
	"soul/internal/llm"
	"soul/internal/memory"
	"soul/internal/skills"
)

type SkillInvoker interface {
	InvokeSkill(ctx context.Context, terminalID, skill string, args json.RawMessage) (domain.InvokeResult, error)
}

type Service struct {
	userID           string
	chatHistoryLimit int
	toolTimeout      time.Duration
	llmModel         string
	llmProvider      llm.Provider
	memoryService    *memory.Service
	skillRegistry    *skills.Registry
	invoker          SkillInvoker
	logger           *slog.Logger
}

type Config struct {
	UserID           string
	ChatHistoryLimit int
	ToolTimeout      time.Duration
	LLMModel         string
}

func New(cfg Config, llmProvider llm.Provider, memoryService *memory.Service, skillRegistry *skills.Registry, invoker SkillInvoker, logger *slog.Logger) *Service {
	return &Service{
		userID:           cfg.UserID,
		chatHistoryLimit: cfg.ChatHistoryLimit,
		toolTimeout:      cfg.ToolTimeout,
		llmModel:         cfg.LLMModel,
		llmProvider:      llmProvider,
		memoryService:    memoryService,
		skillRegistry:    skillRegistry,
		invoker:          invoker,
		logger:           logger,
	}
}

func (s *Service) HandleChat(ctx context.Context, req domain.ChatRequest) (domain.ChatResponse, error) {
	userID := req.UserID
	if userID == "" {
		userID = s.userID
	}

	var soulID string
	state, ok := s.skillRegistry.GetState(req.TerminalID)
	if ok {
		soulID = state.SoulID
	}
	if soulID == "" {
		resolvedSoulID, err := s.memoryService.ResolveOrCreateSoul(ctx, userID, req.TerminalID, req.SoulHint)
		if err != nil {
			return domain.ChatResponse{}, err
		}
		soulID = resolvedSoulID
		s.skillRegistry.SetSoul(req.TerminalID, soulID)
	}

	if err := s.memoryService.PersistMessage(ctx, req.SessionID, userID, req.TerminalID, soulID, "user", "", "", req.Message); err != nil {
		return domain.ChatResponse{}, err
	}

	history, err := s.memoryService.RecentMessages(ctx, req.SessionID, s.chatHistoryLimit)
	if err != nil {
		return domain.ChatResponse{}, err
	}

	memoryContext, err := s.memoryService.BuildContext(ctx, userID, soulID, req.SessionID, req.TerminalID, req.Message)
	if err != nil {
		return domain.ChatResponse{}, err
	}

	terminalSkills := s.skillRegistry.GetSkills(req.TerminalID)
	tools := make([]domain.LLMTool, 0, len(terminalSkills))
	for _, sk := range terminalSkills {
		tools = append(tools, domain.LLMTool{
			Name:        sk.Name,
			Description: sk.Description,
			Schema:      sk.InputSchema,
		})
	}

	systemPrompt := buildSystemPrompt(memoryContext, terminalSkills)
	llmReq := domain.LLMRequest{
		Model:    s.llmModel,
		System:   systemPrompt,
		Tools:    tools,
		Messages: history,
	}
	firstResp, err := s.llmProvider.Complete(ctx, llmReq)
	if err != nil {
		return domain.ChatResponse{}, err
	}

	executedSkills := make([]string, 0, len(firstResp.ToolCalls))
	if len(firstResp.ToolCalls) > 0 {
		history = append(history, domain.Message{Role: "assistant", Content: firstResp.Content, ToolCalls: firstResp.ToolCalls})
		for _, tc := range firstResp.ToolCalls {
			invCtx, cancel := context.WithTimeout(ctx, s.toolTimeout)
			result, invokeErr := s.invoker.InvokeSkill(invCtx, req.TerminalID, tc.Name, tc.Arguments)
			cancel()

			toolOutput := ""
			if invokeErr != nil {
				toolOutput = fmt.Sprintf("技能执行失败: %v", invokeErr)
			} else {
				toolOutput = result.Output
			}

			history = append(history, domain.Message{
				Role:       "tool",
				Name:       tc.Name,
				ToolCallID: tc.ID,
				Content:    toolOutput,
			})
			executedSkills = append(executedSkills, tc.Name)

			if err := s.memoryService.PersistMessage(ctx, req.SessionID, userID, req.TerminalID, soulID, "tool", tc.Name, tc.ID, toolOutput); err != nil {
				s.logger.Warn("persist tool result failed", "error", err)
			}
		}

		secondResp, secondErr := s.llmProvider.Complete(ctx, domain.LLMRequest{
			Model:    s.llmModel,
			System:   systemPrompt,
			Tools:    tools,
			Messages: history,
		})
		if secondErr == nil {
			firstResp = secondResp
		} else {
			s.logger.Warn("second LLM pass failed, fallback to first response", "error", secondErr)
		}
	}

	if firstResp.Content == "" {
		firstResp.Content = "已处理请求。"
	}

	if err := s.memoryService.PersistMessage(ctx, req.SessionID, userID, req.TerminalID, soulID, "assistant", "", "", firstResp.Content); err != nil {
		return domain.ChatResponse{}, err
	}

	return domain.ChatResponse{
		SessionID:      req.SessionID,
		TerminalID:     req.TerminalID,
		SoulID:         soulID,
		Reply:          firstResp.Content,
		ExecutedSkills: executedSkills,
	}, nil
}

func buildSystemPrompt(memoryContext string, skills []domain.SkillDefinition) string {
	var sb strings.Builder
	sb.WriteString("你是单用户设备助手。必须严格基于终端注入技能执行动作。\n")
	sb.WriteString(memoryContext)
	sb.WriteString("\n")

	hasRed := false
	hasGreen := false
	for _, s := range skills {
		if s.Name == "light_red" {
			hasRed = true
		}
		if s.Name == "light_green" {
			hasGreen = true
		}
	}

	if hasRed && hasGreen {
		sb.WriteString("当用户说法正确或你认同时，优先调用 light_green；当你不认同或判断不正确时，调用 light_red。每轮最多调用一个灯技能。\n")
	}

	if len(skills) == 0 {
		sb.WriteString("当前终端无可用技能，只能文本回复。\n")
	}

	return sb.String()
}
