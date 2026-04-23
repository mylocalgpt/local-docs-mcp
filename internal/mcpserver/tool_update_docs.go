package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mylocalgpt/local-docs-mcp/internal/indexer"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

// UpdateDocsInput defines the input schema for the update_docs tool.
type UpdateDocsInput struct {
	Repo *string `json:"repo,omitempty" jsonschema:"Specific repo alias to update. Omit to update all repos."`
}

// registerUpdateDocsTool registers the update_docs tool on the MCP server.
func (s *Server) registerUpdateDocsTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "update_docs",
		Description: "Re-index documentation. Pulls latest changes for git repos and re-scans local directories. Only re-indexes git repos if the commit has changed. Use the repo parameter to target a specific source. Calls are queued and run in order; user calls take priority over background auto-refresh.",
	}, s.handleUpdateDocs)
}

// handleUpdateDocs implements the update_docs tool handler.
func (s *Server) handleUpdateDocs(ctx context.Context, _ *mcp.CallToolRequest, input UpdateDocsInput) (*mcp.CallToolResult, any, error) {
	if s.indexer == nil {
		return nil, nil, fmt.Errorf("indexer not available")
	}

	if input.Repo != nil && *input.Repo != "" {
		return s.updateSingleRepo(ctx, *input.Repo)
	}
	return s.updateAllRepos(ctx)
}

// buildJobFromRepo constructs a queue Job for the given repo row using the
// supplied priority. Returns an error if the git paths column is malformed.
func buildJobFromRepo(repo *store.Repo, priority JobPriority) (*Job, error) {
	job := &Job{
		Alias:       repo.Alias,
		Kind:        kindFromSourceType(repo.SourceType),
		URL:         repo.URL,
		Force:       false,
		Priority:    priority,
		PriorStatus: repo.Status,
		RepoID:      repo.ID,
	}
	if repo.SourceType != "local" {
		var paths []string
		if err := json.Unmarshal([]byte(repo.Paths), &paths); err != nil {
			return nil, fmt.Errorf("parse paths: %w", err)
		}
		job.Paths = paths
	}
	return job, nil
}

// updateSingleRepo re-indexes a single repo by alias, loading from DB.
func (s *Server) updateSingleRepo(ctx context.Context, alias string) (*mcp.CallToolResult, any, error) {
	repo, err := s.store.GetRepo(alias)
	if err != nil {
		return nil, nil, fmt.Errorf("looking up repo: %w", err)
	}
	if repo == nil {
		return nil, nil, fmt.Errorf("repo %q not found", alias)
	}

	job, err := buildJobFromRepo(repo, priorityUser)
	if err != nil {
		return nil, nil, err
	}

	done, position, _, _, enqErr := s.queue.enqueue(job)
	if enqErr != nil {
		if errors.Is(enqErr, errQueueFull) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: enqErr.Error()}},
			}, nil, nil
		}
		return nil, nil, fmt.Errorf("enqueue: %w", enqErr)
	}
	if dbErr := s.store.UpdateRepoStatus(repo.ID, store.StatusQueued, formatQueuedDetail(position)); dbErr != nil {
		return nil, nil, fmt.Errorf("set status: %w", dbErr)
	}

	start := time.Now()

	var res JobResult
	select {
	case res = <-done:
	case <-ctx.Done():
		s.queue.dequeue(alias)
		return nil, nil, ctx.Err()
	}

	// Convert JobResult into the per-repo IndexResult shape used by the formatter.
	var result *indexer.IndexResult
	if res.IndexResult != nil {
		result = res.IndexResult
	} else {
		result = &indexer.IndexResult{Repo: alias}
	}
	if res.Err != nil && result.Error == nil {
		result.Error = res.Err
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

// pendingUpdate tracks a queued repo so updateAllRepos can wait on its
// completion, dequeue it on cancel, and report a stable per-repo line.
type pendingUpdate struct {
	alias string
	done  chan JobResult
}

// updateAllRepos re-indexes all repos from the database.
func (s *Server) updateAllRepos(ctx context.Context) (*mcp.CallToolResult, any, error) {
	repos, err := s.store.ListRepos()
	if err != nil {
		return nil, nil, fmt.Errorf("list repos: %w", err)
	}

	var (
		results  []indexer.IndexResult
		pendings []pendingUpdate
	)

	// First pass: enqueue every repo we can. Errors that prevent enqueue
	// (parse error, queue-full) are reported synchronously as per-repo entries
	// so the caller still sees them in the aggregate output.
	for i := range repos {
		repo := &repos[i]

		job, jobErr := buildJobFromRepo(repo, priorityUser)
		if jobErr != nil {
			results = append(results, indexer.IndexResult{Repo: repo.Alias, Error: jobErr})
			continue
		}

		done, position, _, _, enqErr := s.queue.enqueue(job)
		if enqErr != nil {
			results = append(results, indexer.IndexResult{Repo: repo.Alias, Error: enqErr})
			continue
		}
		if dbErr := s.store.UpdateRepoStatus(repo.ID, store.StatusQueued, formatQueuedDetail(position)); dbErr != nil {
			results = append(results, indexer.IndexResult{Repo: repo.Alias, Error: dbErr})
			continue
		}
		pendings = append(pendings, pendingUpdate{alias: repo.Alias, done: done})
	}

	start := time.Now()
	cancelled := false

	// Second pass: collect each result. If the caller cancels mid-loop, drain
	// the rest by dequeueing pending entries and synthesizing skipped rows so
	// the aggregate report stays honest about what was abandoned.
	for idx, p := range pendings {
		if cancelled {
			s.queue.dequeue(p.alias)
			results = append(results, indexer.IndexResult{Repo: p.alias, Skipped: true, Error: ctx.Err()})
			continue
		}

		select {
		case res := <-p.done:
			result := res.IndexResult
			if result == nil {
				result = &indexer.IndexResult{Repo: p.alias}
			}
			if res.Err != nil && result.Error == nil {
				result.Error = res.Err
			}
			results = append(results, *result)
		case <-ctx.Done():
			cancelled = true
			s.queue.dequeue(p.alias)
			results = append(results, indexer.IndexResult{Repo: p.alias, Skipped: true, Error: ctx.Err()})
			// Continue the loop to drain remaining pendings.
			_ = idx
		}
	}

	var b strings.Builder
	b.WriteString("Update results:\n\n")

	updated := 0
	unchanged := 0
	errCount := 0

	for i := range results {
		s.formatResult(&b, &results[i])
		if results[i].Error != nil {
			errCount++
		} else if results[i].Skipped {
			unchanged++
		} else {
			updated++
		}
	}

	total := len(results)
	duration := time.Since(start).Round(time.Millisecond)
	fmt.Fprintf(&b, "\n%d repos checked in %s.", total, duration)
	if errCount > 0 {
		fmt.Fprintf(&b, " %d updated, %d unchanged, %d errors.\n", updated, unchanged, errCount)
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
