package reposvc

import (
	"context"
	"fmt"
	"testing"
	"time"

	"forgejo-sync/internal/gitsync"
	"forgejo-sync/internal/store"
)

type fakeRepoStore struct {
	repos map[int64]store.Repository
	byName map[string]store.Repository
	nextID int64
}

func newFakeRepoStore() *fakeRepoStore {
	return &fakeRepoStore{repos: map[int64]store.Repository{}, byName: map[string]store.Repository{}}
}

func (f *fakeRepoStore) Create(ctx context.Context, repo store.Repository) (store.Repository, error) {
	f.nextID++
	repo.ID = f.nextID
	f.repos[repo.ID] = repo
	f.byName[repo.Name] = repo
	return repo, nil
}

func (f *fakeRepoStore) Update(ctx context.Context, repo store.Repository) (store.Repository, error) {
	f.repos[repo.ID] = repo
	f.byName[repo.Name] = repo
	return repo, nil
}

func (f *fakeRepoStore) Delete(ctx context.Context, id int64) error {
	delete(f.repos, id)
	for name, repo := range f.byName {
		if repo.ID == id {
			delete(f.byName, name)
			break
		}
	}
	return nil
}

func (f *fakeRepoStore) Get(ctx context.Context, id int64) (store.Repository, error) {
	repo, ok := f.repos[id]
	if !ok {
		return store.Repository{}, fmt.Errorf("not found")
	}
	return repo, nil
}

func (f *fakeRepoStore) List(ctx context.Context) ([]store.Repository, error) {
	out := make([]store.Repository, 0, len(f.repos))
	for _, repo := range f.repos {
		out = append(out, repo)
	}
	return out, nil
}

func (f *fakeRepoStore) ListPage(ctx context.Context, search string, limit, offset int) ([]store.Repository, int, error) {
	var filtered []store.Repository
	for _, repo := range f.repos {
		if search == "" || repo.Name == search {
			filtered = append(filtered, repo)
		}
	}
	total := len(filtered)
	if offset >= len(filtered) {
		return []store.Repository{}, total, nil
	}
	end := len(filtered)
	if limit > 0 && offset+limit < end {
		end = offset + limit
	}
	return filtered[offset:end], total, nil
}

func (f *fakeRepoStore) GetByName(ctx context.Context, name string) (store.Repository, error) {
	repo, ok := f.byName[name]
	if !ok {
		return store.Repository{}, fmt.Errorf("not found")
	}
	return repo, nil
}

func (f *fakeRepoStore) ListByPullURL(ctx context.Context, normalizedPullURL string) ([]store.Repository, error) {
	var out []store.Repository
	for _, repo := range f.repos {
		if repo.PullURLSSH == normalizedPullURL {
			out = append(out, repo)
		}
	}
	return out, nil
}

type fakeKeyStore struct {
	keys map[int64]store.SSHKey
	nextID int64
}

func newFakeKeyStore() *fakeKeyStore {
	return &fakeKeyStore{keys: map[int64]store.SSHKey{}}
}

func (f *fakeKeyStore) Create(ctx context.Context, key store.SSHKey) (store.SSHKey, error) {
	f.nextID++
	key.ID = f.nextID
	f.keys[key.ID] = key
	return key, nil
}

func (f *fakeKeyStore) Delete(ctx context.Context, id int64) error {
	delete(f.keys, id)
	return nil
}

func (f *fakeKeyStore) Get(ctx context.Context, id int64) (store.SSHKey, error) {
	key, ok := f.keys[id]
	if !ok {
		return store.SSHKey{}, fmt.Errorf("not found")
	}
	return key, nil
}

func (f *fakeKeyStore) List(ctx context.Context) ([]store.SSHKey, error) {
	out := make([]store.SSHKey, 0, len(f.keys))
	for _, key := range f.keys {
		out = append(out, key)
	}
	return out, nil
}

func (f *fakeKeyStore) GetByName(ctx context.Context, name string) (store.SSHKey, error) {
	for _, key := range f.keys {
		if key.Name == name {
			return key, nil
		}
	}
	return store.SSHKey{}, fmt.Errorf("not found")
}

func (f *fakeKeyStore) IsKeyInUse(ctx context.Context, id int64) (bool, error) {
	return false, nil
}

type fakeEventStore struct {
	events []store.SyncEvent
}

func (f *fakeEventStore) Create(ctx context.Context, event store.SyncEvent) (store.SyncEvent, error) {
	event.ID = int64(len(f.events) + 1)
	f.events = append(f.events, event)
	return event, nil
}

type fakeGit struct {
	syncCalled bool
	pushCalled bool
	lastSync   gitsync.SyncOptions
	lastPush   gitsync.PushOptions
}

func (f *fakeGit) SyncRepository(ctx context.Context, opts gitsync.SyncOptions) (string, error) {
	f.syncCalled = true
	f.lastSync = opts
	return opts.CacheDir, nil
}

func (f *fakeGit) PushToRemote(ctx context.Context, opts gitsync.PushOptions) (*gitsync.PushResult, error) {
	f.pushCalled = true
	f.lastPush = opts
	return &gitsync.PushResult{RawOutput: "ok"}, nil
}

func (f *fakeGit) LsRemote(ctx context.Context, remoteURL string, keyPath string, timeout time.Duration) error {
	return nil
}

func TestServiceCreateNormalizesURLsAndValidatesKeys(t *testing.T) {
	ctx := context.Background()
	repoStore := newFakeRepoStore()
	keyStore := newFakeKeyStore()
	key1, _ := keyStore.Create(ctx, store.SSHKey{Name: "pull-key", PrivatePath: "pull.pem", PublicPath: "pull.pub"})
	key2, _ := keyStore.Create(ctx, store.SSHKey{Name: "push-key", PrivatePath: "push.pem", PublicPath: "push.pub"})
	eventStore := &fakeEventStore{}
	git := &fakeGit{}
	service := NewService(repoStore, keyStore, eventStore, git, "/tmp/keys", "/tmp/cache", nil)

	repo, err := service.Create(ctx, CreateRequest{
		Name:          "demo-repo",
		PullURL:       "https://github.com/octocat/Hello-World.git",
		PullSSHKeyID:  key1.ID,
		PushURL:       "git@gitlab.com:owner/project.git",
		PushSSHKeyID:  key2.ID,
		WebhookSecret: "super-secret",
	})
	if err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}
	if repo.PullURLSSH != "ssh://git@github.com/octocat/Hello-World.git" {
		t.Fatalf("unexpected pull URL ssh: %s", repo.PullURLSSH)
	}
	if repo.PushURLSSH != "ssh://git@gitlab.com/owner/project.git" {
		t.Fatalf("unexpected push URL ssh: %s", repo.PushURLSSH)
	}
	if repo.WebhookSecHash == "super-secret" {
		t.Fatal("webhook secret was stored in plaintext")
	}
}

func TestServiceCreateRejectsInvalidURL(t *testing.T) {
	ctx := context.Background()
	repoStore := newFakeRepoStore()
	keyStore := newFakeKeyStore()
	key1, _ := keyStore.Create(ctx, store.SSHKey{Name: "pull-key", PrivatePath: "pull.pem", PublicPath: "pull.pub"})
	key2, _ := keyStore.Create(ctx, store.SSHKey{Name: "push-key", PrivatePath: "push.pem", PublicPath: "push.pub"})
	service := NewService(repoStore, keyStore, &fakeEventStore{}, &fakeGit{}, "/tmp/keys", "/tmp/cache", nil)

	_, err := service.Create(ctx, CreateRequest{
		Name:          "bad-repo",
		PullURL:       "ftp://example.com/foo/bar",
		PullSSHKeyID:  key1.ID,
		PushURL:       "git@github.com:owner/repo.git",
		PushSSHKeyID:  key2.ID,
		WebhookSecret: "secret",
	})
	if err == nil {
		t.Fatal("expected invalid URL error")
	}
}

func TestServiceSyncNowRecordsEvent(t *testing.T) {
	ctx := context.Background()
	repoStore := newFakeRepoStore()
	keyStore := newFakeKeyStore()
	pullKey, _ := keyStore.Create(ctx, store.SSHKey{Name: "pull", PrivatePath: "pull.pem", PublicPath: "pull.pub"})
	pushKey, _ := keyStore.Create(ctx, store.SSHKey{Name: "push", PrivatePath: "push.pem", PublicPath: "push.pub"})
	git := &fakeGit{}
	events := &fakeEventStore{}
	service := NewService(repoStore, keyStore, events, git, "/tmp/keys", "/tmp/cache", nil)

	repo, _ := repoStore.Create(ctx, store.Repository{
		Name:           "sync-me",
		PullURLRaw:     "git@github.com:owner/repo",
		PullURLSSH:     "ssh://git@github.com/owner/repo.git",
		PullSSHKeyID:   pullKey.ID,
		PushURLRaw:     "git@github.com:owner/target",
		PushURLSSH:     "ssh://git@github.com/owner/target.git",
		PushSSHKeyID:   pushKey.ID,
		WebhookSecHash: "hash",
		ProviderHint:   "github",
	})

	eventID, err := service.SyncNow(ctx, repo.ID)
	if err != nil {
		t.Fatalf("SyncNow() returned error: %v", err)
	}
	if eventID == 0 {
		t.Fatal("expected event ID > 0")
	}
	if !git.syncCalled || !git.pushCalled {
		t.Fatal("expected both git sync operations to run")
	}
	if len(events.events) != 1 {
		t.Fatalf("expected 1 event, got %d", len(events.events))
	}
	if events.events[0].Status != "success" {
		t.Fatalf("unexpected event status: %s", events.events[0].Status)
	}
}

func TestServiceUpdateKeepsHashWhenSecretUnchanged(t *testing.T) {
	ctx := context.Background()
	repoStore := newFakeRepoStore()
	keyStore := newFakeKeyStore()
	pullKey, _ := keyStore.Create(ctx, store.SSHKey{Name: "pull", PrivatePath: "pull.pem", PublicPath: "pull.pub"})
	pushKey, _ := keyStore.Create(ctx, store.SSHKey{Name: "push", PrivatePath: "push.pem", PublicPath: "push.pub"})
	service := NewService(repoStore, keyStore, &fakeEventStore{}, &fakeGit{}, "/tmp/keys", "/tmp/cache", nil)

	repo, err := service.Create(ctx, CreateRequest{
		Name:          "update-me",
		PullURL:       "git@github.com:owner/repo",
		PullSSHKeyID:  pullKey.ID,
		PushURL:       "git@github.com:owner/dest",
		PushSSHKeyID:  pushKey.ID,
		WebhookSecret: "initial-secret",
	})
	if err != nil {
		t.Fatalf("Create() returned error: %v", err)
	}
	if repo.WebhookSecHash == "initial-secret" {
		t.Fatal("secret was stored in plaintext")
	}

	updated, err := service.Update(ctx, repo.ID, UpdateRequest{Name: "update-me", PullURL: "git@github.com:owner/repo", PullSSHKeyID: pullKey.ID, PushURL: "git@github.com:owner/dest", PushSSHKeyID: pushKey.ID})
	if err != nil {
		t.Fatalf("Update() returned error: %v", err)
	}
	if updated.WebhookSecHash != repo.WebhookSecHash {
		t.Fatalf("expected secret hash to stay stable, got %s != %s", updated.WebhookSecHash, repo.WebhookSecHash)
	}
	if updated.PullURLSSH != "ssh://git@github.com/owner/repo.git" {
		t.Fatalf("unexpected pull SSH url after update: %s", updated.PullURLSSH)
	}
	if updated.PushURLSSH != "ssh://git@github.com/owner/dest.git" {
		t.Fatalf("unexpected push SSH url after update: %s", updated.PushURLSSH)
	}
}

func TestServiceCreateWithSyncNow(t *testing.T) {
	ctx := context.Background()
	repoStore := newFakeRepoStore()
	keyStore := newFakeKeyStore()
	pullKey, _ := keyStore.Create(ctx, store.SSHKey{Name: "pull", PrivatePath: "pull.pem", PublicPath: "pull.pub"})
	pushKey, _ := keyStore.Create(ctx, store.SSHKey{Name: "push", PrivatePath: "push.pem", PublicPath: "push.pub"})
	git := &fakeGit{}
	service := NewService(repoStore, keyStore, &fakeEventStore{}, git, "/tmp/keys", "/tmp/cache", nil)

	_, err := service.Create(ctx, CreateRequest{
		Name:          "sync-on-create",
		PullURL:       "git@github.com:owner/repo",
		PullSSHKeyID:  pullKey.ID,
		PushURL:       "git@github.com:owner/dest",
		PushSSHKeyID:  pushKey.ID,
		WebhookSecret: "abc",
		SyncNow:       true,
	})
	if err != nil {
		t.Fatalf("Create() with SyncNow returned error: %v", err)
	}
	if !git.syncCalled || !git.pushCalled {
		t.Fatal("expected sync operations to run when SyncNow=true")
	}
}

func TestServiceCreateRejectsDuplicateName(t *testing.T) {
	ctx := context.Background()
	repoStore := newFakeRepoStore()
	keyStore := newFakeKeyStore()
	pullKey, _ := keyStore.Create(ctx, store.SSHKey{Name: "pull", PrivatePath: "pull.pem", PublicPath: "pull.pub"})
	pushKey, _ := keyStore.Create(ctx, store.SSHKey{Name: "push", PrivatePath: "push.pem", PublicPath: "push.pub"})
	service := NewService(repoStore, keyStore, &fakeEventStore{}, &fakeGit{}, "/tmp/keys", "/tmp/cache", nil)

	_, err := service.Create(ctx, CreateRequest{Name: "repo", PullURL: "git@github.com:owner/repo", PullSSHKeyID: pullKey.ID, PushURL: "git@github.com:owner/dest", PushSSHKeyID: pushKey.ID, WebhookSecret: "first"})
	if err != nil {
		t.Fatalf("first create failed: %v", err)
	}
	_, err = service.Create(ctx, CreateRequest{Name: "repo", PullURL: "git@github.com:owner/other", PullSSHKeyID: pullKey.ID, PushURL: "git@github.com:owner/dest2", PushSSHKeyID: pushKey.ID, WebhookSecret: "second"})
	if err == nil {
		t.Fatal("expected duplicate name error")
	}
}

func TestServiceListPagination(t *testing.T) {
	ctx := context.Background()
	repoStore := newFakeRepoStore()
	keyStore := newFakeKeyStore()
	service := NewService(repoStore, keyStore, &fakeEventStore{}, &fakeGit{}, "/tmp/keys", "/tmp/cache", nil)

	for i := 1; i <= 5; i++ {
		_, _ = repoStore.Create(ctx, store.Repository{
			Name: fmt.Sprintf("repo-%d", i),
		})
	}

	result, err := service.List(ctx, ListRequest{Page: 1, PageSize: 2})
	if err != nil {
		t.Fatalf("List() error: %v", err)
	}
	if result.Total != 5 {
		t.Errorf("expected total 5, got %d", result.Total)
	}
	if len(result.Items) != 2 {
		t.Errorf("expected 2 items, got %d", len(result.Items))
	}
	if result.Page != 1 || result.PageSize != 2 {
		t.Errorf("expected page 1 size 2, got page %d size %d", result.Page, result.PageSize)
	}
}

func TestServicePushProviderHint(t *testing.T) {
	ctx := context.Background()
	repoStore := newFakeRepoStore()
	keyStore := newFakeKeyStore()
	key1, _ := keyStore.Create(ctx, store.SSHKey{Name: "pull-key", PrivatePath: "pull.pem", PublicPath: "pull.pub"})
	key2, _ := keyStore.Create(ctx, store.SSHKey{Name: "push-key", PrivatePath: "push.pem", PublicPath: "push.pub"})
	service := NewService(repoStore, keyStore, &fakeEventStore{}, &fakeGit{}, "/tmp/keys", "/tmp/cache", nil)

	repo, err := service.Create(ctx, CreateRequest{
		Name:          "provider-test",
		PullURL:       "git@github.com:owner/repo.git",
		PullSSHKeyID:  key1.ID,
		PushURL:       "ssh://git@forgejo.example.com:2222/owner/repo.git",
		PushSSHKeyID:  key2.ID,
		WebhookSecret: "secret",
	})
	if err != nil {
		t.Fatalf("Create() error: %v", err)
	}
	if repo.ProviderHint != "github" {
		t.Errorf("expected github provider hint, got %s", repo.ProviderHint)
	}
	if repo.PushProviderHint != "forgejo" {
		t.Errorf("expected forgejo push provider hint, got %s", repo.PushProviderHint)
	}
}
