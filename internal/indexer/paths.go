package indexer

import (
	"path/filepath"
	"sort"
	"strings"
)

// MergePaths merges two path slices, deduplicates them, and removes paths
// that are children of other paths in the result (parent subsumes child).
// File paths (last segment contains a dot) are not directory-normalized.
func MergePaths(existing, new []string) []string {
	seen := make(map[string]bool)
	var all []string

	for _, p := range append(existing, new...) {
		p = filepath.Clean(p)
		// Normalize directories with trailing slash for prefix comparison,
		// but not file paths (last segment contains a dot).
		if !isFilePath(p) && !strings.HasSuffix(p, "/") {
			p += "/"
		}
		if !seen[p] {
			seen[p] = true
			all = append(all, p)
		}
	}

	sort.Strings(all)

	// Remove paths that are children of other paths.
	var result []string
	for _, p := range all {
		subsumed := false
		for _, parent := range result {
			if strings.HasPrefix(p, parent) && p != parent {
				subsumed = true
				break
			}
		}
		if !subsumed {
			result = append(result, p)
		}
	}

	return result
}

// isFilePath returns true if the path looks like a file (last segment has a dot).
func isFilePath(p string) bool {
	base := filepath.Base(p)
	return strings.Contains(base, ".")
}
