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
	// v2 settings (new)
	ListenAddr     string
	CacheDir       string
	AppDBPath      string // SQLite database for repositories, ssh_keys, and sync_events
	SSHKeysDir     string
	WorkerConcurrency int
	GitCommandTimeout time.Duration
	LogLevel       string
	ParsedLogLevel slog.Level

	// OIDC settings (v2)
	OIDCIssuerURL    string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCRedirectURL  string
	OIDCScopes       string
	OIDCProviderName string
	SessionSecret    string
	SessionCookieName string
	SessionTTL       time.Duration

	// Legacy v1 settings (deprecated, to be removed)
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
	ForgejoSecret          string
	WebhookListenAddr      string
	WebhookGitHubSecret    string
	WebhookGitLabSecret    string
	HistoryDBPath          string
	DashboardListenAddr    string
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

	// Helper to get optional string with default
	getOptional := func(key, defaultVal string) string {
		val := strings.TrimSpace(getenv(key))
		if val == "" {
			return defaultVal
		}
		return val
	}

	// Helper to get optional int with default
	getOptionalInt := func(key string, defaultVal int) int {
		val := strings.TrimSpace(getenv(key))
		if val == "" {
			return defaultVal
		}
		n, err := strconv.Atoi(val)
		if err != nil {
			errs = append(errs, fmt.Errorf("%s must be a valid integer: %w", key, err))
			return defaultVal
		}
		return n
	}

	// Helper to parse duration
	parseDuration := func(key string, defaultVal time.Duration) time.Duration {
		val := strings.TrimSpace(getenv(key))
		if val == "" {
			return defaultVal
		}
		dur, err := time.ParseDuration(val)
		if err != nil || dur <= 0 {
			errs = append(errs, fmt.Errorf("%s must be a valid positive duration, got %q", key, val))
			return defaultVal
		}
		return dur
	}

	// v2 configuration (new)
	cfg.ListenAddr = getRequired("LISTEN_ADDR")
	cfg.CacheDir = getRequired("CACHE_DIR")
	cfg.AppDBPath = getOptional("APP_DB_PATH", filepath.Join(cfg.CacheDir, "app.db"))
	cfg.SSHKeysDir = getRequired("SSH_KEYS_DIR")

	// v2 OIDC configuration
	cfg.OIDCIssuerURL = getRequired("OIDC_ISSUER_URL")
	cfg.OIDCClientID = getRequired("OIDC_CLIENT_ID")
	cfg.OIDCClientSecret = getRequired("OIDC_CLIENT_SECRET")
	cfg.OIDCRedirectURL = getRequired("OIDC_REDIRECT_URL")
	cfg.OIDCScopes = getOptional("OIDC_SCOPES", "openid profile email")
	cfg.OIDCProviderName = getOptional("OIDC_PROVIDER_NAME", "PocketID")
	cfg.SessionSecret = getRequired("SESSION_SECRET")
	cfg.SessionCookieName = getOptional("SESSION_COOKIE_NAME", "forgejo_sync_session")
	cfg.SessionTTL = parseDuration("SESSION_TTL", 24*time.Hour)

	// Worker and git settings
	cfg.WorkerConcurrency = getOptionalInt("WORKER_CONCURRENCY", 4)
	cfg.GitCommandTimeout = parseDuration("GIT_COMMAND_TIMEOUT", 10*time.Minute)

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

	// Legacy v1 configuration (kept for backward compatibility during migration, will be removed)
	cfg.ForgejoDataDir = getOptional("FORGEJO_DATA_DIR", "")
	cfg.ForgejoPushSSHKeyPath = getOptional("FORGEJO_PUSH_SSH_KEY_PATH", "")
	cfg.ForgejoDomain = getOptional("FORGEJO_DOMAIN", "")
	cfg.ForgejoSSHPort = getOptionalInt("FORGEJO_SSH_PORT", 22)
	cfg.ForgejoDBHost = getOptional("FORGEJO_DB_HOST", "")
	cfg.ForgejoDBPort = getOptionalInt("FORGEJO_DB_PORT", 5432)
	cfg.ForgejoDBUser = getOptional("FORGEJO_DB_USER", "")
	cfg.ForgejoDBPassword = getOptional("FORGEJO_DB_PASSWORD", "")
	cfg.ForgejoDBName = getOptional("FORGEJO_DB_NAME", "")
	cfg.ForgejoDBSSLMode = getOptional("FORGEJO_DB_SSLMODE", "")
	cfg.ForgejoSecret = getOptional("FORGEJO_SECRET_KEY", "")
	cfg.WebhookListenAddr = getOptional("WEBHOOK_LISTEN_ADDR", "")
	cfg.WebhookGitHubSecret = getOptional("WEBHOOK_GITHUB_SECRET", "")
	cfg.WebhookGitLabSecret = getOptional("WEBHOOK_GITLAB_SECRET", "")
	cfg.HistoryDBPath = getOptional("HISTORY_DB_PATH", "")
	cfg.DashboardListenAddr = getOptional("DASHBOARD_LISTEN_ADDR", "")

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return cfg, nil
}
