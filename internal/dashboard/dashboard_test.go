package dashboard_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"forgejo-sync/internal/dashboard"
	"forgejo-sync/internal/history"
)

type mockHistoryStore struct {
	events []history.SyncEvent
	err    error
}

func (m *mockHistoryStore) Recent(ctx context.Context, limit int) ([]history.SyncEvent, error) {
	if m.err != nil {
		return nil, m.err
	}
	if len(m.events) > limit {
		return m.events[:limit], nil
	}
	return m.events, nil
}

func TestHealthzHandler(t *testing.T) {
	router, err := dashboard.NewRouter(nil)
	if err != nil {
		t.Fatalf("failed to create router: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "OK") {
		t.Errorf("expected body to contain 'OK', got %q", body)
	}
}

func TestStaticAssets(t *testing.T) {
	router, err := dashboard.NewRouter(nil)
	if err != nil {
		t.Fatalf("failed to create router: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/static/app.css", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "transition-smooth") {
		t.Errorf("expected static css content, got %q", body)
	}
}

func TestIndexEmpty(t *testing.T) {
	mock := &mockHistoryStore{
		events: []history.SyncEvent{},
	}

	router, err := dashboard.NewRouter(mock)
	if err != nil {
		t.Fatalf("failed to create router: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "No sync events recorded yet") {
		t.Errorf("expected empty state message in response body")
	}
}

func TestIndexWithEvents(t *testing.T) {
	dur1 := int64(1420)
	dur2 := int64(350)
	now := time.Date(2026, 8, 28, 12, 0, 0, 0, time.UTC)

	events := []history.SyncEvent{
		{
			ID:           1,
			ReceivedAt:   now,
			Provider:     "github",
			RepoFullName: "org/super-repo",
			ForgejoOwner: "org",
			ForgejoRepo:  "super-repo",
			Ref:          "refs/heads/main",
			CommitBefore: "a1b2c3d4e5f6",
			CommitAfter:  "f6e5d4c3b2a1",
			Status:       history.StatusSuccess,
			Detail:       "Pushed 1 commit to Forgejo",
			DurationMS:   &dur1,
		},
		{
			ID:           2,
			ReceivedAt:   now.Add(-5 * time.Minute),
			Provider:     "gitlab",
			RepoFullName: "gitlab-org/project",
			Ref:          "refs/heads/dev",
			Status:       history.StatusConflict,
			Detail:       "Diverged history on push mirror",
			DurationMS:   &dur2,
		},
		{
			ID:           3,
			ReceivedAt:   now.Add(-10 * time.Minute),
			Provider:     "github",
			RepoFullName: "other/repo",
			Status:       history.StatusError,
			Detail:       "SSH authentication timeout",
		},
		{
			ID:           4,
			ReceivedAt:   now.Add(-15 * time.Minute),
			Provider:     "github",
			RepoFullName: "skip/repo",
			Status:       history.StatusSkipped,
			Detail:       "No push mirror found",
		},
		{
			ID:           5,
			ReceivedAt:   now.Add(-20 * time.Minute),
			Provider:     "gitlab",
			RepoFullName: "incoming/repo",
			Status:       history.StatusReceived,
			Detail:       "Queued for processing",
		},
	}

	mock := &mockHistoryStore{
		events: events,
	}

	router, err := dashboard.NewRouter(mock)
	if err != nil {
		t.Fatalf("failed to create router: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	body := rec.Body.String()

	// Verify status badges
	statuses := []string{"success", "conflict", "error", "skipped", "received"}
	for _, status := range statuses {
		if !strings.Contains(body, status) {
			t.Errorf("expected response to contain status badge %q", status)
		}
	}

	// Verify repo names and details
	expectedSnippets := []string{
		"org/super-repo",
		"gitlab-org/project",
		"other/repo",
		"skip/repo",
		"incoming/repo",
		"refs/heads/main",
		"a1b2c3d4", // truncated commit SHA
		"f6e5d4c3",
		"1.42s", // duration formatting
		"350ms",
		"Pushed 1 commit to Forgejo",
		"Diverged history on push mirror",
		"SSH authentication timeout",
	}

	for _, snippet := range expectedSnippets {
		if !strings.Contains(body, snippet) {
			t.Errorf("expected response body to contain snippet %q", snippet)
		}
	}
}

func TestIndexWithRealSQLiteStore(t *testing.T) {
	// Initialize in-memory SQLite store
	store, err := history.NewStore(":memory:")
	if err != nil {
		t.Fatalf("failed to initialize sqlite store: %v", err)
	}
	defer store.Close()

	ctx := context.Background()
	evtID, err := store.Insert(ctx, history.SyncEvent{
		Provider:     "github",
		RepoFullName: "test/repo-sqlite",
		Status:       history.StatusReceived,
		Detail:       "init event",
	})
	if err != nil {
		t.Fatalf("failed to insert event: %v", err)
	}

	err = store.Finish(ctx, evtID, history.StatusSuccess, "completed successfully", "abcdef1234567890", time.Now().UTC())
	if err != nil {
		t.Fatalf("failed to finish event: %v", err)
	}

	router, err := dashboard.NewRouter(store)
	if err != nil {
		t.Fatalf("failed to create router: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status %d, got %d", http.StatusOK, rec.Code)
	}

	body := rec.Body.String()
	if !strings.Contains(body, "test/repo-sqlite") {
		t.Errorf("expected body to contain repo name, got %q", body)
	}
	if !strings.Contains(body, "completed successfully") {
		t.Errorf("expected body to contain detail, got %q", body)
	}
	if !strings.Contains(body, "success") {
		t.Errorf("expected body to contain success badge, got %q", body)
	}
}

func TestNotFound(t *testing.T) {
	router, err := dashboard.NewRouter(nil)
	if err != nil {
		t.Fatalf("failed to create router: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/unknown-path", nil)
	rec := httptest.NewRecorder()

	router.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Errorf("expected status %d, got %d", http.StatusNotFound, rec.Code)
	}
}
