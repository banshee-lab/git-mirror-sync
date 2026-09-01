package webhook

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"forgejo-sync/internal/giturl"
	"forgejo-sync/internal/history"
	"forgejo-sync/internal/reposvc"
	"forgejo-sync/internal/store"
)

// HistoryWriter defines the interface for inserting sync events into the history store.
type HistoryWriter interface {
	Insert(ctx context.Context, evt history.SyncEvent) (int64, error)
}

// ServerOptions contains dependencies and configuration for Server.
type ServerOptions struct {
	Registry       *Registry
	History        HistoryWriter
	Enqueuer       JobEnqueuer
	RepositoryStore store.RepositoryStore
	MaxBodyBytes   int64
	Logger         *slog.Logger
}

// Server handles incoming webhook HTTP requests.
type Server struct {
	registry     *Registry
	history      HistoryWriter
	enqueuer     JobEnqueuer
	repoStore    store.RepositoryStore
	maxBodyBytes int64
	logger       *slog.Logger
	handler      http.Handler
}

// DefaultMaxBodyBytes is the default maximum webhook payload size (5MB).
const DefaultMaxBodyBytes = 5 * 1024 * 1024

// NewServer creates a new webhook Server and initializes its HTTP routes.
func NewServer(opts ServerOptions) *Server {
	maxBytes := opts.MaxBodyBytes
	if maxBytes <= 0 {
		maxBytes = DefaultMaxBodyBytes
	}

	logger := opts.Logger
	if logger == nil {
		logger = slog.Default()
	}

	reg := opts.Registry
	if reg == nil {
		reg = NewRegistry(NewGitHubProvider(), NewGitLabProvider())
	}

	s := &Server{
		registry:     reg,
		history:      opts.History,
		enqueuer:     opts.Enqueuer,
		repoStore:    opts.RepositoryStore,
		maxBodyBytes: maxBytes,
		logger:       logger,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("POST /webhook", s.handleWebhook)
	mux.HandleFunc("GET /healthz", s.handleHealthz)
	s.handler = mux

	return s
}

// Handler returns the http.Handler for the webhook server.
func (s *Server) Handler() http.Handler {
	return s.handler
}

// ServeHTTP implements http.Handler.
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	s.handler.ServeHTTP(w, r)
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	s.logger.Debug("webhook: incoming request",
		slog.String("method", r.Method),
		slog.String("remote_addr", r.RemoteAddr),
		slog.Int64("content_length", r.ContentLength),
		slog.String("github_event", r.Header.Get("X-GitHub-Event")),
		slog.String("gitlab_event", r.Header.Get("X-Gitlab-Event")),
	)

	if r.Method != http.MethodPost {
		s.logger.Debug("webhook: method not allowed", slog.String("method", r.Method))
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	provider := s.registry.Match(r.Header)
	if provider == nil {
		s.logger.Warn("no matching webhook provider found for request headers",
			slog.String("remote_addr", r.RemoteAddr),
		)
		http.Error(w, "No matching webhook provider", http.StatusBadRequest)
		return
	}
	s.logger.Debug("webhook: matched provider", slog.String("provider", provider.Name()))

	// Limit request body size
	r.Body = http.MaxBytesReader(w, r.Body, s.maxBodyBytes)
	body, err := io.ReadAll(r.Body)
	if err != nil {
		var maxBytesErr *http.MaxBytesError
		if errors.As(err, &maxBytesErr) {
			s.logger.Warn("webhook payload exceeded size limit",
				slog.String("provider", provider.Name()),
				slog.Int64("limit", s.maxBodyBytes),
			)
			http.Error(w, "Payload Too Large", http.StatusRequestEntityTooLarge)
			return
		}
		s.logger.Warn("failed to read webhook body",
			slog.String("provider", provider.Name()),
			slog.Any("error", err),
		)
		http.Error(w, "Bad Request", http.StatusBadRequest)
		return
	}
	s.logger.Debug("webhook: read payload bytes", slog.Int("bytes_read", len(body)))

	// Parse payload first so we can match the repository and verify the request against
	// the repository-specific secret instead of a global fallback secret map.
	repoInfo, err := provider.Parse(body)
	if err != nil {
		s.logger.Warn("failed to parse webhook payload",
			slog.String("provider", provider.Name()),
			slog.Any("error", err),
		)
		http.Error(w, fmt.Sprintf("Failed to parse payload: %v", err), http.StatusBadRequest)
		return
	}

	s.logger.Debug("webhook: parsed repository info",
		slog.String("provider", repoInfo.Provider),
		slog.String("full_name", repoInfo.FullName),
		slog.String("owner", repoInfo.Owner),
		slog.String("name", repoInfo.Name),
		slog.String("ref", repoInfo.Ref),
		slog.String("before", repoInfo.Before),
		slog.String("after", repoInfo.After),
		slog.String("clone_url", repoInfo.CloneURL),
		slog.String("ssh_url", repoInfo.SSHURL),
	)

	matchURL := repoInfo.CloneURL
	if matchURL == "" {
		matchURL = repoInfo.SSHURL
	}
	if matchURL != "" {
		rawMatchURL := matchURL
		matchURL, err = giturl.NormalizeToSSH(matchURL, "git")
		if err != nil {
			s.logger.Warn("failed to normalize repository URL for lookup",
				slog.String("provider", provider.Name()),
				slog.String("repo", repoInfo.FullName),
				slog.String("raw_url", rawMatchURL),
				slog.Any("error", err),
			)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		s.logger.Debug("webhook: normalized URL for lookup",
			slog.String("raw_url", rawMatchURL),
			slog.String("normalized_url", matchURL),
		)
	}

	matchedRepo := (*store.Repository)(nil)
	if s.repoStore != nil {
		s.logger.Debug("webhook: querying store for matching repositories", slog.String("url", matchURL))
		repos, err := s.repoStore.ListByPullURL(r.Context(), matchURL)
		if err != nil {
			s.logger.Warn("failed to lookup matching repositories",
				slog.String("provider", provider.Name()),
				slog.String("repo", repoInfo.FullName),
				slog.Any("error", err),
			)
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		s.logger.Debug("webhook: found candidate repositories", slog.Int("count", len(repos)))
		for i := range repos {
			repo := &repos[i]
			s.logger.Debug("webhook: evaluating candidate repository",
				slog.Int64("repo_id", repo.ID),
				slog.String("repo_name", repo.Name),
				slog.Bool("enabled", repo.Enabled),
				slog.String("provider_hint", repo.ProviderHint),
			)
			if !repo.Enabled {
				s.logger.Debug("webhook: candidate is disabled", slog.Int64("repo_id", repo.ID))
				continue
			}
			if repo.ProviderHint != "" && repo.ProviderHint != provider.Name() {
				s.logger.Debug("webhook: candidate provider hint mismatch",
					slog.Int64("repo_id", repo.ID),
					slog.String("provider_hint", repo.ProviderHint),
					slog.String("actual_provider", provider.Name()),
				)
				continue
			}
			if err := verifyRepoSignature(provider, repo, r.Header, body); err == nil {
				s.logger.Debug("webhook: signature verification succeeded for repository",
					slog.Int64("repo_id", repo.ID),
					slog.String("repo_name", repo.Name),
				)
				matchedRepo = repo
				break
			} else {
				s.logger.Debug("webhook: signature verification failed for repository",
					slog.Int64("repo_id", repo.ID),
					slog.String("repo_name", repo.Name),
					slog.Any("error", err),
				)
			}
		}
	}
	if matchedRepo == nil {
		s.logger.Warn("webhook signature/token verification failed",
			slog.String("provider", provider.Name()),
			slog.String("repo", repoInfo.FullName),
		)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Insert history event
	var eventID int64
	if s.history != nil {
		evt := history.SyncEvent{
			ReceivedAt:   time.Now().UTC(),
			Provider:     repoInfo.Provider,
			RepoFullName: repoInfo.FullName,
			CloneURL:     repoInfo.CloneURL,
			SSHURL:       repoInfo.SSHURL,
			Ref:          repoInfo.Ref,
			CommitBefore: repoInfo.Before,
			CommitAfter:  repoInfo.After,
			Status:       history.StatusReceived,
		}

		id, err := s.history.Insert(r.Context(), evt)
		if err != nil {
			s.logger.Error("failed to record received sync event in history",
				slog.String("repo", repoInfo.FullName),
				slog.Any("error", err),
			)
			http.Error(w, "Internal Server Error", http.StatusInternalServerError)
			return
		}
		eventID = id
		s.logger.Debug("webhook: inserted received sync event", slog.Int64("event_id", eventID))
	}

	// Enqueue background job
	if s.enqueuer != nil {
		s.logger.Debug("webhook: enqueuing sync job to worker pool",
			slog.Int64("event_id", eventID),
			slog.String("repo", repoInfo.FullName),
		)
		if err := s.enqueuer.Enqueue(r.Context(), *repoInfo, eventID); err != nil {
			s.logger.Error("failed to enqueue sync job",
				slog.String("repo", repoInfo.FullName),
				slog.Int64("event_id", eventID),
				slog.Any("error", err),
			)
			http.Error(w, "Failed to enqueue sync job", http.StatusInternalServerError)
			return
		}
		s.logger.Debug("webhook: enqueued sync job successfully", slog.Int64("event_id", eventID))
	}

	s.logger.Info("webhook received and enqueued",
		slog.String("provider", repoInfo.Provider),
		slog.String("repo", repoInfo.FullName),
		slog.Int64("event_id", eventID),
		slog.String("matched_repo", matchedRepo.Name),
	)

	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte("Accepted\n"))
}

func verifyRepoSignature(provider Provider, repo *store.Repository, h http.Header, body []byte) error {
	if repo == nil || repo.WebhookSecHash == "" {
		return errors.New("repository secret missing")
	}

	secret, err := reposvc.DecryptSecret(repo.WebhookSecHash)
	if err != nil {
		return fmt.Errorf("decrypt repository secret: %w", err)
	}
	if err := provider.VerifySignature(secret, h, body); err == nil {
		return nil
	}

	return errors.New("repository secret verification failed")
}

