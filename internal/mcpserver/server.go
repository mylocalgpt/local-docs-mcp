package mcpserver

import (
	"context"
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
			Instructions: "local-docs-mcp provides search over locally cached documentation from git repos.\n\nWorkflow:\n1. Call list_repos to see what documentation is indexed\n2. Call search_docs with your query to find relevant docs\n3. Use browse_docs to explore the doc tree when search isn't specific enough\n4. Call update_docs if documentation seems stale or outdated\n\nSearch supports FTS5 syntax: \"exact phrase\", term1 AND term2, prefix*",
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

// autoRefresh checks each configured repo for staleness (never indexed or
// indexed more than 24 hours ago) and re-indexes in the background.
func (s *Server) autoRefresh(ctx context.Context) {
	if s.indexer == nil {
		return
	}

	var refreshed bool
	for _, repo := range s.config.Repos {
		if ctx.Err() != nil {
			return
		}

		existing, err := s.store.GetRepo(repo.Alias)
		if err != nil {
			log.Printf("auto-refresh: error checking %s: %v", repo.Alias, err)
			continue
		}

		stale := false
		var reason string
		if existing == nil || existing.IndexedAt == "" {
			stale = true
			reason = "never indexed"
		} else {
			t, err := time.Parse(time.RFC3339, existing.IndexedAt)
			if err != nil {
				stale = true
				reason = "unknown age"
			} else if time.Since(t) > 24*time.Hour {
				stale = true
				reason = fmt.Sprintf("last indexed %s", existing.IndexedAt)
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

		repoCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
		_ = repoCtx // timeout context for future use if IndexRepo accepts context

		result, err := s.indexer.IndexRepo(repo, false)
		cancel()
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
