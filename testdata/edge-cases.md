# Edge Case Findings

Results from running edge case integration tests against MicrosoftDocs/entra-docs.

## How to Run

```bash
go test -tags integration ./internal/integrationtest/ -run TestEdgeCase -v
```

## Deep Headings (h4+)

Tests for chunks with heading_level >= 4. These represent deeply nested sections
that may have very short content. The test logs each finding and verifies tokens > 0.

## Code-Heavy Files

Tests for chunks with has_code=1. Verifies that code content is preserved and
chunks have meaningful length (>20 chars). Spot-checks up to 5 code chunks.

## Large Files

Identifies the top 5 files by total token count across all chunks. Verifies that
no single chunk exceeds 1200 tokens (the chunking limit).

## YAML Front Matter

Checks for chunks starting with "---" which may indicate YAML front matter
leaking into content. Some matches may be legitimate markdown horizontal rules.
Flags if >10% of chunks have this pattern.

## INCLUDE Directives

Counts chunks containing `[!INCLUDE ...]` directives. These are left as-is in
the content since the indexer processes local files without resolving includes.

## Empty Chunks

Verifies no chunks have content shorter than 10 characters. If found, logs the
offending chunks for debugging.

## Duplicate Headings

Checks for files with repeated section titles within the same file. If indexing
succeeded, no UNIQUE constraint violations occurred. Logs any files that have
multiple chunks with the same section title.
