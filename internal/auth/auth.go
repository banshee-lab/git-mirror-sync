package auth

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// Config contains the OIDC settings needed for auth flows.
type Config struct {
	Mode          string
	IssuerURL     string
	ClientID      string
	ClientSecret  string
	RedirectURL   string
	Scopes        []string
	ProviderName  string
	SessionSecret string
	SessionCookie string
	SessionTTL    time.Duration
	Logger        *slog.Logger
}

// User represents the authenticated principal.
type User struct {
	Subject string `json:"sub"`
	Email   string `json:"email,omitempty"`
	Name    string `json:"name,omitempty"`
}

type sessionClaims struct {
	Sub   string `json:"sub"`
	Email string `json:"email,omitempty"`
	Name  string `json:"name,omitempty"`
	Exp   int64  `json:"exp"`
}

type contextKey struct{}

// Provider implements the OIDC login and session validation flow.
type Provider struct {
	cfg          Config
	oauth2Config *oauth2.Config
	verifier     *oidc.IDTokenVerifier
	tokenSource  oauth2.TokenSource
	logger       *slog.Logger
	devMode      bool
}

// NewProvider initializes the OIDC provider and verifies the issuer metadata.
func NewProvider(cfg Config) (*Provider, error) {
	if cfg.Logger == nil {
		cfg.Logger = slog.Default()
	}
	if cfg.Scopes == nil || len(cfg.Scopes) == 0 {
		cfg.Scopes = []string{"openid", "profile", "email"}
	}
	if cfg.SessionCookie == "" {
		cfg.SessionCookie = "forgejo_sync_session"
	}
	if cfg.SessionTTL <= 0 {
		cfg.SessionTTL = 24 * time.Hour
	}
	if cfg.ProviderName == "" {
		if strings.EqualFold(cfg.Mode, "development") || strings.EqualFold(cfg.Mode, "dev") {
			cfg.ProviderName = "Development"
		} else {
			cfg.ProviderName = "PocketID"
		}
	}
	if strings.EqualFold(cfg.Mode, "development") || strings.EqualFold(cfg.Mode, "dev") {
		return &Provider{cfg: cfg, logger: cfg.Logger, devMode: true}, nil
	}

	ctx := context.Background()
	issuer, err := oidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("oidc provider: %w", err)
	}

	verifier := issuer.Verifier(&oidc.Config{ClientID: cfg.ClientID})
	provider := &Provider{
		cfg: cfg,
		oauth2Config: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Endpoint:     issuer.Endpoint(),
			Scopes:       cfg.Scopes,
		},
		verifier: verifier,
		logger:   cfg.Logger,
	}

	return provider, nil
}

// Handler returns an http.Handler for the public authentication endpoints.
func (p *Provider) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /auth/config", p.handleConfig)
	mux.HandleFunc("GET /auth/login", p.handleLogin)
	mux.HandleFunc("GET /auth/callback", p.handleCallback)
	mux.HandleFunc("POST /auth/logout", p.handleLogout)
	return mux
}

// RequireAuth validates the signed session cookie and injects the authenticated user.
func RequireAuth(provider *Provider, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, err := provider.UserFromRequest(r)
		if err != nil {
			if provider != nil && provider.devMode {
				user = provider.devUser()
			} else {
				writeJSON(w, http.StatusUnauthorized, map[string]any{
					"error": map[string]any{"code": "unauthorized", "message": "authentication required"},
				})
				return
			}
		}
		ctx := context.WithValue(r.Context(), contextKey{}, user)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// UserFromRequest resolves the request's session cookie into a user.
func (p *Provider) UserFromRequest(r *http.Request) (User, error) {
	if p != nil && p.devMode {
		cookie, err := r.Cookie(p.cfg.SessionCookie)
		if err == nil && cookie.Value != "" {
			return p.parseSession(cookie.Value)
		}
		return p.devUser(), nil
	}
	cookie, err := r.Cookie(p.cfg.SessionCookie)
	if err != nil {
		return User{}, err
	}
	return p.parseSession(cookie.Value)
}

// WithUser returns a new context with the provided User.
func WithUser(ctx context.Context, user User) context.Context {
	return context.WithValue(ctx, contextKey{}, user)
}

// ContextUser returns an authenticated user from the request context.
func ContextUser(ctx context.Context) (User, bool) {
	user, ok := ctx.Value(contextKey{}).(User)
	return user, ok
}

func (p *Provider) handleConfig(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"provider_name": p.cfg.ProviderName,
		"login_url":     "/auth/login",
	})
}

func (p *Provider) devUser() User {
	return User{Subject: "development-user", Email: "dev@example.com", Name: "Development User"}
}

func (p *Provider) setSessionCookie(w http.ResponseWriter, user User) {
	session, err := p.signSession(user)
	if err != nil {
		p.logger.Error("failed to sign user session", slog.Any("error", err))
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}
	setCookie(w, p.cfg.SessionCookie, session, int(p.cfg.SessionTTL.Seconds()), "/")
}

func (p *Provider) handleLogin(w http.ResponseWriter, r *http.Request) {
	if p.devMode {
		p.setSessionCookie(w, p.devUser())
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	state, err := randomToken()
	if err != nil {
		http.Error(w, "failed to create state", http.StatusInternalServerError)
		return
	}
	nonce, err := randomToken()
	if err != nil {
		http.Error(w, "failed to create nonce", http.StatusInternalServerError)
		return
	}

	setCookie(w, p.cfg.SessionCookie, "", -1, "/")
	setCookie(w, "oidc_state", state, 10*60, "/")
	setCookie(w, "oidc_nonce", nonce, 10*60, "/")

	authURL := p.oauth2Config.AuthCodeURL(state, oauth2.SetAuthURLParam("nonce", nonce), oauth2.SetAuthURLParam("prompt", "login"))
	http.Redirect(w, r, authURL, http.StatusFound)
}

func (p *Provider) handleCallback(w http.ResponseWriter, r *http.Request) {
	if p.devMode {
		p.setSessionCookie(w, p.devUser())
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}

	if err := r.ParseForm(); err != nil {
		http.Error(w, "invalid callback", http.StatusBadRequest)
		return
	}
	state := r.FormValue("state")
	code := r.FormValue("code")
	if state == "" || code == "" {
		http.Error(w, "missing oauth state or code", http.StatusBadRequest)
		return
	}
	stateCookie, err := r.Cookie("oidc_state")
	if err != nil || stateCookie.Value != state {
		http.Error(w, "state validation failed", http.StatusBadRequest)
		return
	}
	if _, err := r.Cookie("oidc_nonce"); err != nil {
		http.Error(w, "missing nonce state", http.StatusBadRequest)
		return
	}

	token, err := p.oauth2Config.Exchange(context.Background(), code)
	if err != nil {
		p.logger.Error("oidc token exchange failed", slog.Any("error", err))
		http.Error(w, "token exchange failed", http.StatusUnauthorized)
		return
	}

	idToken, ok := token.Extra("id_token").(string)
	if !ok || idToken == "" {
		http.Error(w, "missing id token", http.StatusUnauthorized)
		return
	}

	verified, err := p.verifier.Verify(context.Background(), idToken)
	if err != nil {
		p.logger.Error("oidc token verification failed", slog.Any("error", err))
		http.Error(w, "token verification failed", http.StatusUnauthorized)
		return
	}

	claims := struct {
		Subject string `json:"sub"`
		Email   string `json:"email"`
		Name    string `json:"name"`
	}{}
	if err := verified.Claims(&claims); err != nil {
		p.logger.Error("failed to decode oidc claims", slog.Any("error", err))
		http.Error(w, "invalid identity claims", http.StatusUnauthorized)
		return
	}

	user := User{Subject: claims.Subject, Email: claims.Email, Name: claims.Name}
	if user.Subject == "" {
		http.Error(w, "missing subject claim", http.StatusUnauthorized)
		return
	}

	session, err := p.signSession(user)
	if err != nil {
		p.logger.Error("failed to sign user session", slog.Any("error", err))
		http.Error(w, "failed to create session", http.StatusInternalServerError)
		return
	}

	setCookie(w, p.cfg.SessionCookie, session, int(p.cfg.SessionTTL.Seconds()), "/")
	setCookie(w, "oidc_state", "", -1, "/")
	setCookie(w, "oidc_nonce", "", -1, "/")
	http.Redirect(w, r, "/", http.StatusFound)
}

func (p *Provider) handleLogout(w http.ResponseWriter, r *http.Request) {
	setCookie(w, p.cfg.SessionCookie, "", -1, "/")
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (p *Provider) signSession(user User) (string, error) {
	claims := sessionClaims{
		Sub:   user.Subject,
		Email: user.Email,
		Name:  user.Name,
		Exp:   time.Now().Add(p.cfg.SessionTTL).Unix(),
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	payloadB64 := base64.RawURLEncoding.EncodeToString(payload)
	sig := hmac.New(sha256.New, []byte(p.cfg.SessionSecret))
	_, _ = sig.Write([]byte(payloadB64))
	signature := base64.RawURLEncoding.EncodeToString(sig.Sum(nil))
	return payloadB64 + "." + signature, nil
}

func (p *Provider) parseSession(raw string) (User, error) {
	parts := strings.Split(raw, ".")
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return User{}, errors.New("session is missing a valid signature")
	}
	payloadB64 := parts[0]
	sig := hmac.New(sha256.New, []byte(p.cfg.SessionSecret))
	_, _ = sig.Write([]byte(payloadB64))
	want := base64.RawURLEncoding.EncodeToString(sig.Sum(nil))
	if !hmac.Equal([]byte(want), []byte(parts[1])) {
		return User{}, errors.New("session signature is invalid")
	}
	payload, err := base64.RawURLEncoding.DecodeString(payloadB64)
	if err != nil {
		return User{}, err
	}
	var claims sessionClaims
	if err := json.Unmarshal(payload, &claims); err != nil {
		return User{}, err
	}
	if claims.Exp <= time.Now().Unix() {
		return User{}, errors.New("session expired")
	}
	return User{Subject: claims.Sub, Email: claims.Email, Name: claims.Name}, nil
}

func setCookie(w http.ResponseWriter, name, value string, maxAge int, path string) {
	http.SetCookie(w, &http.Cookie{
		Name:     name,
		Value:    value,
		Path:     path,
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
		Secure:   false,
	})
}

func randomToken() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
