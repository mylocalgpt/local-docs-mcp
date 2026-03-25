//go:build integration

package integrationtest

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mylocalgpt/local-docs-mcp/internal/config"
	"github.com/mylocalgpt/local-docs-mcp/internal/mcpserver"
	"github.com/mylocalgpt/local-docs-mcp/internal/search"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

func TestRemoveDocsMCPFlow(t *testing.T) {
	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	// Pre-populate
	repoID, _ := s.UpsertRepo("test-repo", "https://example.com", `["docs"]`, "git")
	s.ReplaceDocuments(repoID, []store.Document{
		{RepoID: repoID, Path: "a.md", DocTitle: "A", SectionTitle: "A1", Content: "unique xylophone content", Tokens: 10, HeadingLevel: 1},
	})
	s.UpdateRepoIndex(repoID, "abc123", "2026-01-01T00:00:00Z", 1)

	srch := search.NewSearch(s)
	cfg := &config.Config{}
	srv := mcpserver.New(s, srch, nil, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	st, ct := mcp.NewInMemoryTransports()
	srv.MCPServer().Connect(ctx, st, nil)

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0.0.1"}, nil)
	cs, _ := client.Connect(ctx, ct, nil)

	// Remove
	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "remove_docs",
		Arguments: map[string]any{"alias": "test-repo"},
	})
	if err != nil {
		t.Fatalf("remove_docs: %v", err)
	}
	if result.IsError {
		t.Fatalf("remove_docs error: %v", result.Content)
	}

	text := result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "1 documents deleted") {
		t.Errorf("unexpected response: %s", text)
	}

	// Verify gone
	repo, _ := s.GetRepo("test-repo")
	if repo != nil {
		t.Error("repo should be gone after removal")
	}
}
