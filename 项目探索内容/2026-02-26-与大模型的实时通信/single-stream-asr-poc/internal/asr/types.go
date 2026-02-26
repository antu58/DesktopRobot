package asr

type Result struct {
	Text    string `json:"text"`
	IsFinal bool   `json:"is_final"`
	Source  string `json:"source,omitempty"`
	Error   string `json:"error,omitempty"`
}

type Stream interface {
	PushAudio(pcm16le []byte) error
	Flush() error
	Close() error
}

type Engine interface {
	Name() string
	NewStream(sessionID string, onResult func(Result)) (Stream, error)
}
