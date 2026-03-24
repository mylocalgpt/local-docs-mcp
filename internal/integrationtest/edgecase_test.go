//go:build integration

package integrationtest

import (
	"testing"
)

func TestEdgeCaseDeepHeadings(t *testing.T) {
	s := openTestStore(t)

	rows, err := s.DB().Query(
		"SELECT path, section_title, heading_level, tokens FROM documents WHERE heading_level >= 4 LIMIT 20",
	)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	count := 0
	for rows.Next() {
		var path, sectionTitle string
		var headingLevel, tokens int
		if err := rows.Scan(&path, &sectionTitle, &headingLevel, &tokens); err != nil {
			t.Fatalf("scan: %v", err)
		}
		count++
		if tokens <= 0 {
			t.Errorf("deep heading chunk has 0 tokens: %s / %s (h%d)", path, sectionTitle, headingLevel)
		}
		t.Logf("deep heading: h%d %s / %s (%d tokens)", headingLevel, path, sectionTitle, tokens)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows error: %v", err)
	}
	t.Logf("found %d chunks with heading level >= 4", count)
}

func TestEdgeCaseCodeHeavyFiles(t *testing.T) {
	s := openTestStore(t)

	var codeChunks int
	if err := s.DB().QueryRow("SELECT COUNT(*) FROM documents WHERE has_code = 1").Scan(&codeChunks); err != nil {
		t.Fatalf("count query: %v", err)
	}
	t.Logf("code chunks: %d", codeChunks)

	rows, err := s.DB().Query(
		"SELECT path, section_title, content FROM documents WHERE has_code = 1 LIMIT 5",
	)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var path, title, content string
		if err := rows.Scan(&path, &title, &content); err != nil {
			t.Fatalf("scan: %v", err)
		}
		if len(content) < 20 {
			t.Errorf("code chunk %s/%s has very short content (%d chars)", path, title, len(content))
		}
		t.Logf("code chunk: %s / %s (%d chars)", path, title, len(content))
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows error: %v", err)
	}
}

func TestEdgeCaseLargeFiles(t *testing.T) {
	s := openTestStore(t)

	rows, err := s.DB().Query(`
		SELECT path, COUNT(*) as chunks, SUM(tokens) as total_tokens, MAX(tokens) as max_chunk
		FROM documents
		GROUP BY path
		ORDER BY total_tokens DESC
		LIMIT 5
	`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	for rows.Next() {
		var path string
		var chunks, totalTokens, maxChunk int
		if err := rows.Scan(&path, &chunks, &totalTokens, &maxChunk); err != nil {
			t.Fatalf("scan: %v", err)
		}
		t.Logf("large file: %s - %d chunks, %d total tokens, max chunk %d tokens", path, chunks, totalTokens, maxChunk)
		if maxChunk > 1200 {
			t.Errorf("file %s has a chunk with %d tokens (max 1200)", path, maxChunk)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows error: %v", err)
	}
}

func TestEdgeCaseYAMLFrontMatter(t *testing.T) {
	s := openTestStore(t)

	var yamlArtifacts int
	if err := s.DB().QueryRow(`
		SELECT COUNT(*) FROM documents
		WHERE content LIKE '---%' OR content LIKE '` + "\n" + `---%'
	`).Scan(&yamlArtifacts); err != nil {
		t.Fatalf("query: %v", err)
	}

	t.Logf("chunks with potential YAML artifacts: %d", yamlArtifacts)

	var totalDocs int
	if err := s.DB().QueryRow("SELECT COUNT(*) FROM documents").Scan(&totalDocs); err != nil {
		t.Fatalf("count query: %v", err)
	}
	if totalDocs > 0 && float64(yamlArtifacts)/float64(totalDocs) > 0.1 {
		t.Errorf("%.1f%% of chunks have potential YAML artifacts (%d/%d)",
			float64(yamlArtifacts)/float64(totalDocs)*100, yamlArtifacts, totalDocs)
	}
}

func TestEdgeCaseIncludeDirectives(t *testing.T) {
	s := openTestStore(t)

	var includeCount int
	if err := s.DB().QueryRow(`
		SELECT COUNT(*) FROM documents WHERE content LIKE '%[!INCLUDE%'
	`).Scan(&includeCount); err != nil {
		t.Fatalf("query: %v", err)
	}

	t.Logf("chunks with INCLUDE directives: %d (expected: left as-is in content)", includeCount)
}

func TestEdgeCaseEmptyChunks(t *testing.T) {
	s := openTestStore(t)

	var emptyCount int
	if err := s.DB().QueryRow("SELECT COUNT(*) FROM documents WHERE length(content) < 10").Scan(&emptyCount); err != nil {
		t.Fatalf("query: %v", err)
	}

	if emptyCount > 0 {
		t.Errorf("found %d chunks with content < 10 chars", emptyCount)

		rows, err := s.DB().Query(
			"SELECT path, section_title, content FROM documents WHERE length(content) < 10 LIMIT 10",
		)
		if err != nil {
			t.Fatalf("detail query: %v", err)
		}
		defer rows.Close()
		for rows.Next() {
			var path, title, content string
			if err := rows.Scan(&path, &title, &content); err != nil {
				t.Fatalf("scan: %v", err)
			}
			t.Logf("empty chunk: %s / %s: %q", path, title, content)
		}
	}
}

func TestEdgeCaseDuplicateHeadings(t *testing.T) {
	s := openTestStore(t)

	rows, err := s.DB().Query(`
		SELECT path, section_title, COUNT(*) as c
		FROM documents
		WHERE repo_id = (SELECT id FROM repos WHERE alias = 'entra-hybrid')
		GROUP BY path, section_title
		HAVING c > 1
		LIMIT 10
	`)
	if err != nil {
		t.Fatalf("query: %v", err)
	}
	defer rows.Close()

	var count int
	for rows.Next() {
		var path, title string
		var c int
		if err := rows.Scan(&path, &title, &c); err != nil {
			t.Fatalf("scan: %v", err)
		}
		t.Logf("duplicate heading: %s / %q (count: %d)", path, title, c)
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("rows error: %v", err)
	}
	t.Logf("files with duplicate section titles: %d", count)
}
