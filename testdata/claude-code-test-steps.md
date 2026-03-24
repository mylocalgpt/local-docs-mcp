# Manual Claude Code Testing Steps

## Setup

1. Build the binary:
   ```
   go build -o local-docs-mcp ./cmd/local-docs-mcp
   ```

2. Index the test config:
   ```
   ./local-docs-mcp index --config examples/entra-config.json
   ```

3. Add to `~/.claude/settings.json`:
   ```json
   {
     "mcpServers": {
       "local-docs-mcp": {
         "command": "/absolute/path/to/local-docs-mcp",
         "args": ["serve", "--config", "/absolute/path/to/examples/entra-config.json"]
       }
     }
   }
   ```

4. Restart Claude Code to pick up the new MCP server.

## Test Queries

Try the following queries in Claude Code and verify it uses the local-docs-mcp tools:

- "What is Microsoft Entra Connect Sync filtering?" - should return docs about sync filtering
- "How do I set up a staging server for Entra Connect?" - should return staging server docs
- "What is password hash synchronization?" - should return password hash sync docs
- "How does cloud sync differ from connect sync?" - should reference hybrid identity docs

## Expected Behavior

- Claude should call `search_docs` when asked about Entra topics
- Results should come from the indexed entra-hybrid repo
- Claude may also call `browse_docs` to explore the doc tree
- `list_repos` should show entra-hybrid with a doc count

## Troubleshooting

- If no results, verify the index was built: `./local-docs-mcp search "connect sync"`
- Check the DB exists: `ls -la ~/.local/share/local-docs-mcp/docs.db`
- Verify MCP server starts: `./local-docs-mcp serve --config examples/entra-config.json`
