package gitsync

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// ErrConflict is returned when a push is rejected due to non-fast-forward / diverging history.
type ErrConflict struct {
	Detail string
}

func (e *ErrConflict) Error() string {
	return fmt.Sprintf("push rejected due to conflict (non-fast-forward): %s", e.Detail)
}

// PushResult contains information about a successful push.
type PushResult struct {
	BeforeSHA string
	AfterSHA  string
	RawOutput string
}

// PushOptions holds options for pushing a repository mirror to a remote.
type PushOptions struct {
	CachePath             string
	RemoteURL             string
	RemoteKeyPath         string
	Timeout               time.Duration
	Owner                 string
	Repo                  string
	ForgejoDomain         string
	ForgejoSSHPort        int
	ForgejoPushSSHKeyPath string
}

var (
	// conflictIndicators matches standard git push non-fast-forward rejections
	conflictIndicators = []string{
		"[rejected - non-fast-forward]",
		"[rejected] (non-fast-forward)",
		"[remote rejected] (non-fast-forward)",
		"[rejected] (fetch first)",
		"[remote rejected] (fetch first)",
		"! [rejected]",
		"non-fast-forward",
	}

	// shaRangeRegex matches git push output like "a1b2c3d..e4f5a6b branch -> branch"
	shaRangeRegex = regexp.MustCompile(`([0-9a-fA-F]{7,40})\.\.([0-9a-fA-F]{7,40})`)
)

// PushToRemote pushes the bare mirror cache to a remote SSH URL without force.
func PushToRemote(ctx context.Context, opts PushOptions) (*PushResult, error) {
	start := time.Now()
	slog.Debug("gitsync.PushToRemote started",
		slog.String("cache_path", opts.CachePath),
		slog.String("remote_url", opts.RemoteURL),
		slog.String("remote_key_path", opts.RemoteKeyPath),
		slog.Duration("timeout", opts.Timeout),
		slog.String("owner", opts.Owner),
		slog.String("repo", opts.Repo),
	)

	if opts.CachePath == "" {
		slog.Debug("gitsync.PushToRemote failed: empty cache path")
		return nil, fmt.Errorf("cache path cannot be empty")
	}
	if opts.RemoteURL == "" {
		slog.Debug("gitsync.PushToRemote failed: empty remote URL")
		return nil, fmt.Errorf("remote URL cannot be empty")
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	sshCmd := ResolveSSHCommand(opts.RemoteKeyPath)
	slog.Debug("gitsync.PushToRemote resolved SSH command", slog.String("ssh_cmd", sshCmd))

	cmd := exec.CommandContext(ctx, "git", "-C", opts.CachePath, "push", opts.RemoteURL, "refs/heads/*:refs/heads/*", "refs/tags/*:refs/tags/*")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")
	cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND="+sshCmd)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	slog.Debug("gitsync.PushToRemote executing git push",
		slog.String("cache_path", opts.CachePath),
		slog.String("remote_url", opts.RemoteURL),
	)

	err := cmd.Run()
	outputStr := sanitizeString(stderr.String() + stdout.String())

	if err != nil {
		lowerOut := strings.ToLower(outputStr)
		for _, ind := range conflictIndicators {
			if strings.Contains(lowerOut, strings.ToLower(ind)) {
				slog.Debug("gitsync.PushToRemote detected conflict",
					slog.Duration("duration", time.Since(start)),
					slog.String("conflict_indicator", ind),
					slog.String("output", outputStr),
				)
				return nil, &ErrConflict{Detail: strings.TrimSpace(outputStr)}
			}
		}
		slog.Debug("gitsync.PushToRemote failed",
			slog.Duration("duration", time.Since(start)),
			slog.String("output", outputStr),
			slog.Any("error", err),
		)
		return nil, fmt.Errorf("git push failed: %w (output: %s)", err, strings.TrimSpace(outputStr))
	}

	result := &PushResult{
		RawOutput: strings.TrimSpace(outputStr),
	}

	// Try to extract before/after SHAs if present in the push output
	if matches := shaRangeRegex.FindStringSubmatch(outputStr); len(matches) == 3 {
		result.BeforeSHA = matches[1]
		result.AfterSHA = matches[2]
	}

	slog.Debug("gitsync.PushToRemote succeeded",
		slog.Duration("duration", time.Since(start)),
		slog.String("before_sha", result.BeforeSHA),
		slog.String("after_sha", result.AfterSHA),
		slog.String("raw_output", result.RawOutput),
	)

	return result, nil
}

// PushToForgejo is kept for compatibility with the legacy worker contract.
func PushToForgejo(ctx context.Context, opts PushOptions) (*PushResult, error) {
	return PushToRemote(ctx, opts)
}
