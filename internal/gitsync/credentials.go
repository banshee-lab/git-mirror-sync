package gitsync

import (
	"crypto/aes"
	"crypto/cipher"
	"encoding/base64"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"forgejo-sync/internal/forgejodb"
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

// ExtractHTTPCredentials returns the first matching remote URL from a Forgejo-style repo config.
// It is kept for compatibility with older tests and legacy sync logic.
func ExtractHTTPCredentials(dataDir, owner, repo, targetURL string) (string, error) {
	if targetURL == "" {
		return "", nil
	}
	cfgPath := filepath.Join(dataDir, "git", "repositories", owner, repo+".git", "config")
	b, err := os.ReadFile(cfgPath)
	if err != nil {
		return targetURL, nil
	}
	targetHost := ""
	if parsed, err := url.Parse(targetURL); err == nil && parsed.Host != "" {
		targetHost = strings.ToLower(parsed.Hostname())
	}
	lines := strings.Split(string(b), "\n")
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "url = ") {
			continue
		}
		candidate := strings.TrimSpace(strings.TrimPrefix(trimmed, "url = "))
		if candidate == "" {
			continue
		}
		if targetHost != "" && strings.Contains(strings.ToLower(candidate), targetHost) {
			return candidate, nil
		}
		if targetHost == "" && (strings.Contains(candidate, "github.com") || strings.Contains(candidate, "gitlab.com")) {
			return candidate, nil
		}
	}
	return targetURL, nil
}

// ResolveSSHCredentials resolves SSH credentials for a repository.
// It supports both the modern single-key-path signature and the legacy Forgejo-style signature.
func ResolveSSHCredentials(args ...interface{}) (*SSHCredentials, error) {
	if len(args) == 2 {
		_, ok1 := args[0].(string)
		keyPath, ok2 := args[1].(string)
		if ok1 && ok2 {
			return &SSHCredentials{KeyPath: keyPath, SSHCommand: ResolveSSHCommand(keyPath), Cleanup: func() {}}, nil
		}
	}
	if len(args) == 3 {
		cacheDir, ok1 := args[0].(string)
		repo, ok2 := args[1].(*forgejodb.MatchedRepo)
		secret, ok3 := args[2].(string)
		if !ok1 || !ok2 || !ok3 {
			return nil, fmt.Errorf("invalid ResolveSSHCredentials arguments")
		}
		_ = cacheDir
		keyBytes, err := decryptForgejoKey(secret, repo.PrivateKey)
		if err != nil {
			return nil, err
		}
		if err := os.MkdirAll(cacheDir, 0700); err != nil {
			return nil, fmt.Errorf("mkdir cache dir: %w", err)
		}
		keyPath := filepath.Join(cacheDir, "tmp-key-"+strconv.FormatInt(time.Now().UnixNano(), 10)+".pem")
		if err := os.WriteFile(keyPath, keyBytes, 0600); err != nil {
			return nil, fmt.Errorf("write temp key: %w", err)
		}
		return &SSHCredentials{KeyPath: keyPath, SSHCommand: ResolveSSHCommand(keyPath), Cleanup: func() { _ = os.Remove(keyPath) }}, nil
	}
	return nil, fmt.Errorf("unsupported ResolveSSHCredentials signature")
}

func decryptForgejoKey(secret string, blob []byte) ([]byte, error) {
	if len(blob) == 0 {
		return nil, nil
	}
	if len(blob) <= aes.BlockSize {
		return blob, nil
	}
	key := []byte(secret)
	if len(key) < 32 {
		key = append(key, make([]byte, 32-len(key))...)
	} else if len(key) > 32 {
		key = key[:32]
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	iv := blob[:aes.BlockSize]
	ciphertext := blob[aes.BlockSize:]
	plain := make([]byte, len(ciphertext))
	stream := cipher.NewCFBDecrypter(block, iv)
	stream.XORKeyStream(plain, ciphertext)
	decoded, err := base64.StdEncoding.DecodeString(string(plain))
	if err == nil && len(decoded) > 0 {
		return decoded, nil
	}
	return plain, nil
}
