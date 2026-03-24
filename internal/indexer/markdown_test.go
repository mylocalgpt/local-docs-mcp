package indexer

import (
	"strings"
	"testing"
)

func TestSplitByHeadings_Basic(t *testing.T) {
	content := `# Main Title

Intro paragraph.

## Section One

Content one.

### Subsection

Deep content.
`
	chunks := SplitByHeadings(content, "test.md")

	if len(chunks) != 3 {
		t.Fatalf("expected 3 chunks, got %d", len(chunks))
	}

	// First chunk should be h1
	if chunks[0].SectionTitle != "Main Title" {
		t.Errorf("chunk 0 title = %q, want %q", chunks[0].SectionTitle, "Main Title")
	}
	if chunks[0].HeadingLevel != 1 {
		t.Errorf("chunk 0 level = %d, want 1", chunks[0].HeadingLevel)
	}
	if chunks[0].DocTitle != "Main Title" {
		t.Errorf("chunk 0 doc title = %q, want %q", chunks[0].DocTitle, "Main Title")
	}

	// Second chunk should be h2
	if chunks[1].SectionTitle != "Section One" {
		t.Errorf("chunk 1 title = %q, want %q", chunks[1].SectionTitle, "Section One")
	}
	if chunks[1].HeadingLevel != 2 {
		t.Errorf("chunk 1 level = %d, want 2", chunks[1].HeadingLevel)
	}

	// Third chunk should be h3
	if chunks[2].SectionTitle != "Subsection" {
		t.Errorf("chunk 2 title = %q, want %q", chunks[2].SectionTitle, "Subsection")
	}
	if chunks[2].HeadingLevel != 3 {
		t.Errorf("chunk 2 level = %d, want 3", chunks[2].HeadingLevel)
	}
}

func TestSplitByHeadings_NoHeadings(t *testing.T) {
	content := "Just some text with no headings.\n\nAnother paragraph."
	chunks := SplitByHeadings(content, "docs/readme.md")

	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if chunks[0].DocTitle != "readme" {
		t.Errorf("doc title = %q, want %q", chunks[0].DocTitle, "readme")
	}
	if chunks[0].SectionTitle != "readme" {
		t.Errorf("section title = %q, want %q", chunks[0].SectionTitle, "readme")
	}
	if chunks[0].HeadingLevel != 0 {
		t.Errorf("heading level = %d, want 0", chunks[0].HeadingLevel)
	}
}

func TestStripFrontMatter(t *testing.T) {
	content := "---\ntitle: Test\ndate: 2024-01-01\n---\n# Hello\n\nBody text."
	result := stripFrontMatter(content)
	if strings.Contains(result, "title: Test") {
		t.Error("front matter was not stripped")
	}
	if !strings.Contains(result, "# Hello") {
		t.Error("content after front matter was removed")
	}
}

func TestStripFrontMatter_None(t *testing.T) {
	content := "# Hello\n\nNo front matter here."
	result := stripFrontMatter(content)
	if result != content {
		t.Errorf("content changed when no front matter present")
	}
}

func TestStripFrontMatter_Unclosed(t *testing.T) {
	content := "---\ntitle: Test\nNo closing delimiter."
	result := stripFrontMatter(content)
	if result != content {
		t.Errorf("content changed when front matter is unclosed")
	}
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		text   string
		expect int
	}{
		{"", 0},
		{"a", 1},
		{"abcd", 1},
		{"abcde", 2},
		{"Hello, world!", 4},                    // 13 chars -> (13+3)/4 = 4
		{strings.Repeat("x", 100), 25},          // 100 chars -> 25
		{strings.Repeat("a", 3200), SoftTokenLimit}, // 3200 chars -> 800 tokens
	}

	for _, tt := range tests {
		got := EstimateTokens(tt.text)
		if got != tt.expect {
			t.Errorf("EstimateTokens(%d chars) = %d, want %d", len(tt.text), got, tt.expect)
		}
	}
}

func TestSplitOversizedChunk(t *testing.T) {
	// Create a chunk with about 1000 tokens (4000 chars)
	para1 := strings.Repeat("Word ", 400) // ~2000 chars = ~500 tokens
	para2 := strings.Repeat("Text ", 400) // ~2000 chars = ~500 tokens
	bigContent := para1 + "\n\n" + para2

	chunk := Chunk{
		DocTitle:     "Test",
		SectionTitle: "Big Section",
		Content:      bigContent,
		HeadingLevel: 2,
		Tokens:       EstimateTokens(bigContent),
	}

	result := SplitOversizedChunk(chunk, SoftTokenLimit, HardTokenLimit)

	if len(result) < 2 {
		t.Fatalf("expected at least 2 sub-chunks, got %d", len(result))
	}

	// First sub-chunk keeps original title
	if result[0].SectionTitle != "Big Section" {
		t.Errorf("first sub-chunk title = %q, want %q", result[0].SectionTitle, "Big Section")
	}

	// Second sub-chunk has (part 2)
	if result[1].SectionTitle != "Big Section (part 2)" {
		t.Errorf("second sub-chunk title = %q, want %q", result[1].SectionTitle, "Big Section (part 2)")
	}

	// All sub-chunks should be within limits
	for i, sc := range result {
		if sc.Tokens > HardTokenLimit {
			t.Errorf("sub-chunk %d has %d tokens, exceeds hard limit %d", i, sc.Tokens, HardTokenLimit)
		}
		if sc.DocTitle != "Test" {
			t.Errorf("sub-chunk %d doc title = %q, want %q", i, sc.DocTitle, "Test")
		}
		if sc.HeadingLevel != 2 {
			t.Errorf("sub-chunk %d level = %d, want 2", i, sc.HeadingLevel)
		}
	}
}

func TestSplitOversizedChunk_UnderLimit(t *testing.T) {
	chunk := Chunk{
		DocTitle:     "Test",
		SectionTitle: "Small",
		Content:      "Short content.",
		HeadingLevel: 1,
		Tokens:       EstimateTokens("Short content."),
	}

	result := SplitOversizedChunk(chunk, SoftTokenLimit, HardTokenLimit)
	if len(result) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(result))
	}
	if result[0].SectionTitle != "Small" {
		t.Errorf("title changed: got %q", result[0].SectionTitle)
	}
}

func TestIsTOCSection(t *testing.T) {
	toc := `## Table of Contents

- [Introduction](#introduction)
- [Getting Started](#getting-started)
- [API Reference](#api-reference)
- [Configuration](#configuration)
- [Troubleshooting](#troubleshooting)
`
	if !IsTOCSection(toc) {
		t.Error("TOC section not detected")
	}

	normal := `## Introduction

This is a normal section with some text explaining the library.
It has multiple sentences and does not consist primarily of links.
`
	if IsTOCSection(normal) {
		t.Error("normal section incorrectly detected as TOC")
	}
}

func TestIsTOCSection_Empty(t *testing.T) {
	if IsTOCSection("") {
		t.Error("empty content detected as TOC")
	}
}

func TestCodeFenceDetection(t *testing.T) {
	content := "## Example\n\n```go\nfmt.Println(\"hello\")\n```\n"
	chunks := SplitByHeadings(content, "test.md")

	if len(chunks) != 1 {
		t.Fatalf("expected 1 chunk, got %d", len(chunks))
	}
	if !chunks[0].HasCode {
		t.Error("HasCode should be true for chunk with code fence")
	}
}

func TestHeadingsInsideCodeFence(t *testing.T) {
	content := `# Real Title

Some intro.

` + "```bash\n# This is a bash comment\n## Another comment\necho hello\n```" + `

## Real Section

More content.
`
	chunks := SplitByHeadings(content, "test.md")

	// Should have 2 chunks: h1 with intro+code block, h2 real section
	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}

	if chunks[0].SectionTitle != "Real Title" {
		t.Errorf("chunk 0 title = %q, want %q", chunks[0].SectionTitle, "Real Title")
	}
	if chunks[1].SectionTitle != "Real Section" {
		t.Errorf("chunk 1 title = %q, want %q", chunks[1].SectionTitle, "Real Section")
	}

	// The bash comments should be inside the first chunk's content
	if !strings.Contains(chunks[0].Content, "# This is a bash comment") {
		t.Error("bash comment should be in first chunk content")
	}
}

func TestDeduplicateChunks(t *testing.T) {
	chunks := []Chunk{
		{SectionTitle: "A", Content: "Same content here"},
		{SectionTitle: "B", Content: "Different content"},
		{SectionTitle: "C", Content: "Same content here"}, // duplicate of A
		{SectionTitle: "D", Content: "Yet another"},
	}

	result := DeduplicateChunks(chunks)

	if len(result) != 3 {
		t.Fatalf("expected 3 chunks after dedup, got %d", len(result))
	}

	// Order preserved: A, B, D
	if result[0].SectionTitle != "A" {
		t.Errorf("result[0] title = %q, want A", result[0].SectionTitle)
	}
	if result[1].SectionTitle != "B" {
		t.Errorf("result[1] title = %q, want B", result[1].SectionTitle)
	}
	if result[2].SectionTitle != "D" {
		t.Errorf("result[2] title = %q, want D", result[2].SectionTitle)
	}
}

func TestProcessMarkdownFile_EndToEnd(t *testing.T) {
	content := "---\ntitle: Guide\n---\n" +
		"# User Guide\n\nWelcome to the guide.\n\n" +
		"## Table of Contents\n\n" +
		"- [Setup](#setup)\n- [Usage](#usage)\n- [Advanced](#advanced)\n- [FAQ](#faq)\n\n" +
		"## Setup\n\nHere is how to set up.\n\n" +
		"## Big Section\n\n" +
		strings.Repeat("This is a paragraph with enough words to make it quite long. ", 80) +
		"\n\n" +
		strings.Repeat("Another long paragraph with many repeated words for testing. ", 80) +
		"\n\n" +
		"## Duplicate\n\nHere is how to set up.\n"

	chunks := ProcessMarkdownFile("guide.md", content)

	// Front matter should be stripped (no chunk should contain "title: Guide")
	for _, c := range chunks {
		if strings.Contains(c.Content, "title: Guide") {
			t.Error("front matter not stripped")
		}
	}

	// TOC section should be filtered out
	for _, c := range chunks {
		if c.SectionTitle == "Table of Contents" {
			t.Error("TOC section not filtered")
		}
	}

	// All chunks should have DocTitle = "User Guide"
	for i, c := range chunks {
		if c.DocTitle != "User Guide" {
			t.Errorf("chunk %d doc title = %q, want %q", i, c.DocTitle, "User Guide")
		}
	}

	// All chunks should have token estimates
	for i, c := range chunks {
		if c.Tokens <= 0 {
			t.Errorf("chunk %d has no token estimate", i)
		}
	}

	// The big section should have been split
	bigParts := 0
	for _, c := range chunks {
		if strings.HasPrefix(c.SectionTitle, "Big Section") {
			bigParts++
		}
	}
	if bigParts < 2 {
		t.Errorf("expected big section to be split into 2+ parts, got %d", bigParts)
	}

	// "Duplicate" section has same content as "Setup" section, so one should be deduped
	setupCount := 0
	for _, c := range chunks {
		if strings.Contains(c.Content, "Here is how to set up.") {
			setupCount++
		}
	}
	if setupCount != 1 {
		t.Errorf("expected 1 chunk with setup content (dedup), got %d", setupCount)
	}
}

func TestProcessMarkdownFile_WindowsLineEndings(t *testing.T) {
	content := "# Title\r\n\r\nSome content.\r\n\r\n## Section\r\n\r\nMore content.\r\n"
	chunks := ProcessMarkdownFile("test.md", content)

	if len(chunks) < 2 {
		t.Fatalf("expected at least 2 chunks, got %d", len(chunks))
	}
	if chunks[0].DocTitle != "Title" {
		t.Errorf("doc title = %q, want %q", chunks[0].DocTitle, "Title")
	}
}

func TestSplitByHeadings_PreambleBeforeFirstHeading(t *testing.T) {
	content := "Some intro text.\n\n# Title\n\nBody."
	chunks := SplitByHeadings(content, "test.md")

	if len(chunks) != 2 {
		t.Fatalf("expected 2 chunks, got %d", len(chunks))
	}

	// Preamble chunk
	if chunks[0].HeadingLevel != 0 {
		t.Errorf("preamble level = %d, want 0", chunks[0].HeadingLevel)
	}
	if !strings.Contains(chunks[0].Content, "Some intro text") {
		t.Error("preamble chunk missing intro text")
	}

	// Heading chunk
	if chunks[1].SectionTitle != "Title" {
		t.Errorf("heading chunk title = %q, want %q", chunks[1].SectionTitle, "Title")
	}
}
