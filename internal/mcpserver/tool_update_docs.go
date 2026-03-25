package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mylocalgpt/local-docs-mcp/internal/config"
	"github.com/mylocalgpt/local-docs-mcp/internal/indexer"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

// UpdateDocsInput defines the input schema for the update_docs tool.
type UpdateDocsInput struct {
	Repo string `json:"repo,omitempty" jsonschema:"Specific repo alias to update. Omit to update all repos."`
}

// registerUpdateDocsTool registers the update_docs tool on the MCP server.
func (s *Server) registerUpdateDocsTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "update_docs",
		Description: "Re-index documentation. Pulls latest changes for git repos and re-scans local directories. Only re-indexes git repos if the commit has changed. Use the repo parameter to target a specific source. Only one update can run at a time.",
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

// updateSingleRepo re-indexes a single repo by alias, loading from DB.
func (s *Server) updateSingleRepo(alias string) (*mcp.CallToolResult, any, error) {
	repo, err := s.store.GetRepo(alias)
	if err != nil {
		return nil, nil, fmt.Errorf("looking up repo: %w", err)
	}
	if repo == nil {
		return nil, nil, fmt.Errorf("repo %q not found", alias)
	}

	start := time.Now()
	result := s.indexRepoByType(repo, false)

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

// updateAllRepos re-indexes all repos from the database.
func (s *Server) updateAllRepos() (*mcp.CallToolResult, any, error) {
	repos, err := s.store.ListRepos()
	if err != nil {
		return nil, nil, fmt.Errorf("list repos: %w", err)
	}

	var results []indexer.IndexResult

	for i := range repos {
		// Skip repos currently being indexed
		if repos[i].Status == store.StatusIndexing {
			results = append(results, indexer.IndexResult{
				Repo:    repos[i].Alias,
				Skipped: true,
			})
			continue
		}

		r := s.indexRepoByType(&repos[i], false)
		results = append(results, *r)
	}

	if err := s.store.RebuildFTS(); err != nil {
		return nil, nil, fmt.Errorf("rebuild fts: %w", err)
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

// indexRepoByType indexes a repo based on its source type (git or local).
func (s *Server) indexRepoByType(repo *store.Repo, force bool) *indexer.IndexResult {
	if repo.SourceType == "local" {
		result, err := s.indexer.IndexLocalPath(repo.Alias, repo.URL)
		if err != nil {
			return &indexer.IndexResult{Repo: repo.Alias, Error: err}
		}
		return result
	}

	// Git source: parse paths and construct config
	var paths []string
	if err := json.Unmarshal([]byte(repo.Paths), &paths); err != nil {
		return &indexer.IndexResult{Repo: repo.Alias, Error: fmt.Errorf("parse paths: %w", err)}
	}

	cfg := config.RepoConfig{
		Alias: repo.Alias,
		URL:   repo.URL,
		Paths: paths,
	}
	result, err := s.indexer.IndexRepo(cfg, force)
	if err != nil {
		return &indexer.IndexResult{Repo: repo.Alias, Error: err}
	}
	return result
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
