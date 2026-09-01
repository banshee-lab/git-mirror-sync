package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"

	"forgejo-sync/internal/auth"
	"forgejo-sync/internal/reposvc"
	"forgejo-sync/internal/sshkeys"
	"forgejo-sync/internal/store"
)

// Options contains the dependencies required by the API handlers.
type Options struct {
	RepoStore  store.RepositoryStore
	KeyStore   store.SSHKeyStore
	EventStore store.SyncEventStore
	RepoSvc    *reposvc.Service
	KeyMgr     *sshkeys.Manager
	Auth       *auth.Provider
	Logger     *slog.Logger
}

// NewHandler returns the protected API router mounted under /api.
func NewHandler(opts Options) http.Handler {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/me", func(w http.ResponseWriter, r *http.Request) {
		user, ok := auth.ContextUser(r.Context())
		opts.Logger.Debug("api: GET /api/me", slog.Bool("authenticated", ok), slog.String("subject", user.Subject))
		if !ok {
			writeJSON(w, http.StatusUnauthorized, apiError("unauthorized", "authentication required"))
			return
		}
		writeJSON(w, http.StatusOK, user)
	})
	mux.HandleFunc("GET /api/repositories", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query().Get("q")
		page := 1
		if p := r.URL.Query().Get("page"); p != "" {
			if parsed, err := strconv.Atoi(p); err == nil && parsed > 0 {
				page = parsed
			}
		}
		pageSize := 20
		if ps := r.URL.Query().Get("page_size"); ps != "" {
			if parsed, err := strconv.Atoi(ps); err == nil && parsed > 0 {
				pageSize = parsed
			}
		}

		opts.Logger.Debug("api: GET /api/repositories",
			slog.String("query", q),
			slog.Int("page", page),
			slog.Int("page_size", pageSize),
		)

		if opts.RepoSvc != nil {
			result, err := opts.RepoSvc.List(r.Context(), reposvc.ListRequest{
				Search:   q,
				Page:     page,
				PageSize: pageSize,
			})
			if err != nil {
				opts.Logger.Debug("api: GET /api/repositories failed", slog.Any("error", err))
				writeJSON(w, http.StatusInternalServerError, apiError("repositories_list_failed", err.Error()))
				return
			}
			opts.Logger.Debug("api: GET /api/repositories succeeded", slog.Int("returned", len(result.Items)), slog.Int("total", result.Total))
			writeJSON(w, http.StatusOK, result)
			return
		}

		items, total, err := opts.RepoStore.ListPage(r.Context(), q, pageSize, (page-1)*pageSize)
		if err != nil {
			opts.Logger.Debug("api: GET /api/repositories fallback failed", slog.Any("error", err))
			writeJSON(w, http.StatusInternalServerError, apiError("repositories_list_failed", err.Error()))
			return
		}
		opts.Logger.Debug("api: GET /api/repositories fallback succeeded", slog.Int("returned", len(items)), slog.Int("total", total))
		writeJSON(w, http.StatusOK, map[string]any{
			"items":     items,
			"total":     total,
			"page":      page,
			"page_size": pageSize,
		})
	})
	mux.HandleFunc("POST /api/repositories", func(w http.ResponseWriter, r *http.Request) {
		var req reposvc.CreateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			opts.Logger.Debug("api: POST /api/repositories invalid JSON", slog.Any("error", err))
			writeJSON(w, http.StatusBadRequest, apiError("invalid_request", "request body must be valid JSON"))
			return
		}
		opts.Logger.Debug("api: POST /api/repositories request",
			slog.String("name", req.Name),
			slog.String("pull_url", req.PullURL),
			slog.Int64("pull_ssh_key_id", req.PullSSHKeyID),
			slog.String("push_url", req.PushURL),
			slog.Int64("push_ssh_key_id", req.PushSSHKeyID),
			slog.Bool("sync_now", req.SyncNow),
		)
		repo, err := opts.RepoSvc.Create(r.Context(), req)
		if err != nil {
			opts.Logger.Debug("api: POST /api/repositories failed", slog.Any("error", err))
			writeJSON(w, http.StatusUnprocessableEntity, apiError("repo_create_failed", err.Error()))
			return
		}
		opts.Logger.Debug("api: POST /api/repositories succeeded", slog.Int64("id", repo.ID), slog.String("name", repo.Name))
		writeJSON(w, http.StatusCreated, repo)
	})
	mux.HandleFunc("GET /api/repositories/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := parseID(r.PathValue("id"))
		if err != nil {
			opts.Logger.Debug("api: GET /api/repositories/{id} invalid ID", slog.String("raw_id", r.PathValue("id")), slog.Any("error", err))
			writeJSON(w, http.StatusBadRequest, apiError("invalid_id", err.Error()))
			return
		}
		opts.Logger.Debug("api: GET /api/repositories/{id}", slog.Int64("id", id))
		repo, err := opts.RepoStore.Get(r.Context(), id)
		if err != nil {
			opts.Logger.Debug("api: GET /api/repositories/{id} not found", slog.Int64("id", id), slog.Any("error", err))
			writeJSON(w, http.StatusNotFound, apiError("repository_not_found", err.Error()))
			return
		}
		opts.Logger.Debug("api: GET /api/repositories/{id} succeeded", slog.Int64("id", repo.ID), slog.String("name", repo.Name))
		writeJSON(w, http.StatusOK, repo)
	})
	mux.HandleFunc("PUT /api/repositories/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := parseID(r.PathValue("id"))
		if err != nil {
			opts.Logger.Debug("api: PUT /api/repositories/{id} invalid ID", slog.String("raw_id", r.PathValue("id")), slog.Any("error", err))
			writeJSON(w, http.StatusBadRequest, apiError("invalid_id", err.Error()))
			return
		}
		var req reposvc.UpdateRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			opts.Logger.Debug("api: PUT /api/repositories/{id} invalid JSON", slog.Int64("id", id), slog.Any("error", err))
			writeJSON(w, http.StatusBadRequest, apiError("invalid_request", "request body must be valid JSON"))
			return
		}
		opts.Logger.Debug("api: PUT /api/repositories/{id} request",
			slog.Int64("id", id),
			slog.String("name", req.Name),
			slog.String("pull_url", req.PullURL),
			slog.Int64("pull_ssh_key_id", req.PullSSHKeyID),
			slog.String("push_url", req.PushURL),
			slog.Int64("push_ssh_key_id", req.PushSSHKeyID),
			slog.Bool("sync_now", req.SyncNow),
		)
		repo, err := opts.RepoSvc.Update(r.Context(), id, req)
		if err != nil {
			opts.Logger.Debug("api: PUT /api/repositories/{id} failed", slog.Int64("id", id), slog.Any("error", err))
			writeJSON(w, http.StatusUnprocessableEntity, apiError("repo_update_failed", err.Error()))
			return
		}
		opts.Logger.Debug("api: PUT /api/repositories/{id} succeeded", slog.Int64("id", repo.ID), slog.String("name", repo.Name))
		writeJSON(w, http.StatusOK, repo)
	})
	mux.HandleFunc("DELETE /api/repositories/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := parseID(r.PathValue("id"))
		if err != nil {
			opts.Logger.Debug("api: DELETE /api/repositories/{id} invalid ID", slog.String("raw_id", r.PathValue("id")), slog.Any("error", err))
			writeJSON(w, http.StatusBadRequest, apiError("invalid_id", err.Error()))
			return
		}
		opts.Logger.Debug("api: DELETE /api/repositories/{id}", slog.Int64("id", id))
		if err := opts.RepoStore.Delete(r.Context(), id); err != nil {
			opts.Logger.Debug("api: DELETE /api/repositories/{id} failed", slog.Int64("id", id), slog.Any("error", err))
			writeJSON(w, http.StatusInternalServerError, apiError("repo_delete_failed", err.Error()))
			return
		}
		opts.Logger.Debug("api: DELETE /api/repositories/{id} succeeded", slog.Int64("id", id))
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
	})
	mux.HandleFunc("POST /api/repositories/{id}/sync", func(w http.ResponseWriter, r *http.Request) {
		id, err := parseID(r.PathValue("id"))
		if err != nil {
			opts.Logger.Debug("api: POST /api/repositories/{id}/sync invalid ID", slog.String("raw_id", r.PathValue("id")), slog.Any("error", err))
			writeJSON(w, http.StatusBadRequest, apiError("invalid_id", err.Error()))
			return
		}
		opts.Logger.Debug("api: POST /api/repositories/{id}/sync started", slog.Int64("id", id))
		eventID, err := opts.RepoSvc.SyncNow(r.Context(), id)
		if err != nil {
			opts.Logger.Debug("api: POST /api/repositories/{id}/sync failed", slog.Int64("id", id), slog.Any("error", err))
			writeJSON(w, http.StatusInternalServerError, apiError("sync_failed", err.Error()))
			return
		}
		opts.Logger.Debug("api: POST /api/repositories/{id}/sync succeeded", slog.Int64("id", id), slog.Int64("event_id", eventID))
		writeJSON(w, http.StatusAccepted, map[string]any{"event_id": eventID})
	})
	mux.HandleFunc("POST /api/repositories/test-connection", func(w http.ResponseWriter, r *http.Request) {
		var req reposvc.TestConnectionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			opts.Logger.Debug("api: POST /api/repositories/test-connection invalid JSON", slog.Any("error", err))
			writeJSON(w, http.StatusBadRequest, apiError("invalid_request", "request body must be valid JSON"))
			return
		}
		opts.Logger.Debug("api: POST /api/repositories/test-connection request",
			slog.String("pull_url", req.PullURL),
			slog.Int64("pull_ssh_key_id", req.PullSSHKeyID),
			slog.String("push_url", req.PushURL),
			slog.Int64("push_ssh_key_id", req.PushSSHKeyID),
		)
		result, err := opts.RepoSvc.TestConnection(r.Context(), req)
		if err != nil {
			opts.Logger.Debug("api: POST /api/repositories/test-connection failed", slog.Any("error", err))
			writeJSON(w, http.StatusInternalServerError, apiError("test_connection_failed", err.Error()))
			return
		}
		opts.Logger.Debug("api: POST /api/repositories/test-connection succeeded",
			slog.Bool("pull_ok", result.Pull.OK),
			slog.Bool("push_ok", result.Push.OK),
		)
		writeJSON(w, http.StatusOK, result)
	})
	mux.HandleFunc("POST /api/repositories/{id}/test-connection", func(w http.ResponseWriter, r *http.Request) {
		id, err := parseID(r.PathValue("id"))
		if err != nil {
			opts.Logger.Debug("api: POST /api/repositories/{id}/test-connection invalid ID", slog.String("raw_id", r.PathValue("id")), slog.Any("error", err))
			writeJSON(w, http.StatusBadRequest, apiError("invalid_id", err.Error()))
			return
		}
		opts.Logger.Debug("api: POST /api/repositories/{id}/test-connection started", slog.Int64("id", id))
		repo, err := opts.RepoStore.Get(r.Context(), id)
		if err != nil {
			opts.Logger.Debug("api: POST /api/repositories/{id}/test-connection repo not found", slog.Int64("id", id), slog.Any("error", err))
			writeJSON(w, http.StatusNotFound, apiError("repository_not_found", err.Error()))
			return
		}
		req := reposvc.TestConnectionRequest{
			PullURL:      repo.PullURLSSH,
			PullSSHKeyID: repo.PullSSHKeyID,
			PushURL:      repo.PushURLSSH,
			PushSSHKeyID: repo.PushSSHKeyID,
		}
		result, err := opts.RepoSvc.TestConnection(r.Context(), req)
		if err != nil {
			opts.Logger.Debug("api: POST /api/repositories/{id}/test-connection test failed", slog.Int64("id", id), slog.Any("error", err))
			writeJSON(w, http.StatusInternalServerError, apiError("test_connection_failed", err.Error()))
			return
		}
		opts.Logger.Debug("api: POST /api/repositories/{id}/test-connection succeeded",
			slog.Int64("id", id),
			slog.Bool("pull_ok", result.Pull.OK),
			slog.Bool("push_ok", result.Push.OK),
		)
		writeJSON(w, http.StatusOK, result)
	})
	mux.HandleFunc("GET /api/ssh-keys", func(w http.ResponseWriter, r *http.Request) {
		opts.Logger.Debug("api: GET /api/ssh-keys")
		keys, err := opts.KeyStore.List(r.Context())
		if err != nil {
			opts.Logger.Debug("api: GET /api/ssh-keys failed", slog.Any("error", err))
			writeJSON(w, http.StatusInternalServerError, apiError("keys_list_failed", err.Error()))
			return
		}
		opts.Logger.Debug("api: GET /api/ssh-keys succeeded", slog.Int("count", len(keys)))
		writeJSON(w, http.StatusOK, keys)
	})
	mux.HandleFunc("POST /api/ssh-keys", func(w http.ResponseWriter, r *http.Request) {
		if opts.KeyMgr == nil {
			opts.Logger.Debug("api: POST /api/ssh-keys failed: key manager unavailable")
			writeJSON(w, http.StatusInternalServerError, apiError("ssh_key_manager_missing", "key manager is unavailable"))
			return
		}
		var req struct {
			Name    string `json:"name"`
			KeyType string `json:"key_type"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			opts.Logger.Debug("api: POST /api/ssh-keys invalid JSON", slog.Any("error", err))
			writeJSON(w, http.StatusBadRequest, apiError("invalid_request", "request body must be valid JSON"))
			return
		}
		opts.Logger.Debug("api: POST /api/ssh-keys request", slog.String("name", req.Name), slog.String("key_type", req.KeyType))
		keyType := sshkeys.KeyType(req.KeyType)
		if keyType != sshkeys.KeyTypeRSA && keyType != sshkeys.KeyTypeEd25519 {
			opts.Logger.Debug("api: POST /api/ssh-keys invalid key type", slog.String("key_type", req.KeyType))
			writeJSON(w, http.StatusBadRequest, apiError("invalid_key_type", "key_type must be rsa or ed25519"))
			return
		}
		key, err := opts.KeyMgr.GenerateKeyPair(r.Context(), keyType, req.Name)
		if err != nil {
			opts.Logger.Debug("api: POST /api/ssh-keys failed", slog.Any("error", err))
			writeJSON(w, http.StatusUnprocessableEntity, apiError("ssh_key_create_failed", err.Error()))
			return
		}
		opts.Logger.Debug("api: POST /api/ssh-keys succeeded", slog.Int64("id", key.ID), slog.String("name", key.Name))
		writeJSON(w, http.StatusCreated, key)
	})
	mux.HandleFunc("GET /api/ssh-keys/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := parseID(r.PathValue("id"))
		if err != nil {
			opts.Logger.Debug("api: GET /api/ssh-keys/{id} invalid ID", slog.String("raw_id", r.PathValue("id")), slog.Any("error", err))
			writeJSON(w, http.StatusBadRequest, apiError("invalid_id", err.Error()))
			return
		}
		opts.Logger.Debug("api: GET /api/ssh-keys/{id}", slog.Int64("id", id))
		key, err := opts.KeyStore.Get(r.Context(), id)
		if err != nil {
			opts.Logger.Debug("api: GET /api/ssh-keys/{id} not found", slog.Int64("id", id), slog.Any("error", err))
			writeJSON(w, http.StatusNotFound, apiError("ssh_key_not_found", err.Error()))
			return
		}
		opts.Logger.Debug("api: GET /api/ssh-keys/{id} succeeded", slog.Int64("id", key.ID), slog.String("name", key.Name))
		writeJSON(w, http.StatusOK, key)
	})
	mux.HandleFunc("GET /api/ssh-keys/{id}/public-key", func(w http.ResponseWriter, r *http.Request) {
		id, err := parseID(r.PathValue("id"))
		if err != nil {
			opts.Logger.Debug("api: GET /api/ssh-keys/{id}/public-key invalid ID", slog.String("raw_id", r.PathValue("id")), slog.Any("error", err))
			writeJSON(w, http.StatusBadRequest, apiError("invalid_id", err.Error()))
			return
		}
		opts.Logger.Debug("api: GET /api/ssh-keys/{id}/public-key", slog.Int64("id", id))
		if opts.KeyMgr == nil {
			opts.Logger.Debug("api: GET /api/ssh-keys/{id}/public-key failed: key manager unavailable")
			writeJSON(w, http.StatusInternalServerError, apiError("ssh_key_manager_missing", "key manager is unavailable"))
			return
		}
		publicKey, err := opts.KeyMgr.GetPublicKey(r.Context(), id)
		if err != nil {
			opts.Logger.Debug("api: GET /api/ssh-keys/{id}/public-key not found", slog.Int64("id", id), slog.Any("error", err))
			writeJSON(w, http.StatusNotFound, apiError("public_key_not_found", err.Error()))
			return
		}
		opts.Logger.Debug("api: GET /api/ssh-keys/{id}/public-key succeeded", slog.Int64("id", id))
		writeJSON(w, http.StatusOK, map[string]any{"public_key": strings.TrimSpace(publicKey)})
	})
	mux.HandleFunc("DELETE /api/ssh-keys/{id}", func(w http.ResponseWriter, r *http.Request) {
		id, err := parseID(r.PathValue("id"))
		if err != nil {
			opts.Logger.Debug("api: DELETE /api/ssh-keys/{id} invalid ID", slog.String("raw_id", r.PathValue("id")), slog.Any("error", err))
			writeJSON(w, http.StatusBadRequest, apiError("invalid_id", err.Error()))
			return
		}
		opts.Logger.Debug("api: DELETE /api/ssh-keys/{id}", slog.Int64("id", id))
		if opts.KeyMgr == nil {
			opts.Logger.Debug("api: DELETE /api/ssh-keys/{id} failed: key manager unavailable")
			writeJSON(w, http.StatusInternalServerError, apiError("ssh_key_manager_missing", "key manager is unavailable"))
			return
		}
		if err := opts.KeyMgr.Delete(r.Context(), id); err != nil {
			opts.Logger.Debug("api: DELETE /api/ssh-keys/{id} failed", slog.Int64("id", id), slog.Any("error", err))
			writeJSON(w, http.StatusConflict, apiError("ssh_key_delete_failed", err.Error()))
			return
		}
		opts.Logger.Debug("api: DELETE /api/ssh-keys/{id} succeeded", slog.Int64("id", id))
		writeJSON(w, http.StatusOK, map[string]any{"deleted": true, "id": id})
	})
	mux.HandleFunc("GET /api/history", func(w http.ResponseWriter, r *http.Request) {
		limit := 30
		if q := r.URL.Query().Get("limit"); q != "" {
			parsed, err := strconv.Atoi(q)
			if err != nil || parsed <= 0 {
				opts.Logger.Debug("api: GET /api/history invalid limit", slog.String("limit", q))
				writeJSON(w, http.StatusBadRequest, apiError("invalid_limit", "limit must be a positive integer"))
				return
			}
			limit = parsed
		}
		opts.Logger.Debug("api: GET /api/history", slog.Int("limit", limit))
		if opts.EventStore == nil {
			opts.Logger.Debug("api: GET /api/history failed: event store unavailable")
			writeJSON(w, http.StatusInternalServerError, apiError("history_unavailable", "history store is not configured"))
			return
		}
		events, err := opts.EventStore.List(r.Context(), limit)
		if err != nil {
			opts.Logger.Debug("api: GET /api/history failed", slog.Any("error", err))
			writeJSON(w, http.StatusInternalServerError, apiError("history_list_failed", err.Error()))
			return
		}
		opts.Logger.Debug("api: GET /api/history succeeded", slog.Int("count", len(events)))
		writeJSON(w, http.StatusOK, events)
	})
	return mux
}

func parseID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil {
		return 0, errors.New("id must be a valid integer")
	}
	if id <= 0 {
		return 0, errors.New("id must be greater than zero")
	}
	return id, nil
}

func apiError(code, message string) map[string]any {
	return map[string]any{"error": map[string]string{"code": code, "message": message}}
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

type contextKey string

const apiContextKey contextKey = "api"

func WithContext(ctx context.Context, value any) context.Context {
	return context.WithValue(ctx, apiContextKey, value)
}

func currentUser(ctx context.Context) (auth.User, bool) {
	return auth.ContextUser(ctx)
}
