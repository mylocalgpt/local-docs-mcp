package mcpserver

import (
	"context"
	"runtime/debug"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/mylocalgpt/local-docs-mcp/internal/config"
	"github.com/mylocalgpt/local-docs-mcp/internal/indexer"
	"github.com/mylocalgpt/local-docs-mcp/internal/search"
	"github.com/mylocalgpt/local-docs-mcp/internal/store"
)

// Server wraps the MCP protocol server and the application dependencies
// needed to handle tool calls.
type Server struct {
	server  *mcp.Server
	store   *store.Store
	search  *search.Search
	indexer *indexer.Indexer
	config  *config.Config
}

// New creates a Server backed by the given store, search engine, indexer, and
// config. The MCP server name is "local-docs-mcp" and the version comes from
// build info, falling back to "dev".
func New(s *store.Store, srch *search.Search, ix *indexer.Indexer, cfg *config.Config) *Server {
	version := "dev"
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		version = bi.Main.Version
	}

	mcpSrv := mcp.NewServer(
		&mcp.Implementation{
			Name:    "local-docs-mcp",
			Version: version,
		},
		&mcp.ServerOptions{
			Instructions: "local-docs-mcp provides search over locally cached documentation from git repos.",
		},
	)

	return &Server{
		server:  mcpSrv,
		store:   s,
		search:  srch,
		indexer: ix,
		config:  cfg,
	}
}

// MCPServer returns the underlying mcp.Server. This is useful for tests that
// need to connect via in-memory transports.
func (s *Server) MCPServer() *mcp.Server {
	return s.server
}

// Run starts the MCP server on stdio transport and blocks until the client
// disconnects or the context is cancelled.
func (s *Server) Run(ctx context.Context) error {
	return s.server.Run(ctx, &mcp.StdioTransport{})
}
