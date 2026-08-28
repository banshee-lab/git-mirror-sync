package webhook_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"forgejo-sync/internal/history"
	"forgejo-sync/internal/webhook"
)

type mockHistoryWriter struct {
	mu     sync.Mutex
	events []history.SyncEvent
	lastID int64
}

func (m *mockHistoryWriter) Insert(ctx context.Context, evt history.SyncEvent) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastID++
	evt.ID = m.lastID
	m.events = append(m.events, evt)
	return evt.ID, nil
}

type mockJobEnqueuer struct {
	mu   sync.Mutex
	jobs []struct {
		Repo    webhook.RepoInfo
		EventID int64
	}
}

func (m *mockJobEnqueuer) Enqueue(ctx context.Context, repo webhook.RepoInfo, eventID int64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.jobs = append(m.jobs, struct {
		Repo    webhook.RepoInfo
		EventID int64
	}{Repo: repo, EventID: eventID})
	return nil
}

func computeGitHubSig(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

const (
	testGitHubSecret = "super-secret-gh-key"
	testGitLabSecret = "super-secret-gl-token"
)

const sampleGitHubPayload = `{
  "ref": "refs/heads/main",
  "before": "0000000000000000000000000000000000000000",
  "after": "6dcb09b5b57875f334f61aebed695e2e4193db5e",
  "repository": {
    "name": "hello-world",
    "full_name": "octocat/hello-world",
    "private": false,
    "html_url": "https://github.com/octocat/hello-world",
    "clone_url": "https://github.com/octocat/hello-world.git",
    "ssh_url": "git@github.com:octocat/hello-world.git",
    "owner": {
      "name": "octocat",
      "login": "octocat"
    }
  }
}`

const sampleGitLabPayload = `{
  "ref": "refs/heads/main",
  "before": "0000000000000000000000000000000000000000",
  "after": "da1560886d4f094c3e6c9ef4034937d38b5d27d7",
  "project": {
    "name": "diaspora",
    "path_with_namespace": "mike/diaspora",
    "namespace": "mike",
    "web_url": "http://example.com/mike/diaspora",
    "git_http_url": "http://example.com/mike/diaspora.git",
    "git_ssh_url": "git@example.com:mike/diaspora.git",
    "visibility": "private"
  }
}`

func setupTestServer() (*webhook.Server, *mockHistoryWriter, *mockJobEnqueuer) {
	hist := &mockHistoryWriter{}
	enq := &mockJobEnqueuer{}
	srv := webhook.NewServer(webhook.ServerOptions{
		History:      hist,
		Enqueuer:     enq,
		GitHubSecret: testGitHubSecret,
		GitLabSecret: testGitLabSecret,
		MaxBodyBytes: 1024 * 1024, // 1MB for test
	})
	return srv, hist, enq
}

func TestGitHubWebhookSuccess(t *testing.T) {
	srv, hist, enq := setupTestServer()

	body := []byte(sampleGitHubPayload)
	sig := computeGitHubSig(testGitHubSecret, body)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", sig)

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected status 202 Accepted, got %d: %s", w.Code, w.Body.String())
	}

	if len(hist.events) != 1 {
		t.Fatalf("expected 1 history event recorded, got %d", len(hist.events))
	}
	evt := hist.events[0]
	if evt.Provider != "github" {
		t.Errorf("expected provider github, got %s", evt.Provider)
	}
	if evt.RepoFullName != "octocat/hello-world" {
		t.Errorf("expected repo octocat/hello-world, got %s", evt.RepoFullName)
	}
	if evt.CloneURL != "https://github.com/octocat/hello-world.git" {
		t.Errorf("expected clone url https://github.com/octocat/hello-world.git, got %s", evt.CloneURL)
	}
	if evt.SSHURL != "git@github.com:octocat/hello-world.git" {
		t.Errorf("expected ssh url git@github.com:octocat/hello-world.git, got %s", evt.SSHURL)
	}
	if evt.Ref != "refs/heads/main" {
		t.Errorf("expected ref refs/heads/main, got %s", evt.Ref)
	}
	if evt.CommitAfter != "6dcb09b5b57875f334f61aebed695e2e4193db5e" {
		t.Errorf("expected commit after 6dcb09..., got %s", evt.CommitAfter)
	}
	if evt.Status != history.StatusReceived {
		t.Errorf("expected status received, got %s", evt.Status)
	}

	if len(enq.jobs) != 1 {
		t.Fatalf("expected 1 enqueued job, got %d", len(enq.jobs))
	}
	job := enq.jobs[0]
	if job.Repo.FullName != "octocat/hello-world" {
		t.Errorf("expected enqueued repo octocat/hello-world, got %s", job.Repo.FullName)
	}
	if job.Repo.Owner != "octocat" {
		t.Errorf("expected enqueued repo owner octocat, got %s", job.Repo.Owner)
	}
	if job.Repo.Name != "hello-world" {
		t.Errorf("expected enqueued repo name hello-world, got %s", job.Repo.Name)
	}
	if job.EventID != evt.ID {
		t.Errorf("expected enqueued event ID %d, got %d", evt.ID, job.EventID)
	}
}

func TestGitHubWebhookInvalidSignature(t *testing.T) {
	srv, hist, enq := setupTestServer()

	body := []byte(sampleGitHubPayload)
	sig := computeGitHubSig("wrong-secret", body)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", sig)

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401 Unauthorized, got %d", w.Code)
	}

	if len(hist.events) != 0 {
		t.Errorf("expected 0 history events on auth failure, got %d", len(hist.events))
	}
	if len(enq.jobs) != 0 {
		t.Errorf("expected 0 jobs on auth failure, got %d", len(enq.jobs))
	}
}

func TestGitLabWebhookSuccess(t *testing.T) {
	srv, hist, enq := setupTestServer()

	body := []byte(sampleGitLabPayload)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Gitlab-Event", "Push Hook")
	req.Header.Set("X-Gitlab-Token", testGitLabSecret)

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusAccepted {
		t.Fatalf("expected status 202 Accepted, got %d: %s", w.Code, w.Body.String())
	}

	if len(hist.events) != 1 {
		t.Fatalf("expected 1 history event recorded, got %d", len(hist.events))
	}
	evt := hist.events[0]
	if evt.Provider != "gitlab" {
		t.Errorf("expected provider gitlab, got %s", evt.Provider)
	}
	if evt.RepoFullName != "mike/diaspora" {
		t.Errorf("expected repo mike/diaspora, got %s", evt.RepoFullName)
	}
	if evt.CloneURL != "http://example.com/mike/diaspora.git" {
		t.Errorf("expected clone url http://example.com/mike/diaspora.git, got %s", evt.CloneURL)
	}
	if evt.SSHURL != "git@example.com:mike/diaspora.git" {
		t.Errorf("expected ssh url git@example.com:mike/diaspora.git, got %s", evt.SSHURL)
	}
	if evt.Ref != "refs/heads/main" {
		t.Errorf("expected ref refs/heads/main, got %s", evt.Ref)
	}
	if evt.Status != history.StatusReceived {
		t.Errorf("expected status received, got %s", evt.Status)
	}

	if len(enq.jobs) != 1 {
		t.Fatalf("expected 1 enqueued job, got %d", len(enq.jobs))
	}
	job := enq.jobs[0]
	if job.Repo.FullName != "mike/diaspora" {
		t.Errorf("expected enqueued repo mike/diaspora, got %s", job.Repo.FullName)
	}
	if job.Repo.Owner != "mike" {
		t.Errorf("expected enqueued repo owner mike, got %s", job.Repo.Owner)
	}
	if job.Repo.Name != "diaspora" {
		t.Errorf("expected enqueued repo name diaspora, got %s", job.Repo.Name)
	}
	if !job.Repo.Private {
		t.Errorf("expected repo to be private")
	}
}

func TestGitLabWebhookInvalidToken(t *testing.T) {
	srv, hist, enq := setupTestServer()

	body := []byte(sampleGitLabPayload)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-Gitlab-Event", "Push Hook")
	req.Header.Set("X-Gitlab-Token", "wrong-token")

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected status 401 Unauthorized, got %d", w.Code)
	}

	if len(hist.events) != 0 {
		t.Errorf("expected 0 history events on auth failure, got %d", len(hist.events))
	}
	if len(enq.jobs) != 0 {
		t.Errorf("expected 0 jobs on auth failure, got %d", len(enq.jobs))
	}
}

func TestOversizedPayloadRejected(t *testing.T) {
	hist := &mockHistoryWriter{}
	enq := &mockJobEnqueuer{}
	srv := webhook.NewServer(webhook.ServerOptions{
		History:      hist,
		Enqueuer:     enq,
		GitHubSecret: testGitHubSecret,
		MaxBodyBytes: 100, // Small limit for testing
	})

	largeBody := bytes.Repeat([]byte("a"), 200)
	sig := computeGitHubSig(testGitHubSecret, largeBody)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(largeBody))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", sig)

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("expected status 413 StatusRequestEntityTooLarge, got %d", w.Code)
	}
	if len(hist.events) != 0 {
		t.Errorf("expected 0 history events on rejected body, got %d", len(hist.events))
	}
}

func TestUnknownProviderRejected(t *testing.T) {
	srv, hist, _ := setupTestServer()

	body := []byte(`{"hello": "world"}`)
	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request, got %d", w.Code)
	}
	if len(hist.events) != 0 {
		t.Errorf("expected 0 history events on unknown provider, got %d", len(hist.events))
	}
}

func TestHealthzEndpoint(t *testing.T) {
	srv, _, _ := setupTestServer()

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected status 200 OK, got %d", w.Code)
	}
	if w.Body.String() != "ok" {
		t.Errorf("expected body 'ok', got %q", w.Body.String())
	}
}

func TestMalformedPayload(t *testing.T) {
	srv, hist, enq := setupTestServer()

	body := []byte(`{"ref": "refs/heads/main", "before"`) // Malformed JSON
	sig := computeGitHubSig(testGitHubSecret, body)

	req := httptest.NewRequest(http.MethodPost, "/webhook", bytes.NewReader(body))
	req.Header.Set("X-GitHub-Event", "push")
	req.Header.Set("X-Hub-Signature-256", sig)

	w := httptest.NewRecorder()
	srv.ServeHTTP(w, req)

	if w.Code != http.StatusBadRequest {
		t.Fatalf("expected status 400 Bad Request, got %d", w.Code)
	}
	if len(hist.events) != 0 {
		t.Errorf("expected 0 history events on malformed body, got %d", len(hist.events))
	}
	if len(enq.jobs) != 0 {
		t.Errorf("expected 0 jobs on malformed body, got %d", len(enq.jobs))
	}
}
