# Benchmarks

Results from running integration benchmarks against MicrosoftDocs/entra-docs.

## Environment

- Go version: 1.25.6
- Dataset: entra-hybrid (MicrosoftDocs/entra-docs, hybrid identity subset)

## How to Run

```bash
# Index the test data first (required before benchmarks)
go test -tags integration ./internal/integrationtest/ -run TestIndexEntraDocs -v

# Run benchmarks
go test -tags integration ./internal/integrationtest/ -bench=. -benchmem -run=^$
```

## Index Performance

- Files indexed: ~200 markdown files
- Chunks produced: 500-2000
- Index time target: <60s (includes git clone)

## Search Performance

- BenchmarkSearch: target <10ms per query
- BenchmarkSearchVariedQueries: target <10ms per query average

Run the benchmarks above and paste results here after execution.
