package gitsync

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"time"

	"forgejo-sync/internal/forgejodb"
)

// SyncOptions holds parameters required for cloning or fetching a repository.
type SyncOptions struct {
	CacheDir       string
	Provider       string
	Owner          string
	Repo           string
	SourceURL      string
	SSHCommand     string
	Timeout        time.Duration
	ForgejoDataDir string
	MatchedRepo    *forgejodb.MatchedRepo
	ForgejoSecret  string
}

// GetCachePath returns the bare mirror cache path: <CACHE_DIR>/{provider}/{owner}/{repo}.git
func GetCachePath(cacheDir, provider, owner, repo string) (string, error) {
	if err := ValidateName(provider); err != nil {
		return "", fmt.Errorf("invalid provider name: %w", err)
	}
	if err := ValidateName(owner); err != nil {
		return "", fmt.Errorf("invalid owner name: %w", err)
	}
	if err := ValidateName(repo); err != nil {
		return "", fmt.Errorf("invalid repo name: %w", err)
	}
	return filepath.Join(cacheDir, provider, owner, repo+".git"), nil
}

var urlCredsRegex = regexp.MustCompile(`(https?://)([^:@\s]+):([^@\s]+)@`)

func sanitizeString(s string) string {
	return urlCredsRegex.ReplaceAllString(s, "${1}***:***@")
}

// SanitizeError strips sensitive credentials (such as passwords or tokens in URLs) from error strings or logs.
func SanitizeError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%s", sanitizeString(err.Error()))
}

// SyncRepository performs the initial mirror clone if the bare cache does not exist,
// or a fetch --prune if it already exists.
func SyncRepository(ctx context.Context, opts SyncOptions) (string, error) {
	cachePath, err := GetCachePath(opts.CacheDir, opts.Provider, opts.Owner, opts.Repo)
	if err != nil {
		return "", err
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Resolve credentials and effective source URL
	sourceURL := opts.SourceURL
	sshCmd := opts.SSHCommand

	if IsSSHRemote(sourceURL) {
		if sshCmd == "" && opts.MatchedRepo != nil {
			creds, err := ResolveSSHCredentials(opts.CacheDir, opts.MatchedRepo, opts.ForgejoSecret)
			if err != nil {
				return "", fmt.Errorf("failed to resolve SSH credentials: %w", err)
			}
			defer creds.Cleanup()
			sshCmd = creds.SSHCommand
		}
	} else {
		// HTTP/HTTPS remote
		if opts.ForgejoDataDir != "" {
			forgejoOwner := opts.Owner
			forgejoRepo := opts.Repo
			if opts.MatchedRepo != nil {
				if opts.MatchedRepo.OwnerName != "" {
					forgejoOwner = opts.MatchedRepo.OwnerName
				}
				if opts.MatchedRepo.RepoName != "" {
					forgejoRepo = opts.MatchedRepo.RepoName
				}
			}
			authURL, err := ExtractHTTPCredentials(opts.ForgejoDataDir, forgejoOwner, forgejoRepo, sourceURL)
			if err == nil && authURL != "" {
				sourceURL = authURL
			}
		}
	}

	// Check if bare repo directory exists and has HEAD
	headFile := filepath.Join(cachePath, "HEAD")
	_, headErr := os.Stat(headFile)
	isInitial := os.IsNotExist(headErr)

	if isInitial {
		// Ensure parent directory exists
		if err := os.MkdirAll(filepath.Dir(cachePath), 0755); err != nil {
			return "", fmt.Errorf("failed to create cache directory: %w", err)
		}

		// git clone --mirror <source_url> <cache_path>
		cmd := exec.CommandContext(ctx, "git", "clone", "--mirror", sourceURL, cachePath)
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		if sshCmd != "" {
			cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND="+sshCmd)
		}

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			// If clone fails, clean up partial directory
			_ = os.RemoveAll(cachePath)
			sanitizedErr := sanitizeString(stderr.String() + stdout.String())
			return "", fmt.Errorf("git clone --mirror failed: %w (output: %s)", err, sanitizedErr)
		}
	} else {
		// Set remote origin URL if needed
		setURLCmd := exec.CommandContext(ctx, "git", "-C", cachePath, "remote", "set-url", "origin", sourceURL)
		setURLCmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		_ = setURLCmd.Run()

		// git -C <cache_path> fetch --prune origin '+refs/*:refs/*'
		cmd := exec.CommandContext(ctx, "git", "-C", cachePath, "fetch", "--prune", "origin", "+refs/*:refs/*")
		cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
		if sshCmd != "" {
			cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND="+sshCmd)
		}

		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr

		if err := cmd.Run(); err != nil {
			sanitizedErr := sanitizeString(stderr.String() + stdout.String())
			return "", fmt.Errorf("git fetch --prune failed: %w (output: %s)", err, sanitizedErr)
		}
	}

	return cachePath, nil
}
