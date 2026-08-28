package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"forgejo-sync/internal/config"
	"forgejo-sync/internal/dashboard"
	"forgejo-sync/internal/forgejodb"
	"forgejo-sync/internal/history"
	"forgejo-sync/internal/webhook"
	"forgejo-sync/internal/worker"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("configuration error:\n%w", err)
	}

	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: cfg.ParsedLogLevel,
	}))
	slog.SetDefault(logger)

	logger.Info("starting forgejo-sync",
		slog.String("log_level", cfg.LogLevel),
		slog.String("webhook_addr", cfg.WebhookListenAddr),
		slog.String("dashboard_addr", cfg.DashboardListenAddr),
		slog.Int("worker_concurrency", cfg.WorkerConcurrency),
		slog.Duration("git_timeout", cfg.GitCommandTimeout),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	historyStore, err := history.NewStore(cfg.HistoryDBPath)
	if err != nil {
		return fmt.Errorf("init history store: %w", err)
	}
	defer historyStore.Close()

	dbClient, err := forgejodb.NewClient(ctx, cfg)
	if err != nil {
		return fmt.Errorf("init forgejo db client: %w", err)
	}
	defer dbClient.Close()

	workerPool := worker.New(cfg, dbClient, historyStore)
	workerPool.Start()

	webhookOpts := webhook.ServerOptions{
		History:      historyStore,
		Enqueuer:     workerPool,
		GitHubSecret: cfg.WebhookGitHubSecret,
		GitLabSecret: cfg.WebhookGitLabSecret,
		Logger:       logger,
	}
	webhookServerHandler := webhook.NewServer(webhookOpts)

	webhookServer := &http.Server{
		Addr:    cfg.WebhookListenAddr,
		Handler: webhookServerHandler.Handler(),
	}

	dashboardHandler, err := dashboard.NewRouter(historyStore)
	if err != nil {
		return fmt.Errorf("init dashboard router: %w", err)
	}

	dashboardServer := &http.Server{
		Addr:    cfg.DashboardListenAddr,
		Handler: dashboardHandler,
	}

	errCh := make(chan error, 2)

	go func() {
		logger.Info("webhook listener starting", slog.String("addr", cfg.WebhookListenAddr))
		if err := webhookServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("webhook server error: %w", err)
		}
	}()

	go func() {
		logger.Info("dashboard server starting", slog.String("addr", cfg.DashboardListenAddr))
		if err := dashboardServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("dashboard server error: %w", err)
		}
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutting down gracefully...")
	case err := <-errCh:
		logger.Error("server startup error", slog.Any("error", err))
		return err
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var shutdownErr error
	if err := webhookServer.Shutdown(shutdownCtx); err != nil {
		shutdownErr = errors.Join(shutdownErr, fmt.Errorf("webhook server shutdown error: %w", err))
	}
	if err := dashboardServer.Shutdown(shutdownCtx); err != nil {
		shutdownErr = errors.Join(shutdownErr, fmt.Errorf("dashboard server shutdown error: %w", err))
	}

	logger.Info("waiting for worker pool shutdown...")
	workerPool.Shutdown()

	if shutdownErr != nil {
		return shutdownErr
	}

	logger.Info("shutdown complete")
	return nil
}
