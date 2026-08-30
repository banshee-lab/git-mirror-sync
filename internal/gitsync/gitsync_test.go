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
)

func gitCommand(dir string, args ...string) *exec.Cmd {
	cmdArgs := []string{"-c", "safe.bareRepository=explicit"}
	isBareRepo := dir != "" && !fileExists(filepath.Join(dir, ".git")) && fileExists(filepath.Join(dir, "HEAD"))
	if isBareRepo {
		cmdArgs = append(cmdArgs, "--git-dir", dir)
		dir = ""
	}
	cmdArgs = append(cmdArgs, args...)
	cmd := exec.Command("git", cmdArgs...)
	if dir != "" {
		cmd.Dir = dir
	}
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=Test",
		"GIT_AUTHOR_EMAIL=test@example.com",
		"GIT_COMMITTER_NAME=Test",
		"GIT_COMMITTER_EMAIL=test@example.com",
	)
	return cmd
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := gitCommand(dir, args...)
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

func TestGitSyncEngine_CloneFetchPushConflict(t *testing.T) {
	tempDir := t.TempDir()

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

	branches := runGit(t, cachePath, "branch", "-a")
	if !strings.Contains(branches, "main") {
		t.Fatalf("expected main branch in cache repo, got: %s", branches)
	}

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

	targetBareDir := filepath.Join(tempDir, "forgejo_target.git")
	runGit(t, "", "init", "--bare", targetBareDir)

	pushCmd := gitCommand("", "-C", cachePath, "push", targetBareDir, "refs/heads/*:refs/heads/*", "refs/tags/*:refs/tags/*")
	out, err := pushCmd.CombinedOutput()
	if err != nil {
		t.Fatalf("push to target failed: %v, out: %s", err, string(out))
	}

	targetLog := runGit(t, targetBareDir, "log", "--oneline", "main")
	if !strings.Contains(targetLog, "Second commit") {
		t.Fatalf("expected target bare repo to have 'Second commit', got: %s", targetLog)
	}

	divergeWorkDir := filepath.Join(tempDir, "diverge_work")
	runGit(t, "", "clone", targetBareDir, divergeWorkDir)
	runGit(t, divergeWorkDir, "checkout", "main")
	if err := os.WriteFile(filepath.Join(divergeWorkDir, "divergent.txt"), []byte("divergent change"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, divergeWorkDir, "add", "divergent.txt")
	runGit(t, divergeWorkDir, "commit", "-m", "Divergent commit in Forgejo")
	runGit(t, divergeWorkDir, "push", "origin", "main")

	pushConflictCmd := gitCommand("", "-C", cachePath, "push", targetBareDir, "refs/heads/*:refs/heads/*", "refs/tags/*:refs/tags/*")
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

	var errConflict *ErrConflict
	errTestConflict := &ErrConflict{Detail: conflictStr}
	if !errors.As(errTestConflict, &errConflict) {
		t.Fatalf("expected errors.As with *ErrConflict to succeed")
	}
}

func TestSanitizeError(t *testing.T) {
	rawErr := fmt.Errorf("git clone failed: ******github.com/owner/repo.git not found")
	sanitized := SanitizeError(rawErr)
	if strings.Contains(sanitized.Error(), "supersecretpassword") {
		t.Fatalf("sanitized error still contains secret password: %s", sanitized.Error())
	}
	if !strings.Contains(sanitized.Error(), "******github.com/owner/repo.git") {
		t.Fatalf("sanitized error does not match expected format: %s", sanitized.Error())
	}
}
