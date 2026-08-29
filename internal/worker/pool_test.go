package worker

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"forgejo-sync/internal/config"
	"forgejo-sync/internal/gitsync"
	"forgejo-sync/internal/history"
	"forgejo-sync/internal/store"
	"forgejo-sync/internal/webhook"
)

type fakeRepoLookup struct {
	lookupFunc func(normalizedPullURL string) ([]store.Repository, error)
}

func (f *fakeRepoLookup) ListByPullURL(ctx context.Context, normalizedPullURL string) ([]store.Repository, error) {
	if f.lookupFunc != nil {
		return f.lookupFunc(normalizedPullURL)
	}
	return []store.Repository{{ID: 1, Enabled: true, PullURLSSH: normalizedPullURL, PushURLSSH: normalizedPullURL, PullSSHKeyID: 1, PushSSHKeyID: 2}}, nil
}

type fakeSSHKeyStore struct {
	keys map[int64]store.SSHKey
}

func (f *fakeSSHKeyStore) Get(ctx context.Context, id int64) (store.SSHKey, error) {
	if key, ok := f.keys[id]; ok {
		return key, nil
	}
	return store.SSHKey{}, context.DeadlineExceeded
}

type fakeHistory struct {
	mu           sync.Mutex
	finishedRows map[int64]history.Status
}

func newFakeHistory() *fakeHistory {
	return &fakeHistory{finishedRows: make(map[int64]history.Status)}
}

func (f *fakeHistory) Finish(ctx context.Context, id int64, status history.Status, detail string, commitAfter string, finishedAt time.Time) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.finishedRows[id] = status
	return nil
}
func (f *fakeHistory) Prune(ctx context.Context, olderThan time.Duration) (int64, error) { return 0, nil }

type fakeSyncer struct {
	syncFunc func() (string, error)
	pushFunc func() (*gitsync.PushResult, error)
}

func (f *fakeSyncer) SyncRepository(ctx context.Context, opts gitsync.SyncOptions) (string, error) {
	if f.syncFunc != nil {
		return f.syncFunc()
	}
	return "/cache/path", nil
}

func (f *fakeSyncer) PushToRemote(ctx context.Context, opts gitsync.PushOptions) (*gitsync.PushResult, error) {
	if f.pushFunc != nil {
		return f.pushFunc()
	}
	return &gitsync.PushResult{AfterSHA: "abc"}, nil
}

func TestPoolConcurrencySameRepo(t *testing.T) {
	cfg := &config.Config{WorkerConcurrency: 2, CacheDir: "/tmp", GitCommandTimeout: time.Minute}
	repoLookup := &fakeRepoLookup{lookupFunc: func(normalizedPullURL string) ([]store.Repository, error) {
		return []store.Repository{{ID: 1, Enabled: true, PullURLSSH: normalizedPullURL, PushURLSSH: normalizedPullURL, PullSSHKeyID: 1, PushSSHKeyID: 2}}, nil
	}}
	keyStore := &fakeSSHKeyStore{keys: map[int64]store.SSHKey{1: {PrivatePath: "/tmp/pull-key"}, 2: {PrivatePath: "/tmp/push-key"}}}
	histStore := newFakeHistory()

	var inFlight int32
	var maxInFlight int32

	syncer := &fakeSyncer{
		syncFunc: func() (string, error) {
			current := atomic.AddInt32(&inFlight, 1)
			if current > maxInFlight {
				atomic.StoreInt32(&maxInFlight, current)
			}
			time.Sleep(100 * time.Millisecond)
			atomic.AddInt32(&inFlight, -1)
			return "/cache", nil
		},
	}

	pool := New(cfg, repoLookup, keyStore, histStore)
	pool.gitSyncer = syncer
	pool.Start()

	pool.Enqueue(context.Background(), webhook.RepoInfo{CloneURL: "https://example.com/org/repo.git"}, 1)
	pool.Enqueue(context.Background(), webhook.RepoInfo{CloneURL: "https://example.com/org/repo.git"}, 2)

	time.Sleep(300 * time.Millisecond)
	pool.Shutdown()

	if atomic.LoadInt32(&maxInFlight) > 1 {
		t.Errorf("expected max 1 in-flight for same repo, got %d", maxInFlight)
	}

	histStore.mu.Lock()
	defer histStore.mu.Unlock()
	if histStore.finishedRows[1] != history.StatusSuccess || histStore.finishedRows[2] != history.StatusSuccess {
		t.Errorf("expected both jobs to succeed, got %v", histStore.finishedRows)
	}
}

func TestPoolConcurrencyDifferentRepos(t *testing.T) {
	cfg := &config.Config{WorkerConcurrency: 2, CacheDir: "/tmp", GitCommandTimeout: time.Minute}
	repoLookup := &fakeRepoLookup{lookupFunc: func(normalizedPullURL string) ([]store.Repository, error) {
		if normalizedPullURL == "ssh://git@example.com/org/a.git" {
			return []store.Repository{{ID: 1, Enabled: true, PullURLSSH: normalizedPullURL, PushURLSSH: normalizedPullURL, PullSSHKeyID: 1, PushSSHKeyID: 2}}, nil
		}
		return []store.Repository{{ID: 2, Enabled: true, PullURLSSH: normalizedPullURL, PushURLSSH: normalizedPullURL, PullSSHKeyID: 1, PushSSHKeyID: 2}}, nil
	}}
	keyStore := &fakeSSHKeyStore{keys: map[int64]store.SSHKey{1: {PrivatePath: "/tmp/pull-key"}, 2: {PrivatePath: "/tmp/push-key"}}}
	histStore := newFakeHistory()

	var inFlight int32
	var maxInFlight int32

	syncer := &fakeSyncer{
		syncFunc: func() (string, error) {
			current := atomic.AddInt32(&inFlight, 1)
			for {
				old := atomic.LoadInt32(&maxInFlight)
				if current <= old || atomic.CompareAndSwapInt32(&maxInFlight, old, current) {
					break
				}
			}
			time.Sleep(200 * time.Millisecond)
			atomic.AddInt32(&inFlight, -1)
			return "/cache", nil
		},
	}

	pool := New(cfg, repoLookup, keyStore, histStore)
	pool.gitSyncer = syncer
	pool.Start()

	pool.Enqueue(context.Background(), webhook.RepoInfo{CloneURL: "https://example.com/org/a.git"}, 1)
	pool.Enqueue(context.Background(), webhook.RepoInfo{CloneURL: "https://example.com/org/b.git"}, 2)

	time.Sleep(300 * time.Millisecond)
	pool.Shutdown()

	if atomic.LoadInt32(&maxInFlight) != 2 {
		t.Errorf("expected max 2 in-flight for different repos, got %d", maxInFlight)
	}
}

func TestPoolJobPaths(t *testing.T) {
	cfg := &config.Config{WorkerConcurrency: 2, CacheDir: "/tmp", GitCommandTimeout: time.Minute}
	histStore := newFakeHistory()

	repoLookup := &fakeRepoLookup{lookupFunc: func(normalizedPullURL string) ([]store.Repository, error) {
		if normalizedPullURL == "ssh://git@example.com/skip.git" {
			return nil, nil
		}
		return []store.Repository{{ID: 1, Enabled: true, PullURLSSH: normalizedPullURL, PushURLSSH: normalizedPullURL, PullSSHKeyID: 1, PushSSHKeyID: 2}}, nil
	}}
	keyStore := &fakeSSHKeyStore{keys: map[int64]store.SSHKey{1: {PrivatePath: "/tmp/pull-key"}, 2: {PrivatePath: "/tmp/push-key"}}}

	syncer := &fakeSyncer{
		syncFunc: func() (string, error) { return "/cache", nil },
		pushFunc: func() (*gitsync.PushResult, error) { return nil, &gitsync.ErrConflict{Detail: "boom"} },
	}

	pool := New(cfg, repoLookup, keyStore, histStore)
	pool.gitSyncer = syncer
	pool.Start()

	pool.Enqueue(context.Background(), webhook.RepoInfo{CloneURL: "https://example.com/skip.git"}, 1)
	pool.Enqueue(context.Background(), webhook.RepoInfo{CloneURL: "https://example.com/conflict.git"}, 2)

	time.Sleep(200 * time.Millisecond)
	pool.Shutdown()

	histStore.mu.Lock()
	defer histStore.mu.Unlock()

	if histStore.finishedRows[1] != history.StatusSkipped {
		t.Errorf("expected skipped, got %v", histStore.finishedRows[1])
	}
	if histStore.finishedRows[2] != history.StatusConflict {
		t.Errorf("expected conflict, got %v", histStore.finishedRows[2])
	}
}
