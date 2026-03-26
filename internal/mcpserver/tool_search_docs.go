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
	Query    string `json:"query" jsonschema:"Search query. Supports FTS5 syntax: phrases in quotes, AND/OR/NOT operators, prefix* matching"`
	Repo     string `json:"repo,omitempty" jsonschema:"Filter results to a specific repo alias. Omit to search all repos."`
	Tokens   int    `json:"tokens,omitempty" jsonschema:"Maximum tokens in response. Default 2000."`
	Page     int    `json:"page,omitempty" jsonschema:"Page number (1-indexed). Default 1."`
	PageSize int    `json:"page_size,omitempty" jsonschema:"Results per page. Default 10, max 50."`
}

// registerSearchDocsTool registers the search_docs tool on the MCP server.
func (s *Server) registerSearchDocsTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "search_docs",
		Description: "Search indexed documentation using full-text search. Searches across all indexed repos by default; use the repo parameter to narrow to a specific source. Supports FTS5 syntax: \"exact phrase\", term1 AND term2, prefix*. Use the tokens parameter to control response size (default 2000). Call list_repos first to see available repos, or browse_docs to explore by file structure.",
	}, s.handleSearchDocs)
}

// handleSearchDocs implements the search_docs tool handler.
func (s *Server) handleSearchDocs(_ context.Context, _ *mcp.CallToolRequest, input SearchDocsInput) (*mcp.CallToolResult, any, error) {
	tokenBudget := input.Tokens
	if tokenBudget <= 0 {
		tokenBudget = 2000
	}

	resp, err := s.search.Query(search.SearchOptions{
		Query:       input.Query,
		RepoAlias:   input.Repo,
		TokenBudget: tokenBudget,
		Page:        input.Page,
		PageSize:    input.PageSize,
	})
	if err != nil {
		errLower := strings.ToLower(err.Error())
		if strings.Contains(errLower, "fts5") || strings.Contains(errLower, "fts:") || strings.Contains(errLower, "fts syntax") {
			return nil, nil, fmt.Errorf(
				"search failed: the query contains characters that conflict with FTS5 search syntax "+
					"(the query language used by this search engine). "+
					"Common issues: dots (.) are column selectors, colons (:) are prefix operators, "+
					"parentheses group expressions. "+
					"To search terms containing these characters literally, wrap them in double quotes. "+
					"Example: \"app.settings.json\" instead of app.settings.json. "+
					"Details: %w", err)
		}
		return nil, nil, err
	}

	if len(resp.Results) == 0 {
		if resp.TotalResults == 0 {
			var text string
			if input.Repo != "" {
				text = fmt.Sprintf("No results for %q in alias %q. Try searching without a repo filter, or use list_repos to see available sources.", input.Query, input.Repo)
			} else {
				text = fmt.Sprintf("No results found for %q. Try different search terms or check available repos with list_repos.", input.Query)
			}
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: text}},
			}, nil, nil
		}
		text := fmt.Sprintf("Page %d is beyond the %d total results. Try a lower page number.",
			resp.Page, resp.TotalResults)
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, nil, nil
	}

	// Count total tokens across results
	var totalTokens int
	for _, r := range resp.Results {
		totalTokens += r.Tokens
	}

	totalPages := (resp.TotalResults + resp.PageSize - 1) / resp.PageSize

	var b strings.Builder
	fmt.Fprintf(&b, "Found %d results on page %d of %d (%d total, %d tokens)\n\n",
		len(resp.Results), resp.Page, totalPages, resp.TotalResults, totalTokens)

	for _, r := range resp.Results {
		title := r.SectionTitle
		if title == "" {
			title = r.DocTitle
		}
		fmt.Fprintf(&b, "## repo: %s | alias: %s | %s > %s\n%s\n\n---\n\n",
			DisplayRepo(r.RepoURL, ""), r.RepoAlias, r.Path, title, r.Content)
	}

	if resp.Page < totalPages {
		fmt.Fprintf(&b, "Page %d of %d (%d total results). Use page: %d to see more.\n",
			resp.Page, totalPages, resp.TotalResults, resp.Page+1)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: b.String()}},
	}, nil, nil
}
