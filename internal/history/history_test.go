package history

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func TestStore_MemoryAndLifecycle(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(":memory:")
	if err != nil {
		t.Fatalf("failed to create in-memory store: %v", err)
	}
	defer store.Close()

	// Insert event
	now := time.Now().UTC().Truncate(time.Millisecond)
	evt := SyncEvent{
		ReceivedAt:   now,
		Provider:     "github",
		RepoFullName: "org/repo",
		CloneURL:     "https://github.com/org/repo.git",
		SSHURL:       "git@github.com:org/repo.git",
		ForgejoOwner: "org",
		ForgejoRepo:  "repo",
		Ref:          "refs/heads/main",
		CommitBefore: "1111111111111111111111111111111111111111",
		CommitAfter:  "2222222222222222222222222222222222222222",
		Status:       StatusReceived,
		Detail:       "queued for processing",
	}

	id, err := store.Insert(ctx, evt)
	if err != nil {
		t.Fatalf("failed to insert event: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}

	// UpdateStatus
	err = store.UpdateStatus(ctx, id, StatusSkipped, "skipped mirror")
	if err != nil {
		t.Fatalf("failed to update status: %v", err)
	}

	// Finish
	finishedAt := now.Add(250 * time.Millisecond)
	err = store.Finish(ctx, id, StatusSuccess, "synced successfully", "3333333333333333333333333333333333333333", finishedAt)
	if err != nil {
		t.Fatalf("failed to finish event: %v", err)
	}

	// Recent
	recent, err := store.Recent(ctx, 10)
	if err != nil {
		t.Fatalf("failed to fetch recent events: %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("expected 1 recent event, got %d", len(recent))
	}

	got := recent[0]
	if got.ID != id {
		t.Errorf("expected ID %d, got %d", id, got.ID)
	}
	if got.Status != StatusSuccess {
		t.Errorf("expected status %s, got %s", StatusSuccess, got.Status)
	}
	if got.Detail != "synced successfully" {
		t.Errorf("expected detail 'synced successfully', got %q", got.Detail)
	}
	if got.CommitAfter != "3333333333333333333333333333333333333333" {
		t.Errorf("expected commit_after updated, got %q", got.CommitAfter)
	}
	if got.DurationMS == nil || *got.DurationMS != 250 {
		t.Errorf("expected duration_ms 250, got %v", got.DurationMS)
	}
	if got.FinishedAt == nil || !got.FinishedAt.Equal(finishedAt) {
		t.Errorf("expected finished_at %v, got %v", finishedAt, got.FinishedAt)
	}
}

func TestStore_RecentOrderingAndLimit(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "history_test.db")

	store, err := Open(dbPath)
	if err != nil {
		t.Fatalf("failed to open store at %s: %v", dbPath, err)
	}
	defer store.Close()

	baseTime := time.Date(2026, 1, 1, 10, 0, 0, 0, time.UTC)

	for i := 1; i <= 5; i++ {
		evt := SyncEvent{
			ReceivedAt:   baseTime.Add(time.Duration(i) * time.Hour),
			Provider:     "gitlab",
			RepoFullName: "group/project",
			Status:       StatusReceived,
			Detail:       "test event",
		}
		_, err := store.Insert(ctx, evt)
		if err != nil {
			t.Fatalf("failed to insert event %d: %v", i, err)
		}
	}

	// Query with limit 3
	recent, err := store.Recent(ctx, 3)
	if err != nil {
		t.Fatalf("failed to query recent: %v", err)
	}
	if len(recent) != 3 {
		t.Fatalf("expected 3 events, got %d", len(recent))
	}

	// Should be newest first: i=5, 4, 3
	expectedTimes := []time.Time{
		baseTime.Add(5 * time.Hour),
		baseTime.Add(4 * time.Hour),
		baseTime.Add(3 * time.Hour),
	}

	for i, evt := range recent {
		if !evt.ReceivedAt.Equal(expectedTimes[i]) {
			t.Errorf("event %d: expected received_at %v, got %v", i, expectedTimes[i], evt.ReceivedAt)
		}
	}
}

func TestStore_Prune(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(":memory:")
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	now := time.Now().UTC()

	// 1 old event (40 days old)
	oldEvt := SyncEvent{
		ReceivedAt:   now.Add(-40 * 24 * time.Hour),
		Provider:     "github",
		RepoFullName: "old/repo",
		Status:       StatusSuccess,
	}
	_, err = store.Insert(ctx, oldEvt)
	if err != nil {
		t.Fatalf("failed to insert old event: %v", err)
	}

	// 1 recent event (10 days old)
	recentEvt := SyncEvent{
		ReceivedAt:   now.Add(-10 * 24 * time.Hour),
		Provider:     "github",
		RepoFullName: "recent/repo",
		Status:       StatusSuccess,
	}
	recentID, err := store.Insert(ctx, recentEvt)
	if err != nil {
		t.Fatalf("failed to insert recent event: %v", err)
	}

	// Prune older than 30 days
	deleted, err := store.Prune(ctx, 30*24*time.Hour)
	if err != nil {
		t.Fatalf("failed to prune: %v", err)
	}
	if deleted != 1 {
		t.Fatalf("expected 1 row pruned, got %d", deleted)
	}

	// Verify remaining events
	recent, err := store.Recent(ctx, 10)
	if err != nil {
		t.Fatalf("failed to fetch recent: %v", err)
	}
	if len(recent) != 1 {
		t.Fatalf("expected 1 event remaining, got %d", len(recent))
	}
	if recent[0].ID != recentID {
		t.Errorf("expected remaining event ID %d, got %d", recentID, recent[0].ID)
	}
}

func TestStore_NoCredentialsInModel(t *testing.T) {
	evtType := reflect.TypeOf(SyncEvent{})
	forbiddenFields := []string{"PrivateKey", "Password", "Secret", "Token", "Credentials", "Key"}

	for i := 0; i < evtType.NumField(); i++ {
		fieldName := evtType.Field(i).Name
		for _, forbidden := range forbiddenFields {
			if fieldName == forbidden {
				t.Errorf("SyncEvent model contains forbidden credential field: %s", fieldName)
			}
		}
	}
}

func TestStore_StatusValidation(t *testing.T) {
	ctx := context.Background()
	store, err := NewStore(":memory:")
	if err != nil {
		t.Fatalf("failed to create store: %v", err)
	}
	defer store.Close()

	invalidEvt := SyncEvent{
		Provider:     "github",
		RepoFullName: "test/repo",
		Status:       Status("invalid_status"),
	}

	_, err = store.Insert(ctx, invalidEvt)
	if err == nil {
		t.Errorf("expected error on invalid status insertion, got nil")
	}

	validEvt := SyncEvent{
		Provider:     "github",
		RepoFullName: "test/repo",
		Status:       StatusReceived,
	}
	id, err := store.Insert(ctx, validEvt)
	if err != nil {
		t.Fatalf("failed to insert valid event: %v", err)
	}

	err = store.UpdateStatus(ctx, id, Status("invalid_status"), "detail")
	if err == nil {
		t.Errorf("expected error on invalid status update, got nil")
	}

	err = store.Finish(ctx, id, Status("invalid_status"), "detail", "", time.Now())
	if err == nil {
		t.Errorf("expected error on invalid status finish, got nil")
	}
}
