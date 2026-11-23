package token

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func newStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestTokenIsStoredHashedOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tokens.json")
	s, _ := Open(path)
	tok, _, err := s.Create("pc", DefaultScopes(), false)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), tok) {
		t.Fatal("plaintext token found on disk")
	}
	if !strings.Contains(string(data), Hash(tok)) {
		t.Fatal("hash not persisted")
	}
}

func TestAuthenticateAcceptsOnlyLiveTokens(t *testing.T) {
	s := newStore(t)
	tok, _, err := s.Create("pc", []string{"read"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(tok); err != nil {
		t.Fatalf("live token rejected: %v", err)
	}
	if _, err := s.Revoke("pc"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(tok); err == nil {
		t.Fatal("revoked token still accepted")
	}
}

func TestUnknownScopeIsRejected(t *testing.T) {
	if _, err := NormaliseScopes([]string{"read", "not_a_scope"}); err == nil {
		t.Fatal("unknown scope accepted")
	}
	if _, err := NormaliseScopes(nil); err == nil {
		t.Fatal("empty scope list accepted")
	}
}

func TestScopeCheckRequiresAll(t *testing.T) {
	s := newStore(t)
	tok, rec, err := s.Create("pc", []string{"read", "ui"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(tok); err != nil {
		t.Fatal(err)
	}
	if !HasScope(rec, "read") {
		t.Fatal("read scope missing")
	}
	if HasScope(rec, "read", "admin") {
		t.Fatal("admin granted without being asked for")
	}
}

func TestRotateReplacesTheOldToken(t *testing.T) {
	s := newStore(t)
	old, _, err := s.Create("pc", []string{"read"}, false)
	if err != nil {
		t.Fatal(err)
	}
	fresh, _, err := s.Rotate("pc")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Authenticate(old); err == nil {
		t.Fatal("old token still valid after rotate")
	}
	if _, err := s.Authenticate(fresh); err != nil {
		t.Fatalf("new token rejected: %v", err)
	}
}

func TestAppScopeIsMinimal(t *testing.T) {
	rec := &Record{Scopes: AppScopes()}
	if HasScope(rec, "admin") {
		t.Fatal("the app token must not carry admin")
	}
	if !HasScope(rec, "read") || !HasScope(rec, "hud") {
		t.Fatal("the app token needs read and hud")
	}
}
