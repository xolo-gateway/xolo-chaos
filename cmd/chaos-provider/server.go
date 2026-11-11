package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/xolo-gateway/xolo-chaos/internal/chaos"
)

// modelPrefix is what Xolo sends as the real model name for a profile:
// "chaos-slow" resolves to the "slow" profile.
const modelPrefix = "chaos-"

// Server is an OpenAI-compatible chat completion endpoint whose behaviour is
// chosen by the requested model name.
type Server struct {
	rand    *chaos.Rand
	metrics *metrics

	mu       sync.RWMutex
	profiles map[string]chaos.Profile
}

// NewServer builds a server over the given profiles.
func NewServer(seed int64, profiles map[string]chaos.Profile) *Server {
	return &Server{
		rand:     chaos.NewRand(seed),
		metrics:  newMetrics(),
		profiles: profiles,
	}
}

// Handler returns the HTTP routes.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /v1/chat/completions", s.handleChat)
	mux.HandleFunc("GET /v1/models", s.handleModels)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte("ok\n")) })
	mux.HandleFunc("GET /metrics", s.metrics.ServeHTTP)
	mux.HandleFunc("GET /admin/profiles", s.handleListProfiles)
	mux.HandleFunc("PUT /admin/profiles/{name}", s.handlePutProfile)
	mux.HandleFunc("DELETE /admin/profiles/{name}", s.handleDeleteProfile)
	return mux
}

func (s *Server) profile(model string) (chaos.Profile, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.profiles[strings.TrimPrefix(model, modelPrefix)]
	return p, ok
}

// --- OpenAI wire format ---------------------------------------------------

type chatRequest struct {
	Model    string `json:"model"`
	Stream   bool   `json:"stream"`
	Messages []struct {
		Role    string          `json:"role"`
		Content json.RawMessage `json:"content"`
	} `json:"messages"`
	StreamOptions *struct {
		IncludeUsage bool `json:"include_usage"`
	} `json:"stream_options"`
}

type usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

type apiError struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    *string `json:"code"`
}

func writeError(w http.ResponseWriter, status int, typ, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(map[string]apiError{"error": {Message: msg, Type: typ}})
}

// promptTokens estimates the prompt size the way every provider roughly does:
// four characters per token.
func promptTokens(req chatRequest) int {
	n := 0
	for _, m := range req.Messages {
		n += len(m.Content)
	}
	return n/4 + 3*len(req.Messages)
}

func (s *Server) handleModels(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	names := make([]string, 0, len(s.profiles))
	for name := range s.profiles {
		names = append(names, name)
	}
	s.mu.RUnlock()
	sort.Strings(names)

	data := make([]map[string]any, 0, len(names))
	for _, name := range names {
		data = append(data, map[string]any{"id": modelPrefix + name, "object": "model", "owned_by": "xolo-chaos"})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"object": "list", "data": data})
}

func (s *Server) handleChat(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 8<<20))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "could not read body")
		return
	}
	var req chatRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request_error", "body is not valid JSON: "+err.Error())
		return
	}
	p, ok := s.profile(req.Model)
	if !ok {
		s.metrics.inc(req.Model, "unknown_model")
		writeError(w, http.StatusNotFound, "invalid_request_error", fmt.Sprintf("The model `%s` does not exist", req.Model))
		return
	}

	ctx := r.Context()
	start := time.Now()
	outcome := s.serve(ctx, w, req, p)
	s.metrics.inc(p.Name, outcome)
	s.metrics.observe(p.Name, time.Since(start))
	slog.DebugContext(ctx, "chaos request", slog.String("profile", p.Name), slog.String("outcome", outcome), slog.Bool("stream", req.Stream), slog.Duration("took", time.Since(start)))
}

// serve runs one request through the profile and reports what happened, for
// the metrics: "ok", "error", "hangup", "stall", "garbage" or "cancelled".
func (s *Server) serve(ctx context.Context, w http.ResponseWriter, req chatRequest, p chaos.Profile) string {
	if p.Stall {
		<-ctx.Done()
		return "stall"
	}

	if s.rand.Hit(p.Error.Rate) {
		status := s.rand.Pick(p.Error.Statuses, http.StatusServiceUnavailable)
		if p.Error.RetryAfterS > 0 && (status == http.StatusTooManyRequests || status == http.StatusServiceUnavailable) {
			w.Header().Set("Retry-After", strconv.Itoa(p.Error.RetryAfterS))
		}
		typ := "server_error"
		if status == http.StatusTooManyRequests {
			typ = "rate_limit_error"
		}
		writeError(w, status, typ, fmt.Sprintf("chaos profile %s injected HTTP %d", p.Name, status))
		return "error"
	}

	hangup := s.rand.Hit(p.Hangup.Rate)
	garbage := s.rand.Hit(p.GarbageRate)
	n := p.CompletionTokens.Sample(s.rand)
	text := chaos.Text(s.rand, n)
	u := usage{PromptTokens: promptTokens(req), CompletionTokens: n}
	u.TotalTokens = u.PromptTokens + u.CompletionTokens
	id := "chatcmpl-chaos-" + strconv.FormatInt(time.Now().UnixNano(), 36)

	if !sleep(ctx, p.TTFT.Sample(s.rand)) {
		return "cancelled"
	}

	if !req.Stream {
		if hangup {
			return dropConnection(w)
		}
		if !sleep(ctx, generationTime(p, n)) {
			return "cancelled"
		}
		w.Header().Set("Content-Type", "application/json")
		if garbage {
			// A body cut in the middle of the JSON, as a proxy timing out
			// upstream would produce.
			full, _ := json.Marshal(completion(id, req.Model, text, u))
			w.Write(full[:len(full)/2])
			return "garbage"
		}
		json.NewEncoder(w).Encode(completion(id, req.Model, text, u))
		return "ok"
	}

	return s.stream(ctx, w, req, p, id, text, u, hangup, garbage)
}

func (s *Server) stream(ctx context.Context, w http.ResponseWriter, req chatRequest, p chaos.Profile, id, text string, u usage, hangup, garbage bool) string {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	flush := func() {
		if flusher != nil {
			flusher.Flush()
		}
	}

	chunkCount := 0
	hangupAfter := -1
	if hangup {
		hangupAfter = p.Hangup.AfterChunks.Sample(s.rand)
	}
	pieces := chaos.Words(text)
	garbageAt := -1
	if garbage && len(pieces) > 0 {
		garbageAt = s.rand.Intn(len(pieces))
	}

	emit := func(v any) bool {
		if chunkCount == hangupAfter {
			dropConnection(w)
			return false
		}
		b, _ := json.Marshal(v)
		fmt.Fprintf(w, "data: %s\n\n", b)
		flush()
		chunkCount++
		return true
	}

	if !emit(chunk(id, req.Model, map[string]any{"role": "assistant", "content": ""}, nil, nil)) {
		return "hangup"
	}

	var perToken time.Duration
	if p.TokensPerSec > 0 {
		perToken = time.Duration(float64(time.Second) / p.TokensPerSec)
	}
	for i, piece := range pieces {
		if !sleep(ctx, perToken) {
			return "cancelled"
		}
		if i == garbageAt {
			fmt.Fprintf(w, "data: {\"id\":\"%s\",\"object\":\"chat.completion.chunk\",\"choices\":[{\"index\":0,\"delta\":{\"content\":\"", id)
			flush()
			// Then carry on as if nothing happened: the client now has to
			// cope with one unparsable event in the middle of a valid stream.
			fmt.Fprint(w, "\n\n")
			chunkCount++
		}
		if !emit(chunk(id, req.Model, map[string]any{"content": piece}, nil, nil)) {
			return "hangup"
		}
	}

	stop := "stop"
	if !emit(chunk(id, req.Model, map[string]any{}, &stop, nil)) {
		return "hangup"
	}
	if req.StreamOptions == nil || req.StreamOptions.IncludeUsage {
		// Like OpenAI: a trailing chunk with no choices carrying the usage.
		if !emit(map[string]any{"id": id, "object": "chat.completion.chunk", "created": time.Now().Unix(), "model": req.Model, "choices": []any{}, "usage": u}) {
			return "hangup"
		}
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	flush()
	if garbage {
		return "garbage"
	}
	return "ok"
}

func generationTime(p chaos.Profile, tokens int) time.Duration {
	if p.TokensPerSec <= 0 {
		return 0
	}
	return time.Duration(float64(tokens) / p.TokensPerSec * float64(time.Second))
}

// sleep waits d unless ctx ends first, and reports whether it waited fully.
func sleep(ctx context.Context, d time.Duration) bool {
	if d <= 0 {
		return ctx.Err() == nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return true
	case <-ctx.Done():
		return false
	}
}

// dropConnection closes the underlying TCP connection without a response,
// which the client sees as EOF or a reset. Falls back to a bare 502 when the
// writer cannot be hijacked (HTTP/2, some test recorders).
func dropConnection(w http.ResponseWriter) string {
	h, ok := w.(http.Hijacker)
	if !ok {
		w.WriteHeader(http.StatusBadGateway)
		return "hangup"
	}
	conn, _, err := h.Hijack()
	if err != nil {
		w.WriteHeader(http.StatusBadGateway)
		return "hangup"
	}
	conn.Close()
	return "hangup"
}

func completion(id, model, text string, u usage) map[string]any {
	return map[string]any{
		"id":      id,
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": text},
			"finish_reason": "stop",
		}},
		"usage": u,
	}
}

func chunk(id, model string, delta map[string]any, finish *string, u *usage) map[string]any {
	out := map[string]any{
		"id":      id,
		"object":  "chat.completion.chunk",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": finish}},
	}
	if u != nil {
		out["usage"] = u
	}
	return out
}

// --- admin ----------------------------------------------------------------

func (s *Server) handleListProfiles(w http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	list := make([]chaos.Profile, 0, len(s.profiles))
	for _, p := range s.profiles {
		list = append(list, p)
	}
	s.mu.RUnlock()
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

// handlePutProfile creates or replaces a profile at runtime, so a campaign
// can flip a model from healthy to degraded without restarting anything.
func (s *Server) handlePutProfile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var p chaos.Profile
	if err := json.NewDecoder(r.Body).Decode(&p); err != nil {
		http.Error(w, "invalid JSON: "+err.Error(), http.StatusBadRequest)
		return
	}
	p.Name = name
	if err := p.Validate(); err != nil {
		http.Error(w, err.Error(), http.StatusUnprocessableEntity)
		return
	}
	s.mu.Lock()
	s.profiles[name] = p
	s.mu.Unlock()
	slog.Info("profile updated", slog.String("profile", name))
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleDeleteProfile(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	s.mu.Lock()
	_, ok := s.profiles[name]
	delete(s.profiles, name)
	s.mu.Unlock()
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
