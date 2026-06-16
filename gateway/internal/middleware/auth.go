package middleware

import (
	"context"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// ctxKey is an unexported type to avoid context-key collisions with other pkgs.
type ctxKey string

const accountIDKey ctxKey = "account_id"

// JWTAuth verifies a bearer token signed with the shared HMAC secret and, on
// success, stashes the caller's account id (the `sub` claim) into the request
// context for downstream handlers.
//
// WHY HMAC (HS256) here: the secret is shared between gateway and issuer in this
// simulation; it's simple and fast. For multi-issuer / public deployments you'd
// switch to RS256/asymmetric keys so the gateway only needs the public key.
type JWTAuth struct {
	secret []byte
}

// NewJWTAuth builds an authenticator from the configured secret.
func NewJWTAuth(secret string) *JWTAuth { return &JWTAuth{secret: []byte(secret)} }

// Middleware enforces a valid bearer token. The health endpoint must be wired
// WITHOUT this wrapper so liveness checks need no credentials.
func (a *JWTAuth) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw := bearerToken(r)
		if raw == "" {
			unauthorized(w, "missing bearer token")
			return
		}

		token, err := jwt.Parse(raw, func(t *jwt.Token) (any, error) {
			// CRITICAL: reject any algorithm we did not expect. Without this
			// check an attacker could send alg=none or a key-confusion token.
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, jwt.ErrSignatureInvalid
			}
			return a.secret, nil
		})
		if err != nil || !token.Valid {
			unauthorized(w, "invalid token")
			return
		}

		// Pull the subject (account id) so handlers can attribute orders.
		sub, _ := token.Claims.GetSubject()
		ctx := context.WithValue(r.Context(), accountIDKey, sub)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// AccountID returns the authenticated account id from the context, if present.
func AccountID(ctx context.Context) string {
	if v, ok := ctx.Value(accountIDKey).(string); ok {
		return v
	}
	return ""
}

func bearerToken(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return h[len(prefix):]
	}
	return ""
}

func unauthorized(w http.ResponseWriter, msg string) {
	w.Header().Set("WWW-Authenticate", "Bearer")
	http.Error(w, `{"error":"`+msg+`"}`, http.StatusUnauthorized)
}
