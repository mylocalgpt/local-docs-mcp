# local-docs-mcp

Local documentation search for AI assistants, from Git repos and local directories

## What It Does

An MCP server that gives AI assistants access to locally indexed documentation. It indexes GitHub repositories (via sparse checkout) and local directories into a SQLite database with FTS5 full-text search. Token budgeting keeps results within AI context window limits.

## Quick Start

Add to your MCP client:

**Claude Code** (`~/.claude.json`), **Cursor** (Settings > MCP), **Cline** (VS Code settings):
```json
{
  "mcpServers": {
    "local-docs": {
      "command": "npx",
      "args": ["-y", "local-docs-mcp@latest", "stdio"]
    }
  }
}
```

**VS Code Copilot** (`.vscode/mcp.json`):
```json
{
  "servers": {
    "local-docs": {
      "command": "npx",
      "args": ["-y", "local-docs-mcp@latest", "stdio"]
    }
  }
}
```

### Example

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

## How It Works

- **Sparse checkout** - only fetches specified paths from GitHub repos, minimizing bandwidth
- **FTS5 full-text search** - SQLite FTS5 with BM25 ranking for relevance
- **Markdown chunking** - splits docs by heading, preserving document structure
- **Token budgeting** - fits results within AI context window limits
- **Background indexing** - `add_docs` returns immediately while indexing runs async
- **Auto-refresh** - stale repos are re-indexed automatically on server startup

## More

- [CLI reference and standalone install](docs/cli.md)
- [Architecture and design](docs/design.md)

## AI Agent Integration

A `skills/local-docs-mcp/SKILL.md` is included for AI agent platforms that support the [agentskills.io](https://agentskills.io) specification. Copy or symlink the `skills/local-docs-mcp/` directory to your skills directory for enhanced tool usage guidance.

## Contributing

PRs welcome. Run `go test ./...` before submitting.
