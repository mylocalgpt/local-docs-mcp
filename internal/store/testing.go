//go:build integration

package store

import "database/sql"

// DB returns the underlying database connection for direct queries.
// Only available in integration test builds.
func (s *Store) DB() *sql.DB {
	return s.db
}
