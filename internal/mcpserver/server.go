package mcpserver

import (
	"context"
	"encoding/json"
	"errors"
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

// indexerIface is the subset of *indexer.Indexer that the server uses to run
// indexing jobs. Extracted as an interface so tests can substitute a fake
// (e.g. BlockingIndexer) without driving real git operations.
type indexerIface interface {
	IndexRepo(cfg config.RepoConfig, force bool) (*indexer.IndexResult, error)
	IndexLocalPath(alias, path string) (*indexer.IndexResult, error)
}

// Server wraps the MCP protocol server and the application dependencies
// needed to handle tool calls.
type Server struct {
	server  *mcp.Server
	store   *store.Store
	search  *search.Search
	indexer indexerIface
	config  *config.Config
	queue   *indexQueue
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
		server: mcpSrv,
		store:  s,
		search: srch,
		// Storing nil through the interface variable keeps the original
		// "indexer not available" check working: ix == nil propagates as a
		// typed-nil interface, so handlers explicitly compare to nil via the
		// concrete type stored on the field. To avoid the typed-nil pitfall
		// we only assign when ix is non-nil.
		config: cfg,
		queue:  newIndexQueue(s),
	}
	if ix != nil {
		srv.indexer = ix
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
// stale repos on startup and a single worker goroutine drains the queue.
func (s *Server) Run(ctx context.Context) error {
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.queue.worker(ctx, s.runJob)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		s.autoRefresh(ctx)
	}()

	err := s.server.Run(ctx, &mcp.StdioTransport{})

	// Wait for worker + auto-refresh to finish so Cleanup in the caller is safe.
	wg.Wait()

	// Drain anything that was still queued at shutdown and revert each job's
	// DB status to its prior value so a restart finds a consistent picture.
	for _, j := range s.queue.drainPending() {
		if j.RepoID == 0 {
			continue
		}
		prior := j.PriorStatus
		if prior == "" {
			prior = store.StatusReady
		}
		if dbErr := s.store.UpdateRepoStatus(j.RepoID, prior, ""); dbErr != nil {
			log.Printf("queue: revert status for %s on shutdown: %v", j.Alias, dbErr)
		}
	}

	return err
}

// runJob is the indexQueue worker callback. It performs a single indexing job
// synchronously and returns its outcome. DB status transitions and per-job
// log lines live here so all queued work funnels through one place.
func (s *Server) runJob(j *Job) JobResult {
	if j.RepoID != 0 {
		if err := s.store.UpdateRepoStatus(j.RepoID, store.StatusIndexing, kindDetail(j.Kind)); err != nil {
			log.Printf("queue: %s set indexing status: %v", j.Alias, err)
		}
	}

	var (
		result *indexer.IndexResult
		err    error
	)
	switch j.Kind {
	case jobKindLocal:
		result, err = s.indexer.IndexLocalPath(j.Alias, j.URL)
	default:
		cfg := config.RepoConfig{Alias: j.Alias, URL: j.URL, Paths: j.Paths}
		result, err = s.indexer.IndexRepo(cfg, j.Force)
	}

	// Combine Go error and indexer-reported error into a single failure path.
	jobErr := err
	if jobErr == nil && result != nil && result.Error != nil {
		jobErr = result.Error
	}

	if jobErr != nil {
		log.Printf("queue: %s failed: %v", j.Alias, jobErr)
		if j.RepoID != 0 {
			if dbErr := s.store.UpdateRepoStatus(j.RepoID, store.StatusError, jobErr.Error()); dbErr != nil {
				log.Printf("queue: %s set error status: %v", j.Alias, dbErr)
			}
		}
		return JobResult{IndexResult: result, Err: jobErr}
	}

	// Success. Rebuild FTS so subsequent searches see the new docs.
	rebuildErr := s.store.RebuildFTS()
	if rebuildErr != nil {
		log.Printf("queue: %s rebuild fts failed: %v", j.Alias, rebuildErr)
	}
	if j.RepoID != 0 {
		status := store.StatusReady
		detail := ""
		if rebuildErr != nil {
			status = store.StatusError
			detail = "fts rebuild failed: " + rebuildErr.Error()
		}
		if dbErr := s.store.UpdateRepoStatus(j.RepoID, status, detail); dbErr != nil {
			log.Printf("queue: %s set %s status: %v", j.Alias, status, dbErr)
		}
	}

	if result != nil {
		log.Printf("queue: %s indexed %d docs in %s", j.Alias, result.DocsIndexed, result.Duration.Round(time.Millisecond))
	}
	if rebuildErr != nil {
		return JobResult{IndexResult: result, Err: rebuildErr}
	}
	return JobResult{IndexResult: result, Err: nil}
}

// kindDetail returns the status_detail string used while a job of this Kind
// is actively running. The wording matches the pre-queue handler messages so
// list_repos output stays stable.
func kindDetail(k JobKind) string {
	if k == jobKindLocal {
		return "scanning directory"
	}
	return "clone started"
}

// kindFromSourceType maps a store-level source_type to a queue JobKind.
func kindFromSourceType(sourceType string) JobKind {
	if sourceType == "local" {
		return jobKindLocal
	}
	return jobKindGit
}

// formatQueuedDetail builds the status_detail value written when a job is in
// the queued state. Position is the 1-based queue position returned from
// indexQueue.enqueue.
func formatQueuedDetail(position int) string {
	return fmt.Sprintf("queued, ~%d ahead", position)
}

// autoRefresh checks repos for staleness and enqueues background re-index
// jobs. Reads the repo list from the database. If a config is provided,
// ensures config repos are inserted into the DB on first run.
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

	for i := range repos {
		if ctx.Err() != nil {
			return
		}

		repo := &repos[i]

		// Skip repos that are already in flight or already waiting.
		if repo.Status == store.StatusIndexing || repo.Status == store.StatusQueued {
			continue
		}

		stale := false
		var reason string

		if repo.SourceType == "local" {
			// Local sources are always re-indexed on startup.
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

		var paths []string
		if repo.SourceType != "local" {
			if jsonErr := json.Unmarshal([]byte(repo.Paths), &paths); jsonErr != nil {
				log.Printf("auto-refresh: %s invalid paths JSON: %v", repo.Alias, jsonErr)
				continue
			}
		}

		job := &Job{
			Alias:       repo.Alias,
			Kind:        kindFromSourceType(repo.SourceType),
			URL:         repo.URL,
			Paths:       paths,
			Force:       false,
			Priority:    priorityBackground,
			PriorStatus: repo.Status,
			RepoID:      repo.ID,
		}

		_, position, _, _, enqErr := s.queue.enqueue(job)
		if enqErr != nil {
			if errors.Is(enqErr, errQueueFull) {
				log.Printf("auto-refresh: %s skipped, queue full", repo.Alias)
				continue
			}
			log.Printf("auto-refresh: %s enqueue failed: %v", repo.Alias, enqErr)
			continue
		}

		log.Printf("auto-refresh: queued %s (%s)", repo.Alias, reason)
		if dbErr := s.store.UpdateRepoStatus(repo.ID, store.StatusQueued, formatQueuedDetail(position)); dbErr != nil {
			log.Printf("auto-refresh: %s set queued status: %v", repo.Alias, dbErr)
		}
	}
}
