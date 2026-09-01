package config_test

import (
	"log/slog"
	"strings"
	"testing"
	"time"

	"forgejo-sync/internal/config"
)

func validEnvMap() map[string]string {
	return map[string]string{
		// v2 configuration
		"LISTEN_ADDR":        ":8080",
		"CACHE_DIR":          "/var/cache/forgejo-sync",
		"APP_DB_PATH":        "/var/lib/forgejo-sync/app.db",
		"SSH_KEYS_DIR":       "/var/lib/forgejo-sync/keys",
		"OIDC_ISSUER_URL":    "https://id.example.com",
		"OIDC_CLIENT_ID":     "forgejo-sync",
		"OIDC_CLIENT_SECRET": "client_secret",
		"OIDC_REDIRECT_URL":  "https://sync.example.com/auth/callback",
		"SESSION_SECRET":     "session_secret_32_bytes_minimum",
		"SECRET_KEY":         "development-secret-key-change-me",
	}
}

func TestConfig_Load_Success(t *testing.T) {
	env := validEnvMap()
	env["APP_DB_PATH"] = "/custom/path/app.db"
	env["WORKER_CONCURRENCY"] = "8"
	env["GIT_COMMAND_TIMEOUT"] = "5m"
	env["LOG_LEVEL"] = "debug"
	env["OIDC_SCOPES"] = "openid profile email"
	env["SESSION_TTL"] = "48h"

	cfg, err := config.LoadFrom(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// v2 settings
	if cfg.ListenAddr != ":8080" {
		t.Errorf("expected ListenAddr :8080, got %q", cfg.ListenAddr)
	}
	if cfg.CacheDir != "/var/cache/forgejo-sync" {
		t.Errorf("expected CacheDir to match, got %q", cfg.CacheDir)
	}
	if cfg.AppDBPath != "/custom/path/app.db" {
		t.Errorf("expected AppDBPath to match, got %q", cfg.AppDBPath)
	}
	if cfg.SSHKeysDir != "/var/lib/forgejo-sync/keys" {
		t.Errorf("expected SSHKeysDir to match, got %q", cfg.SSHKeysDir)
	}
	if cfg.OIDCIssuerURL != "https://id.example.com" {
		t.Errorf("expected OIDCIssuerURL to match, got %q", cfg.OIDCIssuerURL)
	}
	if cfg.OIDCClientID != "forgejo-sync" {
		t.Errorf("expected OIDCClientID to match, got %q", cfg.OIDCClientID)
	}
	if cfg.OIDCRedirectURL != "https://sync.example.com/auth/callback" {
		t.Errorf("expected OIDCRedirectURL to match, got %q", cfg.OIDCRedirectURL)
	}
	if cfg.SessionCookieName != "forgejo_sync_session" {
		t.Errorf("expected SessionCookieName default, got %q", cfg.SessionCookieName)
	}
	if cfg.OIDCProviderName != "PocketID" {
		t.Errorf("expected OIDCProviderName default, got %q", cfg.OIDCProviderName)
	}
	if cfg.SessionTTL != 48*time.Hour {
		t.Errorf("expected SessionTTL 48h, got %v", cfg.SessionTTL)
	}
	if cfg.WorkerConcurrency != 8 {
		t.Errorf("expected WorkerConcurrency 8, got %d", cfg.WorkerConcurrency)
	}
	if cfg.GitCommandTimeout != 5*time.Minute {
		t.Errorf("expected GitCommandTimeout 5m, got %v", cfg.GitCommandTimeout)
	}
	if cfg.LogLevel != "debug" || cfg.ParsedLogLevel != slog.LevelDebug {
		t.Errorf("expected LogLevel debug, got %q (%v)", cfg.LogLevel, cfg.ParsedLogLevel)
	}
}

func TestConfig_Load_Defaults(t *testing.T) {
	env := validEnvMap()

	cfg, err := config.LoadFrom(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(cfg.AppDBPath, "app.db") {
		t.Errorf("expected AppDBPath to contain app.db, got %q", cfg.AppDBPath)
	}
	if cfg.SessionCookieName != "forgejo_sync_session" {
		t.Errorf("expected default SessionCookieName, got %q", cfg.SessionCookieName)
	}
	if cfg.SessionTTL != 24*time.Hour {
		t.Errorf("expected default SessionTTL 24h, got %v", cfg.SessionTTL)
	}
	if cfg.OIDCProviderName != "PocketID" {
		t.Errorf("expected default OIDCProviderName, got %q", cfg.OIDCProviderName)
	}
	if cfg.WorkerConcurrency != 4 {
		t.Errorf("expected default WorkerConcurrency 4, got %d", cfg.WorkerConcurrency)
	}
	if cfg.GitCommandTimeout != 10*time.Minute {
		t.Errorf("expected default GitCommandTimeout 10m, got %v", cfg.GitCommandTimeout)
	}
	if cfg.LogLevel != "info" || cfg.ParsedLogLevel != slog.LevelInfo {
		t.Errorf("expected default LogLevel info, got %q (%v)", cfg.LogLevel, cfg.ParsedLogLevel)
	}
}

func TestConfig_Load_DevelopmentMode(t *testing.T) {
	env := validEnvMap()
	env["MODE"] = "development"
	delete(env, "OIDC_ISSUER_URL")
	delete(env, "OIDC_CLIENT_ID")
	delete(env, "OIDC_CLIENT_SECRET")
	delete(env, "OIDC_REDIRECT_URL")
	delete(env, "SESSION_SECRET")
	delete(env, "SECRET_KEY")

	cfg, err := config.LoadFrom(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("unexpected error in development mode: %v", err)
	}
	if cfg.Mode != "development" {
		t.Fatalf("expected MODE to be development, got %q", cfg.Mode)
	}
	if cfg.OIDCProviderName != "Development" {
		t.Fatalf("expected development provider name, got %q", cfg.OIDCProviderName)
	}
	if cfg.SessionSecret != "development-session-secret-change-me" {
		t.Fatalf("expected dev session secret default, got %q", cfg.SessionSecret)
	}
	if cfg.SecretKey != "development-secret-key-change-me" {
		t.Fatalf("expected dev secret key default, got %q", cfg.SecretKey)
	}
}

func TestConfig_Load_MissingRequired_MultiError(t *testing.T) {
	// Empty env - all required variables should be reported
	cfg, err := config.LoadFrom(func(k string) string { return "" })
	if cfg != nil {
		t.Errorf("expected nil config on error, got %+v", cfg)
	}
	if err == nil {
		t.Fatal("expected error for empty env, got nil")
	}

	requiredVars := []string{
		"LISTEN_ADDR",
		"CACHE_DIR",
		"SSH_KEYS_DIR",
		"OIDC_ISSUER_URL",
		"OIDC_CLIENT_ID",
		"OIDC_CLIENT_SECRET",
		"OIDC_REDIRECT_URL",
		"SESSION_SECRET",
		"SECRET_KEY",
	}

	errStr := err.Error()
	for _, reqVar := range requiredVars {
		if !strings.Contains(errStr, reqVar) {
			t.Errorf("expected error message to mention missing %s, full error:\n%s", reqVar, errStr)
		}
	}
}

func TestConfig_Load_InvalidValues(t *testing.T) {
	env := validEnvMap()
	env["WORKER_CONCURRENCY"] = "not_an_int"
	env["GIT_COMMAND_TIMEOUT"] = "invalid_duration"
	env["LOG_LEVEL"] = "super_trace"
	env["SESSION_TTL"] = "invalid_duration"

	_, err := config.LoadFrom(func(k string) string { return env[k] })
	if err == nil {
		t.Fatal("expected errors for invalid values, got nil")
	}

	errStr := err.Error()
	expectedErrors := []string{
		"WORKER_CONCURRENCY",
		"GIT_COMMAND_TIMEOUT",
		"LOG_LEVEL",
		"SESSION_TTL",
	}

	for _, field := range expectedErrors {
		if !strings.Contains(errStr, field) {
			t.Errorf("expected error message to mention invalid %s, full error:\n%s", field, errStr)
		}
	}
}
