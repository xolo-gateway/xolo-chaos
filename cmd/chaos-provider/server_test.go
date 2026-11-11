package main

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/xolo-gateway/xolo-chaos/internal/chaos"
)

func newTestServer(t *testing.T, profiles ...chaos.Profile) (*httptest.Server, *Server) {
	t.Helper()
	m := map[string]chaos.Profile{}
	for _, p := range profiles {
		m[p.Name] = p
	}
	s := NewServer(1, m)
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	return ts, s
}

func post(t *testing.T, url, model string, stream bool) (*http.Response, error) {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"model":  model,
		"stream": stream,
		"messages": []map[string]any{
			{"role": "system", "content": "Tu es un assistant."},
			{"role": "user", "content": "Bonjour, comment vas-tu ?"},
		},
	})
	req, _ := http.NewRequest(http.MethodPost, url+"/v1/chat/completions", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	return http.DefaultClient.Do(req)
}

// readSSE returns the decoded chunks and whether [DONE] was seen.
func readSSE(t *testing.T, r io.Reader) (chunks []map[string]any, done bool, malformed int) {
	t.Helper()
	sc := bufio.NewScanner(r)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		payload := strings.TrimPrefix(line, "data: ")
		if payload == "[DONE]" {
			done = true
			continue
		}
		var c map[string]any
		if err := json.Unmarshal([]byte(payload), &c); err != nil {
			malformed++
			continue
		}
		chunks = append(chunks, c)
	}
	return chunks, done, malformed
}

func TestFastProfile_NonStreaming(t *testing.T) {
	ts, _ := newTestServer(t, chaos.Profile{Name: "fast", CompletionTokens: chaos.Range{Min: 10, Max: 10}})
	res, err := post(t, ts.URL, "chaos-fast", false)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status %d", res.StatusCode)
	}
	var body struct {
		Choices []struct {
			Message struct{ Content string }
		}
		Usage usage
	}
	if err := json.NewDecoder(res.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if got := len(strings.Fields(body.Choices[0].Message.Content)); got != 10 {
		t.Fatalf("expected 10 words, got %d", got)
	}
	if body.Usage.CompletionTokens != 10 || body.Usage.PromptTokens == 0 || body.Usage.TotalTokens != body.Usage.PromptTokens+10 {
		t.Fatalf("unexpected usage %+v", body.Usage)
	}
}

func TestFastProfile_Streaming(t *testing.T) {
	ts, _ := newTestServer(t, chaos.Profile{Name: "fast", CompletionTokens: chaos.Range{Min: 5, Max: 5}})
	res, err := post(t, ts.URL, "chaos-fast", true)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if ct := res.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("content-type %q", ct)
	}
	chunks, done, malformed := readSSE(t, res.Body)
	if !done || malformed != 0 {
		t.Fatalf("done=%v malformed=%d", done, malformed)
	}
	// role + 5 words + finish + usage
	if len(chunks) != 8 {
		t.Fatalf("expected 8 chunks, got %d", len(chunks))
	}
	last := chunks[len(chunks)-1]
	if u, ok := last["usage"].(map[string]any); !ok || u["completion_tokens"].(float64) != 5 {
		t.Fatalf("last chunk should carry usage, got %v", last)
	}
	var text strings.Builder
	for _, c := range chunks[:len(chunks)-1] {
		delta := c["choices"].([]any)[0].(map[string]any)["delta"].(map[string]any)
		if s, ok := delta["content"].(string); ok {
			text.WriteString(s)
		}
	}
	if got := len(strings.Fields(text.String())); got != 5 {
		t.Fatalf("reassembled %d words: %q", got, text.String())
	}
}

func TestUnknownModel(t *testing.T) {
	ts, _ := newTestServer(t)
	res, err := post(t, ts.URL, "gpt-4o", false)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d", res.StatusCode)
	}
}

func TestErrorInjection(t *testing.T) {
	ts, _ := newTestServer(t, chaos.Profile{
		Name:             "flaky",
		CompletionTokens: chaos.Range{Min: 1, Max: 1},
		Error:            chaos.ErrorInjection{Rate: 1, Statuses: []int{429}, RetryAfterS: 3},
	})
	res, err := post(t, ts.URL, "chaos-flaky", false)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status %d", res.StatusCode)
	}
	if ra := res.Header.Get("Retry-After"); ra != "3" {
		t.Fatalf("Retry-After %q", ra)
	}
	var body map[string]apiError
	json.NewDecoder(res.Body).Decode(&body)
	if body["error"].Type != "rate_limit_error" {
		t.Fatalf("error body %+v", body)
	}
}

func TestHangup_MidStream(t *testing.T) {
	ts, _ := newTestServer(t, chaos.Profile{
		Name:             "hangup",
		CompletionTokens: chaos.Range{Min: 50, Max: 50},
		Hangup:           chaos.Hangup{Rate: 1, AfterChunks: chaos.Range{Min: 4, Max: 4}},
	})
	res, err := post(t, ts.URL, "chaos-hangup", true)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	data, err := io.ReadAll(res.Body)
	if err == nil {
		t.Fatalf("expected a transport error, got a clean body of %d bytes", len(data))
	}
	chunks, done, _ := readSSE(t, bytes.NewReader(data))
	if done || len(chunks) != 4 {
		t.Fatalf("expected exactly 4 chunks then a cut, got %d chunks, done=%v", len(chunks), done)
	}
}

func TestHangup_NonStreaming(t *testing.T) {
	ts, _ := newTestServer(t, chaos.Profile{
		Name:             "hangup",
		CompletionTokens: chaos.Range{Min: 1, Max: 1},
		Hangup:           chaos.Hangup{Rate: 1},
	})
	_, err := post(t, ts.URL, "chaos-hangup", false)
	if err == nil {
		t.Fatal("expected a transport error")
	}
}

func TestStall_RespectsClientTimeout(t *testing.T) {
	ts, _ := newTestServer(t, chaos.Profile{Name: "stall", Stall: true, CompletionTokens: chaos.Range{Min: 1, Max: 1}})
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	req, _ := http.NewRequestWithContext(ctx, http.MethodPost, ts.URL+"/v1/chat/completions",
		strings.NewReader(`{"model":"chaos-stall","messages":[]}`))
	start := time.Now()
	_, err := http.DefaultClient.Do(req)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected deadline exceeded, got %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("stall did not release on client cancel")
	}
}

func TestGarbage_Streaming(t *testing.T) {
	ts, _ := newTestServer(t, chaos.Profile{Name: "garbage", CompletionTokens: chaos.Range{Min: 10, Max: 10}, GarbageRate: 1})
	res, err := post(t, ts.URL, "chaos-garbage", true)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	chunks, done, malformed := readSSE(t, res.Body)
	if malformed != 1 || !done {
		t.Fatalf("expected exactly one malformed event and a [DONE], got malformed=%d done=%v", malformed, done)
	}
	if len(chunks) != 13 {
		t.Fatalf("valid chunks should still all be there, got %d", len(chunks))
	}
}

func TestGarbage_NonStreaming(t *testing.T) {
	ts, _ := newTestServer(t, chaos.Profile{Name: "garbage", CompletionTokens: chaos.Range{Min: 10, Max: 10}, GarbageRate: 1})
	res, err := post(t, ts.URL, "chaos-garbage", false)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	var v any
	if err := json.NewDecoder(res.Body).Decode(&v); err == nil {
		t.Fatal("truncated body should not decode")
	}
}

func TestPacing(t *testing.T) {
	ts, _ := newTestServer(t, chaos.Profile{
		Name:             "slow",
		TTFT:             chaos.Latency{MedianMs: 100},
		TokensPerSec:     50,
		CompletionTokens: chaos.Range{Min: 10, Max: 10},
	})
	start := time.Now()
	res, err := post(t, ts.URL, "chaos-slow", true)
	if err != nil {
		t.Fatal(err)
	}
	ttft := time.Since(start)
	io.Copy(io.Discard, res.Body)
	res.Body.Close()
	total := time.Since(start)
	if ttft < 100*time.Millisecond {
		t.Fatalf("headers arrived after %s, before the TTFT", ttft)
	}
	// 10 tokens at 50/s = 200 ms of generation on top of the TTFT.
	if total < 300*time.Millisecond {
		t.Fatalf("stream finished in %s, faster than the pacing allows", total)
	}
}

func TestAdminProfiles(t *testing.T) {
	ts, s := newTestServer(t, chaos.Profile{Name: "fast", CompletionTokens: chaos.Range{Min: 1, Max: 1}})

	req, _ := http.NewRequest(http.MethodPut, ts.URL+"/admin/profiles/fast",
		strings.NewReader(`{"completion_tokens":{"min":1,"max":1},"error":{"rate":1,"statuses":[503]}}`))
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res.Body.Close()
	if res.StatusCode != http.StatusNoContent {
		t.Fatalf("put status %d", res.StatusCode)
	}

	res, _ = post(t, ts.URL, "chaos-fast", false)
	res.Body.Close()
	if res.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("profile change not applied, status %d", res.StatusCode)
	}

	req, _ = http.NewRequest(http.MethodPut, ts.URL+"/admin/profiles/bad", strings.NewReader(`{"error":{"rate":2}}`))
	res, _ = http.DefaultClient.Do(req)
	res.Body.Close()
	if res.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("invalid profile accepted, status %d", res.StatusCode)
	}

	res, _ = http.Get(ts.URL + "/v1/models")
	var models struct{ Data []struct{ ID string } }
	json.NewDecoder(res.Body).Decode(&models)
	res.Body.Close()
	if len(models.Data) != 1 || models.Data[0].ID != "chaos-fast" {
		t.Fatalf("models %+v", models.Data)
	}

	res, _ = http.Get(ts.URL + "/metrics")
	body, _ := io.ReadAll(res.Body)
	res.Body.Close()
	if !strings.Contains(string(body), `chaos_requests_total{profile="fast",outcome="error"} 1`) {
		t.Fatalf("metrics missing the error outcome:\n%s", body)
	}
	_ = s
}
