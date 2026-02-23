package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

const (
	pcmSampleRate        = 16000
	pcmChannels          = 1
	vadEnergyThreshold   = 0.012
	vadMinSpeechSamples  = 3200  // 200ms
	vadSilenceSamplesEnd = 11200 // 700ms
	vadMaxSegmentSamples = 240000
)

type config struct {
	ListenAddr  string
	BaseURL     string
	APIKey      string
	ASRBaseURL  string
	ASRAPIKey   string
	Model       string
	ASRModel    string
	ASRLanguage string
	VADUDPAddr  string
}

type chatRequest struct {
	Message string `json:"message"`
}

type openAIStreamRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Stream      bool            `json:"stream"`
	Temperature float64         `json:"temperature,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
}

type openAIMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type openAIChunk struct {
	Choices []struct {
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
	} `json:"error,omitempty"`
}

type openAITranscriptionResp struct {
	Text string `json:"text"`
}

type voiceClientCommand struct {
	Type string `json:"type"`
}

type voiceSession struct {
	ws        *websocket.Conn
	sendMu    sync.Mutex
	segmentCh chan []byte
	done      chan struct{}
	once      sync.Once
}

func (s *voiceSession) close() {
	s.once.Do(func() {
		close(s.done)
	})
}

type voiceHub struct {
	mu       sync.RWMutex
	sessions map[string]*voiceSession
}

func newVoiceHub() *voiceHub {
	return &voiceHub{sessions: make(map[string]*voiceSession)}
}

func (h *voiceHub) register(sessionID string, ws *websocket.Conn) *voiceSession {
	s := &voiceSession{
		ws:        ws,
		segmentCh: make(chan []byte, 8),
		done:      make(chan struct{}),
	}
	h.mu.Lock()
	h.sessions[sessionID] = s
	h.mu.Unlock()
	return s
}

func (h *voiceHub) unregister(sessionID string) {
	h.mu.Lock()
	s := h.sessions[sessionID]
	delete(h.sessions, sessionID)
	h.mu.Unlock()
	if s != nil {
		s.close()
	}
}

func (h *voiceHub) deliver(sessionID string, pcm []byte) {
	h.mu.RLock()
	s := h.sessions[sessionID]
	h.mu.RUnlock()
	if s == nil {
		return
	}

	select {
	case <-s.done:
		return
	default:
	}

	data := append([]byte(nil), pcm...)
	select {
	case s.segmentCh <- data:
	default:
		select {
		case <-s.segmentCh:
		default:
		}
		select {
		case s.segmentCh <- data:
		default:
		}
	}
}

type vadSessionState struct {
	speaking       bool
	speechSamples  int
	silenceSamples int
	buffer         []byte
}

type vadUDPService struct {
	conn     *net.UDPConn
	logger   *slog.Logger
	mu       sync.Mutex
	sessions map[string]*vadSessionState
}

func newVADUDPService(addr string, logger *slog.Logger) (*vadUDPService, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		return nil, err
	}
	return &vadUDPService{
		conn:     conn,
		logger:   logger,
		sessions: make(map[string]*vadSessionState),
	}, nil
}

func (v *vadUDPService) Run(ctx context.Context, onSegment func(sessionID string, pcm []byte)) {
	buf := make([]byte, 65535)
	for {
		if err := v.conn.SetReadDeadline(time.Now().Add(1 * time.Second)); err != nil {
			v.logger.Warn("set udp read deadline failed", "error", err)
		}
		n, _, err := v.conn.ReadFromUDP(buf)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				select {
				case <-ctx.Done():
					return
				default:
					continue
				}
			}
			if errors.Is(err, net.ErrClosed) || ctx.Err() != nil {
				return
			}
			v.logger.Warn("udp read failed", "error", err)
			continue
		}
		sessionID, pcm, ok := unpackUDPPacket(buf[:n])
		if !ok || len(pcm) == 0 {
			continue
		}
		v.processAudio(sessionID, pcm, onSegment)
	}
}

func (v *vadUDPService) Reset(sessionID string) {
	v.mu.Lock()
	delete(v.sessions, sessionID)
	v.mu.Unlock()
}

func (v *vadUDPService) Flush(sessionID string, onSegment func(sessionID string, pcm []byte)) {
	var out []byte
	v.mu.Lock()
	if st, ok := v.sessions[sessionID]; ok {
		if st.speechSamples >= vadMinSpeechSamples && len(st.buffer) > 0 {
			out = append([]byte(nil), st.buffer...)
		}
		delete(v.sessions, sessionID)
	}
	v.mu.Unlock()

	if len(out) > 0 {
		onSegment(sessionID, out)
	}
}

func (v *vadUDPService) processAudio(sessionID string, pcm []byte, onSegment func(sessionID string, pcm []byte)) {
	if len(pcm)%2 != 0 {
		pcm = pcm[:len(pcm)-1]
	}
	if len(pcm) == 0 {
		return
	}

	isSpeech, samples := estimateSpeech(pcm)
	if samples == 0 {
		return
	}

	var emit []byte

	v.mu.Lock()
	st, ok := v.sessions[sessionID]
	if !ok {
		st = &vadSessionState{}
		v.sessions[sessionID] = st
	}

	if isSpeech {
		st.speaking = true
		st.silenceSamples = 0
		st.speechSamples += samples
		st.buffer = append(st.buffer, pcm...)
	} else if st.speaking {
		st.silenceSamples += samples
		st.buffer = append(st.buffer, pcm...)
	}

	if st.speaking {
		if st.speechSamples >= vadMaxSegmentSamples {
			if st.speechSamples >= vadMinSpeechSamples {
				emit = append([]byte(nil), st.buffer...)
			}
			delete(v.sessions, sessionID)
		} else if st.silenceSamples >= vadSilenceSamplesEnd {
			if st.speechSamples >= vadMinSpeechSamples {
				emit = append([]byte(nil), st.buffer...)
			}
			delete(v.sessions, sessionID)
		}
	}
	v.mu.Unlock()

	if len(emit) > 0 {
		onSegment(sessionID, emit)
	}
}

func estimateSpeech(pcm []byte) (bool, int) {
	samples := len(pcm) / 2
	if samples == 0 {
		return false, 0
	}
	var sum float64
	for i := 0; i < len(pcm); i += 2 {
		v := int16(binary.LittleEndian.Uint16(pcm[i : i+2]))
		n := float64(v) / 32768.0
		sum += n * n
	}
	rms := sum / float64(samples)
	return rms >= vadEnergyThreshold, samples
}

type appServer struct {
	cfg          config
	logger       *slog.Logger
	streamClient *http.Client
	apiClient    *http.Client
	wsUpgrader   websocket.Upgrader
	vad          *vadUDPService
	voiceHub     *voiceHub
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	loadEnvFiles(".env", "Soul/.env")
	cfg, err := loadConfig()
	if err != nil {
		logger.Error("load config failed", "error", err)
		os.Exit(1)
	}

	vad, err := newVADUDPService(cfg.VADUDPAddr, logger)
	if err != nil {
		logger.Error("start vad udp service failed", "error", err)
		os.Exit(1)
	}

	srv := &appServer{
		cfg:          cfg,
		logger:       logger,
		streamClient: &http.Client{Timeout: 0},
		apiClient:    &http.Client{Timeout: 60 * time.Second},
		wsUpgrader: websocket.Upgrader{
			ReadBufferSize:  4096,
			WriteBufferSize: 4096,
			CheckOrigin: func(_ *http.Request) bool {
				return true
			},
		},
		vad:      vad,
		voiceHub: newVoiceHub(),
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go srv.vad.Run(ctx, srv.voiceHub.deliver)

	mux := http.NewServeMux()
	mux.HandleFunc("/", indexHandler)
	mux.HandleFunc("/api/chat/stream", srv.streamHandler)
	mux.HandleFunc("/ws/voice", srv.voiceWSHandler)
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})

	server := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           withCORS(mux),
		ReadHeaderTimeout: 5 * time.Second,
	}

	logger.Info("llm stream test server ready",
		"listen", cfg.ListenAddr,
		"llm_base_url", cfg.BaseURL,
		"model", cfg.Model,
		"asr_base_url", cfg.ASRBaseURL,
		"asr_model", cfg.ASRModel,
		"vad_udp_addr", cfg.VADUDPAddr,
	)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		logger.Error("server failed", "error", err)
		os.Exit(1)
	}
}

func loadConfig() (config, error) {
	baseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("OPENAI_BASE_URL")), "/")
	if baseURL == "" {
		baseURL = "https://api.openai.com/v1"
	}

	apiKey := strings.TrimSpace(os.Getenv("OPENAI_API_KEY"))
	if apiKey == "" {
		return config{}, fmt.Errorf("OPENAI_API_KEY is required")
	}

	asrBaseURL := strings.TrimRight(strings.TrimSpace(os.Getenv("ASR_BASE_URL")), "/")
	if asrBaseURL == "" {
		asrBaseURL = baseURL
	}
	asrAPIKey := strings.TrimSpace(os.Getenv("ASR_API_KEY"))
	if asrAPIKey == "" {
		asrAPIKey = apiKey
	}

	model := strings.TrimSpace(os.Getenv("LLM_MODEL"))
	if model == "" {
		model = strings.TrimSpace(os.Getenv("MODEL"))
	}
	if model == "" {
		model = "gpt-4o-mini"
	}

	listenAddr := strings.TrimSpace(os.Getenv("TEST_CHAT_ADDR"))
	if listenAddr == "" {
		listenAddr = ":9014"
	}

	asrModel := strings.TrimSpace(os.Getenv("ASR_MODEL"))
	if asrModel == "" {
		asrModel = "whisper-1"
	}

	asrLanguage := strings.TrimSpace(os.Getenv("ASR_LANGUAGE"))
	if asrLanguage == "" {
		asrLanguage = "zh"
	}

	vadUDPAddr := strings.TrimSpace(os.Getenv("VAD_UDP_ADDR"))
	if vadUDPAddr == "" {
		vadUDPAddr = "127.0.0.1:19090"
	}

	return config{
		ListenAddr:  listenAddr,
		BaseURL:     baseURL,
		APIKey:      apiKey,
		ASRBaseURL:  asrBaseURL,
		ASRAPIKey:   asrAPIKey,
		Model:       model,
		ASRModel:    asrModel,
		ASRLanguage: asrLanguage,
		VADUDPAddr:  vadUDPAddr,
	}, nil
}

func (s *appServer) streamHandler(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var in chatRequest
	if err := json.NewDecoder(req.Body).Decode(&in); err != nil {
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	message := strings.TrimSpace(in.Message)
	if message == "" {
		http.Error(w, "message is required", http.StatusBadRequest)
		return
	}

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	_, _, err := s.streamLLM(req.Context(), message, func(chunk string) error {
		if _, werr := w.Write([]byte(chunk)); werr != nil {
			return werr
		}
		flusher.Flush()
		return nil
	})
	if err != nil {
		s.logger.Warn("text stream failed", "error", err)
		return
	}
}

func (s *appServer) voiceWSHandler(w http.ResponseWriter, req *http.Request) {
	ws, err := s.wsUpgrader.Upgrade(w, req, nil)
	if err != nil {
		s.logger.Warn("upgrade websocket failed", "error", err)
		return
	}

	sessionID := uuid.NewString()
	voiceSession := s.voiceHub.register(sessionID, ws)
	defer func() {
		s.vad.Reset(sessionID)
		s.voiceHub.unregister(sessionID)
		_ = ws.Close()
	}()

	udpConn, err := net.Dial("udp", s.cfg.VADUDPAddr)
	if err != nil {
		_ = s.sendVoiceEvent(voiceSession, map[string]any{"type": "error", "message": "connect udp vad failed: " + err.Error()})
		return
	}
	defer udpConn.Close()

	ctx, cancel := context.WithCancel(req.Context())
	defer cancel()

	go s.consumeVoiceSegments(ctx, sessionID, voiceSession)

	_ = s.sendVoiceEvent(voiceSession, map[string]any{
		"type":        "ready",
		"session_id":  sessionID,
		"vad_udp":     s.cfg.VADUDPAddr,
		"sample_rate": pcmSampleRate,
	})

	for {
		msgType, payload, err := ws.ReadMessage()
		if err != nil {
			s.logger.Info("voice websocket closed", "session_id", sessionID)
			return
		}

		switch msgType {
		case websocket.BinaryMessage:
			packet := buildUDPPacket(sessionID, payload)
			if _, err := udpConn.Write(packet); err != nil {
				_ = s.sendVoiceEvent(voiceSession, map[string]any{"type": "error", "message": "udp send failed: " + err.Error()})
				return
			}
		case websocket.TextMessage:
			var cmd voiceClientCommand
			if err := json.Unmarshal(payload, &cmd); err != nil {
				continue
			}
			switch strings.ToLower(strings.TrimSpace(cmd.Type)) {
			case "start":
				s.vad.Reset(sessionID)
				_ = s.sendVoiceEvent(voiceSession, map[string]any{"type": "status", "message": "listening"})
			case "stop":
				s.vad.Flush(sessionID, s.voiceHub.deliver)
				_ = s.sendVoiceEvent(voiceSession, map[string]any{"type": "status", "message": "vad_flush"})
			}
		}
	}
}

func (s *appServer) consumeVoiceSegments(ctx context.Context, sessionID string, vs *voiceSession) {
	for {
		select {
		case <-ctx.Done():
			return
		case <-vs.done:
			return
		case pcm := <-vs.segmentCh:
			_ = s.sendVoiceEvent(vs, map[string]any{"type": "status", "message": "transcribing"})
			text, err := s.transcribePCM(ctx, pcm)
			if err != nil {
				_ = s.sendVoiceEvent(vs, map[string]any{"type": "error", "message": "asr failed: " + err.Error()})
				continue
			}
			text = strings.TrimSpace(text)
			if text == "" {
				_ = s.sendVoiceEvent(vs, map[string]any{"type": "status", "message": "empty_transcript"})
				continue
			}

			_ = s.sendVoiceEvent(vs, map[string]any{"type": "transcription", "text": text})
			_ = s.sendVoiceEvent(vs, map[string]any{"type": "llm_start"})

			ttft, total, err := s.streamLLM(ctx, text, func(chunk string) error {
				return s.sendVoiceEvent(vs, map[string]any{"type": "llm_chunk", "text": chunk})
			})
			if err != nil {
				_ = s.sendVoiceEvent(vs, map[string]any{"type": "error", "message": "llm failed: " + err.Error()})
				continue
			}

			_ = s.sendVoiceEvent(vs, map[string]any{
				"type":     "llm_done",
				"ttft_ms":  ttft.Milliseconds(),
				"total_ms": total.Milliseconds(),
			})
			_ = s.sendVoiceEvent(vs, map[string]any{"type": "status", "message": "idle"})
		}
	}
}

func (s *appServer) sendVoiceEvent(vs *voiceSession, payload any) error {
	select {
	case <-vs.done:
		return context.Canceled
	default:
	}
	vs.sendMu.Lock()
	defer vs.sendMu.Unlock()
	return vs.ws.WriteJSON(payload)
}

func (s *appServer) transcribePCM(ctx context.Context, pcm []byte) (string, error) {
	if len(pcm) == 0 {
		return "", fmt.Errorf("empty pcm")
	}

	wav := pcmToWAV(pcm, pcmSampleRate, pcmChannels)

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	fw, err := mw.CreateFormFile("file", "speech.wav")
	if err != nil {
		return "", err
	}
	if _, err := fw.Write(wav); err != nil {
		return "", err
	}
	if err := mw.WriteField("model", s.cfg.ASRModel); err != nil {
		return "", err
	}
	if s.cfg.ASRLanguage != "" {
		if err := mw.WriteField("language", s.cfg.ASRLanguage); err != nil {
			return "", err
		}
	}
	if err := mw.Close(); err != nil {
		return "", err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.ASRBaseURL+"/audio/transcriptions", &body)
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Authorization", "Bearer "+s.cfg.ASRAPIKey)
	httpReq.Header.Set("Content-Type", mw.FormDataContentType())

	resp, err := s.apiClient.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if resp.StatusCode >= 300 {
		return "", fmt.Errorf("status %d: %s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var parsed openAITranscriptionResp
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return "", fmt.Errorf("decode asr response failed: %w", err)
	}
	return parsed.Text, nil
}

func (s *appServer) streamLLM(ctx context.Context, input string, onChunk func(chunk string) error) (time.Duration, time.Duration, error) {
	payload := openAIStreamRequest{
		Model: s.cfg.Model,
		Messages: []openAIMessage{
			{Role: "user", Content: input},
		},
		Stream:      true,
		Temperature: 0.2,
		MaxTokens:   220,
	}
	buf, err := json.Marshal(payload)
	if err != nil {
		return 0, 0, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.cfg.BaseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return 0, 0, err
	}
	httpReq.Header.Set("Authorization", "Bearer "+s.cfg.APIKey)
	httpReq.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := s.streamClient.Do(httpReq)
	if err != nil {
		return 0, 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return 0, 0, fmt.Errorf("upstream status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	reader := bufio.NewReader(resp.Body)
	var ttft time.Duration
	first := false

	for {
		line, rerr := reader.ReadString('\n')
		if line != "" {
			if !strings.HasPrefix(line, "data:") {
				if rerr == nil {
					continue
				}
			} else {
				data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				if data == "[DONE]" {
					return ttft, time.Since(start), nil
				}

				var chunk openAIChunk
				if json.Unmarshal([]byte(data), &chunk) == nil {
					if chunk.Error != nil {
						return ttft, time.Since(start), fmt.Errorf(chunk.Error.Message)
					}
					if len(chunk.Choices) > 0 {
						text := chunk.Choices[0].Delta.Content
						if text == "" {
							text = chunk.Choices[0].Message.Content
						}
						if text != "" {
							if !first {
								first = true
								ttft = time.Since(start)
							}
							if err := onChunk(text); err != nil {
								return ttft, time.Since(start), err
							}
						}
					}
				}
			}
		}

		if rerr != nil {
			if errors.Is(rerr, io.EOF) || errors.Is(rerr, context.Canceled) || errors.Is(ctx.Err(), context.Canceled) {
				return ttft, time.Since(start), nil
			}
			return ttft, time.Since(start), rerr
		}
	}
}

func buildUDPPacket(sessionID string, pcm []byte) []byte {
	out := make([]byte, len(sessionID)+1+len(pcm))
	copy(out, sessionID)
	out[len(sessionID)] = '\n'
	copy(out[len(sessionID)+1:], pcm)
	return out
}

func unpackUDPPacket(packet []byte) (string, []byte, bool) {
	idx := bytes.IndexByte(packet, '\n')
	if idx <= 0 || idx >= len(packet)-1 {
		return "", nil, false
	}
	sid := strings.TrimSpace(string(packet[:idx]))
	if sid == "" {
		return "", nil, false
	}
	return sid, packet[idx+1:], true
}

func pcmToWAV(pcm []byte, sampleRate, channels int) []byte {
	if sampleRate <= 0 {
		sampleRate = 16000
	}
	if channels <= 0 {
		channels = 1
	}
	bitsPerSample := 16
	byteRate := sampleRate * channels * bitsPerSample / 8
	blockAlign := channels * bitsPerSample / 8
	dataLen := len(pcm)

	buf := &bytes.Buffer{}
	_, _ = buf.WriteString("RIFF")
	_ = binary.Write(buf, binary.LittleEndian, uint32(36+dataLen))
	_, _ = buf.WriteString("WAVE")
	_, _ = buf.WriteString("fmt ")
	_ = binary.Write(buf, binary.LittleEndian, uint32(16))
	_ = binary.Write(buf, binary.LittleEndian, uint16(1))
	_ = binary.Write(buf, binary.LittleEndian, uint16(channels))
	_ = binary.Write(buf, binary.LittleEndian, uint32(sampleRate))
	_ = binary.Write(buf, binary.LittleEndian, uint32(byteRate))
	_ = binary.Write(buf, binary.LittleEndian, uint16(blockAlign))
	_ = binary.Write(buf, binary.LittleEndian, uint16(bitsPerSample))
	_, _ = buf.WriteString("data")
	_ = binary.Write(buf, binary.LittleEndian, uint32(dataLen))
	_, _ = buf.Write(pcm)
	return buf.Bytes()
}

func indexHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(indexHTML))
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

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func loadEnvFiles(paths ...string) {
	for _, p := range paths {
		path := p
		if !filepath.IsAbs(path) {
			cwd, err := os.Getwd()
			if err == nil {
				path = filepath.Join(cwd, path)
			}
		}
		content, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		lines := strings.Split(string(content), "\n")
		for _, rawLine := range lines {
			line := strings.TrimSpace(rawLine)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			line = strings.TrimPrefix(line, "export ")
			parts := strings.SplitN(line, "=", 2)
			if len(parts) != 2 {
				continue
			}
			key := strings.TrimSpace(parts[0])
			if key == "" {
				continue
			}
			if os.Getenv(key) != "" {
				continue
			}
			val := strings.TrimSpace(parts[1])
			if len(val) >= 2 {
				if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
					val = val[1 : len(val)-1]
				}
			}
			_ = os.Setenv(key, val)
		}
	}
}

const indexHTML = `<!doctype html>
<html lang="zh-CN">
<head>
  <meta charset="utf-8" />
  <meta name="viewport" content="width=device-width, initial-scale=1" />
  <title>LLM 流式聊天 + 语音 VAD</title>
  <style>
    :root {
      --bg: #f4f7fb;
      --panel: #ffffff;
      --ink: #111827;
      --muted: #6b7280;
      --line: #e5e7eb;
      --brand: #0f766e;
      --danger: #b91c1c;
      --ok: #047857;
    }
    * { box-sizing: border-box; }
    body {
      margin: 0;
      font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif;
      background: radial-gradient(circle at 20% -10%, #d9f3ef, transparent 40%), var(--bg);
      color: var(--ink);
      min-height: 100vh;
      padding: 20px;
      display: flex;
      justify-content: center;
    }
    .app {
      width: 100%;
      max-width: 980px;
      background: var(--panel);
      border: 1px solid var(--line);
      border-radius: 14px;
      box-shadow: 0 10px 28px rgba(17, 24, 39, 0.06);
      overflow: hidden;
    }
    .header {
      padding: 14px 18px;
      border-bottom: 1px solid var(--line);
      font-weight: 600;
      display: flex;
      justify-content: space-between;
      align-items: center;
      gap: 12px;
      flex-wrap: wrap;
    }
    .meta { color: var(--muted); font-size: 13px; }
    .main { padding: 16px; display: grid; gap: 14px; }
    .card {
      border: 1px solid var(--line);
      border-radius: 12px;
      padding: 12px;
      background: #fcfdff;
    }
    .card h3 {
      margin: 0 0 8px;
      font-size: 14px;
      color: #0b3b37;
    }
    textarea {
      width: 100%;
      min-height: 96px;
      resize: vertical;
      border: 1px solid var(--line);
      border-radius: 10px;
      padding: 10px;
      font-size: 15px;
      line-height: 1.45;
      color: var(--ink);
      outline: none;
      margin-bottom: 10px;
      background: #fff;
    }
    textarea:focus {
      border-color: var(--brand);
      box-shadow: 0 0 0 3px rgba(15, 118, 110, 0.12);
    }
    .actions { display: flex; gap: 10px; align-items: center; margin-bottom: 8px; flex-wrap: wrap; }
    button {
      border: 0;
      background: var(--brand);
      color: #fff;
      border-radius: 8px;
      padding: 9px 14px;
      cursor: pointer;
      font-size: 14px;
    }
    button:disabled { background: #94a3b8; cursor: not-allowed; }
    .status { color: var(--muted); font-size: 13px; }
    .status.ok { color: var(--ok); }
    .status.err { color: var(--danger); }
    .out {
      white-space: pre-wrap;
      border: 1px solid var(--line);
      background: #fff;
      border-radius: 10px;
      min-height: 140px;
      padding: 10px;
      line-height: 1.5;
      font-size: 14px;
      overflow: auto;
    }
    .voice-tip {
      padding: 10px;
      border-radius: 8px;
      background: #effaf7;
      border: 1px solid #cceee7;
      font-size: 13px;
      color: #0f4f49;
      margin-bottom: 8px;
    }
    .badge {
      display: inline-block;
      padding: 2px 8px;
      border-radius: 999px;
      font-size: 12px;
      background: #ecfeff;
      color: #155e75;
      border: 1px solid #bae6fd;
    }
    .badge.live {
      background: #fef2f2;
      color: #991b1b;
      border-color: #fecaca;
    }
  </style>
</head>
<body>
  <div class="app">
    <div class="header">
      <div>LLM 流式聊天 + 语音输入（空格按住说话）</div>
      <div class="meta" id="meta">等待发送</div>
    </div>

    <div class="main">
      <div class="card">
        <h3>文本模式</h3>
        <textarea id="prompt" placeholder="输入一句话，点击发送。每次请求都是单轮，不带历史上下文。"></textarea>
        <div class="actions">
          <button id="send">发送并流式接收</button>
          <span class="status">快捷键: Ctrl/Cmd + Enter</span>
        </div>
        <div id="out" class="out"></div>
      </div>

      <div class="card">
        <h3>语音模式 <span id="voiceBadge" class="badge">idle</span></h3>
        <div class="voice-tip">
          按住 <b>空格</b> 开始说话，松开空格结束。使用浏览器 Web Speech API 做语音转文本，再请求后端 LLM 流式回复。
        </div>
        <div class="actions">
          <span id="voiceStatus" class="status">语音识别未初始化</span>
        </div>
        <div id="voiceOut" class="out"></div>
      </div>
    </div>
  </div>

  <script>
    const promptEl = document.getElementById("prompt");
    const sendBtn = document.getElementById("send");
    const outEl = document.getElementById("out");
    const metaEl = document.getElementById("meta");

    const voiceOutEl = document.getElementById("voiceOut");
    const voiceStatusEl = document.getElementById("voiceStatus");
    const voiceBadgeEl = document.getElementById("voiceBadge");

    const SpeechRecognition = window.SpeechRecognition || window.webkitSpeechRecognition;
    let recognition = null;
    let voiceSpeaking = false;
    let voiceBusy = false;
    let finalTranscript = "";
    let interimTranscript = "";

    function setVoiceStatus(text, kind) {
      voiceStatusEl.textContent = text;
      voiceStatusEl.classList.remove("ok", "err");
      if (kind === "ok") voiceStatusEl.classList.add("ok");
      if (kind === "err") voiceStatusEl.classList.add("err");
    }

    function appendVoiceRaw(text) {
      voiceOutEl.textContent += text;
      voiceOutEl.scrollTop = voiceOutEl.scrollHeight;
    }

    function appendVoiceLine(text) {
      appendVoiceRaw(text + "\n");
    }

    function initSpeechRecognition() {
      if (!SpeechRecognition) {
        setVoiceStatus("当前浏览器不支持 Web Speech API（建议使用 Chrome）", "err");
        return false;
      }
      if (recognition) return true;

      recognition = new SpeechRecognition();
      recognition.lang = "zh-CN";
      recognition.interimResults = true;
      recognition.continuous = true;
      recognition.maxAlternatives = 1;

      recognition.onstart = () => {
        voiceSpeaking = true;
        voiceBadgeEl.textContent = "recording";
        voiceBadgeEl.classList.add("live");
        setVoiceStatus("语音识别中... 松开空格结束", "ok");
      };

      recognition.onresult = (event) => {
        let partial = "";
        for (let i = event.resultIndex; i < event.results.length; i++) {
          const text = (event.results[i][0] && event.results[i][0].transcript) || "";
          if (event.results[i].isFinal) {
            finalTranscript += text;
          } else {
            partial += text;
          }
        }
        interimTranscript = partial;
        const preview = (finalTranscript + interimTranscript).trim();
        if (preview) {
          setVoiceStatus("识别中: " + preview.slice(0, 30), "ok");
        }
      };

      recognition.onerror = (event) => {
        voiceSpeaking = false;
        voiceBadgeEl.textContent = "idle";
        voiceBadgeEl.classList.remove("live");
        setVoiceStatus("语音识别错误: " + (event.error || "unknown"), "err");
      };

      recognition.onend = async () => {
        voiceSpeaking = false;
        voiceBadgeEl.textContent = "idle";
        voiceBadgeEl.classList.remove("live");

        const transcript = (finalTranscript + " " + interimTranscript).trim();
        finalTranscript = "";
        interimTranscript = "";

        if (!transcript) {
          setVoiceStatus("未识别到有效语音", "err");
          return;
        }
        setVoiceStatus("识别完成，开始请求 LLM...", "ok");
        await runVoiceChat(transcript);
      };

      setVoiceStatus("Web Speech API 已就绪，按住空格说话", "ok");
      return true;
    }

    async function streamLLM(message, onChunk) {
      const resp = await fetch("/api/chat/stream", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ message })
      });
      if (!resp.ok || !resp.body) {
        const text = await resp.text();
        throw new Error(text || "请求失败");
      }

      const started = performance.now();
      let firstTokenAt = null;
      const reader = resp.body.getReader();
      const decoder = new TextDecoder();

      while (true) {
        const { done, value } = await reader.read();
        if (done) break;
        const chunk = decoder.decode(value, { stream: true });
        if (chunk) {
          if (firstTokenAt === null) firstTokenAt = performance.now();
          onChunk(chunk);
        }
      }

      const finished = performance.now();
      return {
        ttftMs: firstTokenAt === null ? null : Math.round(firstTokenAt - started),
        totalMs: Math.round(finished - started)
      };
    }

    async function runTextChat() {
      const message = promptEl.value.trim();
      if (!message) return;

      sendBtn.disabled = true;
      outEl.textContent = "";
      metaEl.textContent = "请求中...";

      try {
        const timing = await streamLLM(message, (chunk) => {
          outEl.textContent += chunk;
          outEl.scrollTop = outEl.scrollHeight;
        });
        const ttft = timing.ttftMs == null ? "-" : timing.ttftMs + "ms";
        metaEl.textContent = "首字延迟: " + ttft + " | 总耗时: " + timing.totalMs + "ms";
      } catch (err) {
        metaEl.textContent = "请求失败";
        outEl.textContent = String(err);
      } finally {
        sendBtn.disabled = false;
      }
    }

    async function runVoiceChat(transcript) {
      if (voiceBusy) return;
      voiceBusy = true;
      appendVoiceLine("你: " + transcript);
      appendVoiceRaw("助手: ");

      try {
        const timing = await streamLLM(transcript, (chunk) => {
          appendVoiceRaw(chunk);
        });
        appendVoiceLine("");
        const ttft = timing.ttftMs == null ? "-" : timing.ttftMs + "ms";
        appendVoiceLine("[耗时] 首字 " + ttft + " | 总计 " + timing.totalMs + "ms");
        setVoiceStatus("就绪（按住空格继续说话）", "ok");
      } catch (err) {
        appendVoiceLine("");
        appendVoiceLine("[错误] " + String(err));
        setVoiceStatus(String(err), "err");
      } finally {
        voiceBusy = false;
      }
    }

    async function startVoiceCapture() {
      if (voiceBusy || voiceSpeaking) return;
      if (!initSpeechRecognition()) return;
      finalTranscript = "";
      interimTranscript = "";
      try {
        recognition.start();
      } catch (err) {
        setVoiceStatus("语音识别启动失败: " + String(err), "err");
      }
    }

    function stopVoiceCapture() {
      if (!recognition || !voiceSpeaking) return;
      voiceBadgeEl.textContent = "processing";
      setVoiceStatus("已停止录音，等待识别完成...", "ok");
      try {
        recognition.stop();
      } catch (err) {
        setVoiceStatus("语音识别停止失败: " + String(err), "err");
      }
    }

    sendBtn.addEventListener("click", runTextChat);
    promptEl.addEventListener("keydown", (e) => {
      if ((e.ctrlKey || e.metaKey) && e.key === "Enter") runTextChat();
    });

    document.addEventListener("keydown", async (e) => {
      if (e.code !== "Space" || e.repeat) return;
      const tag = (document.activeElement && document.activeElement.tagName || "").toLowerCase();
      if (tag === "textarea" || tag === "input") return;
      e.preventDefault();
      await startVoiceCapture();
    });

    document.addEventListener("keyup", (e) => {
      if (e.code !== "Space") return;
      const tag = (document.activeElement && document.activeElement.tagName || "").toLowerCase();
      if (tag === "textarea" || tag === "input") return;
      e.preventDefault();
      stopVoiceCapture();
    });

    initSpeechRecognition();
  </script>
</body>
</html>`
