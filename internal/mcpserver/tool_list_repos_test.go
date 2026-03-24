package mcpserver

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mylocalgpt/local-docs-mcp/internal/config"
	"github.com/mylocalgpt/local-docs-mcp/internal/search"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

// setupListReposTest creates an in-memory store with test repos, an MCP server,
// and returns a connected client session ready for tool calls.
func setupListReposTest(t *testing.T, populate bool) (*mcp.ClientSession, *mcp.ServerSession, *store.Store, func()) {
	t.Helper()

	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	if populate {
		repoID, err := s.UpsertRepo("react-docs", "https://github.com/reactjs/react.dev", `["docs"]`)
		if err != nil {
			t.Fatalf("upsert repo: %v", err)
		}

		// Set indexed_at to now so it shows as "current"
		now := time.Now().UTC().Format(time.RFC3339)
		if err := s.UpdateRepoIndex(repoID, "abc123", now, 42); err != nil {
			t.Fatalf("update repo index: %v", err)
		}

		repoID2, err := s.UpsertRepo("golang-docs", "https://github.com/golang/go", `["doc"]`)
		if err != nil {
			t.Fatalf("upsert repo: %v", err)
		}

		// Set indexed_at to 3 days ago so it shows as stale
		staleTime := time.Now().UTC().Add(-3 * 24 * time.Hour).Format(time.RFC3339)
		if err := s.UpdateRepoIndex(repoID2, "def456", staleTime, 128); err != nil {
			t.Fatalf("update repo index: %v", err)
		}
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

	return clientSession, serverSession, s, cleanup
}

func TestListReposPopulated(t *testing.T) {
	cs, _, _, cleanup := setupListReposTest(t, true)
	defer cleanup()

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_repos",
		Arguments: map[string]any{},
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
	if !strings.Contains(text.Text, "Indexed repositories:") {
		t.Errorf("expected header, got: %s", text.Text[:min(100, len(text.Text))])
	}

	// Check both repos present
	if !strings.Contains(text.Text, "golang-docs") {
		t.Error("expected 'golang-docs' in output")
	}
	if !strings.Contains(text.Text, "react-docs") {
		t.Error("expected 'react-docs' in output")
	}

	// Check doc counts
	if !strings.Contains(text.Text, "42 docs") {
		t.Error("expected '42 docs' in output")
	}
	if !strings.Contains(text.Text, "128 docs") {
		t.Error("expected '128 docs' in output")
	}

	// Check staleness indicators
	if !strings.Contains(text.Text, "current") {
		t.Error("expected 'current' staleness for react-docs")
	}
	if !strings.Contains(text.Text, "stale") {
		t.Error("expected 'stale' for golang-docs")
	}

	// Check URLs
	if !strings.Contains(text.Text, "https://github.com/reactjs/react.dev") {
		t.Error("expected react-docs URL in output")
	}

	// Check summary line
	if !strings.Contains(text.Text, "2 repos indexed") {
		t.Error("expected summary '2 repos indexed'")
	}
}

func TestListReposEmpty(t *testing.T) {
	cs, _, _, cleanup := setupListReposTest(t, false)
	defer cleanup()

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "list_repos",
		Arguments: map[string]any{},
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

	if !strings.Contains(text.Text, "No documentation repos indexed yet") {
		t.Errorf("expected empty message, got: %s", text.Text)
	}
}

func TestComputeStaleness(t *testing.T) {
	tests := []struct {
		name      string
		indexedAt string
		wantStr   string
	}{
		{"empty", "", "stale - never indexed"},
		{"invalid", "not-a-date", "stale - unknown age"},
		{"current", time.Now().UTC().Format(time.RFC3339), "current"},
		{"stale 1 day", time.Now().UTC().Add(-25 * time.Hour).Format(time.RFC3339), "stale - 1 day ago"},
		{"stale 5 days", time.Now().UTC().Add(-5 * 24 * time.Hour).Format(time.RFC3339), "stale - 5 days ago"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := computeStaleness(tt.indexedAt)
			if got != tt.wantStr {
				t.Errorf("computeStaleness(%q) = %q, want %q", tt.indexedAt, got, tt.wantStr)
			}
		})
	}
}
