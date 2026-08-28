package gitsync

import (
	"bufio"
	"bytes"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"forgejo-sync/internal/forgejodb"
)

var (
	validNameRegex = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
	// remoteSectionRegex matches [remote "remote_mirror_<name>"]
	remoteSectionRegex = regexp.MustCompile(`^\s*\[\s*remote\s+"([^"]+)"\s*\]\s*$`)
	urlKeyRegex        = regexp.MustCompile(`^\s*url\s*=\s*(.+)$`)
)

// ValidateName checks if owner or repository name conforms to ^[A-Za-z0-9._-]+$ to prevent path traversal.
func ValidateName(name string) error {
	if !validNameRegex.MatchString(name) {
		return fmt.Errorf("invalid name %q: must match ^[A-Za-z0-9._-]+$", name)
	}
	return nil
}

// SSHCredentials contains the path to the temporary SSH key, the GIT_SSH_COMMAND string, and a cleanup function.
type SSHCredentials struct {
	KeyPath    string
	SSHCommand string
	Cleanup    func()
}

// ResolveSSHCredentials writes the private key from the matched push mirror to a 0600 temp file
// under <cacheDir>/.ssh-keys/key-*.
func ResolveSSHCredentials(cacheDir string, repo *forgejodb.MatchedRepo, secret string) (*SSHCredentials, error) {
	if repo == nil {
		return nil, errors.New("matched repo cannot be nil")
	}
	if len(repo.PrivateKey) == 0 {
		return &SSHCredentials{
			KeyPath:    "",
			SSHCommand: "ssh -o StrictHostKeyChecking=accept-new -o IdentitiesOnly=yes",
			Cleanup:    func() {},
		}, nil
	}

	keysDir := filepath.Join(cacheDir, ".ssh-keys")
	if err := os.MkdirAll(keysDir, 0700); err != nil {
		return nil, fmt.Errorf("failed to create ssh keys directory: %w", err)
	}

	tmpFile, err := os.CreateTemp(keysDir, "key-*")
	if err != nil {
		return nil, fmt.Errorf("failed to create temporary ssh key file: %w", err)
	}
	tmpKeyPath := tmpFile.Name()

	if err := os.Chmod(tmpKeyPath, 0600); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpKeyPath)
		return nil, fmt.Errorf("failed to set permissions on ssh key file: %w", err)
	}

	keyData, err := DecryptForgejoSecret(secret, repo.PrivateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to decrypt private key: %w", err)
	}

	// Ensure newline at the end if not present
	if len(keyData) > 0 && !bytes.HasSuffix(keyData, []byte("\n")) {
		keyData = append(keyData, '\n')
	}

	if _, err := tmpFile.Write(keyData); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpKeyPath)
		return nil, fmt.Errorf("failed to write ssh key to temp file: %w", err)
	}

	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpKeyPath)
		return nil, fmt.Errorf("failed to close ssh key temp file: %w", err)
	}

	cleanup := func() {
		_ = os.Remove(tmpKeyPath)
	}

	sshCmd := fmt.Sprintf("ssh -i %s -o StrictHostKeyChecking=accept-new -o IdentitiesOnly=yes", tmpKeyPath)
	return &SSHCredentials{
		KeyPath:    tmpKeyPath,
		SSHCommand: sshCmd,
		Cleanup:    cleanup,
	}, nil
}

// ExtractHTTPCredentials parses a Forgejo git repository config to find a remote "remote_mirror_*" section
// matching the target remote URL's host, and extracts the embedded credentials to return an authenticated URL.
func ExtractHTTPCredentials(forgejoDataDir, ownerName, repoName, targetURL string) (string, error) {
	if err := ValidateName(ownerName); err != nil {
		return "", fmt.Errorf("invalid owner name: %w", err)
	}
	if err := ValidateName(repoName); err != nil {
		return "", fmt.Errorf("invalid repo name: %w", err)
	}

	targetParsed, err := url.Parse(targetURL)
	if err != nil {
		return "", fmt.Errorf("invalid target URL: %w", err)
	}
	targetHost := strings.ToLower(targetParsed.Hostname())

	configPath := filepath.Join(forgejoDataDir, "git", "repositories", ownerName, repoName+".git", "config")
	file, err := os.Open(configPath)
	if err != nil {
		return "", fmt.Errorf("failed to open repository config: %w", err)
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	var currentSection string
	var candidateURLs []string

	for scanner.Scan() {
		line := scanner.Text()
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, ";") || trimmed == "" {
			continue
		}

		if match := remoteSectionRegex.FindStringSubmatch(line); match != nil {
			currentSection = match[1]
			continue
		} else if strings.HasPrefix(trimmed, "[") {
			currentSection = ""
			continue
		}

		if strings.HasPrefix(currentSection, "remote_mirror_") || currentSection == "origin" || strings.HasPrefix(currentSection, "remote ") {
			if match := urlKeyRegex.FindStringSubmatch(line); match != nil {
				candidateURLs = append(candidateURLs, strings.TrimSpace(match[1]))
			}
		}
	}

	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("failed to read repository config: %w", err)
	}

	// Try to find a URL matching target host with user credentials
	var bestURL *url.URL
	for _, raw := range candidateURLs {
		parsed, err := url.Parse(raw)
		if err != nil {
			continue
		}
		if strings.ToLower(parsed.Hostname()) == targetHost && parsed.User != nil {
			bestURL = parsed
			break
		}
	}

	if bestURL == nil {
		// No credentials found in config, return the original targetURL without modification
		return targetURL, nil
	}

	// Merge targetParsed with bestURL's credentials
	authenticated := *targetParsed
	authenticated.User = bestURL.User
	return authenticated.String(), nil
}

// IsSSHRemote returns true if the remote URL uses SSH format (e.g. git@... or ssh://...).
func IsSSHRemote(remoteURL string) bool {
	trimmed := strings.TrimSpace(remoteURL)
	if strings.HasPrefix(trimmed, "git@") {
		return true
	}
	if strings.HasPrefix(trimmed, "ssh://") {
		return true
	}
	u, err := url.Parse(trimmed)
	if err == nil && u.Scheme == "ssh" {
		return true
	}
	return false
}
