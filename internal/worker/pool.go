package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"forgejo-sync/internal/config"
	"forgejo-sync/internal/forgejodb"
	"forgejo-sync/internal/gitsync"
	"forgejo-sync/internal/history"
	"forgejo-sync/internal/webhook"
)

// ForgejoDB defines the dependency for looking up Forgejo repositories.
type ForgejoDB interface {
  LookupRepository(ctx context.Context, cloneURL, sshURL string) (*forgejodb.MatchedRepo, error)
}

// HistoryStore defines the dependency for writing sync outcomes and pruning.
type HistoryStore interface {
  Finish(ctx context.Context, id int64, status history.Status, detail string, commitAfter string, finishedAt time.Time) error
  Prune(ctx context.Context, olderThan time.Duration) (int64, error)
}

// GitSyncer abstracts gitsync operations for testing.
type GitSyncer interface {
  SyncRepository(ctx context.Context, opts gitsync.SyncOptions) (string, error)
  PushToForgejo(ctx context.Context, opts gitsync.PushOptions) (*gitsync.PushResult, error)
}

type defaultGitSyncer struct{}

func (s defaultGitSyncer) SyncRepository(ctx context.Context, opts gitsync.SyncOptions) (string, error) {
  return gitsync.SyncRepository(ctx, opts)
}

func (s defaultGitSyncer) PushToForgejo(ctx context.Context, opts gitsync.PushOptions) (*gitsync.PushResult, error) {
  return gitsync.PushToForgejo(ctx, opts)
}

type syncJob struct {
  repo    webhook.RepoInfo
  eventID int64
}

// Pool represents the bounded worker pool for processing sync jobs.
type Pool struct {
  cfg         *config.Config
  db          ForgejoDB
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
func New(cfg *config.Config, db ForgejoDB, store HistoryStore) *Pool {
  ctx, cancel := context.WithCancel(context.Background())
  return &Pool{
    cfg:         cfg,
    db:          db,
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

  // Ensure we always update history when returning
  defer func() {
    err := p.store.Finish(context.Background(), job.eventID, status, detail, commitAfter, time.Now().UTC())
    if err != nil {
      slog.Error("failed to finish history row", slog.Int64("eventID", job.eventID), slog.Any("error", err))
    }
    slog.Info("finished processing sync job", slog.Int64("eventID", job.eventID), slog.Any("status", status))
  }()

  // 1. forgejodb lookup
  slog.Debug("looking up repository in database", slog.Int64("eventID", job.eventID), slog.String("cloneURL", job.repo.CloneURL), slog.String("sshURL", job.repo.SSHURL))
  matchedRepo, err := p.db.LookupRepository(p.ctx, job.repo.CloneURL, job.repo.SSHURL)
  if err != nil {
    if errors.Is(err, forgejodb.ErrNoMatch) {
      slog.Debug("no matching repository found in database", slog.Int64("eventID", job.eventID), slog.String("cloneURL", job.repo.CloneURL), slog.String("sshURL", job.repo.SSHURL))
      status = history.StatusSkipped
      detail = "No matching push mirror found"
    } else {
      slog.Debug("database lookup failed with error", slog.Int64("eventID", job.eventID), slog.Any("error", err))
      status = history.StatusError
      detail = fmt.Sprintf("Lookup failed: %v", err)
    }
    return
  }

  slog.Debug("found matching repository in database", slog.Int64("eventID", job.eventID), slog.Int64("repoID", matchedRepo.RepoID), slog.String("repoName", matchedRepo.RepoName), slog.String("ownerName", matchedRepo.OwnerName))

  // Lock the specific repository to prevent concurrent syncs for the same repo
  mu := p.getRepoLock(matchedRepo.RepoID)
  mu.Lock()
  defer mu.Unlock()

  // 2. credential resolution + clone/fetch
  syncOpts := gitsync.SyncOptions{
    CacheDir:       p.cfg.CacheDir,
    Provider:       job.repo.Provider,
    Owner:          job.repo.Owner,
    Repo:           job.repo.Name,
    SourceURL:      job.repo.CloneURL,
    Timeout:        p.cfg.GitCommandTimeout,
    ForgejoDataDir: p.cfg.ForgejoDataDir,
    MatchedRepo:    matchedRepo,
  }

  // Use SSHURL if CloneURL is empty
  if syncOpts.SourceURL == "" {
    syncOpts.SourceURL = job.repo.SSHURL
  }

  cachePath, err := p.gitSyncer.SyncRepository(p.ctx, syncOpts)
  if err != nil {
    status = history.StatusError
    detail = fmt.Sprintf("Sync failed: %v", err)
    return
  }

  // 3. push to Forgejo
  pushOpts := gitsync.PushOptions{
    CachePath:             cachePath,
    Owner:                 matchedRepo.OwnerName,
    Repo:                  matchedRepo.RepoName,
    ForgejoDomain:         p.cfg.ForgejoDomain,
    ForgejoSSHPort:        p.cfg.ForgejoSSHPort,
    ForgejoPushSSHKeyPath: p.cfg.ForgejoPushSSHKeyPath,
    Timeout:               p.cfg.GitCommandTimeout,
  }

  pushResult, err := p.gitSyncer.PushToForgejo(p.ctx, pushOpts)
  if err != nil {
    var conflictErr *gitsync.ErrConflict
    if errors.As(err, &conflictErr) {
      status = history.StatusConflict
      detail = conflictErr.Detail
    } else {
      status = history.StatusError
      detail = fmt.Sprintf("Push failed: %v", err)
    }
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
