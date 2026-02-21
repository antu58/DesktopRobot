package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/go-chi/chi/v5"

	"soul/internal/config"
	"soul/internal/domain"
	"soul/internal/mqtt"
)

type chatTurn struct {
	Role           string    `json:"role"`
	Content        string    `json:"content"`
	SessionID      string    `json:"session_id"`
	ExecutedSkills []string  `json:"executed_skills,omitempty"`
	At             time.Time `json:"at"`
}

type terminalState struct {
	mu              sync.RWMutex
	color           string
	expression      string
	headPose        string
	headMotion      string
	headMotionUntil time.Time
	headMotionDur   time.Duration
	lastAction      string
	updatedAt       time.Time
	logs            []string
	activeSessionID string
	conversations   map[string][]chatTurn
}

var (
	expressionOptions = []string{"微笑", "大笑", "生气", "哭", "不开心"}
	headActionOptions = []string{"抬头", "低头", "左看", "右看", "点头", "摇头"}
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.LoadTerminalWebConfig()

	state := &terminalState{
		color:           "off",
		expression:      "微笑",
		headPose:        "中位",
		activeSessionID: "s1",
		conversations:   make(map[string][]chatTurn),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	mqttClient, err := startMQTT(ctx, cfg, state, logger)
	if err != nil {
		logger.Error("start terminal mqtt failed", "error", err)
		os.Exit(1)
	}
	defer mqttClient.Disconnect(100)

	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	r.Get("/state", func(w http.ResponseWriter, _ *http.Request) {
		activeSessionID, turns, sessions, color, expression, headPose, headMotion, headMotionDurationSeconds, lastAction, updatedAt, logs := state.snapshot()
		writeJSON(w, http.StatusOK, map[string]any{
			"terminal_id":                  cfg.TerminalID,
			"soul_hint":                    cfg.SoulHint,
			"skill_version":                cfg.SkillVersion,
			"active_session_id":            activeSessionID,
			"sessions":                     sessions,
			"conversation_turns":           turns,
			"color":                        color,
			"expression":                   expression,
			"head_pose":                    headPose,
			"head_motion":                  headMotion,
			"head_motion_duration_seconds": headMotionDurationSeconds,
			"last_action":                  lastAction,
			"updated_at":                   updatedAt,
			"logs":                         logs,
		})
	})

	r.Post("/session/new", func(w http.ResponseWriter, _ *http.Request) {
		sessionID := state.newSessionID()
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":         true,
			"session_id": sessionID,
		})
	})

	r.Post("/report-skills", func(w http.ResponseWriter, _ *http.Request) {
		if err := publishSkills(mqttClient, cfg); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	r.Post("/ask", func(w http.ResponseWriter, req *http.Request) {
		var in struct {
			SessionID string             `json:"session_id"`
			Inputs    []domain.ChatInput `json:"inputs"`
		}
		if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid json"})
			return
		}
		if len(in.Inputs) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "inputs is required"})
			return
		}
		inputs := normalizeInputs(in.Inputs)
		keyboardText := firstKeyboardText(inputs)
		if strings.TrimSpace(keyboardText) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "currently only input.type=keyboard_text with non-empty text is supported"})
			return
		}
		sessionID := state.useOrCreateSession(strings.TrimSpace(in.SessionID))
		state.beginRound(sessionID, keyboardText)

		payload := domain.ChatRequest{
			UserID:     cfg.UserID,
			SessionID:  sessionID,
			TerminalID: cfg.TerminalID,
			SoulHint:   cfg.SoulHint,
			Inputs:     inputs,
		}

		buf, _ := json.Marshal(payload)

		httpReq, _ := http.NewRequestWithContext(req.Context(), http.MethodPost, cfg.SoulAPIBaseURL+"/v1/chat", bytes.NewReader(buf))
		httpReq.Header.Set("content-type", "application/json")
		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			state.appendLog(fmt.Sprintf("%s [session:%s] /v1/chat failed: %v", time.Now().Format(time.RFC3339), sessionID, err))
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "invalid soul response"})
			return
		}
		var out any
		if err := json.Unmarshal(body, &out); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "invalid soul response"})
			return
		}

		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			var chatOut domain.ChatResponse
			if err := json.Unmarshal(body, &chatOut); err == nil {
				state.finishRound(sessionID, chatOut.Reply, chatOut.ExecutedSkills)
			} else {
				state.appendLog(fmt.Sprintf("%s [session:%s] parse chat response failed: %v", time.Now().Format(time.RFC3339), sessionID, err))
			}
		} else {
			state.appendLog(fmt.Sprintf("%s [session:%s] /v1/chat status=%d", time.Now().Format(time.RFC3339), sessionID, resp.StatusCode))
		}

		writeJSON(w, resp.StatusCode, out)
	})

	r.Get("/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("content-type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(indexHTML))
	})

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("terminal web started", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("terminal web http error", "error", err)
			cancel()
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	select {
	case <-sigCh:
		logger.Info("terminal web shutdown signal")
	case <-ctx.Done():
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("terminal web shutdown failed", "error", err)
	}
}

func startMQTT(ctx context.Context, cfg config.TerminalWebConfig, state *terminalState, logger *slog.Logger) (paho.Client, error) {
	opts := paho.NewClientOptions().
		AddBroker(cfg.MQTTBrokerURL).
		SetClientID(cfg.MQTTClientID).
		SetAutoReconnect(true).
		SetConnectRetry(true)

	if cfg.MQTTUsername != "" {
		opts.SetUsername(cfg.MQTTUsername)
		opts.SetPassword(cfg.MQTTPassword)
	}

	onlineTopic := mqtt.TopicOnline(cfg.MQTTTopicPrefix, cfg.TerminalID)
	opts.SetWill(onlineTopic, "offline", 1, true)

	client := paho.NewClient(opts)
	if token := client.Connect(); token.Wait() && token.Error() != nil {
		return nil, token.Error()
	}

	if token := client.Publish(onlineTopic, 1, true, "online"); token.Wait() && token.Error() != nil {
		return nil, token.Error()
	}

	if err := publishSkills(client, cfg); err != nil {
		return nil, err
	}
	heartbeatTopic := mqtt.TopicHeartbeat(cfg.MQTTTopicPrefix, cfg.TerminalID)
	if token := client.Publish(heartbeatTopic, 0, false, []byte("1")); token.Wait() && token.Error() != nil {
		return nil, token.Error()
	}

	invokeTopic := fmt.Sprintf("%s/terminal/%s/invoke/+", cfg.MQTTTopicPrefix, cfg.TerminalID)
	if token := client.Subscribe(invokeTopic, 1, func(_ paho.Client, msg paho.Message) {
		var req domain.InvokeRequest
		if err := json.Unmarshal(msg.Payload(), &req); err != nil {
			logger.Error("invalid invoke payload", "error", err)
			return
		}
		result := handleSkill(req, state)
		resultTopic := mqtt.TopicResult(cfg.MQTTTopicPrefix, cfg.TerminalID, req.RequestID)
		buf, _ := json.Marshal(result)
		if tk := client.Publish(resultTopic, 1, false, buf); tk.Wait() && tk.Error() != nil {
			logger.Error("publish result failed", "error", tk.Error())
		}
	}); token.Wait() && token.Error() != nil {
		return nil, token.Error()
	}

	statusTopic := mqtt.TopicStatus(cfg.MQTTTopicPrefix, cfg.TerminalID)
	if token := client.Subscribe(statusTopic, 1, func(_ paho.Client, msg paho.Message) {
		var payload struct {
			Status    string `json:"status"`
			Message   string `json:"message"`
			SessionID string `json:"session_id"`
			TS        string `json:"ts"`
		}
		logLine := ""
		if err := json.Unmarshal(msg.Payload(), &payload); err == nil {
			if payload.Status == "" {
				payload.Status = "unknown"
			}
			if payload.Message != "" {
				logLine = fmt.Sprintf("%s [status:%s][session:%s] %s", time.Now().Format(time.RFC3339), payload.Status, payload.SessionID, payload.Message)
			} else {
				logLine = fmt.Sprintf("%s [status:%s][session:%s]", time.Now().Format(time.RFC3339), payload.Status, payload.SessionID)
			}
			state.mu.Lock()
			state.lastAction = "状态更新: " + payload.Status
			state.updatedAt = time.Now()
			state.appendLogLocked(logLine)
			state.mu.Unlock()
			return
		}
		logLine = fmt.Sprintf("%s [status:raw] %s", time.Now().Format(time.RFC3339), strings.TrimSpace(string(msg.Payload())))
		state.appendLog(logLine)
	}); token.Wait() && token.Error() != nil {
		return nil, token.Error()
	}

	go func() {
		heartbeatTicker := time.NewTicker(cfg.HeartbeatInterval)
		defer heartbeatTicker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-heartbeatTicker.C:
				client.Publish(heartbeatTopic, 0, false, []byte("1"))
			}
		}
	}()

	go func() {
		<-ctx.Done()
		client.Publish(onlineTopic, 1, true, "offline")
	}()

	return client, nil
}

func publishSkills(client paho.Client, cfg config.TerminalWebConfig) error {
	report := domain.SkillReport{
		TerminalID:   cfg.TerminalID,
		SoulHint:     cfg.SoulHint,
		SkillVersion: cfg.SkillVersion,
		Skills: []domain.SkillDefinition{
			{
				Name:        "light_red",
				Description: "用途：当需要表达否定、不认同或判定信息错误时调用。效果：亮红灯。约束：与 light_green 互斥；若本轮不涉及对错判断，不调用。",
				InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[]}`),
			},
			{
				Name:        "light_green",
				Description: "用途：当需要表达肯定、认同或判定信息正确时调用。效果：亮绿灯。约束：与 light_red 互斥；若本轮不涉及对错判断，不调用。",
				InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[]}`),
			},
			{
				Name:        "set_expression",
				Description: "技能：表情。用途：设置机器人面部状态。参数 emotion 可选值：微笑、大笑、生气、哭、不开心。",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"emotion":{"type":"string","enum":["微笑","大笑","生气","哭","不开心"]}},"required":["emotion"],"additionalProperties":false}`),
			},
			{
				Name:        "set_head_motion",
				Description: "技能：头部动作。参数 action 可选值：抬头、低头、左看、右看、点头、摇头；duration_seconds 为动作持续秒数（仅点头/摇头时建议提供，范围 0.2~10）。",
				InputSchema: json.RawMessage(`{"type":"object","properties":{"action":{"type":"string","enum":["抬头","低头","左看","右看","点头","摇头"]},"duration_seconds":{"type":"number","minimum":0.2,"maximum":10}},"required":["action"],"additionalProperties":false}`),
			},
		},
	}
	buf, _ := json.Marshal(report)
	topic := mqtt.TopicSkills(cfg.MQTTTopicPrefix, cfg.TerminalID)
	if token := client.Publish(topic, 1, true, buf); token.Wait() && token.Error() != nil {
		return token.Error()
	}
	return nil
}

func handleSkill(req domain.InvokeRequest, state *terminalState) domain.InvokeResult {
	state.mu.Lock()
	defer state.mu.Unlock()

	now := time.Now()
	result := domain.InvokeResult{RequestID: req.RequestID, OK: true}
	setError := func(msg string) {
		result.OK = false
		result.Error = msg
		result.Output = msg
	}

	switch req.Skill {
	case "light_red":
		state.color = "red"
		state.lastAction = "亮红灯"
		state.updatedAt = now
		result.Output = "红灯已点亮"
	case "light_green":
		state.color = "green"
		state.lastAction = "亮绿灯"
		state.updatedAt = now
		result.Output = "绿灯已点亮"
	case "set_expression":
		var args struct {
			Emotion string `json:"emotion"`
		}
		if err := decodeSkillArgs(req.Arguments, &args); err != nil {
			setError("set_expression 参数错误: " + err.Error())
			break
		}
		emotion, ok := normalizeExpression(args.Emotion)
		if !ok {
			setError("set_expression 参数错误: emotion 仅支持 " + strings.Join(expressionOptions, "、"))
			break
		}
		state.expression = emotion
		state.lastAction = "表情: " + emotion
		state.updatedAt = now
		result.Output = "表情已设置为 " + emotion
	case "set_head_motion":
		var args struct {
			Action          string  `json:"action"`
			DurationSeconds float64 `json:"duration_seconds"`
		}
		if err := decodeSkillArgs(req.Arguments, &args); err != nil {
			setError("set_head_motion 参数错误: " + err.Error())
			break
		}
		action, ok := normalizeHeadAction(args.Action)
		if !ok {
			setError("set_head_motion 参数错误: action 仅支持 " + strings.Join(headActionOptions, "、"))
			break
		}
		duration, err := normalizeHeadMotionDuration(args.DurationSeconds)
		if err != nil {
			setError("set_head_motion 参数错误: " + err.Error())
			break
		}
		state.applyHeadActionLocked(action, duration, now)
		state.updatedAt = now
		if action == "点头" || action == "摇头" {
			state.lastAction = fmt.Sprintf("头部动作: %s(%.1fs)", action, duration.Seconds())
			result.Output = fmt.Sprintf("头部动作已执行：%s，持续 %.1f 秒", action, duration.Seconds())
		} else {
			state.lastAction = "头部动作: " + action
			result.Output = "头部动作已执行：" + action
		}
	default:
		setError("unknown skill: " + req.Skill)
	}

	state.appendLogLocked(fmt.Sprintf("%s skill=%s args=%s -> %s", now.Format(time.RFC3339), req.Skill, strings.TrimSpace(string(req.Arguments)), result.Output))
	return result
}

func decodeSkillArgs(raw json.RawMessage, out any) error {
	payload := strings.TrimSpace(string(raw))
	if payload == "" || payload == "null" {
		payload = "{}"
	}
	if err := json.Unmarshal([]byte(payload), out); err != nil {
		return fmt.Errorf("invalid json args: %w", err)
	}
	return nil
}

func normalizeExpression(v string) (string, bool) {
	switch strings.TrimSpace(v) {
	case "微笑":
		return "微笑", true
	case "大笑":
		return "大笑", true
	case "生气":
		return "生气", true
	case "哭":
		return "哭", true
	case "不开心":
		return "不开心", true
	default:
		return "", false
	}
}

func normalizeHeadAction(v string) (string, bool) {
	switch strings.TrimSpace(v) {
	case "抬头":
		return "抬头", true
	case "低头":
		return "低头", true
	case "左看":
		return "左看", true
	case "右看":
		return "右看", true
	case "点头":
		return "点头", true
	case "摇头":
		return "摇头", true
	default:
		return "", false
	}
}

func normalizeHeadMotionDuration(seconds float64) (time.Duration, error) {
	if seconds == 0 {
		return 1500 * time.Millisecond, nil
	}
	if seconds < 0.2 || seconds > 10 {
		return 0, fmt.Errorf("duration_seconds 必须在 0.2 到 10 之间")
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func (s *terminalState) applyHeadActionLocked(action string, duration time.Duration, now time.Time) {
	switch action {
	case "抬头", "低头", "左看", "右看":
		s.headPose = action
		s.headMotion = ""
		s.headMotionDur = 0
		s.headMotionUntil = time.Time{}
	case "点头", "摇头":
		s.headMotion = action
		s.headMotionDur = duration
		s.headMotionUntil = now.Add(duration)
	}
}

func (s *terminalState) snapshot() (activeSessionID string, turns []chatTurn, sessions []string, color, expression, headPose, headMotion string, headMotionDurationSeconds float64, lastAction string, updatedAt time.Time, logs []string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	activeSessionID = s.activeSessionID
	color = s.color
	expression = s.expression
	headPose = s.headPose
	if s.headMotion != "" && time.Now().Before(s.headMotionUntil) {
		headMotion = s.headMotion
		headMotionDurationSeconds = s.headMotionDur.Seconds()
	}
	lastAction = s.lastAction
	updatedAt = s.updatedAt

	sessions = make([]string, 0, len(s.conversations))
	for sessionID := range s.conversations {
		sessions = append(sessions, sessionID)
	}
	sort.Strings(sessions)

	rawTurns := s.conversations[activeSessionID]
	turns = make([]chatTurn, len(rawTurns))
	copy(turns, rawTurns)
	logs = make([]string, len(s.logs))
	copy(logs, s.logs)
	return
}

func (s *terminalState) newSessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	sessionID := fmt.Sprintf("s-%d", time.Now().UnixNano())
	s.activeSessionID = sessionID
	if _, ok := s.conversations[sessionID]; !ok {
		s.conversations[sessionID] = []chatTurn{}
	}
	s.appendLogLocked(fmt.Sprintf("%s [session:%s] new session created", time.Now().Format(time.RFC3339), sessionID))
	return sessionID
}

func (s *terminalState) useOrCreateSession(sessionID string) string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if sessionID == "" {
		if strings.TrimSpace(s.activeSessionID) == "" {
			s.activeSessionID = fmt.Sprintf("s-%d", time.Now().UnixNano())
		}
		sessionID = s.activeSessionID
	} else {
		s.activeSessionID = sessionID
	}
	if _, ok := s.conversations[sessionID]; !ok {
		s.conversations[sessionID] = []chatTurn{}
	}
	return sessionID
}

func (s *terminalState) beginRound(sessionID, userText string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.activeSessionID = sessionID
	s.color = "off"
	s.headMotion = ""
	s.headMotionDur = 0
	s.headMotionUntil = time.Time{}
	s.lastAction = "本轮处理中"
	s.updatedAt = time.Now()
	s.appendTurnLocked(sessionID, chatTurn{
		Role:      "user",
		Content:   strings.TrimSpace(userText),
		SessionID: sessionID,
		At:        time.Now(),
	})
	s.appendLogLocked(fmt.Sprintf("%s [session:%s] user: %s", time.Now().Format(time.RFC3339), sessionID, strings.TrimSpace(userText)))
}

func (s *terminalState) finishRound(sessionID, reply string, executedSkills []string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.activeSessionID = sessionID
	s.appendTurnLocked(sessionID, chatTurn{
		Role:           "assistant",
		Content:        strings.TrimSpace(reply),
		SessionID:      sessionID,
		ExecutedSkills: append([]string{}, executedSkills...),
		At:             time.Now(),
	})

	appliedLight := false
	for _, skill := range executedSkills {
		switch skill {
		case "light_red":
			s.color = "red"
			appliedLight = true
		case "light_green":
			s.color = "green"
			appliedLight = true
		}
	}
	if !appliedLight {
		s.color = "off"
	}
	if len(executedSkills) == 0 {
		s.lastAction = "本轮未触发技能"
	} else if strings.TrimSpace(s.lastAction) == "" || s.lastAction == "本轮处理中" {
		s.lastAction = "本轮已执行技能: " + strings.Join(executedSkills, ",")
	}
	s.updatedAt = time.Now()
	s.appendLogLocked(fmt.Sprintf("%s [session:%s] assistant: %s | skills=%s", time.Now().Format(time.RFC3339), sessionID, strings.TrimSpace(reply), strings.Join(executedSkills, ",")))
}

func (s *terminalState) appendLog(line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendLogLocked(line)
}

func (s *terminalState) appendLogLocked(line string) {
	s.logs = append(s.logs, line)
	if len(s.logs) > 200 {
		s.logs = s.logs[len(s.logs)-200:]
	}
}

func (s *terminalState) appendTurnLocked(sessionID string, turn chatTurn) {
	s.conversations[sessionID] = append(s.conversations[sessionID], turn)
	if len(s.conversations[sessionID]) > 120 {
		s.conversations[sessionID] = s.conversations[sessionID][len(s.conversations[sessionID])-120:]
	}
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func firstKeyboardText(inputs []domain.ChatInput) string {
	for _, in := range inputs {
		if strings.EqualFold(strings.TrimSpace(in.Type), "keyboard_text") && strings.TrimSpace(in.Text) != "" {
			return strings.TrimSpace(in.Text)
		}
	}
	return ""
}

func normalizeInputs(inputs []domain.ChatInput) []domain.ChatInput {
	out := make([]domain.ChatInput, 0, len(inputs))
	baseTS := time.Now().UTC()
	for i, in := range inputs {
		item := in
		if strings.TrimSpace(item.InputID) == "" {
			item.InputID = fmt.Sprintf("in-%d-%d", baseTS.UnixNano(), i+1)
		}
		if strings.TrimSpace(item.TS) == "" {
			item.TS = baseTS.Add(time.Duration(i) * time.Millisecond).Format(time.RFC3339Nano)
		}
		if strings.TrimSpace(item.Source) == "" && strings.EqualFold(strings.TrimSpace(item.Type), "keyboard_text") {
			item.Source = "keyboard"
		}
		out = append(out, item)
	}
	return out
}

const indexHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="UTF-8" />
  <meta name="viewport" content="width=device-width,initial-scale=1" />
  <title>Terminal Debug</title>
  <style>
    :root {
      --bg: radial-gradient(1200px 720px at 15% -20%, #193659 0%, #0b1224 45%, #05070f 100%);
      --panel: rgba(15, 23, 42, 0.72);
      --line: rgba(148, 163, 184, 0.25);
      --text: #e2e8f0;
      --muted: #94a3b8;
      --accent: #22d3ee;
      --good: #22c55e;
      --bad: #ef4444;
    }

    * { box-sizing: border-box; }
    body {
      margin: 0;
      min-height: 100vh;
      padding: 24px;
      font-family: "Avenir Next", "PingFang SC", "Hiragino Sans GB", "Microsoft YaHei", sans-serif;
      background: var(--bg);
      color: var(--text);
    }

    h2, h3 { margin: 0 0 12px; letter-spacing: 0.02em; }
    h2 { margin-bottom: 16px; }
    .layout {
      display: grid;
      grid-template-columns: minmax(320px, 420px) minmax(480px, 1fr);
      gap: 18px;
      align-items: start;
    }
    .layout.logs {
      margin-top: 18px;
      grid-template-columns: repeat(2, minmax(320px, 1fr));
    }
    .panel {
      border-radius: 16px;
      border: 1px solid var(--line);
      background: var(--panel);
      backdrop-filter: blur(8px);
      padding: 16px;
    }
    .robot-panel {
      position: sticky;
      top: 16px;
    }
    .status-line {
      margin: 12px 0 10px;
      color: #dbeafe;
      font-size: 14px;
      line-height: 1.4;
    }
    .chips {
      display: flex;
      gap: 8px;
      flex-wrap: wrap;
    }
    .chip {
      font-size: 13px;
      color: #cffafe;
      padding: 4px 10px;
      border-radius: 999px;
      border: 1px solid rgba(34, 211, 238, 0.35);
      background: rgba(12, 74, 110, 0.28);
    }
    .hint {
      margin: 10px 0 0;
      color: var(--muted);
      font-size: 13px;
    }

    .robot-stage {
      position: relative;
      height: 360px;
      border-radius: 14px;
      border: 1px solid rgba(56, 189, 248, 0.28);
      background: radial-gradient(circle at 50% 8%, rgba(14, 116, 144, 0.4), rgba(2, 6, 23, 0.9) 64%);
      overflow: hidden;
      perspective: 1000px;
    }
    .lamp {
      position: absolute;
      top: 14px;
      right: 14px;
      width: 56px;
      height: 56px;
      border-radius: 50%;
      border: 2px solid rgba(15, 23, 42, 0.9);
      background: rgba(203, 213, 225, 0.28);
      box-shadow: 0 0 0 2px rgba(15, 23, 42, 0.25);
      transition: background 0.2s ease, box-shadow 0.2s ease;
    }
    .lamp.red { background: var(--bad); box-shadow: 0 0 20px rgba(239, 68, 68, 0.85); }
    .lamp.green { background: var(--good); box-shadow: 0 0 20px rgba(34, 197, 94, 0.85); }

    .robot {
      --head-rx: 0deg;
      --head-ry: 0deg;
      --motion-duration: 1.5s;
      position: absolute;
      left: 50%;
      bottom: 18px;
      width: 220px;
      height: 310px;
      transform: translateX(-50%);
      transform-style: preserve-3d;
    }
    .robot-head {
      position: absolute;
      left: 50%;
      top: 24px;
      width: 164px;
      height: 128px;
      transform: translateX(-50%) translateZ(26px) rotateX(var(--head-rx)) rotateY(var(--head-ry));
      transform-style: preserve-3d;
      transition: transform 0.35s ease;
    }
    .robot-head-shell {
      position: absolute;
      inset: 0;
      border-radius: 22px;
      border: 1px solid rgba(255, 255, 255, 0.42);
      background: linear-gradient(165deg, #d8e6ff, #aac0e4 55%, #7d90ba 100%);
      box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.6), 0 10px 24px rgba(8, 47, 73, 0.4);
    }
    .face {
      position: absolute;
      inset: 14px 14px 16px;
      border-radius: 16px;
      border: 1px solid rgba(15, 23, 42, 0.16);
      background: linear-gradient(180deg, #f8fdff, #d9e9fc);
      overflow: hidden;
    }
    .brows {
      position: absolute;
      left: 26px;
      right: 26px;
      top: 24px;
      display: flex;
      justify-content: space-between;
      pointer-events: none;
    }
    .brow {
      width: 36px;
      height: 5px;
      border-radius: 999px;
      background: #0f172a;
      opacity: 0;
      transition: opacity 0.2s ease, transform 0.2s ease;
    }
    .brow.left { transform: rotate(0deg); }
    .brow.right { transform: rotate(0deg); }
    .eyes {
      position: absolute;
      left: 28px;
      right: 28px;
      top: 42px;
      display: flex;
      justify-content: space-between;
      align-items: center;
    }
    .eye {
      width: 18px;
      height: 18px;
      border-radius: 50%;
      background: #0f172a;
      transition: all 0.2s ease;
    }
    .mouth {
      position: absolute;
      left: 50%;
      bottom: 22px;
      width: 58px;
      height: 24px;
      transform: translateX(-50%);
      border-bottom: 5px solid #0f172a;
      border-radius: 0 0 64px 64px;
      transition: all 0.2s ease;
    }
    .tears {
      position: absolute;
      inset: 0;
      pointer-events: none;
    }
    .tear {
      position: absolute;
      top: 54px;
      width: 6px;
      height: 16px;
      border-radius: 999px;
      background: linear-gradient(180deg, #7dd3fc, #38bdf8);
      opacity: 0;
    }
    .tear.left { left: 38px; }
    .tear.right { right: 38px; }

    .robot-body {
      position: absolute;
      left: 50%;
      bottom: 36px;
      width: 190px;
      height: 138px;
      transform: translateX(-50%) translateZ(10px);
      border-radius: 26px;
      border: 1px solid rgba(148, 163, 184, 0.45);
      background: linear-gradient(160deg, #1e293b, #0f172a 58%, #020617);
      box-shadow: inset 0 1px 0 rgba(255, 255, 255, 0.15);
    }
    .robot-neck {
      position: absolute;
      left: 50%;
      top: 146px;
      width: 30px;
      height: 24px;
      transform: translateX(-50%);
      border-radius: 10px;
      background: linear-gradient(180deg, #94a3b8, #64748b);
    }
    .chest-core {
      position: absolute;
      left: 50%;
      top: 38px;
      width: 52px;
      height: 52px;
      transform: translateX(-50%);
      border-radius: 50%;
      border: 1px solid rgba(125, 211, 252, 0.6);
      background: radial-gradient(circle at 50% 35%, rgba(103, 232, 249, 0.95), rgba(14, 116, 144, 0.7));
      box-shadow: 0 0 14px rgba(34, 211, 238, 0.65);
      animation: pulse 2.2s ease-in-out infinite;
    }
    .robot-shadow {
      position: absolute;
      left: 50%;
      bottom: 8px;
      width: 180px;
      height: 24px;
      transform: translateX(-50%);
      border-radius: 50%;
      background: radial-gradient(ellipse at center, rgba(15, 23, 42, 0.9), rgba(15, 23, 42, 0));
      filter: blur(1px);
    }

    .robot[data-emotion='laugh'] .eye {
      width: 22px;
      height: 10px;
      border-radius: 28px 28px 0 0;
      border-top: 4px solid #0f172a;
      background: transparent;
      transform: translateY(4px);
    }
    .robot[data-emotion='laugh'] .mouth {
      width: 68px;
      height: 36px;
      border: 4px solid #0f172a;
      border-top-width: 2px;
      border-radius: 0 0 46px 46px;
      background: #0f172a;
    }

    .robot[data-emotion='angry'] .brow { opacity: 1; }
    .robot[data-emotion='angry'] .brow.left { transform: rotate(18deg); }
    .robot[data-emotion='angry'] .brow.right { transform: rotate(-18deg); }
    .robot[data-emotion='angry'] .mouth {
      border-bottom: 0;
      border-top: 5px solid #0f172a;
      border-radius: 56px 56px 0 0;
      transform: translateX(-50%) translateY(8px);
    }
    .robot[data-emotion='angry'] .eye.left { transform: rotate(-10deg); }
    .robot[data-emotion='angry'] .eye.right { transform: rotate(10deg); }

    .robot[data-emotion='sad'] .mouth,
    .robot[data-emotion='cry'] .mouth {
      border-bottom: 0;
      border-top: 5px solid #0f172a;
      border-radius: 56px 56px 0 0;
      transform: translateX(-50%) translateY(4px);
    }
    .robot[data-emotion='cry'] .tear {
      opacity: 1;
      animation: drop 1.15s ease-in infinite;
    }
    .robot[data-emotion='cry'] .tear.right {
      animation-delay: 0.28s;
    }

    .robot.motion-nod .robot-head {
      animation: nod 0.62s ease-in-out infinite;
      animation-duration: calc(var(--motion-duration) / 2);
    }
    .robot.motion-shake .robot-head {
      animation: shake 0.42s ease-in-out infinite;
      animation-duration: calc(var(--motion-duration) / 3);
    }

    .row {
      display: flex;
      gap: 8px;
      flex-wrap: wrap;
      margin: 8px 0;
    }
    input, textarea {
      width: 100%;
      border-radius: 10px;
      border: 1px solid rgba(100, 116, 139, 0.5);
      background: rgba(15, 23, 42, 0.6);
      color: var(--text);
      padding: 10px 12px;
      font-size: 14px;
    }
    textarea {
      min-height: 92px;
      resize: vertical;
    }
    button {
      border: 1px solid rgba(56, 189, 248, 0.45);
      border-radius: 10px;
      background: linear-gradient(120deg, rgba(8, 145, 178, 0.55), rgba(6, 78, 59, 0.55));
      color: #e0f2fe;
      padding: 8px 12px;
      cursor: pointer;
      font-size: 13px;
    }
    pre {
      margin: 0;
      min-height: 110px;
      max-height: 300px;
      overflow: auto;
      border-radius: 12px;
      border: 1px solid rgba(71, 85, 105, 0.6);
      background: rgba(2, 6, 23, 0.75);
      color: #d1d5db;
      padding: 12px;
      font-size: 12.5px;
      line-height: 1.5;
      white-space: pre-wrap;
      word-break: break-word;
    }

    @keyframes pulse {
      0%, 100% { transform: translateX(-50%) scale(1); opacity: 0.95; }
      50% { transform: translateX(-50%) scale(1.08); opacity: 1; }
    }
    @keyframes drop {
      0% { transform: translateY(0); opacity: 0; }
      12% { opacity: 0.9; }
      100% { transform: translateY(20px); opacity: 0; }
    }
    @keyframes nod {
      0%, 100% { transform: translateX(-50%) translateZ(26px) rotateX(var(--head-rx)) rotateY(var(--head-ry)); }
      35% { transform: translateX(-50%) translateZ(26px) rotateX(calc(var(--head-rx) + 20deg)) rotateY(var(--head-ry)); }
      65% { transform: translateX(-50%) translateZ(26px) rotateX(calc(var(--head-rx) - 10deg)) rotateY(var(--head-ry)); }
    }
    @keyframes shake {
      0%, 100% { transform: translateX(-50%) translateZ(26px) rotateX(var(--head-rx)) rotateY(var(--head-ry)); }
      25% { transform: translateX(-50%) translateZ(26px) rotateX(var(--head-rx)) rotateY(calc(var(--head-ry) - 20deg)); }
      75% { transform: translateX(-50%) translateZ(26px) rotateX(var(--head-rx)) rotateY(calc(var(--head-ry) + 20deg)); }
    }

    @media (max-width: 960px) {
      body { padding: 14px; }
      .layout { grid-template-columns: 1fr; }
      .layout.logs { grid-template-columns: 1fr; }
      .robot-panel { position: static; }
    }
  </style>
</head>
<body>
  <h2>Terminal Web Debug</h2>

  <div class="layout">
    <section class="panel robot-panel">
      <div class="robot-stage">
        <div id="lamp" class="lamp off"></div>
        <div id="robot" class="robot" data-emotion="smile">
          <div class="robot-head">
            <div class="robot-head-shell"></div>
            <div class="face">
              <div class="brows">
                <span class="brow left"></span>
                <span class="brow right"></span>
              </div>
              <div class="eyes">
                <span class="eye left"></span>
                <span class="eye right"></span>
              </div>
              <div class="mouth"></div>
              <div class="tears">
                <span class="tear left"></span>
                <span class="tear right"></span>
              </div>
            </div>
          </div>
          <div class="robot-neck"></div>
          <div class="robot-body">
            <div class="chest-core"></div>
          </div>
          <div class="robot-shadow"></div>
        </div>
      </div>
      <p id="status" class="status-line">loading...</p>
      <div class="chips">
        <span id="emotionChip" class="chip">表情: 微笑</span>
        <span id="headChip" class="chip">头部: 中位</span>
      </div>
      <p class="hint">新增技能：set_expression（emotion）与 set_head_motion（action + duration_seconds）。</p>
    </section>

    <section class="panel">
      <h3>Ask Soul</h3>
      <input id="session" placeholder="session_id（留空使用当前会话）" />
      <div class="row">
        <button onclick="newSession()">新建会话</button>
        <button onclick="reportSkills()">重新上报技能</button>
      </div>
      <p id="sessionInfo" class="hint"></p>
      <textarea id="msg" rows="3" placeholder="输入你的话"></textarea>
      <div class="row">
        <button onclick="askSoul()">发送</button>
      </div>
      <pre id="resp"></pre>
    </section>
  </div>

  <div class="layout logs">
    <section class="panel">
      <h3>Conversation</h3>
      <pre id="conversation"></pre>
    </section>
    <section class="panel">
      <h3>Logs</h3>
      <pre id="logs"></pre>
    </section>
  </div>

  <script>
    let activeSessionId = '';

    const EMOTION_MAP = {
      '微笑': 'smile',
      '大笑': 'laugh',
      '生气': 'angry',
      '哭': 'cry',
      '不开心': 'sad'
    };

    const POSE_MAP = {
      '中位': [0, 0],
      '抬头': [-18, 0],
      '低头': [16, 0],
      '左看': [0, -22],
      '右看': [0, 22]
    };

    function renderTurns(turns) {
      return (turns || []).map(function (t) {
        const ts = t.at ? new Date(t.at).toLocaleTimeString() : '';
        const skills = (t.executed_skills || []).length ? (' skills=' + t.executed_skills.join('+')) : '';
        return '[' + ts + '] ' + t.role + ': ' + (t.content || '') + skills;
      }).join('\n');
    }

    function safeNumber(v, fallback) {
      const n = Number(v);
      if (Number.isFinite(n)) {
        return n;
      }
      return fallback;
    }

    function applyRobotState(s) {
      const lamp = document.getElementById('lamp');
      lamp.className = 'lamp ' + (s.color || 'off');

      const robot = document.getElementById('robot');
      const emotion = s.expression || '微笑';
      robot.dataset.emotion = EMOTION_MAP[emotion] || 'smile';

      const poseText = s.head_pose || '中位';
      const pose = POSE_MAP[poseText] || POSE_MAP['中位'];
      robot.style.setProperty('--head-rx', String(pose[0]) + 'deg');
      robot.style.setProperty('--head-ry', String(pose[1]) + 'deg');

      const duration = Math.min(Math.max(safeNumber(s.head_motion_duration_seconds, 1.5), 0.2), 10);
      robot.style.setProperty('--motion-duration', duration.toFixed(2) + 's');
      robot.classList.remove('motion-nod', 'motion-shake');
      const motion = s.head_motion || '';
      if (motion === '点头') {
        robot.classList.add('motion-nod');
      } else if (motion === '摇头') {
        robot.classList.add('motion-shake');
      }

      document.getElementById('emotionChip').textContent = '表情: ' + emotion;
      let headLine = '头部: ' + poseText;
      if (motion) {
        headLine += ' / ' + motion + ' ' + duration.toFixed(1) + 's';
      }
      document.getElementById('headChip').textContent = headLine;
    }

    async function refreshState() {
      const r = await fetch('/state');
      const s = await r.json();
      activeSessionId = s.active_session_id || activeSessionId;
      applyRobotState(s);
      document.getElementById('status').textContent = 'terminal=' + s.terminal_id + ' color=' + (s.color || 'off') + ' action=' + (s.last_action || '');
      document.getElementById('sessionInfo').textContent = 'active_session=' + (activeSessionId || '') + ' sessions=' + ((s.sessions || []).join(', '));
      document.getElementById('conversation').textContent = renderTurns(s.conversation_turns || []);
      document.getElementById('logs').textContent = (s.logs || []).join('\n');
    }

    async function newSession() {
      const r = await fetch('/session/new', { method: 'POST' });
      const out = await r.json();
      if (out.session_id) {
        activeSessionId = out.session_id;
        document.getElementById('session').value = activeSessionId;
      }
      await refreshState();
    }

    async function reportSkills() {
      const r = await fetch('/report-skills', { method: 'POST' });
      const out = await r.json();
      document.getElementById('resp').textContent = JSON.stringify(out, null, 2);
      await refreshState();
    }

    async function askSoul() {
      const keyboardText = document.getElementById('msg').value.trim();
      if (!keyboardText) return;
      const inputSession = document.getElementById('session').value.trim();
      const sessionId = inputSession || activeSessionId;
      const payload = {
        session_id: sessionId,
        inputs: [
          {
            type: 'keyboard_text',
            source: 'keyboard',
            text: keyboardText
          }
        ]
      };
      const r = await fetch('/ask', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(payload)
      });
      const out = await r.json();
      document.getElementById('resp').textContent = JSON.stringify(out, null, 2);
      document.getElementById('msg').value = '';
      await refreshState();
    }

    setInterval(refreshState, 1200);
    refreshState();
  </script>
</body>
</html>`
