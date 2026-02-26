package asr

import (
	"fmt"
	"sync"
)

const sampleRate = 16000

type MockEngine struct{}

func (m *MockEngine) Name() string {
	return "mock"
}

func (m *MockEngine) NewStream(_ string, onResult func(Result)) (Stream, error) {
	return &mockStream{
		onResult: onResult,
	}, nil
}

type mockStream struct {
	mu           sync.Mutex
	onResult     func(Result)
	closed       bool
	sampleCount  int
	segmentIndex int
}

func (s *mockStream) PushAudio(pcm16le []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}

	s.sampleCount += len(pcm16le) / 2
	for s.sampleCount >= sampleRate {
		s.sampleCount -= sampleRate
		s.segmentIndex++
		if s.onResult != nil {
			s.onResult(Result{
				Text:    fmt.Sprintf("mock 识别片段 %d（请切换真实 ASR）", s.segmentIndex),
				IsFinal: false,
				Source:  "mock",
			})
		}
	}
	return nil
}

func (s *mockStream) Flush() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	if s.onResult != nil {
		s.onResult(Result{
			Text:    "mock 会话结束",
			IsFinal: true,
			Source:  "mock",
		})
	}
	return nil
}

func (s *mockStream) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}
