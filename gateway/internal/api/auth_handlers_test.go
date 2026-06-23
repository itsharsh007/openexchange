package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/itsharsh007/openexchange/gateway/internal/auth"
	"github.com/itsharsh007/openexchange/gateway/internal/config"
	"github.com/itsharsh007/openexchange/gateway/internal/engine"
	"github.com/itsharsh007/openexchange/gateway/internal/middleware"
	"github.com/itsharsh007/openexchange/gateway/internal/ws"
)

const authSecret = "test-secret"

func newAuthServer() *Server {
	srv := NewServer(
		&config.Config{JWTSecret: authSecret, EngineTimeout: time.Second},
		engine.NewMockClient(), nil, ws.NewHub(), &capturePub{}, AllowAllGate{},
	)
	return srv.WithAuth(auth.NewMemoryStore(), auth.NewTokenService(authSecret, 15*time.Minute, 24*time.Hour))
}

func post(t *testing.T, h http.HandlerFunc, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	h(w, httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body)))
	return w
}

type tokenResp struct {
	AccessToken      string `json:"accessToken"`
	RefreshToken     string `json:"refreshToken"`
	AccountID        string `json:"accountId"`
	ExpiresInSeconds int    `json:"expiresInSeconds"`
}

func decodeTokens(t *testing.T, w *httptest.ResponseRecorder) tokenResp {
	t.Helper()
	var r tokenResp
	if err := json.NewDecoder(w.Body).Decode(&r); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return r
}

func TestSignupIssuesTokensAndLoginWorks(t *testing.T) {
	srv := newAuthServer()

	w := post(t, srv.handleSignup, `{"email":"trader@example.com","password":"hunter2hunter"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("signup status = %d, want 200 (%s)", w.Code, w.Body.String())
	}
	signed := decodeTokens(t, w)
	if signed.AccessToken == "" || signed.RefreshToken == "" || !strings.HasPrefix(signed.AccountID, "acct-") {
		t.Fatalf("signup response missing fields: %+v", signed)
	}

	// Correct login returns tokens for the SAME account.
	w = post(t, srv.handleLogin, `{"email":"trader@example.com","password":"hunter2hunter"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("login status = %d, want 200", w.Code)
	}
	if decodeTokens(t, w).AccountID != signed.AccountID {
		t.Error("login returned a different account than signup")
	}

	// The minted access token must authenticate against the shared secret.
	jwtAuth := middleware.NewJWTAuth(authSecret)
	var got string
	h := jwtAuth.Middleware(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		got = middleware.AccountID(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "/account", nil)
	req.Header.Set("Authorization", "Bearer "+signed.AccessToken)
	h.ServeHTTP(httptest.NewRecorder(), req)
	if got != signed.AccountID {
		t.Errorf("access token authenticated as %q, want %q", got, signed.AccountID)
	}
}

func TestLoginFailuresAreGeneric(t *testing.T) {
	srv := newAuthServer()
	post(t, srv.handleSignup, `{"email":"a@b.co","password":"password123"}`)

	wrong := post(t, srv.handleLogin, `{"email":"a@b.co","password":"nope"}`)
	missing := post(t, srv.handleLogin, `{"email":"ghost@b.co","password":"password123"}`)
	if wrong.Code != http.StatusUnauthorized || missing.Code != http.StatusUnauthorized {
		t.Fatalf("want 401 for both, got %d and %d", wrong.Code, missing.Code)
	}
	// Both failures must look identical so emails can't be enumerated.
	if wrong.Body.String() != missing.Body.String() {
		t.Errorf("login errors differ (enumeration risk): %q vs %q", wrong.Body.String(), missing.Body.String())
	}
}

func TestSignupValidation(t *testing.T) {
	srv := newAuthServer()
	cases := map[string]struct {
		body string
		want int
	}{
		"bad email":     {`{"email":"not-an-email","password":"password123"}`, http.StatusBadRequest},
		"short password": {`{"email":"x@y.co","password":"short"}`, http.StatusBadRequest},
		"bad json":       {`{`, http.StatusBadRequest},
	}
	for name, c := range cases {
		if w := post(t, srv.handleSignup, c.body); w.Code != c.want {
			t.Errorf("%s: status = %d, want %d", name, w.Code, c.want)
		}
	}

	// Duplicate email → 409.
	post(t, srv.handleSignup, `{"email":"dup@y.co","password":"password123"}`)
	if w := post(t, srv.handleSignup, `{"email":"dup@y.co","password":"password123"}`); w.Code != http.StatusConflict {
		t.Errorf("duplicate signup status = %d, want 409", w.Code)
	}
}

func TestRefreshRotatesAndAccessRejectedAsRefresh(t *testing.T) {
	srv := newAuthServer()
	signed := decodeTokens(t, post(t, srv.handleSignup, `{"email":"r@b.co","password":"password123"}`))

	// A valid refresh token mints a fresh access token.
	w := post(t, srv.handleRefresh, `{"refreshToken":"`+signed.RefreshToken+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("refresh status = %d, want 200 (%s)", w.Code, w.Body.String())
	}
	if decodeTokens(t, w).AccessToken == "" {
		t.Error("refresh did not return a new access token")
	}

	// An ACCESS token must not work at the refresh endpoint.
	if w := post(t, srv.handleRefresh, `{"refreshToken":"`+signed.AccessToken+`"}`); w.Code != http.StatusUnauthorized {
		t.Errorf("access token accepted at /auth/refresh: status %d", w.Code)
	}

	// A REFRESH token must not pass the access middleware.
	jwtAuth := middleware.NewJWTAuth(authSecret)
	blocked := true
	h := jwtAuth.Middleware(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { blocked = false }))
	req := httptest.NewRequest(http.MethodGet, "/account", nil)
	req.Header.Set("Authorization", "Bearer "+signed.RefreshToken)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if !blocked || rec.Code != http.StatusUnauthorized {
		t.Errorf("refresh token passed access middleware (status %d)", rec.Code)
	}
}
