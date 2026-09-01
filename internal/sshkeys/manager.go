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
	if logger == nil {
		logger = slog.Default()
	}
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
	m.logger.Debug("sshkeys.GenerateKeyPair started",
		slog.String("name", name),
		slog.String("type", string(keyType)),
		slog.String("keys_dir", m.keysDir),
	)

	if err := os.MkdirAll(m.keysDir, 0700); err != nil {
		m.logger.Debug("sshkeys.GenerateKeyPair failed to create keys directory", slog.String("dir", m.keysDir), slog.Any("error", err))
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
			m.logger.Debug("sshkeys.GenerateKeyPair ed25519 generation failed", slog.Any("error", err))
			return store.SSHKey{}, fmt.Errorf("failed to generate ed25519 key: %w", err)
		}

		sshPub, err := ssh.NewPublicKey(pub)
		if err != nil {
			m.logger.Debug("sshkeys.GenerateKeyPair ed25519 public key conversion failed", slog.Any("error", err))
			return store.SSHKey{}, fmt.Errorf("failed to create public key: %w", err)
		}

		rawPrivateKey = priv
		publicKey = sshPub

	case KeyTypeRSA:
		privateKey, err := rsa.GenerateKey(rand.Reader, 4096)
		if err != nil {
			m.logger.Debug("sshkeys.GenerateKeyPair RSA generation failed", slog.Any("error", err))
			return store.SSHKey{}, fmt.Errorf("failed to generate RSA key: %w", err)
		}

		sshPub, err := ssh.NewPublicKey(&privateKey.PublicKey)
		if err != nil {
			m.logger.Debug("sshkeys.GenerateKeyPair RSA public key conversion failed", slog.Any("error", err))
			return store.SSHKey{}, fmt.Errorf("failed to create public key: %w", err)
		}

		rawPrivateKey = privateKey
		publicKey = sshPub

	default:
		m.logger.Debug("sshkeys.GenerateKeyPair unsupported key type", slog.String("type", string(keyType)))
		return store.SSHKey{}, fmt.Errorf("unsupported key type: %v", keyType)
	}

	// Generate public key line
	publicKeyLine := string(ssh.MarshalAuthorizedKey(publicKey))
	fingerprint := ssh.FingerprintSHA256(publicKey)
	m.logger.Debug("sshkeys.GenerateKeyPair generated key pair",
		slog.String("fingerprint", fingerprint),
		slog.String("type", string(keyType)),
	)

	// Generate private key in OpenSSH format
	privateKeyBlock, marshalErr := ssh.MarshalPrivateKey(rawPrivateKey, "")
	if marshalErr != nil {
		m.logger.Debug("sshkeys.GenerateKeyPair marshal private key failed", slog.Any("error", marshalErr))
		return store.SSHKey{}, fmt.Errorf("failed to marshal private key: %w", marshalErr)
	}

	// Encode the PEM block to bytes
	var privateKeyPEM []byte
	if privateKeyBlock != nil {
		buf := &bytes.Buffer{}
		if encodeErr := pem.Encode(buf, privateKeyBlock); encodeErr != nil {
			m.logger.Debug("sshkeys.GenerateKeyPair encode private key failed", slog.Any("error", encodeErr))
			return store.SSHKey{}, fmt.Errorf("failed to encode private key: %w", encodeErr)
		}
		privateKeyPEM = buf.Bytes()
	}

	// Use a stable filename based on the key name + a random component
	keyFileName := generateKeyFileName(name)
	privateKeyPath := filepath.Join(m.keysDir, keyFileName)
	publicKeyPath := filepath.Join(m.keysDir, keyFileName+".pub")

	m.logger.Debug("sshkeys.GenerateKeyPair writing key files",
		slog.String("private_key_path", privateKeyPath),
		slog.String("public_key_path", publicKeyPath),
	)

	// Write private key (0600)
	if err := os.WriteFile(privateKeyPath, privateKeyPEM, 0600); err != nil {
		os.Remove(publicKeyPath) // cleanup if partial
		m.logger.Debug("sshkeys.GenerateKeyPair write private key failed", slog.String("path", privateKeyPath), slog.Any("error", err))
		return store.SSHKey{}, fmt.Errorf("failed to write private key: %w", err)
	}

	// Write public key (0644)
	if err := os.WriteFile(publicKeyPath, []byte(publicKeyLine), 0644); err != nil {
		os.Remove(privateKeyPath)
		m.logger.Debug("sshkeys.GenerateKeyPair write public key failed", slog.String("path", publicKeyPath), slog.Any("error", err))
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
		m.logger.Debug("sshkeys.GenerateKeyPair store in database failed", slog.Any("error", err))
		return store.SSHKey{}, fmt.Errorf("failed to store key in database: %w", err)
	}

	m.logger.Info("SSH key pair generated successfully",
		"name", name,
		"id", createdKey.ID,
		"fingerprint", fingerprint,
	)
	m.logger.Debug("sshkeys.GenerateKeyPair completed",
		slog.Int64("id", createdKey.ID),
		slog.String("name", createdKey.Name),
		slog.String("fingerprint", createdKey.Fingerprint),
		slog.String("private_path", createdKey.PrivatePath),
		slog.String("public_path", createdKey.PublicPath),
	)

	return createdKey, nil
}

// GetPublicKey retrieves the public key content for display/copy.
func (m *Manager) GetPublicKey(ctx context.Context, keyID int64) (string, error) {
	m.logger.Debug("sshkeys.GetPublicKey started", slog.Int64("key_id", keyID))
	if ctx == nil {
		ctx = context.Background()
	}
	key, err := m.keyStore.Get(ctx, keyID)
	if err != nil {
		m.logger.Debug("sshkeys.GetPublicKey key lookup failed", slog.Int64("key_id", keyID), slog.Any("error", err))
		return "", fmt.Errorf("failed to get key: %w", err)
	}

	publicKeyPath := filepath.Join(m.keysDir, key.PublicPath)
	m.logger.Debug("sshkeys.GetPublicKey reading public key file", slog.String("path", publicKeyPath))
	content, err := os.ReadFile(publicKeyPath)
	if err != nil {
		m.logger.Debug("sshkeys.GetPublicKey read file failed", slog.String("path", publicKeyPath), slog.Any("error", err))
		return "", fmt.Errorf("failed to read public key file: %w", err)
	}

	m.logger.Debug("sshkeys.GetPublicKey succeeded",
		slog.Int64("key_id", keyID),
		slog.Int("bytes_read", len(content)),
	)
	return string(content), nil
}

// Delete removes an SSH key if it's not in use by any repository.
func (m *Manager) Delete(ctx context.Context, keyID int64) error {
	m.logger.Debug("sshkeys.Delete started", slog.Int64("key_id", keyID))
	if ctx == nil {
		ctx = context.Background()
	}
	key, err := m.keyStore.Get(ctx, keyID)
	if err != nil {
		m.logger.Debug("sshkeys.Delete key lookup failed", slog.Int64("key_id", keyID), slog.Any("error", err))
		return fmt.Errorf("failed to get key: %w", err)
	}

	// Check if key is in use
	inUse, err := m.keyStore.IsKeyInUse(ctx, keyID)
	if err != nil {
		m.logger.Debug("sshkeys.Delete check key usage failed", slog.Int64("key_id", keyID), slog.Any("error", err))
		return fmt.Errorf("failed to check key usage: %w", err)
	}

	if inUse {
		m.logger.Debug("sshkeys.Delete rejected: key is in use by repositories", slog.Int64("key_id", keyID))
		return fmt.Errorf("cannot delete key in use by one or more repositories")
	}

	// Delete files
	privateKeyPath := filepath.Join(m.keysDir, key.PrivatePath)
	publicKeyPath := filepath.Join(m.keysDir, key.PublicPath)

	m.logger.Debug("sshkeys.Delete removing key files",
		slog.String("private_key_path", privateKeyPath),
		slog.String("public_key_path", publicKeyPath),
	)

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
		m.logger.Debug("sshkeys.Delete database delete failed", slog.Int64("key_id", keyID), slog.Any("error", err))
		return fmt.Errorf("failed to delete key from database: %w", err)
	}

	m.logger.Info("SSH key deleted successfully",
		"name", key.Name,
		"id", keyID,
	)
	m.logger.Debug("sshkeys.Delete completed successfully", slog.Int64("key_id", keyID), slog.String("name", key.Name))
	return nil
}

// GetPrivateKeyPath returns the filesystem path to the private key.
// Used by git operations that need to set GIT_SSH_COMMAND.
func (m *Manager) GetPrivateKeyPath(ctx context.Context, keyID int64) (string, error) {
	m.logger.Debug("sshkeys.GetPrivateKeyPath started", slog.Int64("key_id", keyID))
	if ctx == nil {
		ctx = context.Background()
	}
	key, err := m.keyStore.Get(ctx, keyID)
	if err != nil {
		m.logger.Debug("sshkeys.GetPrivateKeyPath key lookup failed", slog.Int64("key_id", keyID), slog.Any("error", err))
		return "", fmt.Errorf("failed to get key: %w", err)
	}

	fullPath := filepath.Join(m.keysDir, key.PrivatePath)
	m.logger.Debug("sshkeys.GetPrivateKeyPath resolved path",
		slog.Int64("key_id", keyID),
		slog.String("path", fullPath),
	)
	return fullPath, nil
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
