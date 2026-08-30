package sshkeys

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"testing"

	"forgejo-sync/internal/store"
)

type mockKeyStore struct {
	keys map[int64]store.SSHKey
	next int64
}

func newMockKeyStore() *mockKeyStore {
	return &mockKeyStore{
		keys: make(map[int64]store.SSHKey),
		next: 1,
	}
}

func (m *mockKeyStore) Create(ctx context.Context, key store.SSHKey) (store.SSHKey, error) {
	key.ID = m.next
	m.next++
	m.keys[key.ID] = key
	return key, nil
}

func (m *mockKeyStore) Get(ctx context.Context, id int64) (store.SSHKey, error) {
	k, ok := m.keys[id]
	if !ok {
		return store.SSHKey{}, errors.New("not found")
	}
	return k, nil
}

func (m *mockKeyStore) GetByName(ctx context.Context, name string) (store.SSHKey, error) {
	for _, k := range m.keys {
		if k.Name == name {
			return k, nil
		}
	}
	return store.SSHKey{}, errors.New("not found")
}

func (m *mockKeyStore) List(ctx context.Context) ([]store.SSHKey, error) {
	var res []store.SSHKey
	for _, v := range m.keys {
		res = append(res, v)
	}
	return res, nil
}

func (m *mockKeyStore) Delete(ctx context.Context, id int64) error {
	delete(m.keys, id)
	return nil
}

func (m *mockKeyStore) IsKeyInUse(ctx context.Context, id int64) (bool, error) {
	return false, nil
}

func TestGenerateKeyPair(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sshkeys-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	keyStore := newMockKeyStore()
	mgr := NewManager(tmpDir, keyStore, nil, logger)
	ctx := context.Background()

	t.Run("ed25519", func(t *testing.T) {
		key, err := mgr.GenerateKeyPair(ctx, KeyTypeEd25519, "test-ed25519")
		if err != nil {
			t.Fatalf("GenerateKeyPair(ed25519) error: %v", err)
		}
		if key.Fingerprint == "" {
			t.Errorf("expected fingerprint, got empty")
		}
		pubContent, err := mgr.GetPublicKey(ctx, key.ID)
		if err != nil {
			t.Fatalf("GetPublicKey error: %v", err)
		}
		if len(pubContent) == 0 {
			t.Errorf("empty public key content")
		}
	})

	t.Run("rsa", func(t *testing.T) {
		key, err := mgr.GenerateKeyPair(ctx, KeyTypeRSA, "test-rsa")
		if err != nil {
			t.Fatalf("GenerateKeyPair(rsa) error: %v", err)
		}
		if key.Fingerprint == "" {
			t.Errorf("expected fingerprint, got empty")
		}
		pubContent, err := mgr.GetPublicKey(ctx, key.ID)
		if err != nil {
			t.Fatalf("GetPublicKey error: %v", err)
		}
		if len(pubContent) == 0 {
			t.Errorf("empty public key content")
		}
	})
}

type inUseMockKeyStore struct {
	*mockKeyStore
	inUseKeyIDs map[int64]bool
}

func (m *inUseMockKeyStore) IsKeyInUse(ctx context.Context, id int64) (bool, error) {
	return m.inUseKeyIDs[id], nil
}

func TestDeleteKey(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "sshkeys-delete-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	baseKeyStore := newMockKeyStore()
	keyStore := &inUseMockKeyStore{
		mockKeyStore: baseKeyStore,
		inUseKeyIDs:  make(map[int64]bool),
	}
	mgr := NewManager(tmpDir, keyStore, nil, logger)
	ctx := context.Background()

	key, err := mgr.GenerateKeyPair(ctx, KeyTypeEd25519, "to-delete")
	if err != nil {
		t.Fatalf("GenerateKeyPair error: %v", err)
	}

	t.Run("fails when key is in use", func(t *testing.T) {
		keyStore.inUseKeyIDs[key.ID] = true
		defer func() { keyStore.inUseKeyIDs[key.ID] = false }()

		err := mgr.Delete(ctx, key.ID)
		if err == nil {
			t.Fatalf("expected error deleting key in use, got nil")
		}
		if err.Error() != "cannot delete key in use by one or more repositories" {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("successfully deletes when not in use", func(t *testing.T) {
		keyStore.inUseKeyIDs[key.ID] = false

		err := mgr.Delete(ctx, key.ID)
		if err != nil {
			t.Fatalf("Delete() error: %v", err)
		}

		_, err = keyStore.Get(ctx, key.ID)
		if err == nil {
			t.Errorf("expected key to be removed from store, but it was found")
		}
	})
}

