package unlock

// SIM PIN unlock (DESIGN D14's second链).
//
// The SIM PIN prompt is a framework dialog that appears after boot before the
// keyguard; nothing else can dismiss it, so an unattended device with a SIM PIN is
// stuck until a human types it. The rules are the same shape as the pattern path:
// read the credential first, refuse when the remaining attempts are too few, and
// verify by re-reading the screen instead of assuming the keystrokes landed.
//
// This path cannot be exercised on a device whose SIM has no PIN (the real phone
// reports gsm.sim.state=LOADED, i.e. already unlocked); it is written to the
// documented contract and reports unsupported_lock_type when there is no prompt.

import (
	"context"
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"sextant-core/internal/ui"
)

// ErrSIMLowAttempts is returned when the SIM has one try left: typing a wrong PIN
// then bricks the SIM until the PUK is entered, so refusal is the safe answer.
var ErrSIMLowAttempts = errors.New("sim_pin_low_attempts")

// ErrSIMFailed is returned when the PIN was rejected.
var ErrSIMFailed = errors.New("sim_pin_failed")

// simPromptMarkers identify the SIM PIN dialog in a UI dump.
var simPromptMarkers = []string{"simPinEntry", "SIM 卡 PIN", "SIM card PIN", "SIM PIN"}

// simAttemptsRE matches MIUI's remaining-attempts hint ("还可以尝试 2 次").
// The number may come before or after the phrase ("3 attempts left" vs
// "还可以尝试 3 次"), so both orders are matched.
var simAttemptsRE = regexp.MustCompile(
	`(?:(?:还可以尝试|剩余|remaining)[^0-9]{0,4}(\d+))|(?:(\d+)[^0-9]{0,3}(?:次|attempts? left))`)

// simPINRE accepts the 4–8 digit PIN a SIM can have.
var simPINRE = regexp.MustCompile(`^\d{4,8}$`)

// SIMPresent reports whether a SIM PIN prompt is on screen.
func (m *Manager) SIMPresent(ctx context.Context) (bool, error) {
	xmlText, _, err := m.dump(ctx, "simpin-probe")
	if err != nil {
		return false, err
	}
	return hasAny(xmlText, simPromptMarkers), nil
}

// EnsureSIMUnlocked enters the SIM PIN when the device is asking for one.
//
// It returns (false, nil) when no prompt is present: that is the normal case on a
// SIM without a PIN and must not be reported as a failure.
func (m *Manager) EnsureSIMUnlocked(ctx context.Context) (bool, error) {
	if m.SIMSecret == "" {
		return false, nil
	}
	if m.ReleaseUI != nil {
		m.ReleaseUI(ctx)
	}
	xmlText, _, err := m.dump(ctx, "simpin")
	if err != nil {
		return false, err
	}
	if !hasAny(xmlText, simPromptMarkers) {
		return false, nil
	}
	// Constraint: never spend the last attempt automatically.
	if left, ok := remainingSIMAttempts(xmlText); ok && left <= 1 {
		return true, fmt.Errorf("%w: %d attempt(s) left", ErrSIMLowAttempts, left)
	}
	pin, err := m.readSIMPin()
	if err != nil {
		return true, err
	}

	nodes := ui.Parse(xmlText)
	if field, ok := findSimField(nodes); ok {
		x, y := field.Center()
		if _, err := m.Probe.Run.Run(ctx, "input", "tap", strconv.Itoa(x), strconv.Itoa(y)); err != nil {
			return true, err
		}
		time.Sleep(600 * time.Millisecond)
	}
	for _, r := range pin {
		if _, err := m.Probe.Run.Run(ctx, "input", "keyevent", "KEYCODE_"+string(r)); err != nil {
			return true, err
		}
		time.Sleep(120 * time.Millisecond)
	}
	time.Sleep(400 * time.Millisecond)
	if _, err := m.Probe.Run.Run(ctx, "input", "keyevent", "KEYCODE_ENTER"); err != nil {
		return true, err
	}
	// Verify by re-reading: a PIN dialog that is still up means the entry failed.
	deadline := time.Now().Add(6 * time.Second)
	for time.Now().Before(deadline) {
		xml2, _, err := m.dump(ctx, "simpin-after")
		if err != nil {
			return true, err
		}
		if !hasAny(xml2, simPromptMarkers) {
			return true, nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return true, fmt.Errorf("%w: the PIN dialog is still showing", ErrSIMFailed)
}

// readSIMPin reads the optional SIM PIN, never logging or returning it upward.
func (m *Manager) readSIMPin() (string, error) {
	data, err := os.ReadFile(m.SIMSecret)
	if err != nil {
		if os.IsNotExist(err) {
			return "", ErrCredentialMissing
		}
		return "", fmt.Errorf("read sim pin: %w", err)
	}
	pin := strings.TrimSpace(string(data))
	if pin == "" {
		return "", ErrCredentialMissing
	}
	if !simPINRE.MatchString(pin) {
		return "", fmt.Errorf("%w: sim-pin must be 4-8 digits", ErrUnsupported)
	}
	return pin, nil
}

// SIMCredentialPresent reports whether a SIM PIN is configured, without its value.
func (m Manager) SIMCredentialPresent() bool {
	if m.SIMSecret == "" {
		return false
	}
	st, err := os.Stat(m.SIMSecret)
	return err == nil && st.Size() > 0
}

func hasAny(hay string, needles []string) bool {
	for _, n := range needles {
		if strings.Contains(hay, n) {
			return true
		}
	}
	return false
}

// remainingSIMAttempts extracts the remaining-attempts count when the ROM shows it.
func remainingSIMAttempts(xmlText string) (int, bool) {
	m := simAttemptsRE.FindStringSubmatch(xmlText)
	if len(m) != 3 {
		return 0, false
	}
	raw := m[1]
	if raw == "" {
		raw = m[2]
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return 0, false
	}
	return n, true
}

// findSimField locates the PIN input box.
func findSimField(nodes []ui.Node) (ui.Node, bool) {
	for _, n := range nodes {
		id := n.Attrs["resource-id"]
		if strings.Contains(id, "simPinEntry") || strings.Contains(id, "pin") &&
			strings.Contains(n.Attrs["class"], "EditText") {
			return n, true
		}
	}
	for _, n := range nodes {
		if strings.Contains(n.Attrs["class"], "EditText") {
			return n, true
		}
	}
	return ui.Node{}, false
}

// dump is the shared UI dump helper (the pattern path uses it too).
func (m *Manager) dump(ctx context.Context, name string) (string, []ui.Node, error) {
	return m.Dumper.Dump(ctx, name)
}
