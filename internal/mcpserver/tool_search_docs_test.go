package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mylocalgpt/local-docs-mcp/internal/config"
	"github.com/mylocalgpt/local-docs-mcp/internal/search"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

// setupSearchTest creates an in-memory store with test documents, an MCP server,
// and returns a connected client session ready for tool calls.
func setupSearchTest(t *testing.T) (*mcp.ClientSession, *mcp.ServerSession, func()) {
	t.Helper()

	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	// Insert a test repo and documents
	repoID, err := s.UpsertRepo("testrepo", "https://example.com/repo.git", `["docs"]`, "git")
	if err != nil {
		t.Fatalf("upsert repo: %v", err)
	}

	docs := []store.Document{
		{
			RepoID:       repoID,
			Path:         "docs/getting-started.md",
			DocTitle:     "Getting Started",
			SectionTitle: "Installation",
			Content:      "Run go install to install the tool. Make sure you have Go 1.21 or later.",
			Tokens:       20,
			HeadingLevel: 2,
		},
		{
			RepoID:       repoID,
			Path:         "docs/getting-started.md",
			DocTitle:     "Getting Started",
			SectionTitle: "Configuration",
			Content:      "Create a config.yaml file in your home directory with your repository settings.",
			Tokens:       18,
			HeadingLevel: 2,
		},
		{
			RepoID:       repoID,
			Path:         "docs/api-reference.md",
			DocTitle:     "API Reference",
			SectionTitle: "Search Endpoint",
			Content:      "The search endpoint accepts a query parameter and returns matching documents.",
			Tokens:       15,
			HeadingLevel: 2,
		},
	}

	if err := s.ReplaceDocuments(repoID, docs); err != nil {
		t.Fatalf("replace documents: %v", err)
	}

	srch := search.NewSearch(s)
	cfg := &config.Config{Repos: []config.RepoConfig{
		{URL: "https://example.com/repo.git", Paths: []string{"docs"}, Alias: "testrepo"},
	}}

	srv := New(s, srch, nil, cfg)

	ctx, cancel := context.WithCancel(context.Background())

	st, ct := mcp.NewInMemoryTransports()

	serverSession, err := srv.MCPServer().Connect(ctx, st, nil)
	if err != nil {
		cancel()
		s.Close()
		t.Fatalf("server connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "v0.0.1",
	}, nil)

	clientSession, err := client.Connect(ctx, ct, nil)
	if err != nil {
		cancel()
		s.Close()
		t.Fatalf("client connect: %v", err)
	}

	cleanup := func() {
		clientSession.Close()
		serverSession.Wait()
		cancel()
		s.Close()
	}

	return clientSession, serverSession, cleanup
}

func TestSearchDocsWithResults(t *testing.T) {
	cs, _, cleanup := setupSearchTest(t)
	defer cleanup()

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "search_docs",
		Arguments: map[string]any{
			"query": "install",
		},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}

	if result.IsError {
		t.Fatalf("unexpected tool error")
	}

	if len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}

	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}

	if !strings.Contains(text.Text, "Found") {
		t.Errorf("expected summary line with 'Found', got: %s", text.Text[:min(100, len(text.Text))])
	}
	if !strings.Contains(text.Text, "Installation") {
		t.Errorf("expected 'Installation' section in results, got: %s", text.Text[:min(200, len(text.Text))])
	}
	if !strings.Contains(text.Text, "testrepo") {
		t.Errorf("expected repo alias 'testrepo' in results")
	}
}

func TestSearchDocsNoResults(t *testing.T) {
	cs, _, cleanup := setupSearchTest(t)
	defer cleanup()

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "search_docs",
		Arguments: map[string]any{
			"query": "nonexistentxyzterm",
		},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}

	if result.IsError {
		t.Fatalf("unexpected tool error for no-results case")
	}

	if len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}

	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}

	if !strings.Contains(text.Text, "No results found") {
		t.Errorf("expected no-results message, got: %s", text.Text)
	}
}

func TestSearchDocsFTS5Error(t *testing.T) {
	cs, _, cleanup := setupSearchTest(t)
	defer cleanup()

	// An unbalanced quote is invalid FTS5 syntax
	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "search_docs",
		Arguments: map[string]any{
			"query": "\"unclosed quote",
		},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}

	// The SDK wraps handler errors as tool errors (IsError=true) in the result
	if !result.IsError {
		t.Fatal("expected IsError=true for FTS5 syntax error")
	}

	if len(result.Content) == 0 {
		t.Fatal("expected error content")
	}

	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}

	if !strings.Contains(text.Text, "FTS5") {
		t.Errorf("expected FTS5 mentioned in error, got: %s", text.Text)
	}
	if !strings.Contains(text.Text, "double quotes") {
		t.Errorf("expected quoting guidance in error, got: %s", text.Text)
	}
}

func TestSearchDocsFTS5ErrorWithDots(t *testing.T) {
	cs, _, cleanup := setupSearchTest(t)
	defer cleanup()

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "search_docs",
		Arguments: map[string]any{
			"query": "app.config.json settings",
		},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}

	if !result.IsError {
		// Dots may not always trigger an FTS5 error depending on the SQLite version.
		// If it doesn't error, that's acceptable; skip the rest of the test.
		t.Skip("dotted query did not trigger FTS5 error in this environment")
	}

	text := result.Content[0].(*mcp.TextContent)
	if !strings.Contains(text.Text, "FTS5") {
		t.Errorf("expected FTS5 in error, got: %s", text.Text)
	}
}

func TestSearchDocsWithRepoFilter(t *testing.T) {
	cs, _, cleanup := setupSearchTest(t)
	defer cleanup()

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "search_docs",
		Arguments: map[string]any{
			"query": "search",
			"repo":  "testrepo",
		},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}

	if result.IsError {
		t.Fatalf("unexpected tool error")
	}

	if len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}

	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}

	if !strings.Contains(text.Text, "Search Endpoint") {
		t.Errorf("expected 'Search Endpoint' in results, got: %s", text.Text[:min(200, len(text.Text))])
	}
}

func TestSearchDocsWithTokenLimit(t *testing.T) {
	cs, _, cleanup := setupSearchTest(t)
	defer cleanup()

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "search_docs",
		Arguments: map[string]any{
			"query":  "go",
			"tokens": 10,
		},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}

	if result.IsError {
		t.Fatalf("unexpected tool error")
	}

	// With a very low token limit, we should still get at least one result
	// (the token budget includes the crossing result)
	if len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}

	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}

	if !strings.Contains(text.Text, "Found") {
		t.Errorf("expected summary line, got: %s", text.Text[:min(100, len(text.Text))])
	}
}
