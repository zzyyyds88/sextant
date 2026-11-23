// Package perms applies the permission groups declared in the config.
//
// Each group maps to one concrete mechanism and is verified by reading the real
// state back (DESIGN §5). A group that cannot be verified is reported as
// MISSING/DENIED with a reason, never as success.
package perms

import (
	"context"
	"fmt"
	"strings"

	"sextant-core/internal/sysprobe"
)

// Group names. The set is closed: config validation rejects anything else.
const (
	Idle        = "idle"
	Overlay     = "overlay"
	Usage       = "usage"
	Notif       = "notif"
	Alarm       = "alarm"
	UnknownApps = "unknown_apps"
	BgPopup     = "bg_popup"
	// Runtime pre-grants the app's dangerous permissions, so nobody is asked.
	//
	// The phone is the agent's: a KernelSU module runs as uid 0 at boot, and
	// `pm grant` from that context is exactly as authoritative as the person
	// tapping 允许. Leaving it to a dialog meant the app asked for
	// POST_NOTIFICATIONS every time it was opened (MainActivity called
	// requestPermissions), which is a question with only one sensible answer on
	// this device — and a question an agent cannot answer at all when it drives
	// the phone headlessly.
	Runtime = "runtime"
)

// Valid reports whether name is a known group.
func Valid(name string) bool {
	switch name {
	case Idle, Overlay, Usage, Notif, Alarm, UnknownApps, BgPopup, Runtime:
		return true
	}
	return false
}

// Layer applies groups.
type Layer struct {
	Probe sysprobe.Probe
}

// Apply ensures every group in groups is granted, returning per-group state.
func (l Layer) Apply(ctx context.Context, pkg string, groups []string) (map[string]string, error) {
	out := map[string]string{}
	var firstErr error
	for _, g := range groups {
		if g == "" {
			continue
		}
		state, err := l.applyOne(ctx, pkg, g)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		out[g] = state
	}
	return out, firstErr
}

// Status reads the current state of every group without writing.
func (l Layer) Status(ctx context.Context, pkg string, groups []string) map[string]string {
	out := map[string]string{}
	for _, g := range groups {
		if g == "" {
			continue
		}
		out[g] = l.statusOne(ctx, pkg, g)
	}
	return out
}

func (l Layer) applyOne(ctx context.Context, pkg, group string) (string, error) {
	switch group {
	case Idle:
		if _, err := l.Probe.Run.Run(ctx, "cmd", "deviceidle", "whitelist", "+"+pkg); err != nil &&
			!strings.Contains(err.Error(), "already") {
			return "DENIED", err
		}
		return l.statusOne(ctx, pkg, group), nil
	case Overlay:
		if _, err := l.Probe.Run.Run(ctx, "appops", "set", pkg, "SYSTEM_ALERT_WINDOW", "allow"); err != nil {
			return "DENIED", err
		}
		return l.statusOne(ctx, pkg, group), nil
	case Usage:
		if _, err := l.Probe.Run.Run(ctx, "appops", "set", pkg, "GET_USAGE_STATS", "allow"); err != nil {
			return "DENIED", err
		}
		return l.statusOne(ctx, pkg, group), nil
	case Notif:
		if _, err := l.Probe.Run.Run(ctx, "cmd", "notification", "allow_listener",
			pkg+"/"+listenerClass(pkg)); err != nil {
			return "DENIED", err
		}
		return l.statusOne(ctx, pkg, group), nil
	case Alarm:
		if _, err := l.Probe.Run.Run(ctx, "appops", "set", pkg, "SCHEDULE_EXACT_ALARM", "allow"); err != nil {
			return "DENIED", err
		}
		return l.statusOne(ctx, pkg, group), nil
	case UnknownApps:
		if _, err := l.Probe.Run.Run(ctx, "appops", "set", pkg, "REQUEST_INSTALL_PACKAGES", "allow"); err != nil {
			return "DENIED", err
		}
		return l.statusOne(ctx, pkg, group), nil
	case BgPopup:
		if _, err := l.Probe.Run.Run(ctx, "appops", "set", pkg, "10022", "allow"); err != nil {
			return "DENIED", err
		}
		return l.statusOne(ctx, pkg, group), nil
	case Runtime:
		// Grant, then READ BACK.
		//
		// `pm grant` on MIUI can exit 0 and still not take effect (a restricted
		// permission, or one the platform only hands to a signature app), so the
		// exit code is not evidence. The only evidence is re-reading the package
		// dump, which is what permissionGranted does.
		var failed []string
		for _, perm := range runtimePermissions {
			if l.permissionGranted(ctx, pkg, perm) {
				continue
			}
			_, _ = l.Probe.Run.Run(ctx, "pm", "grant", pkg, perm)
			if !l.permissionGranted(ctx, pkg, perm) {
				failed = append(failed, perm)
			}
		}
		if len(failed) > 0 {
			// A partial success is still a failure for this group: reporting OK while
			// one permission is missing is how "granted" becomes a lie.
			return "DENIED", fmt.Errorf("not granted: %s", strings.Join(failed, ", "))
		}
		return "OK", nil
	}
	return "MISSING", fmt.Errorf("unknown permission group %q", group)
}

func (l Layer) statusOne(ctx context.Context, pkg, group string) string {
	switch group {
	case Idle:
		// Lines are `user,<pkg>,<uid>`; `system-excidle,...` does not count.
		out, err := l.Probe.Run.Run(ctx, "dumpsys", "deviceidle", "whitelist")
		if err != nil {
			return "DENIED"
		}
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Split(strings.TrimSpace(line), ",")
			if len(fields) >= 2 && fields[0] == "user" && fields[1] == pkg {
				return "OK"
			}
		}
		return "MISSING"
	case Overlay:
		return opState(ctx, l.Probe, pkg, "SYSTEM_ALERT_WINDOW")
	case Usage:
		return opState(ctx, l.Probe, pkg, "GET_USAGE_STATS")
	case Alarm:
		return opState(ctx, l.Probe, pkg, "SCHEDULE_EXACT_ALARM")
	case UnknownApps:
		return opState(ctx, l.Probe, pkg, "REQUEST_INSTALL_PACKAGES")
	case BgPopup:
		return opState(ctx, l.Probe, pkg, "10022")
	case Runtime:
		for _, perm := range runtimePermissions {
			if !l.permissionGranted(ctx, pkg, perm) {
				return "MISSING"
			}
		}
		return "OK"
	case Notif:
		out, err := l.Probe.SettingsGet(ctx, "secure", "enabled_notification_listeners")
		if err != nil {
			return "DENIED"
		}
		want := pkg + "/" + listenerClass(pkg)
		for _, part := range strings.Split(out, ":") {
			if strings.TrimSpace(part) == want {
				return "OK"
			}
		}
		return "MISSING"
	}
	return "MISSING"
}

func opState(ctx context.Context, p sysprobe.Probe, pkg, op string) string {
	mode, err := p.AppopsGet(ctx, pkg, op)
	if err != nil {
		// MIUI ops are only readable through `cmd appops`; fall back once.
		out, cerr := p.Run.Run(ctx, "cmd", "appops", "get", pkg, op)
		if cerr != nil {
			return "MISSING"
		}
		mode = ""
		for _, line := range strings.Split(out, "\n") {
			if i := strings.LastIndex(strings.TrimSpace(line), ":"); i >= 0 {
				v := strings.TrimSpace(line[i+1:])
				if end := strings.IndexAny(v, "; \t,"); end > 0 {
					v = v[:end]
				}
				if v != "" {
					mode = v
					break
				}
			}
		}
		if mode == "" {
			return "MISSING"
		}
	}
	if mode == "allow" || mode == "default" {
		return "OK"
	}
	return "DENIED"
}

// listenerClass is the notification listener component class. It is a single
// constant on purpose: the component and the package are different strings and
// deriving one from the other is how the IME provider bug happened.
func listenerClass(pkg string) string { return pkg + ".NotifListenerService" }

// runtimePermissions are the app's dangerous permissions, granted at boot.
//
// Kept as a literal list rather than derived from the APK: the daemon cannot read
// the APK's manifest without a package-manager parse, and a wrong guess here fails
// silently (the group reports OK while the permission is missing). Every entry must
// be one the APK actually declares.
var runtimePermissions = []string{
	"android.permission.POST_NOTIFICATIONS",
}

// permissionGranted reads the real state back from the package dump.
//
// `pm grant` returning 0 is not proof; this is. The line looks like
// `android.permission.POST_NOTIFICATIONS: granted=true, flags=[...]`.
func (l Layer) permissionGranted(ctx context.Context, pkg, perm string) bool {
	out, err := l.Probe.Run.Run(ctx, "dumpsys", "package", pkg)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(out, "\n") {
		t := strings.TrimSpace(line)
		if !strings.HasPrefix(t, perm+":") {
			continue
		}
		return strings.Contains(t, "granted=true")
	}
	return false
}
