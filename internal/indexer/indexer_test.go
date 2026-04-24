package indexer

import (
	"context"
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
	defer s.Close() //nolint:errcheck

	ix, err := NewIndexer(s)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Cleanup() //nolint:errcheck

	repoCfg := config.RepoConfig{
		URL:   repoDir,
		Alias: "test-docs",
		Paths: []string{"docs/"},
	}

	// --- First index ---
	result, err := ix.IndexRepo(context.Background(), repoCfg, false)
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
	result2, err := ix.IndexRepo(context.Background(), repoCfg, false)
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

	result3, err := ix.IndexRepo(context.Background(), repoCfg, false)
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
	defer s.Close() //nolint:errcheck

	ix, err := NewIndexer(s)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Cleanup() //nolint:errcheck

	cfg := &config.Config{
		Repos: []config.RepoConfig{
			{URL: repoDir, Alias: "test-all", Paths: []string{"docs/"}},
		},
	}

	results, err := ix.IndexAll(context.Background(), cfg, false)
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

// createLocalDocsDir creates a temp directory with nested markdown files
// for testing IndexLocalPath.
func createLocalDocsDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()

	// Top-level markdown
	if err := os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# My Project\n\nOverview of the project.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Nested directory
	sub := filepath.Join(dir, "guides")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sub, "quickstart.md"), []byte("# Quick Start\n\n## Step 1\n\nInstall the tool.\n\n## Step 2\n\nRun the command.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Non-markdown file (should be ignored)
	if err := os.WriteFile(filepath.Join(dir, "notes.txt"), []byte("not markdown"), 0o644); err != nil {
		t.Fatal(err)
	}

	return dir
}

func TestIndexLocalPath(t *testing.T) {
	dir := createLocalDocsDir(t)

	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck

	ix, err := NewIndexer(s)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Cleanup() //nolint:errcheck

	result, err := ix.IndexLocalPath(context.Background(), "local-docs", dir)
	if err != nil {
		t.Fatalf("IndexLocalPath failed: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("IndexLocalPath result error: %v", result.Error)
	}
	if result.DocsIndexed == 0 {
		t.Fatal("expected documents to be indexed")
	}

	// Verify repo record
	repo, err := s.GetRepo("local-docs")
	if err != nil {
		t.Fatal(err)
	}
	if repo == nil {
		t.Fatal("expected repo record to exist")
	}
	if repo.SourceType != "local" {
		t.Errorf("SourceType: got %q, want %q", repo.SourceType, "local")
	}
	if repo.CommitSHA != "" {
		t.Errorf("CommitSHA should be empty for local, got %q", repo.CommitSHA)
	}
	if repo.DocCount == 0 {
		t.Fatal("expected doc count > 0")
	}
	if repo.URL != dir {
		t.Errorf("URL should be dir path: got %q, want %q", repo.URL, dir)
	}
}

func TestIndexLocalPathReindexReplaces(t *testing.T) {
	dir := createLocalDocsDir(t)

	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck

	ix, err := NewIndexer(s)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Cleanup() //nolint:errcheck

	// First index
	r1, err := ix.IndexLocalPath(context.Background(), "local-docs", dir)
	if err != nil {
		t.Fatal(err)
	}
	if r1.Error != nil {
		t.Fatal(r1.Error)
	}
	firstCount := r1.DocsIndexed

	// Re-index same alias should replace, not duplicate
	r2, err := ix.IndexLocalPath(context.Background(), "local-docs", dir)
	if err != nil {
		t.Fatal(err)
	}
	if r2.Error != nil {
		t.Fatal(r2.Error)
	}
	if r2.DocsIndexed != firstCount {
		t.Errorf("doc count changed on reindex: %d vs %d", firstCount, r2.DocsIndexed)
	}

	// Verify no duplicate documents in DB
	repo, _ := s.GetRepo("local-docs")
	if repo.DocCount != firstCount {
		t.Errorf("DB doc count mismatch: got %d, want %d", repo.DocCount, firstCount)
	}
}

func TestIndexLocalPathNonExistentDir(t *testing.T) {
	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck

	ix, err := NewIndexer(s)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Cleanup() //nolint:errcheck

	_, err = ix.IndexLocalPath(context.Background(), "bad", "/nonexistent/path/that/does/not/exist")
	if err == nil {
		t.Fatal("expected error for nonexistent directory")
	}
}

func TestIndexLocalPathSkipsUndecodable(t *testing.T) {
	dir := t.TempDir()

	// Copy Phase 1 fixtures: utf16le.md (valid) and utf16_truncated.md (truncated).
	for _, name := range []string{"utf16le.md", "utf16_truncated.md"} {
		data, err := os.ReadFile(filepath.Join("testdata", name))
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0o644); err != nil {
			t.Fatalf("write fixture %s: %v", name, err)
		}
	}

	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck

	ix, err := NewIndexer(s)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Cleanup() //nolint:errcheck

	result, err := ix.IndexLocalPath(context.Background(), "skip-test", dir)
	if err != nil {
		t.Fatalf("IndexLocalPath failed: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("IndexLocalPath result error: %v", result.Error)
	}
	if result.DocsIndexed == 0 {
		t.Fatal("expected at least one document from valid utf16le.md")
	}
	if result.SkippedFiles != 1 {
		t.Fatalf("SkippedFiles: got %d, want 1", result.SkippedFiles)
	}
	if len(result.SkippedSample) != 1 || result.SkippedSample[0] != "utf16_truncated.md" {
		t.Fatalf("SkippedSample: got %v, want [utf16_truncated.md]", result.SkippedSample)
	}
}

func TestIndexLocalPathFileNotDir(t *testing.T) {
	tmpFile := filepath.Join(t.TempDir(), "file.md")
	if err := os.WriteFile(tmpFile, []byte("# Test\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close() //nolint:errcheck

	ix, err := NewIndexer(s)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Cleanup() //nolint:errcheck

	_, err = ix.IndexLocalPath(context.Background(), "bad", tmpFile)
	if err == nil {
		t.Fatal("expected error for file (not directory)")
	}
}
