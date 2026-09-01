package events_test

import (
	"testing"
	"time"

	"forgejo-sync/internal/events"
)

func TestHubPublishSubscribe(t *testing.T) {
	hub := events.NewHub()
	ch, unsubscribe := hub.Subscribe()
	defer unsubscribe()

	if count := hub.SubscriberCount(); count != 1 {
		t.Fatalf("expected 1 subscriber, got %d", count)
	}

	payload := events.SyncEventPayload{
		Type:     events.TypeSyncStarted,
		EventID:  10,
		RepoID:   1,
		RepoName: "test-repo",
		Status:   "running",
	}

	hub.Publish(payload)

	select {
	case received := <-ch:
		if received.RepoID != 1 || received.Status != "running" || received.Type != events.TypeSyncStarted {
			t.Fatalf("unexpected payload received: %+v", received)
		}
		if received.Timestamp.IsZero() {
			t.Fatal("expected timestamp to be set")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("timed out waiting for event")
	}

	unsubscribe()
	if count := hub.SubscriberCount(); count != 0 {
		t.Fatalf("expected 0 subscribers after unsubscribe, got %d", count)
	}
}
