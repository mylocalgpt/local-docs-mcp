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

func TestAddDocsLocalMCPFlow(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "guide.md"), []byte("# API Guide\n\n## Authentication\n\nUse bearer tokens for auth.\n"), 0o644)

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

	cfg := &config.Config{}
	srv := mcpserver.New(s, srch, ix, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	st, ct := mcp.NewInMemoryTransports()

	_, err = srv.MCPServer().Connect(ctx, st, nil)
	if err != nil {
		cancel()
		ix.Cleanup()
		s.Close()
		t.Fatal(err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0.0.1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		cancel()
		ix.Cleanup()
		s.Close()
		t.Fatal(err)
	}

	// Add local docs
	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "add_docs",
		Arguments: map[string]any{
			"alias": "api-docs",
			"path":  dir,
		},
	})
	if err != nil {
		t.Fatalf("add_docs: %v", err)
	}
	if result.IsError {
		t.Fatalf("add_docs error: %v", result.Content)
	}

	text := result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "api-docs") {
		t.Errorf("response should contain alias: %s", text)
	}

	// Wait for background indexing
	deadline := time.After(10 * time.Second)
	for {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for indexing")
		default:
		}
		repo, _ := s.GetRepo("api-docs")
		if repo != nil && repo.Status == store.StatusReady {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	// Search the indexed content
	searchResult, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "search_docs",
		Arguments: map[string]any{
			"query": "authentication",
		},
	})
	if err != nil {
		t.Fatalf("search_docs: %v", err)
	}

	searchText := searchResult.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(searchText, "bearer tokens") {
		t.Errorf("expected search results to contain 'bearer tokens': %s", searchText)
	}

	cancel()
	ix.Cleanup()
	s.Close()
}
