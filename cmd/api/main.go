package main

import (
	"context"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/silver/pmvibes/internal/server"
	"github.com/silver/pmvibes/internal/store"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
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

	eventLog, err := store.NewEventLog(getEnv("REDIS_URL", ""))
	if err != nil {
		slog.Warn("invalid REDIS_URL; running without persisted history", "err", err)
		eventLog = nil
	}
	if eventLog != nil {
		defer eventLog.Close()
		slog.Info("simulation events will persist to Redis")
	}

	srv := server.New(cfg, logger, eventLog)

	slog.Info("starting PMVibes server with BTC 5m simulator", "port", cfg.Port)
	slog.Info("view simulation status at", "url", "http://localhost:"+cfg.Port+"/finance")
	slog.Info("persisted audit trail", "url", "http://localhost:"+cfg.Port+"/finance/history")

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
