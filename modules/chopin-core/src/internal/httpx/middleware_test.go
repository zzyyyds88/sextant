package httpx

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"chopin-core/internal/token"
)

func newAuth(t *testing.T) (*Authenticator, map[string]string) {
	t.Helper()
	store, err := token.Open(filepath.Join(t.TempDir(), "tokens.json"))
	if err != nil {
		t.Fatalf("open token store: %v", err)
	}
	tokens := map[string]string{}
	for name, scopes := range map[string][]string{
		"read":  {"read"},
		"hud":   {"hud"},
		"other": {"settings"},
	} {
		tok, _, err := store.Create(name, scopes, false)
		if err != nil {
			t.Fatalf("create %s token: %v", name, err)
		}
		tokens[name] = tok
	}
	return &Authenticator{Store: store}, tokens
}

func requestWith(auth *Authenticator, tok string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, "/v1/hud", nil)
	if tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	rec := httptest.NewRecorder()
	auth.RequireAny([]string{"read", "hud"}, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})(rec, req)
	return rec
}

func TestRequireAnyAcceptsEitherScope(t *testing.T) {
	auth, tokens := newAuth(t)
	for _, name := range []string{"read", "hud"} {
		if code := requestWith(auth, tokens[name]).Code; code != http.StatusNoContent {
			t.Fatalf("%s token: got %d, want %d", name, code, http.StatusNoContent)
		}
	}
}

func TestRequireAnyRejectsNeitherScopeAndMissingToken(t *testing.T) {
	auth, tokens := newAuth(t)
	if code := requestWith(auth, tokens["other"]).Code; code != http.StatusForbidden {
		t.Fatalf("unrelated scope: got %d, want %d", code, http.StatusForbidden)
	}
	if code := requestWith(auth, "").Code; code != http.StatusUnauthorized {
		t.Fatalf("missing token: got %d, want %d", code, http.StatusUnauthorized)
	}
}

func TestRequireStillRequiresEveryScope(t *testing.T) {
	auth, tokens := newAuth(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/hud", nil)
	req.Header.Set("Authorization", "Bearer "+tokens["read"])
	rec := httptest.NewRecorder()
	auth.Require([]string{"read", "hud"}, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("read-only token on AND route: got %d, want %d", rec.Code, http.StatusForbidden)
	}
}
