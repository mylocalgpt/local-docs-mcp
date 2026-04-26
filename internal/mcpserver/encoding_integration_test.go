package mcpserver

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

func waitForRepoStatus(t *testing.T, srv *Server, alias, status string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		repo, err := srv.store.GetRepo(alias)
		if err != nil {
			t.Fatalf("GetRepo: %v", err)
		}
		if repo != nil && repo.Status == status {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("repo %q did not reach status %q", alias, status)
}

// TestEncodingRegressionUTF16LEBOM reproduces the original Windows v0.1.10 bug:
// a markdown file written as UTF-16LE with BOM was indexed as a corrupted blob,
// so FTS5 returned no hits for words inside it. With the encoding fix in place,
// a search for a word from the file must return at least one result.
func TestEncodingRegressionUTF16LEBOM(t *testing.T) {
	cs, srv, cleanup := setupAddDocsTest(t)
	defer cleanup()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "hello.md"), encodeUTF16LEWithBOM("# Hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := callAddDocs(t, cs, map[string]any{
		"alias": "encoding-regression",
		"path":  dir,
	})
	if err != nil {
		t.Fatalf("add_docs failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("add_docs returned tool error: %s", result.Content[0].(*mcp.TextContent).Text)
	}

	// Drive the queue worker manually so we can wait for indexing to complete
	// without racing the server's background goroutine. Mirrors the pattern
	// in TestAddDocsLocalSource.
	workerCtx, workerCancel := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		srv.queue.worker(workerCtx, srv.runJob)
		close(workerDone)
	}()

	waitForRepoStatus(t, srv, "encoding-regression", store.StatusReady)

	workerCancel()
	<-workerDone

	repo, err := srv.store.GetRepo("encoding-regression")
	if err != nil {
		t.Fatal(err)
	}
	if repo == nil || repo.Status != store.StatusReady {
		t.Fatalf("expected ready repo, got %+v", repo)
	}

	searchResult, err := cs.CallTool(context.Background(), &mcp.CallToolParams{
		Name: "search_docs",
		Arguments: map[string]any{
			"query": "world",
		},
	})
	if err != nil {
		t.Fatalf("search_docs failed: %v", err)
	}
	if searchResult.IsError {
		t.Fatalf("search_docs returned tool error: %s", searchResult.Content[0].(*mcp.TextContent).Text)
	}

	text := searchResult.Content[0].(*mcp.TextContent).Text
	// On a regression, the FTS index would contain only BOM-corrupted bytes
	// and "world" would not match. With the decoder in place we expect a hit
	// from the encoding-regression repo.
	if !strings.Contains(text, "encoding-regression") {
		t.Errorf("expected hit from encoding-regression repo, got: %s", text)
	}
	if strings.Contains(text, "No results") {
		t.Errorf("expected at least one result, got: %s", text)
	}
}

func TestAutoRefresh_NoLoopOnCleanContent(t *testing.T) {
	cs, srv, cleanup := setupAddDocsTest(t)
	defer cleanup()

	dir := t.TempDir()
	content := "# Multilingual\n\ncafe resume CJK 漢字 emoji 😀 smart quotes “ok”\n\n```go\nfmt.Println(\"hello\")\n```\n"
	if err := os.WriteFile(filepath.Join(dir, "unicode.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := callAddDocs(t, cs, map[string]any{
		"alias": "clean-local",
		"path":  dir,
	})
	if err != nil {
		t.Fatalf("add_docs failed: %v", err)
	}
	if result.IsError {
		t.Fatalf("add_docs returned tool error: %s", result.Content[0].(*mcp.TextContent).Text)
	}

	workerCtx, workerCancel := context.WithCancel(context.Background())
	workerDone := make(chan struct{})
	go func() {
		srv.queue.worker(workerCtx, srv.runJob)
		close(workerDone)
	}()
	defer func() {
		workerCancel()
		<-workerDone
	}()

	waitForRepoStatus(t, srv, "clean-local", store.StatusReady)
	repo, err := srv.store.GetRepo("clean-local")
	if err != nil {
		t.Fatal(err)
	}
	if repo == nil {
		t.Fatal("repo missing after index")
	}
	firstCount := repo.DocCount

	srv.autoRefresh(context.Background())
	time.Sleep(100 * time.Millisecond)

	repo, err = srv.store.GetRepo("clean-local")
	if err != nil {
		t.Fatal(err)
	}
	if repo.DocCount != firstCount {
		t.Fatalf("index_count changed after second boot: got %d, want %d", repo.DocCount, firstCount)
	}
}

// encodeUTF16LEWithBOM returns the bytes of s encoded as UTF-16LE prefixed
// with the 0xFF 0xFE BOM. Used to reproduce the original Windows file format.
func encodeUTF16LEWithBOM(s string) []byte {
	var buf bytes.Buffer
	buf.Write([]byte{0xFF, 0xFE})
	for _, r := range utf16.Encode([]rune(s)) {
		_ = binary.Write(&buf, binary.LittleEndian, r)
	}
	return buf.Bytes()
}
