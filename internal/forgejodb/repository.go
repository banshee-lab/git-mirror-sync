package forgejodb

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5"
)

// ErrNoMatch is returned when no push_mirror entry matches the candidate URLs.
var ErrNoMatch = errors.New("no matching push_mirror")

// MatchedRepo represents the matched Forgejo repository and push mirror configuration.
type MatchedRepo struct {
	RepoID        int64
	OwnerName     string
	RepoName      string
	PushMirrorID  int64
	RemoteAddress string
	PrivateKey    []byte // bytea from push_mirror
}

// NormalizeRemote generates candidate remote address variants from a given URL
// (such as webhook clone_url or ssh_url) for matching against push_mirror.remote_address.
// Candidate variants include:
// - as-is trimmed
// - with and without trailing .git
// - with and without trailing slash /
// - credential-stripped variant (https://user:pass@host/owner/repo.git -> https://host/owner/repo.git)
// - deduplicated slice of non-empty strings.
func NormalizeRemote(rawURL string) []string {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return nil
	}

	seen := make(map[string]struct{})
	var candidates []string

	addCandidate := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" {
			return
		}
		if _, exists := seen[s]; !exists {
			seen[s] = struct{}{}
			candidates = append(candidates, s)
		}
	}

	// Base candidates set
	rawForms := []string{trimmed}

	// If it contains credentials (e.g. https://user:pass@host/...), strip them
	if u, err := url.Parse(trimmed); err == nil && u.User != nil {
		uClean := *u
		uClean.User = nil
		rawForms = append(rawForms, uClean.String())
	} else if strings.Contains(trimmed, "@") && strings.Contains(trimmed, "://") {
		// Fallback for custom schemes or malformed URLs with userinfo
		schemeIdx := strings.Index(trimmed, "://")
		atIdx := strings.Index(trimmed[schemeIdx+3:], "@")
		if atIdx != -1 {
			stripped := trimmed[:schemeIdx+3] + trimmed[schemeIdx+3+atIdx+1:]
			rawForms = append(rawForms, stripped)
		}
	}

	// For each base form, generate variations (.git, trailing slash)
	for _, form := range rawForms {
		addCandidate(form)

		// Slash variations
		withoutSlash := strings.TrimRight(form, "/")
		withSlash := withoutSlash + "/"
		addCandidate(withoutSlash)
		addCandidate(withSlash)

		// .git variations for both with and without slash
		for _, base := range []string{form, withoutSlash} {
			if strings.HasSuffix(base, ".git") {
				noGit := strings.TrimSuffix(base, ".git")
				addCandidate(noGit)
				addCandidate(noGit + "/")
			} else {
				withGit := base + ".git"
				addCandidate(withGit)
				addCandidate(withGit + "/")
			}
		}
	}

	return candidates
}

const lookupRepoSQL = `
SELECT r.id, r.owner_name, r.name, pm.id, pm.remote_address, pm.private_key
FROM push_mirror pm
JOIN repository r ON r.id = pm.repo_id
WHERE pm.remote_address = ANY($1::text[])
LIMIT 1;
`

// LookupRepository queries the Forgejo database for a repository matching the given clone and/or ssh URLs.
// If no matching push mirror is found, it returns ErrNoMatch.
func (c *Client) LookupRepository(ctx context.Context, cloneURL, sshURL string) (*MatchedRepo, error) {
	if c == nil || c.pool == nil {
		return nil, errors.New("forgejodb client or pool is nil")
	}

	var candidates []string
	seen := make(map[string]struct{})

	for _, u := range []string{cloneURL, sshURL} {
		for _, cand := range NormalizeRemote(u) {
			if _, exists := seen[cand]; !exists {
				seen[cand] = struct{}{}
				candidates = append(candidates, cand)
			}
		}
	}

	if len(candidates) == 0 {
		return nil, ErrNoMatch
	}

	var repo MatchedRepo
	err := c.pool.QueryRow(ctx, lookupRepoSQL, candidates).Scan(
		&repo.RepoID,
		&repo.OwnerName,
		&repo.RepoName,
		&repo.PushMirrorID,
		&repo.RemoteAddress,
		&repo.PrivateKey,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNoMatch
		}
		return nil, fmt.Errorf("failed to lookup repository: %w", err)
	}

	return &repo, nil
}
