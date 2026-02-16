package memory

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Mem0Client struct {
	baseURL string
	apiKey  string
	client  *http.Client
}

func NewMem0Client(baseURL, apiKey string, timeout time.Duration) *Mem0Client {
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	return &Mem0Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		apiKey:  apiKey,
		client:  &http.Client{Timeout: timeout},
	}
}

func (m *Mem0Client) Add(ctx context.Context, entry ExternalMemoryEntry) error {
	if strings.TrimSpace(entry.Text) == "" {
		return nil
	}

	payload := map[string]any{
		"messages": []map[string]string{{
			"role":    entry.Role,
			"content": entry.Text,
		}},
		"user_id":  entry.UserID,
		"agent_id": entry.SoulID,
		"run_id":   entry.SessionID,
		"metadata": map[string]any{
			"terminal_id": entry.TerminalID,
		},
	}
	return m.postJSON(ctx, "/memories", payload, nil)
}

func (m *Mem0Client) Search(ctx context.Context, query string, filter ExternalMemoryFilter, topK int) ([]string, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	if topK <= 0 {
		topK = 5
	}

	payload := map[string]any{
		"query":    query,
		"top_k":    topK,
		"user_id":  filter.UserID,
		"agent_id": filter.SoulID,
		"run_id":   filter.SessionID,
		"filters": map[string]any{
			"terminal_id": filter.TerminalID,
		},
	}

	var out map[string]any
	if err := m.postJSON(ctx, "/search", payload, &out); err != nil {
		return nil, err
	}

	return extractMem0Results(out), nil
}

func (m *Mem0Client) postJSON(ctx context.Context, path string, payload any, out any) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	if m.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+m.apiKey)
	}

	resp, err := m.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("mem0 status %d: %s", resp.StatusCode, string(respBody))
	}

	if out == nil || len(respBody) == 0 {
		return nil
	}
	if err := json.Unmarshal(respBody, out); err != nil {
		return err
	}
	return nil
}

func extractMem0Results(out map[string]any) []string {
	candidates := make([]string, 0, 8)

	readItems := func(items []any) {
		for _, item := range items {
			obj, ok := item.(map[string]any)
			if !ok {
				continue
			}
			for _, key := range []string{"memory", "text", "content"} {
				if v, ok := obj[key].(string); ok && strings.TrimSpace(v) != "" {
					candidates = append(candidates, strings.TrimSpace(v))
					break
				}
			}
		}
	}

	for _, key := range []string{"results", "memories", "data"} {
		if arr, ok := out[key].([]any); ok {
			readItems(arr)
		}
	}

	// dedup
	seen := map[string]struct{}{}
	final := make([]string, 0, len(candidates))
	for _, c := range candidates {
		if _, ok := seen[c]; ok {
			continue
		}
		seen[c] = struct{}{}
		final = append(final, c)
	}
	return final
}
