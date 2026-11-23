package httpx

import (
	"net/http"

	"sextant-core/internal/token"
)

// Authenticator validates a bearer token and enforces its scopes.
type Authenticator struct {
	Store *token.Store
	// OnAuthorized runs after a bearer token and its scopes are accepted. The
	// daemon uses it to record session activity; it must not write a response.
	OnAuthorized func(*http.Request)
}

// Record is the authenticated identity.
type Record struct {
	Agent     string
	LocalOnly bool
	Scopes    []string
}

// Authenticate validates the bearer token and the loopback restriction shared by
// Require and RequireAny. It writes the error response itself when it fails.
func (a *Authenticator) authenticate(w http.ResponseWriter, r *http.Request) (*token.Record, bool) {
	tok := Bearer(r)
	if tok == "" {
		WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "missing bearer token", nil)
		return nil, false
	}
	rec, err := a.Store.Authenticate(tok)
	if err != nil {
		WriteError(w, http.StatusUnauthorized, CodeUnauthorized, "invalid token", err)
		return nil, false
	}
	if rec.LocalOnly && !IsLoopback(r) {
		WriteError(w, http.StatusForbidden, CodeLoopbackOnly, "this token is loopback-only", nil)
		return nil, false
	}
	return rec, true
}

// Require wraps h so that it only runs for an authenticated token holding every
// scope in want. localOnly tokens are rejected from non-loopback peers.
func (a *Authenticator) Require(want []string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec, ok := a.authenticate(w, r)
		if !ok {
			return
		}
		for _, scope := range want {
			if !token.HasScope(rec, scope) {
				WriteError(w, http.StatusForbidden, CodeForbidden,
					"token lacks required scope: "+scope, nil)
				return
			}
		}
		if a.OnAuthorized != nil {
			a.OnAuthorized(r)
		}
		h(w, r)
	}
}

// RequireAny is Require with OR semantics: the token must hold at least one of
// the listed scopes. It exists for state that is legitimately visible to more
// than one client identity — /v1/hud is read by ordinary PC tokens and by the
// overlay's own hud token, without merging their scope sets.
func (a *Authenticator) RequireAny(want []string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec, ok := a.authenticate(w, r)
		if !ok {
			return
		}
		for _, scope := range want {
			if token.HasScope(rec, scope) {
				if a.OnAuthorized != nil {
					a.OnAuthorized(r)
				}
				h(w, r)
				return
			}
		}
		WriteError(w, http.StatusForbidden, CodeForbidden,
			"token lacks a required scope", nil)
	}
}
