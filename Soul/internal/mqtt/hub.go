package mqtt

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	paho "github.com/eclipse/paho.mqtt.golang"
	"github.com/google/uuid"

	"soul/internal/domain"
	"soul/internal/skills"
)

type HubConfig struct {
	BrokerURL   string
	ClientID    string
	Username    string
	Password    string
	TopicPrefix string
}

type Hub struct {
	cfg          HubConfig
	client       paho.Client
	registry     *skills.Registry
	soulResolver SoulResolver
	logger       *slog.Logger

	pendingMu sync.Mutex
	pending   map[string]chan domain.InvokeResult
}

type SoulResolver interface {
	ResolveOrCreateSoul(ctx context.Context, terminalID, soulHint string) (string, error)
}

func NewHub(cfg HubConfig, registry *skills.Registry, soulResolver SoulResolver, logger *slog.Logger) *Hub {
	return &Hub{
		cfg:          cfg,
		registry:     registry,
		soulResolver: soulResolver,
		logger:       logger,
		pending:      make(map[string]chan domain.InvokeResult),
	}
}

func (h *Hub) Start(ctx context.Context) error {
	opts := paho.NewClientOptions().
		AddBroker(h.cfg.BrokerURL).
		SetClientID(h.cfg.ClientID).
		SetAutoReconnect(true).
		SetConnectRetry(true)

	if h.cfg.Username != "" {
		opts.SetUsername(h.cfg.Username)
		opts.SetPassword(h.cfg.Password)
	}

	opts.SetConnectionLostHandler(func(_ paho.Client, err error) {
		h.logger.Error("mqtt connection lost", "error", err)
	})

	h.client = paho.NewClient(opts)
	if token := h.client.Connect(); token.Wait() && token.Error() != nil {
		return token.Error()
	}

	if err := h.subscribeHandlers(); err != nil {
		return err
	}

	go func() {
		<-ctx.Done()
		h.client.Disconnect(100)
	}()

	return nil
}

func (h *Hub) subscribeHandlers() error {
	if token := h.client.Subscribe(TopicTerminalSkills(h.cfg.TopicPrefix), 1, h.handleSkillReport); token.Wait() && token.Error() != nil {
		return token.Error()
	}
	if token := h.client.Subscribe(TopicTerminalOnline(h.cfg.TopicPrefix), 1, h.handleOnline); token.Wait() && token.Error() != nil {
		return token.Error()
	}
	if token := h.client.Subscribe(TopicTerminalHeartbeat(h.cfg.TopicPrefix), 1, h.handleHeartbeat); token.Wait() && token.Error() != nil {
		return token.Error()
	}
	if token := h.client.Subscribe(TopicTerminalResult(h.cfg.TopicPrefix), 1, h.handleInvokeResult); token.Wait() && token.Error() != nil {
		return token.Error()
	}
	return nil
}

func (h *Hub) handleSkillReport(_ paho.Client, msg paho.Message) {
	terminalID, err := ParseTerminalID(msg.Topic(), h.cfg.TopicPrefix)
	if err != nil {
		h.logger.Warn("skip invalid skill topic", "topic", msg.Topic(), "error", err)
		return
	}

	var report domain.SkillReport
	if err := json.Unmarshal(msg.Payload(), &report); err != nil {
		// backward compatible: payload can be an array
		var skillsOnly []domain.SkillDefinition
		if err2 := json.Unmarshal(msg.Payload(), &skillsOnly); err2 != nil {
			h.logger.Warn("invalid skill payload", "terminal_id", terminalID, "error", err)
			return
		}
		report = domain.SkillReport{TerminalID: terminalID, Skills: skillsOnly, SkillVersion: 0}
	}
	if report.TerminalID == "" {
		report.TerminalID = terminalID
	}
	if report.TerminalID != terminalID {
		h.logger.Warn("skill report terminal mismatch", "topic_terminal", terminalID, "payload_terminal", report.TerminalID)
		return
	}

	soulID := ""
	if h.soulResolver != nil {
		resolved, resolveErr := h.soulResolver.ResolveOrCreateSoul(context.Background(), terminalID, report.SoulHint)
		if resolveErr != nil {
			h.logger.Warn("resolve soul failed when skill report", "terminal_id", terminalID, "error", resolveErr)
		} else {
			soulID = resolved
		}
	}

	h.registry.SetSkills(terminalID, soulID, report.SkillVersion, report.Skills)
	h.registry.SetOnline(terminalID, true)
	state, _ := h.registry.GetState(terminalID)
	h.logger.Info("skills updated", "terminal_id", terminalID, "soul_id", soulID, "skill_version", state.SkillVersion, "skill_count", len(report.Skills))
}

func (h *Hub) handleOnline(_ paho.Client, msg paho.Message) {
	terminalID, err := ParseTerminalID(msg.Topic(), h.cfg.TopicPrefix)
	if err != nil {
		h.logger.Warn("skip invalid online topic", "topic", msg.Topic(), "error", err)
		return
	}

	payload := strings.TrimSpace(strings.ToLower(string(msg.Payload())))
	online := payload == "1" || payload == "true" || payload == "online"
	if online && h.soulResolver != nil {
		soulID, resolveErr := h.soulResolver.ResolveOrCreateSoul(context.Background(), terminalID, "")
		if resolveErr != nil {
			h.logger.Warn("resolve soul failed when terminal online", "terminal_id", terminalID, "error", resolveErr)
		} else {
			h.registry.SetSoul(terminalID, soulID)
		}
	}
	h.registry.SetOnline(terminalID, online)
	h.logger.Info("terminal online status", "terminal_id", terminalID, "online", online)
}

func (h *Hub) handleHeartbeat(_ paho.Client, msg paho.Message) {
	terminalID, err := ParseTerminalID(msg.Topic(), h.cfg.TopicPrefix)
	if err != nil {
		h.logger.Warn("skip invalid heartbeat topic", "topic", msg.Topic(), "error", err)
		return
	}
	h.registry.SetOnline(terminalID, true)
}

func (h *Hub) handleInvokeResult(_ paho.Client, msg paho.Message) {
	requestID := ParseRequestID(msg.Topic())
	if requestID == "" {
		return
	}

	var result domain.InvokeResult
	if err := json.Unmarshal(msg.Payload(), &result); err != nil {
		h.logger.Warn("invalid invoke result", "topic", msg.Topic(), "error", err)
		return
	}
	if result.RequestID == "" {
		result.RequestID = requestID
	}

	h.pendingMu.Lock()
	ch, ok := h.pending[result.RequestID]
	h.pendingMu.Unlock()
	if !ok {
		return
	}

	select {
	case ch <- result:
	default:
	}
}

func (h *Hub) InvokeSkill(ctx context.Context, terminalID, skill string, args json.RawMessage) (domain.InvokeResult, error) {
	if len(args) == 0 {
		args = json.RawMessage(`{}`)
	}

	requestID := uuid.NewString()
	payload := domain.InvokeRequest{
		RequestID: requestID,
		Skill:     skill,
		Arguments: args,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return domain.InvokeResult{}, err
	}

	resultCh := make(chan domain.InvokeResult, 1)
	h.pendingMu.Lock()
	h.pending[requestID] = resultCh
	h.pendingMu.Unlock()
	defer func() {
		h.pendingMu.Lock()
		delete(h.pending, requestID)
		h.pendingMu.Unlock()
	}()

	topic := TopicInvoke(h.cfg.TopicPrefix, terminalID, requestID)
	if token := h.client.Publish(topic, 1, false, body); token.Wait() && token.Error() != nil {
		return domain.InvokeResult{}, token.Error()
	}

	select {
	case <-ctx.Done():
		return domain.InvokeResult{}, ctx.Err()
	case result := <-resultCh:
		if !result.OK {
			if result.Error == "" {
				result.Error = "tool invocation failed"
			}
			return result, fmt.Errorf("%s", result.Error)
		}
		return result, nil
	case <-time.After(20 * time.Second):
		return domain.InvokeResult{}, fmt.Errorf("tool timeout")
	}
}
