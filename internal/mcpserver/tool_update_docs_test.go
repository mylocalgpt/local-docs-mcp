package mcpserver

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mylocalgpt/local-docs-mcp/internal/config"
	"github.com/mylocalgpt/local-docs-mcp/internal/indexer"
	"github.com/mylocalgpt/local-docs-mcp/internal/search"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

// setupUpdateTest creates an in-memory store, an MCP server with a config,
// and returns a connected client session for tool calls.
// The indexer is nil because real git operations are not feasible in unit tests.
func setupUpdateTest(t *testing.T, cfg *config.Config) (*mcp.ClientSession, *mcp.ServerSession, *Server, func()) {
	t.Helper()

	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}

	srch := search.NewSearch(s)

	srv := New(s, srch, nil, cfg)

	ctx, cancel := context.WithCancel(context.Background())

	st, ct := mcp.NewInMemoryTransports()

	serverSession, err := srv.MCPServer().Connect(ctx, st, nil)
	if err != nil {
		cancel()
		s.Close() //nolint:errcheck
		t.Fatalf("server connect: %v", err)
	}

	client := mcp.NewClient(&mcp.Implementation{
		Name:    "test-client",
		Version: "v0.0.1",
	}, nil)

	clientSession, err := client.Connect(ctx, ct, nil)
	if err != nil {
		cancel()
		s.Close() //nolint:errcheck
		t.Fatalf("client connect: %v", err)
	}

	cleanup := func() {
		clientSession.Close() //nolint:errcheck
		serverSession.Wait()  //nolint:errcheck
		cancel()
		s.Close() //nolint:errcheck
	}

	return clientSession, serverSession, srv, cleanup
}

func TestUpdateDocsNoIndexer(t *testing.T) {
	cfg := &config.Config{Repos: []config.RepoConfig{
		{URL: "https://example.com/repo.git", Paths: []string{"docs"}, Alias: "test"},
	}}

	cs, _, _, cleanup := setupUpdateTest(t, cfg)
	defer cleanup()

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name:      "update_docs",
		Arguments: map[string]any{},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}

	// Handler returns an error when indexer is nil, SDK wraps it
	if !result.IsError {
		t.Fatal("expected IsError=true when indexer is nil")
	}

	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}

	if !strings.Contains(text.Text, "indexer not available") {
		t.Errorf("expected 'indexer not available' in error, got: %s", text.Text)
	}
}

func TestUpdateDocsRepoNotFound(t *testing.T) {
	cfg := &config.Config{Repos: []config.RepoConfig{
		{URL: "https://example.com/repo.git", Paths: []string{"docs"}, Alias: "real-repo"},
	}}

	cs, _, srv, cleanup := setupUpdateTest(t, cfg)
	defer cleanup()

	// Set a non-nil indexer so the handler progresses past the nil check.
	// We create a real indexer (it won't actually clone anything in this test
	// path because the repo alias won't match).
	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatalf("create temp store: %v", err)
	}
	defer s.Close() //nolint:errcheck

	ix, err := indexer.NewIndexer(s)
	if err != nil {
		t.Fatalf("create indexer: %v", err)
	}
	defer ix.Cleanup() //nolint:errcheck

	srv.indexer = ix

	result, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "update_docs",
		Arguments: map[string]any{
			"repo": "nonexistent",
		},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}

	if !result.IsError {
		t.Fatal("expected IsError=true for unknown repo")
	}

	text, ok := result.Content[0].(*mcp.TextContent)
	if !ok {
		t.Fatalf("expected TextContent, got %T", result.Content[0])
	}

	if !strings.Contains(text.Text, "not found") {
		t.Errorf("expected 'not found' in error, got: %s", text.Text)
	}
}

func TestUpdateDocsWaitsForRunningJob(t *testing.T) {
	cfg := &config.Config{Repos: []config.RepoConfig{}}

	cs, _, srv, cleanup := setupUpdateTest(t, cfg)
	defer cleanup()

	// Pre-populate a repo so update_docs has something to enqueue.
	if _, err := srv.store.UpsertRepo("test", "https://example.com/repo.git", `["docs"]`, "git"); err != nil {
		t.Fatalf("upsert repo: %v", err)
	}

	bi := NewBlockingIndexer()
	srv.indexer = bi

	// Block the alias once so the worker pauses inside IndexRepo. update_docs
	// is supposed to wait for that to finish, then report its own result.
	release := bi.Block("test")

	workerCtx, workerCancel := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		srv.queue.worker(workerCtx, srv.runJob)
		close(workerDone)
	}()
	defer func() {
		workerCancel()
		<-workerDone
	}()

	resultCh := make(chan *mcp.CallToolResult, 1)
	errCh := make(chan error, 1)
	go func() {
		r, e := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "update_docs",
			Arguments: map[string]any{},
		})
		resultCh <- r
		errCh <- e
	}()

	// Give the call time to enqueue and start running.
	time.Sleep(50 * time.Millisecond)

	// Confirm it hasn't returned yet.
	select {
	case <-resultCh:
		t.Fatal("update_docs returned before its job was unblocked")
	default:
	}

	close(release)

	select {
	case r := <-resultCh:
		err := <-errCh
		if err != nil {
			t.Fatalf("update_docs: %v", err)
		}
		if r.IsError {
			t.Fatalf("update_docs IsError=true; got %s", r.Content[0].(*mcp.TextContent).Text)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("update_docs never returned after job was unblocked")
	}
}

func TestFormatResult(t *testing.T) {
	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer s.Close() //nolint:errcheck

	// Insert a repo so GetRepo works for commit SHA lookup
	repoID, err := s.UpsertRepo("myrepo", "https://example.com/repo.git", `["docs"]`, "git")
	if err != nil {
		t.Fatalf("upsert repo: %v", err)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	if err := s.UpdateRepoIndex(repoID, "abcdef1234567890", now, 10); err != nil {
		t.Fatalf("update repo index: %v", err)
	}

	srv := &Server{store: s}

	tests := []struct {
		name     string
		result   *indexer.IndexResult
		contains []string
	}{
		{
			name: "error result",
			result: &indexer.IndexResult{
				Repo:  "myrepo",
				Error: fmt.Errorf("clone failed: connection refused"),
			},
			contains: []string{"myrepo: error", "clone failed"},
		},
		{
			name: "skipped result",
			result: &indexer.IndexResult{
				Repo:    "myrepo",
				Skipped: true,
			},
			contains: []string{"myrepo: skipped (unchanged)"},
		},
		{
			name: "success result with commit sha",
			result: &indexer.IndexResult{
				Repo:        "myrepo",
				DocsIndexed: 42,
				Duration:    2100 * time.Millisecond,
			},
			contains: []string{"myrepo: indexed 42 docs", "2.1s", "commit abcdef1"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var b strings.Builder
			srv.formatResult(&b, tt.result)
			got := b.String()
			for _, want := range tt.contains {
				if !strings.Contains(got, want) {
					t.Errorf("formatResult output %q missing %q", got, want)
				}
			}
		})
	}
}

func TestTruncateSHA(t *testing.T) {
	if got := truncateSHA("abcdef1234567890"); got != "abcdef1" {
		t.Errorf("truncateSHA(long) = %q, want %q", got, "abcdef1")
	}
	if got := truncateSHA("abc"); got != "abc" {
		t.Errorf("truncateSHA(short) = %q, want %q", got, "abc")
	}
}

func TestAutoRefreshStaleDetection(t *testing.T) {
	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer s.Close() //nolint:errcheck

	// Insert a stale repo (indexed 2 days ago)
	repoID, err := s.UpsertRepo("stale-repo", "https://example.com/stale.git", `["docs"]`, "git")
	if err != nil {
		t.Fatalf("upsert repo: %v", err)
	}
	staleTime := time.Now().UTC().Add(-48 * time.Hour).Format(time.RFC3339)
	if err := s.UpdateRepoIndex(repoID, "oldsha", staleTime, 5); err != nil {
		t.Fatalf("update repo index: %v", err)
	}

	// Insert a fresh repo (indexed 1 hour ago)
	repoID2, err := s.UpsertRepo("fresh-repo", "https://example.com/fresh.git", `["docs"]`, "git")
	if err != nil {
		t.Fatalf("upsert repo: %v", err)
	}
	freshTime := time.Now().UTC().Add(-1 * time.Hour).Format(time.RFC3339)
	if err := s.UpdateRepoIndex(repoID2, "newsha", freshTime, 10); err != nil {
		t.Fatalf("update repo index: %v", err)
	}

	// Verify staleness detection logic directly
	staleRepo, err := s.GetRepo("stale-repo")
	if err != nil {
		t.Fatalf("get stale repo: %v", err)
	}
	if staleRepo == nil {
		t.Fatal("stale repo should exist")
	}

	staleT, err := time.Parse(time.RFC3339, staleRepo.IndexedAt)
	if err != nil {
		t.Fatalf("parse stale time: %v", err)
	}
	if time.Since(staleT) <= 24*time.Hour {
		t.Error("stale repo should be older than 24h")
	}

	freshRepo, err := s.GetRepo("fresh-repo")
	if err != nil {
		t.Fatalf("get fresh repo: %v", err)
	}
	if freshRepo == nil {
		t.Fatal("fresh repo should exist")
	}

	freshT, err := time.Parse(time.RFC3339, freshRepo.IndexedAt)
	if err != nil {
		t.Fatalf("parse fresh time: %v", err)
	}
	if time.Since(freshT) > 24*time.Hour {
		t.Error("fresh repo should be within 24h")
	}

	// Verify never-indexed detection
	neverIndexed, err := s.GetRepo("nonexistent")
	if err != nil {
		t.Fatalf("get nonexistent repo: %v", err)
	}
	if neverIndexed != nil {
		t.Error("nonexistent repo should return nil")
	}
}

func TestAutoRefreshNilIndexer(t *testing.T) {
	// autoRefresh should return immediately when indexer is nil
	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer s.Close() //nolint:errcheck

	srv := &Server{
		store:   s,
		indexer: nil,
		config: &config.Config{Repos: []config.RepoConfig{
			{URL: "https://example.com/repo.git", Paths: []string{"docs"}, Alias: "test"},
		}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Should not panic or block
	srv.autoRefresh(ctx)
}

func TestAutoRefreshCancelledContext(t *testing.T) {
	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	defer s.Close() //nolint:errcheck

	ix, err := indexer.NewIndexer(s)
	if err != nil {
		t.Fatalf("create indexer: %v", err)
	}
	defer ix.Cleanup() //nolint:errcheck

	srv := &Server{
		store:   s,
		indexer: ix,
		config: &config.Config{Repos: []config.RepoConfig{
			{URL: "https://example.com/repo.git", Paths: []string{"docs"}, Alias: "test"},
		}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Should return quickly without attempting any indexing
	srv.autoRefresh(ctx)
}

// TestUpdateSingleRepoCancelRevertsStatus verifies that cancelling the
// caller's ctx while the repo is still queued behind another job dequeues
// the pending entry AND reverts the repo's DB status from queued back to
// its prior status. Without the revert the repo would strand in
// StatusQueued for the lifetime of the process (autoRefresh skips queued).
func TestUpdateSingleRepoCancelRevertsStatus(t *testing.T) {
	cs, _, srv, cleanup := setupUpdateTest(t, nil)
	defer cleanup()

	const seedAlias = "seed"
	const targetAlias = "alpha"

	if _, err := srv.store.UpsertRepo(seedAlias, "https://example.com/seed", `["docs"]`, "git"); err != nil {
		t.Fatalf("upsert seed: %v", err)
	}
	if _, err := srv.store.UpsertRepo(targetAlias, "https://example.com/alpha", `["docs"]`, "git"); err != nil {
		t.Fatalf("upsert alpha: %v", err)
	}

	bi := NewBlockingIndexer()
	srv.indexer = bi
	releaseSeed := bi.Block(seedAlias)

	workerCtx, workerCancel := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		srv.queue.worker(workerCtx, srv.runJob)
		close(workerDone)
	}()
	defer func() {
		workerCancel()
		<-workerDone
	}()

	// Occupy the worker with the seed job so alpha will sit in the queue.
	seedRepo, err := srv.store.GetRepo(seedAlias)
	if err != nil || seedRepo == nil {
		t.Fatalf("get seed: %v / %v", seedRepo, err)
	}
	seedJob, err := buildJobFromRepo(seedRepo, priorityUser)
	if err != nil {
		t.Fatalf("build seed job: %v", err)
	}
	if _, _, _, _, err := srv.queue.enqueue(seedJob); err != nil {
		t.Fatalf("enqueue seed: %v", err)
	}

	// Wait until the worker is inside the seed call.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(bi.CallsFor(seedAlias)) >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := len(bi.CallsFor(seedAlias)); got != 1 {
		t.Fatalf("expected seed in flight, got %d calls", got)
	}

	callCtx, cancelCall := context.WithCancel(context.Background())
	callDone := make(chan error, 1)
	go func() {
		_, err := cs.CallTool(callCtx, &mcp.CallToolParams{
			Name:      "update_docs",
			Arguments: map[string]any{"repo": targetAlias},
		})
		callDone <- err
	}()

	// Give alpha time to enqueue.
	time.Sleep(100 * time.Millisecond)

	// Best-effort: alpha should sit in StatusQueued mid-flight. Inherently
	// racy on slow CI; log only.
	if r, _ := srv.store.GetRepo(targetAlias); r != nil && r.Status != store.StatusQueued {
		t.Logf("alpha mid-flight status = %q (expected queued; racy assertion)", r.Status)
	}

	cancelCall()

	select {
	case <-callDone:
	case <-time.After(2 * time.Second):
		t.Fatal("update_docs did not return after cancel")
	}

	// Server-side handler may complete the dequeue+revert AFTER the SDK
	// returns ctx.Err to the client, so poll briefly for the revert to land.
	var r *store.Repo
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r, err = srv.store.GetRepo(targetAlias)
		if err == nil && r != nil && r.Status != store.StatusQueued {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("get alpha: %v", err)
	}
	if r == nil {
		t.Fatal("alpha repo missing after cancel")
	}
	if r.Status != store.StatusReady {
		t.Errorf("alpha status after cancel = %q, want %q", r.Status, store.StatusReady)
	}
	if r.StatusDetail != "" {
		t.Errorf("alpha status_detail after cancel = %q, want empty", r.StatusDetail)
	}

	// Cleanup so the worker can exit.
	close(releaseSeed)
}

// TestUpdateAllReposCancelRevertsAllPending verifies that cancelling
// updateAllRepos mid-flight reverts every still-queued repo (covers both
// the early-skip and ctx-done branches in the result-collection loop).
func TestUpdateAllReposCancelRevertsAllPending(t *testing.T) {
	cs, _, srv, cleanup := setupUpdateTest(t, nil)
	defer cleanup()

	const seedAlias = "seed"
	for _, alias := range []string{seedAlias, "a", "b", "c"} {
		if _, err := srv.store.UpsertRepo(alias, "https://example.com/"+alias, `["docs"]`, "git"); err != nil {
			t.Fatalf("upsert %s: %v", alias, err)
		}
	}

	bi := NewBlockingIndexer()
	srv.indexer = bi
	releaseSeed := bi.Block(seedAlias)
	releaseA := bi.Block("a")
	releaseB := bi.Block("b")
	releaseC := bi.Block("c")

	workerCtx, workerCancel := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		srv.queue.worker(workerCtx, srv.runJob)
		close(workerDone)
	}()
	defer func() {
		workerCancel()
		<-workerDone
	}()

	seedRepo, err := srv.store.GetRepo(seedAlias)
	if err != nil || seedRepo == nil {
		t.Fatalf("get seed: %v / %v", seedRepo, err)
	}
	seedJob, err := buildJobFromRepo(seedRepo, priorityUser)
	if err != nil {
		t.Fatalf("build seed job: %v", err)
	}
	if _, _, _, _, err := srv.queue.enqueue(seedJob); err != nil {
		t.Fatalf("enqueue seed: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(bi.CallsFor(seedAlias)) >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	callCtx, cancelCall := context.WithCancel(context.Background())
	callDone := make(chan error, 1)
	go func() {
		_, err := cs.CallTool(callCtx, &mcp.CallToolParams{
			Name:      "update_docs",
			Arguments: map[string]any{},
		})
		callDone <- err
	}()

	// Give a/b/c time to enqueue.
	time.Sleep(150 * time.Millisecond)

	for _, alias := range []string{"a", "b", "c"} {
		if r, _ := srv.store.GetRepo(alias); r != nil && r.Status != store.StatusQueued {
			t.Logf("%s mid-flight status = %q (expected queued; racy)", alias, r.Status)
		}
	}

	cancelCall()

	select {
	case <-callDone:
	case <-time.After(2 * time.Second):
		t.Fatal("update_docs did not return after cancel")
	}

	// Poll: server-side cancel branch may finish reverting after the SDK
	// returns ctx.Err to the client.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		allReady := true
		for _, alias := range []string{"a", "b", "c"} {
			r, _ := srv.store.GetRepo(alias)
			if r == nil || r.Status == store.StatusQueued {
				allReady = false
				break
			}
		}
		if allReady {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	for _, alias := range []string{"a", "b", "c"} {
		r, err := srv.store.GetRepo(alias)
		if err != nil {
			t.Fatalf("get %s: %v", alias, err)
		}
		if r == nil {
			t.Fatalf("%s repo missing after cancel", alias)
		}
		if r.Status != store.StatusReady {
			t.Errorf("%s status after cancel = %q, want %q", alias, r.Status, store.StatusReady)
		}
		if r.StatusDetail != "" {
			t.Errorf("%s status_detail after cancel = %q, want empty", alias, r.StatusDetail)
		}
	}

	close(releaseSeed)
	close(releaseA)
	close(releaseB)
	close(releaseC)
}
