package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"chatgpt-codex-proxy/internal/config"
	"chatgpt-codex-proxy/internal/observability"
	"chatgpt-codex-proxy/internal/server"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		slog.Error("failed to load config", "error", err)
		os.Exit(1)
	}

	logger := observability.NewLogger(string(cfg.LogLevel))
	slog.SetDefault(logger)

	app, err := server.New(cfg, logger)
	if err != nil {
		logger.Error("failed to build server", "error", err)
		os.Exit(1)
	}
	defer func() {
		if closeErr := app.Close(); closeErr != nil {
			logger.Error("shutdown error", "error", closeErr)
		}
	}()

	srv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           app.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       120 * time.Second,
	}

	go func() {
		logger.Info("server listening", "addr", cfg.ListenAddr, "data_dir", cfg.DataDir)
		if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			logger.Error("server exited", "error", err)
			os.Exit(1)
		}
	}()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	<-ctx.Done()

	logger.Info("shutdown requested")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		logger.Error("graceful shutdown failed", "error", err)
	}
}
