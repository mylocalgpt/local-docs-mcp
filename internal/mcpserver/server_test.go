package mcpserver

import (
	"context"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mylocalgpt/local-docs-mcp/internal/config"
	"github.com/mylocalgpt/local-docs-mcp/internal/indexer"
	"github.com/mylocalgpt/local-docs-mcp/internal/search"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

func TestNew(t *testing.T) {
	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer s.Close() //nolint:errcheck

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
	defer s.Close() //nolint:errcheck

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

// TestRunJobSkippedFilesStatusDetail verifies that when indexing succeeds but
// the indexer reports skipped (undecodable) files, runJob writes a
// human-readable summary into status_detail so list_repos surfaces it.
func TestRunJobSkippedFilesStatusDetail(t *testing.T) {
	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer s.Close() //nolint:errcheck

	srch := search.NewSearch(s)
	srv := New(s, srch, nil, nil)

	bi := NewBlockingIndexer()
	srv.indexer = bi

	const alias = "skip-test"
	repoID, err := s.UpsertRepo(alias, "/tmp/skip-test", "", "local")
	if err != nil {
		t.Fatalf("upsert repo: %v", err)
	}

	bi.SetResult(alias, &indexer.IndexResult{
		Repo:          alias,
		DocsIndexed:   3,
		FilesIndexed:  1,
		SkippedFiles:  2,
		SkippedSample: []string{"a.md", "b.md"},
	})

	job := &Job{
		Alias:  alias,
		Kind:   jobKindLocal,
		URL:    "/tmp/skip-test",
		RepoID: repoID,
		Done:   make(chan JobResult, 1),
	}

	res := srv.runJob(context.Background(), job)
	if res.Err != nil {
		t.Fatalf("runJob err: %v", res.Err)
	}

	repo, err := s.GetRepo(alias)
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if repo == nil {
		t.Fatal("repo not found")
	}
	if repo.Status != store.StatusReady {
		t.Errorf("status = %q, want %q", repo.Status, store.StatusReady)
	}
	for _, want := range []string{"indexed 1 files", "skipped 2", "a.md", "e.g."} {
		if !strings.Contains(repo.StatusDetail, want) {
			t.Errorf("status_detail %q missing %q", repo.StatusDetail, want)
		}
	}
}

func TestRunJobSkippedFilesStatusDetailNoSample(t *testing.T) {
	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer s.Close() //nolint:errcheck

	srch := search.NewSearch(s)
	srv := New(s, srch, nil, nil)

	bi := NewBlockingIndexer()
	srv.indexer = bi

	const alias = "skip-no-sample"
	repoID, err := s.UpsertRepo(alias, "/tmp/skip-no-sample", "", "local")
	if err != nil {
		t.Fatalf("upsert repo: %v", err)
	}

	bi.SetResult(alias, &indexer.IndexResult{
		Repo:         alias,
		DocsIndexed:  3,
		FilesIndexed: 1,
		SkippedFiles: 2,
	})

	job := &Job{
		Alias:  alias,
		Kind:   jobKindLocal,
		URL:    "/tmp/skip-no-sample",
		RepoID: repoID,
		Done:   make(chan JobResult, 1),
	}

	res := srv.runJob(context.Background(), job)
	if res.Err != nil {
		t.Fatalf("runJob err: %v", res.Err)
	}

	repo, err := s.GetRepo(alias)
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if repo == nil {
		t.Fatal("repo not found")
	}
	if !strings.Contains(repo.StatusDetail, "skipped 2") {
		t.Errorf("status_detail %q missing skipped count", repo.StatusDetail)
	}
	for _, unwanted := range []string{"(e.g. )", "e.g."} {
		if strings.Contains(repo.StatusDetail, unwanted) {
			t.Errorf("status_detail %q unexpectedly contains %q", repo.StatusDetail, unwanted)
		}
	}
}
