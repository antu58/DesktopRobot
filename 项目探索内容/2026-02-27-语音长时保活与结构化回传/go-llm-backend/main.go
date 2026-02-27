package main

import (
	"encoding/json"
	"log"
	"net/http"
	"os"
	"strconv"
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
	Text      string `json:"text"`
	Emotion   string `json:"emotion"`
	Event     string `json:"event"`
	Final     bool   `json:"final"`
	Reply     string `json:"reply"`
	TsMS      int64  `json:"ts_ms"`
}

const (
	pongWait      = 70 * time.Second
	pingPeriod    = 25 * time.Second
	writeWait     = 10 * time.Second
	maxMessageLen = 1 << 20
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool { return true },
}

func main() {
	port := getEnvInt("PORT", 8090)

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"status": "ok",
			"ts_ms":  time.Now().UnixMilli(),
		})
	})
	mux.HandleFunc("/ws/edge", handleEdgeWS)

	addr := ":" + strconv.Itoa(port)
	log.Printf("go-llm-backend listening on %s", addr)
	if err := http.ListenAndServe(addr, withCORS(mux)); err != nil {
		log.Fatalf("listen failed: %v", err)
	}
}

func handleEdgeWS(w http.ResponseWriter, r *http.Request) {
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

	stopPing := make(chan struct{})
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
					return
				}
			case <-stopPing:
				return
			}
		}
	}()
	defer close(stopPing)

	for {
		_, payload, err := conn.ReadMessage()
		if err != nil {
			return
		}
		var req llmRequest
		if err := json.Unmarshal(payload, &req); err != nil {
			continue
		}
		if req.Type == "" {
			req.Type = "llm_request"
		}
		if req.RequestID == "" {
			req.RequestID = "missing-request-id"
		}

		reply := req.Text + " [LLM_STUB emo=" + req.Emotion + " event=" + req.Event + " final=" + strconv.FormatBool(req.Final) + "]"
		resp := llmResponse{
			Type:      "llm_response",
			RequestID: req.RequestID,
			SessionID: req.SessionID,
			Text:      req.Text,
			Emotion:   req.Emotion,
			Event:     req.Event,
			Final:     req.Final,
			Reply:     reply,
			TsMS:      time.Now().UnixMilli(),
		}

		writeMu.Lock()
		_ = conn.SetWriteDeadline(time.Now().Add(writeWait))
		err = conn.WriteJSON(resp)
		writeMu.Unlock()
		if err != nil {
			return
		}
	}
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
