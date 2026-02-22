package emotion

import "testing"

func TestConvertSadness(t *testing.T) {
	a := NewAnalyzer()
	got := a.Convert("sadness", 0.91)
	if got.Emotion != "sadness" {
		t.Fatalf("emotion=%s, want sadness", got.Emotion)
	}
	if got.P != -0.65 || got.A != -0.15 || got.D != -0.35 {
		t.Fatalf("pad=(%.2f,%.2f,%.2f), want (-0.65,-0.15,-0.35)", got.P, got.A, got.D)
	}
	if got.Intensity != 0.91 {
		t.Fatalf("intensity=%.2f, want 0.91", got.Intensity)
	}
}

func TestAnalyzeScenarioAnger(t *testing.T) {
	a := NewAnalyzer()
	got := a.Analyze("你个混蛋！")
	if got.Emotion != "anger" {
		t.Fatalf("emotion=%s, want anger", got.Emotion)
	}
}

func TestAnalyzeScenarioFrustration(t *testing.T) {
	a := NewAnalyzer()
	got := a.Analyze("今天被老板批评了")
	if got.Emotion != "frustration" {
		t.Fatalf("emotion=%s, want frustration", got.Emotion)
	}
}
