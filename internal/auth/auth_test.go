package auth

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProvider_DevelopmentModeAllowsAutoUser(t *testing.T) {
	provider, err := NewProvider(Config{
		Mode:          "development",
		SessionCookie: "forgejo_sync_session",
		SessionSecret: "development-session-secret-change-me",
		SessionTTL:    24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("unexpected init error: %v", err)
	}

	user, err := provider.UserFromRequest(httptest.NewRequest(http.MethodGet, "/", nil))
	if err != nil {
		t.Fatalf("expected development fallback user, got error: %v", err)
	}
	if user.Subject != "development-user" {
		t.Fatalf("expected development-user subject, got %q", user.Subject)
	}
}

func TestProvider_DevelopmentModeLoginSetsSession(t *testing.T) {
	provider, err := NewProvider(Config{
		Mode:          "development",
		SessionCookie: "forgejo_sync_session",
		SessionSecret: "development-session-secret-change-me",
		SessionTTL:    24 * time.Hour,
	})
	if err != nil {
		t.Fatalf("unexpected init error: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/auth/login", nil)
	res := httptest.NewRecorder()
	provider.Handler().ServeHTTP(res, req)

	if res.Code != http.StatusFound {
		t.Fatalf("expected redirect status 302, got %d", res.Code)
	}
	cookies := res.Result().Cookies()
	if len(cookies) == 0 {
		t.Fatal("expected a session cookie to be set")
	}
	if cookies[0].Name != "forgejo_sync_session" {
		t.Fatalf("expected session cookie name to match, got %q", cookies[0].Name)
	}
	if cookies[0].Value == "" {
		t.Fatal("expected session cookie value to be populated")
	}
}
