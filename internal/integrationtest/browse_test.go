//go:build integration

package integrationtest

import (
	"strings"
	"testing"
)

func TestBrowseFiles(t *testing.T) {
	s := openTestStore(t)

	repo, err := s.GetRepo("entra-hybrid")
	if err != nil {
		t.Fatalf("get repo: %v", err)
	}
	if repo == nil {
		t.Fatal("repo entra-hybrid not found")
	}

	files, err := s.BrowseFiles(repo.ID)
	if err != nil {
		t.Fatalf("browse files: %v", err)
	}

	t.Logf("file count: %d", len(files))

	if len(files) < 50 {
		t.Errorf("expected 50+ files, got %d", len(files))
	}

	var hasConnect, hasCloudSync, hasHybridRoot bool
	for _, f := range files {
		if strings.HasPrefix(f.Path, "docs/identity/hybrid/connect/") {
			hasConnect = true
		}
		if strings.HasPrefix(f.Path, "docs/identity/hybrid/cloud-sync/") {
			hasCloudSync = true
		}
		if strings.HasPrefix(f.Path, "docs/identity/hybrid/") {
			hasHybridRoot = true
		}
	}

	if !hasConnect {
		t.Error("expected files from docs/identity/hybrid/connect/")
	}
	if !hasCloudSync {
		t.Error("expected files from docs/identity/hybrid/cloud-sync/")
	}
	if !hasHybridRoot {
		t.Error("expected files from docs/identity/hybrid/")
	}

	for _, f := range files {
		if f.Sections <= 0 {
			t.Errorf("file %s has %d sections, expected > 0", f.Path, f.Sections)
		}
	}
}

func TestBrowseHeadings(t *testing.T) {
	s := openTestStore(t)

	repo, err := s.GetRepo("entra-hybrid")
	if err != nil {
		t.Fatalf("get repo: %v", err)
	}
	if repo == nil {
		t.Fatal("repo entra-hybrid not found")
	}

	files, err := s.BrowseFiles(repo.ID)
	if err != nil {
		t.Fatalf("browse files: %v", err)
	}

	// Find a file in the connect path with multiple sections
	var targetPath string
	for _, f := range files {
		if strings.Contains(f.Path, "connect/") && f.Sections >= 3 {
			targetPath = f.Path
			break
		}
	}
	if targetPath == "" {
		t.Skip("no suitable file found for heading test")
	}

	t.Logf("testing headings for: %s", targetPath)

	headings, err := s.BrowseHeadings(repo.ID, targetPath)
	if err != nil {
		t.Fatalf("browse headings: %v", err)
	}
	if len(headings) == 0 {
		t.Fatal("expected headings, got 0")
	}

	for _, h := range headings {
		if h.HeadingLevel < 1 || h.HeadingLevel > 6 {
			t.Errorf("unexpected heading level %d for %q", h.HeadingLevel, h.SectionTitle)
		}
		if h.SectionTitle == "" {
			t.Error("empty section title in headings")
		}
	}

	t.Logf("headings count: %d", len(headings))
}
