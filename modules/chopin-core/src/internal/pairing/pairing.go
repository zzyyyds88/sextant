// Package pairing implements the one-time, adb-mediated first authentication
// (DESIGN D24).
//
// Flow:
//  1. `chopin-core ctl pair new --agent pc` creates a code, prints it once to
//     stdout, and stores only its hash with a TTL.
//  2. The PC calls POST /v1/pair with the code; the daemon trades it for a long
//     lived API token and deletes the code.
//  3. Codes are single use, expire, and lock out after repeated failures.
package pairing

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const (
	// CodeTTL is how long a freshly minted code stays usable.
	CodeTTL = 5 * time.Minute
	// MaxFailures locks pairing after this many wrong codes, so a code cannot
	// be brute forced over the LAN.
	MaxFailures = 3
)

// State is what gets persisted.
type State struct {
	Hash      string `json:"hash"`
	Agent     string `json:"agent"`
	CreatedAt int64  `json:"created_at"`
	ExpiresAt int64  `json:"expires_at"`
	Failures  int    `json:"failures"`
	Locked    bool   `json:"locked"`
}

// Manager owns the on-disk pairing state.
type Manager struct {
	mu    sync.Mutex
	path  string
	state State
}

// Open loads the pairing state (missing file means "no pending pairing").
func Open(path string) (*Manager, error) {
	m := &Manager{path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil
		}
		return nil, fmt.Errorf("read pairing state: %w", err)
	}
	if err := json.Unmarshal(data, &m.state); err != nil {
		return nil, fmt.Errorf("parse pairing state: %w", err)
	}
	return m, nil
}

func randomCode() (string, error) {
	buf := make([]byte, 9) // 18 hex chars
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("random: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func hash(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

// New mints a code for agent and returns it in plaintext exactly once.
func (m *Manager) New(agent string) (string, State, error) {
	if agent == "" {
		return "", State{}, fmt.Errorf("agent is required")
	}
	code, err := randomCode()
	if err != nil {
		return "", State{}, err
	}
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = State{
		Hash:      hash(code),
		Agent:     agent,
		CreatedAt: now.Unix(),
		ExpiresAt: now.Add(CodeTTL).Unix(),
	}
	if err := m.saveLocked(); err != nil {
		return "", State{}, err
	}
	return code, m.state, nil
}

// Redeem validates code and, on success, clears the pending pairing.
//
// It re-reads the file first: the code is minted by `ctl pair new`, a different
// process from the daemon, so an in-memory copy taken at start-up is always
// stale. Without this, pairing looks broken in the only flow that matters (mint
// on the device via adb, redeem over the network).
func (m *Manager) Redeem(code string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reloadLocked()
	if m.state.Hash == "" {
		return "", fmt.Errorf("no pairing is pending; run 'chopin-core ctl pair new' on the device")
	}
	if m.state.Locked {
		return "", fmt.Errorf("pairing is locked after %d failed attempts; start a new pairing", MaxFailures)
	}
	if time.Now().Unix() > m.state.ExpiresAt {
		return "", fmt.Errorf("pairing code expired; start a new pairing")
	}
	if subtle.ConstantTimeCompare([]byte(m.state.Hash), []byte(hash(code))) != 1 {
		m.state.Failures++
		if m.state.Failures >= MaxFailures {
			m.state.Locked = true
		}
		_ = m.saveLocked()
		return "", fmt.Errorf("wrong pairing code (%d/%d)", m.state.Failures, MaxFailures)
	}
	agent := m.state.Agent
	m.state = State{}
	if err := m.saveLocked(); err != nil {
		return "", err
	}
	return agent, nil
}

func (m *Manager) reloadLocked() {
	data, err := os.ReadFile(m.path)
	if err != nil {
		if os.IsNotExist(err) {
			m.state = State{}
		}
		return
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return // a corrupt file is a real problem; keep the last known state
	}
	m.state = st
}

// Pending reports whether a usable pairing exists (for status/diagnostics).
func (m *Manager) Pending() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state.Hash != "" && !m.state.Locked && time.Now().Unix() <= m.state.ExpiresAt
}

func (m *Manager) saveLocked() error {
	if err := os.MkdirAll(filepath.Dir(m.path), 0o700); err != nil {
		return fmt.Errorf("pairing dir: %w", err)
	}
	data, err := json.MarshalIndent(m.state, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal pairing: %w", err)
	}
	data = append(data, '\n')
	tmp := m.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return fmt.Errorf("write pairing: %w", err)
	}
	if err := os.Rename(tmp, m.path); err != nil {
		return fmt.Errorf("rename pairing: %w", err)
	}
	return nil
}
