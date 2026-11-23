// Package status owns the single status object that the daemon publishes.
//
// DESIGN D23: `state/status.json` (root truth) plus a copy under `app/` that is
// chowned to the APK's uid. The APK reads the copy and the PC reads
// GET /v1/status; both see byte-identical fields, so there is no second truth
// and nothing parses CLI text.
package status

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// SchemaVersion is the status schema; clients must reject a mismatch.
const SchemaVersion = 1

// Levels for per-capability state. The set is closed: anything else is a bug.
const (
	OK       = "OK"
	DEGRADED = "DEGRADED"
	MISSING  = "MISSING"
	PENDING  = "PENDING"
	DENIED   = "DENIED"
)

// Daemon is the process-level half.
type Daemon struct {
	PID       int    `json:"pid"`
	Version   string `json:"version"`
	UptimeSec int64  `json:"uptime_sec"`
	State     string `json:"state"`
}

// A11y is the accessibility keep-alive half.
type A11y struct {
	State     string `json:"state"`
	Detail    string `json:"detail"`
	Component string `json:"component"`
	// Compat is the zero-function service bound alongside the working one. It is
	// reported separately because "the automation service is fine" and "WeChat
	// will show us its tree" are different questions (DESIGN §9.3).
	Compat string `json:"compat,omitempty"`
}

// Background is the MIUI background policy half.
type Background struct {
	Autostart string `json:"autostart"`
	BgControl string `json:"bgcontrol"`
	Doze      bool   `json:"doze"`
	OK        bool   `json:"ok"`
}

// Perms maps a permission group name to its state.
type Perms map[string]string

// App is the client half.
type App struct {
	Package     string     `json:"package"`
	Installed   bool       `json:"installed"`
	VersionName string     `json:"version_name"`
	A11y        A11y       `json:"a11y"`
	Bg          Background `json:"bg"`
	Perms       Perms      `json:"perms"`
}

// Unlock is the credential/lock half.
type Unlock struct {
	Credential  string `json:"credential"`
	ScreenLock  bool   `json:"screen_locked"`
	LastAttempt string `json:"last_attempt"`
}

// Sessions is the session half.
type Sessions struct {
	Active int              `json:"active"`
	Max    int              `json:"max"`
	Roster map[string]int64 `json:"roster"`
}

// Error is a durable problem the daemon wants the user to see.
type Error struct {
	At      string `json:"at"`
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Status is the whole published object.
type Status struct {
	Schema    int      `json:"schema"`
	UpdatedAt string   `json:"updated_at"`
	Daemon    Daemon   `json:"daemon"`
	App       App      `json:"app"`
	Unlock    Unlock   `json:"unlock"`
	Sessions  Sessions `json:"sessions"`
	Errors    []Error  `json:"errors"`
}

// Publisher serialises writes so concurrent probes cannot interleave a torn
// JSON document. Writes are atomic (tmp + rename): a reader either sees the old
// complete file or the new complete file.
type Publisher struct {
	mu      sync.Mutex
	primary string   // root-only truth: <data>/state/status.json
	copies  []string // app-readable copies
	appUID  int
	cur     Status
}

// NewPublisher wires the publisher to its paths. copies may be empty on a
// device where the APK is not installed yet.
func NewPublisher(primary string, appUID int, copies ...string) *Publisher {
	return &Publisher{
		primary: primary,
		copies:  append([]string(nil), copies...),
		appUID:  appUID,
		cur: Status{
			Schema:   SchemaVersion,
			Sessions: Sessions{Roster: map[string]int64{}},
			App:      App{Perms: Perms{}},
			Errors:   []Error{},
		},
	}
}

// SetCopies repoints the app-readable copies (and their owner).
//
// The daemon learns the app uid only after the package manager is up, so the
// publisher is created first and told the real location later.
func (p *Publisher) SetCopies(copies []string, appUID int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.copies = append([]string(nil), copies...)
	p.appUID = appUID
}

// Current returns a copy of the last published status.
func (p *Publisher) Current() Status {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cur
}

// Update mutates the status atomically from the caller's point of view and
// publishes the result.
func (p *Publisher) Update(fn func(*Status)) (Status, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	fn(&p.cur)
	p.cur.Schema = SchemaVersion
	p.cur.UpdatedAt = time.Now().Format(time.RFC3339)
	if p.cur.App.Perms == nil {
		p.cur.App.Perms = Perms{}
	}
	if p.cur.Sessions.Roster == nil {
		p.cur.Sessions.Roster = map[string]int64{}
	}
	if p.cur.Errors == nil {
		p.cur.Errors = []Error{}
	}
	if err := p.writeLocked(p.primary, p.cur, 0); err != nil {
		return p.cur, err
	}
	var firstErr error
	for _, c := range p.copies {
		if err := p.writeLocked(c, p.cur, p.appUID); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return p.cur, firstErr
}

// AddError records a durable error, keeping the list bounded.
func (p *Publisher) AddError(code, msg string) {
	p.Update(func(s *Status) {
		s.Errors = append(s.Errors, Error{
			At: time.Now().Format(time.RFC3339), Code: code, Message: msg,
		})
		const keep = 20
		if len(s.Errors) > keep {
			s.Errors = s.Errors[len(s.Errors)-keep:]
		}
	})
}

func (p *Publisher) writeLocked(path string, s Status, uid int) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("status dir %s: %w", filepath.Dir(path), err)
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal status: %w", err)
	}
	b = append(b, '\n')
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("write %s: %w", tmp, err)
	}
	if uid > 0 {
		if err := os.Chown(tmp, uid, uid); err != nil {
			_ = os.Remove(tmp)
			return fmt.Errorf("chown %s: %w", tmp, err)
		}
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("rename %s: %w", path, err)
	}
	return nil
}

// Parse decodes a status document and rejects an unknown schema.
func Parse(data []byte) (Status, error) {
	var s Status
	if err := json.Unmarshal(data, &s); err != nil {
		return s, fmt.Errorf("parse status: %w", err)
	}
	if s.Schema != SchemaVersion {
		return s, fmt.Errorf("status schema %d, this client understands %d", s.Schema, SchemaVersion)
	}
	return s, nil
}
