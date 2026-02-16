package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type SoulServerConfig struct {
	HTTPAddr         string
	UserID           string
	DBDSN            string
	MQTTBrokerURL    string
	MQTTClientID     string
	MQTTUsername     string
	MQTTPassword     string
	MQTTTopicPrefix  string
	LLMProvider      string
	LLMModel         string
	OpenAIBaseURL    string
	OpenAIAPIKey     string
	AnthropicBaseURL string
	AnthropicAPIKey  string
	ToolTimeout      time.Duration
	ChatHistoryLimit int
	SkillSnapshotTTL time.Duration
	Mem0BaseURL      string
	Mem0APIKey       string
	Mem0TopK         int
	Mem0Timeout      time.Duration
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
		HTTPAddr:         getenvDefault("SOUL_HTTP_ADDR", ":9010"),
		UserID:           getenvDefault("USER_ID", "demo-user"),
		DBDSN:            os.Getenv("DB_DSN"),
		MQTTBrokerURL:    getenvDefault("MQTT_BROKER_URL", "tcp://localhost:1883"),
		MQTTClientID:     getenvDefault("SOUL_MQTT_CLIENT_ID", "soul-server"),
		MQTTUsername:     os.Getenv("MQTT_USERNAME"),
		MQTTPassword:     os.Getenv("MQTT_PASSWORD"),
		MQTTTopicPrefix:  getenvDefault("MQTT_TOPIC_PREFIX", "soul"),
		LLMProvider:      getenvDefault("LLM_PROVIDER", "openai"),
		LLMModel:         getenvDefault("LLM_MODEL", "gpt-4o-mini"),
		OpenAIBaseURL:    getenvDefault("OPENAI_BASE_URL", "https://api.openai.com/v1"),
		OpenAIAPIKey:     os.Getenv("OPENAI_API_KEY"),
		AnthropicBaseURL: getenvDefault("ANTHROPIC_BASE_URL", "https://api.anthropic.com"),
		AnthropicAPIKey:  os.Getenv("ANTHROPIC_API_KEY"),
		ToolTimeout:      time.Duration(getenvIntDefault("TOOL_TIMEOUT_SECONDS", 8)) * time.Second,
		ChatHistoryLimit: getenvIntDefault("CHAT_HISTORY_LIMIT", 20),
		SkillSnapshotTTL: time.Duration(getenvIntDefault("SKILL_SNAPSHOT_TTL_SECONDS", 60)) * time.Second,
		Mem0BaseURL:      strings.TrimRight(getenvDefault("MEM0_BASE_URL", "http://localhost:8000"), "/"),
		Mem0APIKey:       os.Getenv("MEM0_API_KEY"),
		Mem0TopK:         getenvIntDefault("MEM0_TOP_K", 5),
		Mem0Timeout:      time.Duration(getenvIntDefault("MEM0_TIMEOUT_SECONDS", 5)) * time.Second,
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
	if cfg.Mem0BaseURL == "" {
		return SoulServerConfig{}, fmt.Errorf("MEM0_BASE_URL is required")
	}

	return cfg, nil
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
