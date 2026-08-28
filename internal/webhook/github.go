package webhook

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// GitHubProvider implements the Provider interface for GitHub webhooks.
type GitHubProvider struct{}

// NewGitHubProvider creates a new GitHubProvider.
func NewGitHubProvider() *GitHubProvider {
	return &GitHubProvider{}
}

// Name returns the provider identifier "github".
func (g *GitHubProvider) Name() string {
	return "github"
}

// CanHandle returns true if the request contains GitHub event headers.
func (g *GitHubProvider) CanHandle(h http.Header) bool {
	return h.Get("X-GitHub-Event") != "" || h.Get("X-Hub-Signature-256") != ""
}

// VerifySignature validates the X-Hub-Signature-256 header using HMAC-SHA256.
func (g *GitHubProvider) VerifySignature(secret string, h http.Header, body []byte) error {
	sigHeader := h.Get("X-Hub-Signature-256")
	if sigHeader == "" {
		return errors.New("missing X-Hub-Signature-256 header")
	}

	sigHex := strings.TrimPrefix(sigHeader, "sha256=")
	if sigHex == sigHeader {
		return errors.New("invalid X-Hub-Signature-256 format: missing sha256= prefix")
	}

	sigBytes, err := hex.DecodeString(sigHex)
	if err != nil {
		return fmt.Errorf("invalid hex in X-Hub-Signature-256 header: %w", err)
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	expectedMAC := mac.Sum(nil)

	if !hmac.Equal(sigBytes, expectedMAC) {
		return errors.New("signature mismatch")
	}

	return nil
}

// githubPayload matches the relevant subset of GitHub push event webhook JSON payload.
type githubPayload struct {
	Ref        string `json:"ref"`
	Before     string `json:"before"`
	After      string `json:"after"`
	Repository struct {
		Name     string `json:"name"`
		FullName string `json:"full_name"`
		Private  bool   `json:"private"`
		HTMLURL  string `json:"html_url"`
		SSHURL   string `json:"ssh_url"`
		CloneURL string `json:"clone_url"`
		Owner    struct {
			Name  string `json:"name"`
			Login string `json:"login"`
		} `json:"owner"`
	} `json:"repository"`
}

// Parse extracts RepoInfo from a GitHub push event payload.
func (g *GitHubProvider) Parse(body []byte) (*RepoInfo, error) {
	var payload githubPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal github payload: %w", err)
	}

	if payload.Repository.FullName == "" && payload.Repository.Name == "" {
		return nil, errors.New("missing repository in github payload")
	}

	owner := payload.Repository.Owner.Login
	if owner == "" {
		owner = payload.Repository.Owner.Name
	}
	if owner == "" && strings.Contains(payload.Repository.FullName, "/") {
		parts := strings.SplitN(payload.Repository.FullName, "/", 2)
		owner = parts[0]
	}

	repoName := payload.Repository.Name
	if repoName == "" && strings.Contains(payload.Repository.FullName, "/") {
		parts := strings.SplitN(payload.Repository.FullName, "/", 2)
		repoName = parts[1]
	}

	fullName := payload.Repository.FullName
	if fullName == "" && owner != "" && repoName != "" {
		fullName = owner + "/" + repoName
	}

	return &RepoInfo{
		Provider: "github",
		FullName: fullName,
		Owner:    owner,
		Name:     repoName,
		Private:  payload.Repository.Private,
		HTMLURL:  payload.Repository.HTMLURL,
		SSHURL:   payload.Repository.SSHURL,
		CloneURL: payload.Repository.CloneURL,
		Ref:      payload.Ref,
		Before:   payload.Before,
		After:    payload.After,
	}, nil
}
