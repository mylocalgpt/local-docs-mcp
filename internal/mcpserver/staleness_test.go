package mcpserver

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mylocalgpt/local-docs-mcp/internal/config"
	"github.com/mylocalgpt/local-docs-mcp/internal/indexer"
	"github.com/mylocalgpt/local-docs-mcp/internal/search"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

// newTestServer builds a Server backed by an in-memory store and a
// BlockingIndexer fake. The returned cleanup closes the store.
func newTestServer(t *testing.T) (*Server, *store.Store, func()) {
	t.Helper()
	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	srv := New(s, search.NewSearch(s), nil, nil)
	srv.indexer = NewBlockingIndexer()
	return srv, s, func() { _ = s.Close() }
}

// seedDoc inserts a single document row through the public API. content may
// contain arbitrary bytes including BOMs or invalid UTF-8.
func seedDoc(t *testing.T, s *store.Store, repoID int64, path, content string) {
	t.Helper()
	if err := s.ReplaceDocuments(repoID, []store.Document{{
		RepoID:       repoID,
		Path:         path,
		DocTitle:     "doc",
		SectionTitle: "section",
		Content:      content,
		Tokens:       1,
		HeadingLevel: 1,
	}}); err != nil {
		t.Fatalf("ReplaceDocuments: %v", err)
	}
}

func TestStaleness(t *testing.T) {
	srv, s, cleanup := newTestServer(t)
	defer cleanup()

	// Seed three repos so the encoding-scan branch has rows to inspect.
	cleanID, err := s.UpsertRepo("clean", "https://example.com/clean", `["docs"]`, "git")
	if err != nil {
		t.Fatalf("upsert clean: %v", err)
	}
	seedDoc(t, s, cleanID, "a.md", "hello world")

	brokenID, err := s.UpsertRepo("broken", "https://example.com/broken", `["docs"]`, "git")
	if err != nil {
		t.Fatalf("upsert broken: %v", err)
	}
	seedDoc(t, s, brokenID, "a.md", "\xEF\xBB\xBFhello") // UTF-8 BOM prefix

	now := time.Now().UTC()
	fresh := now.Add(-time.Minute).Format(time.RFC3339)
	old := now.Add(-25 * time.Hour).Format(time.RFC3339)

	cases := []struct {
		name       string
		repo       store.Repo
		wantStale  bool
		wantReason string
		wantForce  bool
	}{
		{
			name:       "local source",
			repo:       store.Repo{ID: cleanID, SourceType: "local", IndexedAt: fresh},
			wantStale:  true,
			wantReason: "local source",
		},
		{
			name:       "never indexed",
			repo:       store.Repo{ID: cleanID, SourceType: "git", IndexedAt: ""},
			wantStale:  true,
			wantReason: "never indexed",
		},
		{
			name:       "unknown age",
			repo:       store.Repo{ID: cleanID, SourceType: "git", IndexedAt: "not-a-timestamp"},
			wantStale:  true,
			wantReason: "unknown age",
		},
		{
			name:       "older than 24h",
			repo:       store.Repo{ID: cleanID, SourceType: "git", IndexedAt: old},
			wantStale:  true,
			wantReason: fmt.Sprintf("last indexed %s", old),
		},
		{
			name:       "fresh and clean",
			repo:       store.Repo{ID: cleanID, SourceType: "git", IndexedAt: fresh},
			wantStale:  false,
			wantReason: "",
		},
		{
			name:       "fresh but encoding broken",
			repo:       store.Repo{ID: brokenID, SourceType: "git", IndexedAt: fresh},
			wantStale:  true,
			wantReason: "indexed content contains invalid encoding",
			wantForce:  true,
		},
		{
			name:       "fresh encoding error repo",
			repo:       store.Repo{ID: brokenID, SourceType: "git", Status: store.StatusError, IndexedAt: fresh},
			wantStale:  false,
			wantReason: "",
			wantForce:  false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotStale, gotReason, gotForce := srv.staleness(context.Background(), tc.repo)
			if gotStale != tc.wantStale {
				t.Errorf("stale = %v, want %v", gotStale, tc.wantStale)
			}
			if gotReason != tc.wantReason {
				t.Errorf("reason = %q, want %q", gotReason, tc.wantReason)
			}
			if gotForce != tc.wantForce {
				t.Errorf("force = %v, want %v", gotForce, tc.wantForce)
			}
		})
	}
}

func TestAutoRefreshDoesNotQueueCleanUnicodeRepo(t *testing.T) {
	srv, s, cleanup := newTestServer(t)
	defer cleanup()

	const alias = "clean-unicode"
	repoID, err := s.UpsertRepo(alias, "https://example.com/clean", `["docs"]`, "git")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	fresh := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if err := s.UpdateRepoIndex(repoID, "deadbeef", fresh, 1); err != nil {
		t.Fatalf("UpdateRepoIndex: %v", err)
	}
	if err := s.UpdateRepoStatus(repoID, store.StatusReady, ""); err != nil {
		t.Fatalf("UpdateRepoStatus: %v", err)
	}
	seedDoc(t, s, repoID, "unicode.md", "cafe CJK 漢字 emoji 😀 smart quotes “ok”")

	srv.autoRefresh(context.Background())

	repo, err := s.GetRepo(alias)
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if repo == nil || repo.Status != store.StatusReady {
		t.Fatalf("expected ready repo after autoRefresh, got %+v", repo)
	}
	calls := srv.indexer.(*BlockingIndexer).CallsFor(alias)
	if len(calls) != 0 {
		t.Fatalf("indexer was called for clean Unicode repo: %+v", calls)
	}
}

// TestAutoRefreshEncodingHeal verifies the new encoding branch threads the
// queue and log line correctly when a fresh, ready repo holds a corrupt
// document row.
func TestAutoRefreshEncodingHeal(t *testing.T) {
	srv, s, cleanup := newTestServer(t)
	defer cleanup()

	const alias = "broken"
	repoID, err := s.UpsertRepo(alias, "https://example.com/broken", `["docs"]`, "git")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	// Mark repo as ready and recently indexed (within the 24h window).
	fresh := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if err := s.UpdateRepoIndex(repoID, "deadbeef", fresh, 1); err != nil {
		t.Fatalf("UpdateRepoIndex: %v", err)
	}
	if err := s.UpdateRepoStatus(repoID, store.StatusReady, ""); err != nil {
		t.Fatalf("UpdateRepoStatus: %v", err)
	}
	// Plant a row whose content begins with a UTF-8 BOM so
	// RepoHasInvalidEncoding flags it.
	seedDoc(t, s, repoID, "a.md", "\xEF\xBB\xBFhello")

	// Capture log output for the assertion.
	var buf bytes.Buffer
	prev := log.Writer()
	log.SetOutput(&buf)
	t.Cleanup(func() { log.SetOutput(prev) })

	srv.autoRefresh(context.Background())

	// enqueue writes StatusQueued synchronously; no worker is running so the
	// repo should still be queued (or further along, defensively).
	repo, err := s.GetRepo(alias)
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if repo == nil {
		t.Fatal("repo missing after autoRefresh")
	}
	if repo.Status == store.StatusReady {
		t.Errorf("repo still in %q after autoRefresh; expected to leave ready", store.StatusReady)
	}

	if !strings.Contains(buf.String(), "indexed content contains invalid encoding") {
		t.Errorf("log output missing encoding reason; got: %s", buf.String())
	}
}

// healingIndexer is a fake indexer that simulates a real reindex when called
// with Force=true: it overwrites the repo's documents with clean content.
// This lets us verify that autoRefresh propagates Force into the job so the
// real indexer's SHA short-circuit (internal/indexer/indexer.go:94) is bypassed
// during encoding-heal runs.
type healingIndexer struct {
	store     *store.Store
	mu        sync.Mutex
	gotForce  []bool
	cleanText string
}

func (h *healingIndexer) IndexRepo(ctx context.Context, cfg config.RepoConfig, force bool) (*indexer.IndexResult, error) {
	h.mu.Lock()
	h.gotForce = append(h.gotForce, force)
	h.mu.Unlock()

	repo, err := h.store.GetRepo(cfg.Alias)
	if err != nil {
		return nil, err
	}
	if repo == nil {
		return &indexer.IndexResult{Repo: cfg.Alias}, nil
	}

	// Mirror indexer.IndexRepo's SHA short-circuit: when Force is false and
	// the SHA is unchanged, the indexer returns Skipped without touching
	// documents. The whole point of this test is that autoRefresh must NOT
	// hit this branch for encoding heals.
	if !force {
		return &indexer.IndexResult{Repo: cfg.Alias, Skipped: true}, nil
	}

	if err := h.store.ReplaceDocuments(repo.ID, []store.Document{{
		RepoID:       repo.ID,
		Path:         "a.md",
		DocTitle:     "doc",
		SectionTitle: "section",
		Content:      h.cleanText,
		Tokens:       1,
		HeadingLevel: 1,
	}}); err != nil {
		return nil, err
	}
	return &indexer.IndexResult{Repo: cfg.Alias, DocsIndexed: 1}, nil
}

func (h *healingIndexer) IndexLocalPath(ctx context.Context, alias, path string) (*indexer.IndexResult, error) {
	return &indexer.IndexResult{Repo: alias, DocsIndexed: 1}, nil
}

// TestAutoRefreshEncodingHealReplacesCorruptRowsAndStops is the integration guard for
// the F1 fix. It seeds a git repo whose commit SHA is unchanged but whose
// indexed rows contain invalid encoding, then drives autoRefresh + the queue
// worker to completion. Without Force=true on the heal job, the real indexer
// would short-circuit on the SHA match and the corrupt row would persist.
func TestAutoRefreshEncodingHealReplacesCorruptRowsAndStops(t *testing.T) {
	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	srv := New(s, search.NewSearch(s), nil, nil)
	ix := &healingIndexer{store: s, cleanText: "clean cafe 漢字 😀"}
	srv.indexer = ix

	const alias = "broken"
	repoID, err := s.UpsertRepo(alias, "https://example.com/broken", `["docs"]`, "git")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	fresh := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if err := s.UpdateRepoIndex(repoID, "deadbeef", fresh, 1); err != nil {
		t.Fatalf("UpdateRepoIndex: %v", err)
	}
	if err := s.UpdateRepoStatus(repoID, store.StatusReady, ""); err != nil {
		t.Fatalf("UpdateRepoStatus: %v", err)
	}
	// Plant a row with a UTF-8 BOM so RepoHasInvalidEncoding flags it.
	seedDoc(t, s, repoID, "a.md", "\xEF\xBB\xBFhello")

	// Confirm the precondition the heal flow is supposed to fix.
	invalid, err := s.RepoHasInvalidEncoding(context.Background(), repoID)
	if err != nil {
		t.Fatalf("RepoHasInvalidEncoding pre: %v", err)
	}
	if !invalid {
		t.Fatal("expected seeded row to be flagged as invalid encoding")
	}

	// Run the worker so the enqueued heal job actually executes.
	workerCtx, workerCancel := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		srv.queue.worker(workerCtx, srv.runJob)
		close(workerDone)
	}()
	t.Cleanup(func() {
		workerCancel()
		<-workerDone
	})

	srv.autoRefresh(context.Background())

	// Wait for the repo to settle back to ready (heal completed).
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		repo, err := s.GetRepo(alias)
		if err != nil {
			t.Fatalf("GetRepo: %v", err)
		}
		if repo != nil && repo.Status == store.StatusReady {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	repo, err := s.GetRepo(alias)
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if repo == nil || repo.Status != store.StatusReady {
		t.Fatalf("expected ready repo after heal, got %+v", repo)
	}

	// The fix: heal must invoke the indexer with Force=true so the SHA
	// short-circuit is bypassed. Without F1's fix, gotForce would be [false].
	ix.mu.Lock()
	calls := append([]bool(nil), ix.gotForce...)
	ix.mu.Unlock()
	if len(calls) == 0 {
		t.Fatal("indexer was never invoked")
	}
	if !calls[0] {
		t.Errorf("heal job ran with Force=false; corrupt rows would persist (got %v)", calls)
	}

	// And the corrupt row must actually be gone.
	stillInvalid, err := s.RepoHasInvalidEncoding(context.Background(), repoID)
	if err != nil {
		t.Fatalf("RepoHasInvalidEncoding post: %v", err)
	}
	if stillInvalid {
		t.Error("invalid encoding still present after heal; F1 regression")
	}

	srv.autoRefresh(context.Background())
	time.Sleep(50 * time.Millisecond)

	ix.mu.Lock()
	callCount := len(ix.gotForce)
	ix.mu.Unlock()
	if callCount != 1 {
		t.Fatalf("autoRefresh queued another heal after convergence; calls = %d", callCount)
	}
}

func TestAutoRefreshStopsRetryingAfterFailedHeal(t *testing.T) {
	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	srv := New(s, search.NewSearch(s), nil, nil)
	ix := &healingIndexer{store: s, cleanText: "\xEF\xBB\xBFstill broken"}
	srv.indexer = ix

	const alias = "failed-heal"
	repoID, err := s.UpsertRepo(alias, "https://example.com/failed", `["docs"]`, "git")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	fresh := time.Now().UTC().Add(-time.Minute).Format(time.RFC3339)
	if err := s.UpdateRepoIndex(repoID, "deadbeef", fresh, 1); err != nil {
		t.Fatalf("UpdateRepoIndex: %v", err)
	}
	if err := s.UpdateRepoStatus(repoID, store.StatusReady, ""); err != nil {
		t.Fatalf("UpdateRepoStatus: %v", err)
	}
	seedDoc(t, s, repoID, "a.md", "\xEF\xBB\xBFhello")

	workerCtx, workerCancel := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		srv.queue.worker(workerCtx, srv.runJob)
		close(workerDone)
	}()
	t.Cleanup(func() {
		workerCancel()
		<-workerDone
	})

	srv.autoRefresh(context.Background())

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		repo, err := s.GetRepo(alias)
		if err != nil {
			t.Fatalf("GetRepo: %v", err)
		}
		if repo != nil && repo.Status == store.StatusError {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	repo, err := s.GetRepo(alias)
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if repo == nil || repo.Status != store.StatusError {
		t.Fatalf("expected error repo after failed heal, got %+v", repo)
	}
	if !strings.Contains(repo.StatusDetail, "auto-heal did not converge") {
		t.Fatalf("status_detail missing failed convergence message: %q", repo.StatusDetail)
	}

	ix.mu.Lock()
	firstCallCount := len(ix.gotForce)
	ix.mu.Unlock()
	if firstCallCount != 1 {
		t.Fatalf("expected one heal attempt, got %d", firstCallCount)
	}

	srv.autoRefresh(context.Background())
	time.Sleep(50 * time.Millisecond)

	ix.mu.Lock()
	secondCallCount := len(ix.gotForce)
	ix.mu.Unlock()
	if secondCallCount != firstCallCount {
		t.Fatalf("autoRefresh retried failed fresh heal; calls before=%d after=%d", firstCallCount, secondCallCount)
	}
}

func TestRunJobFailedHealReturnsError(t *testing.T) {
	s, err := store.NewStore(":memory:")
	if err != nil {
		t.Fatalf("create store: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	srv := New(s, search.NewSearch(s), nil, nil)
	ix := &healingIndexer{store: s, cleanText: "\xEF\xBB\xBFstill broken"}
	srv.indexer = ix

	const alias = "runjob-failed-heal"
	repoID, err := s.UpsertRepo(alias, "https://example.com/failed", `["docs"]`, "git")
	if err != nil {
		t.Fatalf("upsert: %v", err)
	}
	seedDoc(t, s, repoID, "a.md", "\xEF\xBB\xBFhello")

	res := srv.runJob(context.Background(), &Job{
		Alias:  alias,
		Kind:   jobKindGit,
		URL:    "https://example.com/failed",
		Paths:  []string{"docs"},
		Force:  true,
		RepoID: repoID,
	})
	if res.Err == nil {
		t.Fatal("expected failed convergence to return JobResult.Err")
	}
	if !strings.HasPrefix(res.Err.Error(), autoHealDidNotConvergePrefix) {
		t.Fatalf("JobResult.Err = %q, want prefix %q", res.Err.Error(), autoHealDidNotConvergePrefix)
	}
}
