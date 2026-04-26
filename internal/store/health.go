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
          substr(CAST(content AS BLOB), 1, 3) = x'efbbbf'
          OR substr(CAST(content AS BLOB), 1, 2) = x'fffe'
          OR substr(CAST(content AS BLOB), 1, 2) = x'feff'
          OR instr(CAST(content AS BLOB), x'00') > 0
      )
    LIMIT 1`

// RepoHasInvalidEncoding reports whether any document row for repoID shows
// signs of an encoding the indexer should have rejected. Pass 1 checks all rows
// for forbidden BOM prefixes and embedded NUL bytes using byte semantics. Pass
// 2 checks all rows with utf8.Valid to catch legacy invalid UTF-8 with no BOM
// and no NUL.
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
		`SELECT CAST(content AS BLOB) FROM documents WHERE repo_id = ? ORDER BY id ASC`, repoID)
	if err != nil {
		return false, fmt.Errorf("utf8-validity scan query: %w", err)
	}
	defer rows.Close() //nolint:errcheck
	for rows.Next() {
		var content []byte
		if err := rows.Scan(&content); err != nil {
			return false, fmt.Errorf("utf8-validity scan row: %w", err)
		}
		if !utf8.Valid(content) {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, fmt.Errorf("utf8-validity scan iteration: %w", err)
	}
	return false, nil
}
