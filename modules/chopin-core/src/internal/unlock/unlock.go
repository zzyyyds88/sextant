// Package unlock wakes the device and draws the screen pattern.
//
// Four constraints come straight from device failures and are encoded as code,
// not comments:
//
//  1. Wakefulness is judged by `dumpsys display` mScreenState, never by
//     mWakefulness: MIUI reports Awake with the panel OFF, and then the UI dump
//     contains only a scrim, which used to be misreported as "pattern view
//     missing" (a wrong diagnosis pointing at the pattern code).
//  2. Swiping up to reveal the bouncer is retried up to three times: MIUI answers
//     one swipe in three different ways (notification shade / bouncer / screen
//     off mid-gesture).
//  3. The credential is read BEFORE touching the screen: without it we must fail
//     with credential_missing and not leave the phone on a half-open keyguard.
//  4. "Locked" means the keyguard is showing. `dumpsys user` reports
//     RUNNING_UNLOCKED from the first unlock after boot, so it cannot answer
//     whether an unlock is needed right now.
package unlock

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"chopin-core/internal/status"
	"chopin-core/internal/sysprobe"
	"chopin-core/internal/ui"
)

// Sentinels callers map to HTTP error codes.
var (
	ErrCredentialMissing = errors.New("credential_missing")
	ErrUnsupported       = errors.New("unsupported_lock_type")
	ErrFailed            = errors.New("unlock_failed")
)

// Manager performs unlocks.
type Manager struct {
	Probe  sysprobe.Probe
	Dumper ui.Dumper
	Secret string
	// SIMSecret is the optional SIM PIN file ("" disables the SIM path).
	SIMSecret string
	Attempt   int
	Cooldown  time.Duration
	// ReleaseUI is called before the first UI dump. `uiautomator dump` cannot
	// run while another process holds UiAutomation (the dump is killed), so the
	// caller uses this to hand the session over.
	ReleaseUI func(ctx context.Context)

	lastAttempt time.Time
	// SIMUnlocked records that this call cleared the SIM PIN prompt.
	SIMUnlocked bool
}

// CredentialPresent reports whether a pattern file with content exists. It never
// returns the value.
func (m Manager) CredentialPresent() bool {
	st, err := os.Stat(m.Secret)
	if err != nil {
		return false
	}
	return st.Size() > 0
}

// Result is the outcome of an unlock attempt.
type Result struct {
	Unlocked bool
	Detail   string
}

// readPattern returns the pattern digits. The value never leaves this package: it
// is not logged, not returned, not put on a command line.
func (m Manager) readPattern() (string, error) {
	data, err := os.ReadFile(m.Secret)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrCredentialMissing
		}
		return "", fmt.Errorf("read credential: %w", err)
	}
	pat := strings.TrimSpace(string(data))
	if pat == "" {
		return "", ErrCredentialMissing
	}
	for _, r := range pat {
		if r < '1' || r > '9' {
			return "", fmt.Errorf("%w: credential must be digits 1-9", ErrUnsupported)
		}
	}
	return pat, nil
}

// IsUnlocked reports whether the device is past the keyguard right now.
func (m *Manager) IsUnlocked(ctx context.Context) bool {
	if showing, err := m.Probe.KeyguardShowing(ctx); err == nil {
		return !showing
	}
	ok, err := m.Probe.UserUnlocked(ctx)
	return err == nil && ok
}

// EnsureUnlocked unlocks the device if it needs it. Idempotent: an unlocked
// device returns immediately without touching anything.
func (m *Manager) EnsureUnlocked(ctx context.Context) (Result, error) {
	if m.IsUnlocked(ctx) {
		return Result{Unlocked: true, Detail: "already unlocked"}, nil
	}
	// The SIM PIN dialog comes before the keyguard: while it is up, no pattern
	// view exists, so it must be cleared first (DESIGN D14's order).
	if m.SIMCredentialPresent() {
		if prompted, err := m.EnsureSIMUnlocked(ctx); err != nil {
			return Result{}, err
		} else if prompted {
			m.SIMUnlocked = true
		}
	}
	// Constraint 3: read the credential first.
	pattern, err := m.readPattern()
	if err != nil {
		return Result{}, err
	}
	if m.ReleaseUI != nil {
		m.ReleaseUI(ctx)
	}
	if m.Cooldown > 0 && !m.lastAttempt.IsZero() && time.Since(m.lastAttempt) < m.Cooldown {
		return Result{}, fmt.Errorf("unlock cooling down for %s", m.Cooldown-time.Since(m.lastAttempt))
	}
	attempts := m.Attempt
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		m.lastAttempt = time.Now()
		lastErr = m.attempt(ctx, pattern)
		if lastErr == nil {
			return Result{Unlocked: true, Detail: "unlocked"}, nil
		}
		if errors.Is(lastErr, ErrCredentialMissing) || errors.Is(lastErr, ErrUnsupported) {
			return Result{}, lastErr
		}
	}
	return Result{}, lastErr
}

func (m *Manager) attempt(ctx context.Context, pattern string) error {
	if err := m.wake(ctx); err != nil {
		return err
	}
	revealed := false
	for i := 0; i < 3; i++ {
		if m.IsUnlocked(ctx) {
			return nil
		}
		if err := m.reveal(ctx); err != nil {
			return fmt.Errorf("reveal gesture: %w", err)
		}
		if !sleepCtx(ctx, 2*time.Second) {
			return ctx.Err()
		}
		if m.patternVisible(ctx) {
			revealed = true
			break
		}
		if state, err := m.Probe.DisplayState(ctx); err == nil && state == "OFF" {
			if err := m.wake(ctx); err != nil {
				return err
			}
		}
	}
	if !revealed {
		// MIUI keeps the secure bouncer hidden until dismissal is explicitly
		// requested; a swipe alone may only expand notifications.
		if _, err := m.Probe.Run.Run(ctx, "wm", "dismiss-keyguard"); err == nil {
			if !sleepCtx(ctx, 1500*time.Millisecond) {
				return ctx.Err()
			}
			revealed = m.patternVisible(ctx)
		}
	}
	if !revealed {
		return fmt.Errorf("%w: pattern view not found after reveal attempts", ErrUnsupported)
	}
	if err := m.drawPattern(ctx, pattern); err != nil {
		return err
	}
	if !sleepCtx(ctx, 2500*time.Millisecond) {
		return ctx.Err()
	}
	if m.IsUnlocked(ctx) {
		return nil
	}
	return fmt.Errorf("%w: pattern drawn but device stayed locked", ErrFailed)
}

func (m *Manager) wake(ctx context.Context) error {
	for i := 0; i < 3; i++ {
		state, err := m.Probe.DisplayState(ctx)
		if err != nil {
			return fmt.Errorf("read display state: %w", err)
		}
		if state != "OFF" {
			return nil
		}
		if _, err := m.Probe.Run.Run(ctx, "input", "keyevent", "KEYCODE_WAKEUP"); err != nil {
			return fmt.Errorf("wake keyevent: %w", err)
		}
		if !sleepCtx(ctx, 700*time.Millisecond) {
			return ctx.Err()
		}
	}
	state, err := m.Probe.DisplayState(ctx)
	if err != nil {
		return fmt.Errorf("read display state: %w", err)
	}
	if state == "OFF" {
		return fmt.Errorf("%w: screen did not turn on", ErrFailed)
	}
	return nil
}

// reveal performs the up-swipe with explicit motion events.
//
// `input swipe` is not equivalent: MIUI's keyguard watches the event stream, and a
// swipe whose intermediate points are missing is often treated as a notification
// pull. The four-point sequence is what the device-verified implementation used.
func (m *Manager) reveal(ctx context.Context) error {
	points := [][2]string{{"DOWN", "2100"}, {"MOVE", "1500"}, {"MOVE", "900"}, {"UP", "900"}}
	for _, step := range points {
		if _, err := m.Probe.Run.Run(ctx, "input", "motionevent", step[0], "540", step[1]); err != nil {
			return err
		}
		if !sleepCtx(ctx, 70*time.Millisecond) {
			return ctx.Err()
		}
	}
	return nil
}

// patternVisible asks the UI dump whether the lock pattern view exists.
func (m *Manager) patternVisible(ctx context.Context) bool {
	_, nodes, err := m.Dumper.Dump(ctx, "unlock-probe")
	if err != nil {
		return false
	}
	_, ok := ui.FindByResourceID(nodes, "lockPatternView")
	return ok
}

// patternBounds reads the lock pattern view bounds from the live tree.
func (m *Manager) patternBounds(ctx context.Context) ([4]int, error) {
	_, nodes, err := m.Dumper.Dump(ctx, "unlock-bounds")
	if err != nil {
		return [4]int{}, err
	}
	node, ok := ui.FindByResourceID(nodes, "lockPatternView")
	if !ok {
		return [4]int{}, fmt.Errorf("%w: lockPatternView not present", ErrUnsupported)
	}
	b := node.Bounds
	if b[2] <= b[0] || b[3] <= b[1] {
		return b, fmt.Errorf("%w: empty pattern bounds", ErrUnsupported)
	}
	return b, nil
}

// drawPattern draws the pattern with explicit motion events.
//
// A continuous swipe is not enough: LockPatternView samples pointer positions, so
// a swipe can skip a cell between its two end points. Emitting a MOVE per dot
// reproduces what a finger does, and is what the device-verified implementation
// used.
func (m *Manager) drawPattern(ctx context.Context, pattern string) error {
	b, err := m.patternBounds(ctx)
	if err != nil {
		return err
	}
	w, h := b[2]-b[0], b[3]-b[1]
	point := func(digit rune) (int, int) {
		n := int(digit - '1')
		row, col := n/3, n%3
		x := b[0] + int((float64(col)+0.5)*float64(w)/3)
		y := b[1] + int((float64(row)+0.5)*float64(h)/3)
		return x, y
	}
	x, y := point(rune(pattern[0]))
	if _, err := m.Probe.Run.Run(ctx, "input", "motionevent", "DOWN",
		fmt.Sprintf("%d", x), fmt.Sprintf("%d", y)); err != nil {
		return err
	}
	for _, d := range pattern[1:] {
		if !sleepCtx(ctx, 70*time.Millisecond) {
			return ctx.Err()
		}
		px, py := point(d)
		if _, err := m.Probe.Run.Run(ctx, "input", "motionevent", "MOVE",
			fmt.Sprintf("%d", px), fmt.Sprintf("%d", py)); err != nil {
			return err
		}
	}
	lastX, lastY := point(rune(pattern[len(pattern)-1]))
	_, err = m.Probe.Run.Run(ctx, "input", "motionevent", "UP",
		fmt.Sprintf("%d", lastX), fmt.Sprintf("%d", lastY))
	return err
}

// StatusValue renders the credential/lock half of the published status.
func StatusValue(present bool, locked bool, last string) status.Unlock {
	cred := "absent"
	if present {
		cred = "present"
	}
	return status.Unlock{Credential: cred, ScreenLock: locked, LastAttempt: last}
}

// sleepCtx waits d, returning false when the context ended first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}
