package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/silver/pmvibes/internal/logging"
	"github.com/silver/pmvibes/internal/server"
	"github.com/silver/pmvibes/internal/store"
)

func main() {
	loadDotEnv()

	logger := slog.New(logging.NewHandler(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	})))
	slog.SetDefault(logger)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle shutdown signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigChan
		slog.Info("shutting down...")
		cancel()
	}()

	cfg := server.Config{
		Port: getEnv("PORT", "8080"),
	}

	eventLog, err := store.NewEventLog(getEnv("REDIS_URL", ""), logger)
	if err != nil {
		slog.Error("event log", "err", err)
		os.Exit(1)
	}
	defer eventLog.Close()

	srv := server.New(cfg, logger, eventLog)

	slog.Info("starting PMVibes server with BTC 5m simulator", "port", cfg.Port)
	slog.Info("finance dashboard UI", "url", "http://localhost:"+cfg.Port+"/")
	slog.Info("view simulation status at", "url", "http://localhost:"+cfg.Port+"/finance")
	slog.Info("persisted audit trail", "url", "http://localhost:"+cfg.Port+"/finance/history/1")
	if getEnv("REDIS_URL", "") != "" {
		slog.Info("simulation events persisted to Redis")
	} else {
		slog.Info("REDIS_URL not set; simulation events kept in process memory only")
	}

	if err := srv.Run(ctx); err != nil && err.Error() != "http: Server closed" {
		slog.Error("server exited with error", "err", err)
		os.Exit(1)
	}
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// loadDotEnv reads ./.env from the current working directory (if present) and
// sets environment variables that are not already set. This matches what
// `set -a && . ./.env` does so `go run ./cmd/api` from the repo root picks up PORT and REDIS_URL.
func loadDotEnv() {
	data, err := os.ReadFile(".env")
	if err != nil {
		return
	}
	for line := range strings.Lines(string(data)) {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "export ") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "export "))
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if len(val) >= 2 {
			switch {
			case val[0] == '"' && val[len(val)-1] == '"':
				val = val[1 : len(val)-1]
			case val[0] == '\'' && val[len(val)-1] == '\'':
				val = val[1 : len(val)-1]
			}
		}
		if key == "" || os.Getenv(key) != "" {
			continue
		}
		_ = os.Setenv(key, val)
	}
}
