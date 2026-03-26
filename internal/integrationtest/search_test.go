//go:build integration

package integrationtest

import (
	"strings"
	"testing"

	"github.com/mylocalgpt/local-docs-mcp/internal/search"
)

var searchTests = []struct {
	query       string
	expectFile  string // substring of expected top-result path
	expectTitle string // substring of expected top-result title
}{
	{"connect sync filtering", "filtering", "filter"},
	{"staging server", "staging", "staging"},
	{"password hash synchronization", "password-hash", "password"},
	{"declarative provisioning", "declarative-provisioning", "declarative"},
	{"cloud sync vs connect sync", "hybrid", ""},
	{"troubleshoot sync errors", "troubleshoot", "troubleshoot"},
}

func TestSearchKnownQueries(t *testing.T) {
	for _, tt := range searchTests {
		t.Run(tt.query, func(t *testing.T) {
			s := openTestStore(t)
			srch := search.NewSearch(s)

			resp, err := srch.Query(search.SearchOptions{
				Query:       tt.query,
				TokenBudget: 2000,
			})
			if err != nil {
				t.Fatalf("search error: %v", err)
			}
			if len(resp.Results) == 0 {
				t.Fatal("expected at least 1 result, got 0")
			}

			top := resp.Results[0]
			t.Logf("top result: path=%s title=%s section=%s score=%.4f", top.Path, top.DocTitle, top.SectionTitle, top.Score)

			if tt.expectFile != "" && !strings.Contains(top.Path, tt.expectFile) {
				t.Logf("warning: expected top result path to contain %q, got %q", tt.expectFile, top.Path)
			}
			if tt.expectTitle != "" {
				titleMatch := strings.Contains(strings.ToLower(top.DocTitle), strings.ToLower(tt.expectTitle)) ||
					strings.Contains(strings.ToLower(top.SectionTitle), strings.ToLower(tt.expectTitle))
				if !titleMatch {
					t.Logf("warning: expected title containing %q, got doc=%q section=%q", tt.expectTitle, top.DocTitle, top.SectionTitle)
				}
			}
		})
	}
}

func TestSearchRelevanceFilter(t *testing.T) {
	s := openTestStore(t)
	srch := search.NewSearch(s)

	resp, err := srch.Query(search.SearchOptions{
		Query:       "sync",
		TokenBudget: 4000,
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(resp.Results) < 2 {
		t.Fatalf("expected multiple results for broad query 'sync', got %d", len(resp.Results))
	}

	bestScore := resp.Results[0].Score
	threshold := bestScore * 0.5
	t.Logf("best score: %.4f, threshold: %.4f, results: %d", bestScore, threshold, len(resp.Results))

	for _, r := range resp.Results {
		if r.Score > threshold {
			t.Errorf("result %q score %.4f outside relevance window (best: %.4f, threshold: %.4f)", r.Path, r.Score, bestScore, threshold)
		}
	}
}

func TestSearchTokenBudget(t *testing.T) {
	s := openTestStore(t)
	srch := search.NewSearch(s)

	resp, err := srch.Query(search.SearchOptions{
		Query:       "connect sync",
		TokenBudget: 500,
	})
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected results, got 0")
	}

	var total int
	for _, r := range resp.Results {
		total += r.Tokens
	}
	t.Logf("results: %d, total tokens: %d", len(resp.Results), total)

	if len(resp.Results) > 1 {
		withoutLast := total - resp.Results[len(resp.Results)-1].Tokens
		if withoutLast > 500 {
			t.Errorf("token budget violated: %d tokens before last result (budget: 500)", withoutLast)
		}
	}
}

func TestSearchAdjacentMerge(t *testing.T) {
	s := openTestStore(t)
	srch := search.NewSearch(s)

	resp, err := srch.Query(search.SearchOptions{
		Query:       "connect sync",
		TokenBudget: 4000,
		Limit:       50,
	})
	if err != nil {
		t.Fatalf("search error: %v", err)
	}
	if len(resp.Results) == 0 {
		t.Fatal("expected results, got 0")
	}

	// Log merge indicators for manual inspection
	for i, r := range resp.Results {
		if i >= 5 {
			break
		}
		t.Logf("result %d: path=%s section=%s tokens=%d score=%.4f", i, r.Path, r.SectionTitle, r.Tokens, r.Score)
	}
	t.Logf("total results returned: %d", len(resp.Results))
}

func TestSearchNonexistentRepo(t *testing.T) {
	s := openTestStore(t)
	srch := search.NewSearch(s)

	_, err := srch.Query(search.SearchOptions{
		Query:     "connect sync",
		RepoAlias: "nonexistent",
	})
	if err == nil {
		t.Fatal("expected error for nonexistent repo, got nil")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("expected 'not found' in error, got: %v", err)
	}
}
