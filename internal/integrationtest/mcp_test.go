//go:build integration

package integrationtest

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mylocalgpt/local-docs-mcp/internal/config"
	"github.com/mylocalgpt/local-docs-mcp/internal/indexer"
	"github.com/mylocalgpt/local-docs-mcp/internal/mcpserver"
	"github.com/mylocalgpt/local-docs-mcp/internal/search"
)

// setupMCPClient creates an MCP server + client connected via in-memory
// transports, backed by the real indexed DB from TestMain.
func setupMCPClient(t *testing.T) (*mcp.ClientSession, func()) {
	t.Helper()

	s := openTestStore(t)
	srch := search.NewSearch(s)
	ix, err := indexer.NewIndexer(s)
	if err != nil {
		s.Close()
		t.Fatalf("create indexer: %v", err)
	}

	cfg := &config.Config{Repos: []config.RepoConfig{
		{
			URL:   "https://github.com/MicrosoftDocs/entra-docs",
			Paths: []string{"docs/identity/hybrid/connect/", "docs/identity/hybrid/cloud-sync/", "docs/identity/hybrid/"},
			Alias: "entra-hybrid",
		},
	}}

	srv := mcpserver.New(s, srch, ix, cfg)

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

	return clientSession, cleanup
}

// extractMCPText extracts the text from the first TextContent in a CallToolResult.
func extractMCPText(t *testing.T, result *mcp.CallToolResult) string {
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

func TestMCPListRepos(t *testing.T) {
	cs, cleanup := setupMCPClient(t)
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

	text := extractMCPText(t, result)
	t.Logf("list_repos output:\n%s", text)

	if !strings.Contains(text, "entra-hybrid") {
		t.Error("expected 'entra-hybrid' in list_repos output")
	}
	if !strings.Contains(text, "docs") {
		t.Error("expected 'docs' in list_repos output")
	}
}

func TestMCPSearchDocs(t *testing.T) {
	cs, cleanup := setupMCPClient(t)
	defer cleanup()

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "search_docs",
		Arguments: map[string]any{
			"query": "connect sync filtering",
		},
	})
	if err != nil {
		t.Fatalf("call search_docs: %v", err)
	}
	if result.IsError {
		text := extractMCPText(t, result)
		t.Fatalf("unexpected tool error from search_docs: %s", text)
	}

	text := extractMCPText(t, result)
	t.Logf("search_docs output (first 500 chars):\n%.500s", text)

	if !strings.Contains(text, "Showing") {
		t.Error("expected 'Showing' in search output")
	}
}

func TestMCPBrowseDocsFiles(t *testing.T) {
	cs, cleanup := setupMCPClient(t)
	defer cleanup()

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "browse_docs",
		Arguments: map[string]any{
			"repo": "entra-hybrid",
		},
	})
	if err != nil {
		t.Fatalf("call browse_docs: %v", err)
	}
	if result.IsError {
		t.Fatal("unexpected tool error from browse_docs")
	}

	text := extractMCPText(t, result)
	t.Logf("browse_docs files output (first 500 chars):\n%.500s", text)

	if !strings.Contains(text, "files") {
		t.Error("expected 'files' in browse output")
	}
	if !strings.Contains(text, ".md") {
		t.Error("expected '.md' file paths in browse output")
	}
}

func TestMCPBrowseDocsHeadings(t *testing.T) {
	cs, cleanup := setupMCPClient(t)
	defer cleanup()

	// First browse to get a file path
	browseResult, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "browse_docs",
		Arguments: map[string]any{
			"repo": "entra-hybrid",
		},
	})
	if err != nil {
		t.Fatalf("browse files: %v", err)
	}
	browseText := extractMCPText(t, browseResult)

	// Extract first connect/ .md path from browse output
	var targetPath string
	for _, line := range strings.Split(browseText, "\n") {
		if strings.Contains(line, "connect/") && strings.Contains(line, ".md") {
			fields := strings.Fields(line)
			if len(fields) > 0 {
				targetPath = strings.TrimSpace(fields[0])
				break
			}
		}
	}
	if targetPath == "" {
		t.Skip("could not find a connect/ file in browse output")
	}

	t.Logf("browsing headings for: %s", targetPath)

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "browse_docs",
		Arguments: map[string]any{
			"repo": "entra-hybrid",
			"path": targetPath,
		},
	})
	if err != nil {
		t.Fatalf("call browse_docs headings: %v", err)
	}
	if result.IsError {
		t.Fatal("unexpected tool error from browse_docs headings")
	}

	text := extractMCPText(t, result)
	t.Logf("browse_docs headings output (first 500 chars):\n%.500s", text)

	if !strings.Contains(text, "section") {
		t.Error("expected 'section' in heading output")
	}
}

func TestMCPUpdateDocs(t *testing.T) {
	cs, cleanup := setupMCPClient(t)
	defer cleanup()

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "update_docs",
		Arguments: map[string]any{
			"repo": "entra-hybrid",
		},
	})
	if err != nil {
		t.Fatalf("call update_docs: %v", err)
	}
	if result.IsError {
		text := extractMCPText(t, result)
		t.Fatalf("unexpected tool error from update_docs: %s", text)
	}

	text := extractMCPText(t, result)
	t.Logf("update_docs output:\n%s", text)

	// SHA hasn't changed since TestMain indexed, so expect skip
	hasSkip := strings.Contains(strings.ToLower(text), "skip") ||
		strings.Contains(strings.ToLower(text), "unchanged") ||
		strings.Contains(strings.ToLower(text), "already")
	if !hasSkip {
		t.Logf("warning: expected skip/unchanged indicator in update output")
	}
}
