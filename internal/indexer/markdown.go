package indexer

import (
	"fmt"
	"hash/fnv"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	SoftTokenLimit = 800
	HardTokenLimit = 1200
)

// Chunk represents a section of a markdown file suitable for FTS indexing.
type Chunk struct {
	DocTitle     string
	SectionTitle string
	Content      string
	HeadingLevel int
	Tokens       int
	HasCode      bool
}

var headingRegex = regexp.MustCompile(`^(#{1,6})\s+(.+)$`)
var linkRegex = regexp.MustCompile(`\[[^\]]*\]\([^)]*\)`)

// ProcessMarkdownFile splits a markdown file into heading-based chunks suitable
// for FTS indexing. It strips front matter, splits by headings, filters TOC
// sections, splits oversized chunks, estimates tokens, and deduplicates.
func ProcessMarkdownFile(filePath, content string) []Chunk {
	// 1. Normalize line endings
	content = strings.ReplaceAll(content, "\r\n", "\n")

	// 2. Strip YAML front matter
	content = stripFrontMatter(content)

	// 3. Split by headings
	chunks := SplitByHeadings(content, filePath)

	// 4. Filter out TOC sections
	filtered := make([]Chunk, 0, len(chunks))
	for _, c := range chunks {
		if !IsTOCSection(c.Content) {
			filtered = append(filtered, c)
		}
	}
	chunks = filtered

	// 5. Estimate tokens for each chunk
	for i := range chunks {
		chunks[i].Tokens = EstimateTokens(chunks[i].Content)
	}

	// 6. Split oversized chunks
	var split []Chunk
	for _, c := range chunks {
		split = append(split, SplitOversizedChunk(c, SoftTokenLimit, HardTokenLimit)...)
	}
	chunks = split

	// 7. Deduplicate
	chunks = DeduplicateChunks(chunks)

	return chunks
}

// stripFrontMatter removes YAML front matter delimited by --- lines at the
// start of the file.
func stripFrontMatter(content string) string {
	if !strings.HasPrefix(content, "---\n") {
		return content
	}
	// Find the closing ---
	rest := content[4:] // skip opening "---\n"
	idx := strings.Index(rest, "---\n")
	if idx < 0 {
		// Check if file ends with --- (no trailing newline)
		if strings.HasSuffix(rest, "---") {
			return ""
		}
		return content
	}
	return rest[idx+4:]
}

// SplitByHeadings splits markdown content into chunks based on heading lines.
// It is code-fence-aware: # lines inside fenced code blocks are not treated
// as headings.
func SplitByHeadings(content, filePath string) []Chunk {
	lines := strings.Split(content, "\n")

	// First pass: find the first h1 to use as DocTitle
	docTitle := ""
	inCodeFence := false
	for _, line := range lines {
		if strings.HasPrefix(line, "```") {
			inCodeFence = !inCodeFence
			continue
		}
		if inCodeFence {
			continue
		}
		m := headingRegex.FindStringSubmatch(line)
		if m != nil && len(m[1]) == 1 {
			docTitle = m[2]
			break
		}
	}
	if docTitle == "" {
		// Use filename without path or extension
		base := filepath.Base(filePath)
		docTitle = strings.TrimSuffix(base, filepath.Ext(base))
	}

	// Second pass: split into chunks
	type section struct {
		title string
		level int
		lines []string
	}

	var sections []section
	var current *section
	inCodeFence = false

	for _, line := range lines {
		if strings.HasPrefix(line, "```") {
			inCodeFence = !inCodeFence
		}

		if !inCodeFence {
			m := headingRegex.FindStringSubmatch(line)
			if m != nil {
				// Start a new section
				sections = append(sections, section{
					title: m[2],
					level: len(m[1]),
					lines: []string{line},
				})
				current = &sections[len(sections)-1]
				continue
			}
		}

		if current == nil {
			// Content before first heading
			sections = append([]section{{
				title: docTitle,
				level: 0,
				lines: []string{line},
			}}, sections...)
			current = &sections[0]
		} else {
			current.lines = append(current.lines, line)
			// Update the slice element since current points to our local copy
			sections[len(sections)-1] = *current
		}
	}

	// Edge case: the pointer trick above doesn't work well because we prepend.
	// Let me redo this more carefully.
	sections = nil
	current = nil
	inCodeFence = false
	var preambleLines []string
	hasPreamble := false

	for _, line := range lines {
		if strings.HasPrefix(line, "```") {
			inCodeFence = !inCodeFence
		}

		if !inCodeFence {
			m := headingRegex.FindStringSubmatch(line)
			if m != nil {
				// Start a new section
				sections = append(sections, section{
					title: m[2],
					level: len(m[1]),
					lines: []string{line},
				})
				current = &sections[len(sections)-1]
				continue
			}
		}

		if len(sections) == 0 {
			// Content before first heading
			preambleLines = append(preambleLines, line)
			hasPreamble = true
		} else {
			sections[len(sections)-1].lines = append(sections[len(sections)-1].lines, line)
		}
	}

	// Build chunks
	var chunks []Chunk

	if hasPreamble {
		preambleContent := strings.Join(preambleLines, "\n")
		// Only add preamble if it has non-whitespace content
		if strings.TrimSpace(preambleContent) != "" {
			chunks = append(chunks, Chunk{
				DocTitle:     docTitle,
				SectionTitle: docTitle,
				Content:      preambleContent,
				HeadingLevel: 0,
				HasCode:      strings.Contains(preambleContent, "```"),
			})
		}
	}

	for _, s := range sections {
		body := strings.Join(s.lines, "\n")
		chunks = append(chunks, Chunk{
			DocTitle:     docTitle,
			SectionTitle: s.title,
			Content:      body,
			HeadingLevel: s.level,
			HasCode:      strings.Contains(body, "```"),
		})
	}

	// If no chunks at all (empty content), return a single chunk
	if len(chunks) == 0 {
		chunks = append(chunks, Chunk{
			DocTitle:     docTitle,
			SectionTitle: docTitle,
			Content:      content,
			HeadingLevel: 0,
		})
	}

	return chunks
}

// EstimateTokens returns a rough token count using ceiling division by 4.
func EstimateTokens(text string) int {
	return (len(text) + 3) / 4
}

// SplitOversizedChunk splits a chunk that exceeds the soft token limit into
// smaller sub-chunks at paragraph boundaries. Single paragraphs exceeding the
// hard limit are further split at line boundaries.
func SplitOversizedChunk(chunk Chunk, softLimit, hardLimit int) []Chunk {
	if chunk.Tokens <= softLimit {
		return []Chunk{chunk}
	}

	allParagraphs := strings.Split(chunk.Content, "\n\n")

	// Filter out empty paragraphs (from trailing newlines, etc.)
	paragraphs := make([]string, 0, len(allParagraphs))
	for _, p := range allParagraphs {
		if strings.TrimSpace(p) != "" {
			paragraphs = append(paragraphs, p)
		}
	}

	var subChunks []Chunk
	var accumLines []string
	accumTokens := 0

	flush := func(partNum int) {
		if len(accumLines) == 0 {
			return
		}
		body := strings.Join(accumLines, "\n\n")
		if strings.TrimSpace(body) == "" {
			accumLines = nil
			accumTokens = 0
			return
		}
		title := chunk.SectionTitle
		if partNum > 1 {
			title = fmt.Sprintf("%s (part %d)", chunk.SectionTitle, partNum)
		}
		subChunks = append(subChunks, Chunk{
			DocTitle:     chunk.DocTitle,
			SectionTitle: title,
			Content:      body,
			HeadingLevel: chunk.HeadingLevel,
			Tokens:       EstimateTokens(body),
			HasCode:      strings.Contains(body, "```"),
		})
		accumLines = nil
		accumTokens = 0
	}

	partNum := 1

	for _, para := range paragraphs {
		paraTokens := EstimateTokens(para)

		// If a single paragraph exceeds the hard limit, split it at line boundaries
		if paraTokens > hardLimit {
			// Flush what we have first
			if len(accumLines) > 0 {
				flush(partNum)
				partNum++
			}

			lines := strings.Split(para, "\n")
			for _, line := range lines {
				lineTokens := EstimateTokens(line)
				if accumTokens+lineTokens > softLimit && len(accumLines) > 0 {
					flush(partNum)
					partNum++
				}
				accumLines = append(accumLines, line)
				accumTokens += lineTokens
			}
			if len(accumLines) > 0 {
				flush(partNum)
				partNum++
			}
			continue
		}

		// Check if adding this paragraph would exceed the soft limit
		if accumTokens+paraTokens > softLimit && len(accumLines) > 0 {
			flush(partNum)
			partNum++
		}

		accumLines = append(accumLines, para)
		accumTokens += paraTokens
	}

	// Flush remaining
	if len(accumLines) > 0 {
		flush(partNum)
	}

	return subChunks
}

// IsTOCSection returns true if the content appears to be a table of contents,
// determined by whether markdown links consume more than 50% of the content.
func IsTOCSection(content string) bool {
	if len(content) == 0 {
		return false
	}

	matches := linkRegex.FindAllString(content, -1)
	linkChars := 0
	for _, m := range matches {
		linkChars += len(m)
	}

	return linkChars > len(content)/2
}

// DeduplicateChunks removes chunks with duplicate content, preserving order.
// Two chunks with identical body text are considered duplicates even if they
// have different titles. The hash is computed on the body text only (content
// after the first heading line), so sections with identical prose but different
// headings are still detected as duplicates.
func DeduplicateChunks(chunks []Chunk) []Chunk {
	seen := make(map[string]bool)
	var result []Chunk

	for _, c := range chunks {
		// Extract body text: skip the heading line if present
		body := c.Content
		if c.HeadingLevel > 0 {
			if idx := strings.Index(body, "\n"); idx >= 0 {
				body = body[idx+1:]
			}
		}

		h := fnv.New64a()
		h.Write([]byte(body))
		key := fmt.Sprintf("%016x", h.Sum64())

		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, c)
	}

	return result
}
