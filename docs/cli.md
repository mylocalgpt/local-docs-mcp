# CLI Reference

For standalone usage, install via Go or download a [binary release](https://github.com/mylocalgpt/local-docs-mcp/releases):

```bash
go install github.com/mylocalgpt/local-docs-mcp/cmd/local-docs-mcp@latest
```

All commands support `--db <path>` to override the default database path (`~/.config/local-docs-mcp/docs.db`).

## `stdio`

Start the MCP server on stdio.

```bash
local-docs-mcp stdio [--config <path>] [--db <path>]
```

The `--config` flag is optional. When provided, repos from the config file are pre-seeded into the database on startup.

## `index`

Index repos from a config file.

```bash
local-docs-mcp index --config <path> [--repo <alias>] [--db <path>]
```

## `search`

Search indexed docs from the command line.

```bash
local-docs-mcp search <query> [--repo <alias>] [--limit N] [--tokens N] [--db <path>]
```

## `list`

List indexed repos with status and doc counts.

```bash
local-docs-mcp list [--db <path>]
```

## `update`

Re-index repos. Without `--config`, updates all repos from the database.

```bash
local-docs-mcp update [alias] [--config <path>] [--force] [--db <path>]
```

## `remove`

Remove a repo and all its indexed documents.

```bash
local-docs-mcp remove <alias> [--db <path>]
```

## `browse`

Navigate the doc tree. Without a path, lists files. With a path, shows headings.

```bash
local-docs-mcp browse <alias> [path] [--db <path>]
```

## Config File

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

Pass it to `stdio` or `index`:
```bash
local-docs-mcp stdio --config ./config.json
local-docs-mcp index --config ./config.json
```
