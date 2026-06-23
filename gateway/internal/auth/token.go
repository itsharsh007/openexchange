// Package auth implements password-backed user authentication for the gateway:
// bcrypt password hashing, a pluggable user store (Postgres in the full stack,
// in-memory for tests), and short-lived access / long-lived refresh JWTs.
//
// Token model (see docs/adr/0007-real-auth.md):
//   - access token  — HS256, sub=accountID, ~15 min. Sent as the Bearer token on
//     every request. Verified by middleware.JWTAuth.
//   - refresh token — HS256, sub=accountID, kind="refresh", ~7 days. Used ONLY at
//     POST /auth/refresh to mint a fresh access token (and a rotated refresh).
//
// The "kind" claim is what stops a refresh token being replayed as an access
// token: JWTAuth rejects kind=="refresh", and /auth/refresh requires it.
package auth

import (
	"errors"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

// KindRefresh marks a refresh token. Access/demo tokens carry no kind so they
// remain compatible with the existing JWTAuth verification.
const KindRefresh = "refresh"

// ErrWrongTokenKind is returned when a token is used outside its purpose (e.g. a
// refresh token presented as an access token, or vice versa).
var ErrWrongTokenKind = errors.New("auth: wrong token kind")

// Claims is the gateway's JWT claim set: the standard registered claims plus a
// "kind" discriminator.
type Claims struct {
	jwt.RegisteredClaims
	Kind string `json:"kind,omitempty"`
}

// TokenService mints and verifies HS256 access/refresh tokens with a shared secret.
type TokenService struct {
	secret     []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
	now        func() time.Time // injectable for tests
}

// NewTokenService builds a TokenService. accessTTL should be short (minutes);
// refreshTTL long (days).
func NewTokenService(secret string, accessTTL, refreshTTL time.Duration) *TokenService {
	return &TokenService{
		secret:     []byte(secret),
		accessTTL:  accessTTL,
		refreshTTL: refreshTTL,
		now:        time.Now,
	}
}

func (t *TokenService) mint(sub, kind string, ttl time.Duration) (string, error) {
	now := t.now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   sub,
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
		Kind: kind,
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(t.secret)
}

// Access mints a short-lived access token for accountID.
func (t *TokenService) Access(accountID string) (string, error) {
	return t.mint(accountID, "", t.accessTTL)
}

// Refresh mints a long-lived refresh token for accountID.
func (t *TokenService) Refresh(accountID string) (string, error) {
	return t.mint(accountID, KindRefresh, t.refreshTTL)
}

// AccessTTLSeconds is the access-token lifetime in whole seconds (for the client).
func (t *TokenService) AccessTTLSeconds() int { return int(t.accessTTL.Seconds()) }

// parse verifies the signature + expiry and returns the claims.
func (t *TokenService) parse(raw string) (*Claims, error) {
	var c Claims
	_, err := jwt.ParseWithClaims(raw, &c, func(tok *jwt.Token) (any, error) {
		// Reject any non-HMAC algorithm (defends against alg=none / key confusion).
		if _, ok := tok.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, jwt.ErrSignatureInvalid
		}
		return t.secret, nil
	})
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// VerifyRefresh checks a refresh token and returns its subject (account id). It
// fails if the token is invalid, expired, or not actually a refresh token.
func (t *TokenService) VerifyRefresh(raw string) (string, error) {
	c, err := t.parse(raw)
	if err != nil {
		return "", err
	}
	if c.Kind != KindRefresh {
		return "", ErrWrongTokenKind
	}
	return c.Subject, nil
}
