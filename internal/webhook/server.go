package webhook

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"forgejo-sync/internal/history"
)

// HistoryWriter defines the interface for inserting sync events into the history store.
type HistoryWriter interface {
	Insert(ctx context.Context, evt history.SyncEvent) (int64, error)
}

// ServerOptions contains dependencies and configuration for Server.
type ServerOptions struct {
	Registry      *Registry
	History       HistoryWriter
	Enqueuer      JobEnqueuer
	GitHubSecret  string
	GitLabSecret  string
	MaxBodyBytes  int64
	Logger        *slog.Logger
}

// Server handles incoming webhook HTTP requests.
type Server struct {
	registry     *Registry
	history      HistoryWriter
	enqueuer     JobEnqueuer
	secrets      map[string]string
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

	secrets := map[string]string{
		"github": opts.GitHubSecret,
		"gitlab": opts.GitLabSecret,
	}

	s := &Server{
		registry:     reg,
		history:      opts.History,
		enqueuer:     opts.Enqueuer,
		secrets:      secrets,
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

// SetSecret allows updating or setting a provider secret dynamically.
func (s *Server) SetSecret(providerName, secret string) {
	s.secrets[providerName] = secret
}

func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
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

	// Verify signature/token
	secret := s.secrets[provider.Name()]
	if err := provider.VerifySignature(secret, r.Header, body); err != nil {
		s.logger.Warn("webhook signature/token verification failed",
			slog.String("provider", provider.Name()),
			slog.Any("error", err),
		)
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	// Parse payload
	repoInfo, err := provider.Parse(body)
	if err != nil {
		s.logger.Warn("failed to parse webhook payload",
			slog.String("provider", provider.Name()),
			slog.Any("error", err),
		)
		http.Error(w, fmt.Sprintf("Failed to parse payload: %v", err), http.StatusBadRequest)
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
	}

	// Enqueue background job
	if s.enqueuer != nil {
		if err := s.enqueuer.Enqueue(r.Context(), *repoInfo, eventID); err != nil {
			s.logger.Error("failed to enqueue sync job",
				slog.String("repo", repoInfo.FullName),
				slog.Int64("event_id", eventID),
				slog.Any("error", err),
			)
			// History was recorded, but enqueue failed. We still return 202 or 500?
			// Webhook listener contract: if enqueue fails before return, return 500 so provider retries.
			http.Error(w, "Failed to enqueue sync job", http.StatusInternalServerError)
			return
		}
	}

	s.logger.Info("webhook received and enqueued",
		slog.String("provider", repoInfo.Provider),
		slog.String("repo", repoInfo.FullName),
		slog.Int64("event_id", eventID),
	)

	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte("Accepted\n"))
}
