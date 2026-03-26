//go:build integration

package integrationtest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mylocalgpt/local-docs-mcp/internal/config"
	"github.com/mylocalgpt/local-docs-mcp/internal/indexer"
	"github.com/mylocalgpt/local-docs-mcp/internal/search"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

func TestUpdateSkipsUnchanged(t *testing.T) {
	s := openTestStore(t)

	ix, err := indexer.NewIndexer(s)
	if err != nil {
		t.Fatalf("create indexer: %v", err)
	}
	defer ix.Cleanup()

	cfg := config.RepoConfig{
		URL:   "https://github.com/MicrosoftDocs/entra-docs",
		Paths: []string{"docs/identity/hybrid/connect/", "docs/identity/hybrid/cloud-sync/", "docs/identity/hybrid/"},
		Alias: "entra-hybrid",
	}

	result, err := ix.IndexRepo(cfg, false)
	if err != nil {
		t.Fatalf("index repo: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("index error: %v", result.Error)
	}
	if !result.Skipped {
		t.Error("expected Skipped=true for unchanged repo")
	}
	t.Logf("skipped=%v duration=%s", result.Skipped, result.Duration)
}

func TestUpdateForceReindex(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "force-reindex-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	dbPath := filepath.Join(tmpDir, "test.db")

	s, err := store.NewStore(dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer s.Close()

	ix, err := indexer.NewIndexer(s)
	if err != nil {
		t.Fatalf("create indexer: %v", err)
	}
	defer ix.Cleanup()

	cfg := config.RepoConfig{
		URL:   "https://github.com/MicrosoftDocs/entra-docs",
		Paths: []string{"docs/identity/hybrid/connect/"},
		Alias: "entra-hybrid",
	}

	// First index
	result1, err := ix.IndexRepo(cfg, false)
	if err != nil {
		t.Fatalf("first index: %v", err)
	}
	if result1.Error != nil {
		t.Fatalf("first index error: %v", result1.Error)
	}
	if result1.Skipped {
		t.Error("first index should not be skipped")
	}
	if result1.DocsIndexed == 0 {
		t.Error("expected docs > 0 on first index")
	}
	t.Logf("first index: %d docs in %s", result1.DocsIndexed, result1.Duration)

	// Rebuild FTS after first index
	if err := s.RebuildFTS(); err != nil {
		t.Fatalf("rebuild fts: %v", err)
	}

	// Force re-index (need a new indexer since Cleanup clears temp dirs)
	ix2, err := indexer.NewIndexer(s)
	if err != nil {
		t.Fatalf("create indexer 2: %v", err)
	}
	defer ix2.Cleanup()

	result2, err := ix2.IndexRepo(cfg, true)
	if err != nil {
		t.Fatalf("force reindex: %v", err)
	}
	if result2.Error != nil {
		t.Fatalf("force reindex error: %v", result2.Error)
	}
	if result2.Skipped {
		t.Error("force reindex should not be skipped")
	}
	if result2.DocsIndexed == 0 {
		t.Error("expected docs > 0 on force reindex")
	}
	t.Logf("force reindex: %d docs in %s", result2.DocsIndexed, result2.Duration)

	// Rebuild FTS and verify search works
	if err := s.RebuildFTS(); err != nil {
		t.Fatalf("rebuild fts after reindex: %v", err)
	}

	srch := search.NewSearch(s)
	resp, err := srch.Query(search.SearchOptions{Query: "connect sync", TokenBudget: 2000})
	if err != nil {
		t.Fatalf("search after reindex: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Error("expected search results after force reindex")
	}
}

func TestRemoveAndReindex(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "remove-reindex-*")
	if err != nil {
		t.Fatalf("create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)
	dbPath := filepath.Join(tmpDir, "test.db")

	s, err := store.NewStore(dbPath)
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer s.Close()

	ix, err := indexer.NewIndexer(s)
	if err != nil {
		t.Fatalf("create indexer: %v", err)
	}
	defer ix.Cleanup()

	cfg := config.RepoConfig{
		URL:   "https://github.com/MicrosoftDocs/entra-docs",
		Paths: []string{"docs/identity/hybrid/connect/"},
		Alias: "entra-hybrid",
	}

	// Index
	result, err := ix.IndexRepo(cfg, false)
	if err != nil {
		t.Fatalf("index: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("index error: %v", result.Error)
	}

	// Verify repo exists
	repos, err := s.ListRepos()
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(repos))
	}

	// Remove
	_, err = s.DeleteRepo("entra-hybrid")
	if err != nil {
		t.Fatalf("delete repo: %v", err)
	}

	// Verify empty
	repos, err = s.ListRepos()
	if err != nil {
		t.Fatalf("list repos after delete: %v", err)
	}
	if len(repos) != 0 {
		t.Fatalf("expected 0 repos after delete, got %d", len(repos))
	}

	// Re-index with a new indexer
	ix2, err := indexer.NewIndexer(s)
	if err != nil {
		t.Fatalf("create indexer 2: %v", err)
	}
	defer ix2.Cleanup()

	result2, err := ix2.IndexRepo(cfg, false)
	if err != nil {
		t.Fatalf("reindex: %v", err)
	}
	if result2.Error != nil {
		t.Fatalf("reindex error: %v", result2.Error)
	}

	// Verify repo is back
	repos, err = s.ListRepos()
	if err != nil {
		t.Fatalf("list repos after reindex: %v", err)
	}
	if len(repos) != 1 {
		t.Fatalf("expected 1 repo after reindex, got %d", len(repos))
	}
	if repos[0].Alias != "entra-hybrid" {
		t.Errorf("expected alias 'entra-hybrid', got %q", repos[0].Alias)
	}
}
