# Forgejo Sync Tool

A Go service that listens for webhooks from GitHub/GitLab (and other
providers, pluggable), matches the repository against a Forgejo
instance's `push_mirror` configuration, mirrors the remote repository
into a local cache, pushes it to Forgejo over SSH, and records the
outcome in an internal SQLite history database exposed through a small
read-only web dashboard.

## Why

Forgejo doesn't support bidirectional syncing: it can push changes out
to a remote (`push_mirror`), but it has no equivalent for pulling
changes in from a remote in response to activity there. This project
closes that gap.

Forgejo remains the **main source of truth**. Forgejo's own
`push_mirror` handles the outbound direction as normal (Forgejo →
GitHub/GitLab). This tool handles the missing inbound direction: it
listens for webhooks from GitHub/GitLab, and when something changes
there, mirrors it back into Forgejo — effectively performing the "pull
mirror" half that Forgejo itself doesn't implement. Together, the two
halves give you near-real-time two-way sync between Forgejo and an
external GitHub/GitLab remote.

Design goals:

- **Provider parsing is pluggable** — adding a new webhook source
  (Gitea, Bitbucket, ...) means adding one file, not touching core
  logic.
- **Reuses Forgejo's existing push-mirror credentials** instead of
  requiring the operator to configure credentials twice.
- **Safe by default** — never force-pushes over a diverging history;
  conflicts are recorded, not silently resolved.
- **Observable** — every webhook received results in a history row,
  visible on an unauthenticated dashboard (last 30 events).

## How it works

```
GitHub/GitLab webhook
        │
        ▼
Webhook listener (signature verification, provider parsing)
        │       (this is the "pull mirror" half; Forgejo's own
        │        push_mirror covers the opposite direction)
        │
        ▼
Forgejo DB lookup (match remote URL against push_mirror config)
        │  no match → recorded as "skipped"
        ▼
Credential resolver (reuses Forgejo's push_mirror SSH key / HTTP creds)
        │
        ▼
Git sync engine (mirror clone / fetch --prune into local cache)
        │
        ▼
Forgejo pusher (push --mirror over SSH; conflicts recorded, never forced)
        │
        ▼
SQLite history store ──────────▶ Dashboard (last 30 events)
```

Every webhook is answered with `202 Accepted` immediately; the actual
sync work runs asynchronously on a bounded worker pool, with a per-repo
lock so concurrent pushes to the same repo can't race each other.

## Requirements

- Go 1.27+ (only needed for building from source)
- A running Forgejo instance with:
  - one or more repositories configured with a `push_mirror` pointing at
    the corresponding GitHub/GitLab remote
  - read-only access to its Postgres database
  - read-only access to its data directory (for HTTPS mirror
    credentials)
  - a dedicated SSH deploy key with push access, separate from the
    push-mirror key
- Docker + Docker Compose (for the containerized run)

## Configuration

All configuration is via environment variables, validated at startup —
the process fails fast with a clear error if anything required is
missing.

| Variable | Required | Description |
|---|---|---|
| `CACHE_DIR` | yes | Root of the bare-mirror cache (read/write) |
| `FORGEJO_DATA_DIR` | yes | Forgejo's data dir, for reading repo git configs (read-only) |
| `FORGEJO_PUSH_SSH_KEY_PATH` | yes | Private key used to push to Forgejo (read-only file) |
| `FORGEJO_DOMAIN` | yes | Forgejo hostname for the push SSH URL |
| `FORGEJO_SSH_PORT` | yes | Forgejo SSH port |
| `FORGEJO_DB_HOST` / `_PORT` / `_USER` / `_PASSWORD` / `_NAME` / `_SSLMODE` | yes | Postgres connection (read-only role) |
| `WEBHOOK_LISTEN_ADDR` | yes | e.g. `:8080` |
| `WEBHOOK_GITHUB_SECRET` | yes | HMAC secret for GitHub signature validation |
| `WEBHOOK_GITLAB_SECRET` | yes | Static token for GitLab validation |
| `HISTORY_DB_PATH` | no | SQLite file path (default under `CACHE_DIR`) |
| `DASHBOARD_LISTEN_ADDR` | yes | e.g. `:8081` |
| `WORKER_CONCURRENCY` | no | Worker pool size (default 4) |
| `GIT_COMMAND_TIMEOUT` | no | Per git-command timeout (default `10m`) |
| `LOG_LEVEL` | no | `debug`/`info`/`warn`/`error` (default `info`) |

## Running with Docker Compose (recommended)

1. Copy the example env file and fill in your values:

   ```bash
   cp .env.example .env
   ```

2. Bring up the stack (service + a Postgres fixture seeded with sample
   `repository`/`push_mirror` rows, so you can test the lookup path
   without a real Forgejo instance):

   ```bash
   docker-compose up
   ```

3. Point a GitHub or GitLab webhook at `http://<host>:8080/`, using the
   secret configured in `WEBHOOK_GITHUB_SECRET` / `WEBHOOK_GITLAB_SECRET`.

4. Watch sync results on the dashboard at `http://<host>:8081/`.

An optional `forgejo` service is included (commented out / behind a
compose profile) for full end-to-end testing against a real Forgejo
instance.

## Running from source

```bash
go build -o forgejo-sync ./cmd/forgejo-sync
CACHE_DIR=... FORGEJO_DATA_DIR=... ... ./forgejo-sync
```

Set every required environment variable from the table above first.

## Testing

```bash
go test ./...
```

Covers provider signature verification, URL normalization, DB lookup
(integration, requires a throwaway Postgres), the git sync engine
(including conflict detection with no force-push), the worker pool's
per-repo locking, and dashboard rendering.

## Project status / non-goals (v1)

- No automatic conflict resolution — a diverging history is recorded and
  surfaced on the dashboard for a human to resolve manually. A future
  `--allow-force` flag is a possible v2 addition, intentionally left out
  to avoid silent data loss.
- The dashboard is intentionally unauthenticated and shows only sync
  metadata — no credentials are ever stored or rendered.
- History retention: events older than 30 days are pruned; the dashboard
  always shows at most the last 30.


---

## Disclaimer

This project was designed, architected, and reviewed by a human — a
software developer with 20+ years of experience — but the code itself
was written by an agentic AI. The plan, structure, and every design
decision were specified and checked by a human; the implementation was
generated rather than hand-typed, because at this point there's little
value in typing out code by hand when the same result can be produced,
reviewed, and iterated on in a fraction of the time.
