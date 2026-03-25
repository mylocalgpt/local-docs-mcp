package search

import (
	"testing"

	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

// setupTestStore creates an in-memory store with test data.
func setupTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	return s
}

// insertTestRepo inserts a repo and returns its ID.
func insertTestRepo(t *testing.T, s *store.Store, alias, url string) int64 {
	t.Helper()
	id, err := s.UpsertRepo(alias, url, `["docs"]`, "git")
	if err != nil {
		t.Fatalf("upsert repo %s: %v", alias, err)
	}
	return id
}

// insertTestDocs inserts documents for a repo.
func insertTestDocs(t *testing.T, s *store.Store, repoID int64, docs []store.Document) {
	t.Helper()
	for i := range docs {
		docs[i].RepoID = repoID
	}
	if err := s.ReplaceDocuments(repoID, docs); err != nil {
		t.Fatalf("replace documents: %v", err)
	}
}

func TestSearchQuery(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()

	repoID := insertTestRepo(t, s, "test-repo", "https://example.com/test-repo")
	insertTestDocs(t, s, repoID, []store.Document{
		{Path: "guide.md", DocTitle: "Guide", SectionTitle: "Getting Started", Content: "Welcome to the getting started guide for sync configuration", Tokens: 50, HeadingLevel: 2},
		{Path: "guide.md", DocTitle: "Guide", SectionTitle: "Advanced", Content: "Advanced usage patterns for power users", Tokens: 40, HeadingLevel: 2},
		{Path: "reference.md", DocTitle: "Reference", SectionTitle: "API Reference", Content: "The sync API provides methods for data synchronization", Tokens: 60, HeadingLevel: 2},
	})

	search := NewSearch(s)
	results, err := search.Query(SearchOptions{
		Query:       "sync",
		Limit:       20,
		TokenBudget: 2000,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(results) == 0 {
		t.Fatal("expected results, got none")
	}

	// Results should be ranked; verify they come back
	for _, r := range results {
		if r.RepoAlias != "test-repo" {
			t.Errorf("expected repo alias test-repo, got %s", r.RepoAlias)
		}
	}
}

func TestRelevanceFilter(t *testing.T) {
	// BM25 scores are negative, lower = better
	results := []SearchResult{
		{Score: -4.0, Tokens: 50},
		{Score: -3.5, Tokens: 40},
		{Score: -2.1, Tokens: 30},
		{Score: -1.5, Tokens: 20}, // should be dropped: -1.5 > -2.0 threshold
		{Score: -0.8, Tokens: 10}, // should be dropped
	}

	filtered := applyRelevanceFilter(results)

	// threshold = -4.0 * 0.5 = -2.0. Keep scores <= -2.0
	if len(filtered) != 3 {
		t.Fatalf("expected 3 results after filter, got %d", len(filtered))
	}
	if filtered[0].Score != -4.0 || filtered[1].Score != -3.5 || filtered[2].Score != -2.1 {
		t.Errorf("unexpected scores: %v", filtered)
	}
}

func TestRelevanceFilterSingleResult(t *testing.T) {
	results := []SearchResult{{Score: -1.0, Tokens: 50}}
	filtered := applyRelevanceFilter(results)
	if len(filtered) != 1 {
		t.Fatalf("single result should always pass, got %d", len(filtered))
	}
}

func TestTokenBudget(t *testing.T) {
	results := []SearchResult{
		{Score: -4.0, Tokens: 100},
		{Score: -3.0, Tokens: 100},
		{Score: -2.0, Tokens: 100},
		{Score: -1.0, Tokens: 100},
	}

	// Budget of 250: first two fit (200), third crosses boundary (300) but is included
	budgeted := applyTokenBudget(results, 250)
	if len(budgeted) != 3 {
		t.Fatalf("expected 3 results with budget 250, got %d", len(budgeted))
	}

	// Budget of 100: first fits exactly (100 <= 100), second crosses (200 > 100)
	budgeted = applyTokenBudget(results, 100)
	if len(budgeted) != 2 {
		t.Fatalf("expected 2 results with budget 100, got %d", len(budgeted))
	}

	// Budget of 50: first crosses boundary (100 > 50) but is included
	budgeted = applyTokenBudget(results, 50)
	if len(budgeted) != 1 {
		t.Fatalf("expected 1 result with budget 50, got %d", len(budgeted))
	}
}

func TestMergeAdjacentChunks(t *testing.T) {
	results := []SearchResult{
		{DocID: 1, RepoAlias: "repo", Path: "file.md", SectionTitle: "Section A", Content: "First chunk", Tokens: 50, Score: -3.0},
		{DocID: 2, RepoAlias: "repo", Path: "file.md", SectionTitle: "Section B", Content: "Second chunk", Tokens: 40, Score: -4.0},
		{DocID: 5, RepoAlias: "repo", Path: "other.md", SectionTitle: "Other", Content: "Other file", Tokens: 30, Score: -2.0},
	}

	merged := mergeAdjacentChunks(results)

	// DocID 1 and 2 are adjacent in the same file, should merge
	// DocID 5 is in a different file, stays separate
	if len(merged) != 2 {
		t.Fatalf("expected 2 results after merge, got %d", len(merged))
	}

	// Find the merged result (from file.md)
	var fileMd, otherMd *SearchResult
	for i := range merged {
		if merged[i].Path == "file.md" {
			fileMd = &merged[i]
		} else {
			otherMd = &merged[i]
		}
	}

	if fileMd == nil || otherMd == nil {
		t.Fatal("expected one result from each file")
	}

	// Merged chunk should have combined tokens
	if fileMd.Tokens != 90 {
		t.Errorf("expected merged tokens 90, got %d", fileMd.Tokens)
	}

	// Merged chunk should use the better (lower) score
	if fileMd.Score != -4.0 {
		t.Errorf("expected merged score -4.0, got %f", fileMd.Score)
	}

	// Merged chunk should keep first section title
	if fileMd.SectionTitle != "Section A" {
		t.Errorf("expected first section title, got %s", fileMd.SectionTitle)
	}

	// Content should be combined
	if fileMd.Content != "First chunk\n\nSecond chunk" {
		t.Errorf("unexpected merged content: %q", fileMd.Content)
	}
}

func TestMergeNonAdjacentChunks(t *testing.T) {
	results := []SearchResult{
		{DocID: 1, RepoAlias: "repo", Path: "file.md", SectionTitle: "Section A", Content: "First", Tokens: 50, Score: -3.0},
		{DocID: 5, RepoAlias: "repo", Path: "file.md", SectionTitle: "Section B", Content: "Second", Tokens: 40, Score: -2.0},
	}

	merged := mergeAdjacentChunks(results)

	// DocID 1 and 5 are NOT adjacent (differ by more than 1)
	if len(merged) != 2 {
		t.Fatalf("expected 2 results (non-adjacent), got %d", len(merged))
	}
}

func TestRepoFilter(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()

	repoA := insertTestRepo(t, s, "repo-a", "https://example.com/a")
	repoB := insertTestRepo(t, s, "repo-b", "https://example.com/b")

	insertTestDocs(t, s, repoA, []store.Document{
		{Path: "doc.md", DocTitle: "Doc A", SectionTitle: "Section A", Content: "kubernetes deployment configuration", Tokens: 50, HeadingLevel: 2},
	})
	insertTestDocs(t, s, repoB, []store.Document{
		{Path: "doc.md", DocTitle: "Doc B", SectionTitle: "Section B", Content: "kubernetes service mesh networking", Tokens: 50, HeadingLevel: 2},
	})

	search := NewSearch(s)

	// Search with repo filter
	results, err := search.Query(SearchOptions{
		Query:       "kubernetes",
		RepoAlias:   "repo-a",
		Limit:       20,
		TokenBudget: 2000,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, r := range results {
		if r.RepoAlias != "repo-a" {
			t.Errorf("expected only repo-a results, got %s", r.RepoAlias)
		}
	}
}

func TestEmptyQuery(t *testing.T) {
	s := setupTestStore(t)
	defer s.Close()

	search := NewSearch(s)
	_, err := search.Query(SearchOptions{Query: ""})
	if err == nil {
		t.Fatal("expected error for empty query")
	}
}
