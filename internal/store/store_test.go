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

	id, err := s.UpsertRepo("myrepo", "https://github.com/example/repo", `["docs"]`, "git")
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
	if r.SourceType != "git" {
		t.Errorf("SourceType mismatch: got %q, want %q", r.SourceType, "git")
	}
	if r.Status != "ready" {
		t.Errorf("Status mismatch: got %q, want %q", r.Status, "ready")
	}
	if r.StatusDetail != "" {
		t.Errorf("StatusDetail should be empty, got %q", r.StatusDetail)
	}
	if r.StatusUpdatedAt != "" {
		t.Errorf("StatusUpdatedAt should be empty, got %q", r.StatusUpdatedAt)
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

	id1, err := s.UpsertRepo("myrepo", "https://example.com/old", `["docs"]`, "git")
	if err != nil {
		t.Fatalf("UpsertRepo: %v", err)
	}

	id2, err := s.UpsertRepo("myrepo", "https://example.com/new", `["docs","api"]`, "git")
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

	repoID, err := s.UpsertRepo("testrepo", "https://example.com/repo", `["docs"]`, "git")
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

	repoID, err := s.UpsertRepo("testrepo", "https://example.com/repo", `["docs"]`, "git")
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

func TestListRepos(t *testing.T) {
	s := newTestStore(t)

	// Empty initially
	repos, err := s.ListRepos()
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(repos) != 0 {
		t.Errorf("expected 0 repos, got %d", len(repos))
	}

	// Insert two repos
	s.UpsertRepo("bravo", "https://example.com/b", `["docs"]`, "git")
	s.UpsertRepo("alpha", "https://example.com/a", `["docs"]`, "git")

	repos, err = s.ListRepos()
	if err != nil {
		t.Fatalf("ListRepos: %v", err)
	}
	if len(repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(repos))
	}
	// Should be ordered by alias
	if repos[0].Alias != "alpha" || repos[1].Alias != "bravo" {
		t.Errorf("wrong order: %s, %s", repos[0].Alias, repos[1].Alias)
	}
}

func TestDeleteRepo(t *testing.T) {
	s := newTestStore(t)

	repoID, _ := s.UpsertRepo("deleteme", "https://example.com/del", `["docs"]`, "git")
	s.ReplaceDocuments(repoID, []Document{
		{RepoID: repoID, Path: "a.md", DocTitle: "A", SectionTitle: "A1", Content: "content one", Tokens: 10, HeadingLevel: 1},
		{RepoID: repoID, Path: "b.md", DocTitle: "B", SectionTitle: "B1", Content: "content two", Tokens: 20, HeadingLevel: 1},
	})

	count, err := s.DeleteRepo("deleteme")
	if err != nil {
		t.Fatalf("DeleteRepo: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 deleted docs, got %d", count)
	}

	// Repo should be gone
	r, err := s.GetRepo("deleteme")
	if err != nil {
		t.Fatalf("GetRepo after delete: %v", err)
	}
	if r != nil {
		t.Error("repo still exists after delete")
	}

	// Delete unknown alias should error
	_, err = s.DeleteRepo("nonexistent")
	if err == nil {
		t.Error("expected error deleting nonexistent repo")
	}
}

func TestBrowseFiles(t *testing.T) {
	s := newTestStore(t)

	repoID, _ := s.UpsertRepo("browse-repo", "https://example.com/br", `["docs"]`, "git")
	s.ReplaceDocuments(repoID, []Document{
		{RepoID: repoID, Path: "docs/guide.md", DocTitle: "Guide", SectionTitle: "Intro", Content: "intro", Tokens: 10, HeadingLevel: 1},
		{RepoID: repoID, Path: "docs/guide.md", DocTitle: "Guide", SectionTitle: "Setup", Content: "setup", Tokens: 20, HeadingLevel: 2},
		{RepoID: repoID, Path: "docs/api.md", DocTitle: "API", SectionTitle: "Overview", Content: "overview", Tokens: 15, HeadingLevel: 1},
	})

	files, err := s.BrowseFiles(repoID)
	if err != nil {
		t.Fatalf("BrowseFiles: %v", err)
	}
	if len(files) != 2 {
		t.Fatalf("expected 2 files, got %d", len(files))
	}
	// Ordered by path
	if files[0].Path != "docs/api.md" || files[0].Sections != 1 {
		t.Errorf("file 0: got %+v", files[0])
	}
	if files[1].Path != "docs/guide.md" || files[1].Sections != 2 {
		t.Errorf("file 1: got %+v", files[1])
	}
}

func TestBrowseHeadings(t *testing.T) {
	s := newTestStore(t)

	repoID, _ := s.UpsertRepo("heading-repo", "https://example.com/hr", `["docs"]`, "git")
	s.ReplaceDocuments(repoID, []Document{
		{RepoID: repoID, Path: "guide.md", DocTitle: "Guide", SectionTitle: "Getting Started", Content: "intro", Tokens: 100, HeadingLevel: 2},
		{RepoID: repoID, Path: "guide.md", DocTitle: "Guide", SectionTitle: "Installation", Content: "install", Tokens: 50, HeadingLevel: 3},
		{RepoID: repoID, Path: "guide.md", DocTitle: "Guide", SectionTitle: "Quick Start", Content: "quick", Tokens: 75, HeadingLevel: 3},
	})

	headings, err := s.BrowseHeadings(repoID, "guide.md")
	if err != nil {
		t.Fatalf("BrowseHeadings: %v", err)
	}
	if len(headings) != 3 {
		t.Fatalf("expected 3 headings, got %d", len(headings))
	}
	if headings[0].SectionTitle != "Getting Started" || headings[0].HeadingLevel != 2 || headings[0].Tokens != 100 {
		t.Errorf("heading 0: got %+v", headings[0])
	}
	if headings[1].SectionTitle != "Installation" || headings[1].HeadingLevel != 3 {
		t.Errorf("heading 1: got %+v", headings[1])
	}
}

func TestUpdateRepoStatus(t *testing.T) {
	s := newTestStore(t)

	id, err := s.UpsertRepo("testrepo", "https://example.com/repo", `["docs"]`, "git")
	if err != nil {
		t.Fatalf("UpsertRepo: %v", err)
	}

	if err := s.UpdateRepoStatus(id, StatusIndexing, "cloning..."); err != nil {
		t.Fatalf("UpdateRepoStatus: %v", err)
	}

	r, err := s.GetRepo("testrepo")
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if r.Status != StatusIndexing {
		t.Errorf("Status mismatch: got %q, want %q", r.Status, StatusIndexing)
	}
	if r.StatusDetail != "cloning..." {
		t.Errorf("StatusDetail mismatch: got %q", r.StatusDetail)
	}
	if r.StatusUpdatedAt == "" {
		t.Error("StatusUpdatedAt should be set after UpdateRepoStatus")
	}

	// Update to error
	if err := s.UpdateRepoStatus(id, StatusError, "git clone failed"); err != nil {
		t.Fatalf("UpdateRepoStatus error: %v", err)
	}

	r, err = s.GetRepo("testrepo")
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if r.Status != StatusError {
		t.Errorf("Status mismatch: got %q, want %q", r.Status, StatusError)
	}
	if r.StatusDetail != "git clone failed" {
		t.Errorf("StatusDetail mismatch: got %q", r.StatusDetail)
	}
}

func TestDBPath(t *testing.T) {
	s, err := NewStore(":memory:")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	defer s.Close()

	if s.DBPath() != ":memory:" {
		t.Errorf("DBPath mismatch: got %q, want %q", s.DBPath(), ":memory:")
	}
}

func TestMigrationIdempotent(t *testing.T) {
	// Open a store twice to verify migration ALTER TABLE statements
	// are idempotent (duplicate column errors are ignored).
	s1, err := NewStore(":memory:")
	if err != nil {
		t.Fatalf("NewStore first: %v", err)
	}
	s1.Close()

	// Since :memory: is ephemeral, use a temp file to test real reopening.
	tmpDir := t.TempDir()
	dbPath := tmpDir + "/test.db"

	s2, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore create: %v", err)
	}
	s2.Close()

	// Reopen - migration should run again without error.
	s3, err := NewStore(dbPath)
	if err != nil {
		t.Fatalf("NewStore reopen: %v", err)
	}
	defer s3.Close()

	// Verify columns work after reopening.
	id, err := s3.UpsertRepo("test", "https://example.com", `["docs"]`, "git")
	if err != nil {
		t.Fatalf("UpsertRepo: %v", err)
	}

	r, err := s3.GetRepo("test")
	if err != nil {
		t.Fatalf("GetRepo: %v", err)
	}
	if r.SourceType != "git" {
		t.Errorf("SourceType mismatch: got %q", r.SourceType)
	}
	if r.Status != "ready" {
		t.Errorf("Status mismatch: got %q", r.Status)
	}

	if err := s3.UpdateRepoStatus(id, StatusIndexing, "test"); err != nil {
		t.Fatalf("UpdateRepoStatus: %v", err)
	}
}

func TestUpdateRepoIndex(t *testing.T) {
	s := newTestStore(t)

	id, err := s.UpsertRepo("testrepo", "https://example.com/repo", `["docs"]`, "git")
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
