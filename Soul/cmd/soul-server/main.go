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
	"soul/internal/llm"
	"soul/internal/memory"
	"soul/internal/mqtt"
	"soul/internal/orchestrator"
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

	memorySvc, err := memory.NewService(store, memory.ServiceConfig{
		LLMProvider:              llmProvider,
		LLMModel:                 cfg.LLMModel,
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

	orch := orchestrator.New(orchestrator.Config{
		UserID:           cfg.UserID,
		ChatHistoryLimit: cfg.ChatHistoryLimit,
		ToolTimeout:      cfg.ToolTimeout,
		LLMModel:         cfg.LLMModel,
	}, llmProvider, memorySvc, skillRegistry, mqttHub, logger)

	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
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
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "currently only input.type=keyboard_text with non-empty text is supported"})
			return
		}

		resp, err := orch.HandleChat(req.Context(), chatReq)
		if err != nil {
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
		if strings.EqualFold(strings.TrimSpace(in.Type), "keyboard_text") && strings.TrimSpace(in.Text) != "" {
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
