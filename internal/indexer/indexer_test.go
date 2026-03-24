package indexer

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mylocalgpt/local-docs-mcp/internal/config"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

// initIndexerTestRepo creates a local git repo with richer markdown files
// under docs/ and an ignored file under other/. Returns the repo path.
func initIndexerTestRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	// Use runGitInDir from git_test.go (same package)
	mustGit := func(args ...string) {
		t.Helper()
		if _, err := runGitInDir(dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}

	mustGit("init", "-b", "main")
	mustGit("config", "user.email", "test@test.com")
	mustGit("config", "user.name", "Test")

	// Create docs directory with markdown files
	docsDir := filepath.Join(dir, "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatal(err)
	}

	guide := `# Getting Started

Welcome to the guide.

## Installation

Run the install command to get started.

## Configuration

Edit the config file to customize behavior.
`
	if err := os.WriteFile(filepath.Join(docsDir, "guide.md"), []byte(guide), 0o644); err != nil {
		t.Fatal(err)
	}

	api := "# API Reference\n\n## List Items\n\n```go\nfunc ListItems() []Item {\n    return items\n}\n```\n"
	if err := os.WriteFile(filepath.Join(docsDir, "api.md"), []byte(api), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create a file outside docs/ that should not be indexed
	otherDir := filepath.Join(dir, "other")
	if err := os.MkdirAll(otherDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(otherDir, "ignored.md"), []byte("# Ignored\n\nShould not appear.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mustGit("add", ".")
	mustGit("commit", "-m", "initial docs")

	return dir
}

func TestIndexRepo(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repoDir := initIndexerTestRepo(t)

	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ix, err := NewIndexer(s)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Cleanup()

	repoCfg := config.RepoConfig{
		URL:   repoDir,
		Alias: "test-docs",
		Paths: []string{"docs/"},
	}

	// --- First index ---
	result, err := ix.IndexRepo(repoCfg)
	if err != nil {
		t.Fatalf("IndexRepo failed: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("IndexRepo result error: %v", result.Error)
	}
	if result.Skipped {
		t.Fatal("expected first index to not be skipped")
	}
	if result.DocsIndexed == 0 {
		t.Fatal("expected documents to be indexed")
	}

	// Verify repo record
	repo, err := s.GetRepo("test-docs")
	if err != nil {
		t.Fatal(err)
	}
	if repo == nil {
		t.Fatal("expected repo record to exist")
	}
	if repo.CommitSHA == "" {
		t.Fatal("expected commit SHA to be set")
	}
	if repo.DocCount == 0 {
		t.Fatal("expected doc count > 0")
	}

	// Verify FTS works
	if err := s.RebuildFTS(); err != nil {
		t.Fatal(err)
	}

	// --- Second index (should skip) ---
	result2, err := ix.IndexRepo(repoCfg)
	if err != nil {
		t.Fatalf("IndexRepo (2nd) failed: %v", err)
	}
	if result2.Error != nil {
		t.Fatalf("IndexRepo (2nd) result error: %v", result2.Error)
	}
	if !result2.Skipped {
		t.Fatal("expected second index to be skipped (SHA unchanged)")
	}

	// --- Add a new file, commit, re-index ---
	newFile := filepath.Join(repoDir, "docs", "changelog.md")
	if err := os.WriteFile(newFile, []byte("# Changelog\n\n## v1.0\n\nInitial release.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	mustGit := func(args ...string) {
		t.Helper()
		if _, err := runGitInDir(repoDir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	mustGit("add", ".")
	mustGit("commit", "-m", "add changelog")

	result3, err := ix.IndexRepo(repoCfg)
	if err != nil {
		t.Fatalf("IndexRepo (3rd) failed: %v", err)
	}
	if result3.Error != nil {
		t.Fatalf("IndexRepo (3rd) result error: %v", result3.Error)
	}
	if result3.Skipped {
		t.Fatal("expected third index to NOT be skipped (new commit)")
	}

	// Verify updated repo record
	repo3, err := s.GetRepo("test-docs")
	if err != nil {
		t.Fatal(err)
	}
	if repo3.CommitSHA == repo.CommitSHA {
		t.Fatal("expected commit SHA to have changed")
	}
	if repo3.DocCount <= repo.DocCount {
		t.Fatalf("expected doc count to increase: was %d, now %d", repo.DocCount, repo3.DocCount)
	}
}

func TestIndexAll(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not available")
	}

	repoDir := initIndexerTestRepo(t)

	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ix, err := NewIndexer(s)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Cleanup()

	cfg := &config.Config{
		Repos: []config.RepoConfig{
			{URL: repoDir, Alias: "test-all", Paths: []string{"docs/"}},
		},
	}

	results, err := ix.IndexAll(cfg)
	if err != nil {
		t.Fatalf("IndexAll failed: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}
	if results[0].Error != nil {
		t.Fatalf("unexpected error: %v", results[0].Error)
	}
	if results[0].DocsIndexed == 0 {
		t.Fatal("expected docs to be indexed")
	}
}

func TestIndexRepoConfigPaths(t *testing.T) {
	// Verify that cfg.Paths is correctly marshaled to JSON
	paths := []string{"docs/", "guides/"}
	data, err := json.Marshal(paths)
	if err != nil {
		t.Fatal(err)
	}
	expected := `["docs/","guides/"]`
	if string(data) != expected {
		t.Fatalf("expected %s, got %s", expected, string(data))
	}
}
