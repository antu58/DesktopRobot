package emotion

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

func (c *Client) Analyze(ctx context.Context, text string) (domain.EmotionSignal, error) {
	if !c.Enabled() {
		return domain.EmotionSignal{}, fmt.Errorf("emotion service is not configured")
	}
	payload := map[string]string{"text": strings.TrimSpace(text)}
	body, _ := json.Marshal(payload)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/emotion/analyze", bytes.NewReader(body))
	if err != nil {
		return domain.EmotionSignal{}, err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return domain.EmotionSignal{}, err
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return domain.EmotionSignal{}, fmt.Errorf("emotion service status=%d body=%s", resp.StatusCode, strings.TrimSpace(string(respBody)))
	}

	var out struct {
		Emotion   string  `json:"emotion"`
		P         float64 `json:"p"`
		A         float64 `json:"a"`
		D         float64 `json:"d"`
		Intensity float64 `json:"intensity"`
	}
	if err := json.Unmarshal(respBody, &out); err != nil {
		return domain.EmotionSignal{}, err
	}
	return domain.EmotionSignal{
		Emotion:    out.Emotion,
		P:          out.P,
		A:          out.A,
		D:          out.D,
		Intensity:  out.Intensity,
		Confidence: out.Intensity,
	}, nil
}
