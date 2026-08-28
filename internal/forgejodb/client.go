package forgejodb

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"forgejo-sync/internal/config"
)

// Client wraps a pgxpool.Pool for querying Forgejo database in read-only mode.
type Client struct {
	pool *pgxpool.Pool
}

// NewClient creates a new Forgejo DB client connected via pgxpool using config.Config.
// It includes retry with exponential backoff on transient connection errors during startup.
func NewClient(ctx context.Context, cfg *config.Config) (*Client, error) {
	dsn := buildDSN(cfg)
	return NewClientWithRetry(ctx, dsn, 5, 500*time.Millisecond)
}

// NewPoolFromDSN parses DSN, configures pgxpool options (statement_timeout, read-only session),
// and connects to the database pool.
func NewPoolFromDSN(ctx context.Context, dsn string) (*Client, error) {
	poolConfig, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to parse database DSN: %w", err)
	}

	// Configure connection settings:
	// Set default statement timeout to 5s to avoid long-running queries locking resources.
	if poolConfig.ConnConfig.RuntimeParams == nil {
		poolConfig.ConnConfig.RuntimeParams = make(map[string]string)
	}
	poolConfig.ConnConfig.RuntimeParams["statement_timeout"] = "5000" // 5 seconds in ms
	poolConfig.ConnConfig.RuntimeParams["default_transaction_read_only"] = "on"

	poolConfig.MaxConns = 10
	poolConfig.MinConns = 1
	poolConfig.MaxConnLifetime = 30 * time.Minute
	poolConfig.MaxConnIdleTime = 5 * time.Minute

	pool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create connection pool: %w", err)
	}

	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	return &Client{pool: pool}, nil
}

// NewClientWithRetry attempts to connect with retry and exponential backoff.
func NewClientWithRetry(ctx context.Context, dsn string, maxRetries int, initialBackoff time.Duration) (*Client, error) {
	var lastErr error
	backoff := initialBackoff

	for attempt := 1; attempt <= maxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		client, err := NewPoolFromDSN(ctx, dsn)
		if err == nil {
			return client, nil
		}

		lastErr = err
		if attempt < maxRetries {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(backoff):
			}
			backoff *= 2
		}
	}

	return nil, fmt.Errorf("could not connect to database after %d attempts: %w", maxRetries, lastErr)
}

// Close closes the underlying connection pool.
func (c *Client) Close() {
	if c != nil && c.pool != nil {
		c.pool.Close()
	}
}

// Pool returns the underlying pgxpool.Pool instance.
func (c *Client) Pool() *pgxpool.Pool {
	if c == nil {
		return nil
	}
	return c.pool
}

// buildDSN formats a PostgreSQL connection URI from config.Config.
func buildDSN(cfg *config.Config) string {
	u := &url.URL{
		Scheme: "postgres",
		User:   url.UserPassword(cfg.ForgejoDBUser, cfg.ForgejoDBPassword),
		Host:   fmt.Sprintf("%s:%d", cfg.ForgejoDBHost, cfg.ForgejoDBPort),
		Path:   cfg.ForgejoDBName,
	}

	q := u.Query()
	if cfg.ForgejoDBSSLMode != "" {
		q.Set("sslmode", cfg.ForgejoDBSSLMode)
	} else {
		q.Set("sslmode", "disable")
	}
	u.RawQuery = q.Encode()

	return u.String()
}
