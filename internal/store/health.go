package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"unicode/utf8"
)

const byteSignatureQuery = `
    SELECT 1 FROM documents
    WHERE repo_id = ?
      AND (
          substr(content, 1, 3) = x'efbbbf'
          OR substr(content, 1, 2) = x'fffe'
          OR substr(content, 1, 2) = x'feff'
          OR length(content) < octet_length(content)
      )
    LIMIT 1`

// RepoHasInvalidEncoding reports whether any document row for repoID shows
// signs of an encoding the indexer should have rejected. Two passes:
// (1) SQL aggregate exhaustively checks all rows for BOM prefixes and
// embedded NUL bytes (length(TEXT) stops at first NUL; octet_length always
// returns byte count). (2) If pass 1 finds nothing, a bounded sample of up
// to 200 rows is checked with utf8.ValidString to catch invalid UTF-8 with
// no BOM and no NUL (e.g. Windows-1252). Sparse cases of that specific
// symptom may be missed; manual update_docs is the escape hatch.
func (s *Store) RepoHasInvalidEncoding(ctx context.Context, repoID int64) (bool, error) {
	var hit int
	err := s.db.QueryRowContext(ctx, byteSignatureQuery, repoID).Scan(&hit)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("byte-signature scan: %w", err)
	}

	rows, err := s.db.QueryContext(ctx,
		`SELECT content FROM documents WHERE repo_id = ? LIMIT 200`, repoID)
	if err != nil {
		return false, fmt.Errorf("utf8-validity sample query: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var content string
		if err := rows.Scan(&content); err != nil {
			return false, fmt.Errorf("utf8-validity sample scan: %w", err)
		}
		if !utf8.ValidString(content) {
			return true, nil
		}
	}
	return false, rows.Err()
}
