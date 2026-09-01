package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"forgejo-sync/internal/api"
	"forgejo-sync/internal/auth"
	"forgejo-sync/internal/config"
	"forgejo-sync/internal/gitsync"
	"forgejo-sync/internal/reposvc"
	"forgejo-sync/internal/sshkeys"
	"forgejo-sync/internal/store"
	"forgejo-sync/internal/webapp"
	"forgejo-sync/internal/webhook"
	"forgejo-sync/internal/worker"
)

type gitSyncer struct{}

func (gitSyncer) SyncRepository(ctx context.Context, opts gitsync.SyncOptions) (string, error) {
	slog.Debug("main.gitSyncer: invoking SyncRepository",
		slog.String("source_url", opts.SourceURL),
		slog.String("cache_dir", opts.CacheDir),
		slog.String("source_key_path", opts.SourceKeyPath),
	)
	cachePath, err := gitsync.SyncRepository(ctx, opts)
	if err != nil {
		slog.Debug("main.gitSyncer: SyncRepository returned error", slog.Any("error", err))
		return "", err
	}
	slog.Debug("main.gitSyncer: SyncRepository completed", slog.String("cache_path", cachePath))
	return cachePath, nil
}

func (gitSyncer) PushToRemote(ctx context.Context, opts gitsync.PushOptions) (*gitsync.PushResult, error) {
	slog.Debug("main.gitSyncer: invoking PushToRemote",
		slog.String("cache_path", opts.CachePath),
		slog.String("remote_url", opts.RemoteURL),
		slog.String("remote_key_path", opts.RemoteKeyPath),
	)
	res, err := gitsync.PushToRemote(ctx, opts)
	if err != nil {
		slog.Debug("main.gitSyncer: PushToRemote returned error", slog.Any("error", err))
		return nil, err
	}
	slog.Debug("main.gitSyncer: PushToRemote completed",
		slog.String("before_sha", res.BeforeSHA),
		slog.String("after_sha", res.AfterSHA),
	)
	return res, nil
}

func (gitSyncer) LsRemote(ctx context.Context, remoteURL string, keyPath string, timeout time.Duration) error {
	slog.Debug("main.gitSyncer: invoking LsRemote",
		slog.String("remote_url", remoteURL),
		slog.String("key_path", keyPath),
		slog.Duration("timeout", timeout),
	)
	err := gitsync.LsRemote(ctx, remoteURL, keyPath, timeout)
	if err != nil {
		slog.Debug("main.gitSyncer: LsRemote returned error", slog.Any("error", err))
		return err
	}
	slog.Debug("main.gitSyncer: LsRemote completed successfully")
	return nil
}

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
	logger.Debug("runtime configuration loaded",
		slog.String("mode", cfg.Mode),
		slog.String("app_db_path", cfg.AppDBPath),
		slog.String("cache_dir", cfg.CacheDir),
		slog.String("ssh_keys_dir", cfg.SSHKeysDir),
		slog.String("session_cookie_name", cfg.SessionCookieName),
		slog.Duration("session_ttl", cfg.SessionTTL),
	)

	if cfg.Mode == "development" && cfg.SessionSecret == "development-session-secret-change-me" {
		logger.Warn("running in development mode with default insecure SESSION_SECRET; do not use in production")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Initialize the app database (repositories, ssh_keys)
	appDBPath := cfg.AppDBPath
	logger.Debug("initializing app database", slog.String("db_path", appDBPath))
	appDB, err := store.NewDB(appDBPath)
	if err != nil {
		return fmt.Errorf("init app database: %w", err)
	}
	defer appDB.Close()
	logger.Debug("app database initialized successfully")

	// Extract store interfaces
	repoStore := appDB.Repositories()
	keyStore := appDB.SSHKeys()
	if err := os.MkdirAll(cfg.SSHKeysDir, 0o700); err != nil {
		return fmt.Errorf("init ssh keys directory: %w", err)
	}
	logger.Debug("ensured SSH keys directory exists", slog.String("dir", cfg.SSHKeysDir))

	keyManager := sshkeys.NewManager(cfg.SSHKeysDir, keyStore, repoStore, logger)
	repoSvc := reposvc.NewService(repoStore, keyStore, appDB.SyncEvents(), gitSyncer{}, cfg.SSHKeysDir, cfg.CacheDir, logger)

	logger.Debug("initializing auth provider",
		slog.String("mode", cfg.Mode),
		slog.String("issuer_url", cfg.OIDCIssuerURL),
		slog.String("client_id", cfg.OIDCClientID),
	)
	authProvider, err := auth.NewProvider(auth.Config{
		Mode:          cfg.Mode,
		IssuerURL:     cfg.OIDCIssuerURL,
		ClientID:      cfg.OIDCClientID,
		ClientSecret:  cfg.OIDCClientSecret,
		RedirectURL:   cfg.OIDCRedirectURL,
		Scopes:        strings.Fields(cfg.OIDCScopes),
		ProviderName:  cfg.OIDCProviderName,
		SessionSecret: cfg.SessionSecret,
		SessionCookie: cfg.SessionCookieName,
		SessionTTL:    cfg.SessionTTL,
		Logger:        logger,
	})
	if err != nil {
		return fmt.Errorf("init auth provider: %w", err)
	}
	logger.Debug("auth provider initialized successfully")

	// Initialize the worker pool and webhook server against the same app DB.
	eventStore := appDB.SyncEvents()
	workerPool := worker.New(cfg, repoStore, keyStore, eventStore)
	workerPool.Start()
	logger.Debug("worker pool started", slog.Int("concurrency", cfg.WorkerConcurrency))

	webhookOpts := webhook.ServerOptions{
		History:         eventStore,
		Enqueuer:        workerPool,
		RepositoryStore: repoStore,
		Logger:          logger,
	}
	webhookServerHandler := webhook.NewServer(webhookOpts)

	apiHandler := api.NewHandler(api.Options{
		RepoStore:  repoStore,
		KeyStore:   keyStore,
		EventStore: appDB.SyncEvents(),
		RepoSvc:    repoSvc,
		KeyMgr:     keyManager,
		Auth:       authProvider,
		Logger:     logger,
	})

	// Create a single HTTP server for all routes
	mux := http.NewServeMux()
	mux.Handle("/auth/", authProvider.Handler())
	mux.Handle("/api/", auth.RequireAuth(authProvider, apiHandler))
	mux.HandleFunc("POST /webhook", webhookServerHandler.ServeHTTP)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// Serve the Angular SPA for browser navigation and client-side routes.
	spaHandler, err := webapp.NewHandler()
	if err != nil {
		return fmt.Errorf("init spa router: %w", err)
	}
	mux.Handle("/", spaHandler)
	logger.Debug("HTTP router configured with /auth/*, /api/*, /webhook, /healthz, and /")

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
	logger.Debug("HTTP server shutdown finished")

	logger.Info("waiting for worker pool shutdown...")
	workerPool.Shutdown()
	logger.Debug("worker pool shutdown finished")

	logger.Info("shutdown complete")
	return nil
}
