//go:build integration

package integrationtest

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mylocalgpt/local-docs-mcp/internal/indexer"
	"github.com/mylocalgpt/local-docs-mcp/internal/search"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

func TestLocalPathIndexAndSearch(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "docs")
	os.MkdirAll(sub, 0o755)

	os.WriteFile(filepath.Join(sub, "guide.md"), []byte("# User Guide\n\n## Installation\n\nRun `go install` to set up.\n\n## Configuration\n\nEdit the config file.\n"), 0o644)
	os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# My Project\n\nOverview of the project.\n"), 0o644)

	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	ix, err := indexer.NewIndexer(s)
	if err != nil {
		t.Fatal(err)
	}
	defer ix.Cleanup()

	result, err := ix.IndexLocalPath("local-project", dir)
	if err != nil {
		t.Fatalf("IndexLocalPath: %v", err)
	}
	if result.Error != nil {
		t.Fatalf("IndexLocalPath result error: %v", result.Error)
	}
	if result.DocsIndexed == 0 {
		t.Fatal("expected documents to be indexed")
	}

	// Verify repo record
	repo, _ := s.GetRepo("local-project")
	if repo == nil {
		t.Fatal("repo not found")
	}
	if repo.SourceType != "local" {
		t.Errorf("source type: got %q", repo.SourceType)
	}

	// Search indexed content
	srch := search.NewSearch(s)
	results, err := srch.Query(search.SearchOptions{
		Query:       "installation",
		TokenBudget: 2000,
		Limit:       10,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Error("expected search results for 'installation'")
	}
}
