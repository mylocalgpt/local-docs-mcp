package store

// Status constants for repo indexing state.
const (
	StatusIndexing = "indexing"
	StatusReady    = "ready"
	StatusError    = "error"
)

// Repo represents a tracked documentation repository.
type Repo struct {
	ID              int64
	Alias           string
	URL             string
	Paths           string // JSON array string
	CommitSHA       string
	IndexedAt       string
	DocCount        int
	SourceType      string
	Status          string
	StatusDetail    string
	StatusUpdatedAt string
}

// Document represents a single chunk of documentation content.
type Document struct {
	ID           int64
	RepoID       int64
	Path         string
	DocTitle     string
	SectionTitle string
	Content      string
	Tokens       int
	HeadingLevel int
	HasCode      bool
}

// RawSearchResult holds a single row from the FTS5 search query before
// post-processing (relevance filter, chunk merging, token budgeting).
type RawSearchResult struct {
	DocID        int64
	RepoID       int64
	RepoAlias    string
	RepoURL      string
	SourceType   string
	Path         string
	DocTitle     string
	SectionTitle string
	HeadingLevel int
	Content      string
	Tokens       int
	Excerpt      string
	Score        float64
}

// FileInfo holds a file path and section count for browse output.
type FileInfo struct {
	Path     string
	Sections int
}

// HeadingInfo holds heading data for browse output.
type HeadingInfo struct {
	SectionTitle string
	HeadingLevel int
	Tokens       int
}
