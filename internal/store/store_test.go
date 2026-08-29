package store_test

import (
	"context"
	"os"
	"testing"
	"time"

	"forgejo-sync/internal/store"
)

func TestRepositoryStoreCRUD(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	repos := db.Repositories()

	// Create an SSH key first (foreign key requirement)
	keyStore := db.SSHKeys()
	key, err := keyStore.Create(ctx, store.SSHKey{
		Name:        "test-key",
		KeyType:     "ed25519",
		PrivatePath: "keys/test-key",
		PublicPath:  "keys/test-key.pub",
		Fingerprint: "abc123",
	})
	if err != nil {
		t.Fatalf("failed to create SSH key: %v", err)
	}

	// Create a repository
	repo := store.Repository{
		Name:            "test-repo",
		PullURLRaw:      "git@github.com:user/repo",
		PullURLSSH:      "ssh://git@github.com/user/repo.git",
		PullSSHKeyID:    key.ID,
		PushURLRaw:      "git@github.com:user/mirror",
		PushURLSSH:      "ssh://git@github.com/user/mirror.git",
		PushSSHKeyID:    key.ID,
		WebhookSecHash:  "$2a$10$hashedvalue",
		ProviderHint:    "github",
		Enabled:         true,
	}

	created, err := repos.Create(ctx, repo)
	if err != nil {
		t.Fatalf("failed to create repository: %v", err)
	}

	if created.ID == 0 {
		t.Errorf("expected non-zero ID, got %d", created.ID)
	}

	// Retrieve
	retrieved, err := repos.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("failed to get repository: %v", err)
	}

	if retrieved.Name != repo.Name {
		t.Errorf("expected name %q, got %q", repo.Name, retrieved.Name)
	}
	if retrieved.PullURLSSH != repo.PullURLSSH {
		t.Errorf("expected pull URL %q, got %q", repo.PullURLSSH, retrieved.PullURLSSH)
	}

	// Update
	created.Enabled = false
	updated, err := repos.Update(ctx, created)
	if err != nil {
		t.Fatalf("failed to update repository: %v", err)
	}

	retrieved, err = repos.Get(ctx, updated.ID)
	if err != nil {
		t.Fatalf("failed to get updated repository: %v", err)
	}
	if retrieved.Enabled {
		t.Errorf("expected Enabled=false, got true")
	}

	// List
	list, err := repos.List(ctx)
	if err != nil {
		t.Fatalf("failed to list repositories: %v", err)
	}
	if len(list) == 0 {
		t.Errorf("expected at least 1 repository, got 0")
	}

	// GetByName
	byName, err := repos.GetByName(ctx, repo.Name)
	if err != nil {
		t.Fatalf("failed to get repository by name: %v", err)
	}
	if byName.ID != created.ID {
		t.Errorf("expected ID %d, got %d", created.ID, byName.ID)
	}

	// Delete
	err = repos.Delete(ctx, created.ID)
	if err != nil {
		t.Fatalf("failed to delete repository: %v", err)
	}

	_, err = repos.Get(ctx, created.ID)
	if err == nil {
		t.Errorf("expected error when getting deleted repository, got nil")
	}
}

func TestSSHKeyStoreCRUD(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	keyStore := db.SSHKeys()

	// Create
	key := store.SSHKey{
		Name:        "my-key",
		KeyType:     "rsa",
		PrivatePath: "keys/my-key-id_rsa",
		PublicPath:  "keys/my-key-id_rsa.pub",
		Fingerprint: "SHA256:abc123def456",
	}

	created, err := keyStore.Create(ctx, key)
	if err != nil {
		t.Fatalf("failed to create SSH key: %v", err)
	}

	if created.ID == 0 {
		t.Errorf("expected non-zero ID, got %d", created.ID)
	}

	// Retrieve
	retrieved, err := keyStore.Get(ctx, created.ID)
	if err != nil {
		t.Fatalf("failed to get SSH key: %v", err)
	}

	if retrieved.KeyType != key.KeyType {
		t.Errorf("expected key type %q, got %q", key.KeyType, retrieved.KeyType)
	}

	// GetByName
	byName, err := keyStore.GetByName(ctx, key.Name)
	if err != nil {
		t.Fatalf("failed to get SSH key by name: %v", err)
	}
	if byName.ID != created.ID {
		t.Errorf("expected ID %d, got %d", created.ID, byName.ID)
	}

	// List
	list, err := keyStore.List(ctx)
	if err != nil {
		t.Fatalf("failed to list SSH keys: %v", err)
	}
	if len(list) == 0 {
		t.Errorf("expected at least 1 key, got 0")
	}

	// IsKeyInUse (should be false)
	inUse, err := keyStore.IsKeyInUse(ctx, created.ID)
	if err != nil {
		t.Fatalf("failed to check key usage: %v", err)
	}
	if inUse {
		t.Errorf("expected key not to be in use, but it is")
	}

	// Delete
	err = keyStore.Delete(ctx, created.ID)
	if err != nil {
		t.Fatalf("failed to delete SSH key: %v", err)
	}

	_, err = keyStore.Get(ctx, created.ID)
	if err == nil {
		t.Errorf("expected error when getting deleted key, got nil")
	}
}

func TestSSHKeyIsInUse(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	repos := db.Repositories()
	keyStore := db.SSHKeys()

	// Create SSH key
	key, err := keyStore.Create(ctx, store.SSHKey{
		Name:        "test-key-in-use",
		KeyType:     "ed25519",
		PrivatePath: "keys/test",
		PublicPath:  "keys/test.pub",
		Fingerprint: "xyz789",
	})
	if err != nil {
		t.Fatalf("failed to create SSH key: %v", err)
	}

	// Create repository using the key
	_, err = repos.Create(ctx, store.Repository{
		Name:            "repo-using-key",
		PullURLRaw:      "git@example.com:user/repo",
		PullURLSSH:      "ssh://git@example.com/user/repo.git",
		PullSSHKeyID:    key.ID,
		PushURLRaw:      "git@example.com:user/mirror",
		PushURLSSH:      "ssh://git@example.com/user/mirror.git",
		PushSSHKeyID:    key.ID,
		WebhookSecHash:  "hash",
		ProviderHint:    "github",
		Enabled:         true,
	})
	if err != nil {
		t.Fatalf("failed to create repository: %v", err)
	}

	// Now check if key is in use
	inUse, err := keyStore.IsKeyInUse(ctx, key.ID)
	if err != nil {
		t.Fatalf("failed to check key usage: %v", err)
	}
	if !inUse {
		t.Errorf("expected key to be in use, but it's not")
	}
}

func TestSyncEventStore(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	eventStore := db.SyncEvents()

	// Create events
	now := time.Now()
	event := store.SyncEvent{
		ReceivedAt:   now,
		FinishedAt:   now.Add(1 * time.Second),
		Provider:     "github",
		RepoFullName: "user/repo",
		CloneURL:     "https://github.com/user/repo.git",
		SSHURL:       "ssh://git@github.com/user/repo.git",
		Ref:          "main",
		CommitBefore: "abc123",
		CommitAfter:  "def456",
		Status:       "success",
		Detail:       "sync completed",
		DurationMS:   1000,
	}

	created, err := eventStore.Create(ctx, event)
	if err != nil {
		t.Fatalf("failed to create sync event: %v", err)
	}

	if created.ID == 0 {
		t.Errorf("expected non-zero ID, got %d", created.ID)
	}

	// List
	list, err := eventStore.List(ctx, 10)
	if err != nil {
		t.Fatalf("failed to list sync events: %v", err)
	}

	if len(list) == 0 {
		t.Errorf("expected at least 1 event, got 0")
	}

	if list[0].Status != "success" {
		t.Errorf("expected status 'success', got %q", list[0].Status)
	}

	// Prune (should delete events older than now)
	future := now.Add(10 * time.Second)
	deleted, err := eventStore.PruneOlderThan(ctx, future)
	if err != nil {
		t.Fatalf("failed to prune events: %v", err)
	}

	if deleted != 1 {
		t.Errorf("expected 1 deleted event, got %d", deleted)
	}

	// Verify it's gone
	list, err = eventStore.List(ctx, 10)
	if err != nil {
		t.Fatalf("failed to list events after prune: %v", err)
	}

	if len(list) != 0 {
		t.Errorf("expected 0 events after prune, got %d", len(list))
	}
}

func TestRepositoryListByPullURL(t *testing.T) {
	db := setupTestDB(t)
	defer db.Close()

	ctx := context.Background()
	repos := db.Repositories()
	keyStore := db.SSHKeys()

	// Create SSH key
	key, err := keyStore.Create(ctx, store.SSHKey{
		Name:        "key1",
		KeyType:     "ed25519",
		PrivatePath: "k1",
		PublicPath:  "k1.pub",
		Fingerprint: "fp1",
	})
	if err != nil {
		t.Fatalf("failed to create key: %v", err)
	}

	pullURL := "ssh://git@github.com/owner/source.git"

	// Create multiple repositories with the same pull URL
	for i := 0; i < 2; i++ {
		_, err := repos.Create(ctx, store.Repository{
			Name:           "repo" + string(rune(i)),
			PullURLRaw:     "git@github.com:owner/source",
			PullURLSSH:     pullURL,
			PullSSHKeyID:   key.ID,
			PushURLRaw:     "git@github.com:owner/mirror" + string(rune(i)),
			PushURLSSH:     "ssh://git@github.com/owner/mirror" + string(rune(i)) + ".git",
			PushSSHKeyID:   key.ID,
			WebhookSecHash: "hash",
			ProviderHint:   "github",
			Enabled:        true,
		})
		if err != nil {
			t.Fatalf("failed to create repository: %v", err)
		}
	}

	// List by pull URL
	list, err := repos.ListByPullURL(ctx, pullURL)
	if err != nil {
		t.Fatalf("failed to list by pull URL: %v", err)
	}

	if len(list) != 2 {
		t.Errorf("expected 2 repositories, got %d", len(list))
	}

	for _, r := range list {
		if r.PullURLSSH != pullURL {
			t.Errorf("expected pull URL %q, got %q", pullURL, r.PullURLSSH)
		}
	}
}

// ===== Test Helpers =====

func setupTestDB(t *testing.T) *store.DB {
	tmpfile, err := os.CreateTemp("", "forgejo-sync-test-*.db")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	tmpfile.Close()

	db, err := store.NewDB(tmpfile.Name())
	if err != nil {
		os.Remove(tmpfile.Name())
		t.Fatalf("failed to create test DB: %v", err)
	}

	t.Cleanup(func() {
		db.Close()
		os.Remove(tmpfile.Name())
	})

	return db
}
