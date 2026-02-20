package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"sort"
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

type StatusPublisher interface {
	PublishStatus(ctx context.Context, terminalID, status, message, sessionID string) error
}

const (
	recallMemoryToolName  = "recall_memory"
	recallMemoryToolLimit = 5
)

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
	chatStart := time.Now()
	var firstLLMDur time.Duration
	var recallToolDur time.Duration
	var secondLLMDur time.Duration
	var terminalToolDur time.Duration

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

	keyboardTexts, pendingInputs := extractInputs(req.Inputs)
	latestUserText := strings.TrimSpace(strings.Join(keyboardTexts, "\n"))
	if latestUserText == "" {
		return domain.ChatResponse{}, fmt.Errorf("currently only input.type=keyboard_text with non-empty text is supported")
	}

	observationDigest := buildPendingInputDigest(pendingInputs)
	if err := s.memoryService.PersistObservation(ctx, req.SessionID, userID, req.TerminalID, soulID, observationDigest); err != nil {
		s.logger.Warn("persist observation failed", "error", err)
	}

	if err := s.memoryService.PersistMessage(ctx, req.SessionID, userID, req.TerminalID, soulID, "user", "", "", latestUserText); err != nil {
		return domain.ChatResponse{}, err
	}

	history, err := s.memoryService.RecentMessages(ctx, req.SessionID, s.chatHistoryLimit)
	if err != nil {
		return domain.ChatResponse{}, err
	}

	memoryContext, currentSummary, err := s.memoryService.BuildContext(ctx, soulID, req.SessionID, latestUserText, observationDigest)
	if err != nil {
		return domain.ChatResponse{}, err
	}

	terminalSkills := s.skillRegistry.GetSkills(req.TerminalID)
	terminalTools := make([]domain.LLMTool, 0, len(terminalSkills))
	terminalSkillSet := make(map[string]struct{}, len(terminalSkills))
	for _, sk := range terminalSkills {
		terminalTools = append(terminalTools, domain.LLMTool{
			Name:        sk.Name,
			Description: sk.Description,
			Schema:      sk.InputSchema,
		})
		terminalSkillSet[sk.Name] = struct{}{}
	}
	mem0Ready := s.memoryService.IsMem0RecallReady(ctx)
	firstPassTools := append([]domain.LLMTool{}, terminalTools...)
	if mem0Ready {
		firstPassTools = append(firstPassTools, domain.LLMTool{
			Name:        recallMemoryToolName,
			Description: "回顾历史记忆。当你需要从长期记忆中补全事实、偏好、过往约束时调用。参数: query(string,必填), top_k(integer,可选,默认5)。",
			Schema:      json.RawMessage(`{"type":"object","properties":{"query":{"type":"string"},"top_k":{"type":"integer","minimum":1,"maximum":10}},"required":["query"]}`),
		})
	}

	systemPrompt := buildSystemPrompt(memoryContext, terminalSkills)
	llmReq := domain.LLMRequest{
		Model:    s.llmModel,
		System:   systemPrompt,
		Tools:    firstPassTools,
		Messages: history,
	}
	firstLLMStart := time.Now()
	firstResp, err := s.llmProvider.Complete(ctx, llmReq)
	firstLLMDur = time.Since(firstLLMStart)
	if err != nil {
		return domain.ChatResponse{}, err
	}

	reply := firstResp.Content
	executedSkills := make([]string, 0, len(firstResp.ToolCalls))
	if len(firstResp.ToolCalls) > 0 {
		history = append(history, domain.Message{Role: "assistant", Content: firstResp.Content, ToolCalls: firstResp.ToolCalls})
	}

	recallMode := false
	for _, tc := range firstResp.ToolCalls {
		if tc.Name == recallMemoryToolName {
			recallMode = true
			break
		}
	}

	if recallMode {
		if publisher, ok := s.invoker.(StatusPublisher); ok {
			if err := publisher.PublishStatus(ctx, req.TerminalID, "mem0_searching", "正在回顾历史记忆，请稍候。", req.SessionID); err != nil {
				s.logger.Warn("publish status failed", "status", "mem0_searching", "error", err)
			}
		}

		recallFailed := false
		for _, tc := range firstResp.ToolCalls {
			if tc.Name != recallMemoryToolName {
				s.logger.Warn("skip non-recall skill from first pass in recall mode", "skill", tc.Name, "session_id", req.SessionID)
				continue
			}
			recallStart := time.Now()
			toolOutput, recallErr := s.executeRecallMemoryTool(ctx, tc.Arguments, latestUserText, userID, req.TerminalID, soulID)
			recallToolDur += time.Since(recallStart)
			if recallErr != nil {
				recallFailed = true
			}

			history = append(history, domain.Message{
				Role:       "tool",
				Name:       tc.Name,
				ToolCallID: tc.ID,
				Content:    toolOutput,
			})
			executedSkills = append(executedSkills, tc.Name)

			if err := s.memoryService.PersistMessage(ctx, req.SessionID, userID, req.TerminalID, soulID, "tool", tc.Name, tc.ID, toolOutput); err != nil {
				s.logger.Warn("persist recall tool result failed", "error", err)
			}
		}

		if publisher, ok := s.invoker.(StatusPublisher); ok {
			status := "mem0_search_done"
			msg := "历史记忆回顾完成。"
			if recallFailed {
				status = "mem0_search_failed"
				msg = "历史记忆回顾失败，已继续当前对话。"
			}
			if err := publisher.PublishStatus(ctx, req.TerminalID, status, msg, req.SessionID); err != nil {
				s.logger.Warn("publish status failed", "status", status, "error", err)
			}
		}

		secondLLMStart := time.Now()
		secondResp, secondErr := s.llmProvider.Complete(ctx, domain.LLMRequest{
			Model:    s.llmModel,
			System:   systemPrompt,
			Tools:    terminalTools,
			Messages: history,
		})
		secondLLMDur = time.Since(secondLLMStart)
		if secondErr != nil {
			s.logger.Warn("second llm pass failed in recall mode, fallback to first response", "error", secondErr)
		} else {
			reply = secondResp.Content
			for _, tc := range secondResp.ToolCalls {
				if _, ok := terminalSkillSet[tc.Name]; !ok {
					s.logger.Warn("skip unregistered skill from second pass", "skill", tc.Name, "session_id", req.SessionID)
					continue
				}
				toolStart := time.Now()
				toolOutput := s.executeTerminalSkill(ctx, req.TerminalID, tc.Name, tc.Arguments)
				terminalToolDur += time.Since(toolStart)
				history = append(history, domain.Message{
					Role:       "tool",
					Name:       tc.Name,
					ToolCallID: tc.ID,
					Content:    toolOutput,
				})
				executedSkills = append(executedSkills, tc.Name)

				if err := s.memoryService.PersistMessage(ctx, req.SessionID, userID, req.TerminalID, soulID, "tool", tc.Name, tc.ID, toolOutput); err != nil {
					s.logger.Warn("persist terminal tool result failed", "error", err)
				}
			}
		}
	} else {
		for _, tc := range firstResp.ToolCalls {
			if _, ok := terminalSkillSet[tc.Name]; !ok {
				s.logger.Warn("skip unregistered skill from first pass", "skill", tc.Name, "session_id", req.SessionID)
				continue
			}
			toolStart := time.Now()
			toolOutput := s.executeTerminalSkill(ctx, req.TerminalID, tc.Name, tc.Arguments)
			terminalToolDur += time.Since(toolStart)
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
	}

	if reply == "" {
		reply = "已处理请求。"
	}

	if err := s.memoryService.PersistMessage(ctx, req.SessionID, userID, req.TerminalID, soulID, "assistant", "", "", reply); err != nil {
		return domain.ChatResponse{}, err
	}

	summaryOut := currentSummary
	if compressed, changed, compErr := s.memoryService.MaybeCompressSession(ctx, req.SessionID, userID, req.TerminalID, soulID, false); compErr != nil {
		s.logger.Warn("session compaction failed", "session_id", req.SessionID, "error", compErr)
	} else if changed || strings.TrimSpace(compressed) != "" {
		summaryOut = compressed
	}
	if strings.TrimSpace(summaryOut) == "" {
		if latest, latestErr := s.memoryService.GetSessionSummary(ctx, req.SessionID); latestErr == nil {
			summaryOut = latest
		}
	}

	totalDur := time.Since(chatStart)
	s.logger.Info("chat timing",
		"session_id", req.SessionID,
		"terminal_id", req.TerminalID,
		"mem0_ready", mem0Ready,
		"recall_mode", recallMode,
		"first_llm_ms", firstLLMDur.Milliseconds(),
		"recall_tool_ms", recallToolDur.Milliseconds(),
		"second_llm_ms", secondLLMDur.Milliseconds(),
		"terminal_tool_ms", terminalToolDur.Milliseconds(),
		"total_ms", totalDur.Milliseconds(),
	)

	return domain.ChatResponse{
		SessionID:      req.SessionID,
		TerminalID:     req.TerminalID,
		SoulID:         soulID,
		Reply:          reply,
		ExecutedSkills: executedSkills,
		ContextSummary: strings.TrimSpace(summaryOut),
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
	sb.WriteString("默认流程仅执行一次LLM：直接选择终端技能并结束。\n")
	sb.WriteString("只有在确实需要历史记忆时才调用 recall_memory。\n")
	sb.WriteString("若调用 recall_memory，第一阶段不要同时调用终端技能；待记忆结果返回后，在第二阶段再选择终端技能。\n")

	if len(skills) == 0 {
		sb.WriteString("当前终端无可用技能，只能文本回复。\n")
	}

	return sb.String()
}

type pendingInput struct {
	InputID string
	Type    string
	Source  string
}

func extractInputs(inputs []domain.ChatInput) ([]string, []pendingInput) {
	keyboardTexts := make([]string, 0, len(inputs))
	pending := make([]pendingInput, 0, len(inputs))

	for _, in := range inputs {
		inputType := strings.ToLower(strings.TrimSpace(in.Type))
		switch inputType {
		case "keyboard_text":
			if text := strings.TrimSpace(in.Text); text != "" {
				keyboardTexts = append(keyboardTexts, text)
			}
		default:
			// TODO(v2): support non-keyboard input types (audio/image/video/sensor_state/...).
			pending = append(pending, pendingInput{
				InputID: strings.TrimSpace(in.InputID),
				Type:    inputType,
				Source:  strings.TrimSpace(in.Source),
			})
		}
	}
	return keyboardTexts, pending
}

func buildPendingInputDigest(pending []pendingInput) string {
	if len(pending) == 0 {
		return ""
	}

	uniqueTypes := map[string]struct{}{}
	var lines []string
	for _, p := range pending {
		if p.Type != "" {
			uniqueTypes[p.Type] = struct{}{}
		}
		if p.Type == "" {
			p.Type = "unknown"
		}
		if p.Source == "" {
			p.Source = "unknown"
		}
		if p.InputID == "" {
			lines = append(lines, fmt.Sprintf("[input-not-implemented] type=%s source=%s", p.Type, p.Source))
		} else {
			lines = append(lines, fmt.Sprintf("[input-not-implemented] type=%s input_id=%s source=%s", p.Type, p.InputID, p.Source))
		}
	}

	typeList := make([]string, 0, len(uniqueTypes))
	for t := range uniqueTypes {
		typeList = append(typeList, t)
	}
	sort.Strings(typeList)
	lines = append(lines, "未实现输入类型: "+strings.Join(typeList, ", "))
	return strings.TrimSpace(strings.Join(lines, "\n"))
}

func (s *Service) executeTerminalSkill(ctx context.Context, terminalID, skill string, args json.RawMessage) string {
	invCtx, cancel := context.WithTimeout(ctx, s.toolTimeout)
	defer cancel()

	result, invokeErr := s.invoker.InvokeSkill(invCtx, terminalID, skill, args)
	if invokeErr != nil {
		return fmt.Sprintf("技能执行失败: %v", invokeErr)
	}
	return result.Output
}

func (s *Service) executeRecallMemoryTool(ctx context.Context, args json.RawMessage, latestUserText, userID, terminalID, soulID string) (string, error) {
	query, topK, parseErr := parseRecallMemoryArgs(args, latestUserText)
	if parseErr != nil {
		return fmt.Sprintf("记忆查询参数无效: %v", parseErr), parseErr
	}
	memories, err := s.memoryService.RecallFromMem0(ctx, query, memory.ExternalMemoryFilter{
		UserID:     userID,
		SoulID:     soulID,
		TerminalID: terminalID,
	}, topK)
	if err != nil {
		return fmt.Sprintf("记忆查询失败: %v", err), err
	}
	if len(memories) == 0 {
		return "记忆查询结果：未找到相关历史记忆。", nil
	}

	var sb strings.Builder
	sb.WriteString("记忆查询结果:\n")
	for i, item := range memories {
		sb.WriteString(fmt.Sprintf("%d. %s\n", i+1, strings.TrimSpace(item)))
		if i+1 >= topK {
			break
		}
	}
	return strings.TrimSpace(sb.String()), nil
}

func parseRecallMemoryArgs(raw json.RawMessage, fallbackQuery string) (string, int, error) {
	topK := recallMemoryToolLimit
	query := strings.TrimSpace(fallbackQuery)
	if len(raw) == 0 {
		if query == "" {
			return "", topK, fmt.Errorf("query is required")
		}
		return query, topK, nil
	}

	var payload struct {
		Query string `json:"query"`
		TopK  int    `json:"top_k"`
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return "", topK, err
	}
	if strings.TrimSpace(payload.Query) != "" {
		query = strings.TrimSpace(payload.Query)
	}
	if payload.TopK > 0 && payload.TopK <= 10 {
		topK = payload.TopK
	}
	if query == "" {
		return "", topK, fmt.Errorf("query is required")
	}
	return query, topK, nil
}
