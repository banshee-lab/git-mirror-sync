package store

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"forgejo-sync/internal/history"

	_ "modernc.org/sqlite"
)

// DB is the main database connection holder.
type DB struct {
	conn *sql.DB
	mu   sync.RWMutex
}

const schema = `
CREATE TABLE IF NOT EXISTS repositories (
    id                  INTEGER PRIMARY KEY AUTOINCREMENT,
    name                TEXT NOT NULL UNIQUE,
    pull_url_raw        TEXT NOT NULL,
    pull_url_ssh        TEXT NOT NULL,
    pull_ssh_key_id     INTEGER NOT NULL REFERENCES ssh_keys(id),
    push_url_raw        TEXT NOT NULL,
    push_url_ssh        TEXT NOT NULL,
    push_ssh_key_id     INTEGER NOT NULL REFERENCES ssh_keys(id),
    webhook_secret_hash TEXT NOT NULL,
    provider_hint       TEXT,
    push_provider_hint  TEXT,
    enabled             INTEGER NOT NULL DEFAULT 1,
    created_at          TIMESTAMP NOT NULL,
    updated_at          TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS ssh_keys (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    name         TEXT NOT NULL UNIQUE,
    key_type     TEXT NOT NULL,
    private_path TEXT NOT NULL,
    public_path  TEXT NOT NULL,
    fingerprint  TEXT NOT NULL,
    created_at   TIMESTAMP NOT NULL
);

CREATE TABLE IF NOT EXISTS sync_events (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    received_at   TIMESTAMP NOT NULL,
    finished_at   TIMESTAMP NOT NULL,
    provider      TEXT NOT NULL,
    repo_full_name TEXT NOT NULL,
    clone_url     TEXT NOT NULL,
    ssh_url       TEXT NOT NULL,
    forgejo_owner TEXT,
    forgejo_repo  TEXT,
    ref           TEXT NOT NULL,
    commit_before TEXT NOT NULL,
    commit_after  TEXT NOT NULL,
    status        TEXT NOT NULL,
    detail        TEXT NOT NULL,
    duration_ms   INTEGER NOT NULL
);

CREATE INDEX IF NOT EXISTS idx_repositories_pull_url_ssh ON repositories(pull_url_ssh);
CREATE INDEX IF NOT EXISTS idx_sync_events_received_at ON sync_events(received_at);
`

// NewDB creates a new database connection and initializes the schema.
func NewDB(dbPath string) (*DB, error) {
	slog.Debug("store.NewDB: opening SQLite database", slog.String("db_path", dbPath))
	sqldb, err := sql.Open("sqlite", dbPath)
	if err != nil {
		slog.Debug("store.NewDB: failed to open SQLite database", slog.String("db_path", dbPath), slog.Any("error", err))
		return nil, fmt.Errorf("failed to open SQLite database: %w", err)
	}

	// Enable foreign keys, WAL mode, and busy timeout
	if _, err := sqldb.Exec("PRAGMA foreign_keys = ON; PRAGMA journal_mode = WAL; PRAGMA busy_timeout = 5000;"); err != nil {
		sqldb.Close()
		slog.Debug("store.NewDB: failed to configure pragmas", slog.Any("error", err))
		return nil, fmt.Errorf("failed to configure SQLite pragmas: %w", err)
	}

	db := &DB{conn: sqldb}

	// Initialize schema
	if err := db.initSchema(); err != nil {
		sqldb.Close()
		slog.Debug("store.NewDB: failed to initialize schema", slog.Any("error", err))
		return nil, fmt.Errorf("failed to initialize schema: %w", err)
	}

	slog.Debug("store.NewDB: SQLite database opened and initialized", slog.String("db_path", dbPath))
	return db, nil
}

// initSchema creates tables if they don't exist.
func (db *DB) initSchema() error {
	db.mu.Lock()
	defer db.mu.Unlock()

	slog.Debug("store.DB: executing schema migration")
	if _, err := db.conn.Exec(schema); err != nil {
		return err
	}
	_, _ = db.conn.Exec("ALTER TABLE repositories ADD COLUMN push_provider_hint TEXT")
	slog.Debug("store.DB: schema migration completed")
	return nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	slog.Debug("store.DB: closing database connection")
	return db.conn.Close()
}

// Repositories returns a RepositoryStore backed by this DB.
func (db *DB) Repositories() RepositoryStore {
	return &repositoryStoreImpl{db: db}
}

// SSHKeys returns an SSHKeyStore backed by this DB.
func (db *DB) SSHKeys() SSHKeyStore {
	return &sshKeyStoreImpl{db: db}
}

// SyncEvents returns a SyncEventStore backed by this DB.
func (db *DB) SyncEvents() SyncEventStore {
	return &syncEventStoreImpl{db: db}
}

// ========== RepositoryStore Implementation ==========

type repositoryStoreImpl struct {
	db *DB
}

func scanRepositoryRow(scanner interface{ Scan(dest ...any) error }) (Repository, error) {
	var repo Repository
	var providerHint, pushProviderHint sql.NullString
	var lastSyncAt sql.NullTime
	var lastSyncStatus sql.NullString
	err := scanner.Scan(
		&repo.ID, &repo.Name, &repo.PullURLRaw, &repo.PullURLSSH, &repo.PullSSHKeyID,
		&repo.PushURLRaw, &repo.PushURLSSH, &repo.PushSSHKeyID, &repo.WebhookSecHash,
		&providerHint, &pushProviderHint, &repo.Enabled, &repo.CreatedAt, &repo.UpdatedAt,
		&lastSyncAt, &lastSyncStatus,
	)
	if err != nil {
		return Repository{}, err
	}
	if providerHint.Valid {
		repo.ProviderHint = providerHint.String
	}
	if pushProviderHint.Valid {
		repo.PushProviderHint = pushProviderHint.String
	}
	if lastSyncAt.Valid {
		repo.LastSyncAt = &lastSyncAt.Time
	}
	if lastSyncStatus.Valid {
		repo.LastSyncStatus = lastSyncStatus.String
	}
	return repo, nil
}

const repoSelectFields = `
	r.id, r.name, r.pull_url_raw, r.pull_url_ssh, r.pull_ssh_key_id,
	r.push_url_raw, r.push_url_ssh, r.push_ssh_key_id, r.webhook_secret_hash,
	r.provider_hint, r.push_provider_hint, r.enabled, r.created_at, r.updated_at,
	se.received_at, se.status
FROM repositories r
LEFT JOIN sync_events se ON se.id = (
	SELECT id FROM sync_events WHERE repo_full_name = r.name ORDER BY received_at DESC LIMIT 1
)`

func (r *repositoryStoreImpl) Create(ctx context.Context, repo Repository) (Repository, error) {
	now := time.Now()
	repo.CreatedAt = now
	repo.UpdatedAt = now

	result, err := r.db.conn.ExecContext(
		ctx,
		`INSERT INTO repositories (name, pull_url_raw, pull_url_ssh, pull_ssh_key_id,
			push_url_raw, push_url_ssh, push_ssh_key_id, webhook_secret_hash,
			provider_hint, push_provider_hint, enabled, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		repo.Name, repo.PullURLRaw, repo.PullURLSSH, repo.PullSSHKeyID,
		repo.PushURLRaw, repo.PushURLSSH, repo.PushSSHKeyID, repo.WebhookSecHash,
		repo.ProviderHint, repo.PushProviderHint, repo.Enabled, repo.CreatedAt, repo.UpdatedAt,
	)
	if err != nil {
		return Repository{}, fmt.Errorf("failed to create repository: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return Repository{}, fmt.Errorf("failed to get last insert id: %w", err)
	}

	repo.ID = id
	return repo, nil
}

func (r *repositoryStoreImpl) Update(ctx context.Context, repo Repository) (Repository, error) {
	repo.UpdatedAt = time.Now()

	_, err := r.db.conn.ExecContext(
		ctx,
		`UPDATE repositories
		SET name = ?, pull_url_raw = ?, pull_url_ssh = ?, pull_ssh_key_id = ?,
			push_url_raw = ?, push_url_ssh = ?, push_ssh_key_id = ?, webhook_secret_hash = ?,
			provider_hint = ?, push_provider_hint = ?, enabled = ?, updated_at = ?
		WHERE id = ?`,
		repo.Name, repo.PullURLRaw, repo.PullURLSSH, repo.PullSSHKeyID,
		repo.PushURLRaw, repo.PushURLSSH, repo.PushSSHKeyID, repo.WebhookSecHash,
		repo.ProviderHint, repo.PushProviderHint, repo.Enabled, repo.UpdatedAt, repo.ID,
	)
	if err != nil {
		return Repository{}, fmt.Errorf("failed to update repository: %w", err)
	}

	return repo, nil
}

func (r *repositoryStoreImpl) Delete(ctx context.Context, id int64) error {
	_, err := r.db.conn.ExecContext(ctx, "DELETE FROM repositories WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete repository: %w", err)
	}
	return nil
}

func (r *repositoryStoreImpl) Get(ctx context.Context, id int64) (Repository, error) {
	row := r.db.conn.QueryRowContext(
		ctx,
		`SELECT `+repoSelectFields+` WHERE r.id = ?`,
		id,
	)
	repo, err := scanRepositoryRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return Repository{}, fmt.Errorf("repository not found")
		}
		return Repository{}, fmt.Errorf("failed to get repository: %w", err)
	}
	return repo, nil
}

func (r *repositoryStoreImpl) List(ctx context.Context) ([]Repository, error) {
	items, _, err := r.ListPage(ctx, "", 0, 0)
	return items, err
}

func (r *repositoryStoreImpl) ListPage(ctx context.Context, search string, limit, offset int) ([]Repository, int, error) {
	var total int
	if search != "" {
		pattern := "%" + search + "%"
		err := r.db.conn.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM repositories WHERE name LIKE ? COLLATE NOCASE OR pull_url_ssh LIKE ? COLLATE NOCASE OR push_url_ssh LIKE ? COLLATE NOCASE`,
			pattern, pattern, pattern,
		).Scan(&total)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to count repositories: %w", err)
		}
	} else {
		err := r.db.conn.QueryRowContext(ctx, `SELECT COUNT(*) FROM repositories`).Scan(&total)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to count repositories: %w", err)
		}
	}

	query := `SELECT ` + repoSelectFields
	var args []any
	if search != "" {
		pattern := "%" + search + "%"
		query += ` WHERE r.name LIKE ? COLLATE NOCASE OR r.pull_url_ssh LIKE ? COLLATE NOCASE OR r.push_url_ssh LIKE ? COLLATE NOCASE`
		args = append(args, pattern, pattern, pattern)
	}
	query += ` ORDER BY r.created_at DESC`
	if limit > 0 {
		query += ` LIMIT ? OFFSET ?`
		args = append(args, limit, offset)
	}

	rows, err := r.db.conn.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, 0, fmt.Errorf("failed to list repositories: %w", err)
	}
	defer rows.Close()

	repos := make([]Repository, 0)
	for rows.Next() {
		repo, err := scanRepositoryRow(rows)
		if err != nil {
			return nil, 0, fmt.Errorf("failed to scan repository: %w", err)
		}
		repos = append(repos, repo)
	}

	if err = rows.Err(); err != nil {
		return nil, 0, fmt.Errorf("error reading repositories: %w", err)
	}

	return repos, total, nil
}

func (r *repositoryStoreImpl) GetByName(ctx context.Context, name string) (Repository, error) {
	row := r.db.conn.QueryRowContext(
		ctx,
		`SELECT `+repoSelectFields+` WHERE r.name = ?`,
		name,
	)
	repo, err := scanRepositoryRow(row)
	if err != nil {
		if err == sql.ErrNoRows {
			return Repository{}, fmt.Errorf("repository not found")
		}
		return Repository{}, fmt.Errorf("failed to get repository by name: %w", err)
	}
	return repo, nil
}

func (r *repositoryStoreImpl) ListByPullURL(ctx context.Context, normalizedPullURL string) ([]Repository, error) {
	rows, err := r.db.conn.QueryContext(
		ctx,
		`SELECT `+repoSelectFields+` WHERE r.pull_url_ssh = ?`,
		normalizedPullURL,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list repositories by pull URL: %w", err)
	}
	defer rows.Close()

	repos := make([]Repository, 0)
	for rows.Next() {
		repo, err := scanRepositoryRow(rows)
		if err != nil {
			return nil, fmt.Errorf("failed to scan repository: %w", err)
		}
		repos = append(repos, repo)
	}

	return repos, rows.Err()
}

// ========== SSHKeyStore Implementation ==========

type sshKeyStoreImpl struct {
	db *DB
}

func (s *sshKeyStoreImpl) Create(ctx context.Context, key SSHKey) (SSHKey, error) {
	now := time.Now()
	key.CreatedAt = now

	result, err := s.db.conn.ExecContext(
		ctx,
		`INSERT INTO ssh_keys (name, key_type, private_path, public_path, fingerprint, created_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		key.Name, key.KeyType, key.PrivatePath, key.PublicPath, key.Fingerprint, key.CreatedAt,
	)
	if err != nil {
		return SSHKey{}, fmt.Errorf("failed to create SSH key: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return SSHKey{}, fmt.Errorf("failed to get last insert id: %w", err)
	}

	key.ID = id
	return key, nil
}

func (s *sshKeyStoreImpl) Delete(ctx context.Context, id int64) error {
	_, err := s.db.conn.ExecContext(ctx, "DELETE FROM ssh_keys WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to delete SSH key: %w", err)
	}
	return nil
}

func (s *sshKeyStoreImpl) Get(ctx context.Context, id int64) (SSHKey, error) {
	var key SSHKey
	err := s.db.conn.QueryRowContext(
		ctx,
		`SELECT id, name, key_type, private_path, public_path, fingerprint, created_at
		FROM ssh_keys WHERE id = ?`,
		id,
	).Scan(&key.ID, &key.Name, &key.KeyType, &key.PrivatePath, &key.PublicPath, &key.Fingerprint, &key.CreatedAt)

	if err != nil {
		if err == sql.ErrNoRows {
			return SSHKey{}, fmt.Errorf("SSH key not found")
		}
		return SSHKey{}, fmt.Errorf("failed to get SSH key: %w", err)
	}
	return key, nil
}

func (s *sshKeyStoreImpl) List(ctx context.Context) ([]SSHKey, error) {
	rows, err := s.db.conn.QueryContext(
		ctx,
		`SELECT id, name, key_type, private_path, public_path, fingerprint, created_at
		FROM ssh_keys ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list SSH keys: %w", err)
	}
	defer rows.Close()

	keys := make([]SSHKey, 0)
	for rows.Next() {
		var key SSHKey
		if err := rows.Scan(&key.ID, &key.Name, &key.KeyType, &key.PrivatePath, &key.PublicPath, &key.Fingerprint, &key.CreatedAt); err != nil {
			return nil, fmt.Errorf("failed to scan SSH key: %w", err)
		}
		keys = append(keys, key)
	}

	return keys, rows.Err()
}

func (s *sshKeyStoreImpl) GetByName(ctx context.Context, name string) (SSHKey, error) {
	var key SSHKey
	err := s.db.conn.QueryRowContext(
		ctx,
		`SELECT id, name, key_type, private_path, public_path, fingerprint, created_at
		FROM ssh_keys WHERE name = ?`,
		name,
	).Scan(&key.ID, &key.Name, &key.KeyType, &key.PrivatePath, &key.PublicPath, &key.Fingerprint, &key.CreatedAt)

	if err != nil {
		if err == sql.ErrNoRows {
			return SSHKey{}, fmt.Errorf("SSH key not found")
		}
		return SSHKey{}, fmt.Errorf("failed to get SSH key by name: %w", err)
	}
	return key, nil
}

func (s *sshKeyStoreImpl) IsKeyInUse(ctx context.Context, id int64) (bool, error) {
	var count int
	err := s.db.conn.QueryRowContext(
		ctx,
		`SELECT COUNT(*)
		FROM repositories
		WHERE pull_ssh_key_id = ? OR push_ssh_key_id = ?`,
		id, id,
	).Scan(&count)
	if err != nil {
		return false, fmt.Errorf("failed to check key usage: %w", err)
	}
	return count > 0, nil
}

// ========== SyncEventStore Implementation ==========

type syncEventStoreImpl struct {
	db *DB
}

func (s *syncEventStoreImpl) Create(ctx context.Context, event SyncEvent) (SyncEvent, error) {
	result, err := s.db.conn.ExecContext(
		ctx,
		`INSERT INTO sync_events (received_at, finished_at, provider, repo_full_name,
			clone_url, ssh_url, forgejo_owner, forgejo_repo, ref, commit_before,
			commit_after, status, detail, duration_ms)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		event.ReceivedAt, event.FinishedAt, event.Provider, event.RepoFullName,
		event.CloneURL, event.SSHURL, event.ForgejoOwner, event.ForgejoRepo,
		event.Ref, event.CommitBefore, event.CommitAfter, event.Status, event.Detail,
		event.DurationMS,
	)
	if err != nil {
		return SyncEvent{}, fmt.Errorf("failed to create sync event: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return SyncEvent{}, fmt.Errorf("failed to get last insert id: %w", err)
	}

	event.ID = id
	return event, nil
}

func (s *syncEventStoreImpl) Insert(ctx context.Context, event history.SyncEvent) (int64, error) {
	if !event.Status.IsValid() {
		return 0, fmt.Errorf("invalid status: %q", event.Status)
	}

	insertEvent := SyncEvent{
		ReceivedAt:   event.ReceivedAt,
		Provider:     event.Provider,
		RepoFullName: event.RepoFullName,
		CloneURL:     event.CloneURL,
		SSHURL:       event.SSHURL,
		ForgejoOwner: event.ForgejoOwner,
		ForgejoRepo:  event.ForgejoRepo,
		Ref:          event.Ref,
		CommitBefore: event.CommitBefore,
		CommitAfter:  event.CommitAfter,
		Status:       string(event.Status),
		Detail:       event.Detail,
	}
	if event.FinishedAt != nil {
		insertEvent.FinishedAt = *event.FinishedAt
	}
	if event.DurationMS != nil {
		insertEvent.DurationMS = *event.DurationMS
	}
	created, err := s.Create(ctx, insertEvent)
	if err != nil {
		return 0, err
	}
	return created.ID, nil
}

func (s *syncEventStoreImpl) Finish(ctx context.Context, id int64, status history.Status, detail string, commitAfter string, finishedAt time.Time) error {
	if !status.IsValid() {
		return fmt.Errorf("invalid status: %q", status)
	}
	if finishedAt.IsZero() {
		finishedAt = time.Now().UTC()
	}

	var receivedAt time.Time
	var prevCommitAfter sql.NullString
	if err := s.db.conn.QueryRowContext(ctx, `SELECT received_at, commit_after FROM sync_events WHERE id = ?`, id).Scan(&receivedAt, &prevCommitAfter); err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("sync event with id %d not found", id)
		}
		return fmt.Errorf("query sync event %d: %w", id, err)
	}

	finalCommitAfter := commitAfter
	if finalCommitAfter == "" && prevCommitAfter.Valid {
		finalCommitAfter = prevCommitAfter.String
	}
	durationMS := finishedAt.Sub(receivedAt).Milliseconds()
	if durationMS < 0 {
		durationMS = 0
	}

	_, err := s.db.conn.ExecContext(ctx, `UPDATE sync_events SET finished_at = ?, status = ?, detail = ?, commit_after = ?, duration_ms = ? WHERE id = ?`,
		finishedAt.UTC(), status, detail, finalCommitAfter, durationMS, id,
	)
	if err != nil {
		return fmt.Errorf("update sync_event %d: %w", id, err)
	}
	return nil
}

func (s *syncEventStoreImpl) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	return s.PruneOlderThan(ctx, time.Now().UTC().Add(-olderThan))
}

func (s *syncEventStoreImpl) List(ctx context.Context, limit int) ([]SyncEvent, error) {
	rows, err := s.db.conn.QueryContext(
		ctx,
		`SELECT id, received_at, finished_at, provider, repo_full_name,
			clone_url, ssh_url, forgejo_owner, forgejo_repo, ref, commit_before,
			commit_after, status, detail, duration_ms
		FROM sync_events ORDER BY received_at DESC LIMIT ?`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to list sync events: %w", err)
	}
	defer rows.Close()

	events := make([]SyncEvent, 0)
	for rows.Next() {
		var event SyncEvent
		if err := rows.Scan(
			&event.ID, &event.ReceivedAt, &event.FinishedAt, &event.Provider, &event.RepoFullName,
			&event.CloneURL, &event.SSHURL, &event.ForgejoOwner, &event.ForgejoRepo,
			&event.Ref, &event.CommitBefore, &event.CommitAfter, &event.Status, &event.Detail,
			&event.DurationMS,
		); err != nil {
			return nil, fmt.Errorf("failed to scan sync event: %w", err)
		}
		events = append(events, event)
	}

	return events, rows.Err()
}

func (s *syncEventStoreImpl) PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	result, err := s.db.conn.ExecContext(
		ctx,
		"DELETE FROM sync_events WHERE received_at < ?",
		cutoff,
	)
	if err != nil {
		return 0, fmt.Errorf("failed to prune sync events: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("failed to get rows affected: %w", err)
	}

	return rowsAffected, nil
}
