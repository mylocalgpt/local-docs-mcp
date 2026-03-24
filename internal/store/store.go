package store

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// Store provides access to the SQLite documentation database.
type Store struct {
	db *sql.DB
}

// NewStore opens (or creates) the SQLite database at dbPath, applies pragmas
// and schema, and returns a ready-to-use Store.
// Pass ":memory:" for an ephemeral in-memory database (useful in tests).
func NewStore(dbPath string) (*Store, error) {
	if dbPath != ":memory:" {
		dir := filepath.Dir(dbPath)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, fmt.Errorf("create db directory: %w", err)
		}
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	// Pragmas must be set per-connection, outside any transaction.
	pragmas := []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA busy_timeout=5000;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA foreign_keys=ON;",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			db.Close()
			return nil, fmt.Errorf("set pragma %q: %w", p, err)
		}
	}

	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("apply schema: %w", err)
	}

	return &Store{db: db}, nil
}

// UpsertRepo inserts or updates a repo by alias. Returns the repo ID.
// Does not modify commit_sha, indexed_at, or doc_count.
func (s *Store) UpsertRepo(alias, url, paths string) (int64, error) {
	res, err := s.db.Exec(
		`INSERT INTO repos (alias, url, paths)
		 VALUES (?, ?, ?)
		 ON CONFLICT(alias) DO UPDATE SET url=excluded.url, paths=excluded.paths`,
		alias, url, paths,
	)
	if err != nil {
		return 0, fmt.Errorf("upsert repo: %w", err)
	}

	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("last insert id: %w", err)
	}

	// ON CONFLICT DO UPDATE does not always return a useful LastInsertId.
	// Fall back to a SELECT if needed.
	if id == 0 {
		row := s.db.QueryRow("SELECT id FROM repos WHERE alias = ?", alias)
		if err := row.Scan(&id); err != nil {
			return 0, fmt.Errorf("select repo id: %w", err)
		}
	}

	return id, nil
}

// GetRepo retrieves a repo by alias. Returns (nil, nil) if not found.
func (s *Store) GetRepo(alias string) (*Repo, error) {
	r := &Repo{}
	err := s.db.QueryRow(
		`SELECT id, alias, url, paths,
		        COALESCE(commit_sha, ''), COALESCE(indexed_at, ''), doc_count
		 FROM repos WHERE alias = ?`, alias,
	).Scan(&r.ID, &r.Alias, &r.URL, &r.Paths, &r.CommitSHA, &r.IndexedAt, &r.DocCount)

	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("get repo: %w", err)
	}
	return r, nil
}

// UpdateRepoIndex sets the commit SHA, indexed-at timestamp, and doc count
// for a repo.
func (s *Store) UpdateRepoIndex(id int64, commitSHA, indexedAt string, docCount int) error {
	_, err := s.db.Exec(
		"UPDATE repos SET commit_sha = ?, indexed_at = ?, doc_count = ? WHERE id = ?",
		commitSHA, indexedAt, docCount, id,
	)
	if err != nil {
		return fmt.Errorf("update repo index: %w", err)
	}
	return nil
}

// ReplaceDocuments atomically replaces all documents for a repo. Old documents
// are deleted and new ones inserted in a single transaction.
func (s *Store) ReplaceDocuments(repoID int64, docs []Document) error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	if _, err := tx.Exec("DELETE FROM documents WHERE repo_id = ?", repoID); err != nil {
		return fmt.Errorf("delete old documents: %w", err)
	}

	stmt, err := tx.Prepare(
		`INSERT INTO documents (repo_id, path, doc_title, section_title, content, tokens, heading_level, has_code)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
	)
	if err != nil {
		return fmt.Errorf("prepare insert: %w", err)
	}
	defer stmt.Close()

	for _, d := range docs {
		hasCode := 0
		if d.HasCode {
			hasCode = 1
		}
		if _, err := stmt.Exec(d.RepoID, d.Path, d.DocTitle, d.SectionTitle, d.Content, d.Tokens, d.HeadingLevel, hasCode); err != nil {
			return fmt.Errorf("insert document %q: %w", d.SectionTitle, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit tx: %w", err)
	}
	return nil
}

// RebuildFTS rebuilds the FTS index. Only needed as a maintenance or recovery
// operation since triggers keep the index current.
func (s *Store) RebuildFTS() error {
	_, err := s.db.Exec("INSERT INTO docs_fts(docs_fts) VALUES('rebuild')")
	if err != nil {
		return fmt.Errorf("rebuild fts: %w", err)
	}
	return nil
}

// SearchFTS runs an FTS5 MATCH query with BM25 ranking. If repoID is non-nil,
// results are filtered to that repo. Returns up to limit raw results ordered
// by BM25 score (lower/more negative = better match).
func (s *Store) SearchFTS(query string, repoID *int64, limit int) ([]RawSearchResult, error) {
	baseSQL := `SELECT d.id, d.repo_id, r.alias, d.path, d.doc_title, d.section_title,
	       d.heading_level, d.content, d.tokens,
	       snippet(docs_fts, 2, '**', '**', '...', 48) AS excerpt,
	       bm25(docs_fts, 5.0, 10.0, 1.0) AS score
	FROM docs_fts
	JOIN documents d ON d.id = docs_fts.rowid
	JOIN repos r ON r.id = d.repo_id
	WHERE docs_fts MATCH ?`

	args := []any{query}

	if repoID != nil {
		baseSQL += " AND d.repo_id = ?"
		args = append(args, *repoID)
	}

	baseSQL += " ORDER BY bm25(docs_fts, 5.0, 10.0, 1.0) LIMIT ?"
	args = append(args, limit)

	rows, err := s.db.Query(baseSQL, args...)
	if err != nil {
		return nil, fmt.Errorf("search fts: %w", err)
	}
	defer rows.Close()

	var results []RawSearchResult
	for rows.Next() {
		var r RawSearchResult
		if err := rows.Scan(&r.DocID, &r.RepoID, &r.RepoAlias, &r.Path, &r.DocTitle,
			&r.SectionTitle, &r.HeadingLevel, &r.Content, &r.Tokens,
			&r.Excerpt, &r.Score); err != nil {
			return nil, fmt.Errorf("scan search result: %w", err)
		}
		results = append(results, r)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate search results: %w", err)
	}
	return results, nil
}

// ListRepos returns all repos ordered by alias.
func (s *Store) ListRepos() ([]Repo, error) {
	rows, err := s.db.Query(
		`SELECT id, alias, url, paths, COALESCE(commit_sha, ''), COALESCE(indexed_at, ''), doc_count
		 FROM repos ORDER BY alias`)
	if err != nil {
		return nil, fmt.Errorf("list repos: %w", err)
	}
	defer rows.Close()

	var repos []Repo
	for rows.Next() {
		var r Repo
		if err := rows.Scan(&r.ID, &r.Alias, &r.URL, &r.Paths, &r.CommitSHA, &r.IndexedAt, &r.DocCount); err != nil {
			return nil, fmt.Errorf("scan repo: %w", err)
		}
		repos = append(repos, r)
	}
	return repos, rows.Err()
}

// DeleteRepo removes a repo and all its documents. Returns the number of
// documents that were deleted. The FTS index stays in sync via the delete
// trigger on documents.
func (s *Store) DeleteRepo(alias string) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback() //nolint:errcheck

	var repoID int64
	err = tx.QueryRow("SELECT id FROM repos WHERE alias = ?", alias).Scan(&repoID)
	if err != nil {
		return 0, fmt.Errorf("repo %q not found: %w", alias, err)
	}

	var docCount int
	err = tx.QueryRow("SELECT COUNT(*) FROM documents WHERE repo_id = ?", repoID).Scan(&docCount)
	if err != nil {
		return 0, fmt.Errorf("count documents: %w", err)
	}

	if _, err := tx.Exec("DELETE FROM documents WHERE repo_id = ?", repoID); err != nil {
		return 0, fmt.Errorf("delete documents: %w", err)
	}
	if _, err := tx.Exec("DELETE FROM repos WHERE id = ?", repoID); err != nil {
		return 0, fmt.Errorf("delete repo: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("commit tx: %w", err)
	}
	return docCount, nil
}

// BrowseFiles returns the list of files and their section counts for a repo.
func (s *Store) BrowseFiles(repoID int64) ([]FileInfo, error) {
	rows, err := s.db.Query(
		"SELECT path, COUNT(*) as sections FROM documents WHERE repo_id = ? GROUP BY path ORDER BY path",
		repoID)
	if err != nil {
		return nil, fmt.Errorf("browse files: %w", err)
	}
	defer rows.Close()

	var files []FileInfo
	for rows.Next() {
		var f FileInfo
		if err := rows.Scan(&f.Path, &f.Sections); err != nil {
			return nil, fmt.Errorf("scan file info: %w", err)
		}
		files = append(files, f)
	}
	return files, rows.Err()
}

// BrowseHeadings returns the headings for a specific file in a repo, ordered
// by document insertion order.
func (s *Store) BrowseHeadings(repoID int64, path string) ([]HeadingInfo, error) {
	rows, err := s.db.Query(
		"SELECT section_title, heading_level, tokens FROM documents WHERE repo_id = ? AND path = ? ORDER BY id",
		repoID, path)
	if err != nil {
		return nil, fmt.Errorf("browse headings: %w", err)
	}
	defer rows.Close()

	var headings []HeadingInfo
	for rows.Next() {
		var h HeadingInfo
		if err := rows.Scan(&h.SectionTitle, &h.HeadingLevel, &h.Tokens); err != nil {
			return nil, fmt.Errorf("scan heading info: %w", err)
		}
		headings = append(headings, h)
	}
	return headings, rows.Err()
}

// Close closes the underlying database connection.
func (s *Store) Close() error {
	return s.db.Close()
}
