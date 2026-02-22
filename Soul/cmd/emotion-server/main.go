package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/go-chi/chi/v5"

	"soul/internal/emotion"
)

type serverConfig struct {
	HTTPAddr        string
	ReadBodyMaxByte int64
}

type analyzeRequest struct {
	Text string `json:"text"`
}

type convertRequest struct {
	Emotion    string  `json:"emotion"`
	Confidence float64 `json:"confidence"`
}

type emotionResponse struct {
	Emotion   string  `json:"emotion"`
	P         float64 `json:"p"`
	A         float64 `json:"a"`
	D         float64 `json:"d"`
	Intensity float64 `json:"intensity"`
	LatencyMS float64 `json:"latency_ms"`
}

func main() {
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))
	cfg := loadConfig()
	analyzer := emotion.NewAnalyzer()

	r := chi.NewRouter()
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"ok":     true,
			"schema": emotion.Schema,
			"engine": emotion.Engine,
			"labels": emotion.Labels(),
		})
	})
	r.Get("/v1/emotion/pad-table", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]any{
			"schema":    emotion.Schema,
			"pad_table": emotion.PADTable(),
		})
	})
	r.Post("/v1/emotion/analyze", func(w http.ResponseWriter, req *http.Request) {
		var in analyzeRequest
		if err := decodeJSONBody(req, cfg.ReadBodyMaxByte, &in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		in.Text = strings.TrimSpace(in.Text)
		if in.Text == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "text is required"})
			return
		}

		start := time.Now()
		out := analyzer.Analyze(in.Text)
		writeJSON(w, http.StatusOK, toEmotionResponse(out, time.Since(start)))
	})
	r.Post("/v1/emotion/convert", func(w http.ResponseWriter, req *http.Request) {
		var in convertRequest
		if err := decodeJSONBody(req, cfg.ReadBodyMaxByte, &in); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			return
		}
		if strings.TrimSpace(in.Emotion) == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "emotion is required"})
			return
		}
		if math.IsNaN(in.Confidence) || math.IsInf(in.Confidence, 0) {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "confidence must be a finite number"})
			return
		}

		start := time.Now()
		out := analyzer.Convert(in.Emotion, in.Confidence)
		writeJSON(w, http.StatusOK, toEmotionResponse(out, time.Since(start)))
	})

	httpServer := &http.Server{
		Addr:              cfg.HTTPAddr,
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		logger.Info("emotion server started", "addr", cfg.HTTPAddr, "schema", emotion.Schema, "engine", emotion.Engine)
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

func toEmotionResponse(out emotion.Result, cost time.Duration) emotionResponse {
	return emotionResponse{
		Emotion:   out.Emotion,
		P:         out.P,
		A:         out.A,
		D:         out.D,
		Intensity: out.Intensity,
		LatencyMS: roundMillis(cost),
	}
}

func decodeJSONBody(req *http.Request, maxBytes int64, out any) error {
	defer req.Body.Close()
	data, err := io.ReadAll(io.LimitReader(req.Body, maxBytes+1))
	if err != nil {
		return fmt.Errorf("read body: %w", err)
	}
	if int64(len(data)) > maxBytes {
		return fmt.Errorf("request body too large")
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(out); err != nil {
		return fmt.Errorf("invalid json: %w", err)
	}
	var extra any
	if err := dec.Decode(&extra); err != io.EOF {
		if err == nil {
			return fmt.Errorf("invalid json: multiple JSON values")
		}
		return fmt.Errorf("invalid json: %w", err)
	}
	return nil
}

func loadConfig() serverConfig {
	return serverConfig{
		HTTPAddr:        getenvDefault("EMOTION_HTTP_ADDR", ":9012"),
		ReadBodyMaxByte: int64(getenvIntDefault("EMOTION_MAX_BODY_BYTES", 65536)),
	}
}

func getenvDefault(key, val string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return val
}

func getenvIntDefault(key string, val int) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return val
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return val
	}
	return n
}

func roundMillis(d time.Duration) float64 {
	ms := float64(d.Microseconds()) / 1000.0
	return math.Round(ms*1000) / 1000
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
