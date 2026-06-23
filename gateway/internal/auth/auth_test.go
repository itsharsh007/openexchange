package auth

import (
	"context"
	"testing"
	"time"
)

func TestPasswordHashAndCheck(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	if hash == "correct horse battery staple" {
		t.Fatal("password stored in plaintext")
	}
	if !CheckPassword(hash, "correct horse battery staple") {
		t.Error("correct password rejected")
	}
	if CheckPassword(hash, "wrong password") {
		t.Error("wrong password accepted")
	}
}

func TestAccessTokenRoundTrips(t *testing.T) {
	ts := NewTokenService("test-secret", 15*time.Minute, 7*24*time.Hour)
	tok, err := ts.Access("acct-1")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	c, err := ts.parse(tok)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if c.Subject != "acct-1" {
		t.Errorf("subject = %q, want acct-1", c.Subject)
	}
	// An access token is NOT a refresh token.
	if _, err := ts.VerifyRefresh(tok); err != ErrWrongTokenKind {
		t.Errorf("access token accepted as refresh: %v", err)
	}
}

func TestRefreshTokenVerifies(t *testing.T) {
	ts := NewTokenService("test-secret", 15*time.Minute, 7*24*time.Hour)
	tok, err := ts.Refresh("acct-2")
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	sub, err := ts.VerifyRefresh(tok)
	if err != nil {
		t.Fatalf("verify refresh: %v", err)
	}
	if sub != "acct-2" {
		t.Errorf("subject = %q, want acct-2", sub)
	}
}

func TestExpiredTokenRejected(t *testing.T) {
	ts := NewTokenService("test-secret", 15*time.Minute, 7*24*time.Hour)
	ts.now = func() time.Time { return time.Now().Add(-time.Hour) } // mint in the past
	tok, _ := ts.Access("acct-3")
	ts.now = time.Now
	if _, err := ts.parse(tok); err == nil {
		t.Error("expired token accepted")
	}
}

func TestWrongSecretRejected(t *testing.T) {
	a := NewTokenService("secret-a", time.Minute, time.Hour)
	b := NewTokenService("secret-b", time.Minute, time.Hour)
	tok, _ := a.Access("acct-4")
	if _, err := b.parse(tok); err == nil {
		t.Error("token verified under the wrong secret")
	}
}

func TestMemoryStore(t *testing.T) {
	s := NewMemoryStore()
	ctx := context.Background()
	u := User{AccountID: "acct-5", Email: "Alice@Example.com", PasswordHash: "h"}
	if err := s.Create(ctx, u); err != nil {
		t.Fatalf("create: %v", err)
	}
	// Email lookup is case-insensitive.
	got, err := s.ByEmail(ctx, "alice@example.com")
	if err != nil {
		t.Fatalf("by email: %v", err)
	}
	if got.AccountID != "acct-5" {
		t.Errorf("account = %q, want acct-5", got.AccountID)
	}
	// Duplicate (case-folded) email is rejected.
	if err := s.Create(ctx, User{AccountID: "x", Email: "ALICE@example.com"}); err != ErrEmailTaken {
		t.Errorf("duplicate email err = %v, want ErrEmailTaken", err)
	}
	// Unknown email.
	if _, err := s.ByEmail(ctx, "bob@example.com"); err != ErrNoUser {
		t.Errorf("missing user err = %v, want ErrNoUser", err)
	}
}

func TestEmailValidation(t *testing.T) {
	for _, ok := range []string{"a@b.co", "user.name@sub.example.com"} {
		if !ValidEmail(ok) {
			t.Errorf("%q should be valid", ok)
		}
	}
	for _, bad := range []string{"", "no-at", "a@b", "a@@b.co", "a b@c.co"} {
		if ValidEmail(bad) {
			t.Errorf("%q should be invalid", bad)
		}
	}
}
