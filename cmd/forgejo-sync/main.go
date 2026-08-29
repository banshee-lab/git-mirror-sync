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
	"forgejo-sync/internal/history"
	"forgejo-sync/internal/store"
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
		slog.String("listen_addr", cfg.ListenAddr),
		slog.Int("worker_concurrency", cfg.WorkerConcurrency),
		slog.Duration("git_timeout", cfg.GitCommandTimeout),
	)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Initialize the app database (repositories, ssh_keys)
	appDBPath := cfg.AppDBPath
	appDB, err := store.NewDB(appDBPath)
	if err != nil {
		return fmt.Errorf("init app database: %w", err)
	}
	defer appDB.Close()

	// Initialize the legacy history store (for worker/webhook/dashboard compatibility during migration)
	historyDBPath := cfg.HistoryDBPath
	if historyDBPath == "" {
		historyDBPath = fmt.Sprintf("%s/history.db", cfg.CacheDir)
	}

	historyStore, err := history.NewStore(historyDBPath)
	if err != nil {
		return fmt.Errorf("init history store: %w", err)
	}
	defer historyStore.Close()

	// Extract store interfaces
	repoStore := appDB.Repositories()
	keyStore := appDB.SSHKeys()

	// Initialize the worker pool with the new store-based interfaces
	workerPool := worker.New(cfg, repoStore, keyStore, historyStore)
	workerPool.Start()

	// Initialize the webhook server with per-repository secret verification
	webhookOpts := webhook.ServerOptions{
		History:         historyStore,
		Enqueuer:        workerPool,
		RepositoryStore: repoStore,
		Logger:          logger,
	}
	webhookServerHandler := webhook.NewServer(webhookOpts)

	// Create a single HTTP server for all routes
	mux := http.NewServeMux()
	
	// Mount webhook handler
	mux.HandleFunc("POST /webhook", webhookServerHandler.ServeHTTP)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Mount legacy dashboard (will be replaced by REST API + Angular SPA in v2)
	dashboardHandler, err := dashboard.NewRouter(historyStore)
	if err != nil {
		return fmt.Errorf("init dashboard router: %w", err)
	}
	mux.Handle("/", dashboardHandler)

	server := &http.Server{
		Addr:    cfg.ListenAddr,
		Handler: mux,
	}

	errCh := make(chan error, 1)

	go func() {
		logger.Info("server starting", slog.String("addr", cfg.ListenAddr))
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("server error: %w", err)
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

	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("server shutdown error: %w", err)
	}

	logger.Info("waiting for worker pool shutdown...")
	workerPool.Shutdown()

	logger.Info("shutdown complete")
	return nil
}
