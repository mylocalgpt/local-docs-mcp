package mcpserver

import (
	"context"
	"errors"
	"sort"
	"sync"
	"testing"
	"time"
)

// recorder collects the order in which jobs are processed by the worker.
type recorder struct {
	mu    sync.Mutex
	calls []string
}

func (r *recorder) add(alias string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls = append(r.calls, alias)
}

func (r *recorder) snapshot() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.calls))
	copy(out, r.calls)
	return out
}

// recvWithin reads one JobResult or fails the test on timeout.
func recvWithin(t *testing.T, ch <-chan JobResult, d time.Duration) JobResult {
	t.Helper()
	select {
	case r := <-ch:
		return r
	case <-time.After(d):
		t.Fatalf("timed out waiting for JobResult after %s", d)
		return JobResult{}
	}
}

// startWorker starts a worker goroutine and returns a stop func that
// cancels its context and waits for it to exit. The supplied run uses the
// legacy single-arg shape; this helper adapts it to the ctx-aware signature
// the queue worker requires.
func startWorker(t *testing.T, q *indexQueue, run func(*Job) JobResult) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		q.worker(ctx, func(_ context.Context, j *Job) JobResult { return run(j) })
		close(done)
	}()
	return func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("worker did not exit after cancel")
		}
	}
}

func TestQueueSingleJobCompletes(t *testing.T) {
	q := newIndexQueue(nil)
	rec := &recorder{}
	run := func(j *Job) JobResult {
		rec.add(j.Alias)
		return JobResult{}
	}
	stop := startWorker(t, q, run)
	defer stop()

	done, _, coalesced, _, err := q.enqueue(&Job{Alias: "a", Priority: priorityUser})
	if err != nil {
		t.Fatalf("enqueue: %v", err)
	}
	if coalesced {
		t.Fatalf("expected fresh insert, got coalesced")
	}
	res := recvWithin(t, done, time.Second)
	if res.Err != nil {
		t.Fatalf("unexpected err: %v", res.Err)
	}
	if got := rec.snapshot(); len(got) != 1 || got[0] != "a" {
		t.Fatalf("calls = %v, want [a]", got)
	}
}

func TestQueueTwoDistinctAliasesFIFO(t *testing.T) {
	q := newIndexQueue(nil)
	rec := &recorder{}
	// Block first job until released so we can ensure b is queued behind a.
	gate := make(chan struct{})
	run := func(j *Job) JobResult {
		if j.Alias == "a" {
			<-gate
		}
		rec.add(j.Alias)
		return JobResult{}
	}
	stop := startWorker(t, q, run)
	defer stop()

	doneA, _, _, _, err := q.enqueue(&Job{Alias: "a", Priority: priorityUser})
	if err != nil {
		t.Fatal(err)
	}
	doneB, _, _, _, err := q.enqueue(&Job{Alias: "b", Priority: priorityUser})
	if err != nil {
		t.Fatal(err)
	}
	close(gate)
	recvWithin(t, doneA, time.Second)
	recvWithin(t, doneB, time.Second)

	got := rec.snapshot()
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("calls = %v, want [a b]", got)
	}
}

func TestQueueCoalesceWhilePending(t *testing.T) {
	q := newIndexQueue(nil)
	rec := &recorder{}
	gate := make(chan struct{})
	var capturedPaths []string
	run := func(j *Job) JobResult {
		if j.Alias == "blocker" {
			<-gate
		}
		if j.Alias == "x" {
			capturedPaths = append([]string(nil), j.Paths...)
		}
		rec.add(j.Alias)
		return JobResult{}
	}
	stop := startWorker(t, q, run)
	defer stop()

	// Hold the worker on a different alias so x stays queued.
	doneBlocker, _, _, _, err := q.enqueue(&Job{Alias: "blocker", Priority: priorityUser})
	if err != nil {
		t.Fatal(err)
	}
	// Wait briefly for the worker to pick up "blocker".
	time.Sleep(50 * time.Millisecond)

	doneX1, _, c1, _, err := q.enqueue(&Job{Alias: "x", Paths: []string{"docs/"}, Priority: priorityUser})
	if err != nil {
		t.Fatal(err)
	}
	if c1 {
		t.Fatalf("first enqueue of x should not coalesce")
	}
	doneX2, _, c2, pathsChanged, err := q.enqueue(&Job{Alias: "x", Paths: []string{"api/"}, Priority: priorityUser})
	if err != nil {
		t.Fatal(err)
	}
	if !c2 {
		t.Fatalf("second enqueue of x should coalesce")
	}
	if !pathsChanged {
		t.Fatalf("expected pathsChanged=true after merging api/")
	}
	if doneX1 != doneX2 {
		t.Fatalf("coalesced enqueue should return same Done channel")
	}

	close(gate)
	recvWithin(t, doneBlocker, time.Second)
	recvWithin(t, doneX1, time.Second)

	got := rec.snapshot()
	// blocker, then x exactly once.
	if len(got) != 2 || got[0] != "blocker" || got[1] != "x" {
		t.Fatalf("calls = %v, want [blocker x]", got)
	}
	sort.Strings(capturedPaths)
	if len(capturedPaths) != 2 || capturedPaths[0] != "api/" || capturedPaths[1] != "docs/" {
		t.Fatalf("captured paths = %v, want [api/ docs/]", capturedPaths)
	}
}

func TestQueueCoalesceWhileRunning(t *testing.T) {
	q := newIndexQueue(nil)
	rec := &recorder{}
	first := make(chan struct{})
	release := make(chan struct{})
	run := func(j *Job) JobResult {
		if j.Alias == "x" && len(rec.snapshot()) == 0 {
			// First invocation of x: signal we're inside, then block.
			close(first)
			<-release
		}
		rec.add(j.Alias)
		return JobResult{}
	}
	stop := startWorker(t, q, run)
	defer stop()

	doneFirst, _, _, _, err := q.enqueue(&Job{Alias: "x", Paths: []string{"a/"}, Priority: priorityUser})
	if err != nil {
		t.Fatal(err)
	}
	// Wait until worker is inside run.
	select {
	case <-first:
	case <-time.After(time.Second):
		t.Fatalf("worker did not enter run")
	}

	// Now x is "running"; new enqueues should create a follow-up.
	doneFollow1, _, c1, _, err := q.enqueue(&Job{Alias: "x", Paths: []string{"b/"}, Priority: priorityUser})
	if err != nil {
		t.Fatal(err)
	}
	if c1 {
		t.Fatalf("first follow-up should be a fresh insert (not coalesced)")
	}
	if doneFollow1 == doneFirst {
		t.Fatalf("follow-up Done must differ from in-flight Done")
	}
	// Further enqueues should coalesce into the follow-up.
	doneFollow2, _, c2, _, err := q.enqueue(&Job{Alias: "x", Paths: []string{"c/"}, Priority: priorityUser})
	if err != nil {
		t.Fatal(err)
	}
	if !c2 {
		t.Fatalf("second follow-up should coalesce into the queued follow-up")
	}
	if doneFollow1 != doneFollow2 {
		t.Fatalf("coalesced follow-up should share Done channel")
	}

	close(release)
	recvWithin(t, doneFirst, time.Second)
	recvWithin(t, doneFollow1, time.Second)

	got := rec.snapshot()
	if len(got) != 2 || got[0] != "x" || got[1] != "x" {
		t.Fatalf("calls = %v, want [x x]", got)
	}
}

func TestQueueForceUpgradeBothDirections(t *testing.T) {
	for _, tc := range []struct {
		name        string
		first, snd  bool
		wantOnQueue bool
	}{
		{"falseThenTrue", false, true, true},
		{"trueThenFalse", true, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			q := newIndexQueue(nil)
			gate := make(chan struct{})
			var observedForce bool
			run := func(j *Job) JobResult {
				if j.Alias == "blocker" {
					<-gate
				}
				if j.Alias == "x" {
					observedForce = j.Force
				}
				return JobResult{}
			}
			stop := startWorker(t, q, run)
			defer stop()

			doneBlocker, _, _, _, err := q.enqueue(&Job{Alias: "blocker", Priority: priorityUser})
			if err != nil {
				t.Fatal(err)
			}
			time.Sleep(30 * time.Millisecond)

			doneX, _, _, _, err := q.enqueue(&Job{Alias: "x", Force: tc.first, Priority: priorityUser})
			if err != nil {
				t.Fatal(err)
			}
			_, _, c, _, err := q.enqueue(&Job{Alias: "x", Force: tc.snd, Priority: priorityUser})
			if err != nil {
				t.Fatal(err)
			}
			if !c {
				t.Fatalf("second enqueue should coalesce")
			}

			close(gate)
			recvWithin(t, doneBlocker, time.Second)
			recvWithin(t, doneX, time.Second)
			if observedForce != tc.wantOnQueue {
				t.Fatalf("Force on coalesced job = %v, want %v", observedForce, tc.wantOnQueue)
			}
		})
	}
}

func TestQueueUserPriorityBeatsBackground(t *testing.T) {
	q := newIndexQueue(nil)
	rec := &recorder{}
	gate := make(chan struct{})
	run := func(j *Job) JobResult {
		// Hold the very first job so the rest pile up.
		if len(rec.snapshot()) == 0 {
			<-gate
		}
		rec.add(j.Alias)
		return JobResult{}
	}
	stop := startWorker(t, q, run)
	defer stop()

	bgDone := make([]chan JobResult, 0, 5)
	for i, alias := range []string{"bg1", "bg2", "bg3", "bg4", "bg5"} {
		d, _, _, _, err := q.enqueue(&Job{Alias: alias, Priority: priorityBackground})
		if err != nil {
			t.Fatalf("bg enqueue %d: %v", i, err)
		}
		bgDone = append(bgDone, d)
	}
	// Wait briefly for the worker to pick up bg1 (the blocker).
	time.Sleep(50 * time.Millisecond)

	userDone, _, _, _, err := q.enqueue(&Job{Alias: "user1", Priority: priorityUser})
	if err != nil {
		t.Fatal(err)
	}

	close(gate)

	// Drain everything.
	for _, d := range bgDone {
		recvWithin(t, d, 2*time.Second)
	}
	recvWithin(t, userDone, 2*time.Second)

	got := rec.snapshot()
	if len(got) != 6 {
		t.Fatalf("expected 6 calls, got %v", got)
	}
	if got[0] != "bg1" {
		t.Fatalf("first call should be bg1 (already in flight), got %s", got[0])
	}
	if got[1] != "user1" {
		t.Fatalf("second call should be user1 (priority), got %s; full=%v", got[1], got)
	}
}

func TestQueueCapacityFull(t *testing.T) {
	q := newIndexQueue(nil)
	gate := make(chan struct{})
	run := func(j *Job) JobResult {
		<-gate
		return JobResult{}
	}
	stop := startWorker(t, q, run)
	defer stop()

	// Fill the user lane: the worker takes one immediately and blocks on
	// gate, so the channel still has room for queueCapacity more.
	dones := make([]chan JobResult, 0, queueCapacity+1)
	for i := 0; i < queueCapacity+1; i++ {
		alias := "a" + itoa(i)
		d, _, _, _, err := q.enqueue(&Job{Alias: alias, Priority: priorityUser})
		if err != nil {
			t.Fatalf("enqueue %d: %v", i, err)
		}
		dones = append(dones, d)
	}
	// Now the channel is full (worker holds 1, channel buffers queueCapacity).
	_, _, _, _, err := q.enqueue(&Job{Alias: "overflow", Priority: priorityUser})
	if !errors.Is(err, errQueueFull) {
		t.Fatalf("expected errQueueFull, got %v", err)
	}

	close(gate)
	for _, d := range dones {
		recvWithin(t, d, 5*time.Second)
	}

	// Queue still works after draining.
	d, _, _, _, err := q.enqueue(&Job{Alias: "after", Priority: priorityUser})
	if err != nil {
		t.Fatalf("post-drain enqueue: %v", err)
	}
	recvWithin(t, d, time.Second)
}

func TestQueuePanicRecovery(t *testing.T) {
	q := newIndexQueue(nil)
	rec := &recorder{}
	run := func(j *Job) JobResult {
		if j.Alias == "boom" {
			panic("kapow")
		}
		rec.add(j.Alias)
		return JobResult{}
	}
	stop := startWorker(t, q, run)
	defer stop()

	doneBoom, _, _, _, err := q.enqueue(&Job{Alias: "boom", Priority: priorityUser})
	if err != nil {
		t.Fatal(err)
	}
	doneOk, _, _, _, err := q.enqueue(&Job{Alias: "ok", Priority: priorityUser})
	if err != nil {
		t.Fatal(err)
	}
	res := recvWithin(t, doneBoom, time.Second)
	if res.Err == nil {
		t.Fatalf("expected panic error, got nil")
	}
	resOk := recvWithin(t, doneOk, time.Second)
	if resOk.Err != nil {
		t.Fatalf("ok job failed: %v", resOk.Err)
	}
	if got := rec.snapshot(); len(got) != 1 || got[0] != "ok" {
		t.Fatalf("calls = %v, want [ok]", got)
	}
}

func TestQueueCtxCancelDrain(t *testing.T) {
	q := newIndexQueue(nil)
	gate := make(chan struct{})
	run := func(j *Job) JobResult {
		if j.Alias == "blocker" {
			<-gate
		}
		return JobResult{}
	}

	ctx, cancel := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		q.worker(ctx, func(_ context.Context, j *Job) JobResult { return run(j) })
		close(workerDone)
	}()

	doneBlocker, _, _, _, err := q.enqueue(&Job{Alias: "blocker", Priority: priorityUser})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)

	donePending1, _, _, _, err := q.enqueue(&Job{Alias: "p1", Priority: priorityUser})
	if err != nil {
		t.Fatal(err)
	}
	donePending2, _, _, _, err := q.enqueue(&Job{Alias: "p2", Priority: priorityBackground})
	if err != nil {
		t.Fatal(err)
	}

	cancel()
	// Worker won't exit until blocker returns, so release it.
	close(gate)
	recvWithin(t, doneBlocker, time.Second)

	select {
	case <-workerDone:
	case <-time.After(2 * time.Second):
		t.Fatalf("worker did not exit after cancel")
	}

	jobs := q.drainPending()
	if len(jobs) != 2 {
		t.Fatalf("drainPending returned %d jobs, want 2", len(jobs))
	}
	for _, d := range []chan JobResult{donePending1, donePending2} {
		res := recvWithin(t, d, time.Second)
		if !errors.Is(res.Err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", res.Err)
		}
	}
}

func TestQueueDequeueWhilePending(t *testing.T) {
	q := newIndexQueue(nil)
	rec := &recorder{}
	gate := make(chan struct{})
	run := func(j *Job) JobResult {
		if j.Alias == "blocker" {
			<-gate
		}
		rec.add(j.Alias)
		return JobResult{}
	}
	stop := startWorker(t, q, run)
	defer stop()

	doneBlocker, _, _, _, err := q.enqueue(&Job{Alias: "blocker", Priority: priorityUser})
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(30 * time.Millisecond)

	_, _, _, _, err = q.enqueue(&Job{Alias: "victim", Priority: priorityUser})
	if err != nil {
		t.Fatal(err)
	}

	if !q.dequeue("victim") {
		t.Fatalf("dequeue(victim) = false, want true")
	}

	close(gate)
	recvWithin(t, doneBlocker, time.Second)

	// Give the worker time to consume the orphan and skip it.
	time.Sleep(100 * time.Millisecond)
	got := rec.snapshot()
	for _, name := range got {
		if name == "victim" {
			t.Fatalf("victim should not have run, calls=%v", got)
		}
	}
}

func TestQueueDequeueWhileRunning(t *testing.T) {
	q := newIndexQueue(nil)
	rec := &recorder{}
	inside := make(chan struct{})
	release := make(chan struct{})
	run := func(j *Job) JobResult {
		close(inside)
		<-release
		rec.add(j.Alias)
		return JobResult{}
	}
	stop := startWorker(t, q, run)
	defer stop()

	done, _, _, _, err := q.enqueue(&Job{Alias: "live", Priority: priorityUser})
	if err != nil {
		t.Fatal(err)
	}
	<-inside

	if q.dequeue("live") {
		t.Fatalf("dequeue while running should return false")
	}

	close(release)
	res := recvWithin(t, done, time.Second)
	if res.Err != nil {
		t.Fatalf("unexpected err: %v", res.Err)
	}
	if got := rec.snapshot(); len(got) != 1 || got[0] != "live" {
		t.Fatalf("calls = %v, want [live]", got)
	}
}

// TestQueueUserJobJumpsAheadOfBackground verifies that an `add_docs` user
// job enqueued after a background auto-refresh job for a different alias
// runs first, even though the background job was enqueued earlier.
//
// Determinism note: the worker's priority gate
// (queue.go ~lines 214-217) is:
//
//	select {
//	case j := <-q.userJobs:  // tried first, non-blocking
//	    q.handle(j, run)
//	default:
//	    select { /* user | bg | ctx.Done */ }
//	}
//
// We pre-populate BOTH lanes before the worker starts. On its first
// iteration the outer non-blocking select finds userJobs ready and runs
// the user job. There is exactly one non-default case and it is satisfied,
// so ordering is deterministic. Do NOT "simplify" by collapsing the two
// selects: doing so would let the runtime pick either lane and break this
// guarantee.
func TestQueueUserJobJumpsAheadOfBackground(t *testing.T) {
	cs, srv, cleanup := setupAddDocsTest(t)
	defer cleanup()
	_ = cs // unused; we drive the queue directly

	bi := NewBlockingIndexer()
	srv.indexer = bi

	// Prime BOTH blocks BEFORE enqueue so neither completes until released.
	releaseBg := bi.Block("bg")
	releaseUser := bi.Block("user")

	bgJob := &Job{Alias: "bg", Kind: jobKindGit, URL: "https://example.com/bg", Priority: priorityBackground}
	userJob := &Job{Alias: "user", Kind: jobKindGit, URL: "https://example.com/user", Priority: priorityUser}

	if _, _, _, _, err := srv.queue.enqueue(bgJob); err != nil {
		t.Fatalf("enqueue bg: %v", err)
	}
	if _, _, _, _, err := srv.queue.enqueue(userJob); err != nil {
		t.Fatalf("enqueue user: %v", err)
	}

	// Start worker AFTER both enqueues + both blocks.
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

	// Wait until the worker reports its first call.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(bi.Calls()) >= 1 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	calls := bi.Calls()
	if len(calls) == 0 {
		t.Fatal("worker did not pick up any job within 2s")
	}
	if calls[0].Alias != "user" {
		t.Fatalf("first call alias = %q, want %q (priority gate broken)", calls[0].Alias, "user")
	}

	// Release both, let the worker drain.
	close(releaseUser)
	close(releaseBg)

	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if len(bi.Calls()) >= 2 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	calls = bi.Calls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls total, got %d: %+v", len(calls), calls)
	}
	if calls[1].Alias != "bg" {
		t.Errorf("second call alias = %q, want %q", calls[1].Alias, "bg")
	}
}

// itoa is a tiny no-import-strconv helper used by the capacity test where
// we just need stable distinct strings.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := false
	if n < 0 {
		neg = true
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
