// Package chaos describes fault profiles shared by the chaos provider and the
// chaos-monkey plugin: latency distributions, error injection, stream
// truncation. Every random draw goes through a seeded Rand so a campaign can
// be replayed exactly.
package chaos

import (
	"encoding/json"
	"fmt"
	"math"
	"math/rand"
	"sync"
	"time"
)

// Latency is a log-normal distribution expressed by its median and the sigma
// of the underlying normal. sigma 0 gives a constant delay, 0.5 a moderately
// spread one, 1 a long tail. A zero Median means no delay at all.
type Latency struct {
	MedianMs float64 `json:"median_ms"`
	Sigma    float64 `json:"sigma,omitempty"`
	MaxMs    float64 `json:"max_ms,omitempty"`
}

// Sample draws a delay from the distribution.
func (l Latency) Sample(r *Rand) time.Duration {
	if l.MedianMs <= 0 {
		return 0
	}
	ms := l.MedianMs
	if l.Sigma > 0 {
		ms = l.MedianMs * math.Exp(r.NormFloat64()*l.Sigma)
	}
	if l.MaxMs > 0 && ms > l.MaxMs {
		ms = l.MaxMs
	}
	return time.Duration(ms * float64(time.Millisecond))
}

// Range is an inclusive integer interval.
type Range struct {
	Min int `json:"min"`
	Max int `json:"max"`
}

// Sample draws a value in [Min, Max]. Max below Min is clamped to Min.
func (rg Range) Sample(r *Rand) int {
	if rg.Max <= rg.Min {
		return rg.Min
	}
	return rg.Min + r.Intn(rg.Max-rg.Min+1)
}

// ErrorInjection returns an HTTP error before any content is produced.
type ErrorInjection struct {
	// Rate is the probability, in [0, 1], that a request fails.
	Rate float64 `json:"rate"`
	// Statuses lists the candidate status codes, drawn uniformly. Empty means 503.
	Statuses []int `json:"statuses,omitempty"`
	// RetryAfterS, when > 0, adds a Retry-After header on 429 and 503.
	RetryAfterS int `json:"retry_after_s,omitempty"`
}

// Hangup closes the TCP connection without finishing the response.
type Hangup struct {
	Rate float64 `json:"rate"`
	// AfterChunks is the number of SSE chunks written before the connection is
	// dropped. 0 drops it before any byte, which the client sees as an empty
	// reply; in non-streaming mode the connection is always dropped before
	// the body.
	AfterChunks Range `json:"after_chunks"`
}

// Profile is the full behaviour of one chaos model.
type Profile struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`

	// TTFT is the delay before the first byte of the response (time to first
	// token). In non-streaming mode it adds up with the generation time.
	TTFT Latency `json:"ttft"`
	// TokensPerSec paces content generation. 0 means instantaneous.
	TokensPerSec float64 `json:"tokens_per_sec"`
	// CompletionTokens is the number of words produced.
	CompletionTokens Range `json:"completion_tokens"`

	Error  ErrorInjection `json:"error"`
	Hangup Hangup         `json:"hangup"`
	// Stall, when true, accepts the request and never answers, until the client
	// gives up. It exercises every timeout on the path.
	Stall bool `json:"stall,omitempty"`
	// GarbageRate is the probability of a malformed response: a truncated JSON
	// body or an SSE event that is not JSON, in the middle of the stream.
	GarbageRate float64 `json:"garbage_rate,omitempty"`
}

// Validate reports the first inconsistency in the profile.
func (p Profile) Validate() error {
	if p.Name == "" {
		return fmt.Errorf("profile without name")
	}
	for _, rate := range []struct {
		name string
		v    float64
	}{{"error.rate", p.Error.Rate}, {"hangup.rate", p.Hangup.Rate}, {"garbage_rate", p.GarbageRate}} {
		if rate.v < 0 || rate.v > 1 {
			return fmt.Errorf("profile %s: %s must be within [0, 1], got %v", p.Name, rate.name, rate.v)
		}
	}
	for _, s := range p.Error.Statuses {
		if s < 400 || s > 599 {
			return fmt.Errorf("profile %s: error status %d is not an HTTP error", p.Name, s)
		}
	}
	if p.TokensPerSec < 0 || p.TTFT.MedianMs < 0 || p.TTFT.Sigma < 0 {
		return fmt.Errorf("profile %s: negative timing", p.Name)
	}
	return nil
}

// Defaults returns the built-in profiles, keyed by name. The chaos provider
// serves model "chaos-<name>" with profile <name>.
func Defaults() map[string]Profile {
	list := []Profile{
		{
			Name:             "fast",
			Description:      "Near-immediate response, realistic usage. Baseline.",
			TTFT:             Latency{MedianMs: 20, Sigma: 0.3},
			TokensPerSec:     0,
			CompletionTokens: Range{Min: 40, Max: 120},
		},
		{
			Name:             "slow",
			Description:      "800 ms TTFT with a long tail, 30 tokens/s. A heavy model on a busy day.",
			TTFT:             Latency{MedianMs: 800, Sigma: 0.6, MaxMs: 10_000},
			TokensPerSec:     30,
			CompletionTokens: Range{Min: 80, Max: 300},
		},
		{
			Name:             "flaky",
			Description:      "15% of 429 and 503 with Retry-After, otherwise like fast.",
			TTFT:             Latency{MedianMs: 50, Sigma: 0.3},
			CompletionTokens: Range{Min: 40, Max: 120},
			Error:            ErrorInjection{Rate: 0.15, Statuses: []int{429, 503, 500}, RetryAfterS: 2},
		},
		{
			Name:             "hangup",
			Description:      "One response in three cut mid-stream, after 3 to 20 chunks.",
			TTFT:             Latency{MedianMs: 100, Sigma: 0.3},
			TokensPerSec:     80,
			CompletionTokens: Range{Min: 60, Max: 200},
			Hangup:           Hangup{Rate: 0.33, AfterChunks: Range{Min: 3, Max: 20}},
		},
		{
			Name:             "stall",
			Description:      "Accepts the request and never answers. Tests timeouts.",
			Stall:            true,
			CompletionTokens: Range{Min: 1, Max: 1},
		},
		{
			Name:             "garbage",
			Description:      "One response in four malformed: truncated JSON or unreadable SSE event.",
			TTFT:             Latency{MedianMs: 50, Sigma: 0.3},
			TokensPerSec:     100,
			CompletionTokens: Range{Min: 40, Max: 120},
			GarbageRate:      0.25,
		},
		{
			Name:             "mixed",
			Description:      "A bit of everything: variable latency, 5% errors, 3% cut streams, 2% malformed responses.",
			TTFT:             Latency{MedianMs: 300, Sigma: 0.8, MaxMs: 8_000},
			TokensPerSec:     60,
			CompletionTokens: Range{Min: 30, Max: 250},
			Error:            ErrorInjection{Rate: 0.05, Statuses: []int{429, 500, 502, 503}, RetryAfterS: 1},
			Hangup:           Hangup{Rate: 0.03, AfterChunks: Range{Min: 1, Max: 30}},
			GarbageRate:      0.02,
		},
	}
	out := make(map[string]Profile, len(list))
	for _, p := range list {
		out[p.Name] = p
	}
	return out
}

// ParseProfiles decodes a JSON list of profiles and validates each one.
func ParseProfiles(data []byte) (map[string]Profile, error) {
	var list []Profile
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("decode profiles: %w", err)
	}
	out := make(map[string]Profile, len(list))
	for _, p := range list {
		if err := p.Validate(); err != nil {
			return nil, err
		}
		out[p.Name] = p
	}
	return out, nil
}

// Rand is a seeded, goroutine-safe random source.
type Rand struct {
	mu sync.Mutex
	r  *rand.Rand
}

// NewRand returns a source seeded with seed.
func NewRand(seed int64) *Rand {
	return &Rand{r: rand.New(rand.NewSource(seed))}
}

// Float64 returns a value in [0, 1).
func (r *Rand) Float64() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.r.Float64()
}

// Intn returns a value in [0, n).
func (r *Rand) Intn(n int) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.r.Intn(n)
}

// NormFloat64 returns a standard normal draw.
func (r *Rand) NormFloat64() float64 {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.r.NormFloat64()
}

// Hit reports whether an event with probability rate occurs.
func (r *Rand) Hit(rate float64) bool {
	if rate <= 0 {
		return false
	}
	if rate >= 1 {
		return true
	}
	return r.Float64() < rate
}

// Pick returns one element of values, or fallback when values is empty.
func (r *Rand) Pick(values []int, fallback int) int {
	if len(values) == 0 {
		return fallback
	}
	return values[r.Intn(len(values))]
}
