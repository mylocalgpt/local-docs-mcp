package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// BrowseDocsInput defines the input schema for the browse_docs tool.
type BrowseDocsInput struct {
	Repo string `json:"repo" jsonschema:"Repo alias to browse. Use list_repos to see available repos."`
	Path string `json:"path,omitempty" jsonschema:"File path to drill into. Omit to list all doc files in the repo."`
}

// registerBrowseDocsTool registers the browse_docs tool on the MCP server.
func (s *Server) registerBrowseDocsTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "browse_docs",
		Description: "Browse the documentation tree structure. Use when search isn't specific enough and you want to explore what docs exist. Call without path to list files, with path to see headings.",
	}, s.handleBrowseDocs)
}

// handleBrowseDocs implements the browse_docs tool handler.
func (s *Server) handleBrowseDocs(_ context.Context, _ *mcp.CallToolRequest, input BrowseDocsInput) (*mcp.CallToolResult, any, error) {
	repo, err := s.store.GetRepo(input.Repo)
	if err != nil {
		return nil, nil, err
	}
	if repo == nil {
		return nil, nil, fmt.Errorf("Repo %q not found. Use list_repos to see available repos.", input.Repo)
	}

	if input.Path == "" {
		return s.browseFiles(repo.ID, repo.Alias)
	}
	return s.browseHeadings(repo.ID, repo.Alias, input.Path)
}

// browseFiles lists all files in a repo with their section counts.
func (s *Server) browseFiles(repoID int64, alias string) (*mcp.CallToolResult, any, error) {
	files, err := s.store.BrowseFiles(repoID)
	if err != nil {
		return nil, nil, err
	}

	if len(files) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{
				Text: fmt.Sprintf("No files found in %s. The repo may need to be indexed first.", alias),
			}},
		}, nil, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Files in %s:\n\n", alias)

	totalSections := 0
	for _, f := range files {
		fmt.Fprintf(&b, "%s (%d sections)\n", f.Path, f.Sections)
		totalSections += f.Sections
	}

	fmt.Fprintf(&b, "\n%d files, %d sections total. Specify a path to see headings within a file.\n", len(files), totalSections)

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: b.String()}},
	}, nil, nil
}

// browseHeadings shows the heading tree for a specific file in a repo.
func (s *Server) browseHeadings(repoID int64, alias, path string) (*mcp.CallToolResult, any, error) {
	headings, err := s.store.BrowseHeadings(repoID, path)
	if err != nil {
		return nil, nil, err
	}

	if len(headings) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{
				Text: fmt.Sprintf("No sections found for path %q in %s. Use browse_docs without a path to list available files.", path, alias),
			}},
		}, nil, nil
	}

	// Find minimum heading level for relative indentation
	minLevel := headings[0].HeadingLevel
	for _, h := range headings[1:] {
		if h.HeadingLevel < minLevel {
			minLevel = h.HeadingLevel
		}
	}

	var b strings.Builder
	fmt.Fprintf(&b, "%s: %s\n\n", alias, path)

	totalTokens := 0
	for _, h := range headings {
		indent := strings.Repeat("  ", h.HeadingLevel-minLevel)
		hashes := strings.Repeat("#", h.HeadingLevel)
		fmt.Fprintf(&b, "%s%s %s (%d tokens)\n", indent, hashes, h.SectionTitle, h.Tokens)
		totalTokens += h.Tokens
	}

	fmt.Fprintf(&b, "\n%d sections, %d tokens total. Use search_docs to search within this file.\n", len(headings), totalTokens)

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: b.String()}},
	}, nil, nil
}
