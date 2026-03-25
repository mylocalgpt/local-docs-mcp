package mcpserver

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
	"github.com/mylocalgpt/local-docs-mcp/internal/search"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

// setupAddDocsTest creates a server with a real indexer (no config needed)
// and returns a connected client session.
func setupAddDocsTest(t *testing.T) (*mcp.ClientSession, *Server, func()) {
	t.Helper()

	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	srch := search.NewSearch(s)

	ix, err := indexer.NewIndexer(s)
	if err != nil {
		s.Close()
		t.Fatalf("create indexer: %v", err)
	}

	cfg := &config.Config{Repos: []config.RepoConfig{}}
	srv := New(s, srch, ix, cfg)

	ctx, cancel := context.WithCancel(context.Background())

	st, ct := mcp.NewInMemoryTransports()

	_, err = srv.MCPServer().Connect(ctx, st, nil)
	if err != nil {
		cancel()
		ix.Cleanup()
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
		ix.Cleanup()
		s.Close()
		t.Fatalf("client connect: %v", err)
	}

	cleanup := func() {
		cancel()
		ix.Cleanup()
		s.Close()
	}

	return clientSession, srv, cleanup
}

func callAddDocs(t *testing.T, cs *mcp.ClientSession, args map[string]any) (*mcp.CallToolResult, error) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	return cs.CallTool(ctx, &mcp.CallToolParams{
		Name:      "add_docs",
		Arguments: args,
	})
}

func TestAddDocsValidationMissingAlias(t *testing.T) {
	cs, _, cleanup := setupAddDocsTest(t)
	defer cleanup()

	// SDK enforces "alias" as required at the schema level,
	// so this returns a Go error, not a tool error.
	_, err := callAddDocs(t, cs, map[string]any{
		"url":   "https://example.com/repo",
		"paths": []string{"docs/"},
	})
	if err == nil {
		t.Fatal("expected error for missing alias")
	}
}

func TestAddDocsValidationNeitherURLNorPath(t *testing.T) {
	cs, _, cleanup := setupAddDocsTest(t)
	defer cleanup()

	result, err := callAddDocs(t, cs, map[string]any{
		"alias": "test",
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true when neither url nor path provided")
	}
}

func TestAddDocsValidationBothURLAndPath(t *testing.T) {
	cs, _, cleanup := setupAddDocsTest(t)
	defer cleanup()

	result, err := callAddDocs(t, cs, map[string]any{
		"alias": "test",
		"url":   "https://example.com/repo",
		"path":  "/tmp",
		"paths": []string{"docs/"},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if !result.IsError {
		t.Fatal("expected IsError=true when both url and path provided")
	}
}

func TestAddDocsLocalSource(t *testing.T) {
	cs, srv, cleanup := setupAddDocsTest(t)

	// Create a temp directory with markdown files
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "readme.md"), []byte("# Hello\n\nWorld.\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := callAddDocs(t, cs, map[string]any{
		"alias": "local-test",
		"path":  dir,
	})
	if err != nil {
		t.Fatalf("add_docs local failed: %v", err)
	}

	text := result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "local-test") {
		t.Errorf("response should contain alias, got: %s", text)
	}

	// Wait for background indexing to complete by polling the mutex.
	// The goroutine holds the lock while indexing; when we can acquire it,
	// indexing is done.
	waitDone := make(chan struct{})
	go func() {
		srv.indexMu.Lock()
		srv.indexMu.Unlock()
		close(waitDone)
	}()

	select {
	case <-waitDone:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for indexing to complete")
	}

	// Now safe to close
	defer cleanup()

	repo, err := srv.store.GetRepo("local-test")
	if err != nil {
		t.Fatal(err)
	}
	if repo == nil {
		t.Fatal("expected repo record to exist")
	}
	if repo.Status != store.StatusReady {
		t.Errorf("status: got %q, want %q", repo.Status, store.StatusReady)
	}
	if repo.DocCount == 0 {
		t.Error("expected doc count > 0 after indexing")
	}
	if repo.SourceType != "local" {
		t.Errorf("source type: got %q, want %q", repo.SourceType, "local")
	}
}

func TestAddDocsMutexContention(t *testing.T) {
	cs, srv, cleanup := setupAddDocsTest(t)
	defer cleanup()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "test.md"), []byte("# Test\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Hold the lock manually
	srv.indexMu.Lock()

	result, err := callAddDocs(t, cs, map[string]any{
		"alias": "locked-test",
		"path":  dir,
	})

	srv.indexMu.Unlock()

	if err != nil {
		t.Fatalf("expected non-error response, got: %v", err)
	}

	text := result.Content[0].(*mcp.TextContent).Text
	if !strings.Contains(text, "already in progress") {
		t.Errorf("expected 'already in progress' message, got: %s", text)
	}
}
