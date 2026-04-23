package mcpserver

import (
	"context"
	"log"
	"testing"
	"time"

	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)
// TestServerLifecycle_DrainAfterCompletedJob exercises the worker + drain
// path that Server.Run owns at shutdown. The test starts the queue worker
// directly (Server.Run owns stdio so we cannot call it in a unit test),
// pumps a single add_docs through to completion, then cancels the worker
// context and verifies drainPending sees nothing because the only job
// finished cleanly before cancel.
//
// Phase 3 will add a sibling test where cancel happens WHILE the indexer
// is blocked, asserting that the in-flight job receives context.Canceled
// and the repo's PriorStatus is restored.
func TestServerLifecycle_DrainAfterCompletedJob(t *testing.T) {
	cs, srv, cleanup := setupAddDocsTest(t)
	defer cleanup()

	bi := NewBlockingIndexer()
	srv.indexer = bi

	// Use a local source so add_docs does not require git or network. Git
	// sources need indexer.CheckGitVersion() and a real reachable URL.
	dir := t.TempDir()

	// Prime the block BEFORE issuing add_docs so the worker is guaranteed
	// to be parked inside IndexLocalPath when we observe StatusIndexing.
	release := bi.Block("foo")

	workerCtx, cancel := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		srv.queue.worker(workerCtx, srv.runJob)
	}()

	if _, err := callAddDocs(t, cs, map[string]any{
		"alias": "foo",
		"path":  dir,
	}); err != nil {
		cancel()
		<-workerDone
		t.Fatalf("add_docs: %v", err)
	}

	// Wait for the worker to flip the repo into indexing.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		repo, _ := srv.store.GetRepo("foo")
		if repo != nil && repo.Status == store.StatusIndexing {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	repo, err := srv.store.GetRepo("foo")
	if err != nil {
		cancel()
		close(release)
		<-workerDone
		t.Fatalf("get foo: %v", err)
	}
	if repo == nil || repo.Status != store.StatusIndexing {
		cancel()
		close(release)
		<-workerDone
		t.Fatalf("expected foo status=indexing, got %+v", repo)
	}

	// Cancel the worker, then release the blocking indexer. The worker is
	// inside the synchronous job call; it cannot observe ctx until the job
	// returns. So the order is: cancel -> release -> job completes ->
	// worker loop sees ctx.Done -> goroutine exits.
	cancel()
	close(release)

	select {
	case <-workerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("worker goroutine did not exit after cancel + release")
	}

	// Mirror what Server.Run does after the worker exits: drain anything
	// still queued and revert PriorStatus per drained job. The job ran to
	// completion before cancel, so the slice should be empty.
	drained := srv.queue.drainPending()
	for _, j := range drained {
		if j.RepoID == 0 {
			continue
		}
		prior := j.PriorStatus
		if prior == "" {
			prior = store.StatusReady
		}
		if dbErr := srv.store.UpdateRepoStatus(j.RepoID, prior, ""); dbErr != nil {
			log.Printf("queue: revert status for %s on shutdown: %v", j.Alias, dbErr)
		}
	}
	if len(drained) != 0 {
		t.Fatalf("drainPending returned %d jobs, want 0", len(drained))
	}

	// Final state: foo is ready, queue maps are empty. Read the queue
	// internals under mu so -race stays quiet.
	repo, err = srv.store.GetRepo("foo")
	if err != nil {
		t.Fatalf("get foo: %v", err)
	}
	if repo == nil || repo.Status != store.StatusReady {
		t.Fatalf("expected foo final status=ready, got %+v", repo)
	}

	srv.queue.mu.Lock()
	pendingLen := len(srv.queue.pending)
	running := srv.queue.running
	srv.queue.mu.Unlock()
	if pendingLen != 0 {
		t.Errorf("queue.pending has %d entries, want 0", pendingLen)
	}
	if running != "" {
		t.Errorf("queue.running = %q, want empty", running)
	}
}

// TestServerLifecycle_CancelWhileBlocked exercises the Phase 3 shutdown
// cancellation path: ctx is cancelled WHILE the indexer is parked inside an
// IndexLocalPath call. Asserts the worker exits promptly, the in-flight job
// receives context.Canceled (not StatusError), and the repo's PriorStatus
// (ready) is restored rather than left as indexing.
func TestServerLifecycle_CancelWhileBlocked(t *testing.T) {
	cs, srv, cleanup := setupAddDocsTest(t)
	defer cleanup()

	bi := NewBlockingIndexer()
	srv.indexer = bi

	dir := t.TempDir()

	// Pre-seed PriorStatus so the cancel path can prove the revert.
	// UpsertRepo inserts the row but does not write status; a follow-up
	// UpdateRepoStatus moves it to ready, which add_docs's handler then
	// reads into Job.PriorStatus.
	repoID, err := srv.store.UpsertRepo("foo", dir, "[]", "local")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	if err := srv.store.UpdateRepoStatus(repoID, store.StatusReady, ""); err != nil {
		t.Fatalf("seed ready status: %v", err)
	}

	release := bi.Block("foo")

	workerCtx, cancel := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		defer close(workerDone)
		srv.queue.worker(workerCtx, srv.runJob)
	}()

	if _, err := callAddDocs(t, cs, map[string]any{
		"alias": "foo",
		"path":  dir,
	}); err != nil {
		cancel()
		close(release)
		<-workerDone
		t.Fatalf("add_docs: %v", err)
	}

	// Wait for the worker to flip the repo into indexing (i.e. it is now
	// parked inside BlockingIndexer.recordAndWait).
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		repo, _ := srv.store.GetRepo("foo")
		if repo != nil && repo.Status == store.StatusIndexing {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	repo, err := srv.store.GetRepo("foo")
	if err != nil {
		cancel()
		close(release)
		<-workerDone
		t.Fatalf("get foo: %v", err)
	}
	if repo == nil || repo.Status != store.StatusIndexing {
		cancel()
		close(release)
		<-workerDone
		t.Fatalf("expected foo status=indexing, got %+v", repo)
	}

	// Cancel the worker context. The ctx-aware recordAndWait returns
	// context.Canceled, runJob takes the cancellation branch, and the
	// worker loop exits. We do NOT close(release): the whole point is to
	// prove cancellation kicks in without it.
	cancel()

	select {
	case <-workerDone:
	case <-time.After(2 * time.Second):
		// Release the block as a best-effort cleanup before failing so
		// other tests aren't dragged down by a hung goroutine.
		close(release)
		t.Fatal("worker goroutine did not exit within 2s after cancel")
	}

	// drainPending should be empty: handle() removed the in-flight job
	// from pending before invoking run(), so cancellation routes through
	// the runJob context-cancelled branch, not the drain path.
	drained := srv.queue.drainPending()
	if len(drained) != 0 {
		t.Fatalf("drainPending returned %d jobs, want 0", len(drained))
	}

	// Final status must be exactly ready (the PriorStatus we seeded), not
	// error and not indexing. This proves the revert path works.
	repo, err = srv.store.GetRepo("foo")
	if err != nil {
		t.Fatalf("get foo: %v", err)
	}
	if repo == nil {
		t.Fatal("expected foo to exist after cancel")
	}
	if repo.Status != store.StatusReady {
		t.Errorf("expected foo final status=ready, got %q", repo.Status)
	}

	// Exactly one IndexLocalPath call should have been recorded for foo.
	if calls := bi.CallsFor("foo"); len(calls) != 1 {
		t.Errorf("expected 1 indexer call for foo, got %d", len(calls))
	}

	// Silence unused-import warning if log is no longer referenced after
	// future edits. (No-op write.)
	_ = log.Default()
}
