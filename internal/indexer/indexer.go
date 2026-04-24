package indexer

import (
	"context"
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
// When force is true, the repo is re-indexed even if the SHA hasn't changed.
//
// Cancellation: ctx is forwarded to git invocations and the markdown walker.
// On cancel, the function returns the ctx error promptly. The deferred
// os.RemoveAll(repoDir) cleans up any partial clone, so cancelling mid-run
// leaves no on-disk residue.
func (ix *Indexer) IndexRepo(ctx context.Context, cfg config.RepoConfig, force bool) (*IndexResult, error) {
	start := time.Now()
	result := &IndexResult{Repo: cfg.Alias}

	repoDir := filepath.Join(ix.tempBase, cfg.Alias)
	defer os.RemoveAll(repoDir) //nolint:errcheck

	// 1. Clone without checkout
	if err := CloneNoCheckout(ctx, cfg.URL, repoDir); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return result, ctxErr
		}
		result.Error = fmt.Errorf("clone %s: %w", cfg.Alias, err)
		result.Duration = time.Since(start)
		return result, nil
	}

	// 2. Get HEAD SHA
	sha, err := GetCommitSHA(ctx, repoDir)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return result, ctxErr
		}
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
	if !force && existing != nil && existing.CommitSHA == sha {
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
	repoID, err := ix.store.UpsertRepo(cfg.Alias, cfg.URL, string(pathsJSON), "git")
	if err != nil {
		result.Error = fmt.Errorf("upsert repo %s: %w", cfg.Alias, err)
		result.Duration = time.Since(start)
		return result, nil
	}

	// 5. Sparse checkout the specified paths
	if err := SparseCheckoutAndCheckout(ctx, repoDir, cfg.Paths); err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return result, ctxErr
		}
		result.Error = fmt.Errorf("sparse checkout %s: %w", cfg.Alias, err)
		result.Duration = time.Since(start)
		return result, nil
	}

	// 6. Walk paths and collect markdown files
	docs, err := ix.walkAndChunk(ctx, repoDir, cfg.Paths, repoID)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return result, ctxErr
		}
		result.Error = fmt.Errorf("walk %s: %w", cfg.Alias, err)
		result.Duration = time.Since(start)
		return result, nil
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

// walkAndChunk walks the given rootDir (optionally filtered by paths) and
// returns chunked markdown documents. When paths is nil or empty, the entire
// rootDir is walked. The walk callback checks ctx on every entry, so
// cancellation kicks in within at most one file iteration.
func (ix *Indexer) walkAndChunk(ctx context.Context, rootDir string, paths []string, repoID int64) ([]store.Document, error) {
	var docs []store.Document

	walkRoots := []string{rootDir}
	if len(paths) > 0 {
		walkRoots = make([]string, len(paths))
		for i, p := range paths {
			walkRoots[i] = filepath.Join(rootDir, p)
		}
	}

	for _, walkRoot := range walkRoots {
		if err := ctx.Err(); err != nil {
			return docs, err
		}
		err := filepath.WalkDir(walkRoot, func(path string, d os.DirEntry, err error) error {
			if cerr := ctx.Err(); cerr != nil {
				return cerr
			}
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

			relPath, err := filepath.Rel(rootDir, path)
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
				if len(strings.TrimSpace(c.Content)) < 10 {
					continue
				}
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
			if ctxErr := ctx.Err(); ctxErr != nil {
				return docs, ctxErr
			}
			log.Printf("warning: walk failed for %s: %v", walkRoot, err)
		}
	}

	return docs, nil
}

// IndexLocalPath indexes markdown files from a local directory. ctx is
// forwarded to the walker; on cancel the function returns the ctx error.
func (ix *Indexer) IndexLocalPath(ctx context.Context, alias, dirPath string) (*IndexResult, error) {
	start := time.Now()
	result := &IndexResult{Repo: alias}

	info, err := os.Stat(dirPath)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", dirPath, err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%s is not a directory", dirPath)
	}

	repoID, err := ix.store.UpsertRepo(alias, dirPath, "[]", "local")
	if err != nil {
		result.Error = fmt.Errorf("upsert repo %s: %w", alias, err)
		result.Duration = time.Since(start)
		return result, nil
	}

	docs, err := ix.walkAndChunk(ctx, dirPath, nil, repoID)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return result, ctxErr
		}
		result.Error = fmt.Errorf("walk %s: %w", alias, err)
		result.Duration = time.Since(start)
		return result, nil
	}

	if err := ix.store.ReplaceDocuments(repoID, docs); err != nil {
		result.Error = fmt.Errorf("replace documents %s: %w", alias, err)
		result.Duration = time.Since(start)
		return result, nil
	}

	timestamp := time.Now().UTC().Format(time.RFC3339)
	if err := ix.store.UpdateRepoIndex(repoID, "", timestamp, len(docs)); err != nil {
		result.Error = fmt.Errorf("update repo index %s: %w", alias, err)
		result.Duration = time.Since(start)
		return result, nil
	}

	result.DocsIndexed = len(docs)
	result.Duration = time.Since(start)
	return result, nil
}

// IndexAll indexes all repositories from the config and rebuilds FTS afterward.
// When force is true, all repos are re-indexed regardless of SHA. ctx is
// checked between repo iterations and forwarded to each IndexRepo call so a
// long-running batch can be aborted promptly.
func (ix *Indexer) IndexAll(ctx context.Context, cfg *config.Config, force bool) ([]IndexResult, error) {
	var results []IndexResult
	for _, repo := range cfg.Repos {
		if err := ctx.Err(); err != nil {
			return results, err
		}
		r, err := ix.IndexRepo(ctx, repo, force)
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
