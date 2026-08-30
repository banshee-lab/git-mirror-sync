package gitsync

import (
	"fmt"
	"regexp"
)

// SSHCredentials provides an SSH key path plus the command environment for git operations.
type SSHCredentials struct {
	KeyPath    string
	SSHCommand string
	Cleanup    func()
}

// ResolveSSHCommand builds the GIT_SSH_COMMAND environment variable for a given private key path.
// The key is expected to be on disk at keyPath with proper 0600 permissions.
func ResolveSSHCommand(keyPath string) string {
	if keyPath == "" {
		return "ssh -o StrictHostKeyChecking=accept-new -o IdentitiesOnly=yes"
	}
	return fmt.Sprintf("ssh -i %s -o StrictHostKeyChecking=accept-new -o IdentitiesOnly=yes", keyPath)
}

// ValidateName ensures a repository or owner name is safe for filesystem use.
func ValidateName(name string) error {
	if name == "" {
		return fmt.Errorf("name cannot be empty")
	}
	if !regexp.MustCompile(`^[A-Za-z0-9._-]+$`).MatchString(name) {
		return fmt.Errorf("invalid repository name %q", name)
	}
	return nil
}

// ResolveSSHCredentials resolves an on-disk SSH key path into git SSH command arguments.
func ResolveSSHCredentials(keyPath string) (*SSHCredentials, error) {
	if keyPath == "" {
		return nil, fmt.Errorf("key path cannot be empty")
	}
	return &SSHCredentials{KeyPath: keyPath, SSHCommand: ResolveSSHCommand(keyPath), Cleanup: func() {}}, nil
}
