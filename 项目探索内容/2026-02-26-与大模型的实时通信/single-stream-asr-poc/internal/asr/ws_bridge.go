package asr

import (
	"encoding/json"
	"fmt"
	"net/url"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

type WSBridgeEngine struct {
	BaseURL string
}

const (
	bridgeDialMaxAttempts = 45
	bridgeDialRetryDelay  = 1 * time.Second
)

func (e *WSBridgeEngine) Name() string {
	return "ws-bridge"
}

func (e *WSBridgeEngine) NewStream(sessionID string, onResult func(Result)) (Stream, error) {
	if e.BaseURL == "" {
		return nil, fmt.Errorf("ASR bridge URL is empty")
	}

	u, err := url.Parse(e.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid ASR bridge URL: %w", err)
	}

	q := u.Query()
	q.Set("session_id", sessionID)
	u.RawQuery = q.Encode()

	var conn *websocket.Conn
	for attempt := 1; attempt <= bridgeDialMaxAttempts; attempt++ {
		conn, _, err = websocket.DefaultDialer.Dial(u.String(), nil)
		if err == nil {
			break
		}
		if attempt < bridgeDialMaxAttempts {
			time.Sleep(bridgeDialRetryDelay)
		}
	}
	if err != nil {
		return nil, fmt.Errorf(
			"connect ASR bridge failed after %d attempts (%s): %w",
			bridgeDialMaxAttempts,
			bridgeDialRetryDelay,
			err,
		)
	}

	s := &wsBridgeStream{
		conn:     conn,
		onResult: onResult,
	}
	go s.readLoop()
	return s, nil
}

type wsBridgeStream struct {
	conn     *websocket.Conn
	onResult func(Result)

	writeMu sync.Mutex
	once    sync.Once
}

func (s *wsBridgeStream) readLoop() {
	for {
		messageType, payload, err := s.conn.ReadMessage()
		if err != nil {
			return
		}
		if messageType != websocket.TextMessage {
			continue
		}
		var result Result
		if err := json.Unmarshal(payload, &result); err != nil {
			continue
		}
		result.Source = "bridge"
		if s.onResult != nil {
			s.onResult(result)
		}
	}
}

func (s *wsBridgeStream) PushAudio(pcm16le []byte) error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.WriteMessage(websocket.BinaryMessage, pcm16le)
}

func (s *wsBridgeStream) Flush() error {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	return s.conn.WriteJSON(map[string]string{"event": "flush"})
}

func (s *wsBridgeStream) Close() error {
	var err error
	s.once.Do(func() {
		s.writeMu.Lock()
		defer s.writeMu.Unlock()
		_ = s.conn.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, "bye"))
		err = s.conn.Close()
	})
	return err
}
