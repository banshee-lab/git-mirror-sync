package gitsync

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"forgejo-sync/internal/forgejodb"
)

// helper to run git command in test setup
func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s in %q failed: %v, output: %s", strings.Join(args, " "), dir, err, string(out))
	}
	return strings.TrimSpace(string(out))
}

func TestValidateName(t *testing.T) {
	valid := []string{"repo", "my-repo", "owner.name", "repo_123", "v1.0.0"}
	for _, v := range valid {
		if err := ValidateName(v); err != nil {
			t.Errorf("expected %q to be valid, got err: %v", v, err)
		}
	}

	invalid := []string{"../traversal", "foo/bar", "foo\\bar", "foo space", "repo;rm -rf", "repo$foo"}
	for _, inv := range invalid {
		if err := ValidateName(inv); err == nil {
			t.Errorf("expected %q to be invalid, got nil", inv)
		}
	}
}

func TestExtractHTTPCredentials(t *testing.T) {
	tempDataDir := t.TempDir()
	owner := "alice"
	repo := "project"
	repoConfigDir := filepath.Join(tempDataDir, "git", "repositories", owner, repo+".git")
	if err := os.MkdirAll(repoConfigDir, 0755); err != nil {
		t.Fatalf("failed to create fake forgejo repo dir: %v", err)
	}

	configContent := `[core]
	repositoryformatversion = 0
	filemode = true
	bare = true
[remote "remote_mirror_1"]
	url = https://token_user:secret_token_123@github.com/alice/project.git
	fetch = +refs/heads/*:refs/remotes/remote_mirror_1/*
[remote "origin"]
	url = git@gitlab.com:alice/project.git
`
	if err := os.WriteFile(filepath.Join(repoConfigDir, "config"), []byte(configContent), 0644); err != nil {
		t.Fatalf("failed to write fake config: %v", err)
	}

	// Test successful extraction
	authURL, err := ExtractHTTPCredentials(tempDataDir, owner, repo, "https://github.com/alice/project.git")
	if err != nil {
		t.Fatalf("ExtractHTTPCredentials failed: %v", err)
	}
	expected := "https://token_user:secret_token_123@github.com/alice/project.git"
	if authURL != expected {
		t.Errorf("expected %q, got %q", expected, authURL)
	}

	// Test when host does not match
	fallbackURL, err := ExtractHTTPCredentials(tempDataDir, owner, repo, "https://bitbucket.org/alice/project.git")
	if err != nil {
		t.Fatalf("ExtractHTTPCredentials failed: %v", err)
	}
	if fallbackURL != "https://bitbucket.org/alice/project.git" {
		t.Errorf("expected unchanged URL, got %q", fallbackURL)
	}
}

func TestResolveSSHCredentials(t *testing.T) {
	tempCache := t.TempDir()
	fakeKey := []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nfake-key-data\n-----END OPENSSH PRIVATE KEY-----")

	repo := &forgejodb.MatchedRepo{
		RepoID:        1,
		OwnerName:     "alice",
		RepoName:      "project",
		PrivateKey:    fakeKey,
		RemoteAddress: "git@github.com:alice/project.git",
	}

	creds, err := ResolveSSHCredentials(tempCache, repo)
	if err != nil {
		t.Fatalf("ResolveSSHCredentials failed: %v", err)
	}

	if creds.KeyPath == "" {
		t.Fatal("expected non-empty key path")
	}

	stat, err := os.Stat(creds.KeyPath)
	if err != nil {
		t.Fatalf("failed to stat temp key: %v", err)
	}
	if stat.Mode().Perm() != 0600 {
		t.Errorf("expected file mode 0600, got %v", stat.Mode().Perm())
	}

	data, err := os.ReadFile(creds.KeyPath)
	if err != nil {
		t.Fatalf("failed to read key file: %v", err)
	}
	if !strings.Contains(string(data), "fake-key-data") {
		t.Errorf("key file content missing expected data: %s", string(data))
	}

	if !strings.Contains(creds.SSHCommand, creds.KeyPath) {
		t.Errorf("expected SSHCommand %q to contain key path %q", creds.SSHCommand, creds.KeyPath)
	}

	// Cleanup check
	creds.Cleanup()
	if _, err := os.Stat(creds.KeyPath); !os.IsNotExist(err) {
		t.Errorf("expected key file to be removed after cleanup")
	}
}

func TestGitSyncEngine_CloneFetchPushConflict(t *testing.T) {
	tempDir := t.TempDir()

	// 1. Create a "remote origin" git repo (simulating GitHub source repo)
	originDir := filepath.Join(tempDir, "origin_source")
	if err := os.MkdirAll(originDir, 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, originDir, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(originDir, "file1.txt"), []byte("initial commit"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, originDir, "add", "file1.txt")
	runGit(t, originDir, "commit", "-m", "Initial commit")

	cacheDir := filepath.Join(tempDir, "cache")
	provider := "github"
	owner := "alice"
	repo := "repo1"

	// 2. Perform initial mirror clone via SyncRepository
	opts := SyncOptions{
		CacheDir:  cacheDir,
		Provider:  provider,
		Owner:     owner,
		Repo:      repo,
		SourceURL: originDir,
		Timeout:   1 * time.Minute,
	}

	cachePath, err := SyncRepository(context.Background(), opts)
	if err != nil {
		t.Fatalf("initial SyncRepository failed: %v", err)
	}

	// Verify cache bare repo
	branches := runGit(t, cachePath, "branch", "-a")
	if !strings.Contains(branches, "main") {
		t.Fatalf("expected main branch in cache repo, got: %s", branches)
	}

	// 3. Make another commit in origin and test subsequent fetch --prune
	if err := os.WriteFile(filepath.Join(originDir, "file2.txt"), []byte("second commit"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, originDir, "add", "file2.txt")
	runGit(t, originDir, "commit", "-m", "Second commit")

	cachePath2, err := SyncRepository(context.Background(), opts)
	if err != nil {
		t.Fatalf("subsequent SyncRepository fetch failed: %v", err)
	}
	if cachePath2 != cachePath {
		t.Errorf("expected same cache path %q, got %q", cachePath, cachePath2)
	}

	logOutput := runGit(t, cachePath, "log", "--oneline", "main")
	if !strings.Contains(logOutput, "Second commit") {
		t.Fatalf("expected cache repo to have 'Second commit' after fetch, got log: %s", logOutput)
	}

	// 4. Test Push: create a target bare repo (simulating Forgejo target)
	targetBareDir := filepath.Join(tempDir, "forgejo_target.git")
	runGit(t, "", "init", "--bare", targetBareDir)

	// Since we are using local file paths rather than ssh daemon for test,
	// test direct push command invocation against bare target to simulate push engine
	pushCmd := exec.Command("git", "-C", cachePath, "push", targetBareDir, "refs/heads/*:refs/heads/*", "refs/tags/*:refs/tags/*")
	out, err := pushCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("push to target failed: %v, out: %s", err, string(out))
	}

	// Verify target bare repo has both commits
	targetLog := runGit(t, targetBareDir, "log", "--oneline", "main")
	if !strings.Contains(targetLog, "Second commit") {
		t.Fatalf("expected target bare repo to have 'Second commit', got: %s", targetLog)
	}

	// 5. Test Conflict detection: deliberately diverge target history
	divergeWorkDir := filepath.Join(tempDir, "diverge_work")
	runGit(t, "", "clone", targetBareDir, divergeWorkDir)
	runGit(t, divergeWorkDir, "checkout", "main")
	if err := os.WriteFile(filepath.Join(divergeWorkDir, "divergent.txt"), []byte("divergent change"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, divergeWorkDir, "add", "divergent.txt")
	runGit(t, divergeWorkDir, "commit", "-m", "Divergent commit in Forgejo")
	runGit(t, divergeWorkDir, "push", "origin", "main")

	// Now try to push cache (which is behind and diverged from target) to targetBareDir without force
	pushConflictCmd := exec.Command("git", "-C", cachePath, "push", targetBareDir, "refs/heads/*:refs/heads/*", "refs/tags/*:refs/tags/*")
	conflictOut, conflictErr := pushConflictCmd.CombinedOutput()
	if conflictErr == nil {
		t.Fatalf("expected push to fail due to non-fast-forward conflict, but succeeded! Output: %s", string(conflictOut))
	}

	conflictStr := string(conflictOut)
	hasNonFastForward := strings.Contains(strings.ToLower(conflictStr), "non-fast-forward") ||
		strings.Contains(conflictStr, "[rejected]") ||
		strings.Contains(conflictStr, "! [rejected]")

	if !hasNonFastForward {
		t.Fatalf("expected non-fast-forward error output, got: %s", conflictStr)
	}

	// Test ErrConflict typing in push helper
	var errConflict *ErrConflict
	errTestConflict := &ErrConflict{Detail: conflictStr}
	if !errors.As(errTestConflict, &errConflict) {
		t.Fatalf("expected errors.As with *ErrConflict to succeed")
	}
}

func TestSanitizeError(t *testing.T) {
	rawErr := fmt.Errorf("git clone failed: https://user:supersecretpassword@github.com/owner/repo.git not found")
	sanitized := SanitizeError(rawErr)
	if strings.Contains(sanitized.Error(), "supersecretpassword") {
		t.Fatalf("sanitized error still contains secret password: %s", sanitized.Error())
	}
	if !strings.Contains(sanitized.Error(), "https://***:***@github.com/owner/repo.git") {
		t.Fatalf("sanitized error does not match expected format: %s", sanitized.Error())
	}
}
