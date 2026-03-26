package mcpserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mylocalgpt/local-docs-mcp/internal/config"
	"github.com/mylocalgpt/local-docs-mcp/internal/indexer"
	"github.com/mylocalgpt/local-docs-mcp/internal/search"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

// setupIntegrationTest creates an in-memory store pre-populated with test data,
// a real search engine, a real indexer, and an MCP server connected via
// in-memory transports. All four tools are exercised against this shared setup.
func setupIntegrationTest(t *testing.T) (*mcp.ClientSession, *mcp.ServerSession, func()) {
	t.Helper()

	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	// Insert a test repo with known data
	repoID, err := s.UpsertRepo("mylib", "https://github.com/example/mylib", `["docs"]`, "git")
	if err != nil {
		t.Fatalf("upsert repo: %v", err)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.UpdateRepoIndex(repoID, "abc1234", now, 4); err != nil {
		t.Fatalf("update repo index: %v", err)
	}

	docs := []store.Document{
		{
			RepoID:       repoID,
			Path:         "docs/quickstart.md",
			DocTitle:     "Quickstart",
			SectionTitle: "Quickstart",
			Content:      "Welcome to mylib. This guide helps you get started quickly.",
			Tokens:       15,
			HeadingLevel: 1,
		},
		{
			RepoID:       repoID,
			Path:         "docs/quickstart.md",
			DocTitle:     "Quickstart",
			SectionTitle: "Installation",
			Content:      "Install mylib with go install github.com/example/mylib@latest.",
			Tokens:       12,
			HeadingLevel: 2,
		},
		{
			RepoID:       repoID,
			Path:         "docs/api.md",
			DocTitle:     "API Reference",
			SectionTitle: "Overview",
			Content:      "The API provides create, read, update, and delete operations.",
			Tokens:       18,
			HeadingLevel: 1,
		},
		{
			RepoID:       repoID,
			Path:         "docs/api.md",
			DocTitle:     "API Reference",
			SectionTitle: "Authentication",
			Content:      "All API requests require a bearer token in the Authorization header.",
			Tokens:       16,
			HeadingLevel: 2,
		},
	}

	if err := s.ReplaceDocuments(repoID, docs); err != nil {
		t.Fatalf("replace documents: %v", err)
	}

	// Rebuild FTS so search works against inserted data
	if err := s.RebuildFTS(); err != nil {
		t.Fatalf("rebuild fts: %v", err)
	}

	srch := search.NewSearch(s)

	ix, err := indexer.NewIndexer(s)
	if err != nil {
		t.Fatalf("create indexer: %v", err)
	}

	cfg := &config.Config{Repos: []config.RepoConfig{
		{URL: "https://github.com/example/mylib", Paths: []string{"docs"}, Alias: "mylib"},
	}}

	srv := New(s, srch, ix, cfg)

	ctx, cancel := context.WithCancel(context.Background())

	st, ct := mcp.NewInMemoryTransports()

	serverSession, err := srv.MCPServer().Connect(ctx, st, nil)
	if err != nil {
		ix.Cleanup()
		cancel()
		s.Close()
		t.Fatalf("server connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "integration-test",
		Version: "v0.0.1",
	}, nil)

	clientSession, err := client.Connect(ctx, ct, nil)
	if err != nil {
		ix.Cleanup()
		cancel()
		s.Close()
		t.Fatalf("client connect: %v", err)
	}

	cleanup := func() {
		clientSession.Close()
		serverSession.Wait()
		cancel()
		ix.Cleanup()
		s.Close()
	}

	return clientSession, serverSession, cleanup
}

func TestIntegrationListRepos(t *testing.T) {
	cs, _, cleanup := setupIntegrationTest(t)
	defer cleanup()

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_repos",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("call list_repos: %v", err)
	}
	if result.IsError {
		t.Fatal("unexpected tool error from list_repos")
	}

	text := extractText(t, result)

	if !strings.Contains(text, "mylib") {
		t.Error("expected repo alias 'mylib' in list_repos output")
	}
	if !strings.Contains(text, "4 docs") {
		t.Errorf("expected '4 docs' in list_repos output, got: %s", text)
	}
	if !strings.Contains(text, "current") {
		t.Error("expected 'current' staleness indicator")
	}
}

func TestIntegrationSearchDocs(t *testing.T) {
	cs, _, cleanup := setupIntegrationTest(t)
	defer cleanup()

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "search_docs",
		Arguments: map[string]any{
			"query": "install",
		},
	})
	if err != nil {
		t.Fatalf("call search_docs: %v", err)
	}
	if result.IsError {
		t.Fatal("unexpected tool error from search_docs")
	}

	text := extractText(t, result)

	if !strings.Contains(text, "Found") {
		t.Error("expected 'Found' summary in search results")
	}
	if !strings.Contains(text, "Installation") {
		t.Error("expected 'Installation' section in search results")
	}
	if !strings.Contains(text, "mylib") {
		t.Error("expected repo alias 'mylib' in search results")
	}
}

func TestIntegrationSearchDocsBadFTS5(t *testing.T) {
	cs, _, cleanup := setupIntegrationTest(t)
	defer cleanup()

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "search_docs",
		Arguments: map[string]any{
			"query": "\"unclosed quote",
		},
	})
	if err != nil {
		t.Fatalf("call search_docs: %v", err)
	}

	if !result.IsError {
		t.Fatal("expected IsError=true for bad FTS5 syntax")
	}

	text := extractText(t, result)

	if !strings.Contains(text, "FTS5") {
		t.Errorf("expected 'FTS5' in error message, got: %s", text)
	}
}

func TestIntegrationBrowseDocsFiles(t *testing.T) {
	cs, _, cleanup := setupIntegrationTest(t)
	defer cleanup()

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "browse_docs",
		Arguments: map[string]any{
			"repo": "mylib",
		},
	})
	if err != nil {
		t.Fatalf("call browse_docs: %v", err)
	}
	if result.IsError {
		t.Fatal("unexpected tool error from browse_docs")
	}

	text := extractText(t, result)

	if !strings.Contains(text, "docs/quickstart.md") {
		t.Error("expected quickstart.md in file listing")
	}
	if !strings.Contains(text, "docs/api.md") {
		t.Error("expected api.md in file listing")
	}
	if !strings.Contains(text, "2 files") {
		t.Error("expected '2 files' in summary")
	}
}

func TestIntegrationBrowseDocsHeadings(t *testing.T) {
	cs, _, cleanup := setupIntegrationTest(t)
	defer cleanup()

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "browse_docs",
		Arguments: map[string]any{
			"repo": "mylib",
			"path": "docs/api.md",
		},
	})
	if err != nil {
		t.Fatalf("call browse_docs: %v", err)
	}
	if result.IsError {
		t.Fatal("unexpected tool error from browse_docs headings")
	}

	text := extractText(t, result)

	if !strings.Contains(text, "# Overview") {
		t.Error("expected '# Overview' heading")
	}
	if !strings.Contains(text, "## Authentication") {
		t.Error("expected '## Authentication' heading")
	}
	if !strings.Contains(text, "2 sections") {
		t.Error("expected '2 sections' in summary")
	}
}

func TestIntegrationBrowseDocsBadRepo(t *testing.T) {
	cs, _, cleanup := setupIntegrationTest(t)
	defer cleanup()

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "browse_docs",
		Arguments: map[string]any{
			"repo": "nonexistent",
		},
	})
	if err != nil {
		t.Fatalf("call browse_docs: %v", err)
	}

	if !result.IsError {
		t.Fatal("expected IsError=true for nonexistent repo")
	}

	text := extractText(t, result)

	if !strings.Contains(text, "not found") {
		t.Errorf("expected 'not found' in error, got: %s", text)
	}
}

func TestIntegrationUpdateDocsBadRepo(t *testing.T) {
	cs, _, cleanup := setupIntegrationTest(t)
	defer cleanup()

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "update_docs",
		Arguments: map[string]any{
			"repo": "nonexistent",
		},
	})
	if err != nil {
		t.Fatalf("call update_docs: %v", err)
	}

	if !result.IsError {
		t.Fatal("expected IsError=true for nonexistent repo in update_docs")
	}

	text := extractText(t, result)

	if !strings.Contains(text, "not found") {
		t.Errorf("expected 'not found' in error, got: %s", text)
	}
}

func TestIntegrationInstructions(t *testing.T) {
	cs, _, cleanup := setupIntegrationTest(t)
	defer cleanup()

	initResult := cs.InitializeResult()
	if initResult == nil {
		t.Fatal("InitializeResult is nil")
	}

	instructions := initResult.Instructions
	if !strings.Contains(instructions, "Workflow:") {
		t.Error("expected 'Workflow:' in instructions")
	}
	if !strings.Contains(instructions, "list_repos") {
		t.Error("expected 'list_repos' in instructions")
	}
	if !strings.Contains(instructions, "search_docs") {
		t.Error("expected 'search_docs' in instructions")
	}
	if !strings.Contains(instructions, "FTS5") {
		t.Error("expected 'FTS5' in instructions")
	}
}

// extractText extracts the text from the first TextContent in a CallToolResult.
func extractText(t *testing.T, result *mcp.CallToolResult) string {
	t.Helper()
	if len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}
	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}
	return text.Text
}
