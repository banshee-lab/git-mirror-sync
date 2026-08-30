package sshkeys

import (
	"bytes"
	"context"
	"crypto"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/pem"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"

	"golang.org/x/crypto/ssh"

	"forgejo-sync/internal/store"
)

// KeyType represents the type of SSH key to generate.
type KeyType string

const (
	KeyTypeRSA     KeyType = "rsa"
	KeyTypeEd25519 KeyType = "ed25519"
)

// Manager handles SSH key generation, storage, and retrieval.
type Manager struct {
	keysDir     string        // directory where keys are stored
	keyStore    store.SSHKeyStore
	repoStore   store.RepositoryStore
	logger      *slog.Logger
}

// NewManager creates a new SSH key manager.
func NewManager(keysDir string, keyStore store.SSHKeyStore, repoStore store.RepositoryStore, logger *slog.Logger) *Manager {
	return &Manager{
		keysDir:   keysDir,
		keyStore:  keyStore,
		repoStore: repoStore,
		logger:    logger,
	}
}

// GenerateKeyPair generates a new SSH key pair and stores it.
// Returns the created SSHKey record and any error.
func (m *Manager) GenerateKeyPair(ctx context.Context, keyType KeyType, name string) (store.SSHKey, error) {
	m.logger.Info("generating SSH key pair",
		"name", name,
		"type", string(keyType),
	)

	if err := os.MkdirAll(m.keysDir, 0700); err != nil {
		return store.SSHKey{}, fmt.Errorf("failed to create keys directory: %w", err)
	}

	// Generate the key material
	var (
		rawPrivateKey crypto.PrivateKey
		publicKey     ssh.PublicKey
	)

	switch keyType {
	case KeyTypeEd25519:
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			return store.SSHKey{}, fmt.Errorf("failed to generate ed25519 key: %w", err)
		}

		sshPub, err := ssh.NewPublicKey(pub)
		if err != nil {
			return store.SSHKey{}, fmt.Errorf("failed to create public key: %w", err)
		}

		rawPrivateKey = priv
		publicKey = sshPub

	case KeyTypeRSA:
		privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
		if err != nil {
			return store.SSHKey{}, fmt.Errorf("failed to generate RSA key: %w", err)
		}

		sshPub, err := ssh.NewPublicKey(&privateKey.PublicKey)
		if err != nil {
			return store.SSHKey{}, fmt.Errorf("failed to create public key: %w", err)
		}

		rawPrivateKey = privateKey
		publicKey = sshPub

	default:
		return store.SSHKey{}, fmt.Errorf("unsupported key type: %v", keyType)
	}

	// Generate public key line
	publicKeyLine := string(ssh.MarshalAuthorizedKey(publicKey))
	fingerprint := ssh.FingerprintSHA256(publicKey)

	// Generate private key in OpenSSH format
	privateKeyBlock, marshalErr := ssh.MarshalPrivateKey(rawPrivateKey, "")
	if marshalErr != nil {
		return store.SSHKey{}, fmt.Errorf("failed to marshal private key: %w", marshalErr)
	}

	// Encode the PEM block to bytes
	var privateKeyPEM []byte
	if privateKeyBlock != nil {
		buf := &bytes.Buffer{}
		if encodeErr := pem.Encode(buf, privateKeyBlock); encodeErr != nil {
			return store.SSHKey{}, fmt.Errorf("failed to encode private key: %w", encodeErr)
		}
		privateKeyPEM = buf.Bytes()
	}

	// Use a stable filename based on the key name + a random component
	keyFileName := generateKeyFileName(name)
	privateKeyPath := filepath.Join(m.keysDir, keyFileName)
	publicKeyPath := filepath.Join(m.keysDir, keyFileName+".pub")

	// Write private key (0600)
	if err := os.WriteFile(privateKeyPath, privateKeyPEM, 0600); err != nil {
		os.Remove(publicKeyPath) // cleanup if partial
		return store.SSHKey{}, fmt.Errorf("failed to write private key: %w", err)
	}

	// Write public key (0644)
	if err := os.WriteFile(publicKeyPath, []byte(publicKeyLine), 0644); err != nil {
		os.Remove(privateKeyPath)
		return store.SSHKey{}, fmt.Errorf("failed to write public key: %w", err)
	}

	// Store in database
	key := store.SSHKey{
		Name:        name,
		KeyType:     string(keyType),
		PrivatePath: keyFileName,
		PublicPath:  keyFileName + ".pub",
		Fingerprint: fingerprint,
	}

	if ctx == nil {
		ctx = context.Background()
	}
	createdKey, err := m.keyStore.Create(ctx, key)
	if err != nil {
		os.Remove(privateKeyPath)
		os.Remove(publicKeyPath)
		return store.SSHKey{}, fmt.Errorf("failed to store key in database: %w", err)
	}

	m.logger.Info("SSH key pair generated successfully",
		"name", name,
		"id", createdKey.ID,
		"fingerprint", fingerprint,
	)

	return createdKey, nil
}

// GetPublicKey retrieves the public key content for display/copy.
func (m *Manager) GetPublicKey(ctx context.Context, keyID int64) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	key, err := m.keyStore.Get(ctx, keyID)
	if err != nil {
		return "", fmt.Errorf("failed to get key: %w", err)
	}

	publicKeyPath := filepath.Join(m.keysDir, key.PublicPath)
	content, err := os.ReadFile(publicKeyPath)
	if err != nil {
		return "", fmt.Errorf("failed to read public key file: %w", err)
	}

	return string(content), nil
}

// Delete removes an SSH key if it's not in use by any repository.
func (m *Manager) Delete(ctx context.Context, keyID int64) error {
	if ctx == nil {
		ctx = context.Background()
	}
	key, err := m.keyStore.Get(ctx, keyID)
	if err != nil {
		return fmt.Errorf("failed to get key: %w", err)
	}

	// Check if key is in use
	inUse, err := m.keyStore.IsKeyInUse(ctx, keyID)
	if err != nil {
		return fmt.Errorf("failed to check key usage: %w", err)
	}

	if inUse {
		return fmt.Errorf("cannot delete key in use by one or more repositories")
	}

	// Delete files
	privateKeyPath := filepath.Join(m.keysDir, key.PrivatePath)
	publicKeyPath := filepath.Join(m.keysDir, key.PublicPath)

	if err := os.Remove(privateKeyPath); err != nil && !os.IsNotExist(err) {
		m.logger.Error("failed to delete private key file",
			"path", privateKeyPath,
			"err", err,
		)
		// Continue anyway - remove from DB
	}

	if err := os.Remove(publicKeyPath); err != nil && !os.IsNotExist(err) {
		m.logger.Error("failed to delete public key file",
			"path", publicKeyPath,
			"err", err,
		)
		// Continue anyway - remove from DB
	}

	// Delete from database
	if err := m.keyStore.Delete(ctx, keyID); err != nil {
		return fmt.Errorf("failed to delete key from database: %w", err)
	}

	m.logger.Info("SSH key deleted successfully",
		"name", key.Name,
		"id", keyID,
	)

	return nil
}

// GetPrivateKeyPath returns the filesystem path to the private key.
// Used by git operations that need to set GIT_SSH_COMMAND.
func (m *Manager) GetPrivateKeyPath(ctx context.Context, keyID int64) (string, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	key, err := m.keyStore.Get(ctx, keyID)
	if err != nil {
		return "", fmt.Errorf("failed to get key: %w", err)
	}

	return filepath.Join(m.keysDir, key.PrivatePath), nil
}

// generateKeyFileName creates a unique filename for key storage.
// Uses name + hash to create a stable but unique filename.
func generateKeyFileName(name string) string {
	// Create a stable hash-based suffix to avoid collisions
	hash := sha256.Sum256([]byte(name + randomSuffix()))
	suffix := base64.RawURLEncoding.EncodeToString(hash[:8])

	return fmt.Sprintf("id_%s_%s", sanitizeName(name), suffix)
}

// sanitizeName removes unsafe characters from the key name for use in filenames.
func sanitizeName(name string) string {
	result := ""
	for _, ch := range name {
		if (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') || (ch >= '0' && ch <= '9') || ch == '_' || ch == '-' {
			result += string(ch)
		} else {
			result += "_"
		}
	}
	return result
}

// randomSuffix generates a small random string for added uniqueness.
func randomSuffix() string {
	b := make([]byte, 4)
	rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}
