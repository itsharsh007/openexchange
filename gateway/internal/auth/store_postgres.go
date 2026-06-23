package auth

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/lib/pq"
)

// PostgresStore is a UserStore backed by the `users` table (created by the engine's
// Flyway migration V2__users.sql — the engine owns the schema; the gateway reads
// and writes this one table).
type PostgresStore struct {
	db *sql.DB
}

// NewPostgresStore wraps an open *sql.DB. The caller owns the pool's lifecycle.
func NewPostgresStore(db *sql.DB) *PostgresStore { return &PostgresStore{db: db} }

// OpenDB opens (but does not ping) a Postgres pool from a DATABASE_URL DSN.
func OpenDB(dsn string) (*sql.DB, error) {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, fmt.Errorf("auth: open postgres: %w", err)
	}
	db.SetMaxOpenConns(10)
	db.SetMaxIdleConns(5)
	return db, nil
}

func (p *PostgresStore) Create(ctx context.Context, u User) error {
	_, err := p.db.ExecContext(ctx,
		`INSERT INTO users (account_id, email, password_hash) VALUES ($1, $2, $3)`,
		u.AccountID, NormalizeEmail(u.Email), u.PasswordHash,
	)
	var pqErr *pq.Error
	if errors.As(err, &pqErr) && pqErr.Code == "23505" { // unique_violation
		return ErrEmailTaken
	}
	return err
}

func (p *PostgresStore) ByEmail(ctx context.Context, email string) (User, error) {
	var u User
	err := p.db.QueryRowContext(ctx,
		`SELECT account_id, email, password_hash FROM users WHERE email = $1`,
		NormalizeEmail(email),
	).Scan(&u.AccountID, &u.Email, &u.PasswordHash)
	if errors.Is(err, sql.ErrNoRows) {
		return User{}, ErrNoUser
	}
	return u, err
}
