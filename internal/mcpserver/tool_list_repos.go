package mcpserver

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// ListReposInput defines the input schema for the list_repos tool.
type ListReposInput struct{}

// registerListReposTool registers the list_repos tool on the MCP server.
func (s *Server) registerListReposTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "list_repos",
		Description: "List all indexed documentation repos with their status. Call this first to see what documentation is available before searching.",
	}, s.handleListRepos)
}

// handleListRepos implements the list_repos tool handler.
func (s *Server) handleListRepos(_ context.Context, _ *mcp.CallToolRequest, _ ListReposInput) (*mcp.CallToolResult, any, error) {
	repos, err := s.store.ListRepos()
	if err != nil {
		return nil, nil, err
	}

	if len(repos) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{
				Text: "No documentation repos indexed yet. Use update_docs to trigger indexing, or run 'local-docs-mcp index --config <path>' from the command line.",
			}},
		}, nil, nil
	}

	var b strings.Builder
	b.WriteString("Indexed repositories:\n")

	for _, r := range repos {
		staleness := computeStaleness(r.IndexedAt)
		fmt.Fprintf(&b, "\n%s (%d docs, %s)\n", r.Alias, r.DocCount, staleness)
		fmt.Fprintf(&b, "  URL: %s\n", r.URL)
		fmt.Fprintf(&b, "  Last indexed: %s\n", r.IndexedAt)
	}

	fmt.Fprintf(&b, "\n%d repos indexed. Use search_docs to search, browse_docs to explore, update_docs to refresh stale repos.\n", len(repos))

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: b.String()}},
	}, nil, nil
}

// computeStaleness returns a human-readable staleness indicator for a repo.
func computeStaleness(indexedAt string) string {
	if indexedAt == "" {
		return "stale - never indexed"
	}

	t, err := time.Parse(time.RFC3339, indexedAt)
	if err != nil {
		return "stale - unknown age"
	}

	age := time.Since(t)
	if age <= 24*time.Hour {
		return "current"
	}

	days := int(math.Round(age.Hours() / 24))
	if days == 1 {
		return "stale - 1 day ago"
	}
	return fmt.Sprintf("stale - %d days ago", days)
}
