package chaos

import (
	"strings"
	"testing"
	"time"
)

func TestLatencySample(t *testing.T) {
	r := NewRand(1)
	if d := (Latency{}).Sample(r); d != 0 {
		t.Fatalf("zero median should give no delay, got %s", d)
	}
	if d := (Latency{MedianMs: 100}).Sample(r); d != 100*time.Millisecond {
		t.Fatalf("sigma 0 should be constant, got %s", d)
	}
	l := Latency{MedianMs: 100, Sigma: 1, MaxMs: 250}
	for i := 0; i < 1000; i++ {
		if d := l.Sample(r); d <= 0 || d > 250*time.Millisecond {
			t.Fatalf("sample %d out of bounds: %s", i, d)
		}
	}
}

func TestRandIsDeterministic(t *testing.T) {
	a, b := NewRand(42), NewRand(42)
	for i := 0; i < 100; i++ {
		if a.Float64() != b.Float64() {
			t.Fatal("same seed must give the same sequence")
		}
	}
}

func TestHitRates(t *testing.T) {
	r := NewRand(7)
	if r.Hit(0) || !r.Hit(1) {
		t.Fatal("0 never hits, 1 always hits")
	}
	hits := 0
	for i := 0; i < 10_000; i++ {
		if r.Hit(0.2) {
			hits++
		}
	}
	if hits < 1_800 || hits > 2_200 {
		t.Fatalf("rate 0.2 over 10k draws gave %d hits", hits)
	}
}

func TestDefaultsAreValid(t *testing.T) {
	for name, p := range Defaults() {
		if err := p.Validate(); err != nil {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestParseProfilesRejectsBadRate(t *testing.T) {
	_, err := ParseProfiles([]byte(`[{"name":"x","error":{"rate":1.5}}]`))
	if err == nil || !strings.Contains(err.Error(), "error.rate") {
		t.Fatalf("expected an error.rate validation error, got %v", err)
	}
}

func TestTextRoundTripsThroughWords(t *testing.T) {
	text := Text(NewRand(3), 50)
	if got := strings.Join(Words(text), ""); got != text {
		t.Fatalf("Words must split without loss:\n%q\n%q", text, got)
	}
	if n := len(Words(text)); n != 50 {
		t.Fatalf("expected 50 words, got %d", n)
	}
}
