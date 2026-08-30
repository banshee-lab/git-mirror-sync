package api_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"forgejo-sync/internal/api"
	"forgejo-sync/internal/auth"
	"forgejo-sync/internal/history"
	"forgejo-sync/internal/store"
)

type fakeRepoStore struct {
	repos map[int64]store.Repository
}

func (f *fakeRepoStore) Create(ctx context.Context, repo store.Repository) (store.Repository, error) {
	repo.ID = int64(len(f.repos) + 1)
	f.repos[repo.ID] = repo
	return repo, nil
}
func (f *fakeRepoStore) Update(ctx context.Context, repo store.Repository) (store.Repository, error) {
	f.repos[repo.ID] = repo
	return repo, nil
}
func (f *fakeRepoStore) Delete(ctx context.Context, id int64) error {
	delete(f.repos, id)
	return nil
}
func (f *fakeRepoStore) Get(ctx context.Context, id int64) (store.Repository, error) {
	repo, ok := f.repos[id]
	if !ok {
		return store.Repository{}, fmt.Errorf("not found")
	}
	return repo, nil
}
func (f *fakeRepoStore) List(ctx context.Context) ([]store.Repository, error) {
	var list []store.Repository
	for _, r := range f.repos {
		list = append(list, r)
	}
	return list, nil
}
func (f *fakeRepoStore) ListPage(ctx context.Context, search string, limit, offset int) ([]store.Repository, int, error) {
	list, _ := f.List(ctx)
	return list, len(list), nil
}
func (f *fakeRepoStore) GetByName(ctx context.Context, name string) (store.Repository, error) {
	for _, r := range f.repos {
		if r.Name == name {
			return r, nil
		}
	}
	return store.Repository{}, fmt.Errorf("not found")
}
func (f *fakeRepoStore) ListByPullURL(ctx context.Context, normalizedPullURL string) ([]store.Repository, error) {
	return nil, nil
}

type fakeEventStore struct {
	events []store.SyncEvent
}

func (f *fakeEventStore) Create(ctx context.Context, event store.SyncEvent) (store.SyncEvent, error) {
	event.ID = int64(len(f.events) + 1)
	f.events = append(f.events, event)
	return event, nil
}
func (f *fakeEventStore) Insert(ctx context.Context, event history.SyncEvent) (int64, error) {
	return 1, nil
}
func (f *fakeEventStore) List(ctx context.Context, limit int) ([]store.SyncEvent, error) {
	return f.events, nil
}
func (f *fakeEventStore) Finish(ctx context.Context, id int64, status history.Status, detail string, commitAfter string, finishedAt time.Time) error {
	return nil
}
func (f *fakeEventStore) Prune(ctx context.Context, olderThan time.Duration) (int64, error) {
	return 0, nil
}
func (f *fakeEventStore) PruneOlderThan(ctx context.Context, cutoff time.Time) (int64, error) {
	return 0, nil
}

func TestAPIRoutes(t *testing.T) {
	repos := &fakeRepoStore{repos: map[int64]store.Repository{
		1: {ID: 1, Name: "test-repo", Enabled: true},
	}}
	events := &fakeEventStore{events: []store.SyncEvent{
		{ID: 1, RepoFullName: "test-repo", Status: "success"},
	}}

	handler := api.NewHandler(api.Options{
		RepoStore:  repos,
		EventStore: events,
	})

	t.Run("GET /api/me unauthorized when no user in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusUnauthorized {
			t.Errorf("expected 401 Unauthorized, got %d", rec.Code)
		}
	})

	t.Run("GET /api/me with user in context", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
		req = req.WithContext(auth.WithUser(req.Context(), auth.User{Subject: "user1", Email: "u1@example.com"}))
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 OK, got %d", rec.Code)
		}
	})

	t.Run("GET /api/repositories", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/repositories", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 OK, got %d", rec.Code)
		}
	})

	t.Run("GET /api/repositories/1", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/repositories/1", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 OK, got %d", rec.Code)
		}
	})

	t.Run("GET /api/repositories/999 not found", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/repositories/999", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusNotFound {
			t.Errorf("expected 404 Not Found, got %d", rec.Code)
		}
	})

	t.Run("GET /api/repositories/invalid-id bad request", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/repositories/invalid", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 Bad Request, got %d", rec.Code)
		}
	})

	t.Run("GET /api/history", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/history?limit=10", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 OK, got %d", rec.Code)
		}
	})

	t.Run("GET /api/history invalid limit", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/api/history?limit=invalid", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusBadRequest {
			t.Errorf("expected 400 Bad Request, got %d", rec.Code)
		}
	})
}
