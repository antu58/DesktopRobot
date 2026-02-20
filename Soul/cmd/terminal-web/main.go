package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
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

type terminalState struct {
	mu         sync.RWMutex
	color      string
	lastAction string
	updatedAt  time.Time
	logs       []string
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := config.LoadTerminalWebConfig()

	state := &terminalState{color: "off"}
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
		state.mu.RLock()
		defer state.mu.RUnlock()
		writeJSON(w, http.StatusOK, map[string]any{
			"terminal_id":   cfg.TerminalID,
			"soul_hint":     cfg.SoulHint,
			"skill_version": cfg.SkillVersion,
			"color":         state.color,
			"last_action":   state.lastAction,
			"updated_at":    state.updatedAt,
			"logs":          state.logs,
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
		if in.SessionID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "session_id is required"})
			return
		}
		if len(in.Inputs) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "inputs is required"})
			return
		}
		if !hasKeyboardTextInput(in.Inputs) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "currently only input.type=keyboard_text with non-empty text is supported"})
			return
		}

		payload := domain.ChatRequest{
			UserID:     cfg.UserID,
			SessionID:  in.SessionID,
			TerminalID: cfg.TerminalID,
			SoulHint:   cfg.SoulHint,
			Inputs:     in.Inputs,
		}

		buf, _ := json.Marshal(payload)

		httpReq, _ := http.NewRequestWithContext(req.Context(), http.MethodPost, cfg.SoulAPIBaseURL+"/v1/chat", bytes.NewReader(buf))
		httpReq.Header.Set("content-type", "application/json")
		resp, err := http.DefaultClient.Do(httpReq)
		if err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": err.Error()})
			return
		}
		defer resp.Body.Close()

		var out any
		if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]string{"error": "invalid soul response"})
			return
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
				Description: "亮红灯",
				InputSchema: json.RawMessage(`{"type":"object","properties":{},"required":[]}`),
			},
			{
				Name:        "light_green",
				Description: "亮绿灯",
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

	state.logs = append(state.logs, fmt.Sprintf("%s -> %s", time.Now().Format(time.RFC3339), result.Output))
	if len(state.logs) > 100 {
		state.logs = state.logs[len(state.logs)-100:]
	}
	return result
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("content-type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func hasKeyboardTextInput(inputs []domain.ChatInput) bool {
	for _, in := range inputs {
		if strings.EqualFold(strings.TrimSpace(in.Type), "keyboard_text") && strings.TrimSpace(in.Text) != "" {
			return true
		}
	}
	return false
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
  <input id="session" placeholder="session_id" value="s1" />
  <textarea id="msg" rows="3" placeholder="输入你的话"></textarea><br/>
  <button onclick="askSoul()">发送</button>
  <pre id="resp"></pre>

  <h3>Logs</h3>
  <pre id="logs"></pre>

  <script>
    async function refreshState() {
      const r = await fetch('/state');
      const s = await r.json();
      const lamp = document.getElementById('lamp');
      lamp.className = 'lamp ' + (s.color || '');
      document.getElementById('status').textContent = 'terminal=' + s.terminal_id + ' color=' + s.color + ' action=' + (s.last_action || '');
      document.getElementById('logs').textContent = (s.logs || []).join('\n');
    }

    async function askSoul() {
      const keyboardText = document.getElementById('msg').value;
      const payload = {
        session_id: document.getElementById('session').value,
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
      await refreshState();
    }

    setInterval(refreshState, 1500);
    refreshState();
  </script>
</body>
</html>`
