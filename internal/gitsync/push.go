package gitsync

import (
	"bytes"
	"context"
	"fmt"
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

// PushOptions holds options for pushing a repository mirror to Forgejo.
type PushOptions struct {
	CachePath             string
	Owner                 string
	Repo                  string
	ForgejoDomain         string
	ForgejoSSHPort        int
	ForgejoPushSSHKeyPath string
	Timeout               time.Duration
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

// PushToForgejo pushes the bare mirror cache to Forgejo over SSH without force.
func PushToForgejo(ctx context.Context, opts PushOptions) (*PushResult, error) {
	if err := ValidateName(opts.Owner); err != nil {
		return nil, fmt.Errorf("invalid owner name: %w", err)
	}
	if err := ValidateName(opts.Repo); err != nil {
		return nil, fmt.Errorf("invalid repo name: %w", err)
	}
	if opts.ForgejoDomain == "" {
		return nil, fmt.Errorf("forgejo domain cannot be empty")
	}
	port := opts.ForgejoSSHPort
	if port <= 0 {
		port = 22
	}

	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	pushTarget := fmt.Sprintf("ssh://git@%s:%d/%s/%s.git", opts.ForgejoDomain, port, opts.Owner, opts.Repo)

	cmd := exec.CommandContext(ctx, "git", "-C", opts.CachePath, "push", pushTarget, "refs/heads/*:refs/heads/*", "refs/tags/*:refs/tags/*")
	cmd.Env = append(os.Environ(), "GIT_TERMINAL_PROMPT=0")

	if opts.ForgejoPushSSHKeyPath != "" {
		sshCmd := fmt.Sprintf("ssh -i %s -o StrictHostKeyChecking=accept-new -o IdentitiesOnly=yes", opts.ForgejoPushSSHKeyPath)
		cmd.Env = append(cmd.Env, "GIT_SSH_COMMAND="+sshCmd)
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	outputStr := sanitizeString(stderr.String() + stdout.String())

	if err != nil {
		lowerOut := strings.ToLower(outputStr)
		for _, ind := range conflictIndicators {
			if strings.Contains(lowerOut, strings.ToLower(ind)) {
				return nil, &ErrConflict{Detail: strings.TrimSpace(outputStr)}
			}
		}
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

	return result, nil
}
