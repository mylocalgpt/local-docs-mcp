package search

import (
	"testing"

	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

// setupIntegrationStore creates a store with two repos and various documents.
func setupIntegrationStore(t *testing.T) (*store.Store, int64, int64) {
	t.Helper()
	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	repoA, err := s.UpsertRepo("repo-a", "https://example.com/a", `["docs"]`, "git")
	if err != nil {
		t.Fatalf("upsert repo-a: %v", err)
	}
	if err := s.UpdateRepoIndex(repoA, "aaa1111", "2026-03-24T10:00:00Z", 0); err != nil {
		t.Fatalf("update repo-a index: %v", err)
	}

	repoB, err := s.UpsertRepo("repo-b", "https://example.com/b", `["docs"]`, "git")
	if err != nil {
		t.Fatalf("upsert repo-b: %v", err)
	}
	if err := s.UpdateRepoIndex(repoB, "bbb2222", "2026-03-24T10:01:00Z", 0); err != nil {
		t.Fatalf("update repo-b index: %v", err)
	}

	// repo-a: 3 files, including sequential chunks for merge testing
	docsA := []store.Document{
		{RepoID: repoA, Path: "docs/guide.md", DocTitle: "Guide", SectionTitle: "Getting Started", Content: "Welcome to the kubernetes deployment guide for configuring sync services", Tokens: 100, HeadingLevel: 2},
		{RepoID: repoA, Path: "docs/guide.md", DocTitle: "Guide", SectionTitle: "Installation", Content: "Install the kubernetes operator using helm chart for sync deployment", Tokens: 80, HeadingLevel: 3},
		{RepoID: repoA, Path: "docs/guide.md", DocTitle: "Guide", SectionTitle: "Quick Start", Content: "Run the following commands to start your first deployment", Tokens: 60, HeadingLevel: 3},
		{RepoID: repoA, Path: "docs/reference.md", DocTitle: "Reference", SectionTitle: "API Overview", Content: "The REST API provides kubernetes cluster management endpoints", Tokens: 120, HeadingLevel: 2},
		{RepoID: repoA, Path: "docs/faq.md", DocTitle: "FAQ", SectionTitle: "Common Issues", Content: "Troubleshooting common problems with database connections", Tokens: 90, HeadingLevel: 2},
	}
	if err := s.ReplaceDocuments(repoA, docsA); err != nil {
		t.Fatalf("replace docs repo-a: %v", err)
	}

	// repo-b: 2 files
	docsB := []store.Document{
		{RepoID: repoB, Path: "docs/setup.md", DocTitle: "Setup", SectionTitle: "Prerequisites", Content: "You need kubernetes version 1.24 or later installed on your cluster", Tokens: 70, HeadingLevel: 2},
		{RepoID: repoB, Path: "docs/networking.md", DocTitle: "Networking", SectionTitle: "Service Mesh", Content: "Configure the service mesh for inter-pod networking communication", Tokens: 85, HeadingLevel: 2},
	}
	if err := s.ReplaceDocuments(repoB, docsB); err != nil {
		t.Fatalf("replace docs repo-b: %v", err)
	}

	// Update doc counts
	s.UpdateRepoIndex(repoA, "aaa1111", "2026-03-24T10:00:00Z", len(docsA))
	s.UpdateRepoIndex(repoB, "bbb2222", "2026-03-24T10:01:00Z", len(docsB))

	return s, repoA, repoB
}

func TestIntegrationSearchPipeline(t *testing.T) {
	s, _, _ := setupIntegrationStore(t)
	defer s.Close()

	srch := NewSearch(s)

	// Search for kubernetes - should find results from both repos
	resp, err := srch.Query(SearchOptions{
		Query:       "kubernetes",
		Limit:       20,
		TokenBudget: 2000,
	})
	if err != nil {
		t.Fatalf("search kubernetes: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected results for 'kubernetes'")
	}

	// Verify results come from both repos
	repos := make(map[string]bool)
	for _, r := range resp.Results {
		repos[r.RepoAlias] = true
	}
	if !repos["repo-a"] || !repos["repo-b"] {
		t.Errorf("expected results from both repos, got: %v", repos)
	}
}

func TestIntegrationSearchRepoFilter(t *testing.T) {
	s, _, _ := setupIntegrationStore(t)
	defer s.Close()

	srch := NewSearch(s)

	// Search with repo-b filter
	resp, err := srch.Query(SearchOptions{
		Query:       "kubernetes",
		RepoAlias:   "repo-b",
		Limit:       20,
		TokenBudget: 2000,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	for _, r := range resp.Results {
		if r.RepoAlias != "repo-b" {
			t.Errorf("expected only repo-b results, got %s", r.RepoAlias)
		}
	}
}

func TestIntegrationSearchTokenBudget(t *testing.T) {
	s, _, _ := setupIntegrationStore(t)
	defer s.Close()

	srch := NewSearch(s)

	// Search with very low token budget
	resp, err := srch.Query(SearchOptions{
		Query:       "kubernetes",
		Limit:       20,
		TokenBudget: 100,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	// Calculate total tokens
	var total int
	for _, r := range resp.Results {
		total += r.Tokens
	}

	// Total should be reasonable (within budget + last result that crossed)
	if len(resp.Results) > 0 {
		lastTokens := resp.Results[len(resp.Results)-1].Tokens
		if total-lastTokens > 100 {
			t.Errorf("token budget not enforced: total %d (without last result's %d = %d, expected <= 100)",
				total, lastTokens, total-lastTokens)
		}
	}
}

func TestIntegrationSearchAdjacentMerge(t *testing.T) {
	s, _, _ := setupIntegrationStore(t)
	defer s.Close()

	srch := NewSearch(s)

	// Search for "deployment" which appears in adjacent chunks in repo-a guide.md
	resp, err := srch.Query(SearchOptions{
		Query:       "deployment",
		Limit:       20,
		TokenBudget: 2000,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	// Check that if results from the same file exist, they may have been merged
	// (we can't guarantee merging since it depends on whether adjacent chunks
	// both matched, but we verify no errors occur)
	if len(resp.Results) == 0 {
		t.Fatal("expected results for 'deployment'")
	}
}

func TestIntegrationListRepos(t *testing.T) {
	s, _, _ := setupIntegrationStore(t)
	defer s.Close()

	repos, err := s.ListRepos()
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(repos))
	}
	// Alphabetical order
	if repos[0].Alias != "repo-a" || repos[1].Alias != "repo-b" {
		t.Errorf("wrong order: %s, %s", repos[0].Alias, repos[1].Alias)
	}
	if repos[0].DocCount != 5 {
		t.Errorf("repo-a doc count: expected 5, got %d", repos[0].DocCount)
	}
	if repos[1].DocCount != 2 {
		t.Errorf("repo-b doc count: expected 2, got %d", repos[1].DocCount)
	}
}

func TestIntegrationBrowse(t *testing.T) {
	s, repoA, _ := setupIntegrationStore(t)
	defer s.Close()

	// Browse files
	files, _, err := s.BrowseFiles(repoA, 1, 1000)
	if err != nil {
		t.Fatalf("browse files: %v", err)
	}
	if len(files) != 3 {
		t.Fatalf("expected 3 files in repo-a, got %d", len(files))
	}

	// Browse headings for guide.md
	headings, err := s.BrowseHeadings(repoA, "docs/guide.md")
	if err != nil {
		t.Fatalf("browse headings: %v", err)
	}
	if len(headings) != 3 {
		t.Fatalf("expected 3 headings in guide.md, got %d", len(headings))
	}
	if headings[0].SectionTitle != "Getting Started" {
		t.Errorf("first heading: expected 'Getting Started', got %q", headings[0].SectionTitle)
	}
}

func TestIntegrationDeleteRepo(t *testing.T) {
	s, _, _ := setupIntegrationStore(t)
	defer s.Close()

	// Delete repo-a
	count, err := s.DeleteRepo("repo-a")
	if err != nil {
		t.Fatalf("delete repo: %v", err)
	}
	if count != 5 {
		t.Errorf("expected 5 deleted docs, got %d", count)
	}

	// Verify list only shows repo-b
	repos, err := s.ListRepos()
	if err != nil {
		t.Fatalf("list repos: %v", err)
	}
	if len(repos) != 1 || repos[0].Alias != "repo-b" {
		t.Errorf("expected only repo-b, got %v", repos)
	}

	// Verify search returns no repo-a results
	srch := NewSearch(s)
	resp, err := srch.Query(SearchOptions{
		Query:       "kubernetes",
		Limit:       20,
		TokenBudget: 2000,
	})
	if err != nil {
		t.Fatalf("search after delete: %v", err)
	}
	for _, r := range resp.Results {
		if r.RepoAlias == "repo-a" {
			t.Error("found repo-a result after deletion")
		}
	}
}

func TestIntegrationRelevanceFilter(t *testing.T) {
	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer s.Close()

	repoID, _ := s.UpsertRepo("test", "https://example.com/test", `["docs"]`, "git")

	// Insert docs with varying relevance to "kubernetes"
	docs := []store.Document{
		{RepoID: repoID, Path: "a.md", DocTitle: "Kubernetes Guide", SectionTitle: "Kubernetes Setup", Content: "kubernetes kubernetes kubernetes deployment cluster", Tokens: 50, HeadingLevel: 2},
		{RepoID: repoID, Path: "b.md", DocTitle: "Other", SectionTitle: "Other Topic", Content: "something completely unrelated to cloud computing but mentions kubernetes once", Tokens: 50, HeadingLevel: 2},
	}
	if err := s.ReplaceDocuments(repoID, docs); err != nil {
		t.Fatalf("replace docs: %v", err)
	}

	srch := NewSearch(s)
	resp, err := srch.Query(SearchOptions{
		Query:       "kubernetes",
		Limit:       20,
		TokenBudget: 2000,
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}

	// The highly relevant result should always be present
	if len(resp.Results) == 0 {
		t.Fatal("expected at least one result")
	}
	if resp.Results[0].Path != "a.md" {
		t.Errorf("expected most relevant result from a.md, got %s", resp.Results[0].Path)
	}
}
