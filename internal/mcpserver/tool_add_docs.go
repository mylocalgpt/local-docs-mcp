package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mylocalgpt/local-docs-mcp/internal/indexer"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

// AddDocsInput defines the input schema for the add_docs tool.
type AddDocsInput struct {
	URL   *string  `json:"url,omitempty" jsonschema:"Git repository URL"`
	Paths []string `json:"paths,omitempty" jsonschema:"Subdirectory paths within the repo"`
	Path  *string  `json:"path,omitempty" jsonschema:"Local filesystem directory path"`
	Alias string   `json:"alias" jsonschema:"Unique name for this doc source"`
}

// registerAddDocsTool registers the add_docs tool on the MCP server.
func (s *Server) registerAddDocsTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "add_docs",
		Description: "Add documentation from a git repo or local directory. For git repos, provide the GitHub URL and specific doc subdirectory paths (e.g. [\"docs/guide/\"]). For local directories, provide the absolute filesystem path (ask the user if unknown). Indexing runs in the background; use list_repos to check status.",
	}, s.handleAddDocs)
}

// handleAddDocs implements the add_docs tool handler.
func (s *Server) handleAddDocs(_ context.Context, _ *mcp.CallToolRequest, input AddDocsInput) (*mcp.CallToolResult, any, error) {
	if s.indexer == nil {
		return nil, nil, fmt.Errorf("indexer not available")
	}

	// Validate alias
	if input.Alias == "" {
		return nil, nil, fmt.Errorf("alias is required")
	}

	// Validate: must provide url xor path
	hasURL := input.URL != nil && *input.URL != ""
	hasPath := input.Path != nil && *input.Path != ""
	if hasURL == hasPath {
		return nil, nil, fmt.Errorf("provide either 'url' (git repo) or 'path' (local directory), not both or neither")
	}

	if hasURL {
		return s.handleAddGitDocs(input)
	}
	return s.handleAddLocalDocs(input)
}

// handleAddGitDocs handles adding documentation from a git repository.
func (s *Server) handleAddGitDocs(input AddDocsInput) (*mcp.CallToolResult, any, error) {
	if len(input.Paths) == 0 {
		return nil, nil, fmt.Errorf("paths is required for git repos (specify which subdirectories to index)")
	}

	if err := indexer.CheckGitVersion(); err != nil {
		return nil, nil, fmt.Errorf("git not available: %w", err)
	}

	// Path merging for existing git repos
	var mergedPaths []string
	existing, err := s.store.GetRepo(input.Alias)
	if err != nil {
		return nil, nil, fmt.Errorf("checking existing repo: %w", err)
	}
	priorStatus := ""
	if existing != nil {
		priorStatus = existing.Status
		var existingPaths []string
		if err := json.Unmarshal([]byte(existing.Paths), &existingPaths); err != nil {
			existingPaths = nil
		}
		mergedPaths = indexer.MergePaths(existingPaths, input.Paths)
	} else {
		mergedPaths = indexer.MergePaths(nil, input.Paths)
	}

	pathsJSON, err := json.Marshal(mergedPaths)
	if err != nil {
		return nil, nil, fmt.Errorf("marshal paths: %w", err)
	}

	repoID, err := s.store.UpsertRepo(input.Alias, *input.URL, string(pathsJSON), "git")
	if err != nil {
		return nil, nil, fmt.Errorf("upsert repo: %w", err)
	}

	job := &Job{
		Alias:       input.Alias,
		Kind:        jobKindGit,
		URL:         *input.URL,
		Paths:       mergedPaths,
		Force:       true,
		Priority:    priorityUser,
		PriorStatus: priorStatus,
		RepoID:      repoID,
	}

	_, position, coalesced, pathsChanged, enqErr := s.queue.enqueue(job)
	if enqErr != nil {
		if errors.Is(enqErr, errQueueFull) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: enqErr.Error()}},
			}, nil, nil
		}
		return nil, nil, fmt.Errorf("enqueue: %w", enqErr)
	}

	if !coalesced {
		if dbErr := s.store.UpdateRepoStatus(repoID, store.StatusQueued, formatQueuedDetail(position)); dbErr != nil {
			return nil, nil, fmt.Errorf("set status: %w", dbErr)
		}
	}

	msg := addDocsResponse(input.Alias, *input.URL, mergedPaths, position, coalesced, pathsChanged, false)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}, nil, nil
}

// handleAddLocalDocs handles adding documentation from a local directory.
func (s *Server) handleAddLocalDocs(input AddDocsInput) (*mcp.CallToolResult, any, error) {
	info, err := os.Stat(*input.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("path %q: %w", *input.Path, err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("path %q is not a directory", *input.Path)
	}

	// Look up existing repo so we can capture PriorStatus for shutdown-revert.
	existing, err := s.store.GetRepo(input.Alias)
	if err != nil {
		return nil, nil, fmt.Errorf("checking existing repo: %w", err)
	}
	priorStatus := ""
	if existing != nil {
		priorStatus = existing.Status
	}

	repoID, err := s.store.UpsertRepo(input.Alias, *input.Path, "[]", "local")
	if err != nil {
		return nil, nil, fmt.Errorf("upsert repo: %w", err)
	}

	job := &Job{
		Alias:       input.Alias,
		Kind:        jobKindLocal,
		URL:         *input.Path,
		Paths:       nil,
		Force:       true,
		Priority:    priorityUser,
		PriorStatus: priorStatus,
		RepoID:      repoID,
	}

	_, position, coalesced, pathsChanged, enqErr := s.queue.enqueue(job)
	if enqErr != nil {
		if errors.Is(enqErr, errQueueFull) {
			return &mcp.CallToolResult{
				Content: []mcp.Content{&mcp.TextContent{Text: enqErr.Error()}},
			}, nil, nil
		}
		return nil, nil, fmt.Errorf("enqueue: %w", enqErr)
	}

	if !coalesced {
		if dbErr := s.store.UpdateRepoStatus(repoID, store.StatusQueued, formatQueuedDetail(position)); dbErr != nil {
			return nil, nil, fmt.Errorf("set status: %w", dbErr)
		}
	}

	msg := addDocsResponse(input.Alias, *input.Path, nil, position, coalesced, pathsChanged, true)
	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: msg}},
	}, nil, nil
}

// addDocsResponse builds the human-readable message returned to the MCP
// client. The wording differs based on whether the request started a fresh
// queue entry or coalesced into an existing one.
func addDocsResponse(alias, source string, paths []string, position int, coalesced, pathsChanged, isLocal bool) string {
	var b strings.Builder
	if isLocal {
		fmt.Fprintf(&b, "Adding local documentation source:\n")
		fmt.Fprintf(&b, "  Alias: %s\n", alias)
		fmt.Fprintf(&b, "  Path: %s\n", source)
	} else {
		fmt.Fprintf(&b, "Adding git documentation source:\n")
		fmt.Fprintf(&b, "  Alias: %s\n", alias)
		fmt.Fprintf(&b, "  URL: %s\n", source)
		fmt.Fprintf(&b, "  Paths: %s\n", strings.Join(paths, ", "))
	}
	b.WriteString("\n")

	switch {
	case !coalesced:
		fmt.Fprintf(&b, "Queued for indexing (~%d ahead). Use list_repos to check progress.", position)
	case pathsChanged:
		fmt.Fprintf(&b, "Already queued (~%d ahead); your paths have been merged into the pending job.", position)
	default:
		if isLocal {
			fmt.Fprintf(&b, "Already queued (~%d ahead); folder will be re-scanned automatically.", position)
		} else {
			fmt.Fprintf(&b, "Paths already queued (~%d ahead); repo will be re-fetched and re-indexed automatically.", position)
		}
	}
	return b.String()
}
