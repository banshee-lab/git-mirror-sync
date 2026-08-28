package history

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const schema = `
CREATE TABLE IF NOT EXISTS sync_events (
    id            INTEGER PRIMARY KEY AUTOINCREMENT,
    received_at   TIMESTAMP NOT NULL,
    finished_at   TIMESTAMP,
    provider      TEXT NOT NULL,
    repo_fullname TEXT NOT NULL,
    clone_url     TEXT,
    ssh_url       TEXT,
    forgejo_owner TEXT,
    forgejo_repo  TEXT,
    ref           TEXT,
    commit_before TEXT,
    commit_after  TEXT,
    status        TEXT NOT NULL, -- received|skipped|success|conflict|error
    detail        TEXT,
    duration_ms   INTEGER
);
CREATE INDEX IF NOT EXISTS idx_sync_events_received_at ON sync_events(received_at DESC);
`

// Store handles persistence of sync events in SQLite.
// It synchronizes writes via a mutex to guarantee SQLite concurrency safety.
type Store struct {
	db *sql.DB
	mu sync.Mutex
}

// NewStore opens (or creates) a SQLite database at dbPath and runs migrations idempotently.
// If dbPath is not ":memory:" and parent directories do not exist, they are created.
func NewStore(dbPath string) (*Store, error) {
	if dbPath != ":memory:" && !filepath.IsAbs(dbPath) && dbPath != "" {
		var err error
		dbPath, err = filepath.Abs(dbPath)
		if err != nil {
			return nil, fmt.Errorf("resolve absolute path for db: %w", err)
		}
	}

	if dbPath != ":memory:" && dbPath != "" {
		dir := filepath.Dir(dbPath)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return nil, fmt.Errorf("create database directory %s: %w", dir, err)
		}
	}

	// SQLite connection string with WAL mode and busy timeout enabled for concurrency resilience
	dsn := dbPath
	if dbPath != ":memory:" {
		dsn = fmt.Sprintf("file:%s?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)", dbPath)
	}

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite database: %w", err)
	}

	// For SQLite, limit open connections to avoid lock contention
	db.SetMaxOpenConns(1)

	store := &Store{db: db}
	if err := store.migrate(); err != nil {
		db.Close()
		return nil, fmt.Errorf("run migrations: %w", err)
	}

	return store, nil
}

// Open is an alias for NewStore for convenience.
func Open(dbPath string) (*Store, error) {
	return NewStore(dbPath)
}

func (s *Store) migrate() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("exec schema migration: %w", err)
	}
	return nil
}

// Insert inserts a new SyncEvent and returns the generated row ID.
func (s *Store) Insert(ctx context.Context, evt SyncEvent) (int64, error) {
	if !evt.Status.IsValid() {
		return 0, fmt.Errorf("invalid sync event status: %q", evt.Status)
	}

	if evt.ReceivedAt.IsZero() {
		evt.ReceivedAt = time.Now().UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	query := `
INSERT INTO sync_events (
    received_at, finished_at, provider, repo_fullname,
    clone_url, ssh_url, forgejo_owner, forgejo_repo,
    ref, commit_before, commit_after, status, detail, duration_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?);
`

	var finishedAtVal any
	if evt.FinishedAt != nil {
		finishedAtVal = evt.FinishedAt.UTC().Format(time.RFC3339Nano)
	}

	var durationMSVal any
	if evt.DurationMS != nil {
		durationMSVal = *evt.DurationMS
	}

	res, err := s.db.ExecContext(ctx, query,
		evt.ReceivedAt.UTC().Format(time.RFC3339Nano),
		finishedAtVal,
		evt.Provider,
		evt.RepoFullName,
		evt.CloneURL,
		evt.SSHURL,
		evt.ForgejoOwner,
		evt.ForgejoRepo,
		evt.Ref,
		evt.CommitBefore,
		evt.CommitAfter,
		string(evt.Status),
		evt.Detail,
		durationMSVal,
	)
	if err != nil {
		return 0, fmt.Errorf("insert sync_event: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("get last insert id: %w", err)
	}

	return id, nil
}

// Finish updates a sync event upon completion. It sets status, detail, commit_after (if non-empty),
// finished_at, and calculates duration_ms based on received_at.
func (s *Store) Finish(ctx context.Context, id int64, status Status, detail string, commitAfter string, finishedAt time.Time) error {
	if !status.IsValid() {
		return fmt.Errorf("invalid status: %q", status)
	}
	if finishedAt.IsZero() {
		finishedAt = time.Now().UTC()
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Query received_at and commit_after to calculate duration_ms and preserve commit_after if not provided
	var receivedAtStr string
	var existingCommitAfter sql.NullString
	err := s.db.QueryRowContext(ctx, `SELECT received_at, commit_after FROM sync_events WHERE id = ?`, id).Scan(&receivedAtStr, &existingCommitAfter)
	if err != nil {
		if err == sql.ErrNoRows {
			return fmt.Errorf("sync event with id %d not found", id)
		}
		return fmt.Errorf("query sync event %d: %w", id, err)
	}

	receivedAt, err := parseTime(receivedAtStr)
	if err != nil {
		return fmt.Errorf("parse received_at %q: %w", receivedAtStr, err)
	}

	durationMS := finishedAt.Sub(receivedAt).Milliseconds()
	if durationMS < 0 {
		durationMS = 0
	}

	finalCommitAfter := commitAfter
	if finalCommitAfter == "" && existingCommitAfter.Valid {
		finalCommitAfter = existingCommitAfter.String
	}

	query := `
UPDATE sync_events
SET finished_at = ?,
    status = ?,
    detail = ?,
    commit_after = ?,
    duration_ms = ?
WHERE id = ?;
`
	_, err = s.db.ExecContext(ctx, query,
		finishedAt.UTC().Format(time.RFC3339Nano),
		string(status),
		detail,
		finalCommitAfter,
		durationMS,
		id,
	)
	if err != nil {
		return fmt.Errorf("update sync_event %d: %w", id, err)
	}

	return nil
}

// UpdateStatus updates the status and detail fields of a sync event.
func (s *Store) UpdateStatus(ctx context.Context, id int64, status Status, detail string) error {
	if !status.IsValid() {
		return fmt.Errorf("invalid status: %q", status)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	query := `UPDATE sync_events SET status = ?, detail = ? WHERE id = ?;`
	res, err := s.db.ExecContext(ctx, query, string(status), detail, id)
	if err != nil {
		return fmt.Errorf("update status on sync_event %d: %w", id, err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("rows affected: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("sync event with id %d not found", id)
	}

	return nil
}

// Recent queries the most recent sync events, ordered by received_at DESC, id DESC.
func (s *Store) Recent(ctx context.Context, limit int) ([]SyncEvent, error) {
	if limit <= 0 {
		limit = 30
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	query := `
SELECT id, received_at, finished_at, provider, repo_fullname,
       clone_url, ssh_url, forgejo_owner, forgejo_repo,
       ref, commit_before, commit_after, status, detail, duration_ms
FROM sync_events
ORDER BY received_at DESC, id DESC
LIMIT ?;
`
	rows, err := s.db.QueryContext(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("query recent sync_events: %w", err)
	}
	defer rows.Close()

	var events []SyncEvent
	for rows.Next() {
		var (
			evt           SyncEvent
			receivedAtStr string
			finishedAtStr sql.NullString
			cloneURL      sql.NullString
			sshURL        sql.NullString
			forgejoOwner  sql.NullString
			forgejoRepo   sql.NullString
			ref           sql.NullString
			commitBefore  sql.NullString
			commitAfter   sql.NullString
			statusStr     string
			detail        sql.NullString
			durationMS    sql.NullInt64
		)

		err := rows.Scan(
			&evt.ID,
			&receivedAtStr,
			&finishedAtStr,
			&evt.Provider,
			&evt.RepoFullName,
			&cloneURL,
			&sshURL,
			&forgejoOwner,
			&forgejoRepo,
			&ref,
			&commitBefore,
			&commitAfter,
			&statusStr,
			&detail,
			&durationMS,
		)
		if err != nil {
			return nil, fmt.Errorf("scan sync_event row: %w", err)
		}

		evt.ReceivedAt, err = parseTime(receivedAtStr)
		if err != nil {
			return nil, fmt.Errorf("parse received_at %q: %w", receivedAtStr, err)
		}

		if finishedAtStr.Valid {
			t, err := parseTime(finishedAtStr.String)
			if err == nil {
				evt.FinishedAt = &t
			}
		}

		if cloneURL.Valid {
			evt.CloneURL = cloneURL.String
		}
		if sshURL.Valid {
			evt.SSHURL = sshURL.String
		}
		if forgejoOwner.Valid {
			evt.ForgejoOwner = forgejoOwner.String
		}
		if forgejoRepo.Valid {
			evt.ForgejoRepo = forgejoRepo.String
		}
		if ref.Valid {
			evt.Ref = ref.String
		}
		if commitBefore.Valid {
			evt.CommitBefore = commitBefore.String
		}
		if commitAfter.Valid {
			evt.CommitAfter = commitAfter.String
		}
		evt.Status = Status(statusStr)
		if detail.Valid {
			evt.Detail = detail.String
		}
		if durationMS.Valid {
			evt.DurationMS = &durationMS.Int64
		}

		events = append(events, evt)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sync_event rows: %w", err)
	}

	return events, nil
}

// Prune deletes sync_events where received_at < now - olderThan.
// Returns the number of deleted rows.
func (s *Store) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	cutoff := time.Now().UTC().Add(-olderThan)
	cutoffStr := cutoff.Format(time.RFC3339Nano)

	s.mu.Lock()
	defer s.mu.Unlock()

	res, err := s.db.ExecContext(ctx, `DELETE FROM sync_events WHERE received_at < ?;`, cutoffStr)
	if err != nil {
		return 0, fmt.Errorf("prune sync_events: %w", err)
	}

	rows, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("prune rows affected: %w", err)
	}

	return rows, nil
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Close()
}

func parseTime(s string) (time.Time, error) {
	formats := []string{
		time.RFC3339Nano,
		time.RFC3339,
		"2006-01-02 15:04:05.999999999-07:00",
		"2006-01-02 15:04:05.999999999",
		"2006-01-02 15:04:05",
	}
	for _, layout := range formats {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Parse(time.RFC3339, s)
}
