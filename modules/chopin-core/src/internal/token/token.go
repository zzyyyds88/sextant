// Package token stores API tokens as hashes and answers authentication.
//
// Only the SHA-256 of a token is persisted. A stolen tokens.json therefore does
// not yield usable credentials, and `revoke` genuinely revokes.
package token

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

// Scopes that may be granted. A closed set: an unknown scope is a config error,
// not something to ignore.
var scopes = map[string]bool{
	"read": true, "ui": true, "input": true, "apps": true, "files": true,
	"settings": true, "unlock": true, "sms": true, "location": true,
	"notifications": true, "webrtc": true, "triggers": true, "display": true,
	// device: changing the phone's own hardware state — ending a call, muting the
	// microphone, routing audio, playing sound. Separate from `settings` because
	// these are not stored preferences: they are things a person in the room can
	// hear or notice, and a token that may edit a timeout should not automatically
	// be allowed to speak out loud.
	"device": true, "admin": true, "hud": true,
}

// DefaultScopes is what a PC agent gets unless it asks for less.
func DefaultScopes() []string {
	return []string{
		"read", "ui", "input", "apps", "files", "settings", "unlock",
		"sms", "location", "notifications", "webrtc", "triggers", "display",
		"device",
	}
}

// AppScopes is the APK's own token: status + HUD + the local unlock button.
func AppScopes() []string { return []string{"read", "ui", "hud"} }

// HUDSpecificScopes is the HUD overlay's token: SSE + stop only.
func HUDScopes() []string { return []string{"hud"} }

// Record is one issued token.
type Record struct {
	ID        string   `json:"id"`
	Agent     string   `json:"agent"`
	Hash      string   `json:"hash"`
	Scopes    []string `json:"scopes"`
	LocalOnly bool     `json:"local_only,omitempty"`
	CreatedAt int64    `json:"created_at"`
	LastUsed  int64    `json:"last_used,omitempty"`
	Revoked   bool     `json:"revoked,omitempty"`
}

type file struct {
	Tokens []*Record `json:"tokens"`
}

// Store is the persisted token set.
type Store struct {
	mu       sync.Mutex
	path     string
	tokens   map[string]*Record
	loadedAt time.Time
}

// Open loads (or initialises) the store at path.
func Open(path string) (*Store, error) {
	s := &Store{path: path, tokens: map[string]*Record{}}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read token store: %w", err)
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return nil, fmt.Errorf("parse token store: %w", err)
	}
	for _, t := range f.Tokens {
		if t != nil && t.ID != "" {
			s.tokens[t.ID] = t
		}
	}
	if st, statErr := os.Stat(path); statErr == nil {
		s.loadedAt = st.ModTime()
	}
	return s, nil
}

// Hash is the only thing ever written to disk for a token.
func Hash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func newToken() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("random: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

// NormaliseScopes validates a requested scope list and removes duplicates.
func NormaliseScopes(in []string) ([]string, error) {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if !scopes[s] {
			return nil, fmt.Errorf("unknown scope %q", s)
		}
		if seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no scopes requested")
	}
	sort.Strings(out)
	return out, nil
}

// Create issues a token and returns the plaintext exactly once. Callers must
// hand it straight to the client (pairing) and never log it.
func (s *Store) Create(agent string, scopeNames []string, localOnly bool) (string, *Record, error) {
	sc, err := NormaliseScopes(scopeNames)
	if err != nil {
		return "", nil, err
	}
	tok, err := newToken()
	if err != nil {
		return "", nil, err
	}
	idBytes := make([]byte, 8)
	if _, err := rand.Read(idBytes); err != nil {
		return "", nil, fmt.Errorf("random: %w", err)
	}
	rec := &Record{
		ID:        hex.EncodeToString(idBytes),
		Agent:     agent,
		Hash:      Hash(tok),
		Scopes:    sc,
		LocalOnly: localOnly,
		CreatedAt: time.Now().Unix(),
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.tokens[rec.ID] = rec
	if err := s.saveLocked(); err != nil {
		delete(s.tokens, rec.ID)
		return "", nil, err
	}
	return tok, rec, nil
}

// Authenticate maps a plaintext token to its record.
//
// The file is re-read when its mtime changed, so `ctl token revoke` and
// `ctl token rotate` (separate processes) take effect immediately instead of on
// the next daemon restart.
func (s *Store) Authenticate(tok string) (*Record, error) {
	if tok == "" {
		return nil, fmt.Errorf("empty token")
	}
	h := Hash(tok)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.reloadLocked()
	for _, rec := range s.tokens {
		if rec.Revoked {
			continue
		}
		if subtle.ConstantTimeCompare([]byte(rec.Hash), []byte(h)) == 1 {
			rec.LastUsed = time.Now().Unix()
			_ = s.saveLocked()
			return rec, nil
		}
	}
	return nil, fmt.Errorf("unknown token")
}

// HasScope reports whether rec carries every one of want.
func HasScope(rec *Record, want ...string) bool {
	have := map[string]bool{}
	for _, s := range rec.Scopes {
		have[s] = true
	}
	for _, w := range want {
		if !have[w] {
			return false
		}
	}
	return true
}

// Revoke removes every token for agent. Returns how many were revoked.
func (s *Store) Revoke(agent string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for id, rec := range s.tokens {
		if rec.Agent != agent || rec.Revoked {
			continue
		}
		rec.Revoked = true
		n++
		delete(s.tokens, id)
	}
	if n > 0 {
		if err := s.saveLocked(); err != nil {
			return n, err
		}
	}
	return n, nil
}

// Rotate replaces agent's token with a fresh one.
func (s *Store) Rotate(agent string) (string, *Record, error) {
	var prev *Record
	s.mu.Lock()
	for _, rec := range s.tokens {
		if rec.Agent == agent && !rec.Revoked {
			prev = rec
			break
		}
	}
	s.mu.Unlock()
	scopeNames := DefaultScopes()
	localOnly := false
	if prev != nil {
		scopeNames = prev.Scopes
		localOnly = prev.LocalOnly
		if _, err := s.Revoke(agent); err != nil {
			return "", nil, err
		}
	}
	return s.Create(agent, scopeNames, localOnly)
}

// List returns the token metadata (never the plaintext or the hash).
func (s *Store) List() []Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Record, 0, len(s.tokens))
	for _, rec := range s.tokens {
		cp := *rec
		cp.Hash = ""
		out = append(out, cp)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

// Count reports how many live tokens exist (used to decide whether pairing is
// needed at all).
func (s *Store) Count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := 0
	for _, rec := range s.tokens {
		if !rec.Revoked {
			n++
		}
	}
	return n
}

// Find returns the first live record for agent, or nil.
func (s *Store) Find(agent string) *Record {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, rec := range s.tokens {
		if rec.Agent == agent && !rec.Revoked {
			cp := *rec
			return &cp
		}
	}
	return nil
}

// reloadLocked re-reads the store when the file on disk is newer than the copy
// in memory. A missing or unparsable file keeps the current set: losing every
// token because of a transient write would lock the user out of their own phone.
func (s *Store) reloadLocked() {
	st, err := os.Stat(s.path)
	if err != nil {
		return
	}
	if !s.loadedAt.IsZero() && !st.ModTime().After(s.loadedAt) {
		return
	}
	data, err := os.ReadFile(s.path)
	if err != nil {
		return
	}
	var f file
	if err := json.Unmarshal(data, &f); err != nil {
		return
	}
	next := map[string]*Record{}
	for _, rec := range f.Tokens {
		if rec != nil && rec.ID != "" {
			next[rec.ID] = rec
		}
	}
	s.tokens = next
	s.loadedAt = st.ModTime()
}

func (s *Store) saveLocked() error {
	f := file{Tokens: make([]*Record, 0, len(s.tokens))}
	for _, rec := range s.tokens {
		f.Tokens = append(f.Tokens, rec)
	}
	sort.Slice(f.Tokens, func(i, j int) bool { return f.Tokens[i].CreatedAt < f.Tokens[j].CreatedAt })
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal tokens: %w", err)
	}
	data = append(data, '\n')
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return fmt.Errorf("token dir: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write tokens: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("rename tokens: %w", err)
	}
	if st, statErr := os.Stat(s.path); statErr == nil {
		s.loadedAt = st.ModTime()
	}
	return nil
}
