package mcpserver

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// RemoveDocsInput defines the input schema for the remove_docs tool.
type RemoveDocsInput struct {
	Alias string `json:"alias" jsonschema:"Name of the doc source to remove"`
}

// registerRemoveDocsTool registers the remove_docs tool on the MCP server.
func (s *Server) registerRemoveDocsTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "remove_docs",
		Description: "Remove an indexed documentation source and all its documents. This is destructive and cannot be undone. Always confirm with the user before calling this tool.",
	}, s.handleRemoveDocs)
}

// handleRemoveDocs implements the remove_docs tool handler.
func (s *Server) handleRemoveDocs(_ context.Context, _ *mcp.CallToolRequest, input RemoveDocsInput) (*mcp.CallToolResult, any, error) {
	if input.Alias == "" {
		return nil, nil, fmt.Errorf("alias is required")
	}

	count, err := s.store.DeleteRepo(input.Alias)
	if err != nil {
		return nil, nil, fmt.Errorf("repo %q not found", input.Alias)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{
			Text: fmt.Sprintf("Removed %q: %d documents deleted.", input.Alias, count),
		}},
	}, nil, nil
}
