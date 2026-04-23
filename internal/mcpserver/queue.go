// Package mcpserver - queue.go implements an in-memory single-worker job
// queue for repository indexing operations. The queue has two priority lanes
// (user-initiated and background) and coalesces duplicate enqueues for the
// same repo alias. It is a pure data structure: the actual indexing work is
// performed by a `run` callback supplied by the worker's owner.
package mcpserver

import (
	"context"
	"errors"
	"fmt"
	"log"
	"runtime/debug"
	"sync"

	"github.com/mylocalgpt/local-docs-mcp/internal/indexer"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

// JobKind discriminates between git-sourced and local-directory indexing jobs.
type JobKind int

const (
	jobKindGit JobKind = iota
	jobKindLocal
)

// JobPriority selects which lane a job is enqueued to. priorityUser jobs are
// always picked up before priorityBackground jobs.
type JobPriority int

const (
	priorityUser JobPriority = iota
	priorityBackground
)

// JobResult carries the outcome of running a job. IndexResult is used by
// callers that need the per-file counts (e.g. update_docs); Err is non-nil on
// failure (including panics and shutdown cancellation).
type JobResult struct {
	IndexResult *indexer.IndexResult
	Err         error
}

// Job is one unit of indexing work. It is pushed onto the queue by callers
// and consumed by the single worker goroutine. Done is buffered (size 1) so
// the worker can always deliver the result without blocking, even when the
// caller has stopped listening (the async add_docs case).
type Job struct {
	Alias       string
	Kind        JobKind
	URL         string
	Paths       []string
	Force       bool
	Priority    JobPriority
	PriorStatus string // repo's status before being set to queued; used to revert on shutdown
	RepoID      int64  // DB row id; set by handlers so worker/shutdown can update without an alias lookup
	seq         uint64 // monotonic; assigned at enqueue under mu; used for position calc
	Done        chan JobResult
}

// indexQueue is a single-worker queue with two priority lanes. All mutating
// operations (enqueue, dequeue, handle) take mu; channel sends and receives
// happen outside the lock. The pending map is the source of truth for which
// aliases are queued; the channels carry the *Job values to the worker.
//
// Concurrency model:
//   - One worker goroutine runs at a time.
//   - mu guards pending, running and nextSeq.
//   - A job may exist in a channel but be absent from pending (an "orphan"
//     left behind after dequeue). The worker checks pointer-equality against
//     pending before running and silently skips orphans.
type indexQueue struct {
	userJobs chan *Job
	bgJobs   chan *Job
	pending  map[string]*Job
	running  string
	nextSeq  uint64
	mu       sync.Mutex
	// store, when non-nil, is used by enqueue to write StatusQueued atomically
	// after a fresh insert. Tests that exercise the queue in isolation pass
	// nil to skip the DB write.
	store *store.Store
}

const queueCapacity = 100

// errQueueFull's text is surfaced verbatim to MCP clients, so it intentionally
// reads as a complete user-facing sentence rather than a Go-style fragment.
//
//nolint:staticcheck // ST1005: user-facing message, capitalisation and punctuation are deliberate
var errQueueFull = errors.New("Indexing queue is full (100 jobs pending). Try again shortly.")

// newIndexQueue constructs an empty queue with both lanes sized to
// queueCapacity. Call worker(ctx, run) on the returned queue to start
// processing. The store is used to write StatusQueued from inside enqueue
// so the worker's StatusIndexing write is guaranteed to apply after; pass
// nil from pure-queue unit tests.
func newIndexQueue(s *store.Store) *indexQueue {
	return &indexQueue{
		userJobs: make(chan *Job, queueCapacity),
		bgJobs:   make(chan *Job, queueCapacity),
		pending:  make(map[string]*Job),
		store:    s,
	}
}

// enqueue inserts job into the queue or coalesces it into an existing
// pending entry for the same alias.
//
// Returns:
//   - done:         the channel the caller should read JobResult from.
//   - position:     1-based position among pending jobs (1 = head of pending).
//   - coalesced:    true when the job merged into an existing pending entry.
//   - pathsChanged: true when the merge expanded the effective Paths set
//     (or when this is a fresh insert). Useful for callers that decide
//     whether to update DB rows.
//   - err:          errQueueFull if the lane channel is at capacity.
//
// Coalesce rules:
//   - Force is a strict upgrade: an arriving Force=true upgrades the pending
//     entry. This ensures an explicit update_docs always re-pulls.
//   - Priority is NOT upgraded: re-routing across channels is non-trivial
//     and bg-then-user is rare. First writer wins.
func (q *indexQueue) enqueue(job *Job) (done chan JobResult, position int, coalesced bool, pathsChanged bool, err error) {
	q.mu.Lock()

	if existing, ok := q.pending[job.Alias]; ok {
		// Coalesce into the pending entry. This covers two sub-cases:
		//   - alias is currently running and a follow-up was already queued;
		//   - alias is queued but not yet running.
		before := len(existing.Paths)
		existing.Paths = indexer.MergePaths(existing.Paths, job.Paths)

		// Force is a strict upgrade so an explicit update_docs always wins
		// over a queued auto-refresh.
		if job.Force {
			existing.Force = true
		}
		// Priority is intentionally NOT upgraded. Re-routing across channels
		// is non-trivial and the bg-then-user case is rare in practice.

		pathsChanged = len(existing.Paths) != before
		pos := q.position(job.Alias)
		q.mu.Unlock()
		// Skipping DB write on coalesce: position is unchanged for this
		// alias's slot. If a future change causes coalesce to shift
		// position, status_detail goes stale here.
		return existing.Done, pos, true, pathsChanged, nil
	}

	// Fresh insert.
	job.seq = q.nextSeq
	q.nextSeq++
	if job.Done == nil {
		job.Done = make(chan JobResult, 1)
	}
	q.pending[job.Alias] = job

	ch := q.bgJobs
	if job.Priority == priorityUser {
		ch = q.userJobs
	}

	// Non-blocking send. If the lane is full, roll back the pending insert
	// so the alias can be re-enqueued later.
	select {
	case ch <- job:
		pos := q.position(job.Alias)
		// Perform the StatusQueued DB write while still holding q.mu. The
		// worker's handle() acquires q.mu before its first action (the
		// StatusIndexing write), so writing under the lock here guarantees
		// the queued status lands first and eliminates the
		// indexing -> queued -> indexing flicker. This briefly impacts
		// worker liveness (it must wait for q.mu and this DB write) but
		// correctness wins over the small stall.
		if q.store != nil && job.RepoID != 0 {
			if dbErr := q.store.UpdateRepoStatus(job.RepoID, store.StatusQueued, formatQueuedDetail(pos)); dbErr != nil {
				log.Printf("queue: %s set queued status: %v", job.Alias, dbErr)
			}
		}
		q.mu.Unlock()
		return job.Done, pos, false, true, nil
	default:
		delete(q.pending, job.Alias)
		q.mu.Unlock()
		return nil, 0, false, false, errQueueFull
	}
}

// position returns the 1-based position of alias among pending jobs ordered
// by enqueue sequence. Returns 0 if the alias is not pending. Caller must
// hold q.mu.
func (q *indexQueue) position(alias string) int {
	target, ok := q.pending[alias]
	if !ok {
		return 0
	}
	count := 0
	for _, j := range q.pending {
		if j.seq < target.seq {
			count++
		}
	}
	return count + 1
}

// dequeue removes a pending job for alias. Returns true if the job was
// pending and not currently running. The orphan *Job remains in its lane
// channel; the worker compensates via the pointer-equality check in handle.
func (q *indexQueue) dequeue(alias string) bool {
	q.mu.Lock()
	defer q.mu.Unlock()
	if alias == q.running {
		return false
	}
	if _, ok := q.pending[alias]; !ok {
		return false
	}
	delete(q.pending, alias)
	return true
}

// worker is the single consumer of the queue. It must be started in its own
// goroutine. The user lane is preferred: if a user job is immediately
// available it is taken; otherwise the worker blocks on either lane (or
// shutdown).
//
// run is invoked synchronously per job. The worker passes its own ctx so
// shutdown can interrupt the in-flight indexing call. Panics inside run are
// recovered and reported as JobResult.Err. The worker exits cleanly when ctx
// is cancelled. On shutdown, the caller should invoke drainPending to
// release any jobs still sitting in the channels or pending map.
func (q *indexQueue) worker(ctx context.Context, run func(context.Context, *Job) JobResult) {
	for {
		// Cheap shutdown check first so a steady stream of jobs cannot
		// starve cancellation.
		select {
		case <-ctx.Done():
			return
		default:
		}

		// Priority lane: prefer user jobs when one is immediately
		// available. Otherwise block on either lane or shutdown.
		select {
		case j := <-q.userJobs:
			q.handle(ctx, j, run)
		default:
			select {
			case j := <-q.userJobs:
				q.handle(ctx, j, run)
			case j := <-q.bgJobs:
				q.handle(ctx, j, run)
			case <-ctx.Done():
				return
			}
		}
	}
}

// handle runs one job. It must be called only from worker (the queue assumes
// a single consumer). The pointer-equality check protects against orphans
// (entries that were dequeued or replaced via coalescing while sitting in
// the channel). ctx is forwarded to run so the indexer call can be cancelled
// mid-flight on shutdown.
func (q *indexQueue) handle(ctx context.Context, j *Job, run func(context.Context, *Job) JobResult) {
	q.mu.Lock()
	if q.pending[j.Alias] != j {
		// Orphan: dequeued or replaced. Skip silently.
		q.mu.Unlock()
		return
	}
	q.running = j.Alias
	delete(q.pending, j.Alias)
	q.mu.Unlock()

	var result JobResult
	func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("queue: panic in job %s: %v\n%s", j.Alias, r, debug.Stack())
				result = JobResult{Err: fmt.Errorf("panic: %v", r)}
			}
		}()
		result = run(ctx, j)
	}()

	// Done is buffered size 1, send never blocks.
	j.Done <- result
	close(j.Done)

	q.mu.Lock()
	q.running = ""
	q.mu.Unlock()
}

// drainPending must be called once after the worker has exited (ctx
// cancelled and the worker goroutine has returned). It snapshots the
// pending map, drains both lane channels non-blockingly, and delivers
// context.Canceled on every snapshotted job's Done channel. Returns the
// snapshot so callers can revert per-job DB state (PriorStatus).
func (q *indexQueue) drainPending() []*Job {
	q.mu.Lock()
	jobs := make([]*Job, 0, len(q.pending))
	for _, j := range q.pending {
		jobs = append(jobs, j)
	}
	q.pending = make(map[string]*Job)
	q.mu.Unlock()

	drain(q.userJobs)
	drain(q.bgJobs)

	for _, j := range jobs {
		j.Done <- JobResult{Err: context.Canceled}
		close(j.Done)
	}
	return jobs
}

// drain empties a buffered channel without blocking.
func drain(ch chan *Job) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}
