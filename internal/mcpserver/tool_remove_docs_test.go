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

func setupRemoveDocsTest(t *testing.T) (*mcp.ClientSession, *store.Store, func()) {
	t.Helper()

	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	srch := search.NewSearch(s)
	cfg := &config.Config{Repos: []config.RepoConfig{}}
	srv := New(s, srch, nil, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	st, ct := mcp.NewInMemoryTransports()

	_, err = srv.MCPServer().Connect(ctx, st, nil)
	if err != nil {
		cancel()
		_ = s.Close()
		t.Fatalf("server connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "v0.0.1"}, nil)
	cs, err := client.Connect(ctx, ct, nil)
	if err != nil {
		cancel()
		_ = s.Close()
		t.Fatalf("client connect: %v", err)
	}

	return cs, s, func() { cancel(); _ = s.Close() }
}

func TestRemoveDocsSuccess(t *testing.T) {
	cs, s, cleanup := setupRemoveDocsTest(t)
	defer cleanup()

	// Insert a repo with documents
	repoID, err := s.UpsertRepo("myrepo", "https://example.com", `["docs"]`, "git")
	if err != nil {
		t.Fatal(err)
	}
	_ = s.ReplaceDocuments(repoID, []store.Document{
		{RepoID: repoID, Path: "a.md", DocTitle: "A", SectionTitle: "A1", Content: "content", Tokens: 10, HeadingLevel: 1},
	})

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "remove_docs",
		Arguments: map[string]any{"alias": "myrepo"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if result.IsError {
		t.Fatalf("unexpected error: %v", result.Content)
	}

	text := result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "myrepo") || !strings.Contains(text, "1 documents deleted") {
		t.Errorf("unexpected response: %s", text)
	}
}

func TestRemoveDocsNotFound(t *testing.T) {
	cs, _, cleanup := setupRemoveDocsTest(t)
	defer cleanup()

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "remove_docs",
		Arguments: map[string]any{"alias": "nonexistent"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected error for nonexistent repo")
	}
}
