package main

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"soul/internal/config"
	"soul/internal/db"
	"soul/internal/domain"
	"soul/internal/emotion"
	"soul/internal/intent"
	"soul/internal/llm"
	"soul/internal/memory"
	"soul/internal/mqtt"
	"soul/internal/orchestrator"
	"soul/internal/persona"
	"soul/internal/skills"
)

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	cfg, err := config.LoadSoulServerConfig()
	if err != nil {
		logger.Error("load config failed", "error", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	store, err := db.New(ctx, cfg.DBDSN)
	if err != nil {
		logger.Error("connect db failed", "error", err)
		os.Exit(1)
	}
	defer store.Close()

	if err := store.Migrate(ctx); err != nil {
		logger.Error("migrate db failed", "error", err)
		os.Exit(1)
	}

	llmProvider, err := llm.NewProvider(llm.Config{
		Provider:         strings.ToLower(cfg.LLMProvider),
		Model:            cfg.LLMModel,
		OpenAIBaseURL:    cfg.OpenAIBaseURL,
		OpenAIAPIKey:     cfg.OpenAIAPIKey,
		AnthropicBaseURL: cfg.AnthropicBaseURL,
		AnthropicAPIKey:  cfg.AnthropicAPIKey,
	})
	if err != nil {
		logger.Error("init llm provider failed", "error", err)
		os.Exit(1)
	}

	mem0Client := memory.NewMem0Client(cfg.Mem0BaseURL, cfg.Mem0APIKey, cfg.Mem0Timeout)

	memorySvc, err := memory.NewService(store, memory.ServiceConfig{
		LLMProvider:              llmProvider,
		LLMModel:                 cfg.LLMModel,
		Mem0Client:               mem0Client,
		CompressMessageThreshold: cfg.SessionCompressMsgThreshold,
		CompressCharThreshold:    cfg.SessionCompressCharThreshold,
		CompressScanLimit:        cfg.SessionCompressScanLimit,
		IdleTimeout:              cfg.UserIdleTimeout,
		IdleSummaryScanInterval:  cfg.IdleSummaryScanInterval,
		IdleSummaryBatchSize:     50,
		Mem0AsyncQueueEnabled:    cfg.Mem0AsyncQueueEnabled,
	}, logger)
	if err != nil {
		logger.Error("init memory service failed", "error", err)
		os.Exit(1)
	}
	go memorySvc.RunIdleSummaryWorker(ctx)
	logger.Info("session summary worker enabled",
		"idle_timeout", cfg.UserIdleTimeout,
		"scan_interval", cfg.IdleSummaryScanInterval,
		"compress_msg_threshold", cfg.SessionCompressMsgThreshold,
		"compress_char_threshold", cfg.SessionCompressCharThreshold,
		"mem0_async_queue_enabled", cfg.Mem0AsyncQueueEnabled,
	)

	terminalSoulResolver := memory.NewTerminalSoulResolver(cfg.UserID, memorySvc)

	skillRegistry := skills.NewRegistry(cfg.SkillSnapshotTTL)
	mqttHub := mqtt.NewHub(mqtt.HubConfig{
		BrokerURL:   cfg.MQTTBrokerURL,
		ClientID:    cfg.MQTTClientID,
		Username:    cfg.MQTTUsername,
		Password:    cfg.MQTTPassword,
		TopicPrefix: cfg.MQTTTopicPrefix,
	}, skillRegistry, terminalSoulResolver, logger)
	if err := mqttHub.Start(ctx); err != nil {
		logger.Error("start mqtt hub failed", "error", err)
		os.Exit(1)
	}

	emotionClient := emotion.NewClient(cfg.EmotionBaseURL, cfg.EmotionTimeout)
	intentClient := intent.NewClient(cfg.IntentFilterBaseURL, cfg.IntentFilterTimeout)
	personaEngine := persona.NewEngine(persona.DefaultConfig())

	orch := orchestrator.New(orchestrator.Config{
		UserID:           cfg.UserID,
		ChatHistoryLimit: cfg.ChatHistoryLimit,
		ToolTimeout:      cfg.ToolTimeout,
		LLMModel:         cfg.LLMModel,
	}, llmProvider, memorySvc, skillRegistry, mqttHub, emotionClient, intentClient, personaEngine, logger)
	go orch.RunEmotionDecayPublisher(ctx, cfg.EmotionTickInterval)

	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	})
	r.Get("/v1/users", func(w http.ResponseWriter, req *http.Request) {
		items, err := memorySvc.ListUsers(req.Context())
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"items": items,
		})
	})
	r.Post("/v1/users", func(w http.ResponseWriter, req *http.Request) {
		var payload domain.CreateUserPayload
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
			return
		}
		item, err := memorySvc.CreateUser(req.Context(), payload.UserID, payload.DisplayName, payload.Description)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, item)
	})
	r.Get("/v1/souls", func(w http.ResponseWriter, req *http.Request) {
		userID := strings.TrimSpace(req.URL.Query().Get("user_id"))
		if userID == "" {
			userID = cfg.UserID
		}
		items, err := memorySvc.ListSoulProfiles(req.Context(), userID)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"user_id": userID,
			"items":   items,
		})
	})
	r.Post("/v1/souls", func(w http.ResponseWriter, req *http.Request) {
		var payload domain.CreateSoulPayload
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
			return
		}
		userID := strings.TrimSpace(payload.UserID)
		if userID == "" {
			userID = cfg.UserID
		}
		name := strings.TrimSpace(payload.Name)
		mbti := strings.ToUpper(strings.TrimSpace(payload.MBTIType))
		if name == "" || mbti == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "name and mbti_type are required"})
			return
		}
		vector, err := persona.VectorFromMBTI(mbti)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		state := persona.InitialEmotionState(time.Now().UTC())
		profile, err := memorySvc.CreateSoulProfile(req.Context(), userID, name, mbti, vector, state, persona.ModelVersion)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, profile)
	})
	r.Post("/v1/souls/select", func(w http.ResponseWriter, req *http.Request) {
		var payload domain.SelectSoulPayload
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
			return
		}
		userID := strings.TrimSpace(payload.UserID)
		if userID == "" {
			userID = cfg.UserID
		}
		if strings.TrimSpace(payload.TerminalID) == "" || strings.TrimSpace(payload.SoulID) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "terminal_id and soul_id are required"})
			return
		}
		if err := memorySvc.BindTerminalSoul(req.Context(), userID, payload.TerminalID, payload.SoulID); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		skillRegistry.SetSoul(payload.TerminalID, payload.SoulID)
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":          true,
			"user_id":     userID,
			"terminal_id": payload.TerminalID,
			"soul_id":     payload.SoulID,
		})
	})
	r.Get("/v1/souls/{soul_id}/relations", func(w http.ResponseWriter, req *http.Request) {
		soulID := strings.TrimSpace(chi.URLParam(req, "soul_id"))
		if soulID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "soul_id is required"})
			return
		}
		items, err := memorySvc.ListSoulUserRelations(req.Context(), soulID)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"soul_id": soulID,
			"items":   items,
		})
	})
	r.Post("/v1/souls/{soul_id}/relations", func(w http.ResponseWriter, req *http.Request) {
		soulID := strings.TrimSpace(chi.URLParam(req, "soul_id"))
		if soulID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "soul_id is required"})
			return
		}
		var payload domain.CreateSoulUserRelationPayload
		if err := json.NewDecoder(req.Body).Decode(&payload); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
			return
		}
		item, err := memorySvc.CreateSoulUserRelation(req.Context(), soulID, payload)
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, item)
	})
	r.Post("/v1/chat", func(w http.ResponseWriter, req *http.Request) {
		var chatReq domain.ChatRequest
		if err := json.NewDecoder(req.Body).Decode(&chatReq); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
			return
		}
		if chatReq.SessionID == "" || chatReq.TerminalID == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "session_id and terminal_id are required"})
			return
		}
		if len(chatReq.Inputs) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "inputs is required"})
			return
		}
		if !hasKeyboardTextInput(chatReq.Inputs) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "currently only input.type=keyboard_text|speech_text with non-empty text is supported"})
			return
		}

		resp, err := orch.HandleChat(req.Context(), chatReq)
		if err != nil {
			if errors.Is(err, db.ErrSoulSelectionRequired) || errors.Is(err, db.ErrSoulNotFound) {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			logger.Error("chat failed", "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}

		writeJSON(w, http.StatusOK, resp)
	})

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		logger.Info("soul server started", "addr", cfg.HTTPAddr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("http server error", "error", err)
			cancel()
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-sigCh:
		logger.Info("received shutdown signal")
	case <-ctx.Done():
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		logger.Error("http shutdown failed", "error", err)
	}
}

func hasKeyboardTextInput(inputs []domain.ChatInput) bool {
	for _, in := range inputs {
		tp := strings.ToLower(strings.TrimSpace(in.Type))
		if (tp == "keyboard_text" || tp == "speech_text") && strings.TrimSpace(in.Text) != "" {
			return true
		}
	}
	return false
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
