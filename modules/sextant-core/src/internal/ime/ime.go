// Package ime drives the APK's input method for text that accessibility cannot
// commit.
//
// Two paths exist because the device needs both. Accessibility
// ACTION_SET_TEXT works for ordinary EditText widgets and is per-display, so it
// stays parallel. WeChat's self-drawn input box is not an EditText: only the IME's
// InputConnection reaches it. That path is a single global resource (Android 13
// has one default IME session per user), so it is serialised here with a lease and
// reports `text_input_busy` instead of interleaving two callers' text.
package ime

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Errors callers map to API codes.
var (
	ErrBusy        = errors.New("text_input_busy")
	ErrDisabled    = errors.New("ime_disabled")
	ErrNotVerified = errors.New("text_input_unverified")
)

// Runner executes a device command.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// Target is the APK's IME.
type Target struct {
	// Service is the IME service component, e.g. pkg/.ime.RemoteImeService.
	Service string
	// Authority is the provider authority used for the control call.
	Authority string
	// Package owns the service.
	Package string
}

// Manager serialises IME use and owns the default-IME lease.
type Manager struct {
	Run Runner
	IME Target
	// Settle is how long to wait for the IME to become the active one.
	Settle time.Duration
	// PreviousFile persists the default IME that was in place before we took
	// over. Without it the old value lives only in this process, so a daemon
	// restart strands the user on our IME forever — which is exactly what
	// happened before this file existed.
	PreviousFile string

	mu             sync.Mutex
	busy           bool
	previous       string // the default IME to restore
	switched       bool
	lastRestored   string
	lastRestoreErr error
}

// New builds a manager.
func New(run Runner, target Target) *Manager {
	return &Manager{Run: run, IME: target, Settle: 8 * time.Second}
}

// Busy reports whether a text commit is in flight.
func (m *Manager) Busy() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.busy
}

// Active returns the current default IME, as reported by dumpsys.
func (m *Manager) Active(ctx context.Context) (string, error) {
	out, err := m.Run.Run(ctx, "dumpsys", "input_method")
	if err != nil {
		return "", fmt.Errorf("dumpsys input_method: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if !strings.Contains(line, "mCurMethodId=") {
			continue
		}
		rest := line[strings.Index(line, "mCurMethodId=")+len("mCurMethodId="):]
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			return "", nil
		}
		return fields[0], nil
	}
	return "", nil
}

// EnsureActive switches the default IME to the APK's and remembers the previous
// one so it can be put back.
//
// Both `ime enable` and `ime set` are required: on MIUI the service is disabled on
// first install, and `ime set` alone silently leaves the old IME as the default.
func (m *Manager) EnsureActive(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ensureActiveLocked(ctx)
}

func (m *Manager) ensureActiveLocked(ctx context.Context) error {
	cur, err := m.Active(ctx)
	if err != nil {
		return err
	}
	if sameComponent(cur, m.IME.Service) {
		return nil
	}
	if cur != "" && !m.switched {
		m.previous = cur
		if m.PreviousFile != "" {
			// Best effort: failing to persist must not stop text input, but it
			// does mean a restart cannot put the old IME back, so it is worth a
			// write attempt on every switch.
			_ = os.WriteFile(m.PreviousFile, []byte(cur+"\n"), 0o600)
		}
	}
	if _, err := m.Run.Run(ctx, "ime", "enable", m.IME.Service); err != nil {
		return fmt.Errorf("ime enable: %w", err)
	}
	if _, err := m.Run.Run(ctx, "ime", "set", m.IME.Service); err != nil {
		return fmt.Errorf("ime set: %w", err)
	}
	m.switched = true
	deadline := time.Now().Add(m.Settle)
	for time.Now().Before(deadline) {
		cur, err := m.Active(ctx)
		if err != nil {
			return err
		}
		if sameComponent(cur, m.IME.Service) {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return fmt.Errorf("%w: the default IME did not switch to %s", ErrDisabled, m.IME.Service)
}

// Restore puts the previous default IME back.
//
// A failure leaves the phone with a different keyboard than the user chose, so the
// caller must surface it rather than swallow it.
//
// It also works after a restart: when this process never switched the IME itself,
// the previous one is read back from PreviousFile. Restoring something we did not
// switch is still correct — that file only exists because we wrote it while taking
// the IME over.
func (m *Manager) Restore(ctx context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	prev := m.previous
	if prev == "" && m.PreviousFile != "" {
		if b, err := os.ReadFile(m.PreviousFile); err == nil {
			prev = strings.TrimSpace(string(b))
		}
	}
	if prev == "" {
		return nil
	}
	if sameComponent(prev, m.IME.Service) {
		_ = m.clearPrevious()
		m.switched = false
		return nil
	}
	// A user who deliberately picked another IME after we took over must win: if
	// the active IME is neither ours nor the recorded one, leave it alone.
	if cur, err := m.Active(ctx); err == nil && cur != "" &&
		!sameComponent(cur, m.IME.Service) && !sameComponent(cur, prev) {
		_ = m.clearPrevious()
		m.switched = false
		m.previous = ""
		return nil
	}
	if _, err := m.Run.Run(ctx, "ime", "set", prev); err != nil {
		return fmt.Errorf("restore ime %s: %w", prev, err)
	}
	m.lastRestored = prev
	m.switched = false
	m.previous = ""
	_ = m.clearPrevious()
	return nil
}

// LastRestored reports the IME handed back by the most recent Restore.
//
// The audit log says "the keyboard went back to X" only because this value is
// available: a restore the user cannot see in the log is indistinguishable from
// one that never happened.
func (m *Manager) LastRestored() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastRestored
}

// restoreAfterUse gives the device's keyboard back the moment a commit ends.
//
// It runs on its own context: the request that triggered the commit may already be
// cancelled (client hung up, deadline hit), and the restore must still happen —
// that is the whole point. Errors are logged by the caller via the audit trail, but
// nothing here can fail the commit that already succeeded.
func (m *Manager) restoreAfterUse() {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := m.Restore(ctx)
	m.mu.Lock()
	m.lastRestoreErr = err
	m.mu.Unlock()
}

// LastRestoreError reports the outcome of the most recent automatic restore, so the
// caller can surface "your keyboard may still be mine" instead of hiding it.
func (m *Manager) LastRestoreError() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.lastRestoreErr
}

// RestoreIfIdle puts the previous IME back when nobody is typing any more.
func (m *Manager) RestoreIfIdle(ctx context.Context) error {
	if m.Busy() {
		return nil
	}
	return m.Restore(ctx)
}

func (m *Manager) clearPrevious() error {
	if m.PreviousFile == "" {
		return nil
	}
	err := os.Remove(m.PreviousFile)
	if err != nil && os.IsNotExist(err) {
		return nil
	}
	return err
}

// Acquire takes the global text-input lease.
func (m *Manager) Acquire() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.busy {
		return ErrBusy
	}
	m.busy = true
	return nil
}

// Release frees the lease.
func (m *Manager) Release() {
	m.mu.Lock()
	m.busy = false
	m.mu.Unlock()
}

// Commit sends text through the APK's IME provider and returns its verdict.
//
// The provider answers with a result word rather than a boolean, because "committed
// but unverified" and "rejected" are different facts and a caller that retries on
// the wrong one can double-type a message.
func (m *Manager) Commit(ctx context.Context, text string, clear bool) (string, error) {
	if strings.TrimSpace(text) == "" && !clear {
		return "REJECTED", fmt.Errorf("nothing to commit")
	}
	if err := m.Acquire(); err != nil {
		return "", err
	}
	// The harness IME exists for the duration of one commit, not for the session.
	// Taking the device's keyboard over and keeping it until somebody releases a
	// session is how the user ends up typing their own messages into our invisible
	// 48dp view; they asked for "switch back as soon as the text is in".
	// Registered before Release so it runs AFTER it (defers are LIFO): the lease is
	// free while the restore talks to the device.
	defer m.restoreAfterUse()
	defer m.Release()
	if err := m.EnsureActive(ctx); err != nil {
		return "", err
	}
	// The provider decodes `arg` as Base64 (it carries arbitrary UTF-8 across a
	// shell argv, which cannot). Sending raw text is not an error the provider can
	// detect: it silently decodes to a shorter/garbage string and still answers
	// VERIFIED, so the caller sees success with nothing typed.
	if text == "" && clear {
		// Empty text cannot ride the `input` method: base64 of "" is "", and an
		// empty --arg is dropped by the shell before `content` sees it, which
		// surfaces as "No value for argument: --arg" and a clear that silently
		// never happens. The provider has a dedicated method for exactly this.
		out, err := m.Run.Run(ctx, "content", "call", "--uri", "content://"+m.IME.Authority,
			"--method", "clear")
		if err != nil {
			return "", fmt.Errorf("ime clear call: %w", err)
		}
		return parseResult(out)
	}
	args := []string{"content", "call", "--uri", "content://" + m.IME.Authority,
		"--method", "input", "--arg", base64.StdEncoding.EncodeToString([]byte(text))}
	if clear {
		args = append(args, "--extra", "clear:b:true")
	}
	out, err := m.Run.Run(ctx, args[0], args[1:]...)
	if err != nil {
		return "", fmt.Errorf("ime provider call: %w", err)
	}
	return parseResult(out)
}

// Clear empties the focused field through the IME.
func (m *Manager) Clear(ctx context.Context) (string, error) {
	return m.Commit(ctx, "", true)
}

// keyCodes maps the key names this API accepts onto Android key codes.
//
// The provider takes a numeric key code, not a name; a name is rejected as
// "bad_keycode" and would look like the IME is broken.
var keyCodes = map[string]int{
	"enter":  66,
	"back":   4,
	"delete": 67,
	"tab":    61,
	"escape": 111,
}

// Key sends a named key through the IME (enter, back, delete, tab, escape).
func (m *Manager) Key(ctx context.Context, name string) (string, error) {
	code, ok := keyCodes[strings.ToLower(strings.TrimSpace(name))]
	if !ok {
		return "", fmt.Errorf("unknown key %q (enter|back|delete|tab|escape)", name)
	}
	if err := m.Acquire(); err != nil {
		return "", err
	}
	// The harness IME exists for the duration of one commit, not for the session.
	// Taking the device's keyboard over and keeping it until somebody releases a
	// session is how the user ends up typing their own messages into our invisible
	// 48dp view; they asked for "switch back as soon as the text is in".
	// Registered before Release so it runs AFTER it (defers are LIFO): the lease is
	// free while the restore talks to the device.
	defer m.restoreAfterUse()
	defer m.Release()
	if err := m.EnsureActive(ctx); err != nil {
		return "", err
	}
	out, err := m.Run.Run(ctx, "content", "call", "--uri", "content://"+m.IME.Authority,
		"--method", "key", "--arg", strconv.Itoa(code))
	if err != nil {
		return "", fmt.Errorf("ime key call: %w", err)
	}
	return parseResult(out)
}

// parseResult extracts the provider's verdict from its bundle dump.
//
// `content call` prints a Bundle as `Bundle[{result=VERIFIED}]`, and older builds
// print just the word; both are accepted, but an unrecognised answer is an error
// rather than an assumed success.
func parseResult(out string) (string, error) {
	// Longest first: "ACCEPTED_UNVERIFIED" contains "VERIFIED", so a naive scan
	// would report a weaker result as a stronger one.
	known := []string{
		"ACCEPTED_UNVERIFIED", "COMMIT_OUTCOME_UNKNOWN", "INPUT_SESSION_CHANGED",
		"NO_INPUT_CONNECTION", "VERIFIED", "REJECTED",
	}
	for _, word := range known {
		if strings.Contains(out, word) {
			return word, nil
		}
	}
	return "", fmt.Errorf("%w: the provider answered %q", ErrNotVerified, strings.TrimSpace(out))
}

// sameComponent compares two component specs, accepting the short and long forms.
func sameComponent(a, b string) bool {
	if a == b {
		return true
	}
	return norm(a) == norm(b)
}

func norm(c string) string {
	pkg, cls, ok := strings.Cut(c, "/")
	if !ok {
		// A bare class name has no package part to shorten, so it cannot be
		// normalised any further.
		return c
	}
	switch {
	case strings.HasPrefix(cls, "."):
		cls = pkg + cls
	case !strings.Contains(cls, "."):
		cls = pkg + "." + cls
	}
	return pkg + "/" + cls
}
