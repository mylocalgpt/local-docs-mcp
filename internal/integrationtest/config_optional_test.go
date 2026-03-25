//go:build integration

package integrationtest

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mylocalgpt/local-docs-mcp/internal/config"
	"github.com/mylocalgpt/local-docs-mcp/internal/indexer"
	"github.com/mylocalgpt/local-docs-mcp/internal/mcpserver"
	"github.com/mylocalgpt/local-docs-mcp/internal/search"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

func TestConfigOptionalServerFlow(t *testing.T) {
	// Start server without config (empty config)
	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}

	srch := search.NewSearch(s)
	ix, err := indexer.NewIndexer(s)
	if err != nil {
		s.Close()
		t.Fatal(err)
	}

	// Pass empty config (simulates --config not provided)
	cfg := &config.Config{}
	srv := mcpserver.New(s, srch, ix, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	st, ct := mcp.NewInMemoryTransports()

	srv.MCPServer().Connect(ctx, st, nil)

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0.0.1"}, nil)
	cs, _ := client.Connect(ctx, ct, nil)

	// Initially empty
	listResult, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_repos",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("list_repos: %v", err)
	}
	listText := listResult.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(listText, "No documentation indexed") {
		t.Errorf("expected empty message: %s", listText)
	}

	// Add local docs
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "test.md"), []byte("# Test Doc\n\nSome content here.\n"), 0o644)

	addResult, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "add_docs",
		Arguments: map[string]any{
			"alias": "test-local",
			"path":  dir,
		},
	})
	if err != nil {
		t.Fatalf("add_docs: %v", err)
	}
	if addResult.IsError {
		t.Fatalf("add_docs error: %v", addResult.Content)
	}

	// Wait for indexing
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out")
		default:
		}
		repo, _ := s.GetRepo("test-local")
		if repo != nil && repo.Status == store.StatusReady {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Verify search works
	searchResult, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "search_docs",
		Arguments: map[string]any{
			"query": "content",
		},
	})
	if err != nil {
		t.Fatalf("search_docs: %v", err)
	}
	searchText := searchResult.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(searchText, "Test Doc") && !strings.Contains(searchText, "content") {
		t.Errorf("search should find docs: %s", searchText)
	}

	cancel()
	ix.Cleanup()
	s.Close()
}
