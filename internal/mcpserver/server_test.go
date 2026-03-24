package mcpserver

import (
	"context"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mylocalgpt/local-docs-mcp/internal/config"
	"github.com/mylocalgpt/local-docs-mcp/internal/search"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

func TestNew(t *testing.T) {
	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer s.Close()

	srch := search.NewSearch(s)
	cfg := &config.Config{Repos: []config.RepoConfig{
		{URL: "https://example.com/repo.git", Paths: []string{"docs"}, Alias: "test"},
	}}

	srv := New(s, srch, nil, cfg)
	if srv == nil {
		t.Fatal("New returned nil")
	}
	if srv.server == nil {
		t.Fatal("server.server is nil")
	}
}

func TestServerInitialize(t *testing.T) {
	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer s.Close()

	srch := search.NewSearch(s)
	cfg := &config.Config{Repos: []config.RepoConfig{
		{URL: "https://example.com/repo.git", Paths: []string{"docs"}, Alias: "test"},
	}}

	srv := New(s, srch, nil, cfg)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Connect server and client via in-memory transports
	st, ct := mcp.NewInMemoryTransports()

	serverSession, err := srv.MCPServer().Connect(ctx, st, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "v0.0.1",
	}, nil)

	clientSession, err := client.Connect(ctx, ct, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}

	// Verify the server responded to initialization by checking server info
	result := clientSession.InitializeResult()
	if result == nil {
		t.Fatal("InitializeResult is nil")
	}
	if result.ServerInfo == nil {
		t.Fatal("ServerInfo is nil")
	}
	if result.ServerInfo.Name != "local-docs-mcp" {
		t.Errorf("server name = %q, want %q", result.ServerInfo.Name, "local-docs-mcp")
	}
	if result.Instructions == "" {
		t.Error("instructions should not be empty")
	}

	// Clean shutdown
	if err := clientSession.Close(); err != nil {
		t.Fatalf("client close: %v", err)
	}
	if err := serverSession.Wait(); err != nil {
		t.Fatalf("server wait: %v", err)
	}
}
