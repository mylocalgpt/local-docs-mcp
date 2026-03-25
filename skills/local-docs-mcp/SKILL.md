---
name: local-docs-mcp
description: "Use when searching documentation indexed by local-docs-mcp MCP server"
license: MIT
---

# local-docs-mcp

Use this skill when the user asks about library documentation, needs reference material, or when search yields no results for a known library.

## Workflow

1. Call `list_repos` to check what documentation is already indexed.
2. Call `search_docs` with your query to find answers. Searches all indexed repos by default; use the `repo` parameter to narrow scope.
3. Use `browse_docs` to explore the doc tree when you need to understand structure rather than search for a specific term.
4. If the needed docs are not indexed, use `add_docs` to add them:
   - For git repos: research the correct GitHub URL and identify the specific subdirectory paths containing documentation (e.g. `["docs/", "guides/"]`), then call `add_docs` with `url`, `paths`, and `alias`.
   - For local directories: ask the user for the absolute filesystem path, then call `add_docs` with `path` and `alias`.
5. After `add_docs`, call `list_repos` to check indexing progress. Indexing runs in the background and may take a moment.
6. Once indexing is complete (status shows "ready"), use `search_docs` to find the answer.

## Search Tips

The search engine uses SQLite FTS5 syntax:

- Use quotes for exact phrases: `"hybrid identity"`
- Use AND/OR for boolean queries: `sync AND proxy`
- Use `prefix*` for partial matches: `authenticat*`
- Omit the `repo` parameter to search across all indexed sources
