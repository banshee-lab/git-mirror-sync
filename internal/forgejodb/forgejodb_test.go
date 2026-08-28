package forgejodb_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"forgejo-sync/internal/config"
	"forgejo-sync/internal/forgejodb"
)

func TestNormalizeRemote(t *testing.T) {
	tests := []struct {
		name          string
		input         string
		mustContain   []string
		mustNotContain []string
	}{
		{
			name:  "standard https url with .git",
			input: "https://github.com/octocat/Hello-World.git",
			mustContain: []string{
				"https://github.com/octocat/Hello-World.git",
				"https://github.com/octocat/Hello-World",
				"https://github.com/octocat/Hello-World/",
				"https://github.com/octocat/Hello-World.git/",
			},
		},
		{
			name:  "standard https url without .git",
			input: "https://github.com/octocat/Hello-World",
			mustContain: []string{
				"https://github.com/octocat/Hello-World",
				"https://github.com/octocat/Hello-World.git",
				"https://github.com/octocat/Hello-World/",
				"https://github.com/octocat/Hello-World.git/",
			},
		},
		{
			name:  "https url with trailing slash",
			input: "https://github.com/octocat/Hello-World/",
			mustContain: []string{
				"https://github.com/octocat/Hello-World",
				"https://github.com/octocat/Hello-World.git",
				"https://github.com/octocat/Hello-World/",
			},
		},
		{
			name:  "https url with basic auth credentials",
			input: "https://token:secret123@gitlab.com/group/project.git",
			mustContain: []string{
				"https://token:secret123@gitlab.com/group/project.git",
				"https://gitlab.com/group/project.git",
				"https://gitlab.com/group/project",
			},
		},
		{
			name:  "ssh scp-like url with .git",
			input: "git@github.com:octocat/Hello-World.git",
			mustContain: []string{
				"git@github.com:octocat/Hello-World.git",
				"git@github.com:octocat/Hello-World",
				"git@github.com:octocat/Hello-World/",
			},
		},
		{
			name:  "ssh scp-like url without .git",
			input: "git@gitlab.com:group/subgroup/project",
			mustContain: []string{
				"git@gitlab.com:group/subgroup/project",
				"git@gitlab.com:group/subgroup/project.git",
			},
		},
		{
			name:  "ssh protocol url",
			input: "ssh://git@github.com/octocat/Hello-World.git",
			mustContain: []string{
				"ssh://git@github.com/octocat/Hello-World.git",
				"ssh://git@github.com/octocat/Hello-World",
			},
		},
		{
			name:        "empty string",
			input:       "   ",
			mustContain: nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			results := forgejodb.NormalizeRemote(tc.input)

			if tc.input == "   " {
				if len(results) != 0 {
					t.Fatalf("expected empty slice for whitespace input, got: %v", results)
				}
				return
			}

			// Check for duplicates
			seen := make(map[string]bool)
			for _, r := range results {
				if seen[r] {
					t.Errorf("duplicate candidate generated: %q", r)
				}
				seen[r] = true
				if r == "" {
					t.Errorf("empty string candidate generated")
				}
			}

			for _, expected := range tc.mustContain {
				if !slices.Contains(results, expected) {
					t.Errorf("expected candidate %q not found in results: %v", expected, results)
				}
			}
		})
	}
}

func TestSentinelErrorsAndTypes(t *testing.T) {
	if forgejodb.ErrNoMatch == nil {
		t.Fatal("ErrNoMatch should not be nil")
	}

	if !errors.Is(forgejodb.ErrNoMatch, forgejodb.ErrNoMatch) {
		t.Fatal("ErrNoMatch comparison failed")
	}

	matched := forgejodb.MatchedRepo{
		RepoID:        42,
		OwnerName:     "alice",
		RepoName:      "my-project",
		PushMirrorID:  7,
		RemoteAddress: "https://github.com/alice/my-project.git",
		PrivateKey:    []byte("test-key"),
	}

	if matched.RepoID != 42 || matched.OwnerName != "alice" || matched.RepoName != "my-project" {
		t.Errorf("MatchedRepo fields unexpected: %+v", matched)
	}
}

func TestClientNilHandling(t *testing.T) {
	var client *forgejodb.Client
	ctx := context.Background()

	_, err := client.LookupRepository(ctx, "https://github.com/a/b", "git@github.com:a/b.git")
	if err == nil {
		t.Fatal("expected error on nil client LookupRepository, got nil")
	}

	client.Close() // Should not panic
	if client.Pool() != nil {
		t.Fatal("expected nil pool for nil client")
	}
}

func TestClientConnectionRetryFailure(t *testing.T) {
	// Point to an invalid unreachable port to test retry timeout
	cfg := &config.Config{
		ForgejoDBHost:     "127.0.0.1",
		ForgejoDBPort:     59999,
		ForgejoDBUser:     "user",
		ForgejoDBPassword: "password",
		ForgejoDBName:     "forgejo",
		ForgejoDBSSLMode:  "disable",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := forgejodb.NewClient(ctx, cfg)
	if err == nil {
		t.Fatal("expected connection failure for invalid port, got nil error")
	}
}

func TestDBLookupIntegration(t *testing.T) {
	// Instructions say: "For Postgres-dependent tests in forgejodb, use t.Skip() with a clear message unless the test is purely unit-level"
	t.Skip("Skipping Postgres integration test since no real database is available in this test environment")
}
