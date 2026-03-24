package indexer

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"
)

// initTestRepo creates a temporary git repo with files in two directories:
// docs/guide.md and src/main.go. Returns the repo path.
func initTestRepo(t *testing.T) string {
	t.Helper()

	dir := t.TempDir()

	// git init
	if _, err := runGitInDir(dir, "init"); err != nil {
		t.Fatalf("git init: %v", err)
	}

	// Configure user for commits (needed in CI / bare environments)
	if _, err := runGitInDir(dir, "config", "user.email", "test@test.com"); err != nil {
		t.Fatalf("git config email: %v", err)
	}
	if _, err := runGitInDir(dir, "config", "user.name", "Test"); err != nil {
		t.Fatalf("git config name: %v", err)
	}

	// Create docs/guide.md
	docsDir := filepath.Join(dir, "docs")
	if err := os.MkdirAll(docsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(docsDir, "guide.md"), []byte("# Guide\nHello"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create src/main.go
	srcDir := filepath.Join(dir, "src")
	if err := os.MkdirAll(srcDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Add and commit
	if _, err := runGitInDir(dir, "add", "."); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := runGitInDir(dir, "commit", "-m", "initial"); err != nil {
		t.Fatalf("git commit: %v", err)
	}

	return dir
}

// runGitInDir is a test helper that runs git with -C <dir> prepended.
func runGitInDir(dir string, args ...string) (string, error) {
	fullArgs := append([]string{"-C", dir}, args...)
	return runGit(fullArgs...)
}

func TestCheckGitVersion(t *testing.T) {
	if err := CheckGitVersion(); err != nil {
		t.Fatalf("CheckGitVersion: %v", err)
	}
}

func TestCloneDocFolders(t *testing.T) {
	origin := initTestRepo(t)
	dest := filepath.Join(t.TempDir(), "clone")

	if err := CloneDocFolders(origin, dest, []string{"docs"}); err != nil {
		t.Fatalf("CloneDocFolders: %v", err)
	}

	// docs/guide.md should exist
	if _, err := os.Stat(filepath.Join(dest, "docs", "guide.md")); err != nil {
		t.Errorf("expected docs/guide.md to exist: %v", err)
	}

	// src/main.go should NOT exist (sparse checkout excluded it)
	if _, err := os.Stat(filepath.Join(dest, "src", "main.go")); !os.IsNotExist(err) {
		t.Errorf("expected src/main.go to be absent, got err: %v", err)
	}
}

func TestGetCommitSHA(t *testing.T) {
	repo := initTestRepo(t)

	sha, err := GetCommitSHA(repo)
	if err != nil {
		t.Fatalf("GetCommitSHA: %v", err)
	}

	matched, _ := regexp.MatchString(`^[0-9a-f]{40}$`, sha)
	if !matched {
		t.Errorf("expected 40 hex chars, got %q", sha)
	}
}

func TestCloneNoCheckoutThenSparseCheckout(t *testing.T) {
	origin := initTestRepo(t)
	dest := filepath.Join(t.TempDir(), "clone2")

	// Step 1: clone without checkout
	if err := CloneNoCheckout(origin, dest); err != nil {
		t.Fatalf("CloneNoCheckout: %v", err)
	}

	// No files should be checked out yet (the directory exists but is mostly empty)
	if _, err := os.Stat(filepath.Join(dest, "docs", "guide.md")); !os.IsNotExist(err) {
		t.Errorf("expected docs/guide.md to be absent before checkout, got err: %v", err)
	}

	// Can still read SHA from the bare-ish clone
	sha, err := GetCommitSHA(dest)
	if err != nil {
		t.Fatalf("GetCommitSHA after CloneNoCheckout: %v", err)
	}
	matched, _ := regexp.MatchString(`^[0-9a-f]{40}$`, sha)
	if !matched {
		t.Errorf("expected 40 hex chars, got %q", sha)
	}

	// Step 2: sparse checkout + checkout
	if err := SparseCheckoutAndCheckout(dest, []string{"docs"}); err != nil {
		t.Fatalf("SparseCheckoutAndCheckout: %v", err)
	}

	// Now docs/guide.md should exist
	if _, err := os.Stat(filepath.Join(dest, "docs", "guide.md")); err != nil {
		t.Errorf("expected docs/guide.md after checkout: %v", err)
	}

	// src/main.go still absent
	if _, err := os.Stat(filepath.Join(dest, "src", "main.go")); !os.IsNotExist(err) {
		t.Errorf("expected src/main.go to be absent, got err: %v", err)
	}
}

func TestInvalidRepoURL(t *testing.T) {
	dest := filepath.Join(t.TempDir(), "bad-clone")
	err := CloneDocFolders("https://invalid.example.com/no-such-repo.git", dest, []string{"docs"})
	if err == nil {
		t.Fatal("expected error for invalid repo URL")
	}
	// The error should mention clone
	if got := err.Error(); !contains(got, "clone") {
		t.Errorf("expected error to mention 'clone', got: %s", got)
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, sub string) bool {
	for i := 0; i <= len(s)-len(sub); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
