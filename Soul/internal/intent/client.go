package intent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"soul/internal/domain"
)

type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(baseURL string, timeout time.Duration) *Client {
	if timeout <= 0 {
		timeout = 1500 * time.Millisecond
	}
	return &Client{
		baseURL: strings.TrimRight(strings.TrimSpace(baseURL), "/"),
		http:    &http.Client{Timeout: timeout},
	}
}

func (c *Client) Enabled() bool {
	return c != nil && c.baseURL != ""
}

func DefaultOptions() domain.IntentFilterOptions {
	return domain.IntentFilterOptions{
		AllowMultiIntent:          true,
		MaxIntents:                8,
		MaxIntentsPerSegment:      2,
		MinConfidence:             0.35,
		EnableTimeParser:          true,
		ReturnDebugCandidates:     false,
		ReturnDebugEntities:       false,
		EmitSystemIntentWhenEmpty: true,
	}
}

func (c *Client) Filter(ctx context.Context, req domain.IntentFilterRequest) (domain.IntentFilterResponse, error) {
	if !c.Enabled() {
		return domain.IntentFilterResponse{}, fmt.Errorf("intent filter service is not configured")
	}
	if len(req.IntentCatalog) == 0 {
		return domain.IntentFilterResponse{}, fmt.Errorf("intent catalog is empty")
	}
	body, _ := json.Marshal(req)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/intents/filter", bytes.NewReader(body))
	if err != nil {
		return domain.IntentFilterResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(httpReq)
	if err != nil {
		return domain.IntentFilterResponse{}, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return domain.IntentFilterResponse{}, fmt.Errorf("intent filter status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var out domain.IntentFilterResponse
	if err := json.Unmarshal(respBody, &out); err != nil {
		return domain.IntentFilterResponse{}, err
	}
	return out, nil
}
