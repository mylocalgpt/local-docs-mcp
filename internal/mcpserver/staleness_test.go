package mcpserver

import (
	"bytes"
	"context"
	"fmt"
	"log"
	"strings"
	"testing"
	"time"

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
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			gotStale, gotReason := srv.staleness(context.Background(), tc.repo)
			if gotStale != tc.wantStale {
				t.Errorf("stale = %v, want %v", gotStale, tc.wantStale)
			}
			if gotReason != tc.wantReason {
				t.Errorf("reason = %q, want %q", gotReason, tc.wantReason)
			}
		})
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
