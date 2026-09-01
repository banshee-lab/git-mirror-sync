package events

import (
	"sync"
	"time"
)

// EventType represents the type of a real-time event.
type EventType string

const (
	TypeSyncStarted   EventType = "sync_started"
	TypeSyncCompleted EventType = "sync_completed"
	TypeSyncFailed    EventType = "sync_failed"
)

// SyncEventPayload represents the JSON payload sent via Server-Sent Events.
type SyncEventPayload struct {
	Type       EventType `json:"type"`
	EventID    int64     `json:"event_id"`
	RepoID     int64     `json:"repo_id"`
	RepoName   string    `json:"repo_name"`
	Status     string    `json:"status"` // "running", "success", "error", "conflict", "skipped"
	Detail     string    `json:"detail,omitempty"`
	DurationMS int64     `json:"duration_ms,omitempty"`
	Timestamp  time.Time `json:"timestamp"`
}

// Hub manages SSE subscribers and event broadcasting.
type Hub struct {
	mu          sync.RWMutex
	subscribers map[chan SyncEventPayload]struct{}
}

// NewHub initializes an event hub.
func NewHub() *Hub {
	return &Hub{
		subscribers: make(map[chan SyncEventPayload]struct{}),
	}
}

// Subscribe registers a new subscriber channel and returns an unsubscribe function.
func (h *Hub) Subscribe() (chan SyncEventPayload, func()) {
	ch := make(chan SyncEventPayload, 64)
	h.mu.Lock()
	h.subscribers[ch] = struct{}{}
	h.mu.Unlock()

	unsubscribe := func() {
		h.mu.Lock()
		if _, ok := h.subscribers[ch]; ok {
			delete(h.subscribers, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
	return ch, unsubscribe
}

// Publish broadcasts a payload to all active subscribers.
func (h *Hub) Publish(payload SyncEventPayload) {
	if payload.Timestamp.IsZero() {
		payload.Timestamp = time.Now().UTC()
	}
	h.mu.RLock()
	defer h.mu.RUnlock()
	for ch := range h.subscribers {
		select {
		case ch <- payload:
		default:
			// Buffer full: non-blocking drop to protect slow consumers
		}
	}
}

// SubscriberCount returns the current number of active subscribers.
func (h *Hub) SubscriberCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.subscribers)
}
