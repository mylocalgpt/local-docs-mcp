package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/mylocalgpt/local-docs-mcp/internal/config"
	"github.com/mylocalgpt/local-docs-mcp/internal/indexer"
	"github.com/mylocalgpt/local-docs-mcp/internal/mcpserver"
	"github.com/mylocalgpt/local-docs-mcp/internal/search"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

func main() {
	log.SetOutput(os.Stderr)

	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: local-docs-mcp <command>\n")
		fmt.Fprintf(os.Stderr, "Commands: stdio, index, search, list, update, remove, browse\n")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "stdio":
		runServe()
	case "index":
		runIndex()
	case "search":
		runSearch()
	case "list":
		runList()
	case "update":
		runUpdate()
	case "remove":
		runRemove()
	case "browse":
		runBrowse()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		fmt.Fprintf(os.Stderr, "Commands: stdio, index, search, list, update, remove, browse\n")
		os.Exit(1)
	}
}

func runServe() {
	fs := flag.NewFlagSet("stdio", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config JSON file (optional, enables pre-configured repos)")
	dbPath := fs.String("db", "", "override database path (optional, defaults to ~/.config/local-docs-mcp/docs.db)")
	if err := fs.Parse(os.Args[2:]); err != nil {
		os.Exit(1)
	}

	var cfg *config.Config
	if *configPath != "" {
		var err error
		cfg, err = config.LoadConfig(*configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		// Check git version when a config is provided (config repos are always git)
		if err := indexer.CheckGitVersion(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v. Ensure git 2.25.0+ is installed and in PATH.\n", err)
			os.Exit(1)
		}
	} else {
		cfg = &config.Config{}
	}

	db, err := resolveDBPath(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	s := openStoreOrDie(db, false)
	defer s.Close()

	srch := search.NewSearch(s)

	ix, err := indexer.NewIndexer(s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer ix.Cleanup()

	srv := mcpserver.New(s, srch, ix, cfg)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	log.Println("MCP server started")
	if err := srv.Run(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	log.Println("MCP server stopped")
}

// resolveDBPath returns the database path from flags or the default location.
// If the old default path (~/.local/share/local-docs-mcp/docs.db) exists and the
// new default path (~/.config/local-docs-mcp/docs.db) does not, the old path is
// used and a migration hint is logged to stderr.
func resolveDBPath(dbFlag string) (string, error) {
	if dbFlag != "" {
		return dbFlag, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("cannot determine home directory: %w", err)
	}

	newPath := filepath.Join(home, ".config", "local-docs-mcp", "docs.db")
	oldPath := filepath.Join(home, ".local", "share", "local-docs-mcp", "docs.db")

	if _, err := os.Stat(oldPath); err == nil {
		if _, err := os.Stat(newPath); os.IsNotExist(err) {
			log.Printf("Using existing database at %s. Consider moving it to %s.", oldPath, newPath)
			return oldPath, nil
		}
	}

	return newPath, nil
}

// openStoreOrDie opens the store at dbPath, checking that the file exists first.
// For commands that only read (search, list, browse, remove), the DB must exist.
func openStoreOrDie(dbPath string, mustExist bool) *store.Store {
	if mustExist {
		if _, err := os.Stat(dbPath); os.IsNotExist(err) {
			fmt.Fprintf(os.Stderr, "No database found. Run 'local-docs-mcp index --config <path>' first.\n")
			os.Exit(1)
		}
	}
	s, err := store.NewStore(dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	return s
}

// isFTSError checks if an error is an FTS5 syntax error.
func isFTSError(err error) bool {
	return strings.Contains(strings.ToLower(err.Error()), "fts5")
}

func runIndex() {
	fs := flag.NewFlagSet("index", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config JSON file (required)")
	repoFilter := fs.String("repo", "", "index only this repo alias (optional)")
	dbPath := fs.String("db", "", "override database path (optional)")
	if err := fs.Parse(os.Args[2:]); err != nil {
		os.Exit(1)
	}

	if *configPath == "" {
		fmt.Fprintf(os.Stderr, "error: --config is required\n")
		os.Exit(1)
	}

	if err := indexer.CheckGitVersion(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v. Ensure git 2.25.0+ is installed and in PATH.\n", err)
		os.Exit(1)
	}

	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	db, err := resolveDBPath(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	s := openStoreOrDie(db, false)
	defer s.Close()

	ix, err := indexer.NewIndexer(s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer ix.Cleanup()

	var results []indexer.IndexResult
	var indexErr error

	if *repoFilter != "" {
		var repoCfg *config.RepoConfig
		for _, r := range cfg.Repos {
			if r.Alias == *repoFilter {
				rc := r
				repoCfg = &rc
				break
			}
		}
		if repoCfg == nil {
			fmt.Fprintf(os.Stderr, "error: repo alias %q not found in config\n", *repoFilter)
			os.Exit(1)
		}
		r, err := ix.IndexRepo(*repoCfg, false)
		if err != nil {
			indexErr = err
		} else {
			results = []indexer.IndexResult{*r}
		}
		if rebuildErr := s.RebuildFTS(); rebuildErr != nil {
			log.Printf("warning: FTS rebuild failed: %v", rebuildErr)
		}
	} else {
		results, indexErr = ix.IndexAll(cfg, false)
	}

	if indexErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", indexErr)
		os.Exit(1)
	}

	printIndexResults(results)
}

func runSearch() {
	fs := flag.NewFlagSet("search", flag.ExitOnError)
	repoAlias := fs.String("repo", "", "filter by repo alias")
	limit := fs.Int("limit", 20, "max raw results")
	tokens := fs.Int("tokens", 2000, "token budget")
	dbPath := fs.String("db", "", "override database path")
	if err := fs.Parse(os.Args[2:]); err != nil {
		os.Exit(1)
	}

	query := strings.Join(fs.Args(), " ")
	if query == "" {
		fmt.Fprintf(os.Stderr, "Usage: local-docs-mcp search <query> [--repo <alias>] [--limit N] [--tokens N] [--db <path>]\n")
		os.Exit(1)
	}

	db, err := resolveDBPath(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	s := openStoreOrDie(db, true)
	defer s.Close()

	srch := search.NewSearch(s)
	resp, err := srch.Query(search.SearchOptions{
		Query:       query,
		RepoAlias:   *repoAlias,
		Limit:       *limit,
		PageSize:    *limit,
		TokenBudget: *tokens,
	})
	if err != nil {
		if isFTSError(err) {
			fmt.Fprintf(os.Stderr, "Search error: %v. Try quoting your query or simplifying it.\n", err)
		} else {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
		}
		os.Exit(1)
	}

	if len(resp.Results) == 0 {
		fmt.Fprintf(os.Stderr, "No results found for '%s'\n", query)
		return
	}

	printSearchResults(resp.Results)
}

func runList() {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	dbPath := fs.String("db", "", "override database path")
	if err := fs.Parse(os.Args[2:]); err != nil {
		os.Exit(1)
	}

	db, err := resolveDBPath(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	s := openStoreOrDie(db, true)
	defer s.Close()

	repos, err := s.ListRepos()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if len(repos) == 0 {
		fmt.Fprintf(os.Stderr, "No repos indexed. Use add_docs or run 'local-docs-mcp index --config <path>' first.\n")
		return
	}

	fmt.Printf("%-20s %-6s %-8s %5s  %-10s %-24s %s\n", "ALIAS", "TYPE", "STATUS", "DOCS", "SIZE", "LAST INDEXED", "SHA")
	for _, r := range repos {
		sha := r.CommitSHA
		if len(sha) > 7 {
			sha = sha[:7]
		}
		status := r.Status
		if status == store.StatusError && r.StatusDetail != "" {
			status = "error"
		}
		contentSize, _ := s.RepoContentSize(r.ID)
		fmt.Printf("%-20s %-6s %-8s %5d  %-10s %-24s %s\n",
			r.Alias, r.SourceType, status, r.DocCount, formatBytesCompact(contentSize), r.IndexedAt, sha)
	}
}

// formatBytesCompact returns a compact human-readable byte size.
func formatBytesCompact(bytes int64) string {
	if bytes < 1024 {
		return fmt.Sprintf("%dB", bytes)
	}
	if bytes < 1024*1024 {
		return fmt.Sprintf("%.1fKB", float64(bytes)/1024)
	}
	return fmt.Sprintf("%.1fMB", float64(bytes)/(1024*1024))
}

func runUpdate() {
	fs := flag.NewFlagSet("update", flag.ExitOnError)
	configPath := fs.String("config", "", "path to config JSON file (optional, uses DB repos when omitted)")
	force := fs.Bool("force", false, "re-index even if SHA unchanged")
	dbPath := fs.String("db", "", "override database path")
	if err := fs.Parse(os.Args[2:]); err != nil {
		os.Exit(1)
	}

	db, err := resolveDBPath(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	s := openStoreOrDie(db, false)
	defer s.Close()

	ix, err := indexer.NewIndexer(s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer ix.Cleanup()

	aliasArg := ""
	if len(fs.Args()) > 0 {
		aliasArg = fs.Args()[0]
	}

	// Config-based update (original behavior)
	if *configPath != "" {
		if err := indexer.CheckGitVersion(); err != nil {
			fmt.Fprintf(os.Stderr, "error: %v. Ensure git 2.25.0+ is installed and in PATH.\n", err)
			os.Exit(1)
		}

		cfg, err := config.LoadConfig(*configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}

		var results []indexer.IndexResult
		var indexErr error

		if aliasArg != "" {
			var repoCfg *config.RepoConfig
			for _, r := range cfg.Repos {
				if r.Alias == aliasArg {
					rc := r
					repoCfg = &rc
					break
				}
			}
			if repoCfg == nil {
				fmt.Fprintf(os.Stderr, "Repo alias '%s' not found in config\n", aliasArg)
				os.Exit(1)
			}
			r, err := ix.IndexRepo(*repoCfg, *force)
			if err != nil {
				indexErr = err
			} else {
				results = []indexer.IndexResult{*r}
			}
			if rebuildErr := s.RebuildFTS(); rebuildErr != nil {
				log.Printf("warning: FTS rebuild failed: %v", rebuildErr)
			}
		} else {
			results, indexErr = ix.IndexAll(cfg, *force)
		}

		if indexErr != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", indexErr)
			os.Exit(1)
		}
		printIndexResults(results)
		return
	}

	// DB-driven update
	updateFromDB(s, ix, aliasArg, *force)
}

// updateFromDB re-indexes repos loaded from the database.
func updateFromDB(s *store.Store, ix *indexer.Indexer, aliasFilter string, force bool) {
	repos, err := s.ListRepos()
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	if len(repos) == 0 {
		fmt.Fprintf(os.Stderr, "No repos in database. Add docs first with 'local-docs-mcp stdio' and the add_docs tool.\n")
		os.Exit(1)
	}

	var results []indexer.IndexResult
	for _, repo := range repos {
		if aliasFilter != "" && repo.Alias != aliasFilter {
			continue
		}

		r := indexRepoByType(ix, &repo, force)
		results = append(results, *r)
	}

	if aliasFilter != "" && len(results) == 0 {
		fmt.Fprintf(os.Stderr, "Repo alias '%s' not found in database\n", aliasFilter)
		os.Exit(1)
	}

	if err := s.RebuildFTS(); err != nil {
		log.Printf("warning: FTS rebuild failed: %v", err)
	}

	printIndexResults(results)
}

// indexRepoByType indexes a repo based on its source type.
func indexRepoByType(ix *indexer.Indexer, repo *store.Repo, force bool) *indexer.IndexResult {
	if repo.SourceType == "local" {
		result, err := ix.IndexLocalPath(repo.Alias, repo.URL)
		if err != nil {
			return &indexer.IndexResult{Repo: repo.Alias, Error: err}
		}
		return result
	}

	var paths []string
	if err := json.Unmarshal([]byte(repo.Paths), &paths); err != nil {
		return &indexer.IndexResult{Repo: repo.Alias, Error: fmt.Errorf("parse paths: %w", err)}
	}

	cfg := config.RepoConfig{Alias: repo.Alias, URL: repo.URL, Paths: paths}
	result, err := ix.IndexRepo(cfg, force)
	if err != nil {
		return &indexer.IndexResult{Repo: repo.Alias, Error: err}
	}
	return result
}

func runRemove() {
	fs := flag.NewFlagSet("remove", flag.ExitOnError)
	dbPath := fs.String("db", "", "override database path")
	if err := fs.Parse(os.Args[2:]); err != nil {
		os.Exit(1)
	}

	if len(fs.Args()) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: local-docs-mcp remove <alias> [--db <path>]\n")
		os.Exit(1)
	}
	alias := fs.Args()[0]

	db, err := resolveDBPath(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	s := openStoreOrDie(db, true)
	defer s.Close()

	count, err := s.DeleteRepo(alias)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Repo '%s' not found. Run 'local-docs-mcp list' to see indexed repos.\n", alias)
		os.Exit(1)
	}

	fmt.Printf("Removed %s (%d documents)\n", alias, count)
}

func runBrowse() {
	fs := flag.NewFlagSet("browse", flag.ExitOnError)
	dbPath := fs.String("db", "", "override database path")
	if err := fs.Parse(os.Args[2:]); err != nil {
		os.Exit(1)
	}

	args := fs.Args()
	if len(args) == 0 {
		fmt.Fprintf(os.Stderr, "Usage: local-docs-mcp browse <alias> [path] [--db <path>]\n")
		os.Exit(1)
	}
	alias := args[0]

	db, err := resolveDBPath(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	s := openStoreOrDie(db, true)
	defer s.Close()

	repo, err := s.GetRepo(alias)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if repo == nil {
		fmt.Fprintf(os.Stderr, "Repo '%s' not found. Run 'local-docs-mcp list' to see indexed repos.\n", alias)
		os.Exit(1)
	}

	if len(args) < 2 {
		// List files
		files, _, err := s.BrowseFiles(repo.ID, 1, 1000)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		for _, f := range files {
			fmt.Printf("%s (%d sections)\n", f.Path, f.Sections)
		}
	} else {
		// List headings for a file
		filePath := args[1]
		headings, err := s.BrowseHeadings(repo.ID, filePath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			os.Exit(1)
		}
		if len(headings) == 0 {
			fmt.Fprintf(os.Stderr, "No headings found for '%s' in '%s'\n", filePath, alias)
			return
		}

		// Find the minimum heading level for indentation
		minLevel := headings[0].HeadingLevel
		for _, h := range headings[1:] {
			if h.HeadingLevel < minLevel {
				minLevel = h.HeadingLevel
			}
		}

		for _, h := range headings {
			indent := strings.Repeat("  ", h.HeadingLevel-minLevel)
			prefix := strings.Repeat("#", h.HeadingLevel)
			fmt.Printf("%s%s %s (%d tokens)\n", indent, prefix, h.SectionTitle, h.Tokens)
		}
	}
}

// printSearchResults formats search results grouped by document.
func printSearchResults(results []search.SearchResult) {
	// Group by (RepoAlias, Path)
	type docKey struct {
		RepoAlias string
		Path      string
	}
	type docGroup struct {
		key     docKey
		results []search.SearchResult
	}

	var groups []docGroup
	seen := make(map[docKey]int)

	for _, r := range results {
		k := docKey{r.RepoAlias, r.Path}
		if idx, ok := seen[k]; ok {
			groups[idx].results = append(groups[idx].results, r)
		} else {
			seen[k] = len(groups)
			groups = append(groups, docGroup{key: k, results: []search.SearchResult{r}})
		}
	}

	for i, g := range groups {
		if i > 0 {
			fmt.Println("---")
			fmt.Println()
		}
		fmt.Printf("## %s: %s\n", g.key.RepoAlias, g.key.Path)
		for _, r := range g.results {
			fmt.Printf("### %s\n", r.SectionTitle)
			fmt.Printf("Score: %.2f | Tokens: %d\n", r.Score, r.Tokens)
			if r.Excerpt != "" {
				fmt.Println(r.Excerpt)
			}
			fmt.Println()
		}
	}
}

// printIndexResults prints indexing results to stderr.
func printIndexResults(results []indexer.IndexResult) {
	hasErrors := false
	for _, r := range results {
		if r.Error != nil {
			fmt.Fprintf(os.Stderr, "%s: error: %v\n", r.Repo, r.Error)
			hasErrors = true
		} else if r.Skipped {
			fmt.Fprintf(os.Stderr, "%s: skipped (unchanged)\n", r.Repo)
		} else {
			fmt.Fprintf(os.Stderr, "%s: indexed %d docs in %.1fs\n", r.Repo, r.DocsIndexed, r.Duration.Seconds())
		}
	}
	if hasErrors {
		os.Exit(1)
	}
}
