package mcpserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"runtime/debug"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mylocalgpt/local-docs-mcp/internal/config"
	"github.com/mylocalgpt/local-docs-mcp/internal/indexer"
	"github.com/mylocalgpt/local-docs-mcp/internal/search"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

var Version = "dev"

// Server wraps the MCP protocol server and the application dependencies
// needed to handle tool calls.
type Server struct {
	server  *mcp.Server
	store   *store.Store
	search  *search.Search
	indexer *indexer.Indexer
	config  *config.Config
	indexMu sync.Mutex
}

// New creates a Server backed by the given store, search engine, indexer, and
// config. The MCP server name is "local-docs-mcp" and the version comes from
// build info, falling back to "dev".
func New(s *store.Store, srch *search.Search, ix *indexer.Indexer, cfg *config.Config) *Server {
	version := Version
	if version == "dev" {
		if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
			version = bi.Main.Version
		}
	}

	mcpSrv := mcp.NewServer(
		&mcp.Implementation{
			Name:    "local-docs-mcp",
			Version: version,
		},
		&mcp.ServerOptions{
			Instructions: `local-docs-mcp provides search over locally indexed documentation from git repos and local directories.

Workflow:
1. Call list_repos to discover what documentation is already indexed.
2. Call search_docs to find answers. Searches all indexed repos by default; use the repo parameter to narrow scope.
3. Use browse_docs to explore the doc tree when you need to understand structure rather than search for a term.
4. Call update_docs to refresh stale documentation (pulls latest for git, re-scans local dirs).

Adding new documentation:
- If the user asks about a library with no indexed docs, or search returns no results, proactively use add_docs.
- For git repos: research the correct GitHub URL and identify the specific subdirectory paths containing documentation (e.g. ["docs/", "guides/"]) before calling add_docs.
- For local directories: ask the user for the absolute filesystem path.
- Indexing runs in the background. Call list_repos to check progress.

Search syntax (FTS5): "exact phrase", term1 AND term2, prefix*`,
		},
	)

	srv := &Server{
		server:  mcpSrv,
		store:   s,
		search:  srch,
		indexer: ix,
		config:  cfg,
	}

	srv.registerSearchDocsTool()
	srv.registerListReposTool()
	srv.registerBrowseDocsTool()
	srv.registerUpdateDocsTool()
	srv.registerAddDocsTool()
	srv.registerRemoveDocsTool()

	return srv
}

// MCPServer returns the underlying mcp.Server. This is useful for tests that
// need to connect via in-memory transports.
func (s *Server) MCPServer() *mcp.Server {
	return s.server
}

// Run starts the MCP server on stdio transport and blocks until the client
// disconnects or the context is cancelled. A background goroutine refreshes
// stale repos on startup.
func (s *Server) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		s.autoRefresh(ctx)
	}()

	err := s.server.Run(ctx, &mcp.StdioTransport{})

	// Wait for auto-refresh to finish so Cleanup in the caller is safe.
	wg.Wait()
	return err
}

// autoRefresh checks repos for staleness and re-indexes in the background.
// Reads the repo list from the database. If a config is provided, ensures
// config repos are inserted into the DB on first run.
func (s *Server) autoRefresh(ctx context.Context) {
	if s.indexer == nil {
		return
	}

	// Seed config repos into DB if they don't exist yet.
	if s.config != nil {
		for _, cfgRepo := range s.config.Repos {
			existing, err := s.store.GetRepo(cfgRepo.Alias)
			if err != nil {
				log.Printf("auto-refresh: error checking config repo %s: %v", cfgRepo.Alias, err)
				continue
			}
			if existing == nil {
				pathsJSON, _ := json.Marshal(cfgRepo.Paths)
				if _, err := s.store.UpsertRepo(cfgRepo.Alias, cfgRepo.URL, string(pathsJSON), "git"); err != nil {
					log.Printf("auto-refresh: error seeding config repo %s: %v", cfgRepo.Alias, err)
				}
			}
		}
	}

	repos, err := s.store.ListRepos()
	if err != nil {
		log.Printf("auto-refresh: error listing repos: %v", err)
		return
	}

	var refreshed bool
	for i := range repos {
		if ctx.Err() != nil {
			return
		}

		repo := &repos[i]

		// Skip repos currently being indexed
		if repo.Status == store.StatusIndexing {
			continue
		}

		stale := false
		var reason string

		if repo.SourceType == "local" {
			// Local sources are always re-indexed
			stale = true
			reason = "local source"
		} else if repo.IndexedAt == "" {
			stale = true
			reason = "never indexed"
		} else {
			t, err := time.Parse(time.RFC3339, repo.IndexedAt)
			if err != nil {
				stale = true
				reason = "unknown age"
			} else if time.Since(t) > 24*time.Hour {
				stale = true
				reason = fmt.Sprintf("last indexed %s", repo.IndexedAt)
			}
		}

		if !stale {
			continue
		}

		if !s.indexMu.TryLock() {
			log.Printf("auto-refresh: skipping %s, indexing already in progress", repo.Alias)
			continue
		}

		log.Printf("auto-refresh: re-indexing %s (%s)", repo.Alias, reason)

		var result *indexer.IndexResult
		if repo.SourceType == "local" {
			result, err = s.indexer.IndexLocalPath(repo.Alias, repo.URL)
		} else {
			var paths []string
			if jsonErr := json.Unmarshal([]byte(repo.Paths), &paths); jsonErr != nil {
				log.Printf("auto-refresh: %s invalid paths JSON: %v", repo.Alias, jsonErr)
				s.indexMu.Unlock()
				continue
			}
			cfg := config.RepoConfig{Alias: repo.Alias, URL: repo.URL, Paths: paths}
			result, err = s.indexer.IndexRepo(cfg, false)
		}
		s.indexMu.Unlock()

		if err != nil {
			log.Printf("auto-refresh: %s failed: %v", repo.Alias, err)
			continue
		}
		if result.Error != nil {
			log.Printf("auto-refresh: %s error: %v", repo.Alias, result.Error)
			continue
		}
		if result.Skipped {
			log.Printf("auto-refresh: %s skipped (unchanged)", repo.Alias)
		} else {
			log.Printf("auto-refresh: %s indexed %d docs in %s", repo.Alias, result.DocsIndexed, result.Duration.Round(time.Millisecond))
			refreshed = true
		}
	}

	if refreshed {
		if err := s.store.RebuildFTS(); err != nil {
			log.Printf("auto-refresh: rebuild fts failed: %v", err)
		}
	}
}
