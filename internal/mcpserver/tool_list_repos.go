package mcpserver

import (
	"context"
	"fmt"
	"math"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

// ListReposInput defines the input schema for the list_repos tool.
type ListReposInput struct{}

// registerListReposTool registers the list_repos tool on the MCP server.
func (s *Server) registerListReposTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "list_repos",
		Description: "List all indexed documentation repos with their status and doc counts. Also shows indexing progress for background add_docs operations. Call this first to discover what documentation is available before using search_docs or browse_docs.",
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
				Text: "No documentation indexed yet. Use add_docs to add documentation sources, or run 'local-docs-mcp index --config <path>' from the command line.",
			}},
		}, nil, nil
	}

	var b strings.Builder
	b.WriteString("Indexed repositories:\n")

	for _, r := range repos {
		statusText := formatRepoStatus(&r)
		contentSize, _ := s.store.RepoContentSize(r.ID)

		fmt.Fprintf(&b, "\n%s [%s] (%d docs, %s)\n", r.Alias, r.SourceType, r.DocCount, formatBytes(contentSize))
		fmt.Fprintf(&b, "  URL: %s\n", r.URL)
		fmt.Fprintf(&b, "  Status: %s\n", statusText)
		if r.IndexedAt != "" {
			fmt.Fprintf(&b, "  Last indexed: %s\n", r.IndexedAt)
		}
	}

	// Show DB file size
	dbPath := s.store.DBPath()
	if dbPath != "" && dbPath != ":memory:" {
		if info, err := os.Stat(dbPath); err == nil {
			fmt.Fprintf(&b, "\nDatabase: %s (%s)\n", dbPath, formatBytes(info.Size()))
		}
	}

	fmt.Fprintf(&b, "\n%d repos indexed. Use search_docs to search, browse_docs to explore, update_docs to refresh, add_docs to add new sources.\n", len(repos))

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: b.String()}},
	}, nil, nil
}

// formatRepoStatus returns a human-readable status string for a repo.
func formatRepoStatus(r *store.Repo) string {
	switch r.Status {
	case store.StatusIndexing:
		if r.StatusUpdatedAt != "" {
			if t, err := time.Parse(time.RFC3339, r.StatusUpdatedAt); err == nil {
				elapsed := time.Since(t).Round(time.Second)
				return fmt.Sprintf("indexing (started %s ago)", elapsed)
			}
		}
		return "indexing"
	case store.StatusError:
		if r.StatusDetail != "" {
			return fmt.Sprintf("error: %s", r.StatusDetail)
		}
		return "error"
	default:
		return computeStaleness(r.IndexedAt)
	}
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

// formatBytes formats a byte count as a human-readable string.
func formatBytes(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%d B", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1f KB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1f MB", float64(bytes)/(1024*1024))
}
