// Package sysprobe runs the Android framework CLI tools the daemon needs and
// parses their output.
//
// Two hard rules from real device failures (DESIGN §10.2):
//   - Every framework CLI call gets stdin redirected from /dev/null. These are
//     app_process programs; a /data/adb file as fd 0 gets dragged into a binder
//     transaction and system_server is denied by SELinux, producing
//     "Failed transaction" and exit 2.
//   - Nothing here guesses: a parser that cannot find the expected shape returns
//     an error, never a plausible default.
package sysprobe

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// Runner executes a command and returns its combined output. Tests inject a fake.
type Runner interface {
	Run(ctx context.Context, name string, args ...string) (string, error)
}

// ExecRunner is the production runner.
type ExecRunner struct{ Timeout time.Duration }

// Run executes name with args, stdin from /dev/null, and a timeout.
func (r ExecRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	timeout := r.Timeout
	if timeout <= 0 {
		timeout = 20 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	// /dev/null, not a file: see the package comment.
	cmd.Stdin = nil
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	text := out.String()
	if err != nil {
		if cctx.Err() != nil {
			return text, fmt.Errorf("%s %s: timeout after %s", name, strings.Join(args, " "), timeout)
		}
		return text, fmt.Errorf("%s %s: %w (output: %s)", name, strings.Join(args, " "), err, truncate(text, 400))
	}
	return text, nil
}

func truncate(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

// Probe is the typed view of the device the daemon needs.
type Probe struct {
	Run Runner
}

// SettingsGet reads one secure/global/system key. A "null" value is the empty
// string, which is what callers actually reason about.
func (p Probe) SettingsGet(ctx context.Context, namespace, key string) (string, error) {
	out, err := p.Run.Run(ctx, "settings", "get", namespace, key)
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(out)
	if v == "null" {
		return "", nil
	}
	return v, nil
}

// SettingsPut writes a key. Returns an error when the write itself failed.
func (p Probe) SettingsPut(ctx context.Context, namespace, key, value string) error {
	_, err := p.Run.Run(ctx, "settings", "put", namespace, key, value)
	return err
}

// AppopsGet returns the mode reported for a package/op pair.
func (p Probe) AppopsGet(ctx context.Context, pkg, op string) (string, error) {
	out, err := p.Run.Run(ctx, "appops", "get", pkg, op)
	if err != nil {
		return "", err
	}
	// Format: "com.pkg: mode=allow; time=+1h2m3s ago" or "No operations."
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "mode="); i >= 0 {
			rest := line[i+len("mode="):]
			end := len(rest)
			for j, r := range rest {
				if r == ';' || r == ' ' {
					end = j
					break
				}
			}
			return rest[:end], nil
		}
	}
	return "", fmt.Errorf("appops get %s %s: no mode in output: %s", pkg, op, truncate(out, 200))
}

// PackageInstalled reports whether pkg is installed for the current user.
func (p Probe) PackageInstalled(ctx context.Context, pkg string) (bool, error) {
	out, err := p.Run.Run(ctx, "pm", "list", "packages", pkg)
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "package:"+pkg {
			return true, nil
		}
	}
	return false, nil
}

// PackageUID returns the app id (uid % 100000) of pkg.
func (p Probe) PackageUID(ctx context.Context, pkg string) (int, error) {
	out, err := p.Run.Run(ctx, "pm", "list", "packages", "-U", pkg)
	if err != nil {
		return 0, err
	}
	prefix := "package:" + pkg + " uid:"
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, prefix) {
			var uid int
			if _, err := fmt.Sscanf(line[len(prefix):], "%d", &uid); err != nil {
				return 0, fmt.Errorf("pm list -U %s: unparsable uid in %q", pkg, line)
			}
			return uid % 100000, nil
		}
	}
	return 0, fmt.Errorf("package %s not installed", pkg)
}

// DisplayState returns dumpsys display's mScreenState value.
//
// mScreenState, NOT mWakefulness: MIUI reports Awake while the panel is OFF, and
// using wakefulness sends the unlock path looking for a pattern view that cannot
// exist yet (DESIGN D14 constraint 1). "UNKNOWN" is returned verbatim so callers
// can distinguish "no idea" from "off".
func (p Probe) DisplayState(ctx context.Context) (string, error) {
	out, err := p.Run.Run(ctx, "dumpsys", "display")
	if err != nil {
		return "", err
	}
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "mScreenState="); i >= 0 {
			rest := line[i+len("mScreenState="):]
			end := len(rest)
			for j, r := range rest {
				if r == ' ' || r == ',' {
					end = j
					break
				}
			}
			return rest[:end], nil
		}
	}
	return "UNKNOWN", nil
}

// UserUnlocked reports whether the device is past the lock screen. A locked
// device legitimately cannot bind accessibility services, so callers must not
// treat that as a fault.
func (p Probe) UserUnlocked(ctx context.Context) (bool, error) {
	out, err := p.Run.Run(ctx, "dumpsys", "user")
	if err != nil {
		return false, err
	}
	// "State: RUNNING_UNLOCKED" / "RUNNING_LOCKED" / "RUNNING_UNLOCKING"
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "State:") {
			state := strings.TrimSpace(strings.TrimPrefix(line, "State:"))
			if i := strings.Index(state, " "); i > 0 {
				state = state[:i]
			}
			switch state {
			case "RUNNING_UNLOCKED":
				return true, nil
			case "RUNNING_LOCKED", "RUNNING_UNLOCKING":
				return false, nil
			default:
				return false, fmt.Errorf("dumpsys user: unexpected state %q", state)
			}
		}
	}
	return false, fmt.Errorf("dumpsys user: no State line")
}

// KeyguardShowing reports whether the keyguard is up.
func (p Probe) KeyguardShowing(ctx context.Context) (bool, error) {
	out, err := p.Run.Run(ctx, "dumpsys", "window")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "mDreamingLockscreen=") {
			return strings.Contains(line, "mDreamingLockscreen=true"), nil
		}
		if strings.Contains(line, "isKeyguardShowing=true") {
			return true, nil
		}
	}
	// Absence of evidence is not "unlocked": report an error, not false.
	return false, fmt.Errorf("dumpsys window: no keyguard state in output")
}
