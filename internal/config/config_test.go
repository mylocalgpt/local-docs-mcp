package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTestConfig(t *testing.T, dir, content string) string {
	t.Helper()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestLoadConfig_Valid(t *testing.T) {
	dir := t.TempDir()
	path := writeTestConfig(t, dir, `{
		"repos": [
			{"url": "https://github.com/org/repo1", "paths": ["docs/"], "alias": "repo1"},
			{"url": "https://github.com/org/repo2", "paths": ["README.md", "guide/"], "alias": "repo2"}
		]
	}`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Repos) != 2 {
		t.Fatalf("expected 2 repos, got %d", len(cfg.Repos))
	}
	if cfg.Repos[0].Alias != "repo1" {
		t.Errorf("expected alias repo1, got %s", cfg.Repos[0].Alias)
	}
	if cfg.Repos[1].URL != "https://github.com/org/repo2" {
		t.Errorf("expected repo2 URL, got %s", cfg.Repos[1].URL)
	}
	if len(cfg.Repos[1].Paths) != 2 {
		t.Errorf("expected 2 paths for repo2, got %d", len(cfg.Repos[1].Paths))
	}
}

func TestLoadConfig_MissingURL(t *testing.T) {
	dir := t.TempDir()
	path := writeTestConfig(t, dir, `{
		"repos": [{"url": "", "paths": ["docs/"], "alias": "myrepo"}]
	}`)

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for missing URL")
	}
	if !strings.Contains(err.Error(), "missing url") {
		t.Errorf("expected error about missing url, got: %v", err)
	}
}

func TestLoadConfig_MissingPaths(t *testing.T) {
	dir := t.TempDir()
	path := writeTestConfig(t, dir, `{
		"repos": [{"url": "https://github.com/org/repo", "paths": [], "alias": "myrepo"}]
	}`)

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for missing paths")
	}
	if !strings.Contains(err.Error(), "missing paths") {
		t.Errorf("expected error about missing paths, got: %v", err)
	}
}

func TestLoadConfig_MissingAlias(t *testing.T) {
	dir := t.TempDir()
	path := writeTestConfig(t, dir, `{
		"repos": [{"url": "https://github.com/org/repo", "paths": ["docs/"], "alias": ""}]
	}`)

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for missing alias")
	}
	if !strings.Contains(err.Error(), "missing alias") {
		t.Errorf("expected error about missing alias, got: %v", err)
	}
}

func TestLoadConfig_EmptyRepos(t *testing.T) {
	dir := t.TempDir()
	path := writeTestConfig(t, dir, `{"repos": []}`)

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for empty repos")
	}
	if !strings.Contains(err.Error(), "repos must not be empty") {
		t.Errorf("expected error about empty repos, got: %v", err)
	}
}

func TestLoadConfig_DuplicateAliases(t *testing.T) {
	dir := t.TempDir()
	path := writeTestConfig(t, dir, `{
		"repos": [
			{"url": "https://github.com/org/repo1", "paths": ["docs/"], "alias": "samename"},
			{"url": "https://github.com/org/repo2", "paths": ["docs/"], "alias": "samename"}
		]
	}`)

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for duplicate aliases")
	}
	if !strings.Contains(err.Error(), "duplicate alias") || !strings.Contains(err.Error(), "samename") {
		t.Errorf("expected error mentioning duplicate alias 'samename', got: %v", err)
	}
}

func TestLoadConfig_SchemaFieldPreserved(t *testing.T) {
	dir := t.TempDir()
	path := writeTestConfig(t, dir, `{
		"$schema": "https://example.com/schema.json",
		"repos": [
			{"url": "https://github.com/org/repo", "paths": ["docs/"], "alias": "myrepo"}
		]
	}`)

	cfg, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Schema != "https://example.com/schema.json" {
		t.Errorf("expected schema to be preserved, got %q", cfg.Schema)
	}
}

func TestLoadConfig_RelativePath(t *testing.T) {
	dir := t.TempDir()
	writeTestConfig(t, dir, `{
		"repos": [
			{"url": "https://github.com/org/repo", "paths": ["docs/"], "alias": "myrepo"}
		]
	}`)

	// Save and restore the working directory.
	origDir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origDir) })

	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}

	cfg, err := LoadConfig("config.json")
	if err != nil {
		t.Fatalf("unexpected error loading with relative path: %v", err)
	}
	if len(cfg.Repos) != 1 {
		t.Fatalf("expected 1 repo, got %d", len(cfg.Repos))
	}
}

func TestLoadConfig_MalformedJSON(t *testing.T) {
	dir := t.TempDir()
	path := writeTestConfig(t, dir, `{"repos": [broken}`)

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for malformed JSON")
	}
	if !strings.Contains(err.Error(), "parsing config JSON") {
		t.Errorf("expected descriptive JSON parse error, got: %v", err)
	}
}

func TestLoadConfig_NonexistentFile(t *testing.T) {
	_, err := LoadConfig("/tmp/nonexistent-config-file-abc123.json")
	if err == nil {
		t.Fatal("expected error for nonexistent file")
	}
	if !strings.Contains(err.Error(), "reading config file") {
		t.Errorf("expected descriptive file read error, got: %v", err)
	}
}

func TestLoadConfig_EmptyPathElement(t *testing.T) {
	dir := t.TempDir()
	path := writeTestConfig(t, dir, `{
		"repos": [{"url": "https://github.com/org/repo", "paths": ["docs/", ""], "alias": "myrepo"}]
	}`)

	_, err := LoadConfig(path)
	if err == nil {
		t.Fatal("expected error for empty path element")
	}
	if !strings.Contains(err.Error(), "path at index 1 is empty") {
		t.Errorf("expected error about empty path, got: %v", err)
	}
}
