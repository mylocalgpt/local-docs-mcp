package search

import (
	"fmt"
	"sort"

	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

// Search provides FTS5 search with BM25 ranking, relevance filtering,
// adjacent chunk merging, and token budgeting.
type Search struct {
	store *store.Store
}

// SearchOptions configures a search query.
type SearchOptions struct {
	Query       string
	RepoAlias   string // empty = all repos
	Limit       int    // max raw results before filtering, default 20
	TokenBudget int    // max total tokens in response, default 2000
}

// SearchResult holds a single processed search result.
type SearchResult struct {
	DocID        int64   // documents.id, needed for adjacency detection
	RepoAlias    string
	Path         string
	DocTitle     string
	SectionTitle string
	Content      string
	Excerpt      string
	HeadingLevel int
	Tokens       int
	Score        float64
}

// NewSearch creates a Search instance backed by the given store.
func NewSearch(s *store.Store) *Search {
	return &Search{store: s}
}

// Query runs the full search pipeline: FTS5 query, relevance filter,
// adjacent chunk merging, and token budgeting.
func (s *Search) Query(opts SearchOptions) ([]SearchResult, error) {
	if opts.Query == "" {
		return nil, fmt.Errorf("search query must not be empty")
	}
	if opts.Limit <= 0 {
		opts.Limit = 20
	}
	if opts.TokenBudget <= 0 {
		opts.TokenBudget = 2000
	}

	// 1. Resolve repo filter
	var repoID *int64
	if opts.RepoAlias != "" {
		repo, err := s.store.GetRepo(opts.RepoAlias)
		if err != nil {
			return nil, fmt.Errorf("look up repo %q: %w", opts.RepoAlias, err)
		}
		if repo == nil {
			return nil, fmt.Errorf("repo %q not found", opts.RepoAlias)
		}
		repoID = &repo.ID
	}

	// 2. Execute FTS5 query
	raw, err := s.store.SearchFTS(opts.Query, repoID, opts.Limit)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, nil
	}

	// Convert raw results to SearchResult
	results := make([]SearchResult, len(raw))
	for i, r := range raw {
		results[i] = SearchResult{
			DocID:        r.DocID,
			RepoAlias:    r.RepoAlias,
			Path:         r.Path,
			DocTitle:     r.DocTitle,
			SectionTitle: r.SectionTitle,
			Content:      r.Content,
			Excerpt:      r.Excerpt,
			HeadingLevel: r.HeadingLevel,
			Tokens:       r.Tokens,
			Score:        r.Score,
		}
	}

	// 3. Relevance filter
	results = applyRelevanceFilter(results)

	// 4. Merge adjacent chunks
	results = mergeAdjacentChunks(results)

	// 5. Apply token budget
	results = applyTokenBudget(results, opts.TokenBudget)

	return results, nil
}

// applyRelevanceFilter drops results below 50% of the top score.
// BM25 scores are negative; lower (more negative) = better match.
// threshold = topScore * 0.5. Keep results where score <= threshold.
// Example: best=-4.0, threshold=-2.0, keep -4.0, -3.5, -2.1; drop -1.5, -0.8.
func applyRelevanceFilter(results []SearchResult) []SearchResult {
	if len(results) <= 1 {
		return results
	}

	topScore := results[0].Score // best (most negative)
	threshold := topScore * 0.5

	var filtered []SearchResult
	for _, r := range results {
		if r.Score <= threshold {
			filtered = append(filtered, r)
		}
	}
	// Always keep at least one result
	if len(filtered) == 0 {
		return results[:1]
	}
	return filtered
}

// mergeAdjacentChunks merges consecutive chunks from the same document.
// Adjacency is detected by consecutive DocID values within the same
// (RepoAlias, Path) group.
func mergeAdjacentChunks(results []SearchResult) []SearchResult {
	if len(results) <= 1 {
		return results
	}

	// Group results by (RepoAlias, Path)
	type groupKey struct {
		RepoAlias string
		Path      string
	}
	groups := make(map[groupKey][]SearchResult)
	var keyOrder []groupKey

	for _, r := range results {
		k := groupKey{r.RepoAlias, r.Path}
		if _, exists := groups[k]; !exists {
			keyOrder = append(keyOrder, k)
		}
		groups[k] = append(groups[k], r)
	}

	var merged []SearchResult
	for _, k := range keyOrder {
		group := groups[k]
		// Sort by DocID within each group for adjacency detection
		sort.Slice(group, func(i, j int) bool {
			return group[i].DocID < group[j].DocID
		})

		current := group[0]
		for i := 1; i < len(group); i++ {
			if group[i].DocID == current.DocID+1 {
				// Adjacent: merge
				current.Content += "\n\n" + group[i].Content
				current.Tokens += group[i].Tokens
				// Keep the better (lower) score
				if group[i].Score < current.Score {
					current.Score = group[i].Score
				}
				// Update DocID to the latest for continued adjacency detection
				current.DocID = group[i].DocID
			} else {
				merged = append(merged, current)
				current = group[i]
			}
		}
		merged = append(merged, current)
	}

	// Re-sort by score (lower = better)
	sort.Slice(merged, func(i, j int) bool {
		return merged[i].Score < merged[j].Score
	})

	return merged
}

// applyTokenBudget walks results in score order and stops when the token
// budget is exceeded. The result that crosses the boundary is included.
func applyTokenBudget(results []SearchResult, budget int) []SearchResult {
	if budget <= 0 {
		return results
	}

	var total int
	for i, r := range results {
		total += r.Tokens
		if total > budget {
			// Include the result that crosses the boundary
			return results[:i+1]
		}
	}
	return results
}
