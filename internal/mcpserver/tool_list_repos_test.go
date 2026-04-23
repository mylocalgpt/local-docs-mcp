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
		repoID, err := s.UpsertRepo("react-docs", "https://github.com/reactjs/react.dev", `["docs"]`, "git")
		if err != nil {
			t.Fatalf("upsert repo: %v", err)
		}

		// Set indexed_at to now so it shows as "current"
		now := time.Now().UTC().Format(time.RFC3339)
		if err := s.UpdateRepoIndex(repoID, "abc123", now, 42); err != nil {
			t.Fatalf("update repo index: %v", err)
		}

		repoID2, err := s.UpsertRepo("golang-docs", "https://github.com/golang/go", `["doc"]`, "git")
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
		_ = s.Close()
		t.Fatalf("server connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "v0.0.1",
	}, nil)

	clientSession, err := client.Connect(ctx, ct, nil)
	if err != nil {
		cancel()
		_ = s.Close()
		t.Fatalf("client connect: %v", err)
	}

	cleanup := func() {
		_ = clientSession.Close()
		_ = serverSession.Wait()
		cancel()
		_ = s.Close()
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

	// Check source type
	if !strings.Contains(text.Text, "[git]") {
		t.Error("expected '[git]' source type in output")
	}

	// Check status line
	if !strings.Contains(text.Text, "Status:") {
		t.Error("expected 'Status:' in output")
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

	if !strings.Contains(text.Text, "No documentation indexed yet") {
		t.Errorf("expected empty message, got: %s", text.Text)
	}
}

func TestFormatBytes(t *testing.T) {
	tests := []struct {
		bytes int64
		want  string
	}{
		{0, "0 B"},
		{500, "500 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1048576, "1.0 MB"},
		{2621440, "2.5 MB"},
	}
	for _, tt := range tests {
		got := formatBytes(tt.bytes)
		if got != tt.want {
			t.Errorf("formatBytes(%d) = %q, want %q", tt.bytes, got, tt.want)
		}
	}
}

func TestFormatRepoStatusQueued(t *testing.T) {
	tests := []struct {
		name string
		repo store.Repo
		want string
	}{
		{
			name: "queued with position hint",
			repo: store.Repo{Status: store.StatusQueued, StatusDetail: "queued, ~1 ahead"},
			want: "queued, ~1 ahead",
		},
		{
			name: "queued without detail",
			repo: store.Repo{Status: store.StatusQueued, StatusDetail: ""},
			want: "queued",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatRepoStatus(&tt.repo)
			if got != tt.want {
				t.Errorf("formatRepoStatus() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestListReposShowsQueuedStatus(t *testing.T) {
	cs, _, s, cleanup := setupListReposTest(t, false)
	defer cleanup()

	repoID, err := s.UpsertRepo("queued-alias", "https://example.com/repo", `["docs"]`, "git")
	if err != nil {
		t.Fatalf("upsert repo: %v", err)
	}
	if err := s.UpdateRepoStatus(repoID, store.StatusQueued, "queued, ~3 ahead"); err != nil {
		t.Fatalf("update status: %v", err)
	}

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

	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}

	if !strings.Contains(text.Text, "Status: queued, ~3 ahead") {
		t.Errorf("expected 'Status: queued, ~3 ahead' in output, got: %s", text.Text)
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
