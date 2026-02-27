package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type llmRequest struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	SessionID string `json:"session_id"`
	Text      string `json:"text"`
	Emotion   string `json:"emotion"`
	Event     string `json:"event"`
	Final     bool   `json:"final"`
	TsMS      int64  `json:"ts_ms"`
}

type llmResponse struct {
	Type      string `json:"type"`
	RequestID string `json:"request_id"`
	SessionID string `json:"session_id"`
	Text      string `json:"text,omitempty"`
	Emotion   string `json:"emotion,omitempty"`
	Event     string `json:"event,omitempty"`
	Final     bool   `json:"final"`
	Reply     string `json:"reply,omitempty"`
	Delta     string `json:"delta,omitempty"`
	Error     string `json:"error,omitempty"`
	TsMS      int64  `json:"ts_ms"`
}

type openAIRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Stream   bool            `json:"stream"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`
}

type openAIStreamChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		FinishReason string `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type sessionMemory struct {
	mu          sync.Mutex
	maxMessages int
	history     map[string][]openAIMessage
}

func newSessionMemory(maxMessages int) *sessionMemory {
	if maxMessages < 2 {
		maxMessages = 2
	}
	return &sessionMemory{
		maxMessages: maxMessages,
		history:     make(map[string][]openAIMessage),
	}
}

func (m *sessionMemory) snapshotWithUser(sessionID, userContent string) []openAIMessage {
	m.mu.Lock()
	defer m.mu.Unlock()

	base := append([]openAIMessage(nil), m.history[sessionID]...)
	base = append(base, openAIMessage{Role: "user", Content: userContent})
	if len(base) > m.maxMessages {
		base = base[len(base)-m.maxMessages:]
	}
	return base
}

func (m *sessionMemory) appendTurn(sessionID, userContent, assistantContent string) {
	if strings.TrimSpace(sessionID) == "" {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	h := append([]openAIMessage(nil), m.history[sessionID]...)
	if strings.TrimSpace(userContent) != "" {
		h = append(h, openAIMessage{Role: "user", Content: userContent})
	}
	if strings.TrimSpace(assistantContent) != "" {
		h = append(h, openAIMessage{Role: "assistant", Content: assistantContent})
	}
	if len(h) > m.maxMessages {
		h = h[len(h)-m.maxMessages:]
	}
	m.history[sessionID] = h
}

type llmBackend struct {
	client       *http.Client
	baseURL      string
	apiKey       string
	model        string
	systemPrompt string
	timeout      time.Duration
	memory       *sessionMemory
}

func newLLMBackendFromEnv() *llmBackend {
	baseURL := strings.TrimRight(getEnvString("OPENAI_BASE_URL", "https://api.openai.com/v1"), "/")
	model := getEnvString("LLM_MODEL", "gpt-4o-mini")
	apiKey := os.Getenv("OPENAI_API_KEY")
	timeout := time.Duration(getEnvInt("LLM_TIMEOUT_S", 90)) * time.Second
	historyLimit := getEnvInt("CHAT_HISTORY_LIMIT", 20)
	systemPrompt := getEnvString("LLM_SYSTEM_PROMPT", "你是语音助手，请基于用户输入直接给出简洁有帮助的中文回答。")

	return &llmBackend{
		client: &http.Client{
			Timeout: timeout,
		},
		baseURL:      baseURL,
		apiKey:       apiKey,
		model:        model,
		systemPrompt: systemPrompt,
		timeout:      timeout,
		memory:       newSessionMemory(historyLimit),
	}
}

func formatUserInput(req llmRequest) string {
	text := strings.TrimSpace(req.Text)
	if text == "" {
		return ""
	}
	if req.Emotion == "" && req.Event == "" {
		return text
	}
	return fmt.Sprintf("%s\n\n[voice_meta] emotion=%s event=%s final=%t", text, req.Emotion, req.Event, req.Final)
}

func (b *llmBackend) streamReply(ctx context.Context, req llmRequest, onDelta func(string) error) (string, error) {
	if strings.TrimSpace(req.Text) == "" {
		return "", fmt.Errorf("empty text")
	}
	if strings.TrimSpace(b.apiKey) == "" {
		return "", fmt.Errorf("OPENAI_API_KEY is required")
	}

	userContent := formatUserInput(req)
	messages := []openAIMessage{
		{Role: "system", Content: b.systemPrompt},
	}
	messages = append(messages, b.memory.snapshotWithUser(req.SessionID, userContent)...)

	payload := openAIRequest{
		Model:    b.model,
		Messages: messages,
		Stream:   true,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, b.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Authorization", "Bearer "+b.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := b.client.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return "", fmt.Errorf("openai status %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var sb strings.Builder
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 2*1024*1024)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}

		var chunk openAIStreamChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			continue
		}
		if chunk.Error != nil {
			return "", fmt.Errorf("openai error: %s", chunk.Error.Message)
		}
		if len(chunk.Choices) == 0 {
			continue
		}
		piece := chunk.Choices[0].Delta.Content
		if piece == "" {
			piece = chunk.Choices[0].Message.Content
		}
		if piece == "" {
			continue
		}
		sb.WriteString(piece)
		if onDelta != nil {
			if err := onDelta(piece); err != nil {
				return "", err
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}

	reply := sb.String()
	if strings.TrimSpace(reply) == "" {
		return "", fmt.Errorf("empty llm response")
	}
	b.memory.appendTurn(req.SessionID, userContent, reply)
	return reply, nil
}

const (
	pongWait      = 70 * time.Second
	pingPeriod    = 25 * time.Second
	writeWait     = 10 * time.Second
	maxMessageLen = 1 << 20
	maxQueuedReqs = 32
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func main() {
	port := getEnvInt("PORT", 8090)
	backend := newLLMBackendFromEnv()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status":              "ok",
			"ts_ms":               time.Now().UnixMilli(),
			"llm_model":           backend.model,
			"openai_base_url":     backend.baseURL,
			"has_openai_api_key":  strings.TrimSpace(backend.apiKey) != "",
			"chat_history_limit":  backend.memory.maxMessages,
			"llm_timeout_seconds": int(backend.timeout.Seconds()),
		})
	})
	mux.HandleFunc("/ws/edge", handleEdgeWS(backend))

	addr := ":" + strconv.Itoa(port)
	log.Printf("go-llm-backend listening on %s", addr)
	if err := http.ListenAndServe(addr, withCORS(mux)); err != nil {
		log.Fatalf("listen failed: %v", err)
	}
}

func handleEdgeWS(backend *llmBackend) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			http.Error(w, "upgrade failed", http.StatusBadRequest)
			return
		}
		defer conn.Close()

		var writeMu sync.Mutex

		conn.SetReadLimit(maxMessageLen)
		_ = conn.SetReadDeadline(time.Now().Add(pongWait))
		conn.SetPongHandler(func(string) error {
			_ = conn.SetReadDeadline(time.Now().Add(pongWait))
			return nil
		})

		ctx, cancel := context.WithCancel(r.Context())
		defer cancel()

		reqQueue := make(chan llmRequest, maxQueuedReqs)
		workerDone := make(chan struct{})
		go func() {
			defer close(workerDone)
			for {
				select {
				case <-ctx.Done():
					return
				case req, ok := <-reqQueue:
					if !ok {
						return
					}
					reqCtx, reqCancel := context.WithTimeout(ctx, backend.timeout)
					reply, err := backend.streamReply(reqCtx, req, func(delta string) error {
						return writeJSON(conn, &writeMu, llmResponse{
							Type:      "llm_stream",
							RequestID: req.RequestID,
							SessionID: req.SessionID,
							Emotion:   req.Emotion,
							Event:     req.Event,
							Final:     false,
							Delta:     delta,
							TsMS:      time.Now().UnixMilli(),
						})
					})
					reqCancel()

					if err != nil {
						if err := writeJSON(conn, &writeMu, llmResponse{
							Type:      "llm_error",
							RequestID: req.RequestID,
							SessionID: req.SessionID,
							Emotion:   req.Emotion,
							Event:     req.Event,
							Final:     true,
							Error:     err.Error(),
							TsMS:      time.Now().UnixMilli(),
						}); err != nil {
							cancel()
							return
						}
						continue
					}

					if err := writeJSON(conn, &writeMu, llmResponse{
						Type:      "llm_response",
						RequestID: req.RequestID,
						SessionID: req.SessionID,
						Text:      req.Text,
						Emotion:   req.Emotion,
						Event:     req.Event,
						Final:     true,
						Reply:     reply,
						TsMS:      time.Now().UnixMilli(),
					}); err != nil {
						cancel()
						return
					}
				}
			}
		}()

		go func() {
			ticker := time.NewTicker(pingPeriod)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					writeMu.Lock()
					_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
					err := conn.WriteControl(websocket.PingMessage, []byte("keepalive"), time.Now().Add(writeWait))
					writeMu.Unlock()
					if err != nil {
						cancel()
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}()

	readLoop:
		for {
			_, payload, err := conn.ReadMessage()
			if err != nil {
				cancel()
				break
			}
			var req llmRequest
			if err := json.Unmarshal(payload, &req); err != nil {
				continue
			}
			if req.Type == "" {
				req.Type = "llm_request"
			}
			if req.RequestID == "" {
				req.RequestID = "req-" + strconv.FormatInt(time.Now().UnixMilli(), 10)
			}
			select {
			case reqQueue <- req:
			case <-ctx.Done():
				break readLoop
			default:
				if err := writeJSON(conn, &writeMu, llmResponse{
					Type:      "llm_error",
					RequestID: req.RequestID,
					SessionID: req.SessionID,
					Emotion:   req.Emotion,
					Event:     req.Event,
					Final:     true,
					Error:     "too many pending llm requests",
					TsMS:      time.Now().UnixMilli(),
				}); err != nil {
					cancel()
					break readLoop
				}
			}
		}
		close(reqQueue)
		<-workerDone
	}
}

func writeJSON(conn *websocket.Conn, mu *sync.Mutex, payload any) error {
	mu.Lock()
	defer mu.Unlock()
	_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
	return conn.WriteJSON(payload)
}

func withCORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		w.Header().Set("Access-Control-Allow-Methods", "GET,POST,OPTIONS")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func getEnvInt(key string, fallback int) int {
	v, ok := os.LookupEnv(key)
	if !ok || v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return fallback
	}
	return n
}

func getEnvString(key, fallback string) string {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	return v
}
