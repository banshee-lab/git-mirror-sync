# forgejo-sync

A Go service for mirroring repository activity into a Git target over SSH, with an OIDC-protected web UI and a single SQLite-backed app database.

## What it does

- Receives GitHub/GitLab push webhooks.
- Matches the incoming source repo against a configured `repositories` record in the local SQLite database.
- Resolves per-repository SSH key paths from `ssh_keys`.
- Clones/fetches the pull source and pushes the mirror to the configured push URL.
- Records sync outcomes in `sync_events`.
- Exposes a protected Angular SPA, with `/auth/*` login flow and `/api/*` protected endpoints.

## Architecture

- `cmd/forgejo-sync` wires the app together as a single HTTP server.
- `internal/config` validates and loads environment variables for v2 settings.
- `internal/store` owns the SQLite schema for `repositories`, `ssh_keys`, and `sync_events`.
- `internal/giturl` normalizes source and target URLs to the SSH form used by git.
- `internal/gitsync` orchestrates repository clone/fetch and push operations.
- `internal/reposvc` handles repository CRUD plus optional `sync_now` actions.
- `internal/sshkeys` generates and manages SSH key pairs on disk.
- `internal/worker` handles async sync jobs with per-repository locking.
- `internal/webhook` verifies provider signatures against the repository's configured secret.
- `internal/auth` provides OIDC login/session handling.
- `internal/api` exposes protected REST endpoints.
- `internal/webapp` embeds the Angular frontend build into the Go binary.
- `internal/events` tracks and manages sync event history.
- `internal/dashboard` provides dashboard functionality for monitoring and management.

## Frontend

The Angular frontend is located in the `frontend/` directory and is embedded at build time into the Go binary. It is served by `internal/webapp` and communicates with the backend through protected REST endpoints under `/api/*`.

## Data Model

The SQLite database (`APP_DB_PATH`) stores three main entities:

- **repositories** — Configured source and target URLs, webhook secrets, and SSH key references.
- **ssh_keys** — Per-repository SSH key references pointing to disk-managed key files.
- **sync_events** — Historical records of sync outcomes, push results, and errors.

## Required environment

```bash
MODE=production
LISTEN_ADDR=:8080
CACHE_DIR=/var/cache/forgejo-sync
APP_DB_PATH=/var/cache/forgejo-sync/app.db
SSH_KEYS_DIR=/var/cache/forgejo-sync/keys
WORKER_CONCURRENCY=4
GIT_COMMAND_TIMEOUT=10m
LOG_LEVEL=info
# Optional: public base URL used to build the webhook URL shown in the UI (<PUBLIC_URL>/webhook).
# If unset, the UI falls back to the browser origin.
PUBLIC_URL=https://sync.example.com

# Production OIDC settings (ignored in MODE=development)
OIDC_ISSUER_URL=https://id.example.com
OIDC_CLIENT_ID=forgejo-sync
OIDC_CLIENT_SECRET=replace-me
OIDC_REDIRECT_URL=http://localhost:8080/auth/callback
OIDC_SCOPES=openid profile email
OIDC_PROVIDER_NAME=PocketID
SESSION_SECRET=replace-with-a-32-byte-random-secret
SECRET_KEY=replace-with-a-32-byte-random-secret-used-for-webhook-encryption
SESSION_COOKIE_NAME=forgejo_sync_session
SESSION_TTL=24h
```

When `MODE=development`, the app skips the external OIDC provider and automatically signs in a `Development User` session so the full UI and API can be exercised locally without a real identity provider. The login route still works for manual testing, but it does not require an external provider.

## Run

```bash
cp .env.example .env
# fill in values, then
go build ./...
go test ./...
```

Or run the app directly with the required env vars loaded.

## Notes

- The frontend is embedded at build time and served from the Go binary.
- Webhook verification is repository-scoped rather than a global secret map.
- The app database is the single source of truth for repositories, SSH keys, and sync history.
---

## Disclaimer

This project was designed, architected, and reviewed by a human — a
software developer with 20+ years of experience — but the code itself
was written by an agentic AI. The plan, structure, and every design
decision were specified and checked by a human; the implementation was
generated rather than hand-typed, because at this point there's little
value in typing out code by hand when the same result can be produced,
reviewed, and iterated on in a fraction of the time.

This is a personal project, and I don't plan to maintain it unless
I found something that affects my workflow or I need a new feature.
