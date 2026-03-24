# local-docs-mcp

MCP server that indexes documentation from GitHub repos for fast local search.

## Install

**npm:**
```bash
npx local-docs-mcp serve --config ./config.json
```

**Go:**
```bash
go install github.com/mylocalgpt/local-docs-mcp/cmd/local-docs-mcp@latest
```

**Binary:** Download from [GitHub releases](https://github.com/mylocalgpt/local-docs-mcp/releases).

## Quick Start

1. Create a config file:
```json
{
  "$schema": "https://raw.githubusercontent.com/mylocalgpt/local-docs-mcp/main/schema.json",
  "repos": [
    {
      "url": "https://github.com/MicrosoftDocs/entra-docs",
      "paths": ["docs/identity/hybrid/"],
      "alias": "entra-hybrid"
    }
  ]
}
```

2. Index the repos:
```bash
local-docs-mcp index --config ./config.json
```

3. Start the MCP server:
```bash
local-docs-mcp serve --config ./config.json
```

## Config File Format

```json
{
  "$schema": "https://raw.githubusercontent.com/mylocalgpt/local-docs-mcp/main/schema.json",
  "repos": [
    {
      "url": "https://github.com/owner/repo",
      "paths": ["docs/", "README.md"],
      "alias": "my-docs"
    }
  ]
}
```

| Field | Description |
|-------|-------------|
| `url` | GitHub repository URL |
| `paths` | Doc folder/file paths within the repo (sparse checkout) |
| `alias` | Short name for this repo (must be unique) |

## MCP Client Setup

**Claude Code** (`~/.claude.json`):
```json
{
  "mcpServers": {
    "local-docs": {
      "command": "local-docs-mcp",
      "args": ["serve", "--config", "/path/to/config.json"]
    }
  }
}
```

**Cursor** (Settings → MCP):
```json
{
  "mcpServers": {
    "local-docs": {
      "command": "local-docs-mcp",
      "args": ["serve", "--config", "/path/to/config.json"]
    }
  }
}
```

**Cline** (VS Code settings):
```json
{
  "mcpServers": {
    "local-docs": {
      "command": "local-docs-mcp",
      "args": ["serve", "--config", "/path/to/config.json"]
    }
  }
}
```

## CLI Reference

**Note:** CLI commands are separate from MCP tools. Use `<command> --help` for per-command help.

### `serve`
Start the MCP server.
```bash
local-docs-mcp serve --config <path> [--db <path>]
```

### `index`
Index repos from config.
```bash
local-docs-mcp index --config <path> [--repo <alias>]
```

### `search`
Search indexed docs (CLI, not MCP).
```bash
local-docs-mcp search <query> [--repo <alias>] [--limit N] [--tokens N]
```

### `list`
List indexed repos.
```bash
local-docs-mcp list
```

### `update`
Re-index from git repos.
```bash
local-docs-mcp update --config <path> [alias] [--force]
```

### `remove`
Remove a repo from the index.
```bash
local-docs-mcp remove <alias>
```

### `browse`
Navigate doc tree.
```bash
local-docs-mcp browse <alias> [path]
```

## MCP Tools

| Tool | Description |
|------|-------------|
| `search_docs` | Full-text search with BM25 ranking |
| `list_repos` | Show indexed repos and status |
| `browse_docs` | Navigate doc tree structure |
| `update_docs` | Re-index from git repos |

## How It Works

- **Sparse checkout:** Only fetches specified paths from GitHub repos
- **FTS5 full-text search:** SQLite FTS5 with BM25 ranking
- **Token budgeting:** Respects context limits for AI assistants

## Contributing

PRs welcome. Run `go test ./...` before submitting.
