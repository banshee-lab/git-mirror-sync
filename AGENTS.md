# Forgejo Sync - AGENTS.md

This repository is currently in the v2 architecture:

- single Go binary
- single HTTP listener on `LISTEN_ADDR`
- SQLite app database (`APP_DB_PATH`) for `repositories`, `ssh_keys`, and `sync_events`
- per-repository SSH key references and per-repository webhook secrets
- OIDC login flow behind `/auth/*`
- protected API under `/api/*`
- embedded Angular frontend via `internal/webapp`

## Core packages

- `cmd/forgejo-sync` — app wiring and shutdown flow
- `internal/config` — env validation for v2 settings
- `internal/store` — SQLite-backed repository/key/history models
- `internal/giturl` — SSH normalization for source and target git URLs
- `internal/gitsync` — repository clone/fetch and push operations
- `internal/sshkeys` — SSH key generation and file management
- `internal/reposvc` — repository CRUD and sync-now orchestration
- `internal/worker` — async sync jobs with per-repository locking
- `internal/webhook` — webhook parsing and signature verification
- `internal/auth` — OIDC login, callback, session cookie, RequireAuth middleware
- `internal/api` — protected REST endpoints
- `internal/webapp` — embedded frontend asset serving
- `internal/events` — sync event tracking and history management
- `internal/dashboard` — dashboard functionality for monitoring and management

## Runtime expectations

- Use a single database file, not an additional legacy history DB.
- Do not reintroduce Forgejo-specific Postgres lookups or global webhook secrets.
- Prefer repository-scoped configuration and per-key file paths.
- Keep the frontend build embedded into the Go binary for deployment.
