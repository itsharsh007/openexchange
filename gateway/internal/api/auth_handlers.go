package api

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/itsharsh007/openexchange/gateway/internal/auth"
)

// Password-backed auth handlers (signup / login / refresh). These are only wired
// when a UserStore + TokenService are configured (full stack with Postgres). The
// public link runs without them and keeps the anonymous /auth/demo guest path.
//
// On the wire every successful call returns the same envelope so the client has
// one code path:
//
//	{ "accessToken": "...", "refreshToken": "...", "accountId": "...", "expiresInSeconds": 900 }

const minPasswordLen = 8

// WithAuth attaches the user store + token service, enabling the auth routes.
func (s *Server) WithAuth(users auth.UserStore, tokens *auth.TokenService) *Server {
	s.users = users
	s.tokens = tokens
	return s
}

// authEnabled reports whether password auth is configured.
func (s *Server) authEnabled() bool { return s.users != nil && s.tokens != nil }

type credentials struct {
	Email    string `json:"email"`
	Password string `json:"password"`
}

// newAccountID mints a fresh, unguessable account identity for a new user.
func newAccountID() string {
	var b [9]byte
	_, _ = rand.Read(b[:])
	return "acct-" + hex.EncodeToString(b[:])
}

// handleSignup: POST /auth/signup — register a new user, then issue tokens so the
// client is logged in immediately.
func (s *Server) handleSignup(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	email := auth.NormalizeEmail(c.Email)
	if !auth.ValidEmail(email) {
		writeErr(w, http.StatusBadRequest, "invalid email address")
		return
	}
	if len(c.Password) < minPasswordLen {
		writeErr(w, http.StatusBadRequest, "password must be at least 8 characters")
		return
	}

	hash, err := auth.HashPassword(c.Password)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not hash password")
		return
	}
	u := auth.User{AccountID: newAccountID(), Email: email, PasswordHash: hash}
	if err := s.users.Create(r.Context(), u); err != nil {
		if errors.Is(err, auth.ErrEmailTaken) {
			writeErr(w, http.StatusConflict, "that email is already registered")
			return
		}
		writeErr(w, http.StatusInternalServerError, "could not create account")
		return
	}
	s.issueTokens(w, u.AccountID)
}

// handleLogin: POST /auth/login — verify credentials and issue tokens. The error
// is deliberately identical whether the email is unknown or the password is wrong,
// so it can't be used to enumerate registered emails.
func (s *Server) handleLogin(w http.ResponseWriter, r *http.Request) {
	var c credentials
	if err := json.NewDecoder(r.Body).Decode(&c); err != nil {
		writeErr(w, http.StatusBadRequest, "invalid JSON body")
		return
	}
	u, err := s.users.ByEmail(r.Context(), c.Email)
	if err != nil || !auth.CheckPassword(u.PasswordHash, c.Password) {
		writeErr(w, http.StatusUnauthorized, "invalid email or password")
		return
	}
	s.issueTokens(w, u.AccountID)
}

// handleRefresh: POST /auth/refresh — exchange a valid refresh token for a fresh
// access token. The refresh token is rotated (a new one is issued) on every use.
func (s *Server) handleRefresh(w http.ResponseWriter, r *http.Request) {
	var body struct {
		RefreshToken string `json:"refreshToken"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.RefreshToken == "" {
		writeErr(w, http.StatusBadRequest, "missing refreshToken")
		return
	}
	account, err := s.tokens.VerifyRefresh(body.RefreshToken)
	if err != nil {
		writeErr(w, http.StatusUnauthorized, "invalid or expired refresh token")
		return
	}
	s.issueTokens(w, account)
}

// issueTokens mints an access + refresh pair for accountID and writes the envelope.
func (s *Server) issueTokens(w http.ResponseWriter, accountID string) {
	access, err := s.tokens.Access(accountID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not mint token")
		return
	}
	refresh, err := s.tokens.Refresh(accountID)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "could not mint token")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"accessToken":      access,
		"refreshToken":     refresh,
		"accountId":        accountID,
		"expiresInSeconds": s.tokens.AccessTTLSeconds(),
	})
}
