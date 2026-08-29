package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"forgejo-sync/internal/config"
	"forgejo-sync/internal/giturl"
	"forgejo-sync/internal/gitsync"
	"forgejo-sync/internal/history"
	"forgejo-sync/internal/store"
	"forgejo-sync/internal/webhook"
)

// RepoLookup defines the dependency for looking up repositories by normalized pull URL.
type RepoLookup interface {
	ListByPullURL(ctx context.Context, normalizedPullURL string) ([]store.Repository, error)
}

// SSHKeyStore resolves SSH private keys by id.
type SSHKeyStore interface {
	Get(ctx context.Context, id int64) (store.SSHKey, error)
}

// HistoryStore defines the dependency for writing sync outcomes and pruning.
type HistoryStore interface {
	Finish(ctx context.Context, id int64, status history.Status, detail string, commitAfter string, finishedAt time.Time) error
	Prune(ctx context.Context, olderThan time.Duration) (int64, error)
}

// GitSyncer abstracts gitsync operations for testing.
type GitSyncer interface {
	SyncRepository(ctx context.Context, opts gitsync.SyncOptions) (string, error)
	PushToRemote(ctx context.Context, opts gitsync.PushOptions) (*gitsync.PushResult, error)
}

type defaultGitSyncer struct{}

func (s defaultGitSyncer) SyncRepository(ctx context.Context, opts gitsync.SyncOptions) (string, error) {
	return gitsync.SyncRepository(ctx, opts)
}

func (s defaultGitSyncer) PushToRemote(ctx context.Context, opts gitsync.PushOptions) (*gitsync.PushResult, error) {
	return gitsync.PushToRemote(ctx, opts)
}

type syncJob struct {
	repo    webhook.RepoInfo
	eventID int64
}

// Pool represents the bounded worker pool for processing sync jobs.
type Pool struct {
	cfg         *config.Config
	repoLookup  RepoLookup
	keyStore    SSHKeyStore
	store       HistoryStore
	gitSyncer   GitSyncer
	jobCh       chan syncJob
	repoLocks   map[int64]*sync.Mutex
	repoLocksMu sync.Mutex
	wg          sync.WaitGroup
	ctx         context.Context
	cancel      context.CancelFunc
}

// New creates a new worker pool.
func New(cfg *config.Config, repoLookup RepoLookup, keyStore SSHKeyStore, store HistoryStore) *Pool {
	ctx, cancel := context.WithCancel(context.Background())
	return &Pool{
		cfg:         cfg,
		repoLookup:  repoLookup,
		keyStore:    keyStore,
		store:       store,
		gitSyncer:   defaultGitSyncer{},
		jobCh:       make(chan syncJob, 1000), // Buffered channel for enqueueing
		repoLocks:   make(map[int64]*sync.Mutex),
		repoLocksMu: sync.Mutex{},
		ctx:         ctx,
		cancel:      cancel,
	}
}

// getRepoLock returns the mutex for a specific Forgejo repository ID, creating it if needed.
func (p *Pool) getRepoLock(repoID int64) *sync.Mutex {
	p.repoLocksMu.Lock()
	defer p.repoLocksMu.Unlock()
	mu, ok := p.repoLocks[repoID]
	if !ok {
		mu = &sync.Mutex{}
		p.repoLocks[repoID] = mu
	}
	return mu
}

// Enqueue implements webhook.JobEnqueuer.
func (p *Pool) Enqueue(ctx context.Context, repo webhook.RepoInfo, eventID int64) error {
	select {
	case <-p.ctx.Done():
		return errors.New("worker pool is shutting down")
	case p.jobCh <- syncJob{repo: repo, eventID: eventID}:
		return nil
	default:
		return errors.New("worker pool job channel is full")
	}
}

// Start launches the worker goroutines and the pruning task.
func (p *Pool) Start() {
	concurrency := p.cfg.WorkerConcurrency
	if concurrency <= 0 {
		concurrency = 4
	}

	for i := 0; i < concurrency; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}

	p.wg.Add(1)
	go p.pruner()
}

// Shutdown initiates a graceful shutdown and waits for workers to finish.
func (p *Pool) Shutdown() {
	p.cancel()
	p.wg.Wait()
}

func (p *Pool) worker(id int) {
	defer p.wg.Done()
	for {
		select {
		case <-p.ctx.Done():
			return
		case job := <-p.jobCh:
			p.processJob(job)
		}
	}
}

func (p *Pool) pruner() {
	defer p.wg.Done()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	for {
		select {
		case <-p.ctx.Done():
			return
		case <-ticker.C:
			// Prune history older than 30 days
			_, err := p.store.Prune(p.ctx, 30*24*time.Hour)
			if err != nil {
				slog.Error("failed to prune history", slog.Any("error", err))
			}
		}
	}
}

func (p *Pool) processJob(job syncJob) {
	status := history.StatusError
	detail := ""
	commitAfter := ""

	slog.Info("starting processing sync job", slog.Int64("eventID", job.eventID), slog.String("repo", job.repo.Name), slog.String("owner", job.repo.Owner))

	defer func() {
		err := p.store.Finish(context.Background(), job.eventID, status, detail, commitAfter, time.Now().UTC())
		if err != nil {
			slog.Error("failed to finish history row", slog.Int64("eventID", job.eventID), slog.Any("error", err))
		}
		slog.Info("finished processing sync job", slog.Int64("eventID", job.eventID), slog.Any("status", status))
	}()

	normalizedURL := job.repo.CloneURL
	if normalizedURL == "" {
		normalizedURL = job.repo.SSHURL
	}
	if normalizedURL != "" {
		var err error
		normalizedURL, err = giturl.NormalizeToSSH(normalizedURL, "git")
		if err != nil {
			slog.Debug("failed to normalize webhook URL, skipping repo lookup", slog.Int64("eventID", job.eventID), slog.String("url", normalizedURL), slog.Any("error", err))
			status = history.StatusSkipped
			detail = "No matching repository found"
			return
		}
	}

	slog.Debug("looking up matching repositories in database", slog.Int64("eventID", job.eventID), slog.String("normalizedURL", normalizedURL))
	matches, err := p.repoLookup.ListByPullURL(p.ctx, normalizedURL)
	if err != nil {
		slog.Debug("database lookup failed with error", slog.Int64("eventID", job.eventID), slog.Any("error", err))
		status = history.StatusError
		detail = fmt.Sprintf("Lookup failed: %v", err)
		return
	}
	if len(matches) == 0 {
		slog.Debug("no matching repository found in database", slog.Int64("eventID", job.eventID), slog.String("url", normalizedURL))
		status = history.StatusSkipped
		detail = "No matching repository found"
		return
	}

	var matchedRepo *store.Repository
	for i := range matches {
		repo := &matches[i]
		if !repo.Enabled {
			continue
		}
		if _, err := p.keyStore.Get(p.ctx, repo.PullSSHKeyID); err != nil {
			slog.Warn("pull SSH key missing for repo", slog.Int64("eventID", job.eventID), slog.Int64("repoID", repo.ID), slog.Any("error", err))
			continue
		}
		if _, err := p.keyStore.Get(p.ctx, repo.PushSSHKeyID); err != nil {
			slog.Warn("push SSH key missing for repo", slog.Int64("eventID", job.eventID), slog.Int64("repoID", repo.ID), slog.Any("error", err))
			continue
		}
		matchedRepo = repo
		break
	}
	if matchedRepo == nil {
		status = history.StatusSkipped
		detail = "No runnable repository match found"
		return
	}

	slog.Debug("found matching repository in database", slog.Int64("eventID", job.eventID), slog.Int64("repoID", matchedRepo.ID), slog.String("repoName", matchedRepo.Name))
	mu := p.getRepoLock(matchedRepo.ID)
	mu.Lock()
	defer mu.Unlock()

	pullKey, err := p.keyStore.Get(p.ctx, matchedRepo.PullSSHKeyID)
	if err != nil {
		status = history.StatusError
		detail = fmt.Sprintf("Pull key lookup failed: %v", err)
		return
	}
	pushKey, err := p.keyStore.Get(p.ctx, matchedRepo.PushSSHKeyID)
	if err != nil {
		status = history.StatusError
		detail = fmt.Sprintf("Push key lookup failed: %v", err)
		return
	}

	syncOpts := gitsync.SyncOptions{
		CacheDir:      p.cfg.CacheDir,
		SourceURL:     matchedRepo.PullURLSSH,
		SourceKeyPath: pullKey.PrivatePath,
		Timeout:       p.cfg.GitCommandTimeout,
	}
	cachePath, err := p.gitSyncer.SyncRepository(p.ctx, syncOpts)
	if err != nil {
		status = history.StatusError
		detail = fmt.Sprintf("Sync failed: %v", err)
		slog.Error(detail)
		return
	}

	pushOpts := gitsync.PushOptions{
		CachePath:     cachePath,
		RemoteURL:     matchedRepo.PushURLSSH,
		RemoteKeyPath: pushKey.PrivatePath,
		Timeout:       p.cfg.GitCommandTimeout,
	}
	pushResult, err := p.gitSyncer.PushToRemote(p.ctx, pushOpts)
	if err != nil {
		var conflictErr *gitsync.ErrConflict
		if errors.As(err, &conflictErr) {
			status = history.StatusConflict
			detail = conflictErr.Detail
		} else {
			status = history.StatusError
			detail = fmt.Sprintf("Push failed: %v", err)
		}
		slog.Error(detail)
		return
	}

	status = history.StatusSuccess
	detail = pushResult.RawOutput
	if pushResult.AfterSHA != "" {
		commitAfter = pushResult.AfterSHA
	} else if job.repo.After != "" {
		commitAfter = job.repo.After
	}
}
