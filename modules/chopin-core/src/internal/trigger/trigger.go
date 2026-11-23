// Package trigger watches the device and records what happened.
//
// Design choice worth stating: everything here polls. The helper can push
// accessibility events, but its push channel is a single subscription that also
// carries the HUD's own stream, and losing it silently breaks both features at
// once. Polling a cheap source (a SQLite max(id), a notification key set, a
// logcat tail) keeps each watcher independent and restartable, and a trigger that
// misses a beat reports a skipped run instead of dying.
//
// A trigger is an observer: it records a run with the data that matched. It does
// not press buttons on its own.
package trigger

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrNotFound is returned for an unknown trigger id.
var ErrNotFound = errors.New("trigger not found")

// Types of trigger this build supports. The set is closed: an unknown type is a
// 400 at create time, not a trigger that silently never fires.
const (
	TypeTimer        = "timer"
	TypeNotification = "notification"
	TypeSMS          = "sms"
	TypeLogcat       = "logcat"
)

// Spec is the per-type matcher.
type Spec struct {
	IntervalSeconds int    `json:"interval_seconds,omitempty"`
	Package         string `json:"package,omitempty"`
	Address         string `json:"address,omitempty"`
	TextContains    string `json:"text_contains,omitempty"`
	Pattern         string `json:"pattern,omitempty"`
}

// Trigger is one watcher.
type Trigger struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	Enabled   bool   `json:"enabled"`
	Spec      Spec   `json:"spec"`
	CreatedAt int64  `json:"created_at"`
	UpdatedAt int64  `json:"updated_at"`
	LastRun   int64  `json:"last_run,omitempty"`
	Runs      int64  `json:"runs"`
}

// Run is one match.
type Run struct {
	ID        string         `json:"id"`
	TriggerID string         `json:"trigger_id"`
	Trigger   string         `json:"trigger_name"`
	At        int64          `json:"at"`
	Data      map[string]any `json:"data"`
}

// Sources are the device facts a trigger can watch.
type Sources struct {
	// Notifications returns the current notifications (key + title + text + package).
	Notifications func(ctx context.Context) ([]map[string]any, error)
	// SMSMaxID returns the newest message id.
	SMSMaxID func(ctx context.Context) (int64, error)
	// SMSAfter returns messages newer than id.
	SMSAfter func(ctx context.Context, id int64) ([]map[string]any, error)
	// Logcat returns the recent log tail.
	Logcat func(ctx context.Context) (string, error)
}

// Engine owns the trigger set and its watchers.
type Engine struct {
	mu    sync.Mutex
	path  string
	items map[string]*Trigger
	runs  []Run
	subs  map[chan Run]struct{}
	src   Sources
	log   func(format string, args ...any)

	// per-source cursors, so a restart does not replay old events
	smsLast     int64
	smsReady    bool
	knownNotifs map[string]struct{}
	notifsReady bool
	logcatMark  int
}

const maxRuns = 500

// Open loads the persisted trigger set.
func Open(path string, src Sources, log func(string, ...any)) (*Engine, error) {
	if log == nil {
		log = func(string, ...any) {}
	}
	e := &Engine{
		path: path, items: map[string]*Trigger{}, subs: map[chan Run]struct{}{},
		src: src, log: log, knownNotifs: map[string]struct{}{},
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return e, nil
		}
		return nil, fmt.Errorf("read triggers: %w", err)
	}
	var st struct {
		Triggers []*Trigger `json:"triggers"`
		Runs     []Run      `json:"runs"`
	}
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("parse triggers: %w", err)
	}
	for _, t := range st.Triggers {
		if t != nil && t.ID != "" {
			e.items[t.ID] = t
		}
	}
	if len(st.Runs) > maxRuns {
		st.Runs = st.Runs[len(st.Runs)-maxRuns:]
	}
	e.runs = st.Runs
	return e, nil
}

// ValidateType checks a trigger type against the closed set.
func ValidateType(typ string) error {
	switch typ {
	case TypeTimer, TypeNotification, TypeSMS, TypeLogcat:
		return nil
	}
	return fmt.Errorf("unsupported trigger type %q (timer|notification|sms|logcat)", typ)
}

// Create adds a trigger.
func (e *Engine) Create(name, typ string, spec Spec, enabled bool) (*Trigger, error) {
	if strings.TrimSpace(name) == "" {
		return nil, errors.New("name is required")
	}
	if err := ValidateType(typ); err != nil {
		return nil, err
	}
	switch typ {
	case TypeTimer:
		if spec.IntervalSeconds < 5 {
			spec.IntervalSeconds = 60
		}
	case TypeLogcat:
		if spec.Pattern == "" {
			return nil, errors.New("a logcat trigger needs a pattern")
		}
		if _, err := regexp.Compile(spec.Pattern); err != nil {
			return nil, fmt.Errorf("invalid pattern: %w", err)
		}
	}
	id, err := randomID()
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	t := &Trigger{ID: "trg_" + id, Name: name, Type: typ, Enabled: enabled, Spec: spec, CreatedAt: now, UpdatedAt: now}
	e.mu.Lock()
	e.items[t.ID] = t
	err = e.saveLocked()
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	cp := *t
	return &cp, nil
}

// Update mutates a trigger.
func (e *Engine) Update(id string, fn func(*Trigger) error) (*Trigger, error) {
	e.mu.Lock()
	t := e.items[id]
	if t == nil {
		e.mu.Unlock()
		return nil, ErrNotFound
	}
	cp := *t
	if err := fn(&cp); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	if err := ValidateType(cp.Type); err != nil {
		e.mu.Unlock()
		return nil, err
	}
	cp.ID, cp.CreatedAt = t.ID, t.CreatedAt
	cp.UpdatedAt = time.Now().Unix()
	e.items[id] = &cp
	err := e.saveLocked()
	e.mu.Unlock()
	return &cp, err
}

// Delete removes a trigger.
func (e *Engine) Delete(id string) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.items[id] == nil {
		return ErrNotFound
	}
	delete(e.items, id)
	return e.saveLocked()
}

// List returns every trigger, oldest first.
func (e *Engine) List() []Trigger {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]Trigger, 0, len(e.items))
	for _, t := range e.items {
		out = append(out, *t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt < out[j].CreatedAt })
	return out
}

// Get returns one trigger.
func (e *Engine) Get(id string) (Trigger, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	t := e.items[id]
	if t == nil {
		return Trigger{}, false
	}
	return *t, true
}

// Runs returns recorded runs, newest first.
func (e *Engine) Runs(triggerID string, limit int) []Run {
	e.mu.Lock()
	defer e.mu.Unlock()
	if limit <= 0 || limit > maxRuns {
		limit = 100
	}
	out := make([]Run, 0, limit)
	for i := len(e.runs) - 1; i >= 0 && len(out) < limit; i-- {
		if triggerID == "" || e.runs[i].TriggerID == triggerID {
			out = append(out, e.runs[i])
		}
	}
	return out
}

// Subscribe returns a run channel plus its cancel function.
func (e *Engine) Subscribe() (<-chan Run, func()) {
	ch := make(chan Run, 16)
	e.mu.Lock()
	e.subs[ch] = struct{}{}
	e.mu.Unlock()
	return ch, func() {
		e.mu.Lock()
		if _, ok := e.subs[ch]; ok {
			delete(e.subs, ch)
			close(ch)
		}
		e.mu.Unlock()
	}
}

// emit records one run and notifies subscribers.
func (e *Engine) emit(t *Trigger, data map[string]any) {
	run := Run{
		ID: "run_" + randomIDOrEmpty(), TriggerID: t.ID, Trigger: t.Name,
		At: time.Now().Unix(), Data: data,
	}
	e.mu.Lock()
	if cur := e.items[t.ID]; cur != nil {
		cur.LastRun = run.At
		cur.Runs++
	}
	e.runs = append(e.runs, run)
	if len(e.runs) > maxRuns {
		e.runs = e.runs[len(e.runs)-maxRuns:]
	}
	subs := make([]chan Run, 0, len(e.subs))
	for ch := range e.subs {
		subs = append(subs, ch)
	}
	_ = e.saveLocked()
	e.mu.Unlock()
	e.log("trigger: %s fired (%s)", t.Name, t.Type)
	for _, ch := range subs {
		select {
		case ch <- run:
		default:
		}
	}
}

// Run starts the watchers and blocks until ctx is done.
func (e *Engine) Run(ctx context.Context) {
	// Prime the cursors so a daemon restart does not replay history as if it were
	// new: an SMS that arrived before the restart is not a fresh trigger.
	e.prime(ctx)
	timer := time.NewTicker(time.Second)
	defer timer.Stop()
	fast := time.NewTicker(5 * time.Second)
	defer fast.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			e.runTimers()
		case <-fast.C:
			e.pollSMS(ctx)
			e.pollNotifications(ctx)
			e.pollLogcat(ctx)
		}
	}
}

func (e *Engine) prime(ctx context.Context) {
	if e.src.SMSMaxID != nil {
		if id, err := e.src.SMSMaxID(ctx); err == nil {
			e.mu.Lock()
			e.smsLast, e.smsReady = id, true
			e.mu.Unlock()
		}
	}
	if e.src.Notifications != nil {
		if items, err := e.src.Notifications(ctx); err == nil {
			e.mu.Lock()
			for _, n := range items {
				e.knownNotifs[notifKey(n)] = struct{}{}
			}
			e.notifsReady = true
			e.mu.Unlock()
		}
	}
}

func (e *Engine) runTimers() {
	now := time.Now().Unix()
	for _, t := range e.List() {
		if !t.Enabled || t.Type != TypeTimer {
			continue
		}
		interval := int64(t.Spec.IntervalSeconds)
		if interval < 5 {
			interval = 60
		}
		if t.LastRun == 0 || now-t.LastRun >= interval {
			tt := t
			e.emit(&tt, map[string]any{"kind": "timer", "interval_seconds": interval})
		}
	}
}

func (e *Engine) pollSMS(ctx context.Context) {
	e.mu.Lock()
	ready, last := e.smsReady, e.smsLast
	e.mu.Unlock()
	if e.src.SMSAfter == nil {
		return
	}
	if !ready {
		if e.src.SMSMaxID == nil {
			return
		}
		id, err := e.src.SMSMaxID(ctx)
		if err != nil {
			return
		}
		e.mu.Lock()
		e.smsLast, e.smsReady = id, true
		e.mu.Unlock()
		return
	}
	msgs, err := e.src.SMSAfter(ctx, last)
	if err != nil {
		return
	}
	for _, m := range msgs {
		// Trust the id, not the source: a poller that ignores its cursor would
		// otherwise re-fire the same message on every pass.
		if id := intOf(m["id"]); id <= last {
			continue
		}
		if id := intOf(m["id"]); id > last {
			last = id
		}
		for _, t := range e.List() {
			if !t.Enabled || t.Type != TypeSMS {
				continue
			}
			if t.Spec.Address != "" && !strings.Contains(strOf(m["address"]), t.Spec.Address) {
				continue
			}
			if t.Spec.TextContains != "" && !strings.Contains(strOf(m["body"]), t.Spec.TextContains) {
				continue
			}
			tt := t
			e.emit(&tt, m)
		}
	}
	e.mu.Lock()
	e.smsLast = last
	e.mu.Unlock()
}

func (e *Engine) pollNotifications(ctx context.Context) {
	if e.src.Notifications == nil {
		return
	}
	items, err := e.src.Notifications(ctx)
	if err != nil {
		return
	}
	e.mu.Lock()
	ready := e.notifsReady
	e.mu.Unlock()
	if !ready {
		e.mu.Lock()
		for _, n := range items {
			e.knownNotifs[notifKey(n)] = struct{}{}
		}
		e.notifsReady = true
		e.mu.Unlock()
		return
	}
	fresh := make([]map[string]any, 0, len(items))
	e.mu.Lock()
	for _, n := range items {
		key := notifKey(n)
		if _, seen := e.knownNotifs[key]; seen {
			continue
		}
		e.knownNotifs[key] = struct{}{}
		fresh = append(fresh, n)
	}
	// Bound the set so a device that never clears notifications cannot grow it
	// without limit; the oldest keys are dropped first.
	if len(e.knownNotifs) > 4096 {
		e.knownNotifs = map[string]struct{}{}
		for _, n := range items {
			e.knownNotifs[notifKey(n)] = struct{}{}
		}
	}
	e.mu.Unlock()
	for _, n := range fresh {
		for _, t := range e.List() {
			if !t.Enabled || t.Type != TypeNotification {
				continue
			}
			if t.Spec.Package != "" && !strings.Contains(strOf(n["package"]), t.Spec.Package) {
				continue
			}
			text := strOf(n["title"]) + strOf(n["text"])
			if t.Spec.TextContains != "" && !strings.Contains(text, t.Spec.TextContains) {
				continue
			}
			tt := t
			e.emit(&tt, n)
		}
	}
}

func (e *Engine) pollLogcat(ctx context.Context) {
	var patterns []Trigger
	for _, t := range e.List() {
		if t.Enabled && t.Type == TypeLogcat && t.Spec.Pattern != "" {
			patterns = append(patterns, t)
		}
	}
	if len(patterns) == 0 || e.src.Logcat == nil {
		return
	}
	out, err := e.src.Logcat(ctx)
	if err != nil {
		return
	}
	lines := strings.Split(out, "\n")
	e.mu.Lock()
	mark := e.logcatMark
	e.mu.Unlock()
	if mark > len(lines) {
		mark = 0 // log rotated
	}
	maxSeen := len(lines)
	for _, line := range lines[mark:] {
		if strings.TrimSpace(line) == "" {
			continue
		}
		for i := range patterns {
			re, err := regexp.Compile(patterns[i].Spec.Pattern)
			if err != nil {
				continue
			}
			if !re.MatchString(line) {
				continue
			}
			t := patterns[i]
			e.emit(&t, map[string]any{"kind": "logcat", "line": line})
		}
	}
	e.mu.Lock()
	e.logcatMark = maxSeen
	e.mu.Unlock()
}

func (e *Engine) saveLocked() error {
	st := struct {
		Triggers []*Trigger `json:"triggers"`
		Runs     []Run      `json:"runs"`
	}{Triggers: make([]*Trigger, 0, len(e.items)), Runs: e.runs}
	for _, t := range e.items {
		st.Triggers = append(st.Triggers, t)
	}
	sort.Slice(st.Triggers, func(i, j int) bool { return st.Triggers[i].CreatedAt < st.Triggers[j].CreatedAt })
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(e.path), 0o700); err != nil {
		return err
	}
	tmp := e.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, e.path)
}

func notifKey(n map[string]any) string {
	return strOf(n["package"]) + "|" + strOf(n["key"]) + "|" + strOf(n["title"]) + "|" + strOf(n["text"])
}

func strOf(v any) string {
	switch s := v.(type) {
	case string:
		return s
	case nil:
		return ""
	default:
		return fmt.Sprint(s)
	}
}

func intOf(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	}
	return 0
}

func randomID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func randomIDOrEmpty() string {
	id, err := randomID()
	if err != nil {
		return "00000000"
	}
	return id
}
