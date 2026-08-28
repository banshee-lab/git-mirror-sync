package webhook

import (
	"context"
	"fmt"
	"net/http"
)

// RepoInfo is the standard repository representation extracted from webhook payloads.
// It is the shared contract consumed by downstream packages.
type RepoInfo struct {
	Provider string
	FullName string
	Owner    string
	Name     string
	Private  bool
	HTMLURL  string
	SSHURL   string
	CloneURL string
	Ref      string
	Before   string
	After    string
}

// Provider defines the interface for webhook source platforms (e.g., GitHub, GitLab).
type Provider interface {
	// Name returns the provider identifier (e.g. "github", "gitlab").
	Name() string
	// CanHandle inspects HTTP request headers to determine if this provider should handle the request.
	CanHandle(h http.Header) bool
	// VerifySignature validates the webhook secret/signature against the request headers and raw body.
	VerifySignature(secret string, h http.Header, body []byte) error
	// Parse extracts RepoInfo from the webhook payload.
	Parse(body []byte) (*RepoInfo, error)
}

// JobEnqueuer defines the interface for passing parsed webhook sync jobs to a worker pool.
type JobEnqueuer interface {
	Enqueue(ctx context.Context, repo RepoInfo, eventID int64) error
}

// Registry manages registered webhook providers.
type Registry struct {
	providers []Provider
}

// NewRegistry creates a new Registry with the provided providers.
func NewRegistry(providers ...Provider) *Registry {
	return &Registry{
		providers: append([]Provider(nil), providers...),
	}
}

// Register adds a provider to the registry.
func (r *Registry) Register(p Provider) {
	r.providers = append(r.providers, p)
}

// Match finds the first provider whose CanHandle method returns true for the given headers.
// Returns nil if no provider matches.
func (r *Registry) Match(h http.Header) Provider {
	for _, p := range r.providers {
		if p.CanHandle(h) {
			return p
		}
	}
	return nil
}

// Providers returns all registered providers.
func (r *Registry) Providers() []Provider {
	return append([]Provider(nil), r.providers...)
}

// ErrNoProviderMatched indicates no registered provider matched the request headers.
var ErrNoProviderMatched = fmt.Errorf("no matching webhook provider found")

// ErrInvalidSignature indicates signature or token verification failed.
var ErrInvalidSignature = fmt.Errorf("invalid webhook signature or token")
