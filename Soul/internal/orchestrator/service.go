package orchestrator

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"soul/internal/domain"
	"soul/internal/llm"
	"soul/internal/memory"
	"soul/internal/persona"
	"soul/internal/skills"
)

type SkillInvoker interface {
	InvokeSkill(ctx context.Context, terminalID, skill string, args json.RawMessage) (domain.InvokeResult, error)
}

type StatusPublisher interface {
	PublishStatus(ctx context.Context, terminalID, status, message, sessionID string) error
}

type EmotionAnalyzer interface {
	Analyze(ctx context.Context, text string) (domain.EmotionSignal, error)
}

type IntentFilter interface {
	Filter(ctx context.Context, req domain.IntentFilterRequest) (domain.IntentFilterResponse, error)
}

type EmotionPublisher interface {
	PublishEmotionUpdate(ctx context.Context, terminalID string, payload domain.EmotionUpdatePayload) error
}

type IntentActionPublisher interface {
	PublishIntentAction(ctx context.Context, terminalID string, payload domain.IntentActionPayload) error
}

const (
	recallMemoryToolName  = "recall_memory"
	recallMemoryToolLimit = 5
	personaBaseExecProb   = 0.95
)

var mbtiPattern = regexp.MustCompile(`(?i)(?:^|[^A-Za-z])([EI][SN][TF][JP])(?:$|[^A-Za-z])`)

type Service struct {
	userID           string
	chatHistoryLimit int
	toolTimeout      time.Duration
	llmModel         string
	llmProvider      llm.Provider
	memoryService    *memory.Service
	skillRegistry    *skills.Registry
	invoker          SkillInvoker
	emotionAnalyzer  EmotionAnalyzer
	intentFilter     IntentFilter
	personaEngine    *persona.Engine
	emotionMu        sync.Mutex
	logger           *slog.Logger
}

type Config struct {
	UserID           string
	ChatHistoryLimit int
	ToolTimeout      time.Duration
	LLMModel         string
}

type llmEmotionPromptSnapshot struct {
	At              time.Time
	UserEmotion     domain.EmotionSignal
	SoulEmotion     domain.SoulEmotionState
	ExecMode        string
	ExecProbability float64
	Keywords        []string
}

func New(cfg Config, llmProvider llm.Provider, memoryService *memory.Service, skillRegistry *skills.Registry, invoker SkillInvoker, emotionAnalyzer EmotionAnalyzer, intentFilter IntentFilter, personaEngine *persona.Engine, logger *slog.Logger) *Service {
	if personaEngine == nil {
		personaEngine = persona.NewEngine(persona.DefaultConfig())
	}
	return &Service{
		userID:           cfg.UserID,
		chatHistoryLimit: cfg.ChatHistoryLimit,
		toolTimeout:      cfg.ToolTimeout,
		llmModel:         cfg.LLMModel,
		llmProvider:      llmProvider,
		memoryService:    memoryService,
		skillRegistry:    skillRegistry,
		invoker:          invoker,
		emotionAnalyzer:  emotionAnalyzer,
		intentFilter:     intentFilter,
		personaEngine:    personaEngine,
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
	if strings.TrimSpace(req.SoulID) != "" {
		soulID = strings.TrimSpace(req.SoulID)
		if err := s.memoryService.BindTerminalSoul(ctx, userID, req.TerminalID, soulID); err != nil {
			return domain.ChatResponse{}, err
		}
		s.skillRegistry.SetSoul(req.TerminalID, soulID)
	} else {
		state, ok := s.skillRegistry.GetState(req.TerminalID)
		if ok {
			soulID = strings.TrimSpace(state.SoulID)
		}
		if soulID == "" {
			resolvedSoulID, err := s.memoryService.ResolveSoul(ctx, userID, req.TerminalID, req.SoulHint)
			if err != nil {
				return domain.ChatResponse{}, err
			}
			soulID = resolvedSoulID
			s.skillRegistry.SetSoul(req.TerminalID, soulID)
		}
	}

	keyboardTexts, pendingInputs := extractInputs(req.Inputs)
	latestUserText := strings.TrimSpace(strings.Join(keyboardTexts, "\n"))
	if latestUserText == "" {
		return domain.ChatResponse{}, fmt.Errorf("currently only input.type=keyboard_text|speech_text with non-empty text is supported")
	}

	execProbability := 1.0
	execMode := "auto_execute"
	intentDecision := ""
	userEmotion := domain.EmotionSignal{Emotion: "neutral", P: 0.0, A: 0.05, D: 0.0, Intensity: 0.0, Confidence: 0.0}
	observationDigest := buildPendingInputDigest(pendingInputs)
	if err := s.memoryService.PersistObservation(ctx, req.SessionID, userID, req.TerminalID, soulID, observationDigest); err != nil {
		s.logger.Warn("persist observation failed", "error", err)
	}
	if err := s.memoryService.PersistMessage(ctx, req.SessionID, userID, req.TerminalID, soulID, "user", "", "", latestUserText); err != nil {
		return domain.ChatResponse{}, err
	}

	soulProfile, err := s.memoryService.GetSoulProfileByID(ctx, soulID)
	if err != nil {
		return domain.ChatResponse{}, err
	}
	if s.emotionAnalyzer != nil {
		emotionOut, emoErr := s.emotionAnalyzer.Analyze(ctx, latestUserText)
		if emoErr != nil {
			s.logger.Warn("emotion analyze failed", "session_id", req.SessionID, "terminal_id", req.TerminalID, "error", emoErr)
		} else {
			userEmotion = emotionOut
		}
	}
	if s.personaEngine != nil {
		s.emotionMu.Lock()
		if latestSoulProfile, latestErr := s.memoryService.GetSoulProfileByID(ctx, soulID); latestErr != nil {
			s.logger.Warn("refresh soul profile before persona update failed", "soul_id", soulID, "error", latestErr)
		} else {
			soulProfile = latestSoulProfile
		}
		result := s.personaEngine.Update(
			soulProfile.PersonalityVector,
			soulProfile.EmotionState,
			persona.UpdateInput{
				Now:          time.Now().UTC(),
				UserEmotion:  userEmotion,
				HasUserInput: true,
			},
			personaBaseExecProb,
		)
		execProbability = result.ExecProbability
		execMode = result.ExecMode
		soulProfile.EmotionState = result.State
		if err := s.memoryService.UpdateSoulEmotionState(ctx, soulID, result.State); err != nil {
			s.logger.Warn("update soul emotion state failed", "soul_id", soulID, "error", err)
		}
		s.emotionMu.Unlock()
		if publisher, ok := s.invoker.(EmotionPublisher); ok {
			payload := domain.EmotionUpdatePayload{
				SessionID:       req.SessionID,
				TerminalID:      req.TerminalID,
				SoulID:          soulID,
				UserEmotion:     userEmotion,
				SoulEmotion:     result.State,
				ExecProbability: execProbability,
				ExecMode:        execMode,
				TS:              time.Now().UTC().Format(time.RFC3339Nano),
			}
			if err := publisher.PublishEmotionUpdate(ctx, req.TerminalID, payload); err != nil {
				s.logger.Warn("publish emotion update failed", "terminal_id", req.TerminalID, "error", err)
			}
		}
	}

	intentResp, intentMatched := s.tryIntentAction(ctx, req, soulID, latestUserText, execProbability, execMode)
	if strings.TrimSpace(intentResp.Decision.Action) != "" {
		intentDecision = intentResp.Decision.Action
	}
	if intentMatched {
		reply := intentReplyByMode(intentResp.Decision.Action, execMode)
		executedSkills := []string(nil)
		if strings.TrimSpace(execMode) == "auto_execute" {
			executedSkills = extractExecutedSkillsFromIntents(intentResp)
		}
		if err := s.memoryService.PersistMessage(ctx, req.SessionID, userID, req.TerminalID, soulID, "assistant", "", "", reply); err != nil {
			return domain.ChatResponse{}, err
		}
		return domain.ChatResponse{
			SessionID:       req.SessionID,
			TerminalID:      req.TerminalID,
			SoulID:          soulID,
			Reply:           reply,
			ExecutedSkills:  executedSkills,
			IntentDecision:  intentDecision,
			ExecMode:        execMode,
			ExecProbability: execProbability,
		}, nil
	}

	history, err := s.memoryService.RecentMessages(ctx, req.SessionID, s.chatHistoryLimit)
	if err != nil {
		return domain.ChatResponse{}, err
	}

	memoryContext, currentSummary, err := s.memoryService.BuildContext(ctx, soulID, req.SessionID, observationDigest)
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

	firstLLMNow := time.Now().UTC()
	execProbability, execMode = s.evaluateExecGateAt(firstLLMNow, soulProfile, execProbability, execMode)
	firstEmotionSnapshot := buildLLMEmotionPromptSnapshot(firstLLMNow, userEmotion, soulProfile.EmotionState, execMode, execProbability)
	relationGuidance := buildPersonaRelationGuidance(latestUserText, soulProfile)
	systemPrompt := buildSystemPrompt(memoryContext, terminalSkills, mem0Ready, firstEmotionSnapshot, relationGuidance)
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

		if latestSoulProfile, latestErr := s.memoryService.GetSoulProfileByID(ctx, soulID); latestErr != nil {
			s.logger.Warn("refresh soul profile before second llm failed", "soul_id", soulID, "error", latestErr)
		} else {
			soulProfile = latestSoulProfile
		}
		secondLLMNow := time.Now().UTC()
		execProbability, execMode = s.evaluateExecGateAt(secondLLMNow, soulProfile, execProbability, execMode)
		secondEmotionSnapshot := buildLLMEmotionPromptSnapshot(secondLLMNow, userEmotion, soulProfile.EmotionState, execMode, execProbability)
		secondRelationGuidance := buildPersonaRelationGuidance(latestUserText, soulProfile)
		secondSystemPrompt := buildSystemPrompt(memoryContext, terminalSkills, false, secondEmotionSnapshot, secondRelationGuidance)

		secondLLMStart := time.Now()
		secondResp, secondErr := s.llmProvider.Complete(ctx, domain.LLMRequest{
			Model:    s.llmModel,
			System:   secondSystemPrompt,
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
				toolOutput := s.executeTerminalSkillWithGate(ctx, req.TerminalID, tc.Name, tc.Arguments, execMode, execProbability)
				terminalToolDur += time.Since(toolStart)
				history = append(history, domain.Message{
					Role:       "tool",
					Name:       tc.Name,
					ToolCallID: tc.ID,
					Content:    toolOutput,
				})
				if execMode == "auto_execute" {
					executedSkills = append(executedSkills, tc.Name)
				}

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
			toolOutput := s.executeTerminalSkillWithGate(ctx, req.TerminalID, tc.Name, tc.Arguments, execMode, execProbability)
			terminalToolDur += time.Since(toolStart)
			history = append(history, domain.Message{
				Role:       "tool",
				Name:       tc.Name,
				ToolCallID: tc.ID,
				Content:    toolOutput,
			})
			if execMode == "auto_execute" {
				executedSkills = append(executedSkills, tc.Name)
			}

			if err := s.memoryService.PersistMessage(ctx, req.SessionID, userID, req.TerminalID, soulID, "tool", tc.Name, tc.ID, toolOutput); err != nil {
				s.logger.Warn("persist tool result failed", "error", err)
			}
		}
	}

	reply, silentReply := normalizeAssistantReply(reply)
	if reply == "" && !silentReply {
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
		SessionID:       req.SessionID,
		TerminalID:      req.TerminalID,
		SoulID:          soulID,
		Reply:           reply,
		ExecutedSkills:  executedSkills,
		ContextSummary:  strings.TrimSpace(summaryOut),
		IntentDecision:  intentDecision,
		ExecMode:        execMode,
		ExecProbability: execProbability,
	}, nil
}

func buildSystemPrompt(memoryContext string, skills []domain.SkillDefinition, recallEnabled bool, emotion llmEmotionPromptSnapshot, relationGuidance string) string {
	var sb strings.Builder
	sb.WriteString("你是单用户桌面机器人编排助手。你只能使用本轮请求提供的 tools 执行动作，不要假设任何未提供工具。\n\n")
	sb.WriteString("上下文信息：\n")
	sb.WriteString(memoryContext)
	sb.WriteString("\n\n情绪门控快照（当前 LLM 调用时刻）：\n")
	at := emotion.At.UTC()
	if at.IsZero() {
		at = time.Now().UTC()
	}
	sb.WriteString(fmt.Sprintf("- snapshot_at: %s\n", at.Format(time.RFC3339Nano)))
	userEmotionLabel := strings.TrimSpace(emotion.UserEmotion.Emotion)
	if userEmotionLabel == "" {
		userEmotionLabel = "neutral"
	}
	sb.WriteString(fmt.Sprintf("- user_emotion: %s (intensity=%.3f)\n", userEmotionLabel, emotion.UserEmotion.Intensity))
	sb.WriteString(fmt.Sprintf("- soul_pad: p=%.3f a=%.3f d=%.3f\n", emotion.SoulEmotion.P, emotion.SoulEmotion.A, emotion.SoulEmotion.D))
	sb.WriteString(fmt.Sprintf("- execution_gate: mode=%s probability=%.3f\n", strings.TrimSpace(emotion.ExecMode), emotion.ExecProbability))
	if len(emotion.Keywords) > 0 {
		sb.WriteString("- emotion_keywords: " + strings.Join(emotion.Keywords, ", ") + "\n")
	}
	sb.WriteString("\n人格关系快照（用于回复风格，不改变工具集合）：\n")
	if strings.TrimSpace(relationGuidance) != "" {
		sb.WriteString(relationGuidance)
		if !strings.HasSuffix(relationGuidance, "\n") {
			sb.WriteString("\n")
		}
	} else {
		sb.WriteString("- target_persona: unknown\n")
		sb.WriteString("- relation_strategy: 先用中性、低侵入、可撤回表达，优先确认对方接受度。\n")
	}
	sb.WriteString("\n\n决策规则：\n")
	sb.WriteString("1) 先理解用户意图，再查看可用 tools。\n")
	sb.WriteString("2) 若多个 tools 与意图匹配，可在同一轮调用多个 tools（并行或顺序）。\n")
	sb.WriteString("3) 若 tools 语义冲突（互斥动作），只调用最符合当前意图的一组。\n")
	sb.WriteString("4) 若没有合适 tool，可直接文本回复。\n")
	sb.WriteString("5) tool 参数必须严格符合对应 schema，不要编造字段。\n")
	if recallEnabled {
		sb.WriteString("6) 当前提供 recall_memory：仅在确实需要长期记忆时调用。调用后先回顾记忆，再选择终端技能。\n")
	} else {
		sb.WriteString("6) 当前未提供 recall_memory，不要假设可用。\n")
	}
	sb.WriteString("7) 参考 emotion_keywords 调整回复语气与工具选择，但不要编造不存在的技能。\n")
	switch strings.TrimSpace(emotion.ExecMode) {
	case "blocked":
		sb.WriteString("8) 当前处于 blocked：可给出解释和安抚，不要声称动作已执行。\n")
	default:
		sb.WriteString("8) 当前处于 auto_execute：按意图正常调用工具并给出明确结果。\n")
	}
	sb.WriteString("9) 除技能执行外，结合人格关系快照调整措辞、长度、主动性与边界。\n")
	sb.WriteString("10) 若判断“当前不回复更合适”，仅输出 `<NO_REPLY>`（不要附加任何文字）。\n")
	sb.WriteString("11) 其余情况保持简洁中文回复。\n")

	if len(skills) == 0 {
		sb.WriteString("当前终端无可用技能，可直接文本回复。\n")
	}

	return sb.String()
}

type targetPersonaHint struct {
	Known  bool
	Source string
	Label  string
	Vector domain.PersonalityVector
	Cues   []string
}

func buildPersonaRelationGuidance(latestUserText string, soulProfile domain.SoulProfile) string {
	soulMBTI := strings.ToUpper(strings.TrimSpace(soulProfile.MBTIType))
	if soulMBTI == "" {
		soulMBTI = "UNKNOWN"
	}
	soul := domain.PersonalityVector{
		Empathy:        clamp01(soulProfile.PersonalityVector.Empathy + soulProfile.EmotionState.Drift.Empathy),
		Sensitivity:    clamp01(soulProfile.PersonalityVector.Sensitivity + soulProfile.EmotionState.Drift.Sensitivity),
		Stability:      clamp01(soulProfile.PersonalityVector.Stability + soulProfile.EmotionState.Drift.Stability),
		Expressiveness: clamp01(soulProfile.PersonalityVector.Expressiveness + soulProfile.EmotionState.Drift.Expressiveness),
		Dominance:      clamp01(soulProfile.PersonalityVector.Dominance + soulProfile.EmotionState.Drift.Dominance),
	}

	target := inferTargetPersonaHint(latestUserText)
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("- soul_mbti: %s\n", soulMBTI))
	sb.WriteString(fmt.Sprintf("- soul_traits: empathy=%.2f sensitivity=%.2f stability=%.2f expressiveness=%.2f dominance=%.2f\n", soul.Empathy, soul.Sensitivity, soul.Stability, soul.Expressiveness, soul.Dominance))
	if !target.Known {
		sb.WriteString("- target_persona: unknown\n")
		sb.WriteString("- relation_assessment: unknown\n")
		sb.WriteString("- relation_strategy: 先试探目标人物风格，默认中性、简短、低主导，避免一次性给高压建议。\n")
		return sb.String()
	}

	delta := (math.Abs(soul.Empathy-target.Vector.Empathy) +
		math.Abs(soul.Sensitivity-target.Vector.Sensitivity) +
		math.Abs(soul.Stability-target.Vector.Stability) +
		math.Abs(soul.Expressiveness-target.Vector.Expressiveness) +
		math.Abs(soul.Dominance-target.Vector.Dominance)) / 5.0
	resonance := clamp01(1 - delta)
	relationship := "高张力"
	if resonance >= 0.72 {
		relationship = "同频"
	} else if resonance >= 0.56 {
		relationship = "可协同"
	}
	dominanceGap := math.Abs(soul.Dominance - target.Vector.Dominance)
	sensitivityGap := math.Abs(soul.Sensitivity - target.Vector.Sensitivity)

	sb.WriteString(fmt.Sprintf("- target_persona: %s (%s)\n", target.Label, target.Source))
	sb.WriteString(fmt.Sprintf("- target_traits: empathy=%.2f sensitivity=%.2f stability=%.2f expressiveness=%.2f dominance=%.2f\n", target.Vector.Empathy, target.Vector.Sensitivity, target.Vector.Stability, target.Vector.Expressiveness, target.Vector.Dominance))
	if len(target.Cues) > 0 {
		sb.WriteString("- target_cues: " + strings.Join(target.Cues, ", ") + "\n")
	}
	sb.WriteString(fmt.Sprintf("- relation_assessment: %s (resonance=%.2f dominance_gap=%.2f sensitivity_gap=%.2f)\n", relationship, resonance, dominanceGap, sensitivityGap))

	switch relationship {
	case "同频":
		sb.WriteString("- relation_strategy: 可直接给结论与动作建议，保持礼貌并避免重复确认。\n")
	case "可协同":
		sb.WriteString("- relation_strategy: 先给简短结论，再补一条可选方案；语气平稳、不过度主导。\n")
	default:
		sb.WriteString("- relation_strategy: 先降冲突（低主导、短句、可撤回建议），必要时先确认再执行。\n")
	}
	return sb.String()
}

func inferTargetPersonaHint(text string) targetPersonaHint {
	raw := strings.TrimSpace(text)
	if raw == "" {
		return targetPersonaHint{}
	}
	if match := mbtiPattern.FindStringSubmatch(raw); len(match) == 2 {
		mbti := strings.ToUpper(strings.TrimSpace(match[1]))
		if vector, err := persona.VectorFromMBTI(mbti); err == nil {
			return targetPersonaHint{
				Known:  true,
				Source: "mbti_mention",
				Label:  mbti,
				Vector: vector,
			}
		}
	}

	v := domain.PersonalityVector{
		Empathy:        0.5,
		Sensitivity:    0.5,
		Stability:      0.5,
		Expressiveness: 0.5,
		Dominance:      0.5,
	}
	cues := make([]string, 0, 6)
	score := 0
	apply := func(hit bool, cue string, delta domain.PersonalityVector) {
		if !hit {
			return
		}
		score++
		cues = append(cues, cue)
		v.Empathy = clamp01(v.Empathy + delta.Empathy)
		v.Sensitivity = clamp01(v.Sensitivity + delta.Sensitivity)
		v.Stability = clamp01(v.Stability + delta.Stability)
		v.Expressiveness = clamp01(v.Expressiveness + delta.Expressiveness)
		v.Dominance = clamp01(v.Dominance + delta.Dominance)
	}

	apply(containsAny(raw, "老板", "领导", "上级", "甲方", "强势", "命令"), "dominant_target", domain.PersonalityVector{Empathy: -0.05, Sensitivity: -0.02, Stability: 0.10, Dominance: 0.28})
	apply(containsAny(raw, "孩子", "宝宝", "家人", "朋友", "温和"), "gentle_target", domain.PersonalityVector{Empathy: 0.12, Sensitivity: 0.08, Stability: 0.06, Dominance: -0.18})
	apply(containsAny(raw, "外向", "健谈", "热情", "直爽"), "expressive_target", domain.PersonalityVector{Expressiveness: 0.30})
	apply(containsAny(raw, "内向", "慢热", "寡言", "不爱说话"), "introvert_target", domain.PersonalityVector{Expressiveness: -0.28})
	apply(containsAny(raw, "理性", "冷静", "克制"), "rational_target", domain.PersonalityVector{Empathy: -0.10, Sensitivity: -0.08, Stability: 0.20})
	apply(containsAny(raw, "感性", "敏感", "焦虑", "易怒", "脆弱"), "sensitive_target", domain.PersonalityVector{Empathy: 0.10, Sensitivity: 0.22, Stability: -0.18})
	apply(containsAny(raw, "体贴", "温柔", "共情"), "high_empathy_target", domain.PersonalityVector{Empathy: 0.22, Dominance: -0.10})

	if score == 0 {
		return targetPersonaHint{}
	}
	return targetPersonaHint{
		Known:  true,
		Source: "text_heuristic",
		Label:  "heuristic_target",
		Vector: v,
		Cues:   cues,
	}
}

func normalizeAssistantReply(reply string) (string, bool) {
	trimmed := strings.TrimSpace(reply)
	if trimmed == "" {
		return "", false
	}
	marker := strings.ToUpper(strings.Trim(trimmed, "`"))
	switch marker {
	case "<NO_REPLY>", "NO_REPLY", "[NO_REPLY]":
		return "", true
	default:
		return trimmed, false
	}
}

func containsAny(text string, keywords ...string) bool {
	if strings.TrimSpace(text) == "" {
		return false
	}
	for _, kw := range keywords {
		if kw != "" && strings.Contains(text, kw) {
			return true
		}
	}
	return false
}

func (s *Service) evaluateExecGateAt(now time.Time, soulProfile domain.SoulProfile, fallbackProb float64, fallbackMode string) (float64, string) {
	if s.personaEngine == nil {
		return clamp01(fallbackProb), strings.TrimSpace(fallbackMode)
	}
	effective := s.personaEngine.EffectiveVector(soulProfile.PersonalityVector, soulProfile.EmotionState.Drift)
	prob, mode := s.personaEngine.ExecutionProbability(effective, soulProfile.EmotionState, personaBaseExecProb, now.UTC())
	return prob, mode
}

func buildLLMEmotionPromptSnapshot(now time.Time, user domain.EmotionSignal, soul domain.SoulEmotionState, execMode string, execProbability float64) llmEmotionPromptSnapshot {
	snapshot := llmEmotionPromptSnapshot{
		At:              now.UTC(),
		UserEmotion:     user,
		SoulEmotion:     soul,
		ExecMode:        strings.TrimSpace(execMode),
		ExecProbability: clamp01(execProbability),
	}
	snapshot.Keywords = buildEmotionKeywords(snapshot.UserEmotion, snapshot.SoulEmotion, snapshot.ExecMode, snapshot.ExecProbability)
	return snapshot
}

func buildEmotionKeywords(user domain.EmotionSignal, soul domain.SoulEmotionState, execMode string, execProbability float64) []string {
	keywords := make([]string, 0, 16)
	userEmotion := strings.ToLower(strings.TrimSpace(user.Emotion))
	if userEmotion == "" {
		userEmotion = "neutral"
	}
	keywords = append(keywords, "user_"+userEmotion)
	keywords = append(keywords, "user_intensity_"+intensityBucket(user.Intensity))

	keywords = append(keywords, padLevelKeyword(soul.P, "soul_valence_negative", "soul_valence_neutral", "soul_valence_positive"))
	keywords = append(keywords, padLevelKeyword(soul.A, "soul_arousal_low", "soul_arousal_neutral", "soul_arousal_high"))
	keywords = append(keywords, padLevelKeyword(soul.D, "soul_dominance_low", "soul_dominance_neutral", "soul_dominance_high"))

	switch strings.TrimSpace(execMode) {
	case "auto_execute":
		keywords = append(keywords, "gate_auto_execute")
	default:
		keywords = append(keywords, "gate_blocked")
	}
	keywords = append(keywords, "gate_prob_"+probabilityBucket(execProbability))

	if soul.ShockLoad >= 0.45 {
		keywords = append(keywords, "soul_shock_high")
	}
	if soul.ExtremeMemory >= 0.60 {
		keywords = append(keywords, "soul_extreme_memory_high")
	}

	switch userEmotion {
	case "sadness", "disappointment", "fear", "anxiety":
		keywords = append(keywords, "strategy_supportive_tone")
	case "anger", "disgust", "frustration":
		keywords = append(keywords, "strategy_deescalate_tone")
	case "joy", "gratitude", "relief", "excitement", "surprise":
		keywords = append(keywords, "strategy_positive_tone")
	default:
		keywords = append(keywords, "strategy_neutral_clear_tone")
	}

	return uniqueStrings(keywords)
}

func intensityBucket(v float64) string {
	switch {
	case v >= 0.67:
		return "high"
	case v >= 0.34:
		return "mid"
	default:
		return "low"
	}
}

func probabilityBucket(v float64) string {
	switch {
	case v >= 0.75:
		return "high"
	case v >= 0.35:
		return "mid"
	default:
		return "low"
	}
}

func clamp01(v float64) float64 {
	switch {
	case v < 0:
		return 0
	case v > 1:
		return 1
	default:
		return v
	}
}

func padLevelKeyword(v float64, negative, neutral, positive string) string {
	switch {
	case v <= -0.35:
		return negative
	case v >= 0.35:
		return positive
	default:
		return neutral
	}
}

func uniqueStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	out := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, item := range values {
		key := strings.TrimSpace(item)
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		out = append(out, key)
	}
	return out
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
		case "speech_text":
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

func (s *Service) tryIntentAction(ctx context.Context, req domain.ChatRequest, soulID, latestUserText string, execProbability float64, execMode string) (domain.IntentFilterResponse, bool) {
	if s.intentFilter == nil {
		return domain.IntentFilterResponse{}, false
	}
	catalog := s.skillRegistry.GetIntentCatalog(req.TerminalID)
	if len(catalog) == 0 {
		return domain.IntentFilterResponse{}, false
	}

	filterResp, err := s.intentFilter.Filter(ctx, domain.IntentFilterRequest{
		Command:       latestUserText,
		IntentCatalog: catalog,
		Options: domain.IntentFilterOptions{
			AllowMultiIntent:          true,
			MaxIntents:                8,
			MaxIntentsPerSegment:      2,
			MinConfidence:             0.35,
			EnableTimeParser:          true,
			ReturnDebugCandidates:     false,
			ReturnDebugEntities:       false,
			EmitSystemIntentWhenEmpty: true,
		},
	})
	if err != nil {
		s.logger.Warn("intent filter failed", "session_id", req.SessionID, "terminal_id", req.TerminalID, "error", err)
		return domain.IntentFilterResponse{}, false
	}

	if strings.TrimSpace(filterResp.Decision.Action) != "execute_intents" {
		return filterResp, false
	}

	items := make([]domain.IntentActionItem, 0, len(filterResp.Intents))
	for _, in := range filterResp.Intents {
		if strings.TrimSpace(in.Status) != "ready" {
			continue
		}
		items = append(items, domain.IntentActionItem{
			IntentID:   in.IntentID,
			IntentName: in.IntentName,
			Confidence: in.Confidence,
			Parameters: in.Parameters,
			Normalized: in.Normalized,
		})
	}
	if len(items) == 0 {
		return filterResp, false
	}
	if execMode != "auto_execute" {
		return filterResp, true
	}

	pub, ok := s.invoker.(IntentActionPublisher)
	if !ok {
		s.logger.Warn("intent action publisher is unavailable", "terminal_id", req.TerminalID)
		return filterResp, false
	}

	requestID := strings.TrimSpace(filterResp.RequestID)
	if requestID == "" {
		requestID = "ia-" + uuid.NewString()
	}
	payload := domain.IntentActionPayload{
		RequestID:       requestID,
		SessionID:       req.SessionID,
		TerminalID:      req.TerminalID,
		SoulID:          soulID,
		Intents:         items,
		ExecProbability: execProbability,
		TS:              time.Now().UTC().Format(time.RFC3339Nano),
	}
	if err := pub.PublishIntentAction(ctx, req.TerminalID, payload); err != nil {
		s.logger.Warn("publish intent action failed", "terminal_id", req.TerminalID, "error", err)
		return filterResp, false
	}
	return filterResp, true
}

func intentReplyByMode(intentDecision, execMode string) string {
	if strings.TrimSpace(intentDecision) != "execute_intents" {
		return "已完成意图分析。"
	}
	switch strings.TrimSpace(execMode) {
	case "auto_execute":
		return "已命中意图并通过 MQTT 下发到终端执行。"
	default:
		return "已命中意图，但当前情绪波动较高，已暂缓执行。"
	}
}

func extractExecutedSkillsFromIntents(resp domain.IntentFilterResponse) []string {
	if len(resp.Intents) == 0 {
		return nil
	}
	out := make([]string, 0, len(resp.Intents))
	seen := make(map[string]struct{}, len(resp.Intents))
	for _, in := range resp.Intents {
		if strings.TrimSpace(in.Status) != "ready" {
			continue
		}
		skill := firstNonEmptyMapString(in.Normalized, "skill")
		if skill == "" {
			skill = firstNonEmptyMapString(in.Parameters, "skill")
		}
		if skill == "" {
			skill = inferSkillFromIntentID(in.IntentID)
		}
		skill = strings.TrimSpace(skill)
		if skill == "" {
			continue
		}
		if _, ok := seen[skill]; ok {
			continue
		}
		seen[skill] = struct{}{}
		out = append(out, skill)
	}
	return out
}

func firstNonEmptyMapString(m map[string]any, key string) string {
	if len(m) == 0 {
		return ""
	}
	v, ok := m[key]
	if !ok || v == nil {
		return ""
	}
	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", val))
	}
}

func inferSkillFromIntentID(intentID string) string {
	switch strings.ToLower(strings.TrimSpace(intentID)) {
	case "intent_light_control":
		return "control_light"
	case "intent_alarm_create":
		return "create_alarm"
	case "intent_head_motion":
		return "set_head_motion"
	default:
		return ""
	}
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

func (s *Service) executeTerminalSkillWithGate(ctx context.Context, terminalID, skill string, args json.RawMessage, execMode string, execProbability float64) string {
	switch strings.TrimSpace(execMode) {
	case "auto_execute":
		return s.executeTerminalSkill(ctx, terminalID, skill, args)
	default:
		return fmt.Sprintf("技能执行已拦截（mode=%s, prob=%.3f, skill=%s）", execMode, execProbability, skill)
	}
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
