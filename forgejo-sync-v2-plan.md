# forgejo-sync v2 — Conversion Plan (Backend + Angular Frontend + OIDC)

## Status snapshot (2026-08-29)

- Completed: `internal/giturl` package, the SQLite-backed `internal/store` package, the SSH-only compatibility layer in `internal/gitsync`, the key-generation manager in `internal/sshkeys`, and the repository orchestration service in `internal/reposvc`.
- Current in-progress work: final worker compatibility pass and webhook/repository-secret alignment (`v2-worker`, `v2-webhook-secret`).
- Next ready tasks: finish the local-repository matcher and per-repo secret verification, then move on to OIDC, API handlers, Angular SPA, and Docker/CI modernization.
- Validation note: the Go-based backend packages exercise cleanly in the repo; the only remaining test issue is an environment-specific Git setting (`safe.bareRepository=explicit`) in this runtime, which blocks the legacy bare-repo test harness from running a direct `git log`/`git branch` inside a bare repo. This is a runtime constraint, not a repo-logic regression in the service code.

## 0. Context — what exists today

Current repo (`forgejo-sync`) is a Go service with two HTTP listeners:

- **Webhook server** (`internal/webhook`): receives GitHub/GitLab push webhooks, verifies signature, records a `history.SyncEvent`, enqueues a job.
- **Worker pool** (`internal/worker`): looks up the repo in the **Forgejo Postgres DB** (`internal/forgejodb`, table `push_mirror`/`repository`), decrypts the stored private key (`internal/gitsync/decrypt.go`, AES-CFB using `FORGEJO_SECRET_KEY`), clones/fetches the source repo (`internal/gitsync/clone.go`) using either the decrypted SSH key or credentials scraped from Forgejo's on-disk git config (`ExtractHTTPCredentials`), then pushes to Forgejo over SSH using a single global `FORGEJO_PUSH_SSH_KEY_PATH` (`internal/gitsync/push.go`).
- **Dashboard server** (`internal/dashboard`): unauthenticated `html/template` UI showing the last 30 `sync_events` from a local SQLite `history.db`.
- **Config** (`internal/config`): flat struct, all env vars, required-or-fail validation.

Hard dependencies to remove: `internal/forgejodb` (Postgres, read-only role into Forgejo's own DB), `internal/gitsync/decrypt.go` (Forgejo secret-key AES decryption), `FORGEJO_DATA_DIR` scraping in `credentials.go`, and the global single push key / single push target derived from `FORGEJO_DOMAIN`.

Everything else (webhook parsing, worker pool concurrency model, conflict detection, history schema/pruning) is reusable and should be preserved and adapted rather than rewritten from scratch.

## 1. Target architecture

```
┌─────────────────────────────┐        ┌──────────────────────────────┐
│ Angular 20 SPA (dist/)       │  HTTPS │ Go backend (single binary)    │
│ - served as static files by  │◄──────►│ - net/http chi/stdlib router   │
│   the Go backend             │        │ - /api/**  (JWT-protected)     │
│ - OIDC login redirect flow   │        │ - /auth/** (OIDC login/callback)│
└─────────────────────────────┘        │ - /webhook (HMAC-protected,     │
                                        │   no OIDC — external callers)   │
                                        │ - /healthz (public)             │
                                        │ - static file server (SPA)      │
                                        └──────────────┬───────────────┘
                                                        │
                        ┌───────────────────────────────┼─────────────────────────┐
                        ▼                                ▼                         ▼
                 local SQLite DB                 local SSH key folder      remote git repos
                 (repos, ssh_keys,                (0600 private keys,      (source + Forgejo,
                  history, webhook secrets)         one file per key)       both over SSH)
```

Key decisions:

- **One binary, one process, one listen address** (`LISTEN_ADDR`). Drop the two-server split (webhook vs dashboard); everything is one `http.Server` with a router that mounts `/api`, `/auth`, `/webhook`, `/healthz`, and a catch-all SPA handler serving the embedded (or on-disk) Angular build.
- **Single local SQLite database** (or a second file — see §3.6) replaces Postgres entirely. No more `internal/forgejodb`.
- **Per-repository credentials** stored locally (SSH key *references*, not the keys themselves duplicated) instead of pulled from Forgejo's own DB/data dir.
- **OIDC authentication** in front of everything except `/webhook/*` (which keeps HMAC/token auth, since external providers can't do OIDC) and `/healthz`.
- **SOLID structure**: introduce interfaces at every package boundary (repository store, key store, git executor, OIDC verifier, clock) so the worker/services depend on abstractions, not concrete DB/exec types — this also makes the existing `worker` package's testing style (it already uses interfaces like `ForgejoDB`, `HistoryStore`, `GitSyncer`) the pattern to extend project-wide.

## 2. New environment variables

Replace the Forgejo-DB and secret-decryption variables. Keep webhook, worker, logging, history vars mostly as-is.

| Variable | Required | Default | Purpose |
|---|---|---|---|
| `LISTEN_ADDR` | Yes | — | Single HTTP listen address (replaces `WEBHOOK_LISTEN_ADDR` + `DASHBOARD_LISTEN_ADDR`) |
| `CACHE_DIR` | Yes | — | Bare-mirror git cache root (unchanged) |
| `APP_DB_PATH` | No | `${CACHE_DIR}/app.db` | Single SQLite DB for repositories, ssh keys, and sync history |
| `SSH_KEYS_DIR` | Yes | — | Folder where generated/stored SSH private+public keys live (replaces `FORGEJO_PUSH_SSH_KEY_PATH`) |
| `WORKER_CONCURRENCY` | No | 4 | unchanged |
| `GIT_COMMAND_TIMEOUT` | No | 10m | unchanged |
| `LOG_LEVEL` | No | info | unchanged |
| `OIDC_ISSUER_URL` | Yes | — | e.g. `https://id.example.com` (PocketID or any OIDC issuer) |
| `OIDC_CLIENT_ID` | Yes | — | |
| `OIDC_CLIENT_SECRET` | Yes | — | |
| `OIDC_REDIRECT_URL` | Yes | — | e.g. `https://sync.example.com/auth/callback` |
| `OIDC_SCOPES` | No | `openid profile email` | |
| `OIDC_PROVIDER_NAME` | No | `PocketID` | Display name used by the frontend button ("Login using PocketID") |
| `SESSION_SECRET` | Yes | — | 32+ byte secret for signing the stateless session cookie |
| `SESSION_COOKIE_NAME` | No | `forgejo_sync_session` | |
| `SESSION_TTL` | No | `24h` | |

Remove entirely: `FORGEJO_DATA_DIR`, `FORGEJO_PUSH_SSH_KEY_PATH`, `FORGEJO_DOMAIN`, `FORGEJO_SSH_PORT`, `FORGEJO_DB_*`, `FORGEJO_SECRET_KEY`, `DASHBOARD_LISTEN_ADDR`, `WEBHOOK_LISTEN_ADDR`, `HISTORY_DB_PATH` (folded into `APP_DB_PATH`), `WEBHOOK_GITHUB_SECRET`, `WEBHOOK_GITLAB_SECRET` (webhook secrets are now exclusively per-repository, set via the ABM — no global fallback), `FRONTEND_DIST_DIR` (frontend is always embedded via `go:embed`, no runtime directory option).

Update `config.go`, `config_test.go`, `.env.example`, `docker-compose.yml`, `AGENTS.md`, `README.md` accordingly.

## 3. Backend — detailed task list

### 3.1 URL normalization helper (new package `internal/giturl`)

Implement a pure function used both by the ABM "add repository" flow and anywhere a source URL needs to be turned into the SSH form actually used for git operations.

Rules (from the request, generalized):

1. Parse the input as `git@host:owner/repo(.git)?`, `ssh://[user@]host[:port]/owner/repo(.git)?`, `https://host/owner/repo(.git)?`, or `http://host/owner/repo(.git)?`.
2. Extract `host`, optional `port`, and `path` (owner/repo, may have more path segments for GitLab subgroups — preserve all segments).
3. Strip a trailing `.git` if present, then always re-append exactly one `.git`.
4. Re-emit as `ssh://git@host[:port]/<path>.git` (default git user `git`, configurable per-repo if a provider needs a different SSH user — expose an optional field, default `git`).
5. Cover the three examples from the prompt as table-driven unit tests, plus: URLs with credentials embedded, URLs with non-standard ports, GitLab nested groups (`group/subgroup/repo`), and invalid input (return a typed `ErrInvalidRepoURL`).

```go
func NormalizeToSSH(raw string, sshUser string) (string, error)
```

Write this as the very first coding step — everything else (repository ABM, sync engine) depends on it, and it's the easiest piece to fully unit-test in isolation.

### 3.2 Local data model (new package `internal/store`, SQLite via `modernc.org/sqlite`, already a dependency)

A single SQLite file (`APP_DB_PATH`, one `.db`) holds all three tables — `repositories`, `ssh_keys`, and `sync_events` (the latter carried over from `internal/history`'s existing schema, just relocated into the same file/package). One `Store`, one migration path, one backup target.

```sql
CREATE TABLE repositories (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    name                TEXT NOT NULL UNIQUE,        -- display name, user-provided or derived
    pull_url_raw        TEXT NOT NULL,               -- exactly what the user typed for the pull side
    pull_url_ssh        TEXT NOT NULL,               -- normalized ssh://... form used for pulls
    pull_ssh_key_id     INTEGER NOT NULL REFERENCES ssh_keys(id),
    push_url_raw        TEXT NOT NULL,               -- exactly what the user typed for the push side
    push_url_ssh        TEXT NOT NULL,               -- normalized ssh://... form used for pushes
    push_ssh_key_id     INTEGER NOT NULL REFERENCES ssh_keys(id),
    webhook_secret_hash TEXT NOT NULL,                -- store a hash, not the raw secret
    provider_hint       TEXT,                         -- github|gitlab|generic, for webhook signature verification
    enabled             INTEGER NOT NULL DEFAULT 1,
    created_at          TIMESTAMP NOT NULL,
    updated_at          TIMESTAMP NOT NULL
);

CREATE TABLE ssh_keys (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL UNIQUE,
    key_type     TEXT NOT NULL,        -- rsa | ed25519
    private_path TEXT NOT NULL,        -- path under SSH_KEYS_DIR, 0600, OpenSSH-format PEM
    public_path  TEXT NOT NULL,
    fingerprint  TEXT NOT NULL,
    created_at   TIMESTAMP NOT NULL
);

-- sync_events: carried over unchanged from internal/history/sqlite.go's existing schema,
-- just living in the same app.db file instead of a separate history.db.
```

Both `pull_url` and `push_url` are modeled identically — either can be any SSH git repository the configured key has access to; there is no assumption that either side is a Forgejo instance specifically.

Security note: store **only paths** to key files on disk (never key material in the DB), protect `SSH_KEYS_DIR` with filesystem permissions (0700 dir, 0600 files, owned by the service user), and store webhook secrets **hashed** (bcrypt/argon2) for verification, with the raw value shown to the user exactly once at creation time (standard "copy this now" UX) — mirrors how GitHub/GitLab themselves handle webhook secrets. This avoids reintroducing a bespoke encryption scheme while still not leaving secrets in plaintext at rest beyond what's unavoidable (the private key files themselves, which SSH requires to be plaintext PEM on disk anyway — this is no worse than the current `FORGEJO_PUSH_SSH_KEY_PATH` model).

Implement `internal/store` with two focused interfaces (Interface Segregation):

```go
type RepositoryStore interface {
    Create(ctx, Repository) (Repository, error)
    Update(ctx, Repository) (Repository, error)
    Delete(ctx, id int64) error
    Get(ctx, id int64) (Repository, error)
    List(ctx) ([]Repository, error)
}

type SSHKeyStore interface {
    Create(ctx, SSHKey) (SSHKey, error)
    Delete(ctx, id int64) error
    Get(ctx, id int64) (SSHKey, error)
    List(ctx) ([]SSHKey, error)
}
```

Concrete SQLite implementations satisfy these; the service/handler layer never imports `database/sql` directly (Dependency Inversion).

### 3.3 SSH key management (new package `internal/sshkeys`)

- `GenerateKeyPair(keyType KeyType, name string) (SSHKey, error)` — use `golang.org/x/crypto/ssh` + `crypto/ed25519` / `crypto/rsa` (add these deps to `go.mod`) to generate keys in-process (no shelling out to `ssh-keygen`, for portability and testability). Write private key as PEM (OpenSSH or PKCS#8 format — prefer OpenSSH format via `golang.org/x/crypto/ssh` marshaling for direct compatibility with `git`/`ssh`), public key in `authorized_keys` line format.
- Enforce unique `name`, write files to `SSH_KEYS_DIR/<uuid or slug>` (never trust user-supplied names as filenames — generate an internal filename, keep the human name only in the DB).
- `GetPublicKey(id) (string, error)` — read and return the public key line for display/copy in the UI ("retrieve the public key" requirement) — this is what the user pastes into GitHub/GitLab/target repo's deploy keys.
- `Delete(id)` should refuse if the key is referenced by any repository (return a clear conflict error) — surfaced to the UI as "this key is in use by repositories: X, Y".

### 3.4 Repository ABM service (new package `internal/reposvc`)

Orchestrates create/update/delete/list and the "sync now" action, built on `giturl`, `store`, `sshkeys`, and the existing `gitsync` package (adapted per §3.5).

`CreateRepository` input DTO fields, matching the request 1:1:
- `name` (display name)
- `pull_url` (raw, any of the 3 formats) → normalized via `giturl.NormalizeToSSH`
- `pull_ssh_key_id`
- `push_url` (raw, any of the 3 formats — any SSH git repository, not assumed to be Forgejo) → normalized via `giturl.NormalizeToSSH`
- `push_ssh_key_id`
- `webhook_secret` (plaintext in the request only; hashed before storage)
- `sync_now` (bool) — if true, after saving, synchronously (or via the existing worker queue) triggers one pull+push cycle and returns the resulting history event id so the frontend can poll/display it.

Validation: normalize both URLs first (reject with 422 + explanation on `ErrInvalidRepoURL`), verify both SSH key IDs exist, reject duplicate `name`.

### 3.5 Adapt `gitsync` to per-repository credentials

- Delete `internal/gitsync/decrypt.go` entirely.
- Delete the `ExtractHTTPCredentials`/Forgejo-data-dir branch in `credentials.go`; the "HTTP/HTTPS remote" code path in `clone.go` goes away too since every remote is now normalized to SSH before it ever reaches `gitsync` (§3.1) — this actually simplifies `SyncRepository` significantly (drop `ForgejoDataDir`, `MatchedRepo`, `ForgejoSecret` fields from `SyncOptions`).
- `ResolveSSHCredentials` changes signature from `(cacheDir, *forgejodb.MatchedRepo, secret)` to `(cacheDir, privateKeyPath string)` — it now just builds the `GIT_SSH_COMMAND` pointing at a real on-disk key path (from `sshkeys` store), no decryption, no temp file (the persistent key file already has correct 0600 perms — no need to copy it to a temp file per sync, which was only necessary before because the key came from an encrypted DB blob).
- `PushToForgejo` is renamed to `PushToRemote` (the destination is no longer assumed to be Forgejo) and takes the repository's `push_url_ssh` and `push_ssh_key_id`-resolved key path directly, instead of `ForgejoDomain`/`ForgejoSSHPort`/global key path.
- Keep `SanitizeError`, conflict detection (`ErrConflict`, `conflictIndicators`, `shaRangeRegex`) unchanged — this logic is provider-agnostic and still correct.

### 3.6 Worker pool adaptation

- Replace the `ForgejoDB` interface/dependency with a `RepositoryStore`-backed lookup: instead of `LookupRepository(cloneURL, sshURL)` against Postgres, match the incoming webhook's clone/ssh URL against `repositories.pull_url_ssh` (normalize the *incoming* webhook URL the same way via `giturl.NormalizeToSSH` and compare, or store enough normalized variants to match — reuse the existing `NormalizeRemote` candidate-generation idea from `forgejodb/repository.go`, just point it at the local SQLite table instead of Postgres).
- `processJob` now: look up local repo row → resolve pull key path via `SSHKeyStore.Get(pull_ssh_key_id)` → `gitsync.SyncRepository` (SSH-only now, pulling from `pull_url_ssh`) → resolve push key path via `SSHKeyStore.Get(push_ssh_key_id)` → `gitsync.PushToRemote` (pushing to `push_url_ssh`) → record history exactly as today.
- Per-repo locking (`getRepoLock` keyed by `matchedRepo.RepoID`) stays, just keyed by the local `repositories.id` now.
- `history.db` and `app.db` are merged into one SQLite file (§3.2) — the worker's `HistoryStore` interface and the new `RepositoryStore`/`SSHKeyStore` interfaces are all backed by the same underlying `*sql.DB`/connection, just separate Go interfaces per Interface Segregation.

### 3.7 Webhook signature verification

`WEBHOOK_GITHUB_SECRET`/`WEBHOOK_GITLAB_SECRET` global env vars are removed entirely — no fallback, no default. Every repository has its own webhook secret, set via the ABM at creation and stored hashed (`repositories.webhook_secret_hash`). `webhook/server.go`'s `handleWebhook` needs to: parse payload first to identify the repo (as it does today), look up the matched repository, then verify the signature using **that repository's** secret instead of a global map. Keep the existing `Provider` interface (`VerifySignature(secret, header, body)`) — only the secret lookup changes from `map[string]string` (2 global entries) to a per-repository DB fetch. Since one repository maps to exactly one provider (§7, decision 7), also store `provider_hint` (`github`/`gitlab`) on the repository row so the registry match can be cross-checked against the expected provider for that repo, not just header sniffing.

`/webhook/*` routes stay **outside** the OIDC-protected route group (external providers can't do an interactive login) but must remain behind their existing signature/HMAC verification — do not accidentally leave them open when wiring the new auth middleware.

### 3.8 OIDC authentication (new package `internal/auth`)

- Use `github.com/coreos/go-oidc/v3` + `golang.org/x/oauth2` (add to `go.mod`) — standard, well-maintained, generic-provider-compatible (works with PocketID, Authentik, Keycloak, Authelia, Zitadel, Auth0, etc. — anything spec-compliant), so avoid hardcoding anything PocketID-specific beyond the display name.
- Endpoints:
  - `GET /auth/login` — builds authorization URL (with PKCE + `state` + `nonce` cookies), redirects.
  - `GET /auth/callback` — exchanges code, verifies ID token, extracts claims (sub, email, name), issues a signed session cookie (HMAC or JWT signed with `SESSION_SECRET`), redirects to frontend root.
  - `POST /auth/logout` — clears session cookie (and optionally calls the OIDC provider's end-session endpoint if advertised in discovery).
  - `GET /auth/config` — **public**, tiny JSON `{ "provider_name": "PocketID", "login_url": "/auth/login" }` so the Angular app can render the "Login using PocketID" button without hardcoding the name.
  - `GET /api/me` — protected, returns the current user's claims for the frontend header/profile display.
- Middleware `RequireAuth` wraps every `/api/**` route (chi middleware or stdlib wrapper); on missing/invalid/expired session, returns 401 JSON (SPA handles redirect to `/auth/login`), not an HTML redirect (keep API vs browser-nav concerns separate).
- Session storage: stateless signed cookie (JWT or `securecookie`-style) is sufficient for a single-instance app; no server-side session store needed unless the user wants server-side revocation — flag as an option, default to stateless.
- CSRF: since this is a same-origin SPA using cookies, add `SameSite=Lax` (or `Strict`) on the session cookie and a CSRF double-submit token for state-changing `/api` requests if the cookie is also readable cross-site — recommend `SameSite=Strict` + no CSRF token needed for the common single-origin deployment, but document the trade-off.

### 3.9 HTTP API surface (new package `internal/api`, mounted under `/api`)

All protected by `RequireAuth` except where noted.

```
GET    /auth/config                     (public)
GET    /auth/login                      (public)
GET    /auth/callback                   (public)
POST   /auth/logout
GET    /api/me

GET    /api/repositories
POST   /api/repositories                { name, pull_url, pull_ssh_key_id,
                                           push_url, push_ssh_key_id,
                                           webhook_secret, sync_now }
GET    /api/repositories/{id}
PUT    /api/repositories/{id}
DELETE /api/repositories/{id}
POST   /api/repositories/{id}/sync      trigger pull+push now, returns { event_id }
GET    /api/repositories/{id}/webhook-secret/regenerate   (optional convenience)

GET    /api/ssh-keys
POST   /api/ssh-keys                    { name, key_type: rsa|ed25519 }
GET    /api/ssh-keys/{id}
GET    /api/ssh-keys/{id}/public-key    returns { public_key: "ssh-ed25519 AAAA... name" }
DELETE /api/ssh-keys/{id}

GET    /api/history?limit=30            existing dashboard data, as JSON now

POST   /webhook                         (public, HMAC/token verified per-repo — unchanged path/shape)
GET    /healthz                         (public)
```

Response envelope: consistent JSON error shape `{ "error": { "code": "...", "message": "..." } }`; use proper HTTP status codes (400/401/404/409/422/500) so the Angular app can branch cleanly.

### 3.10 Serving the SPA

- `internal/webapp` (extend/rename `internal/dashboard`, since it's no longer a server-rendered dashboard): the Angular production build is embedded via `go:embed` directly into the binary (matches current `dashboard` package's `go:embed` pattern already in the codebase) — single artifact, no runtime frontend directory.
- Catch-all route: any non-`/api`, non-`/auth`, non-`/webhook`, non-`/healthz` GET request serves `index.html` (Angular client-side routing support) with a `no-cache` header; actual hashed asset files get long-lived cache headers.
- Delete `internal/dashboard/templates/*.html` and `static/app.css` (server-rendered dashboard goes away, replaced by the SPA's own history view calling `GET /api/history`).

### 3.11 SOLID refactor pass (cross-cutting, do last as a review pass)

Concretely, for this codebase:
- **S**: split `internal/api` handlers into one file per resource (`repositories.go`, `sshkeys.go`, `history.go`, `auth.go`), each handler struct depending only on the interface(s) it needs.
- **O**: `webhook.Provider` interface already supports this (adding a new provider = new implementation, no edits to `server.go`) — keep and extend for e.g. a generic/Bitbucket provider later.
- **L**: ensure `RepositoryStore`/`SSHKeyStore`/`HistoryStore` mock/fake implementations used in tests are fully substitutable without special-casing.
- **I**: keep the worker's existing small-interface style (`ForgejoDB`→ rename `RepoLookup`, `HistoryStore`, `GitSyncer`) — don't collapse into one fat interface.
- **D**: `main.go` wires concrete implementations into interfaces at the composition root only; no package below `cmd/` should import a concrete SQLite/OIDC/exec type it doesn't own.

### 3.12 Logging

Add `slog.Debug`/`Info`/`Error` at every new boundary, following the existing style in `worker/pool.go`:
- Auth: login initiated, callback received (sub, but never token contents), token verification failure reason, session issued/rejected.
- Repository ABM: create/update/delete with repo name + id, normalization result (raw → ssh url), validation failures.
- SSH keys: generation start/end with type + fingerprint (never log private key material), deletion, in-use conflict.
- Git sync: reuse/extend current debug logs in `clone.go`/`push.go`/`pool.go` (already fairly thorough) — add key-id (not path) and target repo id to every log line for correlation.
- Webhook: existing logs stay; add which repository the signature was checked against.
- Never log: private key contents, webhook secret plaintext, session/JWT signing secret, OIDC client secret.

## 4. Frontend — Angular 20 + Tailwind

### 4.1 Project setup

Angular CLI is **not** installed globally or added as a project dependency assumption — every `ng` invocation goes through `npx` (e.g. `npx @angular/cli new ...`, `npx ng build`, `npx ng generate ...`). Package manager is **pnpm**, not npm/yarn:

```bash
npx @angular/cli new frontend --package-manager=pnpm --style=css --routing --ssr=false
cd frontend
pnpm add -D tailwindcss @tailwindcss/postcss postcss
pnpm add @fortawesome/fontawesome-free
```

This adapts the `angular-project-setup` skill's baseline conventions (Tailwind, no SSR, non-interactive) to this project's specific choice of pnpm-over-yarn and npx-over-global-CLI. All subsequent frontend build commands in this plan (`ng build`, `ng test`, `ng generate`) are run as `npx ng ...` or `pnpm exec ng ...` — never assume a global `ng` binary exists. Add `@fortawesome/fontawesome-free` free icon set for the sidebar/nav icons per the requirement.

### 4.2 Structure

```
frontend/
  src/app/
    core/
      auth/            AuthService (calls /auth/config, /api/me, handles 401 -> redirect to /auth/login)
      http/            HTTP interceptor: attach credentials:'include', global 401 handler
      layout/          Shell component: collapsible left nav, topbar with user + logout
    features/
      repositories/    list, create/edit form, sync-now action, delete confirm
      ssh-keys/        list, generate form (rsa/ed25519 choice), view/copy public key, delete
      history/         table/list of sync events (replaces server-rendered dashboard), filters, status badges
      login/           "Login using {{ providerName }}" screen, calls AuthService.login()
    app.routes.ts       auth guard on all feature routes; public /login route
```

### 4.3 UX requirements mapping

- **Dark mode**: Tailwind `dark:` classes + a `class` strategy toggle (default dark, since the current dashboard is already dark-themed per `handlers.go`'s Tailwind classes — reuse the same color palette: emerald/amber/rose/sky/slate for status badges).
- **Mobile-friendly + collapsible left menu**: a `<nav>` that's an overlay drawer on small screens (Tailwind breakpoints) and a persistent collapsible sidebar (icon-only ↔ icon+label toggle) on desktop; use FontAwesome icons for Repositories, SSH Keys, History, and a Logout icon.
- **Auth gating**: root `AuthGuard` (functional guard, Angular 20 style) checks a signal/observable from `AuthService`; unauthenticated users see only the login screen with the dynamic provider-name button; guard redirects to backend `/auth/login` (full page navigation, since OIDC requires leaving the SPA).
- **Repository ABM form fields**, exactly matching backend DTO: name, pull URL (with live preview of the normalized SSH form as the user types — nice touch, calls a debounce-validated backend endpoint or replicates the normalization rule client-side), pull SSH key (dropdown populated from `/api/ssh-keys`, with a "create new" shortcut), push URL (same normalization preview), push SSH key (dropdown), webhook secret (generate-random button + reveal-once + copy), "Synchronize now" checkbox/button.
- **SSH key ABM**: list with name/type/fingerprint/created date; "Generate new key" modal (choose RSA or Ed25519); "View public key" opens a copyable text box (fetches `/api/ssh-keys/{id}/public-key` on demand, never fetches/display private key).
- **History**: reuse current dashboard's columns (received/finished time, provider, repo, ref, commits, status badge, duration, detail) as a responsive table/card list.

### 4.4 Build integration

Single deployment mode: `npx ng build` output is copied into the Go module and embedded via `go:embed` (single binary). Build step: `cd frontend && pnpm install --frozen-lockfile && pnpm exec ng build` → copy `dist/frontend/browser/*` → `internal/webapp/dist/` → `go build ./...` picks it up via `//go:embed dist/*`.

For local iteration, run the Angular dev server (`pnpm exec ng serve`) with a dev-server proxy config (`proxy.conf.json`) pointing `/api`, `/auth`, `/webhook` at the locally running Go backend — this avoids rebuilding/re-embedding on every frontend change during development, while production always ships the single embedded binary.

## 5. CI/CD & Docker

- Update `.forgejo/workflows/build.yml` to add a frontend build stage (Node + pnpm via `corepack enable && corepack prepare pnpm@latest --activate`, then `pnpm install --frozen-lockfile && pnpm exec ng build`) before the Go build, and to run `go build ./...` / `go test ./...` after backend changes.
- Update `Dockerfile` to a multi-stage build: stage 1 `node:XX` enables corepack/pnpm, runs `pnpm install --frozen-lockfile && pnpm exec ng build`; stage 2 Go builds the binary with the frontend `dist/` embedded (`go:embed`); stage 3 minimal runtime image (keep `git` + `openssh-client`, drop nothing there since git/ssh are still required).
- Update `docker-compose.yml`: remove the Postgres fixture service entirely (no longer needed), add OIDC env vars (pointing at wherever PocketID runs in the dev compose, or document connecting to an external instance), mount a volume for `SSH_KEYS_DIR` and `CACHE_DIR`.
- Update `fixtures/init.sql` → delete (Postgres fixture no longer needed); replace with a small SQLite seed script/test helper if useful for local dev.

## 6. Suggested execution order (for the agentic AI)

Small, independently-testable steps, each endable with `go build ./... && go test ./...` (backend) or `ng build` (frontend):

1. `internal/giturl` package + full unit test suite (pure, no I/O).
2. `internal/store` (SQLite schema + `RepositoryStore`/`SSHKeyStore` interfaces + impls) + tests.
3. `internal/sshkeys` (generate/read/delete) + tests, including "key in use" delete-conflict behavior.
4. Refactor `internal/gitsync` to drop Forgejo-specific code (decrypt.go deleted, credentials.go simplified, clone.go/push.go signatures updated) + update `gitsync_test.go`.
5. `internal/reposvc` orchestration service + tests (mock store/sshkeys/gitsync).
6. Rework `internal/worker` to use `RepositoryStore` instead of `forgejodb`; delete `internal/forgejodb` entirely; update `pool_test.go`.
7. `internal/webhook`: per-repo secret lookup instead of global map; update tests.
8. `internal/auth` (OIDC + session) + tests (use `go-oidc`'s test/mock provider or an httptest OIDC discovery server).
9. `internal/api`: REST handlers for repositories/ssh-keys/history/me, wired behind `RequireAuth`; update/replace `internal/dashboard` → rename to serve JSON, not HTML.
10. `internal/webapp` (or extend dashboard) static-file + SPA-fallback serving; wire everything in `cmd/forgejo-sync/main.go` as a single server.
11. Update `internal/config` (new env vars, delete old ones) + `config_test.go` + `.env.example`.
12. Angular project scaffold via `npx @angular/cli new frontend --package-manager=pnpm` + Tailwind + FontAwesome (§4.1).
13. Angular `core` (auth service, HTTP interceptor, shell/layout with collapsible nav, dark mode).
14. Angular `features/login`, `features/repositories` (pull/push URL + key fields), `features/ssh-keys`, `features/history`.
15. Wire `pnpm`-based frontend build into Dockerfile/CI (`go:embed`); remove Postgres from `docker-compose.yml`/fixtures.
16. Update `AGENTS.md` and `README.md` to reflect the new architecture (this file becomes stale once executed — fold its final-state description back into `AGENTS.md`).
17. End-to-end manual test pass: OIDC login, add a repository (all three URL formats), generate both key types, retrieve a public key, trigger "sync now," confirm a webhook-triggered sync still works and appears in history, confirm unauthenticated requests to `/api/**` get 401 and `/webhook` still works without OIDC.

## 7. Decisions (confirmed)

1. **Pull/push naming**: the repository ABM fields are named `pull_url` / `pull_ssh_key_id` and `push_url` / `push_ssh_key_id` (renamed from "source/target" and "local repository" for clarity). Both are arbitrary SSH git URLs — no assumption that push goes to a Forgejo instance specifically. §3.2, §3.4, §3.9, and §4.3 below are updated accordingly.
2. **One SQLite database** (`app.db`) holds `repositories`, `ssh_keys`, and `sync_events`. `HISTORY_DB_PATH` is removed as a separate concept; `APP_DB_PATH` is the only DB path env var.
3. **Webhook secrets**: stored hashed only (show-once-on-creation UX), one secret per repository. `WEBHOOK_GITHUB_SECRET` / `WEBHOOK_GITLAB_SECRET` global env vars are **removed entirely** — no global fallback, no per-provider default. Every repository must have its own webhook secret set via the ABM.
4. **Frontend deployment**: Angular build is embedded into the Go binary via `go:embed` (single artifact). No `FRONTEND_DIST_DIR` runtime option — that env var is dropped from §2.
5. **OIDC session model**: stateless signed cookie only. No server-side session store/revocation list.
6. **SSH key format**: OpenSSH-format private keys.
7. **Webhook providers per repository**: exactly one provider per repository, one pull source → one push target. No multi-provider fan-in.
8. **Frontend tooling**: Angular CLI is **not** installed globally or as a dev dependency assumption — all `ng` invocations go through `npx ng ...` (or `pnpm dlx ng ...`, see below). Package manager is **pnpm** (not npm/yarn) — update the `angular-project-setup` skill's yarn-based conventions to pnpm for this project specifically: `pnpm create @angular ...` / `npx @angular/cli new ... --package-manager=pnpm`, then `pnpm install`, `pnpm exec ng build`, etc. CI and Dockerfile frontend-build stages use `pnpm` + `corepack` (or explicit `npm i -g pnpm`) rather than `yarn`.

These are now treated as fixed constraints for execution, not open questions.

## Brief summary

Done so far: the v2 backend foundation is in place. URL normalization is completed in `internal/giturl`, the SQLite-backed repository/key/history store is implemented in `internal/store`, the SSH-only git sync path was refactored in `internal/gitsync`, key generation/management is started in `internal/sshkeys`, and the repository orchestration service in `internal/reposvc` is now working with the new model.

Next steps: finish the worker rework to use the local repository store instead of the legacy Forgejo lookup, update webhook verification to use per-repository secrets, then move into OIDC/auth, the REST API, the Angular frontend under `web/`, and the final Docker/CI and end-to-end validation pass.
