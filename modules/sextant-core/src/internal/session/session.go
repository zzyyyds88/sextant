// Package session owns the write-session locks.
//
// DESIGN §6.2 D12: one write session per display, at most two displays (main +
// one virtual), and global-configuration writes require that nobody else holds a
// session. Claims are leased (idle timeout), renewed by heartbeat, and can be
// reaped; roster/ban keeps a crashed agent from immediately re-claiming.
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Errors callers map to HTTP codes.
var (
	ErrConflict  = errors.New("session_conflict")
	ErrNotFound  = errors.New("session_not_found")
	ErrExclusive = errors.New("exclusive_required")
	ErrForbidden = errors.New("session_forbidden")
	ErrBanned    = errors.New("agent_banned")
)

// Session is one write claim.
type Session struct {
	ID         string `json:"id"`
	Token      string `json:"-"`
	Agent      string `json:"agent"`
	Display    int    `json:"display"`
	Created    int64  `json:"created_at"`
	LastSeen   int64  `json:"last_seen"`
	LastAction int64  `json:"last_action"`
	IdleSec    int    `json:"idle_sec"`
}

// Guard is a read-only observer that asked not to be reaped.
type Guard struct {
	Agent    string `json:"agent"`
	LastSeen int64  `json:"last_seen"`
}

// Config holds the timing rules.
type Config struct {
	IdleSec          int
	StaleSec         int
	LingerSec        int
	HeartbeatSec     int
	GuardStaleSec    int
	RosterBanSec     int
	MaxWriteSessions int
}

// Manager is the session store.
type Manager struct {
	mu       sync.Mutex
	cfg      Config
	path     string
	sessions map[string]*Session
	guards   map[string]*Guard
	roster   map[string]int64
}

// Open loads (or creates) the persisted session state.
func Open(path string, cfg Config) (*Manager, error) {
	if cfg.IdleSec <= 0 {
		cfg.IdleSec = 900
	}
	if cfg.MaxWriteSessions <= 0 {
		cfg.MaxWriteSessions = 2
	}
	m := &Manager{
		cfg: cfg, path: path,
		sessions: map[string]*Session{},
		guards:   map[string]*Guard{},
		roster:   map[string]int64{},
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return nil, fmt.Errorf("read sessions: %w", err)
	}
	var st struct {
		Sessions []*Session        `json:"sessions"`
		Guards   map[string]*Guard `json:"guards"`
		Roster   map[string]int64  `json:"roster"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse sessions: %w", err)
	}
	for _, s := range st.Sessions {
		if s != nil && s.ID != "" {
			m.sessions[s.ID] = s
		}
	}
	if st.Guards != nil {
		m.guards = st.Guards
	}
	if st.Roster != nil {
		m.roster = st.Roster
	}
	return m, nil
}

func randomID() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// Claim takes a write session for agent on display.
//
// A ban is checked first: an agent whose session was force-reaped cannot
// immediately re-claim, otherwise a crash loop starves everyone else.
func (m *Manager) Claim(ctx context.Context, agent string, display int) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().Unix()
	if until, banned := m.roster[agent]; banned && now < until {
		return nil, fmt.Errorf("%w: banned for another %ds", ErrBanned, until-now)
	}
	for _, s := range m.sessions {
		if s.Display == display {
			return nil, fmt.Errorf("%w: display %d already has a session (%s)", ErrConflict, display, s.Agent)
		}
	}
	if len(m.sessions) >= m.cfg.MaxWriteSessions {
		return nil, fmt.Errorf("%w: %d write sessions already active", ErrConflict, len(m.sessions))
	}
	id, err := randomID()
	if err != nil {
		return nil, err
	}
	token, err := randomID()
	if err != nil {
		return nil, err
	}
	s := &Session{
		ID: id, Token: token, Agent: agent, Display: display,
		Created: now, LastSeen: now, LastAction: now, IdleSec: m.cfg.IdleSec,
	}
	m.sessions[s.ID] = s
	if err := m.saveLocked(); err != nil {
		delete(m.sessions, s.ID)
		return nil, err
	}
	return s, nil
}

// Heartbeat renews a session.
func (m *Manager) Heartbeat(id, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return ErrNotFound
	}
	if s.Token != token {
		return ErrForbidden
	}
	s.LastSeen = time.Now().Unix()
	return m.saveLocked()
}

// Touch records activity for a session token. Heartbeats pass action=false:
// they keep the session alive but must not make an idle task look busy (D37).
func (m *Manager) Touch(token string, action bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().Unix()
	for _, s := range m.sessions {
		if s.Token != token {
			continue
		}
		s.LastSeen = now
		if action {
			s.LastAction = now
		}
		return m.saveLocked()
	}
	return ErrNotFound
}

// Release drops a session.
func (m *Manager) Release(id, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[id]
	if !ok {
		return ErrNotFound
	}
	if token != "" && s.Token != token {
		return ErrForbidden
	}
	delete(m.sessions, id)
	return m.saveLocked()
}

// ReleaseAll drops every session (used by the HUD stop button and by shutdown).
func (m *Manager) ReleaseAll(reason string) int {
	m.mu.Lock()
	defer m.mu.Unlock()
	n := len(m.sessions)
	m.sessions = map[string]*Session{}
	_ = m.saveLocked()
	return n
}

// Require checks that a session token is live.
func (m *Manager) Require(token string) (*Session, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.sessions {
		if s.Token == token {
			cp := *s
			return &cp, nil
		}
	}
	return nil, ErrNotFound
}

// ExclusiveAllowed returns nil only when this session is the sole active one.
func (m *Manager) ExclusiveAllowed(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.sessions) <= 1 {
		return nil
	}
	if _, ok := m.sessions[id]; ok && len(m.sessions) == 1 {
		return nil
	}
	return fmt.Errorf("%w: %d sessions active", ErrExclusive, len(m.sessions))
}

// Reap releases sessions that missed their heartbeat past the idle timeout and
// bans their agents for the roster window.
func (m *Manager) Reap() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().Unix()
	n := 0
	for id, s := range m.sessions {
		limit := int64(s.IdleSec)
		if limit <= 0 {
			limit = int64(m.cfg.IdleSec)
		}
		// D36: a lost heartbeat is recovered sooner than a merely idle session.
		if m.cfg.StaleSec > 0 && int64(m.cfg.StaleSec) < limit {
			limit = int64(m.cfg.StaleSec)
		}
		if now-s.LastSeen > limit {
			m.roster[s.Agent] = now + int64(m.cfg.RosterBanSec)
			delete(m.sessions, id)
			n++
		}
	}
	if n > 0 {
		_ = m.saveLocked()
	}
	return n
}

// ReapLoop releases expired sessions every interval until ctx is done. A
// non-positive interval disables the loop; the /v1/guard/reap endpoint remains.
func (m *Manager) ReapLoop(ctx context.Context, interval time.Duration, onReap func(int)) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n := m.Reap(); n > 0 && onReap != nil {
				onReap(n)
			}
		}
	}
}

// ReapZombie releases sessions whose heartbeat is still fresh but which have
// had no business activity for limit. Sessions already past the heartbeat limit
// belong to Reap, so this never double-counts. Zombies are not banned: the
// agent may simply have died and should be able to claim again (D37).
func (m *Manager) ReapZombie(limit time.Duration) int {
	if limit <= 0 {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	now := time.Now().Unix()
	staleLimit := int64(m.cfg.IdleSec)
	if m.cfg.StaleSec > 0 && int64(m.cfg.StaleSec) < staleLimit {
		staleLimit = int64(m.cfg.StaleSec)
	}
	zombieLimit := int64(limit.Seconds())
	n := 0
	for id, s := range m.sessions {
		if s.LastAction <= 0 {
			// Sessions persisted before D37 have no activity clock; let the next
			// business request set it instead of treating them as ancient.
			continue
		}
		if now-s.LastSeen > staleLimit {
			continue
		}
		if now-s.LastAction > zombieLimit {
			delete(m.sessions, id)
			n++
		}
	}
	if n > 0 {
		_ = m.saveLocked()
	}
	return n
}

// ZombieLoop runs ReapZombie every interval until ctx is done.
func (m *Manager) ZombieLoop(ctx context.Context, interval, limit time.Duration, onZombie func(int)) {
	if interval <= 0 || limit <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if n := m.ReapZombie(limit); n > 0 && onZombie != nil {
				onZombie(n)
			}
		}
	}
}

// List returns the active sessions.
func (m *Manager) List() []Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Session, 0, len(m.sessions))
	for _, s := range m.sessions {
		cp := *s
		cp.Token = ""
		out = append(out, cp)
	}
	return out
}

// Roster returns agent → ban-until.
func (m *Manager) Roster() map[string]int64 {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make(map[string]int64, len(m.roster))
	for k, v := range m.roster {
		out[k] = v
	}
	return out
}

// Count returns the number of active sessions.
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.sessions)
}

func (m *Manager) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return fmt.Errorf("session dir: %w", err)
	}
	st := struct {
		Sessions []*Session        `json:"sessions"`
		Guards   map[string]*Guard `json:"guards"`
		Roster   map[string]int64  `json:"roster"`
	}{
		Sessions: make([]*Session, 0, len(m.sessions)),
		Guards:   m.guards,
		Roster:   m.roster,
	}
	for _, s := range m.sessions {
		st.Sessions = append(st.Sessions, s)
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write sessions: %w", err)
	}
	return os.Rename(tmp, m.path)
}
