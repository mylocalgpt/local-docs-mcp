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

	// TODO(phase3): add cancel-while-blocked variant asserting context.Canceled + PriorStatus restore.
}
