package reposvc

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"time"

	"forgejo-sync/internal/giturl"
	"forgejo-sync/internal/gitsync"
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
}

// Service orchestrates repository CRUD and sync execution based on local SQLite-backed stores.
type Service struct {
	repos    store.RepositoryStore
	keys     store.SSHKeyStore
	events   EventRecorder
	git      GitSyncer
	keysDir  string
	cacheDir string
	logger   *slog.Logger
}

// NewService creates a repository service with the provided dependencies.
func NewService(repos store.RepositoryStore, keys store.SSHKeyStore, events EventRecorder, git GitSyncer, keysDir, cacheDir string, logger *slog.Logger) *Service {
	if logger == nil {
		logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	return &Service{repos: repos, keys: keys, events: events, git: git, keysDir: keysDir, cacheDir: cacheDir, logger: logger}
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

	repo := store.Repository{
		Name:             req.Name,
		PullURLRaw:       req.PullURL,
		PullURLSSH:       pullSSH,
		PullSSHKeyID:     req.PullSSHKeyID,
		PushURLRaw:       req.PushURL,
		PushURLSSH:       pushSSH,
		PushSSHKeyID:     req.PushSSHKeyID,
		WebhookSecHash:   hashSecret(req.WebhookSecret),
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
	if req.WebhookSecret == "" {
		req.WebhookSecret = existing.WebhookSecHash
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
	if req.WebhookSecret != existing.WebhookSecHash {
		existing.WebhookSecHash = hashSecret(req.WebhookSecret)
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

// SyncNow performs a pull+push sync for the repository and records a sync event.
func (s *Service) SyncNow(ctx context.Context, repoID int64) (int64, error) {
	start := time.Now()
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

	_, err = s.git.PushToRemote(ctx, gitsync.PushOptions{
		CachePath:     cachePath,
		RemoteURL:     repo.PushURLSSH,
		RemoteKeyPath: pushKeyPath,
		Timeout:       10 * time.Minute,
	})
	if err != nil {
		s.logger.Debug("reposvc.SyncNow push sync failed", slog.Int64("repo_id", repoID), slog.Any("error", err))
		return 0, fmt.Errorf("push sync failed: %w", err)
	}

	event := store.SyncEvent{
		ReceivedAt:   start,
		FinishedAt:   time.Now(),
		Provider:     repo.ProviderHint,
		RepoFullName: repo.Name,
		CloneURL:     repo.PullURLRaw,
		SSHURL:       repo.PullURLSSH,
		Ref:          "refs/heads/*",
		CommitBefore: "",
		CommitAfter:  "",
		Status:       "success",
		Detail:       "sync completed",
		DurationMS:   time.Since(start).Milliseconds(),
	}
	created, err := s.events.Create(ctx, event)
	if err != nil {
		s.logger.Debug("reposvc.SyncNow failed to record sync event", slog.Int64("repo_id", repoID), slog.Any("error", err))
		return 0, fmt.Errorf("failed to record sync event: %w", err)
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

func hashSecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
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
