package mcpserver

import (
	"context"
	"testing"
	"time"

	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

// TestAddDocsNoStatusFlicker verifies the indexing -> queued -> indexing
// race is gone. Before Phase 2, the handler wrote StatusQueued AFTER
// enqueue returned, racing with the worker's StatusIndexing write inside
// runJob. Phase 2 moved the StatusQueued write into enqueue under SQLite's
// single-writer ordering so no flicker can occur.
//
// Strategy: prime a BlockingIndexer block on alias "foo" so the worker
// pauses inside IndexLocalPath right after writing StatusIndexing. From
// the moment we first observe StatusIndexing, poll for ~50ms and assert
// the status never reverts to queued. Then release the worker and confirm
// final status is ready.
func TestAddDocsNoStatusFlicker(t *testing.T) {
	cs, srv, cleanup := setupAddDocsTest(t)
	defer cleanup()

	bi := NewBlockingIndexer()
	srv.indexer = bi

	dir := t.TempDir()

	// Prime the block BEFORE add_docs so the worker is parked inside
	// IndexLocalPath after writing StatusIndexing.
	release := bi.Block("foo")

	workerCtx, cancel := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		srv.queue.worker(workerCtx, srv.runJob)
	}()
	defer func() {
		cancel()
		<-workerDone
	}()

	if _, err := callAddDocs(t, cs, map[string]any{
		"alias": "foo",
		"path":  dir,
	}); err != nil {
		close(release)
		t.Fatalf("add_docs: %v", err)
	}

	// Right after add_docs returns, status should be queued or indexing.
	// Both are valid: the worker may have already advanced.
	repo, err := srv.store.GetRepo("foo")
	if err != nil {
		close(release)
		t.Fatalf("get foo: %v", err)
	}
	if repo == nil || (repo.Status != store.StatusQueued && repo.Status != store.StatusIndexing) {
		close(release)
		t.Fatalf("expected foo status queued|indexing immediately after add_docs, got %+v", repo)
	}

	// Wait until we observe indexing.
	deadline := time.Now().Add(2 * time.Second)
	sawIndexing := false
	for time.Now().Before(deadline) {
		r, gerr := srv.store.GetRepo("foo")
		if gerr == nil && r != nil && r.Status == store.StatusIndexing {
			sawIndexing = true
			break
		}
		time.Sleep(2 * time.Millisecond)
	}
	if !sawIndexing {
		close(release)
		t.Fatal("never observed status=indexing within 2s")
	}

	// Now poll for ~50ms and assert status NEVER reverts to queued.
	flickerDeadline := time.Now().Add(50 * time.Millisecond)
	for time.Now().Before(flickerDeadline) {
		r, gerr := srv.store.GetRepo("foo")
		if gerr != nil {
			close(release)
			t.Fatalf("get foo during flicker check: %v", gerr)
		}
		if r != nil && r.Status == store.StatusQueued {
			close(release)
			t.Fatalf("status reverted to queued after indexing: %+v", r)
		}
		time.Sleep(2 * time.Millisecond)
	}

	// Release the worker; let the job finish; assert final status is ready.
	close(release)

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		r, _ := srv.store.GetRepo("foo")
		if r != nil && r.Status == store.StatusReady {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	r, _ := srv.store.GetRepo("foo")
	t.Fatalf("expected final status=ready within 2s, got %+v", r)
}
