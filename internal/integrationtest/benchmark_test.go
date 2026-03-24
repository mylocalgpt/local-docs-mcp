//go:build integration

package integrationtest

import (
	"testing"

	"github.com/mylocalgpt/local-docs-mcp/internal/search"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

func BenchmarkSearch(b *testing.B) {
	s, err := store.NewStore(testDBPath)
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	defer s.Close()

	srch := search.NewSearch(s)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := srch.Query(search.SearchOptions{
			Query:       "connect sync filtering",
			TokenBudget: 2000,
		})
		if err != nil {
			b.Fatalf("search: %v", err)
		}
	}
}

var benchQueries = []string{
	"connect sync filtering",
	"password hash synchronization",
	"staging server",
	"troubleshoot sync errors",
	"cloud sync provisioning",
}

func BenchmarkSearchVariedQueries(b *testing.B) {
	s, err := store.NewStore(testDBPath)
	if err != nil {
		b.Fatalf("open store: %v", err)
	}
	defer s.Close()

	srch := search.NewSearch(s)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		q := benchQueries[i%len(benchQueries)]
		_, err := srch.Query(search.SearchOptions{Query: q, TokenBudget: 2000})
		if err != nil {
			b.Fatalf("search %q: %v", q, err)
		}
	}
}
