package config

import (
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// Config holds all configuration loaded from environment variables.
type Config struct {
	CacheDir               string
	ForgejoDataDir         string
	ForgejoPushSSHKeyPath  string
	ForgejoDomain          string
	ForgejoSSHPort         int
	ForgejoDBHost          string
	ForgejoDBPort          int
	ForgejoDBUser          string
	ForgejoDBPassword      string
	ForgejoDBName          string
	ForgejoDBSSLMode       string
	WebhookListenAddr      string
	WebhookGitHubSecret    string
	WebhookGitLabSecret    string
	HistoryDBPath          string
	DashboardListenAddr    string
	WorkerConcurrency      int
	GitCommandTimeout      time.Duration
	LogLevel               string
	ParsedLogLevel         slog.Level
}

// Load reads and validates configuration from environment variables.
func Load() (*Config, error) {
	return LoadFrom(os.Getenv)
}

// LoadFrom reads and validates configuration using a lookup function.
func LoadFrom(getenv func(string) string) (*Config, error) {
	var errs []error
	cfg := &Config{}

	// Helper to get string and check required
	getRequired := func(key string) string {
		val := strings.TrimSpace(getenv(key))
		if val == "" {
			errs = append(errs, fmt.Errorf("%s is required", key))
		}
		return val
	}

	// Helper to get required int
	getRequiredInt := func(key string) int {
		val := strings.TrimSpace(getenv(key))
		if val == "" {
			errs = append(errs, fmt.Errorf("%s is required", key))
			return 0
		}
		n, err := strconv.Atoi(val)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s must be a valid integer: %w", key, err))
			return 0
		}
		return n
	}

	cfg.CacheDir = getRequired("CACHE_DIR")
	cfg.ForgejoDataDir = getRequired("FORGEJO_DATA_DIR")
	cfg.ForgejoPushSSHKeyPath = getRequired("FORGEJO_PUSH_SSH_KEY_PATH")
	cfg.ForgejoDomain = getRequired("FORGEJO_DOMAIN")
	cfg.ForgejoSSHPort = getRequiredInt("FORGEJO_SSH_PORT")

	cfg.ForgejoDBHost = getRequired("FORGEJO_DB_HOST")
	cfg.ForgejoDBPort = getRequiredInt("FORGEJO_DB_PORT")
	cfg.ForgejoDBUser = getRequired("FORGEJO_DB_USER")
	cfg.ForgejoDBPassword = getRequired("FORGEJO_DB_PASSWORD")
	cfg.ForgejoDBName = getRequired("FORGEJO_DB_NAME")
	cfg.ForgejoDBSSLMode = getRequired("FORGEJO_DB_SSLMODE")

	cfg.WebhookListenAddr = getRequired("WEBHOOK_LISTEN_ADDR")
	cfg.WebhookGitHubSecret = getRequired("WEBHOOK_GITHUB_SECRET")
	cfg.WebhookGitLabSecret = getRequired("WEBHOOK_GITLAB_SECRET")

	// HISTORY_DB_PATH (optional, defaults to filepath.Join(CACHE_DIR, "history.db"))
	historyDBPath := strings.TrimSpace(getenv("HISTORY_DB_PATH"))
	if historyDBPath == "" {
		if cfg.CacheDir != "" {
			historyDBPath = filepath.Join(cfg.CacheDir, "history.db")
		}
	}
	cfg.HistoryDBPath = historyDBPath

	cfg.DashboardListenAddr = getRequired("DASHBOARD_LISTEN_ADDR")

	// WORKER_CONCURRENCY (optional, default 4, int)
	workerConcurrencyStr := strings.TrimSpace(getenv("WORKER_CONCURRENCY"))
	if workerConcurrencyStr == "" {
		cfg.WorkerConcurrency = 4
	} else {
		n, err := strconv.Atoi(workerConcurrencyStr)
		if err != nil || n <= 0 {
			errs = append(errs, fmt.Errorf("WORKER_CONCURRENCY must be a positive integer, got %q", workerConcurrencyStr))
		} else {
			cfg.WorkerConcurrency = n
		}
	}

	// GIT_COMMAND_TIMEOUT (optional, default 10m, time.Duration parsed via time.ParseDuration)
	gitTimeoutStr := strings.TrimSpace(getenv("GIT_COMMAND_TIMEOUT"))
	if gitTimeoutStr == "" {
		cfg.GitCommandTimeout = 10 * time.Minute
	} else {
		dur, err := time.ParseDuration(gitTimeoutStr)
		if err != nil || dur <= 0 {
			errs = append(errs, fmt.Errorf("GIT_COMMAND_TIMEOUT must be a valid positive duration, got %q", gitTimeoutStr))
		} else {
			cfg.GitCommandTimeout = dur
		}
	}

	// LOG_LEVEL (optional, default info, validated one of debug/info/warn/error)
	logLevelStr := strings.TrimSpace(getenv("LOG_LEVEL"))
	if logLevelStr == "" {
		logLevelStr = "info"
	}
	switch strings.ToLower(logLevelStr) {
	case "debug":
		cfg.LogLevel = "debug"
		cfg.ParsedLogLevel = slog.LevelDebug
	case "info":
		cfg.LogLevel = "info"
		cfg.ParsedLogLevel = slog.LevelInfo
	case "warn", "warning":
		cfg.LogLevel = "warn"
		cfg.ParsedLogLevel = slog.LevelWarn
	case "error":
		cfg.LogLevel = "error"
		cfg.ParsedLogLevel = slog.LevelError
	default:
		errs = append(errs, fmt.Errorf("LOG_LEVEL must be one of debug, info, warn, error; got %q", logLevelStr))
	}

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return cfg, nil
}
