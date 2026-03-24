package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mylocalgpt/local-docs-mcp/internal/search"
)

// SearchDocsInput defines the input schema for the search_docs tool.
type SearchDocsInput struct {
	Query  string `json:"query" jsonschema:"Search query. Supports FTS5 syntax: phrases in quotes, AND/OR/NOT operators, prefix* matching"`
	Repo   string `json:"repo,omitempty" jsonschema:"Filter results to a specific repo alias. Omit to search all repos."`
	Tokens int    `json:"tokens,omitempty" jsonschema:"Maximum tokens in response. Default 2000."`
}

// registerSearchDocsTool registers the search_docs tool on the MCP server.
func (s *Server) registerSearchDocsTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "search_docs",
		Description: "Search indexed documentation using full-text search. Supports FTS5 syntax: \"exact phrase\", term1 AND term2, prefix*. Use the tokens parameter to control response size (default 2000). Call list_repos first to see available repos, or browse_docs to explore by file structure.",
	}, s.handleSearchDocs)
}

// handleSearchDocs implements the search_docs tool handler.
func (s *Server) handleSearchDocs(_ context.Context, _ *mcp.CallToolRequest, input SearchDocsInput) (*mcp.CallToolResult, any, error) {
	tokenBudget := input.Tokens
	if tokenBudget <= 0 {
		tokenBudget = 2000
	}

	results, err := s.search.Query(search.SearchOptions{
		Query:       input.Query,
		RepoAlias:   input.Repo,
		TokenBudget: tokenBudget,
	})
	if err != nil {
		errLower := strings.ToLower(err.Error())
		if strings.Contains(errLower, "fts5") || strings.Contains(errLower, "fts:") || strings.Contains(errLower, "fts syntax") {
			return nil, nil, fmt.Errorf("search syntax error: %v. Try quoting your query or simplifying boolean operators", err)
		}
		return nil, nil, err
	}

	if len(results) == 0 {
		text := fmt.Sprintf("No results found for %q. Try different search terms or check available repos with list_repos.", input.Query)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, nil, nil
	}

	// Count total tokens across results
	var totalTokens int
	for _, r := range results {
		totalTokens += r.Tokens
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d results (%d tokens)\n\n", len(results), totalTokens)

	for _, r := range results {
		title := r.SectionTitle
		if title == "" {
			title = r.DocTitle
		}
		fmt.Fprintf(&b, "## %s: %s > %s\n%s\n\n---\n\n", r.RepoAlias, r.Path, title, r.Content)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: b.String()}},
	}, nil, nil
}
