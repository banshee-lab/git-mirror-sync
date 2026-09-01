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
	Mode              string
	ListenAddr        string
	CacheDir          string
	AppDBPath         string
	SSHKeysDir        string
	WorkerConcurrency int
	GitCommandTimeout time.Duration
	LogLevel          string
	ParsedLogLevel    slog.Level

	OIDCIssuerURL     string
	OIDCClientID      string
	OIDCClientSecret  string
	OIDCRedirectURL   string
	OIDCScopes        string
	OIDCProviderName  string
	SessionSecret     string
	SecretKey         string
	SessionCookieName string
	SessionTTL        time.Duration

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
	cfg.Mode = strings.ToLower(strings.TrimSpace(getenv("MODE")))
	if cfg.Mode == "development" || cfg.Mode == "dev" {
		cfg.OIDCIssuerURL = getOptional("OIDC_ISSUER_URL", "https://development.invalid")
		cfg.OIDCClientID = getOptional("OIDC_CLIENT_ID", "dev-client")
		cfg.OIDCClientSecret = getOptional("OIDC_CLIENT_SECRET", "dev-secret")
		cfg.OIDCRedirectURL = getOptional("OIDC_REDIRECT_URL", "http://localhost:8080/auth/callback")
		cfg.OIDCScopes = getOptional("OIDC_SCOPES", "openid profile email")
		cfg.OIDCProviderName = getOptional("OIDC_PROVIDER_NAME", "Development")
		cfg.SessionSecret = getOptional("SESSION_SECRET", "development-session-secret-change-me")
		cfg.SecretKey = getOptional("SECRET_KEY", "development-secret-key-change-me")
		if len(cfg.SecretKey) < 16 {
			errs = append(errs, errors.New("SECRET_KEY must be at least 16 characters in development mode"))
		}
		cfg.SessionCookieName = getOptional("SESSION_COOKIE_NAME", "forgejo_sync_session")
		cfg.SessionTTL = parseDuration("SESSION_TTL", 24*time.Hour)
	} else {
		cfg.Mode = "production"
		cfg.OIDCIssuerURL = getRequired("OIDC_ISSUER_URL")
		cfg.OIDCClientID = getRequired("OIDC_CLIENT_ID")
		cfg.OIDCClientSecret = getRequired("OIDC_CLIENT_SECRET")
		cfg.OIDCRedirectURL = getRequired("OIDC_REDIRECT_URL")
		cfg.OIDCScopes = getOptional("OIDC_SCOPES", "openid profile email")
		cfg.OIDCProviderName = getOptional("OIDC_PROVIDER_NAME", "PocketID")
		cfg.SessionSecret = getRequired("SESSION_SECRET")
		if len(cfg.SessionSecret) < 16 {
			errs = append(errs, errors.New("SESSION_SECRET must be at least 16 characters in production mode"))
		}
		cfg.SecretKey = getRequired("SECRET_KEY")
		if len(cfg.SecretKey) < 32 {
			errs = append(errs, errors.New("SECRET_KEY must be at least 32 characters in production mode"))
		}
		cfg.SessionCookieName = getOptional("SESSION_COOKIE_NAME", "forgejo_sync_session")
		cfg.SessionTTL = parseDuration("SESSION_TTL", 24*time.Hour)
	}

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

	if len(errs) > 0 {
		return nil, errors.Join(errs...)
	}

	return cfg, nil
}
