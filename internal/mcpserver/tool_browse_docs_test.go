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

// setupBrowseTest creates an in-memory store with test documents, an MCP server,
// and returns a connected client session ready for tool calls.
func setupBrowseTest(t *testing.T) (*mcp.ClientSession, *mcp.ServerSession, func()) {
	t.Helper()

	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	repoID, err := s.UpsertRepo("myrepo", "https://example.com/repo.git", `["docs"]`, "git")
	if err != nil {
		t.Fatalf("upsert repo: %v", err)
	}

	docs := []store.Document{
		{
			RepoID:       repoID,
			Path:         "docs/getting-started.md",
			DocTitle:     "Getting Started",
			SectionTitle: "Getting Started",
			Content:      "Welcome to the project.",
			Tokens:       10,
			HeadingLevel: 1,
		},
		{
			RepoID:       repoID,
			Path:         "docs/getting-started.md",
			DocTitle:     "Getting Started",
			SectionTitle: "Installation",
			Content:      "Run go install to set up.",
			Tokens:       15,
			HeadingLevel: 2,
		},
		{
			RepoID:       repoID,
			Path:         "docs/getting-started.md",
			DocTitle:     "Getting Started",
			SectionTitle: "Configuration",
			Content:      "Create a config file.",
			Tokens:       12,
			HeadingLevel: 2,
		},
		{
			RepoID:       repoID,
			Path:         "docs/api-reference.md",
			DocTitle:     "API Reference",
			SectionTitle: "Overview",
			Content:      "The API provides several endpoints.",
			Tokens:       20,
			HeadingLevel: 1,
		},
		{
			RepoID:       repoID,
			Path:         "docs/api-reference.md",
			DocTitle:     "API Reference",
			SectionTitle: "Search Endpoint",
			Content:      "The search endpoint accepts queries.",
			Tokens:       25,
			HeadingLevel: 2,
		},
	}

	if err := s.ReplaceDocuments(repoID, docs); err != nil {
		t.Fatalf("replace documents: %v", err)
	}

	srch := search.NewSearch(s)
	cfg := &config.Config{}

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

func TestBrowseDocsFileList(t *testing.T) {
	cs, _, cleanup := setupBrowseTest(t)
	defer cleanup()

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "browse_docs",
		Arguments: map[string]any{
			"repo": "myrepo",
		},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}

	if result.IsError {
		t.Fatal("unexpected tool error")
	}

	if len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}

	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}

	// Check header
	if !strings.Contains(text.Text, "Files in myrepo (repo: example.com/repo):") {
		t.Errorf("expected header, got: %s", text.Text[:min(100, len(text.Text))])
	}

	// Check file paths
	if !strings.Contains(text.Text, "docs/getting-started.md") {
		t.Error("expected getting-started.md in file list")
	}
	if !strings.Contains(text.Text, "docs/api-reference.md") {
		t.Error("expected api-reference.md in file list")
	}

	// Check section counts
	if !strings.Contains(text.Text, "3 sections") {
		t.Error("expected '3 sections' for getting-started.md")
	}
	if !strings.Contains(text.Text, "2 sections") {
		t.Error("expected '2 sections' for api-reference.md")
	}

	// Check summary
	if !strings.Contains(text.Text, "2 files") {
		t.Error("expected '2 files' in summary")
	}
	if !strings.Contains(text.Text, "5 sections total") {
		t.Error("expected '5 sections total' in summary")
	}
}

func TestBrowseDocsHeadingTree(t *testing.T) {
	cs, _, cleanup := setupBrowseTest(t)
	defer cleanup()

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "browse_docs",
		Arguments: map[string]any{
			"repo": "myrepo",
			"path": "docs/getting-started.md",
		},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}

	if result.IsError {
		t.Fatal("unexpected tool error")
	}

	if len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}

	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}

	// Check header
	if !strings.Contains(text.Text, "myrepo (repo: example.com/repo): docs/getting-started.md") {
		t.Errorf("expected header, got: %s", text.Text[:min(100, len(text.Text))])
	}

	// Check heading entries with proper formatting
	if !strings.Contains(text.Text, "# Getting Started") {
		t.Error("expected '# Getting Started' heading")
	}
	if !strings.Contains(text.Text, "## Installation") {
		t.Error("expected '## Installation' heading")
	}
	if !strings.Contains(text.Text, "## Configuration") {
		t.Error("expected '## Configuration' heading")
	}

	// Check tokens shown
	if !strings.Contains(text.Text, "10 tokens") {
		t.Error("expected '10 tokens' for Getting Started")
	}
	if !strings.Contains(text.Text, "15 tokens") {
		t.Error("expected '15 tokens' for Installation")
	}

	// Check indentation: h2 headings should be indented relative to h1
	if !strings.Contains(text.Text, "  ## Installation") {
		t.Error("expected indentation for h2 headings")
	}

	// Check summary
	if !strings.Contains(text.Text, "3 sections") {
		t.Error("expected '3 sections' in summary")
	}
	if !strings.Contains(text.Text, "37 tokens total") {
		t.Error("expected '37 tokens total' in summary")
	}
}

func TestBrowseDocsUnknownRepo(t *testing.T) {
	cs, _, cleanup := setupBrowseTest(t)
	defer cleanup()

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "browse_docs",
		Arguments: map[string]any{
			"repo": "nonexistent",
		},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}

	// The SDK wraps handler errors as tool errors
	if !result.IsError {
		t.Fatal("expected IsError=true for unknown repo")
	}

	if len(result.Content) == 0 {
		t.Fatal("expected error content")
	}

	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}

	if !strings.Contains(text.Text, "not found") {
		t.Errorf("expected 'not found' in error, got: %s", text.Text)
	}
	if !strings.Contains(text.Text, "list_repos") {
		t.Errorf("expected 'list_repos' suggestion in error, got: %s", text.Text)
	}
}

func TestBrowseDocsUnknownPath(t *testing.T) {
	cs, _, cleanup := setupBrowseTest(t)
	defer cleanup()

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "browse_docs",
		Arguments: map[string]any{
			"repo": "myrepo",
			"path": "nonexistent/file.md",
		},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}

	if result.IsError {
		t.Fatal("unexpected tool error for unknown path")
	}

	if len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}

	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}

	if !strings.Contains(text.Text, "No sections found") {
		t.Errorf("expected 'No sections found' message, got: %s", text.Text)
	}
	if !strings.Contains(text.Text, "nonexistent/file.md") {
		t.Errorf("expected path in message, got: %s", text.Text)
	}
}

// setupBrowsePaginationTest creates a store with 5 distinct file paths for pagination testing.
func setupBrowsePaginationTest(t *testing.T) (*mcp.ClientSession, *mcp.ServerSession, func()) {
	t.Helper()

	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	repoID, err := s.UpsertRepo("paged", "https://example.com/paged.git", `["docs"]`, "git")
	if err != nil {
		t.Fatalf("upsert repo: %v", err)
	}

	docs := []store.Document{
		{RepoID: repoID, Path: "docs/a.md", DocTitle: "A", SectionTitle: "A", Content: "a", Tokens: 10, HeadingLevel: 1},
		{RepoID: repoID, Path: "docs/b.md", DocTitle: "B", SectionTitle: "B", Content: "b", Tokens: 10, HeadingLevel: 1},
		{RepoID: repoID, Path: "docs/c.md", DocTitle: "C", SectionTitle: "C", Content: "c", Tokens: 10, HeadingLevel: 1},
		{RepoID: repoID, Path: "docs/d.md", DocTitle: "D", SectionTitle: "D", Content: "d", Tokens: 10, HeadingLevel: 1},
		{RepoID: repoID, Path: "docs/e.md", DocTitle: "E", SectionTitle: "E", Content: "e", Tokens: 10, HeadingLevel: 1},
	}

	if err := s.ReplaceDocuments(repoID, docs); err != nil {
		t.Fatalf("replace documents: %v", err)
	}

	srch := search.NewSearch(s)
	cfg := &config.Config{}
	srv := New(s, srch, nil, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	st, ct := mcp.NewInMemoryTransports()

	serverSession, err := srv.MCPServer().Connect(ctx, st, nil)
	if err != nil {
		cancel()
		s.Close()
		t.Fatalf("server connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test-client", Version: "v0.0.1"}, nil)
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

func TestBrowseDocsFileListPagination(t *testing.T) {
	cs, _, cleanup := setupBrowsePaginationTest(t)
	defer cleanup()

	// Page 1 with page_size=2
	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "browse_docs",
		Arguments: map[string]any{
			"repo":      "paged",
			"page":      1,
			"page_size": 2,
		},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if result.IsError {
		t.Fatal("unexpected tool error")
	}

	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}

	if !strings.Contains(text.Text, "Showing files 1-2 of 5") {
		t.Errorf("expected pagination footer, got: %s", text.Text)
	}
	if !strings.Contains(text.Text, "Use page: 2 to see more") {
		t.Errorf("expected next page hint, got: %s", text.Text)
	}

	// Page 2
	result2, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "browse_docs",
		Arguments: map[string]any{
			"repo":      "paged",
			"page":      2,
			"page_size": 2,
		},
	})
	if err != nil {
		t.Fatalf("call tool page 2: %v", err)
	}
	if result2.IsError {
		t.Fatal("unexpected tool error on page 2")
	}

	text2, ok := result2.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result2.Content[0])
	}

	if !strings.Contains(text2.Text, "Showing files 3-4 of 5") {
		t.Errorf("expected page 2 range, got: %s", text2.Text)
	}
}

func TestBrowseDocsHeadingsIgnoresPagination(t *testing.T) {
	cs, _, cleanup := setupBrowseTest(t)
	defer cleanup()

	// Call with path and page=2 - pagination should be ignored for headings
	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "browse_docs",
		Arguments: map[string]any{
			"repo": "myrepo",
			"path": "docs/getting-started.md",
			"page": 2,
		},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if result.IsError {
		t.Fatal("unexpected tool error")
	}

	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}

	// Should still show all headings (page param ignored in heading mode)
	if !strings.Contains(text.Text, "# Getting Started") {
		t.Error("expected '# Getting Started' heading")
	}
	if !strings.Contains(text.Text, "3 sections") {
		t.Error("expected '3 sections' in summary")
	}
}
