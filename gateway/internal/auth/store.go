package auth

import (
	"context"
	"errors"
	"regexp"
	"strings"
)

// User is a registered account. AccountID is the stable identity used everywhere
// downstream (orders, ledger, JWT subject); Email is the login handle.
type User struct {
	AccountID    string
	Email        string
	PasswordHash string
}

// Store errors. Handlers translate these to HTTP status codes; they deliberately
// do NOT leak which of email/password was wrong (see handleLogin).
var (
	ErrEmailTaken = errors.New("auth: email already registered")
	ErrNoUser     = errors.New("auth: no such user")
)

// UserStore persists users. The Postgres implementation backs the full stack; the
// in-memory one backs tests. The public link runs with NO store (guest only).
type UserStore interface {
	// Create inserts a new user. Returns ErrEmailTaken if the email already exists.
	Create(ctx context.Context, u User) error
	// ByEmail looks a user up by (normalized) email. Returns ErrNoUser if absent.
	ByEmail(ctx context.Context, email string) (User, error)
}

var emailRe = regexp.MustCompile(`^[^@\s]+@[^@\s]+\.[^@\s]+$`)

// NormalizeEmail lower-cases and trims so lookups are case-insensitive.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// ValidEmail reports whether email is plausibly an address (basic shape check).
func ValidEmail(email string) bool {
	return emailRe.MatchString(email)
}
