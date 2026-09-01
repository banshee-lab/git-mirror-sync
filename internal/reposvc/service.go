package reposvc

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"forgejo-sync/internal/events"
	"forgejo-sync/internal/giturl"
	"forgejo-sync/internal/gitsync"
	"forgejo-sync/internal/history"
	"forgejo-sync/internal/store"
)

// GitSyncer abstracts the git mirror operations used by the repository service.
type GitSyncer interface {
	SyncRepository(ctx context.Context, opts gitsync.SyncOptions) (string, error)
	PushToRemote(ctx context.Context, opts gitsync.PushOptions) (*gitsync.PushResult, error)
	LsRemote(ctx context.Context, remoteURL string, keyPath string, timeout time.Duration) error
}

// EventRecorder stores sync history entries.
type EventRecorder interface {
	Create(ctx context.Context, event store.SyncEvent) (store.SyncEvent, error)
	Finish(ctx context.Context, id int64, status history.Status, detail string, commitAfter string, finishedAt time.Time) error
}

// Service orchestrates repository CRUD and sync execution based on local SQLite-backed stores.
type Service struct {
	repos       store.RepositoryStore
	keys        store.SSHKeyStore
	events      EventRecorder
	git         GitSyncer
	keysDir     string
	cacheDir    string
	hub         *events.Hub
	repoLocks   map[int64]*sync.Mutex
	repoLocksMu sync.Mutex
	logger      *slog.Logger
}

// NewService creates a repository service with the provided dependencies.
func NewService(repos store.RepositoryStore, keys store.SSHKeyStore, events EventRecorder, git GitSyncer, keysDir, cacheDir string, hub *events.Hub, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{
		repos:     repos,
		keys:      keys,
		events:    events,
		git:       git,
		keysDir:   keysDir,
		cacheDir:  cacheDir,
		hub:       hub,
		repoLocks: make(map[int64]*sync.Mutex),
		logger:    logger,
	}
}

// ListRequest is the input parameters for listing repositories.
type ListRequest struct {
	Search   string `json:"search"`
	Page     int    `json:"page"`
	PageSize int    `json:"page_size"`
}

// ListResult represents paginated repository results.
type ListResult struct {
	Items    []store.Repository `json:"items"`
	Total    int                `json:"total"`
	Page     int                `json:"page"`
	PageSize int                `json:"page_size"`
}

// CreateRequest is the input DTO for repository creation.
type CreateRequest struct {
	Name          string `json:"name"`
	PullURL       string `json:"pull_url"`
	PullSSHKeyID  int64  `json:"pull_ssh_key_id"`
	PushURL       string `json:"push_url"`
	PushSSHKeyID  int64  `json:"push_ssh_key_id"`
	WebhookSecret string `json:"webhook_secret"`
	SyncNow       bool   `json:"sync_now"`
}

// UpdateRequest is the input DTO for repository updates.
type UpdateRequest struct {
	Name          string `json:"name"`
	PullURL       string `json:"pull_url"`
	PullSSHKeyID  int64  `json:"pull_ssh_key_id"`
	PushURL       string `json:"push_url"`
	PushSSHKeyID  int64  `json:"push_ssh_key_id"`
	WebhookSecret string `json:"webhook_secret"`
	SyncNow       bool   `json:"sync_now"`
}

// Create creates a repository entry, normalizes both URLs, validates keys, and optionally syncs immediately.
func (s *Service) Create(ctx context.Context, req CreateRequest) (store.Repository, error) {
	s.logger.Debug("reposvc.Create started",
		slog.String("name", req.Name),
		slog.String("pull_url", req.PullURL),
		slog.Int64("pull_ssh_key_id", req.PullSSHKeyID),
		slog.String("push_url", req.PushURL),
		slog.Int64("push_ssh_key_id", req.PushSSHKeyID),
		slog.Bool("has_webhook_secret", req.WebhookSecret != ""),
		slog.Bool("sync_now", req.SyncNow),
	)

	if req.Name == "" {
		s.logger.Debug("reposvc.Create validation failed: missing name")
		return store.Repository{}, errors.New("repository name is required")
	}
	if req.PullURL == "" {
		s.logger.Debug("reposvc.Create validation failed: missing pull URL")
		return store.Repository{}, errors.New("pull URL is required")
	}
	if req.PushURL == "" {
		s.logger.Debug("reposvc.Create validation failed: missing push URL")
		return store.Repository{}, errors.New("push URL is required")
	}
	if req.WebhookSecret == "" {
		s.logger.Debug("reposvc.Create validation failed: missing webhook secret")
		return store.Repository{}, errors.New("webhook secret is required")
	}
	if req.PullSSHKeyID == 0 {
		s.logger.Debug("reposvc.Create validation failed: missing pull SSH key id")
		return store.Repository{}, errors.New("pull SSH key id is required")
	}
	if req.PushSSHKeyID == 0 {
		s.logger.Debug("reposvc.Create validation failed: missing push SSH key id")
		return store.Repository{}, errors.New("push SSH key id is required")
	}

	if _, err := s.keys.Get(ctx, req.PullSSHKeyID); err != nil {
		s.logger.Debug("reposvc.Create failed: pull SSH key lookup error", slog.Int64("pull_ssh_key_id", req.PullSSHKeyID), slog.Any("error", err))
		return store.Repository{}, fmt.Errorf("pull SSH key not found: %w", err)
	}
	if _, err := s.keys.Get(ctx, req.PushSSHKeyID); err != nil {
		s.logger.Debug("reposvc.Create failed: push SSH key lookup error", slog.Int64("push_ssh_key_id", req.PushSSHKeyID), slog.Any("error", err))
		return store.Repository{}, fmt.Errorf("push SSH key not found: %w", err)
	}

	pullSSH, err := giturl.NormalizeToSSH(req.PullURL, "git")
	if err != nil {
		s.logger.Debug("reposvc.Create failed: normalize pull URL error", slog.String("pull_url", req.PullURL), slog.Any("error", err))
		return store.Repository{}, fmt.Errorf("invalid pull URL: %w", err)
	}
	pushSSH, err := giturl.NormalizeToSSH(req.PushURL, "git")
	if err != nil {
		s.logger.Debug("reposvc.Create failed: normalize push URL error", slog.String("push_url", req.PushURL), slog.Any("error", err))
		return store.Repository{}, fmt.Errorf("invalid push URL: %w", err)
	}

	if _, err := s.repos.GetByName(ctx, req.Name); err == nil {
		s.logger.Debug("reposvc.Create failed: duplicate repository name", slog.String("name", req.Name))
		return store.Repository{}, fmt.Errorf("repository %q already exists", req.Name)
	}

	encryptedSecret, err := encryptSecret(req.WebhookSecret)
	if err != nil {
		s.logger.Debug("reposvc.Create failed: encrypt webhook secret", slog.Any("error", err))
		return store.Repository{}, fmt.Errorf("failed to encrypt webhook secret: %w", err)
	}

	repo := store.Repository{
		Name:             req.Name,
		PullURLRaw:       req.PullURL,
		PullURLSSH:       pullSSH,
		PullSSHKeyID:     req.PullSSHKeyID,
		PushURLRaw:       req.PushURL,
		PushURLSSH:       pushSSH,
		PushSSHKeyID:     req.PushSSHKeyID,
		WebhookSecHash:   encryptedSecret,
		ProviderHint:     inferProvider(req.PullURL),
		PushProviderHint: inferProvider(req.PushURL),
		Enabled:          true,
	}

	created, err := s.repos.Create(ctx, repo)
	if err != nil {
		s.logger.Debug("reposvc.Create database insert failed", slog.Any("error", err))
		return store.Repository{}, fmt.Errorf("failed to create repository: %w", err)
	}

	s.logger.Debug("reposvc.Create created repository record",
		slog.Int64("id", created.ID),
		slog.String("name", created.Name),
		slog.String("pull_url_ssh", created.PullURLSSH),
		slog.String("push_url_ssh", created.PushURLSSH),
	)

	if req.SyncNow {
		s.logger.Debug("reposvc.Create triggering sync now", slog.Int64("repo_id", created.ID))
		if _, err := s.SyncNow(ctx, created.ID); err != nil {
			s.logger.Debug("reposvc.Create sync now failed", slog.Int64("repo_id", created.ID), slog.Any("error", err))
			return created, err
		}
	}

	s.logger.Debug("reposvc.Create finished successfully", slog.Int64("id", created.ID), slog.String("name", created.Name))
	return created, nil
}

// Get fetches a repository by ID.
func (s *Service) Get(ctx context.Context, id int64) (store.Repository, error) {
	s.logger.Debug("reposvc.Get started", slog.Int64("id", id))
	repo, err := s.repos.Get(ctx, id)
	if err != nil {
		s.logger.Debug("reposvc.Get failed", slog.Int64("id", id), slog.Any("error", err))
		return store.Repository{}, err
	}
	s.logger.Debug("reposvc.Get succeeded", slog.Int64("id", repo.ID), slog.String("name", repo.Name))
	return repo, nil
}

// List lists repositories with pagination and optional search filter.
func (s *Service) List(ctx context.Context, req ListRequest) (ListResult, error) {
	s.logger.Debug("reposvc.List started",
		slog.String("search", req.Search),
		slog.Int("page", req.Page),
		slog.Int("page_size", req.PageSize),
	)
	if req.Page < 1 {
		req.Page = 1
	}
	if req.PageSize < 1 {
		req.PageSize = 20
	} else if req.PageSize > 100 {
		req.PageSize = 100
	}
	offset := (req.Page - 1) * req.PageSize
	items, total, err := s.repos.ListPage(ctx, req.Search, req.PageSize, offset)
	if err != nil {
		s.logger.Debug("reposvc.List failed", slog.Any("error", err))
		return ListResult{}, err
	}
	res := ListResult{
		Items:    items,
		Total:    total,
		Page:     req.Page,
		PageSize: req.PageSize,
	}
	s.logger.Debug("reposvc.List succeeded",
		slog.Int("returned_count", len(items)),
		slog.Int("total", total),
		slog.Int("page", req.Page),
		slog.Int("page_size", req.PageSize),
	)
	return res, nil
}

// Delete removes a repository by ID.
func (s *Service) Delete(ctx context.Context, id int64) error {
	s.logger.Debug("reposvc.Delete started", slog.Int64("id", id))
	if err := s.repos.Delete(ctx, id); err != nil {
		s.logger.Debug("reposvc.Delete failed", slog.Int64("id", id), slog.Any("error", err))
		return err
	}
	s.logger.Debug("reposvc.Delete succeeded", slog.Int64("id", id))
	return nil
}

// Update updates an existing repository.
func (s *Service) Update(ctx context.Context, id int64, req UpdateRequest) (store.Repository, error) {
	s.logger.Debug("reposvc.Update started",
		slog.Int64("id", id),
		slog.String("name", req.Name),
		slog.String("pull_url", req.PullURL),
		slog.Int64("pull_ssh_key_id", req.PullSSHKeyID),
		slog.String("push_url", req.PushURL),
		slog.Int64("push_ssh_key_id", req.PushSSHKeyID),
		slog.Bool("has_webhook_secret", req.WebhookSecret != ""),
		slog.Bool("sync_now", req.SyncNow),
	)

	existing, err := s.repos.Get(ctx, id)
	if err != nil {
		s.logger.Debug("reposvc.Update failed to get existing repository", slog.Int64("id", id), slog.Any("error", err))
		return store.Repository{}, err
	}

	if req.Name == "" {
		req.Name = existing.Name
	}
	if req.PullURL == "" {
		req.PullURL = existing.PullURLRaw
	}
	if req.PushURL == "" {
		req.PushURL = existing.PushURLRaw
	}
	if req.PullSSHKeyID == 0 {
		req.PullSSHKeyID = existing.PullSSHKeyID
	}
	if req.PushSSHKeyID == 0 {
		req.PushSSHKeyID = existing.PushSSHKeyID
	}
	if _, err := s.keys.Get(ctx, req.PullSSHKeyID); err != nil {
		s.logger.Debug("reposvc.Update failed: pull SSH key not found", slog.Int64("pull_ssh_key_id", req.PullSSHKeyID), slog.Any("error", err))
		return store.Repository{}, fmt.Errorf("pull SSH key not found: %w", err)
	}
	if _, err := s.keys.Get(ctx, req.PushSSHKeyID); err != nil {
		s.logger.Debug("reposvc.Update failed: push SSH key not found", slog.Int64("push_ssh_key_id", req.PushSSHKeyID), slog.Any("error", err))
		return store.Repository{}, fmt.Errorf("push SSH key not found: %w", err)
	}

	pullSSH, err := giturl.NormalizeToSSH(req.PullURL, "git")
	if err != nil {
		s.logger.Debug("reposvc.Update failed: invalid pull URL", slog.String("pull_url", req.PullURL), slog.Any("error", err))
		return store.Repository{}, fmt.Errorf("invalid pull URL: %w", err)
	}
	pushSSH, err := giturl.NormalizeToSSH(req.PushURL, "git")
	if err != nil {
		s.logger.Debug("reposvc.Update failed: invalid push URL", slog.String("push_url", req.PushURL), slog.Any("error", err))
		return store.Repository{}, fmt.Errorf("invalid push URL: %w", err)
	}

	if req.Name != existing.Name {
		if _, err := s.repos.GetByName(ctx, req.Name); err == nil {
			s.logger.Debug("reposvc.Update failed: name collision", slog.String("name", req.Name))
			return store.Repository{}, fmt.Errorf("repository %q already exists", req.Name)
		}
	}

	existing.Name = req.Name
	existing.PullURLRaw = req.PullURL
	existing.PullURLSSH = pullSSH
	existing.PullSSHKeyID = req.PullSSHKeyID
	existing.PushURLRaw = req.PushURL
	existing.PushURLSSH = pushSSH
	existing.PushSSHKeyID = req.PushSSHKeyID
	if req.WebhookSecret != "" {
		encryptedSecret, err := encryptSecret(req.WebhookSecret)
		if err != nil {
			s.logger.Debug("reposvc.Update failed: encrypt webhook secret", slog.Any("error", err))
			return store.Repository{}, fmt.Errorf("failed to encrypt webhook secret: %w", err)
		}
		existing.WebhookSecHash = encryptedSecret
	}
	existing.ProviderHint = inferProvider(req.PullURL)
	existing.PushProviderHint = inferProvider(req.PushURL)

	updated, err := s.repos.Update(ctx, existing)
	if err != nil {
		s.logger.Debug("reposvc.Update database update failed", slog.Int64("id", id), slog.Any("error", err))
		return store.Repository{}, fmt.Errorf("failed to update repository: %w", err)
	}

	s.logger.Debug("reposvc.Update updated repository record",
		slog.Int64("id", updated.ID),
		slog.String("name", updated.Name),
		slog.String("pull_url_ssh", updated.PullURLSSH),
		slog.String("push_url_ssh", updated.PushURLSSH),
	)

	if req.SyncNow {
		s.logger.Debug("reposvc.Update triggering sync now", slog.Int64("repo_id", updated.ID))
		if _, err := s.SyncNow(ctx, updated.ID); err != nil {
			s.logger.Debug("reposvc.Update sync now failed", slog.Int64("repo_id", updated.ID), slog.Any("error", err))
			return updated, err
		}
	}

	s.logger.Debug("reposvc.Update finished successfully", slog.Int64("id", updated.ID), slog.String("name", updated.Name))
	return updated, nil
}

func (s *Service) getRepoLock(repoID int64) *sync.Mutex {
	s.repoLocksMu.Lock()
	defer s.repoLocksMu.Unlock()
	mu, ok := s.repoLocks[repoID]
	if !ok {
		mu = &sync.Mutex{}
		s.repoLocks[repoID] = mu
	}
	return mu
}

// SyncNowAsync creates an initial "running" sync event and executes the sync asynchronously in the background.
func (s *Service) SyncNowAsync(ctx context.Context, repoID int64) (int64, error) {
	s.logger.Debug("reposvc.SyncNowAsync started", slog.Int64("repo_id", repoID))

	repo, err := s.repos.Get(ctx, repoID)
	if err != nil {
		s.logger.Debug("reposvc.SyncNowAsync failed to find repo", slog.Int64("repo_id", repoID), slog.Any("error", err))
		return 0, err
	}

	if _, err := s.keys.Get(ctx, repo.PullSSHKeyID); err != nil {
		s.logger.Debug("reposvc.SyncNowAsync pull key not found", slog.Int64("pull_ssh_key_id", repo.PullSSHKeyID), slog.Any("error", err))
		return 0, fmt.Errorf("pull key not found: %w", err)
	}
	if _, err := s.keys.Get(ctx, repo.PushSSHKeyID); err != nil {
		s.logger.Debug("reposvc.SyncNowAsync push key not found", slog.Int64("push_ssh_key_id", repo.PushSSHKeyID), slog.Any("error", err))
		return 0, fmt.Errorf("push key not found: %w", err)
	}

	now := time.Now().UTC()
	initialEvent := store.SyncEvent{
		ReceivedAt:   now,
		FinishedAt:   now,
		Provider:     repo.ProviderHint,
		RepoFullName: repo.Name,
		CloneURL:     repo.PullURLRaw,
		SSHURL:       repo.PullURLSSH,
		Ref:          "refs/heads/*",
		Status:       string(history.StatusRunning),
		Detail:       "sync in progress",
	}

	created, err := s.events.Create(ctx, initialEvent)
	if err != nil {
		s.logger.Debug("reposvc.SyncNowAsync failed to record initial sync event", slog.Int64("repo_id", repoID), slog.Any("error", err))
		return 0, fmt.Errorf("failed to record initial sync event: %w", err)
	}

	if s.hub != nil {
		s.hub.Publish(events.SyncEventPayload{
			Type:      events.TypeSyncStarted,
			EventID:   created.ID,
			RepoID:    repo.ID,
			RepoName:  repo.Name,
			Status:    string(history.StatusRunning),
			Detail:    "sync in progress",
			Timestamp: now,
		})
	}

	go s.executeSync(created.ID, repo)

	s.logger.Debug("reposvc.SyncNowAsync enqueued sync job",
		slog.Int64("repo_id", repoID),
		slog.Int64("event_id", created.ID),
	)
	return created.ID, nil
}

func (s *Service) executeSync(eventID int64, repo store.Repository) {
	mu := s.getRepoLock(repo.ID)
	mu.Lock()
	defer mu.Unlock()

	start := time.Now().UTC()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	pullKey, err := s.keys.Get(ctx, repo.PullSSHKeyID)
	if err != nil {
		s.finishSync(eventID, repo, history.StatusError, fmt.Sprintf("Pull key not found: %v", err), "", start)
		return
	}
	pushKey, err := s.keys.Get(ctx, repo.PushSSHKeyID)
	if err != nil {
		s.finishSync(eventID, repo, history.StatusError, fmt.Sprintf("Push key not found: %v", err), "", start)
		return
	}

	pullKeyPath := filepath.Join(s.keysDir, pullKey.PrivatePath)
	pushKeyPath := filepath.Join(s.keysDir, pushKey.PrivatePath)

	cachePath, err := gitsync.GetCachePath(s.cacheDir, repo.PullURLSSH)
	if err != nil {
		s.finishSync(eventID, repo, history.StatusError, fmt.Sprintf("Failed to resolve cache path: %v", err), "", start)
		return
	}

	_, err = s.git.SyncRepository(ctx, gitsync.SyncOptions{
		CacheDir:      s.cacheDir,
		SourceURL:     repo.PullURLSSH,
		SourceKeyPath: pullKeyPath,
		Timeout:       10 * time.Minute,
	})
	if err != nil {
		s.finishSync(eventID, repo, history.StatusError, fmt.Sprintf("Pull sync failed: %v", err), "", start)
		return
	}

	pushResult, err := s.git.PushToRemote(ctx, gitsync.PushOptions{
		CachePath:     cachePath,
		RemoteURL:     repo.PushURLSSH,
		RemoteKeyPath: pushKeyPath,
		Timeout:       10 * time.Minute,
	})
	if err != nil {
		var conflictErr *gitsync.ErrConflict
		if errors.As(err, &conflictErr) {
			s.finishSync(eventID, repo, history.StatusConflict, conflictErr.Detail, "", start)
		} else {
			s.finishSync(eventID, repo, history.StatusError, fmt.Sprintf("Push sync failed: %v", err), "", start)
		}
		return
	}

	detail := "sync completed"
	if pushResult != nil && pushResult.RawOutput != "" {
		detail = pushResult.RawOutput
	}
	commitAfter := ""
	if pushResult != nil && pushResult.AfterSHA != "" {
		commitAfter = pushResult.AfterSHA
	}
	s.finishSync(eventID, repo, history.StatusSuccess, detail, commitAfter, start)
}

func (s *Service) finishSync(eventID int64, repo store.Repository, status history.Status, detail string, commitAfter string, start time.Time) {
	finishedAt := time.Now().UTC()
	durationMS := finishedAt.Sub(start).Milliseconds()
	if durationMS < 0 {
		durationMS = 0
	}

	if err := s.events.Finish(context.Background(), eventID, status, detail, commitAfter, finishedAt); err != nil {
		s.logger.Error("reposvc: failed to finish sync event", slog.Int64("event_id", eventID), slog.Any("error", err))
	}

	eventType := events.TypeSyncCompleted
	if status == history.StatusError || status == history.StatusConflict {
		eventType = events.TypeSyncFailed
	}

	if s.hub != nil {
		s.hub.Publish(events.SyncEventPayload{
			Type:       eventType,
			EventID:    eventID,
			RepoID:     repo.ID,
			RepoName:   repo.Name,
			Status:     string(status),
			Detail:     detail,
			DurationMS: durationMS,
			Timestamp:  finishedAt,
		})
	}
}

// SyncNow performs a pull+push sync for the repository synchronously and records a sync event.
func (s *Service) SyncNow(ctx context.Context, repoID int64) (int64, error) {
	start := time.Now().UTC()
	s.logger.Debug("reposvc.SyncNow started", slog.Int64("repo_id", repoID))

	repo, err := s.repos.Get(ctx, repoID)
	if err != nil {
		s.logger.Debug("reposvc.SyncNow failed to find repo", slog.Int64("repo_id", repoID), slog.Any("error", err))
		return 0, err
	}

	pullKey, err := s.keys.Get(ctx, repo.PullSSHKeyID)
	if err != nil {
		s.logger.Debug("reposvc.SyncNow pull key not found", slog.Int64("pull_ssh_key_id", repo.PullSSHKeyID), slog.Any("error", err))
		return 0, fmt.Errorf("pull key not found: %w", err)
	}
	pushKey, err := s.keys.Get(ctx, repo.PushSSHKeyID)
	if err != nil {
		s.logger.Debug("reposvc.SyncNow push key not found", slog.Int64("push_ssh_key_id", repo.PushSSHKeyID), slog.Any("error", err))
		return 0, fmt.Errorf("push key not found: %w", err)
	}

	pullKeyPath := filepath.Join(s.keysDir, pullKey.PrivatePath)
	pushKeyPath := filepath.Join(s.keysDir, pushKey.PrivatePath)

	cachePath, err := gitsync.GetCachePath(s.cacheDir, repo.PullURLSSH)
	if err != nil {
		s.logger.Debug("reposvc.SyncNow failed to resolve cache path", slog.Any("error", err))
		return 0, fmt.Errorf("failed to resolve cache path: %w", err)
	}

	s.logger.Debug("reposvc.SyncNow pulling source repository",
		slog.Int64("repo_id", repoID),
		slog.String("source_url", repo.PullURLSSH),
		slog.String("cache_path", cachePath),
		slog.String("pull_key_path", pullKeyPath),
	)

	_, err = s.git.SyncRepository(ctx, gitsync.SyncOptions{
		CacheDir:      s.cacheDir,
		SourceURL:     repo.PullURLSSH,
		SourceKeyPath: pullKeyPath,
		Timeout:       10 * time.Minute,
	})
	if err != nil {
		s.logger.Debug("reposvc.SyncNow pull sync failed", slog.Int64("repo_id", repoID), slog.Any("error", err))
		return 0, fmt.Errorf("pull sync failed: %w", err)
	}

	s.logger.Debug("reposvc.SyncNow pushing to remote repository",
		slog.Int64("repo_id", repoID),
		slog.String("remote_url", repo.PushURLSSH),
		slog.String("cache_path", cachePath),
		slog.String("push_key_path", pushKeyPath),
	)

	pushResult, err := s.git.PushToRemote(ctx, gitsync.PushOptions{
		CachePath:     cachePath,
		RemoteURL:     repo.PushURLSSH,
		RemoteKeyPath: pushKeyPath,
		Timeout:       10 * time.Minute,
	})
	if err != nil {
		s.logger.Debug("reposvc.SyncNow push sync failed", slog.Int64("repo_id", repoID), slog.Any("error", err))
		return 0, fmt.Errorf("push sync failed: %w", err)
	}

	detail := "sync completed"
	if pushResult != nil && pushResult.RawOutput != "" {
		detail = pushResult.RawOutput
	}
	commitAfter := ""
	if pushResult != nil && pushResult.AfterSHA != "" {
		commitAfter = pushResult.AfterSHA
	}

	event := store.SyncEvent{
		ReceivedAt:   start,
		FinishedAt:   time.Now().UTC(),
		Provider:     repo.ProviderHint,
		RepoFullName: repo.Name,
		CloneURL:     repo.PullURLRaw,
		SSHURL:       repo.PullURLSSH,
		Ref:          "refs/heads/*",
		CommitBefore: "",
		CommitAfter:  commitAfter,
		Status:       string(history.StatusSuccess),
		Detail:       detail,
		DurationMS:   time.Since(start).Milliseconds(),
	}
	created, err := s.events.Create(ctx, event)
	if err != nil {
		s.logger.Debug("reposvc.SyncNow failed to record sync event", slog.Int64("repo_id", repoID), slog.Any("error", err))
		return 0, fmt.Errorf("failed to record sync event: %w", err)
	}

	if s.hub != nil {
		s.hub.Publish(events.SyncEventPayload{
			Type:       events.TypeSyncCompleted,
			EventID:    created.ID,
			RepoID:     repo.ID,
			RepoName:   repo.Name,
			Status:     string(history.StatusSuccess),
			Detail:     detail,
			DurationMS: event.DurationMS,
			Timestamp:  event.FinishedAt,
		})
	}

	s.logger.Debug("reposvc.SyncNow completed successfully",
		slog.Int64("repo_id", repoID),
		slog.Int64("event_id", created.ID),
		slog.Int64("duration_ms", event.DurationMS),
	)
	return created.ID, nil
}

func inferProvider(rawURL string) string {
	lower := strings.ToLower(rawURL)
	if strings.Contains(lower, "gitlab") {
		return "gitlab"
	}
	if strings.Contains(lower, "github") {
		return "github"
	}
	if strings.Contains(lower, "forgejo") {
		return "forgejo"
	}
	if strings.Contains(lower, "gitea") {
		return "gitea"
	}
	return "generic"
}

func getWebhookSecretKey() ([]byte, error) {
	secret := strings.TrimSpace(os.Getenv("SECRET_KEY"))
	if secret == "" {
		secret = "development-secret-key-change-me"
	}
	sum := sha256.Sum256([]byte(secret))
	return sum[:], nil
}

func EncryptSecret(secret string) (string, error) {
	if strings.TrimSpace(secret) == "" {
		return "", nil
	}
	key, err := getWebhookSecretKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", fmt.Errorf("generate nonce: %w", err)
	}
	ciphertext := gcm.Seal(nonce, nonce, []byte(secret), nil)
	return hex.EncodeToString(ciphertext), nil
}

func DecryptSecret(secret string) (string, error) {
	secret = strings.TrimSpace(secret)
	if secret == "" {
		return "", nil
	}
	key, err := getWebhookSecretKey()
	if err != nil {
		return "", err
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", fmt.Errorf("create AES cipher: %w", err)
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", fmt.Errorf("create GCM: %w", err)
	}
	ciphertext, err := hex.DecodeString(secret)
	if err != nil {
		return "", fmt.Errorf("decode encrypted secret: %w", err)
	}
	if len(ciphertext) < gcm.NonceSize() {
		return "", errors.New("encrypted secret is too short")
	}
	nonce, payload := ciphertext[:gcm.NonceSize()], ciphertext[gcm.NonceSize():]
	plaintext, err := gcm.Open(nil, nonce, payload, nil)
	if err != nil {
		return "", fmt.Errorf("decrypt webhook secret: %w", err)
	}
	return string(plaintext), nil
}

func encryptSecret(secret string) (string, error) {
	return EncryptSecret(secret)
}

// TestConnectionRequest is the input for testing repository connections.
type TestConnectionRequest struct {
	PullURL      string `json:"pull_url"`
	PullSSHKeyID int64  `json:"pull_ssh_key_id"`
	PushURL      string `json:"push_url"`
	PushSSHKeyID int64  `json:"push_ssh_key_id"`
}

// DirectionTestResult holds the reachability outcome for one remote direction.
type DirectionTestResult struct {
	OK      bool   `json:"ok"`
	Message string `json:"message"`
}

// TestConnectionResult holds the reachability results for pull and push remotes.
type TestConnectionResult struct {
	Pull DirectionTestResult `json:"pull"`
	Push DirectionTestResult `json:"push"`
}

// TestConnection verifies reachability for pull and push remotes.
func (s *Service) TestConnection(ctx context.Context, req TestConnectionRequest) (TestConnectionResult, error) {
	s.logger.Debug("reposvc.TestConnection started",
		slog.String("pull_url", req.PullURL),
		slog.Int64("pull_ssh_key_id", req.PullSSHKeyID),
		slog.String("push_url", req.PushURL),
		slog.Int64("push_ssh_key_id", req.PushSSHKeyID),
	)

	result := TestConnectionResult{
		Pull: DirectionTestResult{OK: true, Message: "Connection successful"},
		Push: DirectionTestResult{OK: true, Message: "Connection successful"},
	}

	if req.PullURL == "" {
		result.Pull = DirectionTestResult{OK: false, Message: "Pull URL is required"}
		s.logger.Debug("reposvc.TestConnection pull validation failed: missing pull URL")
	} else {
		pullSSH, err := giturl.NormalizeToSSH(req.PullURL, "git")
		if err != nil {
			result.Pull = DirectionTestResult{OK: false, Message: fmt.Sprintf("Invalid pull URL: %v", err)}
			s.logger.Debug("reposvc.TestConnection pull normalize failed", slog.String("pull_url", req.PullURL), slog.Any("error", err))
		} else if req.PullSSHKeyID == 0 {
			result.Pull = DirectionTestResult{OK: false, Message: "Pull SSH key is required"}
			s.logger.Debug("reposvc.TestConnection pull validation failed: missing pull SSH key")
		} else {
			key, err := s.keys.Get(ctx, req.PullSSHKeyID)
			if err != nil {
				result.Pull = DirectionTestResult{OK: false, Message: fmt.Sprintf("Pull SSH key not found: %v", err)}
				s.logger.Debug("reposvc.TestConnection pull key get failed", slog.Int64("pull_ssh_key_id", req.PullSSHKeyID), slog.Any("error", err))
			} else {
				keyPath := filepath.Join(s.keysDir, key.PrivatePath)
				s.logger.Debug("reposvc.TestConnection testing pull LsRemote", slog.String("pull_ssh", pullSSH), slog.String("key_path", keyPath))
				if err := s.git.LsRemote(ctx, pullSSH, keyPath, 15*time.Second); err != nil {
					result.Pull = DirectionTestResult{OK: false, Message: err.Error()}
					s.logger.Debug("reposvc.TestConnection pull LsRemote failed", slog.Any("error", err))
				} else {
					s.logger.Debug("reposvc.TestConnection pull LsRemote succeeded")
				}
			}
		}
	}

	if req.PushURL == "" {
		result.Push = DirectionTestResult{OK: false, Message: "Push URL is required"}
		s.logger.Debug("reposvc.TestConnection push validation failed: missing push URL")
	} else {
		pushSSH, err := giturl.NormalizeToSSH(req.PushURL, "git")
		if err != nil {
			result.Push = DirectionTestResult{OK: false, Message: fmt.Sprintf("Invalid push URL: %v", err)}
			s.logger.Debug("reposvc.TestConnection push normalize failed", slog.String("push_url", req.PushURL), slog.Any("error", err))
		} else if req.PushSSHKeyID == 0 {
			result.Push = DirectionTestResult{OK: false, Message: "Push SSH key is required"}
			s.logger.Debug("reposvc.TestConnection push validation failed: missing push SSH key")
		} else {
			key, err := s.keys.Get(ctx, req.PushSSHKeyID)
			if err != nil {
				result.Push = DirectionTestResult{OK: false, Message: fmt.Sprintf("Push SSH key not found: %v", err)}
				s.logger.Debug("reposvc.TestConnection push key get failed", slog.Int64("push_ssh_key_id", req.PushSSHKeyID), slog.Any("error", err))
			} else {
				keyPath := filepath.Join(s.keysDir, key.PrivatePath)
				s.logger.Debug("reposvc.TestConnection testing push LsRemote", slog.String("push_ssh", pushSSH), slog.String("key_path", keyPath))
				if err := s.git.LsRemote(ctx, pushSSH, keyPath, 15*time.Second); err != nil {
					result.Push = DirectionTestResult{OK: false, Message: err.Error()}
					s.logger.Debug("reposvc.TestConnection push LsRemote failed", slog.Any("error", err))
				} else {
					s.logger.Debug("reposvc.TestConnection push LsRemote succeeded")
				}
			}
		}
	}

	s.logger.Debug("reposvc.TestConnection finished",
		slog.Bool("pull_ok", result.Pull.OK),
		slog.String("pull_message", result.Pull.Message),
		slog.Bool("push_ok", result.Push.OK),
		slog.String("push_message", result.Push.Message),
	)
	return result, nil
}
