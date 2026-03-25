package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mylocalgpt/local-docs-mcp/internal/config"
	"github.com/mylocalgpt/local-docs-mcp/internal/indexer"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

// AddDocsInput defines the input schema for the add_docs tool.
type AddDocsInput struct {
	URL   string   `json:"url,omitempty" jsonschema:"GitHub repository URL"`
	Paths []string `json:"paths,omitempty" jsonschema:"Subdirectory paths within the repo"`
	Path  string   `json:"path,omitempty" jsonschema:"Local filesystem directory path"`
	Alias string   `json:"alias" jsonschema:"Unique name for this doc source"`
}

// registerAddDocsTool registers the add_docs tool on the MCP server.
func (s *Server) registerAddDocsTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "add_docs",
		Description: "Add documentation from a git repo or local directory. Indexing runs in the background. Use list_repos to check progress.",
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
	hasURL := input.URL != ""
	hasPath := input.Path != ""
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
	if existing != nil {
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

	repoID, err := s.store.UpsertRepo(input.Alias, input.URL, string(pathsJSON), "git")
	if err != nil {
		return nil, nil, fmt.Errorf("upsert repo: %w", err)
	}

	// Acquire mutex before setting status to avoid leaving repo in indexing
	// state if the lock cannot be acquired.
	if !s.indexMu.TryLock() {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{
				Text: "Indexing already in progress, try again shortly.",
			}},
		}, nil, nil
	}

	if err := s.store.UpdateRepoStatus(repoID, store.StatusIndexing, "clone started"); err != nil {
		s.indexMu.Unlock()
		return nil, nil, fmt.Errorf("set status: %w", err)
	}

	// Launch background indexing. Lock is acquired in the handler and
	// released in the goroutine (valid cross-goroutine mutex transfer).
	go func() {
		defer s.indexMu.Unlock()

		cfg := config.RepoConfig{Alias: input.Alias, URL: input.URL, Paths: mergedPaths}
		result, err := s.indexer.IndexRepo(cfg, true)
		if err != nil {
			log.Printf("add_docs: %s indexing failed: %v", input.Alias, err)
			s.store.UpdateRepoStatus(repoID, store.StatusError, err.Error())
			return
		}
		if result.Error != nil {
			log.Printf("add_docs: %s indexing error: %v", input.Alias, result.Error)
			s.store.UpdateRepoStatus(repoID, store.StatusError, result.Error.Error())
			return
		}

		s.store.UpdateRepoStatus(repoID, store.StatusReady, "")
		if err := s.store.RebuildFTS(); err != nil {
			log.Printf("add_docs: rebuild fts failed: %v", err)
		}
		log.Printf("add_docs: %s indexed %d docs in %s", input.Alias, result.DocsIndexed, result.Duration)
	}()

	var b strings.Builder
	fmt.Fprintf(&b, "Adding git documentation source:\n")
	fmt.Fprintf(&b, "  Alias: %s\n", input.Alias)
	fmt.Fprintf(&b, "  URL: %s\n", input.URL)
	fmt.Fprintf(&b, "  Paths: %s\n", strings.Join(mergedPaths, ", "))
	fmt.Fprintf(&b, "\nIndexing started in the background. Use list_repos to check progress.")

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: b.String()}},
	}, nil, nil
}

// handleAddLocalDocs handles adding documentation from a local directory.
func (s *Server) handleAddLocalDocs(input AddDocsInput) (*mcp.CallToolResult, any, error) {
	info, err := os.Stat(input.Path)
	if err != nil {
		return nil, nil, fmt.Errorf("path %q: %w", input.Path, err)
	}
	if !info.IsDir() {
		return nil, nil, fmt.Errorf("path %q is not a directory", input.Path)
	}

	repoID, err := s.store.UpsertRepo(input.Alias, input.Path, "[]", "local")
	if err != nil {
		return nil, nil, fmt.Errorf("upsert repo: %w", err)
	}

	// Acquire mutex before setting status.
	if !s.indexMu.TryLock() {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{
				Text: "Indexing already in progress, try again shortly.",
			}},
		}, nil, nil
	}

	if err := s.store.UpdateRepoStatus(repoID, store.StatusIndexing, "scanning directory"); err != nil {
		s.indexMu.Unlock()
		return nil, nil, fmt.Errorf("set status: %w", err)
	}

	// Launch background indexing.
	go func() {
		defer s.indexMu.Unlock()

		result, err := s.indexer.IndexLocalPath(input.Alias, input.Path)
		if err != nil {
			log.Printf("add_docs: %s indexing failed: %v", input.Alias, err)
			s.store.UpdateRepoStatus(repoID, store.StatusError, err.Error())
			return
		}
		if result.Error != nil {
			log.Printf("add_docs: %s indexing error: %v", input.Alias, result.Error)
			s.store.UpdateRepoStatus(repoID, store.StatusError, result.Error.Error())
			return
		}

		s.store.UpdateRepoStatus(repoID, store.StatusReady, "")
		if err := s.store.RebuildFTS(); err != nil {
			log.Printf("add_docs: rebuild fts failed: %v", err)
		}
		log.Printf("add_docs: %s indexed %d docs in %s", input.Alias, result.DocsIndexed, result.Duration)
	}()

	var b strings.Builder
	fmt.Fprintf(&b, "Adding local documentation source:\n")
	fmt.Fprintf(&b, "  Alias: %s\n", input.Alias)
	fmt.Fprintf(&b, "  Path: %s\n", input.Path)
	fmt.Fprintf(&b, "\nIndexing started in the background. Use list_repos to check progress.")

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: b.String()}},
	}, nil, nil
}
