package giturl

import (
	"fmt"
	"net"
	"net/url"
	"regexp"
	"strings"
)

// ErrInvalidRepoURL is returned when a URL cannot be parsed or normalized to SSH form.
var ErrInvalidRepoURL = fmt.Errorf("invalid repository URL format")

// NormalizeToSSH converts a Git repository URL (HTTPS, SSH, or git://) to SSH form.
// It accepts:
//   - git@host:owner/repo(.git)?
//   - ssh://[user@]host[:port]/owner/repo(.git)?
//   - https://host/owner/repo(.git)?
//   - http://host/owner/repo(.git)?
//
// Returns ssh://git@host[:port]/path/to/repo.git
//
// sshUser defaults to "git" if empty. Trailing .git is stripped and re-added exactly once.
func NormalizeToSSH(rawURL, sshUser string) (string, error) {
	if rawURL == "" {
		return "", fmt.Errorf("%w: empty URL", ErrInvalidRepoURL)
	}

	if sshUser == "" {
		sshUser = "git"
	}

	// Try standard URL schemes first (ssh://, https://, http://, git://)
	if strings.HasPrefix(rawURL, "ssh://") || strings.HasPrefix(rawURL, "https://") || strings.HasPrefix(rawURL, "http://") || strings.HasPrefix(rawURL, "git://") {
		return parseStandardURL(rawURL, sshUser)
	}

	// Try SSH-shorthand format: git@host:owner/repo[.git]
	// Must have @ and :, but not look like a URL scheme
	if strings.Contains(rawURL, "@") && strings.Contains(rawURL, ":") {
		return parseSSHShorthand(rawURL, sshUser)
	}

	return "", fmt.Errorf("%w: unrecognized format (expected git@host:path, ssh://, https://, or http://)", ErrInvalidRepoURL)
}

// parseSSHShorthand parses git@host:owner/repo[.git] format.
func parseSSHShorthand(rawURL, sshUser string) (string, error) {
	parts := strings.SplitN(rawURL, "@", 2)
	if len(parts) != 2 {
		return "", fmt.Errorf("%w: invalid SSH shorthand", ErrInvalidRepoURL)
	}

	hostAndPath := parts[1]
	colonIdx := strings.IndexByte(hostAndPath, ':')
	if colonIdx < 0 {
		return "", fmt.Errorf("%w: missing ':' in SSH shorthand", ErrInvalidRepoURL)
	}

	host := hostAndPath[:colonIdx]
	path := hostAndPath[colonIdx+1:]

	if host == "" || path == "" {
		return "", fmt.Errorf("%w: host or path empty in SSH shorthand", ErrInvalidRepoURL)
	}

	path = normalizePath(path)
	return fmt.Sprintf("ssh://%s@%s/%s", sshUser, host, path), nil
}

// parseStandardURL parses https://, http://, ssh://, and git:// URLs.
func parseStandardURL(rawURL, sshUser string) (string, error) {
	u, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrInvalidRepoURL, err)
	}

	host := u.Hostname()
	if host == "" {
		return "", fmt.Errorf("%w: missing hostname", ErrInvalidRepoURL)
	}

	// Preserve non-standard ports
	port := u.Port()
	if port != "" {
		host = net.JoinHostPort(host, port)
	}

	// For SSH URLs, extract the user from the URL if provided
	if u.Scheme == "ssh" {
		if u.User != nil && u.User.Username() != "" {
			sshUser = u.User.Username()
		}
	}

	// Extract path and normalize
	path := u.Path
	if path == "" {
		return "", fmt.Errorf("%w: missing path", ErrInvalidRepoURL)
	}

	// Remove leading slash
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return "", fmt.Errorf("%w: empty path after scheme", ErrInvalidRepoURL)
	}

	path = normalizePath(path)
	return fmt.Sprintf("ssh://%s@%s/%s", sshUser, host, path), nil
}

// normalizePath strips .git suffix and re-adds it exactly once, validating path segments.
func normalizePath(path string) string {
	path = strings.TrimSuffix(path, ".git")
	path = strings.TrimSpace(path)
	
	// Ensure it ends with .git
	if !strings.HasSuffix(path, ".git") {
		path = path + ".git"
	}
	
	return path
}

// ValidateOwnerAndRepo checks if an owner/repo name matches safe-for-filesystem patterns.
// Returns true if both segments are alphanumeric+._- (typical for Git hosting platforms),
// but must start and end with alphanumeric (no leading/trailing dots, hyphens, or underscores).
// This is used to prevent path-traversal attacks when constructing cache paths.
func ValidateOwnerAndRepo(owner, repo string) bool {
	// Pattern: start with alphanumeric, followed by any alphanumeric/._-, end with alphanumeric
	// At least 1 character, at most 255 (filesystem limits)
	pattern := regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._\-]*[A-Za-z0-9])?$`)
	
	return len(owner) > 0 && len(owner) <= 255 &&
		len(repo) > 0 && len(repo) <= 255 &&
		pattern.MatchString(owner) &&
		pattern.MatchString(repo)
}

// IsSSHURL returns true if the URL uses SSH format.
func IsSSHURL(rawURL string) bool {
	if strings.HasPrefix(rawURL, "git@") {
		return true
	}
	if strings.HasPrefix(rawURL, "ssh://") {
		return true
	}
	return false
}
