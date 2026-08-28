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
		"CACHE_DIR":                 "/var/cache/forgejo-sync",
		"FORGEJO_DATA_DIR":          "/var/lib/forgejo/data",
		"FORGEJO_PUSH_SSH_KEY_PATH": "/etc/forgejo/push_key",
		"FORGEJO_DOMAIN":            "forgejo.example.com",
		"FORGEJO_SSH_PORT":          "2222",
		"FORGEJO_DB_HOST":           "postgres.example.com",
		"FORGEJO_DB_PORT":           "5432",
		"FORGEJO_DB_USER":           "forgejo_ro",
		"FORGEJO_DB_PASSWORD":       "secret_password",
		"FORGEJO_DB_NAME":           "forgejo",
		"FORGEJO_DB_SSLMODE":        "disable",
		"WEBHOOK_LISTEN_ADDR":       ":8080",
		"WEBHOOK_GITHUB_SECRET":     "gh_secret",
		"WEBHOOK_GITLAB_SECRET":     "gl_secret",
		"DASHBOARD_LISTEN_ADDR":     ":8081",
		"FORGEJO_SECRET_KEY":				 "secret",
	}
}

func TestConfig_Load_Success(t *testing.T) {
	env := validEnvMap()
	env["HISTORY_DB_PATH"] = "/custom/path/history.db"
	env["WORKER_CONCURRENCY"] = "8"
	env["GIT_COMMAND_TIMEOUT"] = "5m"
	env["LOG_LEVEL"] = "debug"

	cfg, err := config.LoadFrom(func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.CacheDir != "/var/cache/forgejo-sync" {
		t.Errorf("expected CacheDir to match, got %q", cfg.CacheDir)
	}
	if cfg.ForgejoDataDir != "/var/lib/forgejo/data" {
		t.Errorf("expected ForgejoDataDir to match, got %q", cfg.ForgejoDataDir)
	}
	if cfg.ForgejoPushSSHKeyPath != "/etc/forgejo/push_key" {
		t.Errorf("expected ForgejoPushSSHKeyPath to match, got %q", cfg.ForgejoPushSSHKeyPath)
	}
	if cfg.ForgejoDomain != "forgejo.example.com" {
		t.Errorf("expected ForgejoDomain to match, got %q", cfg.ForgejoDomain)
	}
	if cfg.ForgejoSSHPort != 2222 {
		t.Errorf("expected ForgejoSSHPort 2222, got %d", cfg.ForgejoSSHPort)
	}
	if cfg.ForgejoDBHost != "postgres.example.com" {
		t.Errorf("expected ForgejoDBHost to match, got %q", cfg.ForgejoDBHost)
	}
	if cfg.ForgejoDBPort != 5432 {
		t.Errorf("expected ForgejoDBPort 5432, got %d", cfg.ForgejoDBPort)
	}
	if cfg.ForgejoDBUser != "forgejo_ro" {
		t.Errorf("expected ForgejoDBUser to match, got %q", cfg.ForgejoDBUser)
	}
	if cfg.ForgejoDBPassword != "secret_password" {
		t.Errorf("expected ForgejoDBPassword to match, got %q", cfg.ForgejoDBPassword)
	}
	if cfg.ForgejoDBName != "forgejo" {
		t.Errorf("expected ForgejoDBName to match, got %q", cfg.ForgejoDBName)
	}
	if cfg.ForgejoDBSSLMode != "disable" {
		t.Errorf("expected ForgejoDBSSLMode to match, got %q", cfg.ForgejoDBSSLMode)
	}
	if cfg.WebhookListenAddr != ":8080" {
		t.Errorf("expected WebhookListenAddr to match, got %q", cfg.WebhookListenAddr)
	}
	if cfg.WebhookGitHubSecret != "gh_secret" {
		t.Errorf("expected WebhookGitHubSecret to match, got %q", cfg.WebhookGitHubSecret)
	}
	if cfg.WebhookGitLabSecret != "gl_secret" {
		t.Errorf("expected WebhookGitLabSecret to match, got %q", cfg.WebhookGitLabSecret)
	}
	if cfg.DashboardListenAddr != ":8081" {
		t.Errorf("expected DashboardListenAddr to match, got %q", cfg.DashboardListenAddr)
	}
	if cfg.HistoryDBPath != "/custom/path/history.db" {
		t.Errorf("expected HistoryDBPath to match, got %q", cfg.HistoryDBPath)
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

	if cfg.HistoryDBPath != "/var/cache/forgejo-sync/history.db" {
		t.Errorf("expected default HistoryDBPath %q, got %q", "/var/cache/forgejo-sync/history.db", cfg.HistoryDBPath)
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
		"CACHE_DIR",
		"FORGEJO_DATA_DIR",
		"FORGEJO_PUSH_SSH_KEY_PATH",
		"FORGEJO_DOMAIN",
		"FORGEJO_SSH_PORT",
		"FORGEJO_DB_HOST",
		"FORGEJO_DB_PORT",
		"FORGEJO_DB_USER",
		"FORGEJO_DB_PASSWORD",
		"FORGEJO_DB_NAME",
		"FORGEJO_DB_SSLMODE",
		"WEBHOOK_LISTEN_ADDR",
		"FORGEJO_SECRET_KEY",
		"DASHBOARD_LISTEN_ADDR",
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
	env["FORGEJO_SSH_PORT"] = "not_an_int"
	env["FORGEJO_DB_PORT"] = "also_not_an_int"
	env["WORKER_CONCURRENCY"] = "-1"
	env["GIT_COMMAND_TIMEOUT"] = "invalid_duration"
	env["LOG_LEVEL"] = "super_trace"

	_, err := config.LoadFrom(func(k string) string { return env[k] })
	if err == nil {
		t.Fatal("expected errors for invalid values, got nil")
	}

	errStr := err.Error()
	expectedErrors := []string{
		"FORGEJO_SSH_PORT",
		"FORGEJO_DB_PORT",
		"WORKER_CONCURRENCY",
		"GIT_COMMAND_TIMEOUT",
		"LOG_LEVEL",
	}

	for _, field := range expectedErrors {
		if !strings.Contains(errStr, field) {
			t.Errorf("expected error message to mention invalid %s, full error:\n%s", field, errStr)
		}
	}
}
