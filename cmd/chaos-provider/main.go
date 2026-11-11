// Command chaos-provider is an OpenAI-compatible LLM endpoint that misbehaves
// on purpose. Register it in Xolo as an "openai" provider and expose one
// model per profile (chaos-fast, chaos-slow, chaos-flaky…) to drive load and
// chaos campaigns without spending a token.
//
// Configuration, by environment:
//
//	CHAOS_ADDR      listen address (default :9090)
//	CHAOS_SEED      PRNG seed, for replayable campaigns (default: current time)
//	CHAOS_PROFILES  path to a JSON list of profiles replacing the built-in set
//	CHAOS_LOG_LEVEL debug|info|warn|error (default info)
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"github.com/xolo-gateway/xolo-chaos/internal/chaos"
)

func main() {
	if err := run(); err != nil {
		slog.Error("chaos-provider failed", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	configureLogging()

	addr := envOr("CHAOS_ADDR", ":9090")
	seed := time.Now().UnixNano()
	if raw := os.Getenv("CHAOS_SEED"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return errors.New("CHAOS_SEED must be an integer")
		}
		seed = v
	}

	profiles := chaos.Defaults()
	if path := os.Getenv("CHAOS_PROFILES"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		profiles, err = chaos.ParseProfiles(data)
		if err != nil {
			return err
		}
	}

	srv := &http.Server{
		Addr:              addr,
		Handler:           NewServer(seed, profiles).Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		// No write timeout: the stall profile must be able to hold a
		// connection for as long as the client is willing to wait.
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	errCh := make(chan error, 1)
	go func() { errCh <- srv.ListenAndServe() }()
	slog.Info("chaos-provider listening", slog.String("addr", addr), slog.Int64("seed", seed), slog.Int("profiles", len(profiles)))

	select {
	case err := <-errCh:
		return err
	case <-ctx.Done():
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return srv.Shutdown(shutdownCtx)
	}
}

func configureLogging() {
	level := slog.LevelInfo
	if err := level.UnmarshalText([]byte(envOr("CHAOS_LOG_LEVEL", "info"))); err != nil {
		level = slog.LevelInfo
	}
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: level})))
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
