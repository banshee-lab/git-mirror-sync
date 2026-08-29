package store

import (
	"context"
	"time"
)

// Repository represents a source-to-destination git sync configuration.
type Repository struct {
	ID              int64
	Name            string // user-friendly display name, must be unique
	PullURLRaw      string // exactly what user provided
	PullURLSSH      string // normalized ssh://... form
	PullSSHKeyID    int64  // foreign key to ssh_keys.id
	PushURLRaw      string
	PushURLSSH      string
	PushSSHKeyID    int64
	WebhookSecHash  string // bcrypt hash of the webhook secret (not stored plaintext)
	ProviderHint    string // "github" | "gitlab" | "generic", for signature verification
	Enabled         bool
	CreatedAt       time.Time
	UpdatedAt       time.Time
}

// SSHKey represents an SSH key pair stored on disk.
type SSHKey struct {
	ID         int64
	Name       string // user-friendly display name, must be unique
	KeyType    string // "rsa" or "ed25519"
	PrivatePath string // relative path under SSH_KEYS_DIR
	PublicPath  string // relative path under SSH_KEYS_DIR
	Fingerprint string // hash of the public key for display
	CreatedAt  time.Time
}

// SyncEvent represents a historical record of one synchronization attempt.
// This model is carried over from internal/history/models.go.
type SyncEvent struct {
	ID            int64
	ReceivedAt    time.Time
	FinishedAt    time.Time
	Provider      string // "github" | "gitlab" | "generic"
	RepoFullName  string // user-provided or inferred from repo name
	CloneURL      string
	SSHURL        string
	ForgejoOwner  string // deprecated: kept for backwards compat, now unused
	ForgejoRepo   string // deprecated: kept for backwards compat, now unused
	Ref           string
	CommitBefore  string
	CommitAfter   string
	Status        string // "received" | "skipped" | "success" | "conflict" | "error"
	Detail        string // error message or status detail (sanitized, no secrets)
	DurationMS    int64
}

// RepositoryStore provides CRUD operations for repositories.
type RepositoryStore interface {
	Create(ctx context.Context, repo Repository) (Repository, error)
	Update(ctx context.Context, repo Repository) (Repository, error)
	Delete(ctx context.Context, id int64) error
	Get(ctx context.Context, id int64) (Repository, error)
	List(ctx context.Context) ([]Repository, error)
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
	List(ctx context.Context, limit int) ([]SyncEvent, error)
	// PruneOlderThan deletes all events created before the given time.
	PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error)
}
