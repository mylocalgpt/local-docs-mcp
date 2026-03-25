# local-docs-mcp

Local documentation search for AI assistants, from Git repos and local directories

## What It Does

An MCP server that gives AI assistants access to locally indexed documentation. It indexes GitHub repositories (via sparse checkout) and local directories into a SQLite database with FTS5 full-text search. Token budgeting keeps results within AI context window limits.

## Install

**npm:**
```bash
npx local-docs-mcp serve
```

**Go:**
```bash
go install github.com/mylocalgpt/local-docs-mcp/cmd/local-docs-mcp@latest
```

**Binary:** Download from [GitHub releases](https://github.com/mylocalgpt/local-docs-mcp/releases).

## Quick Start

### 1. Add to your MCP client

**Claude Code** (`~/.claude.json`), **Cursor** (Settings > MCP), **Cline** (VS Code settings):
```json
{
  "mcpServers": {
    "local-docs": {
      "command": "local-docs-mcp",
      "args": ["serve"]
    }
  }
}
```

**VS Code Copilot** (`.vscode/mcp.json`):
```json
{
  "servers": {
    "local-docs": {
      "command": "local-docs-mcp",
      "args": ["serve"]
    }
  }
}
```

### 2. Use it

The AI assistant discovers and indexes documentation on the fly. For example:

1. You ask: "How does Entra Connect hybrid identity sync work?"
2. AI calls `list_repos` - no relevant docs found
3. AI researches the GitHub repo and calls `add_docs` with:
   - `url`: `"https://github.com/MicrosoftDocs/entra-docs"`
   - `paths`: `["docs/identity/hybrid/"]`
   - `alias`: `"entra-hybrid"`
4. AI calls `list_repos` to check indexing progress
5. AI calls `search_docs` with `"hybrid identity sync"` and answers with the indexed docs

No config files needed. The AI handles discovery, indexing, and search.

## MCP Tools

| Tool | Description |
|------|-------------|
| `search_docs` | Full-text search across all indexed docs (or a specific repo) |
| `list_repos` | List available documentation sources, status, and indexing progress |
| `browse_docs` | Explore doc tree structure and headings |
| `update_docs` | Re-index to pull latest changes |
| `add_docs` | Add a new git repo or local directory as a doc source |
| `remove_docs` | Remove a documentation source |

## CLI Reference

All commands support `--db <path>` to override the default database path (`~/.config/local-docs-mcp/docs.db`).

### `serve`

Start the MCP server on stdio.

```bash
local-docs-mcp serve [--config <path>] [--db <path>]
```

The `--config` flag is optional. When provided, repos from the config file are pre-seeded into the database on startup.

### `index`

Index repos from a config file.

```bash
local-docs-mcp index --config <path> [--repo <alias>] [--db <path>]
```

### `search`

Search indexed docs from the command line.

```bash
local-docs-mcp search <query> [--repo <alias>] [--limit N] [--tokens N] [--db <path>]
```

### `list`

List indexed repos with status and doc counts.

```bash
local-docs-mcp list [--db <path>]
```

### `update`

Re-index repos. Without `--config`, updates all repos from the database.

```bash
local-docs-mcp update [alias] [--config <path>] [--force] [--db <path>]
```

### `remove`

Remove a repo and all its indexed documents.

```bash
local-docs-mcp remove <alias> [--db <path>]
```

### `browse`

Navigate the doc tree. Without a path, lists files. With a path, shows headings.

```bash
local-docs-mcp browse <alias> [path] [--db <path>]
```

## Advanced: Config File

For pre-seeding repos without AI interaction, create a config file:

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
| `paths` | Subdirectory or file paths to index (sparse checkout) |
| `alias` | Unique short name for this source |

Pass it to `serve` or `index`:
```bash
local-docs-mcp serve --config ./config.json
local-docs-mcp index --config ./config.json
```

## How It Works

- **Sparse checkout** - only fetches specified paths from GitHub repos, minimizing bandwidth
- **FTS5 full-text search** - SQLite FTS5 with BM25 ranking for relevance
- **Markdown chunking** - splits docs by heading, preserving document structure
- **Token budgeting** - fits results within AI context window limits
- **Background indexing** - `add_docs` returns immediately while indexing runs async
- **Auto-refresh** - stale repos are re-indexed automatically on server startup

## AI Agent Integration

A `skills/local-docs-mcp/SKILL.md` is included for AI agent platforms that support the [agentskills.io](https://agentskills.io) specification. Copy or symlink the `skills/local-docs-mcp/` directory to your skills directory for enhanced tool usage guidance.

## Contributing

PRs welcome. Run `go test ./...` before submitting.
