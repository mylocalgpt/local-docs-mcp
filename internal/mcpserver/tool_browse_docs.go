package mcpserver

import (
	"context"
	"fmt"
	"strings"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// BrowseDocsInput defines the input schema for the browse_docs tool.
type BrowseDocsInput struct {
	Repo     string `json:"repo" jsonschema:"Repo alias to browse. Use list_repos to see available repos."`
	Path     string `json:"path,omitempty" jsonschema:"File path to drill into. Omit to list all doc files in the repo."`
	Page     int    `json:"page,omitempty" jsonschema:"Page number (1-indexed). Default 1. Only applies to file listing mode."`
	PageSize int    `json:"page_size,omitempty" jsonschema:"Files per page. Default 30, max 100. Only applies to file listing mode."`
}

// registerBrowseDocsTool registers the browse_docs tool on the MCP server.
func (s *Server) registerBrowseDocsTool() {
	mcp.AddTool(s.server, &mcp.Tool{
		Name:        "browse_docs",
		Description: "Browse the documentation tree structure. Two modes: omit path to list all files with section counts, or provide a path to see the heading tree with token sizes. Use when search isn't specific enough and you want to explore what docs exist.",
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
		return s.browseFiles(repo.ID, repo.Alias, repo.URL, repo.SourceType, input.Page, input.PageSize)
	}
	return s.browseHeadings(repo.ID, repo.Alias, repo.URL, repo.SourceType, input.Path)
}

// browseFiles lists files in a repo with their section counts, with pagination.
func (s *Server) browseFiles(repoID int64, alias, url, sourceType string, page, pageSize int) (*mcp.CallToolResult, any, error) {
	if page < 1 {
		page = 1
	}
	if pageSize < 1 {
		pageSize = 30
	}
	if pageSize > 100 {
		pageSize = 100
	}

	files, total, err := s.store.BrowseFiles(repoID, page, pageSize)
	if err != nil {
		return nil, nil, err
	}

	if total == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{
				Text: fmt.Sprintf("No files found in %s. The repo may need to be indexed first.", alias),
			}},
		}, nil, nil
	}

	if len(files) == 0 {
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{
				Text: fmt.Sprintf("Page %d is beyond the %d files. Try a lower page number.", page, total),
			}},
		}, nil, nil
	}

	var b strings.Builder
	fmt.Fprintf(&b, "Files in %s (repo: %s):\n\n", alias, DisplayRepo(url, sourceType))

	totalSections := 0
	for _, f := range files {
		fmt.Fprintf(&b, "%s (%d sections)\n", f.Path, f.Sections)
		totalSections += f.Sections
	}

	totalPages := (total + pageSize - 1) / pageSize
	startIdx := (page-1)*pageSize + 1
	endIdx := startIdx + len(files) - 1

	if totalPages > 1 {
		fmt.Fprintf(&b, "\nShowing files %d-%d of %d.", startIdx, endIdx, total)
		if page < totalPages {
			fmt.Fprintf(&b, " Use page: %d to see more.", page+1)
		}
		fmt.Fprintf(&b, "\n")
	} else {
		fmt.Fprintf(&b, "\n%d files, %d sections total. Specify a path to see headings within a file.\n",
			total, totalSections)
	}

	return &mcp.CallToolResult{
		Content: []mcp.Content{&mcp.TextContent{Text: b.String()}},
	}, nil, nil
}

// browseHeadings shows the heading tree for a specific file in a repo.
func (s *Server) browseHeadings(repoID int64, alias, url, sourceType, path string) (*mcp.CallToolResult, any, error) {
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
	fmt.Fprintf(&b, "%s (repo: %s): %s\n\n", alias, DisplayRepo(url, sourceType), path)

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
