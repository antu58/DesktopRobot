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
	lastAction      string
	updatedAt       time.Time
	logs            []string
	activeSessionID string
	conversations   map[string][]chatTurn
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.LoadTerminalWebConfig()

	state := &terminalState{
		color:           "off",
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
		activeSessionID, turns, sessions, color, lastAction, updatedAt, logs := state.snapshot()
		writeJSON(w, http.StatusOK, map[string]any{
			"terminal_id":        cfg.TerminalID,
			"soul_hint":          cfg.SoulHint,
			"skill_version":      cfg.SkillVersion,
			"active_session_id":  activeSessionID,
			"sessions":           sessions,
			"conversation_turns": turns,
			"color":              color,
			"last_action":        lastAction,
			"updated_at":         updatedAt,
			"logs":               logs,
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

	result := domain.InvokeResult{RequestID: req.RequestID, OK: true}
	switch req.Skill {
	case "light_red":
		state.color = "red"
		state.lastAction = "亮红灯"
		state.updatedAt = time.Now()
		result.Output = "红灯已点亮"
	case "light_green":
		state.color = "green"
		state.lastAction = "亮绿灯"
		state.updatedAt = time.Now()
		result.Output = "绿灯已点亮"
	default:
		result.OK = false
		result.Error = "unknown skill: " + req.Skill
		result.Output = result.Error
	}

	state.appendLogLocked(fmt.Sprintf("%s -> %s", time.Now().Format(time.RFC3339), result.Output))
	return result
}

func (s *terminalState) snapshot() (activeSessionID string, turns []chatTurn, sessions []string, color, lastAction string, updatedAt time.Time, logs []string) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	activeSessionID = s.activeSessionID
	color = s.color
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
	if appliedLight {
		s.lastAction = "本轮已执行亮灯技能"
	} else {
		s.color = "off"
		s.lastAction = "本轮未触发亮灯（非对错场景或无需动作）"
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
    body { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; margin: 24px; }
    .lamp { width: 100px; height: 100px; border-radius: 50%; border: 2px solid #111; margin-bottom: 16px; background: #ddd; }
    .lamp.red { background: #ef4444; }
    .lamp.green { background: #22c55e; }
    textarea, input { width: 100%; max-width: 720px; }
    button { margin-top: 8px; }
    pre { background: #111; color: #ddd; padding: 12px; border-radius: 8px; max-width: 720px; overflow: auto; }
  </style>
</head>
<body>
  <h2>Terminal Web Debug</h2>
  <div id="lamp" class="lamp"></div>
  <p id="status">loading...</p>

  <h3>Ask Soul</h3>
  <input id="session" placeholder="session_id（留空使用当前会话）" />
  <button onclick="newSession()">新建会话</button>
  <p id="sessionInfo"></p>
  <textarea id="msg" rows="3" placeholder="输入你的话"></textarea><br/>
  <button onclick="askSoul()">发送</button>
  <pre id="resp"></pre>

  <h3>Conversation</h3>
  <pre id="conversation"></pre>

  <h3>Logs</h3>
  <pre id="logs"></pre>

  <script>
    let activeSessionId = '';

    function renderTurns(turns) {
      return (turns || []).map(t => {
        const ts = t.at ? new Date(t.at).toLocaleTimeString() : '';
        const skills = (t.executed_skills || []).length ? (' skills=' + t.executed_skills.join('+')) : '';
        return '[' + ts + '] ' + t.role + ': ' + (t.content || '') + skills;
      }).join('\n');
    }

    async function refreshState() {
      const r = await fetch('/state');
      const s = await r.json();
      const lamp = document.getElementById('lamp');
      lamp.className = 'lamp ' + (s.color || '');
      activeSessionId = s.active_session_id || activeSessionId;
      document.getElementById('status').textContent = 'terminal=' + s.terminal_id + ' color=' + s.color + ' action=' + (s.last_action || '');
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
      const r = await fetch('/ask', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(payload) });
      const out = await r.json();
      document.getElementById('resp').textContent = JSON.stringify(out, null, 2);
      document.getElementById('msg').value = '';
      await refreshState();
    }

    setInterval(refreshState, 1500);
    refreshState();
  </script>
</body>
</html>`
