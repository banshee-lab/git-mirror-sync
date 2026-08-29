package giturl_test

import (
	"errors"
	"strings"
	"testing"

	"forgejo-sync/internal/giturl"
)

func TestNormalizeToSSH(t *testing.T) {
	tests := []struct {
		name    string
		url     string
		sshUser string
		want    string
		wantErr bool
	}{
		// SSH shorthand: git@host:owner/repo[.git]
		{
			name:    "ssh shorthand without .git",
			url:     "git@github.com:octocat/Hello-World",
			sshUser: "",
			want:    "ssh://git@github.com/octocat/Hello-World.git",
		},
		{
			name:    "ssh shorthand with .git",
			url:     "git@github.com:octocat/Hello-World.git",
			sshUser: "",
			want:    "ssh://git@github.com/octocat/Hello-World.git",
		},
		{
			name:    "ssh shorthand custom user",
			url:     "deploy@github.com:owner/repo",
			sshUser: "deploy",
			want:    "ssh://deploy@github.com/owner/repo.git",
		},
		{
			name:    "ssh shorthand ignores sshUser if already in url",
			url:     "deploy@github.com:owner/repo",
			sshUser: "git",
			want:    "ssh://git@github.com/owner/repo.git",
		},
		// HTTPS URLs
		{
			name:    "https url without .git",
			url:     "https://github.com/octocat/Hello-World",
			sshUser: "",
			want:    "ssh://git@github.com/octocat/Hello-World.git",
		},
		{
			name:    "https url with .git",
			url:     "https://github.com/octocat/Hello-World.git",
			sshUser: "",
			want:    "ssh://git@github.com/octocat/Hello-World.git",
		},
		{
			name:    "https url with credentials (embedded)",
			url:     "https://user:pass@github.com/owner/repo.git",
			sshUser: "",
			want:    "ssh://git@github.com/owner/repo.git",
		},
		{
			name:    "https url with non-standard port",
			url:     "https://git.example.com:8443/owner/repo",
			sshUser: "",
			want:    "ssh://git@git.example.com:8443/owner/repo.git",
		},
		// HTTP URLs
		{
			name:    "http url",
			url:     "http://git.example.com/owner/repo",
			sshUser: "",
			want:    "ssh://git@git.example.com/owner/repo.git",
		},
		// SSH:// URLs
		{
			name:    "ssh scheme url",
			url:     "ssh://git@github.com/owner/repo",
			sshUser: "",
			want:    "ssh://git@github.com/owner/repo.git",
		},
		{
			name:    "ssh scheme with non-standard port",
			url:     "ssh://git@github.com:2222/owner/repo",
			sshUser: "",
			want:    "ssh://git@github.com:2222/owner/repo.git",
		},
		{
			name:    "ssh scheme custom user in url",
			url:     "ssh://deploy@github.com/owner/repo",
			sshUser: "git",
			want:    "ssh://deploy@github.com/owner/repo.git",
		},
		// Git:// URLs
		{
			name:    "git scheme url",
			url:     "git://git.example.com/owner/repo",
			sshUser: "",
			want:    "ssh://git@git.example.com/owner/repo.git",
		},
		// GitLab nested groups
		{
			name:    "gitlab nested groups",
			url:     "https://gitlab.com/group/subgroup/project",
			sshUser: "",
			want:    "ssh://git@gitlab.com/group/subgroup/project.git",
		},
		{
			name:    "gitlab deeply nested",
			url:     "git@gitlab.com:group/sub1/sub2/project",
			sshUser: "",
			want:    "ssh://git@gitlab.com/group/sub1/sub2/project.git",
		},
		// Invalid inputs
		{
			name:    "empty url",
			url:     "",
			sshUser: "",
			wantErr: true,
		},
		{
			name:    "invalid ssh shorthand missing colon",
			url:     "git@github.com/owner/repo",
			sshUser: "",
			wantErr: true,
		},
		{
			name:    "invalid url scheme",
			url:     "ftp://example.com/owner/repo",
			sshUser: "",
			wantErr: true,
		},
		{
			name:    "https url without path",
			url:     "https://github.com",
			sshUser: "",
			wantErr: true,
		},
		{
			name:    "ssh shorthand host empty",
			url:     "git@:owner/repo",
			sshUser: "",
			wantErr: true,
		},
		{
			name:    "ssh shorthand path empty",
			url:     "git@github.com:",
			sshUser: "",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := giturl.NormalizeToSSH(tt.url, tt.sshUser)
			if (err != nil) != tt.wantErr {
				t.Errorf("NormalizeToSSH() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if err != nil && !errors.Is(err, giturl.ErrInvalidRepoURL) {
				t.Errorf("NormalizeToSSH() error type = %T, want ErrInvalidRepoURL", err)
			}
			if got != tt.want {
				t.Errorf("NormalizeToSSH() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestValidateOwnerAndRepo(t *testing.T) {
	tests := []struct {
		owner   string
		repo    string
		want    bool
		name    string
	}{
		// Valid cases
		{"octocat", "Hello-World", true, "valid with hyphen"},
		{"owner", "repo", true, "simple valid"},
		{"owner_name", "repo_name", true, "valid with underscore"},
		{"owner.name", "repo.name", true, "valid with dot"},
		{"123owner", "456repo", true, "starting with digit"},
		{"o", "r", true, "single character"},
		{"a1b2c3d4e5f6g7h8i9j0", "z9y8x7w6v5u4t3s2r1q0", true, "long valid names"},

		// Invalid cases
		{"", "repo", false, "empty owner"},
		{"owner", "", false, "empty repo"},
		{"", "", false, "both empty"},
		{"own@r", "repo", false, "owner has @"},
		{"owner", "rep#o", false, "repo has #"},
		{"own/er", "repo", false, "owner has /"},
		{"owner", "rep/o", false, "repo has /"},
		{"own er", "repo", false, "owner has space"},
		{"owner", "re po", false, "repo has space"},
		{".owner", "repo", false, "owner starts with dot"},
		{"owner", ".repo", false, "repo starts with dot"},
		{"-owner", "repo", false, "owner starts with hyphen"},
		{"owner", "-repo", false, "repo starts with hyphen"},
		{"..owner", "repo", false, "owner has multiple dots"},
		{"owner", "repo..name", true, "consecutive dots allowed"},
		{strings.Repeat("a", 256), "repo", false, "owner exceeds 255 chars"},
		{"owner", strings.Repeat("b", 256), false, "repo exceeds 255 chars"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := giturl.ValidateOwnerAndRepo(tt.owner, tt.repo)
			if got != tt.want {
				t.Errorf("ValidateOwnerAndRepo(%q, %q) = %v, want %v", tt.owner, tt.repo, got, tt.want)
			}
		})
	}
}

func TestNormalizeToSSHErrorWrapping(t *testing.T) {
	_, err := giturl.NormalizeToSSH("invalid://url", "")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, giturl.ErrInvalidRepoURL) {
		t.Errorf("expected ErrInvalidRepoURL, got %v", err)
	}
}
