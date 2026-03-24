package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mylocalgpt/local-docs-mcp/internal/indexer"
)

// UpdateDocsInput defines the input schema for the update_docs tool.
type UpdateDocsInput struct {
	Repo string `json:"repo,omitempty" jsonschema:"Specific repo alias to update. Omit to update all configured repos."`
}

// registerUpdateDocsTool registers the update_docs tool on the MCP server.
func (s *Server) registerUpdateDocsTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "update_docs",
		Description: "Re-index documentation from git repos. Pulls latest changes and checks for new commits, only re-indexing if the repo has changed. Call this if docs seem stale or outdated. Only one update can run at a time.",
	}, s.handleUpdateDocs)
}

// handleUpdateDocs implements the update_docs tool handler.
func (s *Server) handleUpdateDocs(_ context.Context, _ *mcp.CallToolRequest, input UpdateDocsInput) (*mcp.CallToolResult, any, error) {
	if s.indexer == nil {
		return nil, nil, fmt.Errorf("indexer not available")
	}

	if !s.indexMu.TryLock() {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{
				Text: "Re-indexing already in progress, please wait and try again.",
			}},
		}, nil, nil
	}
	defer s.indexMu.Unlock()

	if input.Repo != "" {
		return s.updateSingleRepo(input.Repo)
	}
	return s.updateAllRepos()
}

// updateSingleRepo re-indexes a single repo by alias.
func (s *Server) updateSingleRepo(alias string) (*mcp.CallToolResult, any, error) {
	// Find matching config
	for _, cfg := range s.config.Repos {
		if cfg.Alias != alias {
			continue
		}

		start := time.Now()
		result, err := s.indexer.IndexRepo(cfg, false)
		if err != nil {
			return nil, nil, fmt.Errorf("indexing %s: %w", alias, err)
		}

		// Rebuild FTS after single-repo index for consistency
		if err := s.store.RebuildFTS(); err != nil {
			return nil, nil, fmt.Errorf("rebuild fts: %w", err)
		}

		var b strings.Builder
		b.WriteString("Update results:\n\n")
		s.formatResult(&b, result)
		duration := time.Since(start).Round(time.Millisecond)

		if result.Error != nil {
			fmt.Fprintf(&b, "\n1 repo checked in %s. 0 updated, 1 error.\n", duration)
		} else if result.Skipped {
			fmt.Fprintf(&b, "\n1 repo checked in %s. 0 updated, 1 unchanged.\n", duration)
		} else {
			fmt.Fprintf(&b, "\n1 repo checked in %s. 1 updated, 0 unchanged.\n", duration)
		}

		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: b.String()}},
		}, nil, nil
	}

	return nil, nil, fmt.Errorf("Repo %q not found in configuration.", alias)
}

// updateAllRepos re-indexes all configured repos.
func (s *Server) updateAllRepos() (*mcp.CallToolResult, any, error) {
	results, err := s.indexer.IndexAll(s.config, false)
	if err != nil {
		return nil, nil, fmt.Errorf("index all: %w", err)
	}

	var b strings.Builder
	b.WriteString("Update results:\n\n")

	updated := 0
	unchanged := 0
	errors := 0

	for i := range results {
		s.formatResult(&b, &results[i])
		if results[i].Error != nil {
			errors++
		} else if results[i].Skipped {
			unchanged++
		} else {
			updated++
		}
	}

	total := len(results)
	fmt.Fprintf(&b, "\n%d repos checked.", total)
	if errors > 0 {
		fmt.Fprintf(&b, " %d updated, %d unchanged, %d errors.\n", updated, unchanged, errors)
	} else {
		fmt.Fprintf(&b, " %d updated, %d unchanged.\n", updated, unchanged)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: b.String()}},
	}, nil, nil
}

// formatResult writes a single repo result line to the builder.
func (s *Server) formatResult(b *strings.Builder, r *indexer.IndexResult) {
	if r.Error != nil {
		fmt.Fprintf(b, "%s: error - %v\n", r.Repo, r.Error)
		return
	}

	if r.Skipped {
		fmt.Fprintf(b, "%s: skipped (unchanged)\n", r.Repo)
		return
	}

	// Get commit SHA from store after successful indexing
	commitInfo := ""
	repo, err := s.store.GetRepo(r.Repo)
	if err == nil && repo != nil && repo.CommitSHA != "" {
		commitInfo = fmt.Sprintf(" (commit %s)", truncateSHA(repo.CommitSHA))
	}

	fmt.Fprintf(b, "%s: indexed %d docs in %s%s\n", r.Repo, r.DocsIndexed, r.Duration.Round(time.Millisecond), commitInfo)
}

// truncateSHA returns the first 7 characters of a SHA, or the full string if shorter.
func truncateSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}
