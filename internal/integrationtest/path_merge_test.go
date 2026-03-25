//go:build integration

package integrationtest

import (
	"testing"

	"github.com/mylocalgpt/local-docs-mcp/internal/indexer"
)

func TestPathMergeSubsumption(t *testing.T) {
	// Parent should subsume child
	result := indexer.MergePaths([]string{"docs/"}, []string{"docs/identity/hybrid/"})
	if len(result) != 1 || result[0] != "docs/" {
		t.Errorf("expected [docs/], got %v", result)
	}

	// Disjoint paths both kept
	result2 := indexer.MergePaths([]string{"docs/"}, []string{"api/"})
	if len(result2) != 2 {
		t.Errorf("expected 2 paths, got %v", result2)
	}

	// File paths not subsumed by directories
	result3 := indexer.MergePaths([]string{"README.md"}, []string{"docs/"})
	if len(result3) != 2 {
		t.Errorf("expected 2 paths (file + dir), got %v", result3)
	}
}
