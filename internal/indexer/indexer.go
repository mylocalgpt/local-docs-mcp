package indexer

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mylocalgpt/local-docs-mcp/internal/config"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

// Indexer wires together the store, config, git, and markdown components to
// clone repos, chunk their documentation, and populate the SQLite database.
type Indexer struct {
	store    *store.Store
	tempBase string
}

// IndexResult holds the outcome of indexing a single repository.
type IndexResult struct {
	Repo        string
	DocsIndexed int
	Duration    time.Duration
	Skipped     bool
	Error       error
}

// NewIndexer creates an Indexer with a temporary working directory for clones.
func NewIndexer(s *store.Store) (*Indexer, error) {
	tempBase, err := os.MkdirTemp(os.TempDir(), "local-docs-mcp-*")
	if err != nil {
		return nil, fmt.Errorf("create temp dir: %w", err)
	}
	return &Indexer{store: s, tempBase: tempBase}, nil
}

// IndexRepo runs the full indexing pipeline for a single repository config.
func (ix *Indexer) IndexRepo(cfg config.RepoConfig) (*IndexResult, error) {
	start := time.Now()
	result := &IndexResult{Repo: cfg.Alias}

	repoDir := filepath.Join(ix.tempBase, cfg.Alias)
	defer os.RemoveAll(repoDir)

	// 1. Clone without checkout
	if err := CloneNoCheckout(cfg.URL, repoDir); err != nil {
		result.Error = fmt.Errorf("clone %s: %w", cfg.Alias, err)
		result.Duration = time.Since(start)
		return result, nil
	}

	// 2. Get HEAD SHA
	sha, err := GetCommitSHA(repoDir)
	if err != nil {
		result.Error = fmt.Errorf("get sha %s: %w", cfg.Alias, err)
		result.Duration = time.Since(start)
		return result, nil
	}

	// 3. Check if repo already indexed at this SHA
	existing, err := ix.store.GetRepo(cfg.Alias)
	if err != nil {
		result.Error = fmt.Errorf("get repo %s: %w", cfg.Alias, err)
		result.Duration = time.Since(start)
		return result, nil
	}
	if existing != nil && existing.CommitSHA == sha {
		result.Skipped = true
		result.Duration = time.Since(start)
		return result, nil
	}

	// 4. Upsert repo record
	pathsJSON, err := json.Marshal(cfg.Paths)
	if err != nil {
		result.Error = fmt.Errorf("marshal paths: %w", err)
		result.Duration = time.Since(start)
		return result, nil
	}
	repoID, err := ix.store.UpsertRepo(cfg.Alias, cfg.URL, string(pathsJSON))
	if err != nil {
		result.Error = fmt.Errorf("upsert repo %s: %w", cfg.Alias, err)
		result.Duration = time.Since(start)
		return result, nil
	}

	// 5. Sparse checkout the specified paths
	if err := SparseCheckoutAndCheckout(repoDir, cfg.Paths); err != nil {
		result.Error = fmt.Errorf("sparse checkout %s: %w", cfg.Alias, err)
		result.Duration = time.Since(start)
		return result, nil
	}

	// 6. Walk paths and collect markdown files
	var docs []store.Document
	for _, p := range cfg.Paths {
		walkRoot := filepath.Join(repoDir, p)
		err := filepath.WalkDir(walkRoot, func(path string, d os.DirEntry, err error) error {
			if err != nil {
				log.Printf("warning: walk error at %s: %v", path, err)
				return nil
			}
			if d.IsDir() {
				return nil
			}
			ext := strings.ToLower(filepath.Ext(path))
			if ext != ".md" && ext != ".mdx" {
				return nil
			}

			// Compute path relative to repo root
			relPath, err := filepath.Rel(repoDir, path)
			if err != nil {
				log.Printf("warning: cannot compute relative path for %s: %v", path, err)
				return nil
			}

			data, err := os.ReadFile(path)
			if err != nil {
				log.Printf("warning: cannot read %s: %v", relPath, err)
				return nil
			}

			chunks := ProcessMarkdownFile(relPath, string(data))
			for _, c := range chunks {
				docs = append(docs, store.Document{
					RepoID:       repoID,
					Path:         relPath,
					DocTitle:     c.DocTitle,
					SectionTitle: c.SectionTitle,
					Content:      c.Content,
					Tokens:       c.Tokens,
					HeadingLevel: c.HeadingLevel,
					HasCode:      c.HasCode,
				})
			}
			return nil
		})
		if err != nil {
			log.Printf("warning: walk failed for path %s in %s: %v", p, cfg.Alias, err)
		}
	}

	// 7. Replace documents atomically
	if err := ix.store.ReplaceDocuments(repoID, docs); err != nil {
		result.Error = fmt.Errorf("replace documents %s: %w", cfg.Alias, err)
		result.Duration = time.Since(start)
		return result, nil
	}

	// 8. Update repo index metadata
	timestamp := time.Now().UTC().Format(time.RFC3339)
	if err := ix.store.UpdateRepoIndex(repoID, sha, timestamp, len(docs)); err != nil {
		result.Error = fmt.Errorf("update repo index %s: %w", cfg.Alias, err)
		result.Duration = time.Since(start)
		return result, nil
	}

	result.DocsIndexed = len(docs)
	result.Duration = time.Since(start)
	return result, nil
}

// IndexAll indexes all repositories from the config and rebuilds FTS afterward.
func (ix *Indexer) IndexAll(cfg *config.Config) ([]IndexResult, error) {
	var results []IndexResult
	for _, repo := range cfg.Repos {
		r, err := ix.IndexRepo(repo)
		if err != nil {
			results = append(results, IndexResult{
				Repo:  repo.Alias,
				Error: err,
			})
			continue
		}
		results = append(results, *r)
	}

	// Rebuild FTS as a safety net
	if err := ix.store.RebuildFTS(); err != nil {
		return results, fmt.Errorf("rebuild fts: %w", err)
	}

	return results, nil
}

// Cleanup removes the temporary directory used for clones.
func (ix *Indexer) Cleanup() error {
	return os.RemoveAll(ix.tempBase)
}
