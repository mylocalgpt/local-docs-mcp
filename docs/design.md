# Architecture

## Overview

local-docs-mcp is an MCP server that provides local documentation search for AI assistants. It indexes GitHub repositories (via sparse checkout) and local filesystem directories into a SQLite database with FTS5 full-text search. The server runs over stdio and exposes six tools that AI agents use to discover, search, browse, and manage documentation sources. It requires zero configuration - the AI agent drives indexing through the `add_docs` tool - though an optional config file can pre-seed repos.

## Data Flow

```
Sources                  Indexer              Store               Search            MCP Tools
+-----------------+    +---------------+   +--------------+   +-------------+   +-------------+
| GitHub repos    |--->| Sparse        |-->| SQLite DB    |-->| FTS5 query  |-->| search_docs |
| (sparse clone)  |    | checkout +    |   | - repos      |   | BM25 rank   |   | list_repos  |
+-----------------+    | markdown      |   | - documents  |   | Token budget|   | browse_docs |
| Local dirs      |--->| chunker       |   | - docs_fts   |   +-------------+   | update_docs |
| (walk + read)   |    +---------------+   +--------------+                     | add_docs    |
+-----------------+                                                             | remove_docs |
                                                                                +-------------+
```

**Indexing pipeline:**

1. Source acquisition: git sparse checkout for remote repos, filesystem walk for local directories.
2. File reading: markdown files are read from the checked-out tree or local path.
3. Chunking: each markdown file is split by headings. Each chunk records the doc title, section title, heading level, content, and an estimated token count.
4. Storage: chunks are inserted into the `documents` table. The FTS5 virtual table (`docs_fts`) is rebuilt to include new content.
5. Status tracking: the `repos` table tracks source metadata, indexing status, and timestamps.

**Query pipeline:**

1. The AI agent calls `search_docs` with a query string.
2. The query runs against the FTS5 index with BM25 ranking.
3. Results are filtered by token budget - sections are returned in relevance order until the budget is exhausted.
4. Formatted results are returned to the AI agent.

## Package Responsibilities

### `cmd/local-docs-mcp`

CLI entry point. Uses Go's standard `flag` package with manual `switch os.Args[1]` subcommand dispatch. Supports seven commands: stdio, index, search, list, update, remove, browse. Initializes the store, indexer, search engine, and MCP server. Each command parses its own flag set.

### `internal/store`

SQLite database layer. Manages the schema (repos table, documents table, docs_fts virtual table). Provides CRUD operations for repos and documents, repo status tracking (indexing, ready, error), FTS5 rebuild, content size queries, and browse queries (file listing, heading listing). The database path defaults to `~/.config/local-docs-mcp/docs.db`.

### `internal/indexer`

Handles source acquisition and content extraction. For git repos, performs sparse checkout using the system `git` binary (requires git 2.25.0+). For local directories, walks the filesystem and reads markdown files. The markdown chunker splits files by heading hierarchy, producing sections with title, content, heading level, and token count. Also handles path merging for incremental additions to existing git sources.

### `internal/search`

FTS5 query execution with BM25 ranking. Implements token budgeting to fit results within AI context window limits. Sanitizes queries to handle common FTS5 syntax issues. Returns ranked results with repo alias, path, section title, content, score, and token count.

### `internal/mcpserver`

MCP protocol server over stdio. Registers six tools (search_docs, list_repos, browse_docs, update_docs, add_docs, remove_docs). Each tool is defined in its own `tool_*.go` file. Indexing requests from `add_docs`, `update_docs`, and auto-refresh are funneled through an in-memory job queue drained by a single worker goroutine, with a user lane that takes priority over background work. Auto-refresh checks repo staleness on server startup and enqueues stale repos as needed. Config repo seeding inserts config-defined repos into the database on first run.

### `internal/config`

JSON config file loading and validation. Defines the `RepoConfig` struct (alias, URL, paths) and loads the config from disk. Entirely optional - the server runs without any config file.

## Key Design Decisions

### SQLite FTS5

Single-file database with zero external dependencies. BM25 ranking is built in, providing relevance-scored search without additional libraries. Reliable for single-process use. The FTS5 virtual table is rebuilt after indexing rather than maintained incrementally, which simplifies the write path.

### Sparse Checkout

Minimizes bandwidth and disk usage for large repositories. Only the specified doc paths are fetched, not the entire repo. Uses the system `git` binary with `--sparse` and `--filter=blob:none` flags. This means large monorepos like MicrosoftDocs can be indexed by targeting just the relevant subdirectories.

### Background Indexing

The `add_docs` tool returns immediately while indexing runs asynchronously. A single worker goroutine drains an in-memory job queue, so the AI conversation keeps flowing and the agent can check progress via `list_repos`. Status is tracked in the `repos` table, with `queued` joining the existing `indexing`, `ready`, and `error` values (no schema migration).

- **Two priority lanes:** user calls (`add_docs`, `update_docs`) drain before background auto-refresh, so startup refresh never blocks an interactive request.
- **Coalescing:** a duplicate enqueue for the same repo alias merges into the existing pending job - paths are unioned and the force flag upgrades from false to true.
- **Capacity:** each lane holds up to 100 pending jobs; over capacity the caller gets a clean error rather than an unbounded wait.
- **In-memory only:** on restart, pending jobs are dropped and repos revert to their pre-queue status; the next auto-refresh tick picks them up again.
- **Shutdown cancellation:** the worker's context is propagated through the indexer (including `git` subprocesses), so server shutdown interrupts an in-flight job promptly. The job's prior status is restored with `status_detail = "cancelled at shutdown"` rather than recorded as an error, so the next auto-refresh re-runs it cleanly.

### Heading-Based Chunking

Markdown files are split at heading boundaries rather than by fixed token count. This preserves document structure - each chunk has a clear context (doc title, section title, heading level). Token counts per section enable the search layer to budget context window usage at section granularity, returning complete sections rather than arbitrary fragments.

### Auto-Refresh

On server startup, all repos are checked for staleness. Git repos older than 24 hours are re-indexed (skipped if the commit SHA is unchanged). Local directories are always re-scanned. This keeps documentation current without manual intervention.

## Database Schema

Three tables:

- **repos**: source metadata (alias, URL, paths, source type, commit SHA, status, timestamps)
- **documents**: indexed content (repo ID, path, doc title, section title, heading level, content, tokens)
- **docs_fts**: FTS5 virtual table over documents (content, doc title, section title) for full-text search

## MCP Protocol

The server communicates over stdio using the MCP protocol. It declares an `Instructions` string that teaches AI agents the recommended workflow (list, search, browse, add). Each tool has a `Description` field that tells agents when and how to use it. The server name is `local-docs-mcp` and the version is injected at build time.
