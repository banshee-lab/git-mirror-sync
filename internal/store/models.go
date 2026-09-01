package store

import (
	"context"
	"time"

	"forgejo-sync/internal/history"
)

// Repository represents a source-to-destination git sync configuration.
type Repository struct {
	ID             int64     `json:"id"`
	Name           string    `json:"name"` // user-friendly display name, must be unique
	PullURLRaw     string    `json:"pull_url_raw"` // exactly what user provided
	PullURLSSH     string    `json:"pull_url_ssh"` // normalized ssh://... form
	PullSSHKeyID   int64     `json:"pull_ssh_key_id"` // foreign key to ssh_keys.id
	PushURLRaw     string    `json:"push_url_raw"`
	PushURLSSH     string    `json:"push_url_ssh"`
	PushSSHKeyID   int64     `json:"push_ssh_key_id"`
	WebhookSecHash string    `json:"-"` // AES-GCM encrypted webhook secret; reversible for signature verification
	ProviderHint     string     `json:"provider_hint"` // "github" | "gitlab" | "forgejo" | "gitea" | "generic", for signature verification
	PushProviderHint string     `json:"push_provider_hint"`
	Enabled          bool       `json:"enabled"`
	CreatedAt        time.Time  `json:"created_at"`
	UpdatedAt        time.Time  `json:"updated_at"`
	LastSyncAt       *time.Time `json:"last_sync_at,omitempty"`
	LastSyncStatus   string     `json:"last_sync_status,omitempty"`
}

// SSHKey represents an SSH key pair stored on disk.
type SSHKey struct {
	ID          int64     `json:"id"`
	Name        string    `json:"name"` // user-friendly display name, must be unique
	KeyType     string    `json:"key_type"` // "rsa" or "ed25519"
	PrivatePath string    `json:"private_path"` // relative path under SSH_KEYS_DIR
	PublicPath  string    `json:"public_path"` // relative path under SSH_KEYS_DIR
	Fingerprint string    `json:"fingerprint"` // hash of the public key for display
	CreatedAt   time.Time `json:"created_at"`
}

// SyncEvent represents a historical record of one synchronization attempt.
// This model is carried over from internal/history/models.go.
type SyncEvent struct {
	ID           int64     `json:"id"`
	ReceivedAt   time.Time `json:"received_at"`
	FinishedAt   time.Time `json:"finished_at"`
	Provider     string    `json:"provider"` // "github" | "gitlab" | "generic"
	RepoFullName string    `json:"repo_full_name"` // user-provided or inferred from repo name
	CloneURL     string    `json:"clone_url,omitempty"`
	SSHURL       string    `json:"ssh_url,omitempty"`
	ForgejoOwner string    `json:"forgejo_owner,omitempty"` // deprecated: kept for backwards compat, now unused
	ForgejoRepo  string    `json:"forgejo_repo,omitempty"` // deprecated: kept for backwards compat, now unused
	Ref          string    `json:"ref,omitempty"`
	CommitBefore string    `json:"commit_before,omitempty"`
	CommitAfter  string    `json:"commit_after,omitempty"`
	Status       string    `json:"status"` // "received" | "skipped" | "success" | "conflict" | "error"
	Detail       string    `json:"detail,omitempty"` // error message or status detail (sanitized, no secrets)
	DurationMS   int64     `json:"duration_ms,omitempty"`
}

// RepositoryStore provides CRUD operations for repositories.
type RepositoryStore interface {
	Create(ctx context.Context, repo Repository) (Repository, error)
	Update(ctx context.Context, repo Repository) (Repository, error)
	Delete(ctx context.Context, id int64) error
	Get(ctx context.Context, id int64) (Repository, error)
	List(ctx context.Context) ([]Repository, error)
	ListPage(ctx context.Context, search string, limit, offset int) ([]Repository, int, error)
	// GetByName retrieves a repository by its unique name.
	GetByName(ctx context.Context, name string) (Repository, error)
	// ListByPullURL returns all repositories with matching pull URL (for webhook routing).
	ListByPullURL(ctx context.Context, normalizedPullURL string) ([]Repository, error)
}

// SSHKeyStore provides CRUD operations for SSH keys.
type SSHKeyStore interface {
	Create(ctx context.Context, key SSHKey) (SSHKey, error)
	Delete(ctx context.Context, id int64) error
	Get(ctx context.Context, id int64) (SSHKey, error)
	List(ctx context.Context) ([]SSHKey, error)
	// GetByName retrieves an SSH key by its unique name.
	GetByName(ctx context.Context, name string) (SSHKey, error)
	// IsKeyInUse returns true if this key is referenced by any repository.
	IsKeyInUse(ctx context.Context, id int64) (bool, error)
}

// SyncEventStore provides operations for sync event history.
type SyncEventStore interface {
	Create(ctx context.Context, event SyncEvent) (SyncEvent, error)
	Insert(ctx context.Context, event history.SyncEvent) (int64, error)
	List(ctx context.Context, limit int) ([]SyncEvent, error)
	Finish(ctx context.Context, id int64, status history.Status, detail string, commitAfter string, finishedAt time.Time) error
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
	// PruneOlderThan deletes all events created before the given time.
	PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}
