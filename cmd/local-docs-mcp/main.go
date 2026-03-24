package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/mylocalgpt/local-docs-mcp/internal/config"
	"github.com/mylocalgpt/local-docs-mcp/internal/indexer"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

func main() {
	log.SetOutput(os.Stderr)

	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "Usage: local-docs-mcp <command>\n")
		fmt.Fprintf(os.Stderr, "Commands: index\n")
		os.Exit(1)
	}

	switch os.Args[1] {
	case "index":
		runIndex()
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n", os.Args[1])
		os.Exit(1)
	}
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

	// Check git version early
	if err := indexer.CheckGitVersion(); err != nil {
		fmt.Fprintf(os.Stderr, "error: %v. Ensure git 2.25.0+ is installed and in PATH.\n", err)
		os.Exit(1)
	}

	// Load config
	cfg, err := config.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}

	// Determine DB path
	db := *dbPath
	if db == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			fmt.Fprintf(os.Stderr, "error: cannot determine home directory: %v\n", err)
			os.Exit(1)
		}
		db = filepath.Join(home, ".local", "share", "local-docs-mcp", "docs.db")
	}

	// Open store
	s, err := store.NewStore(db)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer s.Close()

	// Create indexer
	ix, err := indexer.NewIndexer(s)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	defer ix.Cleanup()

	var results []indexer.IndexResult
	var indexErr error

	if *repoFilter != "" {
		// Find the matching repo config
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
		r, err := ix.IndexRepo(*repoCfg)
		if err != nil {
			indexErr = err
		} else {
			results = []indexer.IndexResult{*r}
		}
		// Rebuild FTS for single repo
		if rebuildErr := s.RebuildFTS(); rebuildErr != nil {
			log.Printf("warning: FTS rebuild failed: %v", rebuildErr)
		}
	} else {
		results, indexErr = ix.IndexAll(cfg)
	}

	if indexErr != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", indexErr)
		os.Exit(1)
	}

	// Print results to stderr (stdout reserved for future MCP)
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
