//go:build integration

package integrationtest

import (
	"context"
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/mylocalgpt/local-docs-mcp/internal/config"
	"github.com/mylocalgpt/local-docs-mcp/internal/indexer"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

// indexResult captures the result of the initial indexing for assertions.
var indexResult *indexer.IndexResult

func TestMain(m *testing.M) {
	tmpDir, err := os.MkdirTemp("", "integration-test-*")
	if err != nil {
		log.Fatalf("create temp dir: %v", err)
	}
	testDBPath = filepath.Join(tmpDir, "test.db")

	s, err := store.NewStore(testDBPath)
	if err != nil {
		log.Fatalf("create store: %v", err)
	}
	ix, err := indexer.NewIndexer(s)
	if err != nil {
		log.Fatalf("create indexer: %v", err)
	}

	cfg, err := config.LoadConfig(filepath.Join(findProjectRoot(), "examples", "entra-config.json"))
	if err != nil {
		log.Fatalf("load config: %v", err)
	}
	for _, repo := range cfg.Repos {
		result, err := ix.IndexRepo(context.Background(), repo, false)
		if err != nil {
			log.Fatalf("index %s: %v", repo.Alias, err)
		}
		if result.Error != nil {
			log.Fatalf("index %s: %v", repo.Alias, result.Error)
		}
		indexResult = result
	}
	if err := s.RebuildFTS(); err != nil {
		log.Fatalf("rebuild fts: %v", err)
	}
	ix.Cleanup()
	s.Close()

	code := m.Run()
	os.RemoveAll(tmpDir)
	os.Exit(code)
}

func TestIndexEntraDocs(t *testing.T) {
	if indexResult == nil {
		t.Fatal("indexResult is nil, TestMain did not complete indexing")
	}
	if indexResult.DocsIndexed == 0 {
		t.Error("expected DocsIndexed > 0")
	}
	if indexResult.Duration <= 0 {
		t.Error("expected Duration > 0")
	}
	if indexResult.Error != nil {
		t.Errorf("unexpected error: %v", indexResult.Error)
	}
	t.Logf("indexed %d docs in %s", indexResult.DocsIndexed, indexResult.Duration)
}

func TestIndexDocCount(t *testing.T) {
	s := openTestStore(t)
	db := s.DB()

	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM documents WHERE repo_id = (SELECT id FROM repos WHERE alias = 'entra-hybrid')").Scan(&count)
	if err != nil {
		t.Fatalf("count query: %v", err)
	}
	t.Logf("doc count: %d", count)

	if count < 500 {
		t.Errorf("expected at least 500 chunks, got %d", count)
	}
	if count > 5000 {
		t.Errorf("expected at most 5000 chunks, got %d", count)
	}
}

func TestIndexTokenDistribution(t *testing.T) {
	s := openTestStore(t)
	db := s.DB()

	var minTok, maxTok int
	var avgTok float64
	err := db.QueryRow("SELECT MIN(tokens), MAX(tokens), AVG(tokens) FROM documents").Scan(&minTok, &maxTok, &avgTok)
	if err != nil {
		t.Fatalf("token stats query: %v", err)
	}
	t.Logf("tokens: min=%d, max=%d, avg=%.1f", minTok, maxTok, avgTok)

	if maxTok > 1200 {
		t.Errorf("max tokens %d exceeds 1200", maxTok)
	}
	if avgTok < 100 || avgTok > 800 {
		t.Errorf("avg tokens %.1f outside expected range 100-800", avgTok)
	}
}

func TestIndexChunkMetadata(t *testing.T) {
	s := openTestStore(t)
	db := s.DB()

	rows, err := db.Query("SELECT doc_title, section_title, content FROM documents ORDER BY RANDOM() LIMIT 10")
	if err != nil {
		t.Fatalf("sample query: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var docTitle, sectionTitle, content string
		if err := rows.Scan(&docTitle, &sectionTitle, &content); err != nil {
			t.Fatalf("scan: %v", err)
		}
		count++
		if docTitle == "" {
			t.Errorf("chunk %d: doc_title is empty", count)
		}
		if sectionTitle == "" {
			t.Errorf("chunk %d: section_title is empty", count)
		}
		if len(content) < 20 {
			t.Errorf("chunk %d: content too short (%d chars)", count, len(content))
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows error: %v", err)
	}
	if count < 10 {
		t.Errorf("expected 10 sample chunks, got %d", count)
	}
}

func TestIndexHasCode(t *testing.T) {
	s := openTestStore(t)
	db := s.DB()

	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM documents WHERE has_code = 1").Scan(&count)
	if err != nil {
		t.Fatalf("has_code query: %v", err)
	}
	t.Logf("chunks with code: %d", count)
	if count < 1 {
		t.Error("expected at least 1 chunk with has_code = 1")
	}
}

func TestIndexDeduplication(t *testing.T) {
	s := openTestStore(t)
	db := s.DB()

	rows, err := db.Query("SELECT repo_id, path, content, COUNT(*) as c FROM documents GROUP BY repo_id, path, content HAVING c > 1")
	if err != nil {
		t.Fatalf("dedup query: %v", err)
	}
	defer rows.Close()

	dupes := 0
	for rows.Next() {
		var repoID int64
		var path, content string
		var c int
		if err := rows.Scan(&repoID, &path, &content, &c); err != nil {
			t.Fatalf("scan: %v", err)
		}
		dupes++
		if dupes <= 3 {
			t.Logf("duplicate: repo_id=%d path=%s count=%d content_len=%d", repoID, path, c, len(content))
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows error: %v", err)
	}
	if dupes > 0 {
		t.Errorf("found %d intra-file content duplicates, expected 0", dupes)
	}
}

func TestIndexNoEmptyChunks(t *testing.T) {
	s := openTestStore(t)
	db := s.DB()

	var count int
	err := db.QueryRow("SELECT COUNT(*) FROM documents WHERE length(content) < 10").Scan(&count)
	if err != nil {
		t.Fatalf("empty chunks query: %v", err)
	}
	t.Logf("chunks with content < 10 chars: %d", count)
	if count > 0 {
		t.Errorf("found %d chunks with content shorter than 10 chars", count)
	}
}
