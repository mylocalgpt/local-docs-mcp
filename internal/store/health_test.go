package store

import (
	"context"
	"fmt"
	"testing"
)

// insertRawDoc bypasses the regular writer so we can plant rows whose
// content the normal indexer would have rejected (BOMs, embedded NULs,
// invalid UTF-8 sequences).
func insertRawDoc(t *testing.T, s *Store, repoID int64, path, content string) {
	t.Helper()
	_, err := s.db.Exec(
		`INSERT INTO documents (repo_id, path, doc_title, section_title, content, tokens, heading_level, has_code)
         VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		repoID, path, "doc", "section", content, 1, 1, 0,
	)
	if err != nil {
		t.Fatalf("insertRawDoc: %v", err)
	}
}

func newRepoForHealth(t *testing.T, s *Store, alias string) int64 {
	t.Helper()
	id, err := s.UpsertRepo(alias, "https://example.com/"+alias, `["docs"]`, "git")
	if err != nil {
		t.Fatalf("UpsertRepo: %v", err)
	}
	return id
}

func TestRepoHasInvalidEncoding_CleanRepo(t *testing.T) {
	s := newTestStore(t)
	repoID := newRepoForHealth(t, s, "clean")
	insertRawDoc(t, s, repoID, "a.md", "hello world")
	insertRawDoc(t, s, repoID, "b.md", "another clean ascii row")

	bad, err := s.RepoHasInvalidEncoding(context.Background(), repoID)
	if err != nil {
		t.Fatalf("RepoHasInvalidEncoding: %v", err)
	}
	if bad {
		t.Errorf("expected false for clean repo, got true")
	}
}

func TestRepoHasInvalidEncoding_CleanRepoUnicode(t *testing.T) {
	s := newTestStore(t)
	repoID := newRepoForHealth(t, s, "cleanunicode")
	docs := map[string]string{
		"ascii.md":      "hello world",
		"latin.md":      "héllo",
		"decomposed.md": "he\u0301llo",
		"cjk.md":        "日本語",
		"emoji.md":      "🎉",
		"quotes.md":     "“smart quotes”",
		"rtl.md":        "مرحبا بالعالم",
		"code.md":       "```go\nfmt.Println(\"hello\")\n```",
	}
	for path, content := range docs {
		insertRawDoc(t, s, repoID, path, content)
	}

	bad, err := s.RepoHasInvalidEncoding(context.Background(), repoID)
	if err != nil {
		t.Fatalf("RepoHasInvalidEncoding: %v", err)
	}
	if bad {
		t.Errorf("expected false for clean Unicode repo, got true")
	}
}

func TestRepoHasInvalidEncoding_UTF8BOM(t *testing.T) {
	s := newTestStore(t)
	repoID := newRepoForHealth(t, s, "utf8bom")
	insertRawDoc(t, s, repoID, "a.md", "\xEF\xBB\xBFhello")

	bad, err := s.RepoHasInvalidEncoding(context.Background(), repoID)
	if err != nil {
		t.Fatalf("RepoHasInvalidEncoding: %v", err)
	}
	if !bad {
		t.Errorf("expected true for UTF-8 BOM, got false")
	}
}

func TestRepoHasInvalidEncoding_UTF16LEBOM(t *testing.T) {
	s := newTestStore(t)
	repoID := newRepoForHealth(t, s, "utf16le")
	insertRawDoc(t, s, repoID, "a.md", "\xFF\xFEhello")

	bad, err := s.RepoHasInvalidEncoding(context.Background(), repoID)
	if err != nil {
		t.Fatalf("RepoHasInvalidEncoding: %v", err)
	}
	if !bad {
		t.Errorf("expected true for UTF-16LE BOM, got false")
	}
}

func TestRepoHasInvalidEncoding_UTF16BEBOM(t *testing.T) {
	s := newTestStore(t)
	repoID := newRepoForHealth(t, s, "utf16be")
	insertRawDoc(t, s, repoID, "a.md", "\xFE\xFFhello")

	bad, err := s.RepoHasInvalidEncoding(context.Background(), repoID)
	if err != nil {
		t.Fatalf("RepoHasInvalidEncoding: %v", err)
	}
	if !bad {
		t.Errorf("expected true for UTF-16BE BOM, got false")
	}
}

func TestRepoHasInvalidEncoding_EmbeddedNUL(t *testing.T) {
	s := newTestStore(t)
	repoID := newRepoForHealth(t, s, "nul")
	insertRawDoc(t, s, repoID, "a.md", "hello\x00world")

	bad, err := s.RepoHasInvalidEncoding(context.Background(), repoID)
	if err != nil {
		t.Fatalf("RepoHasInvalidEncoding: %v", err)
	}
	if !bad {
		t.Errorf("expected true for embedded NUL, got false")
	}
}

func TestRepoHasInvalidEncoding_Pass1ExhaustiveAcrossManyRows(t *testing.T) {
	s := newTestStore(t)
	repoID := newRepoForHealth(t, s, "pass1sparse")
	for i := 0; i < 250; i++ {
		insertRawDoc(t, s, repoID, fmt.Sprintf("doc-%03d.md", i), "clean ascii row")
	}
	insertRawDoc(t, s, repoID, "corrupt.md", "\xEF\xBB\xBFcorrupt")

	bad, err := s.RepoHasInvalidEncoding(context.Background(), repoID)
	if err != nil {
		t.Fatalf("RepoHasInvalidEncoding: %v", err)
	}
	if !bad {
		t.Errorf("expected true for Pass-1 BOM hit across many rows, got false")
	}
}

func TestRepoHasInvalidEncoding_Pass2InvalidUTF8(t *testing.T) {
	s := newTestStore(t)
	repoID := newRepoForHealth(t, s, "pass2invalid")
	// Leading byte 0xC3 expects a continuation byte 0x80-0xBF; 0x28 is not.
	insertRawDoc(t, s, repoID, "a.md", "hello \xC3\x28 world")

	bad, err := s.RepoHasInvalidEncoding(context.Background(), repoID)
	if err != nil {
		t.Fatalf("RepoHasInvalidEncoding: %v", err)
	}
	if !bad {
		t.Errorf("expected true for invalid UTF-8 via Pass 2, got false")
	}
}

func TestRepoHasInvalidEncoding_Pass2ExhaustiveAcrossManyRows(t *testing.T) {
	s := newTestStore(t)
	repoID := newRepoForHealth(t, s, "pass2sparse")
	for i := 0; i < 250; i++ {
		insertRawDoc(t, s, repoID, fmt.Sprintf("doc-%03d.md", i), "clean ascii row")
	}
	insertRawDoc(t, s, repoID, "corrupt.md", "hello \xC3\x28 world")

	bad, err := s.RepoHasInvalidEncoding(context.Background(), repoID)
	if err != nil {
		t.Fatalf("RepoHasInvalidEncoding: %v", err)
	}
	if !bad {
		t.Errorf("expected true for invalid UTF-8 across many rows, got false")
	}
}
