package store

import (
	"testing"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(":memory:")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestUpsertAndGetRepo(t *testing.T) {
	s := newTestStore(t)

	id, err := s.UpsertRepo("myrepo", "https://github.com/example/repo", `["docs"]`)
	if err != nil {
		t.Fatalf("UpsertRepo: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}

	r, err := s.GetRepo("myrepo")
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if r == nil {
		t.Fatal("GetRepo returned nil")
	}
	if r.ID != id {
		t.Errorf("ID mismatch: got %d, want %d", r.ID, id)
	}
	if r.Alias != "myrepo" {
		t.Errorf("Alias mismatch: got %q", r.Alias)
	}
	if r.URL != "https://github.com/example/repo" {
		t.Errorf("URL mismatch: got %q", r.URL)
	}
	if r.Paths != `["docs"]` {
		t.Errorf("Paths mismatch: got %q", r.Paths)
	}
}

func TestGetRepoNotFound(t *testing.T) {
	s := newTestStore(t)

	r, err := s.GetRepo("nonexistent")
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if r != nil {
		t.Fatalf("expected nil, got %+v", r)
	}
}

func TestUpsertRepoPreservesID(t *testing.T) {
	s := newTestStore(t)

	id1, err := s.UpsertRepo("myrepo", "https://example.com/old", `["docs"]`)
	if err != nil {
		t.Fatalf("UpsertRepo: %v", err)
	}

	id2, err := s.UpsertRepo("myrepo", "https://example.com/new", `["docs","api"]`)
	if err != nil {
		t.Fatalf("UpsertRepo: %v", err)
	}

	if id1 != id2 {
		t.Errorf("upsert changed ID: %d -> %d", id1, id2)
	}

	r, err := s.GetRepo("myrepo")
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if r.URL != "https://example.com/new" {
		t.Errorf("URL not updated: got %q", r.URL)
	}
	if r.Paths != `["docs","api"]` {
		t.Errorf("Paths not updated: got %q", r.Paths)
	}
}

func TestReplaceDocumentsAndFTS(t *testing.T) {
	s := newTestStore(t)

	repoID, err := s.UpsertRepo("testrepo", "https://example.com/repo", `["docs"]`)
	if err != nil {
		t.Fatalf("UpsertRepo: %v", err)
	}

	docs := []Document{
		{
			RepoID:       repoID,
			Path:         "docs/guide.md",
			DocTitle:     "Getting Started",
			SectionTitle: "Installation",
			Content:      "Run go install to set up the toolchain",
			Tokens:       8,
			HeadingLevel: 2,
			HasCode:      true,
		},
		{
			RepoID:       repoID,
			Path:         "docs/guide.md",
			DocTitle:     "Getting Started",
			SectionTitle: "Configuration",
			Content:      "Create a config file in your home directory",
			Tokens:       9,
			HeadingLevel: 2,
			HasCode:      false,
		},
	}

	if err := s.ReplaceDocuments(repoID, docs); err != nil {
		t.Fatalf("ReplaceDocuments: %v", err)
	}

	// Verify row count.
	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM documents WHERE repo_id = ?", repoID).Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 documents, got %d", count)
	}

	// Verify FTS search.
	var rowid int64
	var title string
	err = s.db.QueryRow(
		"SELECT rowid, doc_title FROM docs_fts WHERE docs_fts MATCH 'toolchain'",
	).Scan(&rowid, &title)
	if err != nil {
		t.Fatalf("FTS search: %v", err)
	}
	if title != "Getting Started" {
		t.Errorf("FTS title mismatch: got %q", title)
	}
}

func TestReplaceDocumentsEmptyClearsFTS(t *testing.T) {
	s := newTestStore(t)

	repoID, err := s.UpsertRepo("testrepo", "https://example.com/repo", `["docs"]`)
	if err != nil {
		t.Fatalf("UpsertRepo: %v", err)
	}

	docs := []Document{
		{
			RepoID:       repoID,
			Path:         "docs/guide.md",
			DocTitle:     "Guide",
			SectionTitle: "Intro",
			Content:      "Unique searchable xylophone content",
			Tokens:       5,
			HeadingLevel: 1,
			HasCode:      false,
		},
	}

	if err := s.ReplaceDocuments(repoID, docs); err != nil {
		t.Fatalf("ReplaceDocuments: %v", err)
	}

	// Replace with empty slice to remove everything.
	if err := s.ReplaceDocuments(repoID, nil); err != nil {
		t.Fatalf("ReplaceDocuments empty: %v", err)
	}

	var count int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM documents WHERE repo_id = ?", repoID).Scan(&count); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if count != 0 {
		t.Errorf("expected 0 documents, got %d", count)
	}

	// FTS should also be empty.
	err = s.db.QueryRow("SELECT rowid FROM docs_fts WHERE docs_fts MATCH 'xylophone'").Scan(new(int64))
	if err == nil {
		t.Error("FTS should be empty after clearing documents")
	}
}

func TestRebuildFTS(t *testing.T) {
	s := newTestStore(t)

	if err := s.RebuildFTS(); err != nil {
		t.Fatalf("RebuildFTS: %v", err)
	}
}

func TestUpdateRepoIndex(t *testing.T) {
	s := newTestStore(t)

	id, err := s.UpsertRepo("testrepo", "https://example.com/repo", `["docs"]`)
	if err != nil {
		t.Fatalf("UpsertRepo: %v", err)
	}

	if err := s.UpdateRepoIndex(id, "abc123", "2025-01-01T00:00:00Z", 42); err != nil {
		t.Fatalf("UpdateRepoIndex: %v", err)
	}

	r, err := s.GetRepo("testrepo")
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if r.CommitSHA != "abc123" {
		t.Errorf("CommitSHA mismatch: got %q", r.CommitSHA)
	}
	if r.IndexedAt != "2025-01-01T00:00:00Z" {
		t.Errorf("IndexedAt mismatch: got %q", r.IndexedAt)
	}
	if r.DocCount != 42 {
		t.Errorf("DocCount mismatch: got %d", r.DocCount)
	}
}
