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
	slog.Debug("worker.Pool.Enqueue requested",
		slog.Int64("eventID", eventID),
		slog.String("repo", repo.FullName),
		slog.String("provider", repo.Provider),
		slog.Int("queue_len", len(p.jobCh)),
		slog.Int("queue_cap", cap(p.jobCh)),
	)
	select {
	case <-p.ctx.Done():
		slog.Debug("worker.Pool.Enqueue rejected: shutting down", slog.Int64("eventID", eventID))
		return errors.New("worker pool is shutting down")
	case p.jobCh <- syncJob{repo: repo, eventID: eventID}:
		slog.Debug("worker.Pool.Enqueue succeeded",
			slog.Int64("eventID", eventID),
			slog.Int("queue_len", len(p.jobCh)),
		)
		return nil
	default:
		slog.Debug("worker.Pool.Enqueue rejected: queue full",
			slog.Int64("eventID", eventID),
			slog.Int("queue_len", len(p.jobCh)),
		)
		return errors.New("worker pool job channel is full")
	}
}

// Start launches the worker goroutines and the pruning task.
func (p *Pool) Start() {
	concurrency := p.cfg.WorkerConcurrency
	if concurrency <= 0 {
		concurrency = 4
	}

	slog.Debug("worker.Pool.Start initiating workers",
		slog.Int("concurrency", concurrency),
		slog.Int("queue_cap", cap(p.jobCh)),
	)

	for i := 0; i < concurrency; i++ {
		p.wg.Add(1)
		go p.worker(i)
	}

	p.wg.Add(1)
	go p.pruner()
	slog.Debug("worker.Pool.Start all workers and pruner started")
}

// Shutdown initiates a graceful shutdown and waits for workers to finish.
func (p *Pool) Shutdown() {
	slog.Debug("worker.Pool.Shutdown initiated")
	p.cancel()
	p.wg.Wait()
	slog.Debug("worker.Pool.Shutdown completed")
}

func (p *Pool) worker(id int) {
	defer p.wg.Done()
	slog.Debug("worker routine started", slog.Int("worker_id", id))
	for {
		select {
		case <-p.ctx.Done():
			slog.Debug("worker routine stopping", slog.Int("worker_id", id))
			return
		case job := <-p.jobCh:
			slog.Debug("worker picked up job", slog.Int("worker_id", id), slog.Int64("eventID", job.eventID), slog.String("repo", job.repo.FullName))
			p.processJob(job)
		}
	}
}

func (p *Pool) pruner() {
	defer p.wg.Done()
	ticker := time.NewTicker(24 * time.Hour)
	defer ticker.Stop()

	slog.Debug("pruner routine started")
	for {
		select {
		case <-p.ctx.Done():
			slog.Debug("pruner routine stopping")
			return
		case <-ticker.C:
			slog.Debug("pruner tick firing: pruning history older than 30 days")
			pruned, err := p.store.Prune(p.ctx, 30*24*time.Hour)
			if err != nil {
				slog.Error("failed to prune history", slog.Any("error", err))
			} else {
				slog.Debug("pruned expired history records", slog.Int64("pruned_count", pruned))
			}
		}
	}
}

func (p *Pool) processJob(job syncJob) {
	status := history.StatusError
	detail := ""
	commitAfter := ""

	slog.Info("starting processing sync job",
		slog.Int64("eventID", job.eventID),
		slog.String("repo", job.repo.Name),
		slog.String("owner", job.repo.Owner),
		slog.String("ref", job.repo.Ref),
		slog.String("before", job.repo.Before),
		slog.String("after", job.repo.After),
		slog.String("clone_url", job.repo.CloneURL),
		slog.String("ssh_url", job.repo.SSHURL),
	)

	timeout := p.cfg.GitCommandTimeout
	if timeout <= 0 {
		timeout = 10 * time.Minute
	}
	jobCtx, jobCancel := context.WithTimeout(context.Background(), timeout+30*time.Second)
	defer jobCancel()

	start := time.Now()
	defer func() {
		err := p.store.Finish(context.Background(), job.eventID, status, detail, commitAfter, time.Now().UTC())
		if err != nil {
			slog.Error("failed to finish history row", slog.Int64("eventID", job.eventID), slog.Any("error", err))
		}
		slog.Info("finished processing sync job",
			slog.Int64("eventID", job.eventID),
			slog.Any("status", status),
			slog.String("detail", detail),
			slog.String("commit_after", commitAfter),
			slog.Duration("duration", time.Since(start)),
		)
	}()

	normalizedURL := job.repo.CloneURL
	if normalizedURL == "" {
		normalizedURL = job.repo.SSHURL
	}
	if normalizedURL != "" {
		var err error
		rawURL := normalizedURL
		normalizedURL, err = giturl.NormalizeToSSH(normalizedURL, "git")
		if err != nil {
			slog.Debug("failed to normalize webhook URL, skipping repo lookup",
				slog.Int64("eventID", job.eventID),
				slog.String("raw_url", rawURL),
				slog.Any("error", err),
			)
			status = history.StatusSkipped
			detail = "No matching repository found"
			return
		}
		slog.Debug("normalized webhook URL for lookup",
			slog.Int64("eventID", job.eventID),
			slog.String("raw_url", rawURL),
			slog.String("normalized_url", normalizedURL),
		)
	}

	slog.Debug("looking up matching repositories in database", slog.Int64("eventID", job.eventID), slog.String("normalizedURL", normalizedURL))
	matches, err := p.repoLookup.ListByPullURL(jobCtx, normalizedURL)
	if err != nil {
		slog.Debug("database lookup failed with error", slog.Int64("eventID", job.eventID), slog.Any("error", err))
		status = history.StatusError
		detail = fmt.Sprintf("Lookup failed: %v", err)
		return
	}
	slog.Debug("database lookup returned candidates", slog.Int64("eventID", job.eventID), slog.Int("match_count", len(matches)))
	if len(matches) == 0 {
		slog.Debug("no matching repository found in database", slog.Int64("eventID", job.eventID), slog.String("url", normalizedURL))
		status = history.StatusSkipped
		detail = "No matching repository found"
		return
	}

	var matchedRepo *store.Repository
	for i := range matches {
		repo := &matches[i]
		slog.Debug("evaluating candidate repository",
			slog.Int64("eventID", job.eventID),
			slog.Int64("repoID", repo.ID),
			slog.String("repoName", repo.Name),
			slog.Bool("enabled", repo.Enabled),
		)
		if !repo.Enabled {
			slog.Debug("skipping disabled candidate repository", slog.Int64("repoID", repo.ID))
			continue
		}
		if _, err := p.keyStore.Get(jobCtx, repo.PullSSHKeyID); err != nil {
			slog.Warn("pull SSH key missing for repo", slog.Int64("eventID", job.eventID), slog.Int64("repoID", repo.ID), slog.Any("error", err))
			continue
		}
		if _, err := p.keyStore.Get(jobCtx, repo.PushSSHKeyID); err != nil {
			slog.Warn("push SSH key missing for repo", slog.Int64("eventID", job.eventID), slog.Int64("repoID", repo.ID), slog.Any("error", err))
			continue
		}
		matchedRepo = repo
		break
	}
	if matchedRepo == nil {
		slog.Debug("no enabled runnable repository matched", slog.Int64("eventID", job.eventID))
		status = history.StatusSkipped
		detail = "No runnable repository match found"
		return
	}

	slog.Debug("selected matching repository for sync",
		slog.Int64("eventID", job.eventID),
		slog.Int64("repoID", matchedRepo.ID),
		slog.String("repoName", matchedRepo.Name),
		slog.String("pull_url_ssh", matchedRepo.PullURLSSH),
		slog.String("push_url_ssh", matchedRepo.PushURLSSH),
	)

	slog.Debug("acquiring repository execution lock", slog.Int64("repoID", matchedRepo.ID))
	mu := p.getRepoLock(matchedRepo.ID)
	mu.Lock()
	slog.Debug("repository execution lock acquired", slog.Int64("repoID", matchedRepo.ID))
	defer func() {
		mu.Unlock()
		slog.Debug("repository execution lock released", slog.Int64("repoID", matchedRepo.ID))
	}()

	pullKey, err := p.keyStore.Get(jobCtx, matchedRepo.PullSSHKeyID)
	if err != nil {
		slog.Debug("pull key lookup failed", slog.Int64("repoID", matchedRepo.ID), slog.Any("error", err))
		status = history.StatusError
		detail = fmt.Sprintf("Pull key lookup failed: %v", err)
		return
	}
	pushKey, err := p.keyStore.Get(jobCtx, matchedRepo.PushSSHKeyID)
	if err != nil {
		slog.Debug("push key lookup failed", slog.Int64("repoID", matchedRepo.ID), slog.Any("error", err))
		status = history.StatusError
		detail = fmt.Sprintf("Push key lookup failed: %v", err)
		return
	}

	slog.Debug("resolved repository keys",
		slog.Int64("repoID", matchedRepo.ID),
		slog.String("pull_key_file", pullKey.PrivatePath),
		slog.String("push_key_file", pushKey.PrivatePath),
	)

	syncOpts := gitsync.SyncOptions{
		CacheDir:      p.cfg.CacheDir,
		SourceURL:     matchedRepo.PullURLSSH,
		SourceKeyPath: pullKey.PrivatePath,
		Timeout:       p.cfg.GitCommandTimeout,
	}
	slog.Debug("executing SyncRepository in worker", slog.Int64("eventID", job.eventID), slog.String("source_url", syncOpts.SourceURL))
	cachePath, err := p.gitSyncer.SyncRepository(jobCtx, syncOpts)
	if err != nil {
		status = history.StatusError
		detail = fmt.Sprintf("Sync failed: %v", err)
		slog.Error("worker sync repository step failed", slog.Int64("eventID", job.eventID), slog.String("detail", detail))
		return
	}
	slog.Debug("worker sync repository step succeeded", slog.Int64("eventID", job.eventID), slog.String("cache_path", cachePath))

	pushOpts := gitsync.PushOptions{
		CachePath:     cachePath,
		RemoteURL:     matchedRepo.PushURLSSH,
		RemoteKeyPath: pushKey.PrivatePath,
		Timeout:       p.cfg.GitCommandTimeout,
	}
	slog.Debug("executing PushToRemote in worker", slog.Int64("eventID", job.eventID), slog.String("remote_url", pushOpts.RemoteURL))
	pushResult, err := p.gitSyncer.PushToRemote(jobCtx, pushOpts)
	if err != nil {
		var conflictErr *gitsync.ErrConflict
		if errors.As(err, &conflictErr) {
			status = history.StatusConflict
			detail = conflictErr.Detail
			slog.Warn("worker push detected conflict", slog.Int64("eventID", job.eventID), slog.String("detail", detail))
		} else {
			status = history.StatusError
			detail = fmt.Sprintf("Push failed: %v", err)
			slog.Error("worker push failed", slog.Int64("eventID", job.eventID), slog.String("detail", detail))
		}
		return
	}

	slog.Debug("worker push succeeded",
		slog.Int64("eventID", job.eventID),
		slog.String("before_sha", pushResult.BeforeSHA),
		slog.String("after_sha", pushResult.AfterSHA),
	)

	status = history.StatusSuccess
	detail = pushResult.RawOutput
	if pushResult.AfterSHA != "" {
		commitAfter = pushResult.AfterSHA
	} else if job.repo.After != "" {
		commitAfter = job.repo.After
	}
}
