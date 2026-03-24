package store

// Repo represents a tracked documentation repository.
type Repo struct {
	ID        int64
	Alias     string
	URL       string
	Paths     string // JSON array string
	CommitSHA string
	IndexedAt string
	DocCount  int
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
