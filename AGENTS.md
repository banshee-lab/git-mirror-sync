# Forgejo Sync - AGENTS.md

Welcome to `forgejo-sync`. This document is the source of truth for any future agent (human or AI) contributing to this project. It reflects the *actual* implemented system architecture and rules.

## 1. Project Summary

`forgejo-sync` is a Go service designed to listen for incoming webhooks from external Git providers (like GitHub or GitLab) and automatically mirror repository pushes to a Forgejo instance. When a webhook is received, it matches the repository against Forgejo's database to find an existing `push_mirror` configuration. If matched, it uses Forgejo's own stored credentials to fetch the latest changes into a local bare-mirror cache, and then pushes those changes to Forgejo over SSH.

The tool is built to be "safe by default" and highly observable. It never force-pushes if the Forgejo repository has diverged (e.g., someone pushed directly to Forgejo), ensuring no data is accidentally lost. Instead, it records every sync outcome (success, failure, or conflict) in an internal SQLite database, which can be viewed via a lightweight, unauthenticated web dashboard.

## 2. Repository Layout

Here is the structure of the repository and the purpose of each package based on what was built:

| Package / File | Purpose |
| --- | --- |
| `cmd/forgejo-sync/` | Application entry point, configures wiring, sets up servers, and manages graceful shutdown. |
| `internal/config/` | Environment variable loading, validation, and defaults management (`Config` struct). |
| `internal/webhook/` | Pluggable provider interface, GitHub/GitLab webhook parsers, and the HTTP server. |
| `internal/forgejodb/` | Read-only Postgres client and URL normalization for matching against Forgejo's `push_mirror`. |
| `internal/gitsync/` | Credential resolution, `git clone/fetch --prune`, and `git push --mirror` operations with conflict handling. |
| `internal/history/` | SQLite database schema, `SyncEvent` models, querying, and retention pruning (runs automatically). |
| `internal/dashboard/` | Read-only web UI handlers, with embedded `html/template` layouts and static assets (CSS). |
| `internal/worker/` | Bounded worker pool handling the queue of sync jobs, ensuring per-repository locking to prevent races. |
| `Dockerfile` | Multi-stage build producing a lightweight Alpine image with `git` and `openssh-client` installed. |
| `docker-compose.yml` | Local development environment that runs `forgejo-sync` and a seeded `postgres` fixture database. |
| `fixtures/` | Contains test data (e.g., `init.sql` for Postgres, fake SSH keys, Forgejo data directory mocks). |

## 3. How to Build and Run

- **Build**: `go build ./...`
- **Test**: `go test ./...`
- **Run Locally (Docker)**: `docker-compose up` will start the service and a Postgres database pre-seeded with fixtures.
- **Run Locally (Go)**: Requires setting all required environment variables listed below.

### Simulating a Webhook

You can trigger a webhook manually for local testing. Here is a curl example for GitHub (ensure `WEBHOOK_GITHUB_SECRET` matches the signature):

```bash
# Example for GitHub (You will need to generate a valid HMAC-SHA256 signature for the payload)
curl -v -X POST http://localhost:8080/ \
  -H "Content-Type: application/json" \
  -H "X-GitHub-Event: push" \
  -H "X-Hub-Signature-256: sha256=VALID_HEX_SIGNATURE" \
  -d '{"repository": {"clone_url": "https://github.com/owner/repo.git", "full_name": "owner/repo"}}'
```

## 4. Shared Contracts

These core types and constants represent shared boundaries between packages. **DO NOT** silently change these without updating all consumers.

- **`webhook.RepoInfo`**: The exact fields passed from webhook parsers to the worker pool: `Provider`, `FullName`, `Owner`, `Name`, `Private`, `HTMLURL`, `SSHURL`, `CloneURL`, `Ref`, `Before`, `After`.
- **`history.SyncEvent`**: The model written by the worker and read by the dashboard. No credentials or secrets should ever exist on this struct. Fields: `ID`, `ReceivedAt`, `FinishedAt`, `Provider`, `RepoFullName`, `CloneURL`, `SSHURL`, `ForgejoOwner`, `ForgejoRepo`, `Ref`, `CommitBefore`, `CommitAfter`, `Status`, `Detail`, `DurationMS`.
- **`history.Status` enum**: Exactly one of: `received`, `skipped`, `success`, `conflict`, `error`. No other strings are supported.
- **`forgejodb.MatchedRepo`**: The result of a Postgres lookup. Contains `RepoID`, `OwnerName`, `RepoName`, `PushMirrorID`, `RemoteAddress`, and the `PrivateKey` (bytea).
- **`gitsync.ErrConflict`**: A typed error returned by the push engine when a non-fast-forward rejection happens. The system relies on this type to explicitly record a `conflict` status rather than a generic `error`.
- **`config.Config`**: Centralized struct for all environment configurations. No package should call `os.Getenv` directly outside of `internal/config`.

## 5. Configuration Reference

The service is configured entirely via environment variables.

| Variable | Required? | Default | Description |
| --- | --- | --- | --- |
| `CACHE_DIR` | Yes | - | Root directory for storing bare-mirror git caches. |
| `FORGEJO_DATA_DIR` | Yes | - | Path to Forgejo's data directory (read-only) for fetching HTTPS credentials. |
| `FORGEJO_PUSH_SSH_KEY_PATH` | Yes | - | Path to the private SSH key used to push to Forgejo. |
| `FORGEJO_DOMAIN` | Yes | - | Forgejo hostname for constructing the push SSH URL. |
| `FORGEJO_SSH_PORT` | Yes | - | Forgejo SSH port (integer). |
| `FORGEJO_DB_HOST` | Yes | - | Postgres host. |
| `FORGEJO_DB_PORT` | Yes | - | Postgres port (integer). |
| `FORGEJO_DB_USER` | Yes | - | Postgres user (should be a read-only role). |
| `FORGEJO_DB_PASSWORD` | Yes | - | Postgres password. |
| `FORGEJO_DB_NAME` | Yes | - | Postgres database name. |
| `FORGEJO_DB_SSLMODE` | Yes | - | Postgres SSL mode. |
| `WEBHOOK_LISTEN_ADDR` | Yes | - | Listen address for the webhook HTTP server (e.g., `:8080`). |
| `WEBHOOK_GITHUB_SECRET` | Yes | - | HMAC secret for GitHub signature validation. |
| `WEBHOOK_GITLAB_SECRET` | Yes | - | Static token for GitLab header validation. |
| `DASHBOARD_LISTEN_ADDR` | Yes | - | Listen address for the dashboard HTTP server (e.g., `:8081`). |
| `HISTORY_DB_PATH` | No | `$CACHE_DIR/history.db` | File path for the SQLite database. |
| `WORKER_CONCURRENCY` | No | `4` | Number of concurrent workers processing syncs. |
| `GIT_COMMAND_TIMEOUT` | No | `10m` | Maximum duration a git command is allowed to run. |
| `LOG_LEVEL` | No | `info` | Logging level (`debug`, `info`, `warn`, `error`). |

## 6. Known Non-Goals (Out of Scope for v1)

- **No `--allow-force` flag**: `forgejo-sync` intentionally *never* force-pushes on conflict to avoid silent data loss. Conflicts must be manually resolved by an operator.
- **Dashboard is unauthenticated**: The dashboard intentionally lacks authentication. It never displays secrets or credentials. It is purely read-only observability.
- **No web UI for configuration**: You cannot configure mirrored repos from this tool. All mirror configurations must be set up natively within Forgejo's `push_mirror` settings.

## 7. Security Notes

- SSH keys fetched from the database are written to `0600` temporary files within `CACHE_DIR` and are *always* deferred for cleanup (`os.Remove`) immediately after the process ends.
- To prevent path traversal attacks, owner and repository names are validated against strict regex patterns (`^[A-Za-z0-9._-]+$`) before being used in filesystem paths.
- Execution errors from `git` commands are sanitized to prevent credential leaking in the database or logs.
- The PostgreSQL database connection should always use a read-only role.
