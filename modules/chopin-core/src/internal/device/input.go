// Package device implements the device control and input-injection surface.
//
// Both halves are thin wrappers over framework commands, but they exist as typed
// functions so that argument shapes live in exactly one place and every call
// goes through the same runner (which redirects stdin, see sysprobe).
package device

import (
	"context"
	"fmt"

	"chopin-core/internal/sysprobe"
)

// Layer performs device actions.
type Layer struct {
	Probe sysprobe.Probe
}

// Tap sends a tap at (x, y) on the given display.
func (l Layer) Tap(ctx context.Context, x, y, display int) error {
	args := []string{"input"}
	if display > 0 {
		args = append(args, "-d", fmt.Sprintf("%d", display))
	}
	args = append(args, "tap", fmt.Sprintf("%d", x), fmt.Sprintf("%d", y))
	_, err := l.Probe.Run.Run(ctx, args[0], args[1:]...)
	return err
}

// Swipe drags from (x1,y1) to (x2,y2) over durationMS.
func (l Layer) Swipe(ctx context.Context, x1, y1, x2, y2, durationMS, display int) error {
	args := []string{"input"}
	if display > 0 {
		args = append(args, "-d", fmt.Sprintf("%d", display))
	}
	args = append(args, "swipe",
		fmt.Sprintf("%d", x1), fmt.Sprintf("%d", y1),
		fmt.Sprintf("%d", x2), fmt.Sprintf("%d", y2),
		fmt.Sprintf("%d", durationMS))
	_, err := l.Probe.Run.Run(ctx, args[0], args[1:]...)
	return err
}

// LongPress is a swipe that does not move.
func (l Layer) LongPress(ctx context.Context, x, y, durationMS, display int) error {
	return l.Swipe(ctx, x, y, x, y, durationMS, display)
}

// Key sends a key event. The name may be a KEYCODE_* constant or a number.
func (l Layer) Key(ctx context.Context, name string, display int) error {
	if !validKeyName(name) {
		return fmt.Errorf("invalid key name %q", name)
	}
	args := []string{"input"}
	if display > 0 {
		args = append(args, "-d", fmt.Sprintf("%d", display))
	}
	args = append(args, "keyevent", name)
	_, err := l.Probe.Run.Run(ctx, args[0], args[1:]...)
	return err
}

func validKeyName(name string) bool {
	if name == "" {
		return false
	}
	for _, r := range name {
		if !(r == '_' || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// Wake turns the panel on. Screen state is read from dumpsys display, never from
// mWakefulness (DESIGN D14).
func (l Layer) Wake(ctx context.Context) (string, error) {
	if _, err := l.Probe.Run.Run(ctx, "input", "keyevent", "KEYCODE_WAKEUP"); err != nil {
		return "", err
	}
	return l.Probe.DisplayState(ctx)
}

// Sleep turns the panel off.
func (l Layer) Sleep(ctx context.Context) error {
	_, err := l.Probe.Run.Run(ctx, "input", "keyevent", "KEYCODE_SLEEP")
	return err
}

// Global sends a global action such as BACK or HOME.
//
// The API names these after AccessibilityService.GLOBAL_ACTION_*, but the values
// are NOT interchangeable with KeyEvent codes, and this used to send the
// GLOBAL_ACTION number to `input keyevent`. Measured on the device: "back" sent
// keycode 1 (KEYCODE_MENU) and did nothing, "recents" sent keycode 3
// (KEYCODE_HOME) and went home. Both answered 200. Each action now runs the
// command that actually performs it.
func (l Layer) Global(ctx context.Context, name string) error {
	switch name {
	case "back":
		return l.Key(ctx, "KEYCODE_BACK", 0)
	case "home":
		return l.Key(ctx, "KEYCODE_HOME", 0)
	case "recents":
		return l.Key(ctx, "KEYCODE_APP_SWITCH", 0)
	case "notifications":
		_, err := l.Probe.Run.Run(ctx, "cmd", "statusbar", "expand-notifications")
		return err
	case "quick_settings":
		_, err := l.Probe.Run.Run(ctx, "cmd", "statusbar", "expand-settings")
		return err
	case "power_dialog":
		_, err := l.Probe.Run.Run(ctx, "input", "keyevent", "--longpress", "KEYCODE_POWER")
		return err
	case "lock_screen":
		return l.Sleep(ctx)
	case "screenshot":
		// KEYCODE_SYSRQ is the hardware screenshot key; on this ROM it saves to
		// the gallery rather than returning anything, which is what the name means.
		return l.Key(ctx, "KEYCODE_SYSRQ", 0)
	}
	return fmt.Errorf("unknown global action %q", name)
}

// Rotation reads the current rotation.
//
// It comes from `dumpsys window`, not `dumpsys display`: on this ROM the display
// dump only mentions rotation inside per-device DisplayInfo blobs (where the
// first hit is a per-app orientation, not the panel's), while `dumpsys window`
// prints a plain `mRotation=0..3` line. Reading the wrong one reports a rotation
// that never changes.
func (l Layer) Rotation(ctx context.Context) (string, error) {
	out, err := l.Probe.Run.Run(ctx, "dumpsys", "window")
	if err != nil {
		return "", err
	}
	for _, line := range splitLines(out) {
		trimmed := trimSpace(line)
		if !hasPrefix(trimmed, "mRotation=") {
			continue
		}
		value := firstToken(trimmed[len("mRotation="):])
		switch value {
		case "0":
			return "0", nil
		case "1":
			return "90", nil
		case "2":
			return "180", nil
		case "3":
			return "270", nil
		}
		return value, nil
	}
	return "", fmt.Errorf("dumpsys window: no mRotation line")
}

// SetRotation pins the rotation, or returns to auto-rotate with "auto".
//
// `user_rotation` alone does nothing while `accelerometer_rotation` is 1 (the
// sensor keeps winning, verified: writing user_rotation=1 left the panel at
// ROTATION_0 until accelerometer_rotation was turned off first).
func (l Layer) SetRotation(ctx context.Context, value string) error {
	if value == "auto" {
		_, err := l.Probe.Run.Run(ctx, "settings", "put", "system", "accelerometer_rotation", "1")
		return err
	}
	// The stored value is the Surface.ROTATION index (0..3), not degrees: writing
	// "90" is rejected by SettingsProvider ("Invalid value: 90 for setting:
	// user_rotation"), while this API speaks degrees. Convert, don't pass through.
	index, ok := rotationIndex(value)
	if !ok {
		return fmt.Errorf("unknown rotation %q (use 0, 90, 180, 270 or auto)", value)
	}
	if _, err := l.Probe.Run.Run(ctx, "settings", "put", "system", "accelerometer_rotation", "0"); err != nil {
		return err
	}
	_, err := l.Probe.Run.Run(ctx, "settings", "put", "system", "user_rotation", index)
	return err
}

// rotationIndex maps degrees to the Surface.ROTATION index that settings stores.
func rotationIndex(degrees string) (string, bool) {
	switch degrees {
	case "0":
		return "0", true
	case "90":
		return "1", true
	case "180":
		return "2", true
	case "270":
		return "3", true
	}
	return "", false
}

// SetScreen controls brightness and timeout; both are global-config writes and
// the HTTP layer enforces session exclusivity.
func (l Layer) SetScreen(ctx context.Context, brightness, timeoutMS string) error {
	if brightness != "" {
		if _, err := l.Probe.Run.Run(ctx, "settings", "put", "system", "screen_brightness", brightness); err != nil {
			return err
		}
	}
	if timeoutMS != "" {
		if _, err := l.Probe.Run.Run(ctx, "settings", "put", "system", "screen_off_timeout", timeoutMS); err != nil {
			return err
		}
	}
	return nil
}

// SetAirplane toggles airplane mode.
func (l Layer) SetAirplane(ctx context.Context, on bool) error {
	mode := "0"
	if on {
		mode = "1"
	}
	_, err := l.Probe.Run.Run(ctx, "settings", "put", "global", "airplane_mode_on", mode)
	return err
}

// SetWifi toggles Wi-Fi.
func (l Layer) SetWifi(ctx context.Context, on bool) error {
	mode := "0"
	if on {
		mode = "1"
	}
	_, err := l.Probe.Run.Run(ctx, "svc", "wifi", map[bool]string{true: "enable", false: "disable"}[on])
	_ = mode
	return err
}

// Reboot reboots the device. Callers must have gone through confirmation
// (DESIGN D13).
func (l Layer) Reboot(ctx context.Context) error {
	_, err := l.Probe.Run.Run(ctx, "svc", "power", "reboot")
	return err
}
