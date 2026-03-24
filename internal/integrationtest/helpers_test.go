//go:build integration

package integrationtest

import (
	"log"
	"os"
	"path/filepath"
	"testing"

	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

var testDBPath string

// findProjectRoot walks up from the current working directory looking for go.mod.
func findProjectRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		log.Fatalf("getwd: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			log.Fatal("could not find project root (no go.mod)")
		}
		dir = parent
	}
}

// openTestStore opens the pre-indexed DB for read-only test queries.
// Tests sharing testDBPath must not write to it. Update/write tests
// use separate temp DBs.
func openTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.NewStore(testDBPath)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
