package mcpserver

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

// TestAddDocs_ConcurrentSameAlias_MergesPaths is the regression test for the
// pre-queue path-loss bug. While job A is mid-run for an alias, a second
// add_docs for the same alias with a new path should coalesce into a follow-up
// run that also covers the new path - never silently dropped.
func TestAddDocs_ConcurrentSameAlias_MergesPaths(t *testing.T) {
	cs, srv, cleanup := setupAddDocsTest(t)
	defer cleanup()

	bi := NewBlockingIndexer()
	srv.indexer = bi

	release := bi.Block("foo")

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

	// First add_docs with paths=[A]. This will reach the indexer and block.
	if _, err := callAddDocs(t, cs, map[string]any{
		"alias": "foo",
		"url":   "https://example.com/foo",
		"paths": []string{"A/"},
	}); err != nil {
		t.Fatalf("first add_docs: %v", err)
	}

	// Wait for the first call to actually be inside IndexRepo.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(bi.CallsFor("foo")) >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := len(bi.CallsFor("foo")); got != 1 {
		t.Fatalf("expected first call to be in flight, got %d calls", got)
	}

	// Second add_docs with paths=[B]. This must coalesce as a follow-up so
	// the worker re-runs with merged paths once the first call returns.
	if _, err := callAddDocs(t, cs, map[string]any{
		"alias": "foo",
		"url":   "https://example.com/foo",
		"paths": []string{"B/"},
	}); err != nil {
		t.Fatalf("second add_docs: %v", err)
	}

	close(release)

	// Wait for the second invocation to land. The coalesced job should fire
	// after the first one completes and trigger a second IndexRepo call.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(bi.CallsFor("foo")) >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	calls := bi.CallsFor("foo")
	if len(calls) != 2 {
		t.Fatalf("expected 2 IndexRepo calls for foo (initial + coalesced follow-up), got %d", len(calls))
	}

	merged := append([]string{}, calls[1].Paths...)
	sort.Strings(merged)
	if len(merged) != 2 || merged[0] != "A/" || merged[1] != "B/" {
		t.Errorf("follow-up call paths = %v, want [A/ B/]", calls[1].Paths)
	}

	// Verify final DB state for the alias is ready.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		repo, _ := srv.store.GetRepo("foo")
		if repo != nil && repo.Status == store.StatusReady {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	repo, err := srv.store.GetRepo("foo")
	if err != nil {
		t.Fatalf("get foo: %v", err)
	}
	if repo == nil || repo.Status != store.StatusReady {
		t.Errorf("expected foo status=ready, got %v", repo)
	}
}

// TestUpdateDocs_CtxCancelDuringRun verifies that cancelling the caller's
// context while their job is mid-run returns ctx.Err() to the caller, but
// lets the worker finish the job so the DB ends up in the ready state.
func TestUpdateDocs_CtxCancelDuringRun(t *testing.T) {
	cs, _, srv, cleanup := setupUpdateTest(t, nil)
	defer cleanup()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "x.md"), []byte("# X\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := srv.store.UpsertRepo("alpha", dir, "[]", "local"); err != nil {
		t.Fatalf("upsert: %v", err)
	}

	bi := NewBlockingIndexer()
	srv.indexer = bi
	release := bi.Block("alpha")

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

	callCtx, cancelCall := context.WithCancel(context.Background())
	resultCh := make(chan error, 1)
	go func() {
		_, err := cs.CallTool(callCtx, &mcp.CallToolParams{
			Name:      "update_docs",
			Arguments: map[string]any{"repo": "alpha"},
		})
		resultCh <- err
	}()

	// Wait for the worker to be inside IndexLocalPath.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(bi.CallsFor("alpha")) >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}

	cancelCall()

	// The handler should return promptly with a ctx-cancel error wrapped by
	// the SDK. We do not assert on the exact wrapping, only that it returns
	// quickly while the worker is still parked.
	select {
	case <-resultCh:
	case <-time.After(2 * time.Second):
		t.Fatal("update_docs did not return after caller ctx cancel")
	}

	// The job continues to completion.
	close(release)
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		repo, _ := srv.store.GetRepo("alpha")
		if repo != nil && repo.Status == store.StatusReady {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	repo, _ := srv.store.GetRepo("alpha")
	t.Fatalf("expected alpha to reach ready, got %+v", repo)
}

// TestUpdateDocs_CtxCancelWhileQueued verifies that cancelling a caller's
// context while their job is still queued (worker busy with someone else)
// dequeues the pending job so the indexer is never invoked for it.
func TestUpdateDocs_CtxCancelWhileQueued(t *testing.T) {
	cs, _, srv, cleanup := setupUpdateTest(t, nil)
	defer cleanup()

	dirA := t.TempDir()
	if err := os.WriteFile(filepath.Join(dirA, "a.md"), []byte("# A\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	dirB := t.TempDir()
	if err := os.WriteFile(filepath.Join(dirB, "b.md"), []byte("# B\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, err := srv.store.UpsertRepo("a", dirA, "[]", "local"); err != nil {
		t.Fatalf("upsert a: %v", err)
	}
	if _, err := srv.store.UpsertRepo("b", dirB, "[]", "local"); err != nil {
		t.Fatalf("upsert b: %v", err)
	}

	bi := NewBlockingIndexer()
	srv.indexer = bi
	releaseA := bi.Block("a")

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

	// Caller 1: update_docs A. It will sit inside IndexLocalPath.
	aCallDone := make(chan error, 1)
	go func() {
		_, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
			Name:      "update_docs",
			Arguments: map[string]any{"repo": "a"},
		})
		aCallDone <- err
	}()

	// Wait for A to be in flight.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(bi.CallsFor("a")) >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	if got := len(bi.CallsFor("a")); got != 1 {
		t.Fatalf("expected A to be in flight, got %d calls", got)
	}

	// Caller 2: update_docs B. It will be queued behind A.
	bCtx, bCancel := context.WithCancel(context.Background())
	bCallDone := make(chan error, 1)
	go func() {
		_, err := cs.CallTool(bCtx, &mcp.CallToolParams{
			Name:      "update_docs",
			Arguments: map[string]any{"repo": "b"},
		})
		bCallDone <- err
	}()

	// Give B time to enqueue.
	time.Sleep(100 * time.Millisecond)

	// Cancel B's caller. This should dequeue B.
	bCancel()

	select {
	case <-bCallDone:
	case <-time.After(2 * time.Second):
		t.Fatal("update_docs B did not return after caller ctx cancel")
	}

	// Now release A so the worker drains.
	close(releaseA)

	select {
	case <-aCallDone:
	case <-time.After(2 * time.Second):
		t.Fatal("update_docs A did not complete")
	}

	// Give the worker one more tick to make sure it doesn't pick up B.
	time.Sleep(50 * time.Millisecond)

	if calls := bi.CallsFor("b"); len(calls) != 0 {
		var aliases []string
		for _, c := range bi.Calls() {
			aliases = append(aliases, c.Alias)
		}
		t.Fatalf("expected no IndexLocalPath calls for B (it was dequeued), got %d: aliases=%v", len(calls), strings.Join(aliases, ","))
	}
}
