# AGENTS.md

## Build

```
go build ./cmd/local-docs-mcp
```

## Test

```
go test ./...
```

## Lint

```
go vet ./...
```

## Packages

- `cmd/local-docs-mcp` - CLI entry point (flag-based subcommands)
- `internal/store` - SQLite database, FTS5, CRUD
- `internal/indexer` - Git sparse checkout, local dir walk, markdown chunking
- `internal/search` - FTS5 query, BM25 ranking, token budgeting
- `internal/mcpserver` - MCP server, tool handlers, background indexing
- `internal/config` - JSON config loading (optional)

## Docs

- `docs/design.md` - Architecture, data flow, design decisions
- `docs/cli.md` - CLI reference and standalone install

## Conventions

- Standard Go project layout, all packages under `internal/`
- MCP tools registered in `internal/mcpserver/tool_*.go` files
- Database schema managed in `internal/store/schema.go`
- CLI uses Go's `flag` package with manual subcommand dispatch, not cobra
- **Do not manually bump versions.** Versioning is handled automatically by CI/CD
