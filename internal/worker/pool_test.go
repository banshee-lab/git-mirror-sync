package worker

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"forgejo-sync/internal/config"
	"forgejo-sync/internal/forgejodb"
	"forgejo-sync/internal/gitsync"
	"forgejo-sync/internal/history"
	"forgejo-sync/internal/webhook"
)

type fakeDB struct {
	lookupFunc func(cloneURL string) (*forgejodb.MatchedRepo, error)
}

func (f *fakeDB) LookupRepository(ctx context.Context, cloneURL, sshURL string) (*forgejodb.MatchedRepo, error) {
	if f.lookupFunc != nil {
		return f.lookupFunc(cloneURL)
	}
	return &forgejodb.MatchedRepo{RepoID: 1}, nil
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

func (f *fakeSyncer) PushToForgejo(ctx context.Context, opts gitsync.PushOptions) (*gitsync.PushResult, error) {
	if f.pushFunc != nil {
		return f.pushFunc()
	}
	return &gitsync.PushResult{AfterSHA: "abc"}, nil
}

func TestPoolConcurrencySameRepo(t *testing.T) {
	cfg := &config.Config{WorkerConcurrency: 2}
	db := &fakeDB{}
	store := newFakeHistory()
	
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
	
	pool := New(cfg, db, store)
	pool.gitSyncer = syncer
	pool.Start()
	
	pool.Enqueue(context.Background(), webhook.RepoInfo{CloneURL: "a"}, 1)
	pool.Enqueue(context.Background(), webhook.RepoInfo{CloneURL: "a"}, 2)
	
	// Wait a bit for jobs to process
	time.Sleep(300 * time.Millisecond)
	pool.Shutdown()
	
	if atomic.LoadInt32(&maxInFlight) > 1 {
		t.Errorf("expected max 1 in-flight for same repo, got %d", maxInFlight)
	}
	
	store.mu.Lock()
	defer store.mu.Unlock()
	if store.finishedRows[1] != history.StatusSuccess || store.finishedRows[2] != history.StatusSuccess {
		t.Errorf("expected both jobs to succeed, got %v", store.finishedRows)
	}
}

func TestPoolConcurrencyDifferentRepos(t *testing.T) {
	cfg := &config.Config{WorkerConcurrency: 2}
	db := &fakeDB{
		lookupFunc: func(cloneURL string) (*forgejodb.MatchedRepo, error) {
			if cloneURL == "a" {
				return &forgejodb.MatchedRepo{RepoID: 1}, nil
			}
			return &forgejodb.MatchedRepo{RepoID: 2}, nil
		},
	}
	store := newFakeHistory()
	
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
	
	pool := New(cfg, db, store)
	pool.gitSyncer = syncer
	pool.Start()
	
	pool.Enqueue(context.Background(), webhook.RepoInfo{CloneURL: "a"}, 1)
	pool.Enqueue(context.Background(), webhook.RepoInfo{CloneURL: "b"}, 2)
	
	time.Sleep(300 * time.Millisecond)
	pool.Shutdown()
	
	if atomic.LoadInt32(&maxInFlight) != 2 {
		t.Errorf("expected max 2 in-flight for different repos, got %d", maxInFlight)
	}
}

func TestPoolJobPaths(t *testing.T) {
	cfg := &config.Config{WorkerConcurrency: 2}
	store := newFakeHistory()
	
	db := &fakeDB{
		lookupFunc: func(cloneURL string) (*forgejodb.MatchedRepo, error) {
			if cloneURL == "skipped" {
				return nil, forgejodb.ErrNoMatch
			}
			return &forgejodb.MatchedRepo{RepoID: 1}, nil
		},
	}
	
	syncer := &fakeSyncer{
		syncFunc: func() (string, error) {
			return "/cache", nil
		},
		pushFunc: func() (*gitsync.PushResult, error) {
			return nil, &gitsync.ErrConflict{Detail: "boom"}
		},
	}
	
	pool := New(cfg, db, store)
	pool.gitSyncer = syncer
	pool.Start()
	
	pool.Enqueue(context.Background(), webhook.RepoInfo{CloneURL: "skipped"}, 1)
	pool.Enqueue(context.Background(), webhook.RepoInfo{CloneURL: "conflict"}, 2)
	
	time.Sleep(200 * time.Millisecond)
	pool.Shutdown()
	
	store.mu.Lock()
	defer store.mu.Unlock()
	
	if store.finishedRows[1] != history.StatusSkipped {
		t.Errorf("expected skipped, got %v", store.finishedRows[1])
	}
	if store.finishedRows[2] != history.StatusConflict {
		t.Errorf("expected conflict, got %v", store.finishedRows[2])
	}
}
