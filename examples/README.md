# Examples

## Entra ID Hybrid Documentation

`entra-config.json` indexes Microsoft's Entra ID hybrid identity documentation,
covering Connect Sync, Cloud Sync, and general hybrid identity topics.

### Quick start

1. Build the binary:
   ```
   go build -o local-docs-mcp ./cmd/local-docs-mcp
   ```

2. Index the documentation:
   ```
   ./local-docs-mcp index --config examples/entra-config.json
   ```

3. Search:
   ```
   ./local-docs-mcp search "connect sync filtering"
   ```

4. Browse the doc tree:
   ```
   ./local-docs-mcp browse entra-hybrid
   ```

5. Use as MCP server:
   ```
   ./local-docs-mcp serve --config examples/entra-config.json
   ```

### What gets indexed

The config indexes three directory trees from MicrosoftDocs/entra-docs:
- `docs/identity/hybrid/connect/` - Connect Sync documentation
- `docs/identity/hybrid/cloud-sync/` - Cloud Sync documentation
- `docs/identity/hybrid/` - General hybrid identity topics

This produces ~200+ markdown files, resulting in 500-2000 document chunks
in the search index.

### Claude Code integration

Add to your `~/.claude/settings.json`:

```json
{
  "mcpServers": {
    "local-docs-mcp": {
      "command": "/path/to/local-docs-mcp",
      "args": ["serve", "--config", "/path/to/examples/entra-config.json"]
    }
  }
}
```

Then ask Claude about Entra hybrid identity topics and it will search
the indexed documentation.
