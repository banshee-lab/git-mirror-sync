package webapp_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"forgejo-sync/internal/webapp"
)

func TestWebappHandler(t *testing.T) {
	handler, err := webapp.NewHandler()
	if err != nil {
		t.Fatalf("NewHandler() failed: %v", err)
	}

	t.Run("GET / returns html", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 OK, got %d", rec.Code)
		}
		if contentType := rec.Header().Get("Content-Type"); contentType == "" {
			t.Errorf("expected Content-Type header to be set")
		}
	})

	t.Run("GET /repositories/123 fallback to index html", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodGet, "/repositories/123", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Errorf("expected 200 OK for SPA client route, got %d", rec.Code)
		}
	})

	t.Run("POST / rejected method", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/", nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusMethodNotAllowed {
			t.Errorf("expected 405 Method Not Allowed, got %d", rec.Code)
		}
	})
}
