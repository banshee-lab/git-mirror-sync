package history

import (
	"time"
)

// Status represents the status of a sync event.
type Status string

const (
	StatusReceived Status = "received"
	StatusSkipped  Status = "skipped"
	StatusSuccess  Status = "success"
	StatusConflict Status = "conflict"
	StatusError    Status = "error"
)

// IsValid checks if the status is one of the allowed values.
func (s Status) IsValid() bool {
	switch s {
	case StatusReceived, StatusSkipped, StatusSuccess, StatusConflict, StatusError:
		return true
	default:
		return false
	}
}

// SyncEvent represents a single repository sync event recorded in SQLite.
// NOTE: No credential or secret fields must ever be present on this struct.
type SyncEvent struct {
	ID           int64      `json:"id"`
	ReceivedAt   time.Time  `json:"received_at"`
	FinishedAt   *time.Time `json:"finished_at,omitempty"`
	Provider     string     `json:"provider"`
	RepoFullName string     `json:"repo_fullname"`
	CloneURL     string     `json:"clone_url,omitempty"`
	SSHURL       string     `json:"ssh_url,omitempty"`
	ForgejoOwner string     `json:"forgejo_owner,omitempty"`
	ForgejoRepo  string     `json:"forgejo_repo,omitempty"`
	Ref          string     `json:"ref,omitempty"`
	CommitBefore string     `json:"commit_before,omitempty"`
	CommitAfter  string     `json:"commit_after,omitempty"`
	Status       Status     `json:"status"`
	Detail       string     `json:"detail,omitempty"`
	DurationMS   *int64     `json:"duration_ms,omitempty"`
}
