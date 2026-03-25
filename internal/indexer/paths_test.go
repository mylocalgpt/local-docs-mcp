package indexer

import (
	"reflect"
	"testing"
)

func TestMergePathsDisjoint(t *testing.T) {
	got := MergePaths([]string{"docs/"}, []string{"api/"})
	want := []string{"api/", "docs/"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMergePathsParentSubsumesChild(t *testing.T) {
	got := MergePaths([]string{"docs/"}, []string{"docs/identity/hybrid/"})
	want := []string{"docs/"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMergePathsIdenticalDeduplicated(t *testing.T) {
	got := MergePaths([]string{"docs/", "api/"}, []string{"docs/", "api/"})
	want := []string{"api/", "docs/"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMergePathsFilePaths(t *testing.T) {
	got := MergePaths([]string{"README.md"}, []string{"docs/"})
	want := []string{"README.md", "docs/"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMergePathsBothEmpty(t *testing.T) {
	got := MergePaths(nil, nil)
	if len(got) != 0 {
		t.Errorf("expected empty, got %v", got)
	}
}

func TestMergePathsOneEmpty(t *testing.T) {
	got := MergePaths(nil, []string{"docs/"})
	want := []string{"docs/"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestMergePathsNormalization(t *testing.T) {
	// Paths without trailing slash should be normalized
	got := MergePaths([]string{"docs"}, []string{"api"})
	want := []string{"api/", "docs/"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
