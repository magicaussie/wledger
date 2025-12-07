package auth

import (
	"context"
	"database/sql"
	"time"

	"github.com/tuxedocurly/wledger/internal/db"
)

// SQLCStore implements the scs.Store interface using sqlc queries from /internal/sql/queries
type SQLCStore struct {
	queries *db.Queries
	db      *sql.DB // Needed for transactions
}

func NewStore(q *db.Queries) *SQLCStore {
	return &SQLCStore{queries: q}
}

// Find returns the data for a given session token from the SQLCStore instance
// If the session token is not found or is expired, the returned exists flag will
// be set to false
func (s *SQLCStore) Find(token string) (b []byte, exists bool, err error) {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	session, err := s.queries.GetSession(ctx, token)
	if err == sql.ErrNoRows {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}

	// Helper: check expiry manually if the DB doesn't handle it
	if session.Expiry < float64(time.Now().Unix()) {
		return nil, false, nil
	}

	return session.Data, true, nil
}

// Commit adds a session token and data to the SQLCStore instance with the
// given expiry time. If the session token already exists, then data and
// expiry time are updated
func (s *SQLCStore) Commit(token string, b []byte, expiry time.Time) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	// Convert expiry time to Unix timestamp (float64) for SQLite REAL column
	expiryFloat := float64(expiry.Unix())

	err := s.queries.DeleteSession(ctx, token)
	if err != nil {
		return err
	}

	return s.queries.CreateSession(ctx, db.CreateSessionParams{
		Token:  token,
		Data:   b,
		Expiry: expiryFloat,
	})
}

// Delete removes a sesssion token and corresponding data from the SQLCStore
// instance
func (s *SQLCStore) Delete(token string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	return s.queries.DeleteSession(ctx, token)
}
