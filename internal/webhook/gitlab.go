package webhook

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
)

// GitLabProvider implements the Provider interface for GitLab webhooks.
type GitLabProvider struct{}

// NewGitLabProvider creates a new GitLabProvider.
func NewGitLabProvider() *GitLabProvider {
	return &GitLabProvider{}
}

// Name returns the provider identifier "gitlab".
func (g *GitLabProvider) Name() string {
	return "gitlab"
}

// CanHandle returns true if the request contains GitLab event or token headers.
func (g *GitLabProvider) CanHandle(h http.Header) bool {
	return h.Get("X-Gitlab-Event") != "" || h.Get("X-Gitlab-Token") != ""
}

// VerifySignature validates the X-Gitlab-Token header using constant-time comparison.
func (g *GitLabProvider) VerifySignature(secret string, h http.Header, body []byte) error {
	token := h.Get("X-Gitlab-Token")
	if token == "" {
		return errors.New("missing X-Gitlab-Token header")
	}

	if subtle.ConstantTimeCompare([]byte(token), []byte(secret)) != 1 {
		return errors.New("token mismatch")
	}

	return nil
}

// gitlabPayload matches the relevant subset of GitLab push hook webhook JSON payload.
type gitlabPayload struct {
	Ref        string `json:"ref"`
	Before     string `json:"before"`
	After      string `json:"after"`
	Project    struct {
		Name              string `json:"name"`
		PathWithNamespace string `json:"path_with_namespace"`
		Namespace         string `json:"namespace"`
		WebURL            string `json:"web_url"`
		GitHTTPURL        string `json:"git_http_url"`
		Visibility        string `json:"visibility_level"`
	} `json:"project"`
}

// Parse extracts RepoInfo from a GitLab push hook payload.
func (g *GitLabProvider) Parse(body []byte) (*RepoInfo, error) {
	var payload gitlabPayload
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, fmt.Errorf("unmarshal gitlab payload: %w", err)
	}

	if payload.Project.PathWithNamespace == "" && payload.Project.Name == "" {
		return nil, errors.New("missing project in gitlab payload")
	}

	fullName := payload.Project.PathWithNamespace
	owner := payload.Project.Namespace
	repoName := payload.Project.Name

	if owner == "" && strings.Contains(fullName, "/") {
		parts := strings.Split(fullName, "/")
		owner = strings.Join(parts[:len(parts)-1], "/")
		if repoName == "" {
			repoName = parts[len(parts)-1]
		}
	}

	if fullName == "" && owner != "" && repoName != "" {
		fullName = owner + "/" + repoName
	}

	private := payload.Project.Visibility == "0" || payload.Project.Visibility == "10"

		// Convert "https:" to "ssh:" in the GitHTTPURL field
  sshURL := strings.Replace(payload.Project.GitHTTPURL, "https:", "ssh:", 1)

	return &RepoInfo{
		Provider: "gitlab",
		FullName: fullName,
		Owner:    owner,
		Name:     repoName,
		Private:  private,
		HTMLURL:  payload.Project.WebURL,
		SSHURL:   sshURL,
		CloneURL: payload.Project.GitHTTPURL,
		Ref:      payload.Ref,
		Before:   payload.Before,
		After:    payload.After,
	}, nil
}
