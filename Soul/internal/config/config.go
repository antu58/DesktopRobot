package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type SoulServerConfig struct {
	HTTPAddr                     string
	UserID                       string
	DBDSN                        string
	MQTTBrokerURL                string
	MQTTClientID                 string
	MQTTUsername                 string
	MQTTPassword                 string
	MQTTTopicPrefix              string
	LLMProvider                  string
	LLMModel                     string
	OpenAIBaseURL                string
	OpenAIAPIKey                 string
	AnthropicBaseURL             string
	AnthropicAPIKey              string
	ToolTimeout                  time.Duration
	ChatHistoryLimit             int
	SkillSnapshotTTL             time.Duration
	UserIdleTimeout              time.Duration
	IdleSummaryScanInterval      time.Duration
	SessionCompressMsgThreshold  int
	SessionCompressCharThreshold int
	SessionCompressScanLimit     int
	Mem0BaseURL                  string
	Mem0APIKey                   string
	Mem0Timeout                  time.Duration
	Mem0AsyncQueueEnabled        bool
	EmotionBaseURL               string
	EmotionTimeout               time.Duration
	IntentFilterBaseURL          string
	IntentFilterTimeout          time.Duration
	EmotionTickInterval          time.Duration
}

type TerminalWebConfig struct {
	HTTPAddr          string
	TerminalID        string
	SoulHint          string
	SkillVersion      int64
	HeartbeatInterval time.Duration
	MQTTBrokerURL     string
	MQTTClientID      string
	MQTTUsername      string
	MQTTPassword      string
	MQTTTopicPrefix   string
	SoulAPIBaseURL    string
	UserID            string
}

func LoadSoulServerConfig() (SoulServerConfig, error) {
	cfg := SoulServerConfig{
		HTTPAddr:                     getenvDefault("SOUL_HTTP_ADDR", ":9010"),
		UserID:                       getenvDefault("USER_ID", "demo-user"),
		DBDSN:                        os.Getenv("DB_DSN"),
		MQTTBrokerURL:                getenvDefault("MQTT_BROKER_URL", "tcp://localhost:1883"),
		MQTTClientID:                 getenvDefault("SOUL_MQTT_CLIENT_ID", "soul-server"),
		MQTTUsername:                 os.Getenv("MQTT_USERNAME"),
		MQTTPassword:                 os.Getenv("MQTT_PASSWORD"),
		MQTTTopicPrefix:              getenvDefault("MQTT_TOPIC_PREFIX", "soul"),
		LLMProvider:                  getenvDefault("LLM_PROVIDER", "openai"),
		LLMModel:                     getenvDefault("LLM_MODEL", "gpt-4o-mini"),
		OpenAIBaseURL:                getenvDefault("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		OpenAIAPIKey:                 os.Getenv("OPENAI_API_KEY"),
		AnthropicBaseURL:             getenvDefault("ANTHROPIC_BASE_URL", "https://api.anthropic.com"),
		AnthropicAPIKey:              os.Getenv("ANTHROPIC_API_KEY"),
		ToolTimeout:                  time.Duration(getenvIntDefault("TOOL_TIMEOUT_SECONDS", 8)) * time.Second,
		ChatHistoryLimit:             getenvIntDefault("CHAT_HISTORY_LIMIT", 20),
		SkillSnapshotTTL:             time.Duration(getenvIntDefault("SKILL_SNAPSHOT_TTL_SECONDS", 60)) * time.Second,
		UserIdleTimeout:              time.Duration(getenvIntDefault("USER_IDLE_TIMEOUT_SECONDS", 180)) * time.Second,
		IdleSummaryScanInterval:      time.Duration(getenvIntDefault("IDLE_SUMMARY_SCAN_INTERVAL_SECONDS", 15)) * time.Second,
		SessionCompressMsgThreshold:  getenvIntDefault("SESSION_COMPRESS_MSG_THRESHOLD", 80),
		SessionCompressCharThreshold: getenvIntDefault("SESSION_COMPRESS_CHAR_THRESHOLD", 12000),
		SessionCompressScanLimit:     getenvIntDefault("SESSION_COMPRESS_SCAN_LIMIT", 200),
		Mem0BaseURL:                  strings.TrimRight(getenvDefault("MEM0_BASE_URL", "http://localhost:8000"), "/"),
		Mem0APIKey:                   os.Getenv("MEM0_API_KEY"),
		Mem0Timeout:                  time.Duration(getenvIntDefault("MEM0_TIMEOUT_SECONDS", 5)) * time.Second,
		Mem0AsyncQueueEnabled:        getenvBoolDefault("MEM0_ASYNC_QUEUE_ENABLED", true),
		EmotionBaseURL:               strings.TrimRight(getenvDefault("EMOTION_BASE_URL", "http://localhost:9012"), "/"),
		EmotionTimeout:               time.Duration(getenvIntDefault("EMOTION_TIMEOUT_MS", 1500)) * time.Millisecond,
		IntentFilterBaseURL:          strings.TrimRight(getenvDefault("INTENT_FILTER_BASE_URL", "http://localhost:9013"), "/"),
		IntentFilterTimeout:          time.Duration(getenvIntDefault("INTENT_FILTER_TIMEOUT_MS", 1500)) * time.Millisecond,
		EmotionTickInterval:          time.Duration(clampInt(getenvIntDefault("EMOTION_TICK_INTERVAL_SECONDS", 3), 2, 5)) * time.Second,
	}

	if cfg.DBDSN == "" {
		return SoulServerConfig{}, fmt.Errorf("DB_DSN is required")
	}

	if cfg.LLMProvider == "openai" && cfg.OpenAIAPIKey == "" {
		return SoulServerConfig{}, fmt.Errorf("OPENAI_API_KEY is required when LLM_PROVIDER=openai")
	}
	if cfg.LLMProvider == "claude" && cfg.AnthropicAPIKey == "" {
		return SoulServerConfig{}, fmt.Errorf("ANTHROPIC_API_KEY is required when LLM_PROVIDER=claude")
	}
	return cfg, nil
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func LoadTerminalWebConfig() TerminalWebConfig {
	return TerminalWebConfig{
		HTTPAddr:          getenvDefault("TERMINAL_WEB_HTTP_ADDR", ":9011"),
		TerminalID:        getenvDefault("TERMINAL_ID", "terminal-debug-01"),
		SoulHint:          os.Getenv("TERMINAL_SOUL_HINT"),
		SkillVersion:      getenvInt64Default("TERMINAL_SKILL_VERSION", 1),
		HeartbeatInterval: time.Duration(getenvIntDefault("TERMINAL_HEARTBEAT_INTERVAL_SECONDS", 10)) * time.Second,
		MQTTBrokerURL:     getenvDefault("MQTT_BROKER_URL", "tcp://localhost:1883"),
		MQTTClientID:      getenvDefault("TERMINAL_MQTT_CLIENT_ID", "terminal-web-debug"),
		MQTTUsername:      os.Getenv("MQTT_USERNAME"),
		MQTTPassword:      os.Getenv("MQTT_PASSWORD"),
		MQTTTopicPrefix:   getenvDefault("MQTT_TOPIC_PREFIX", "soul"),
		SoulAPIBaseURL:    getenvDefault("SOUL_API_BASE_URL", "http://localhost:9010"),
		UserID:            getenvDefault("USER_ID", "demo-user"),
	}
}

func getenvDefault(key, val string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return val
}

func getenvIntDefault(key string, val int) int {
	v := os.Getenv(key)
	if v == "" {
		return val
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return val
	}
	return n
}

func getenvInt64Default(key string, val int64) int64 {
	v := os.Getenv(key)
	if v == "" {
		return val
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return val
	}
	return n
}

func getenvBoolDefault(key string, val bool) bool {
	v := strings.TrimSpace(strings.ToLower(os.Getenv(key)))
	if v == "" {
		return val
	}
	switch v {
	case "1", "true", "yes", "y", "on":
		return true
	case "0", "false", "no", "n", "off":
		return false
	default:
		return val
	}
}
