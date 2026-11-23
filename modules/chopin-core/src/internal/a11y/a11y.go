// Package a11y owns the accessibility binding: the only writer of
// enabled_accessibility_services on this device (DESIGN §10.1).
//
// The rules encoded here come from real device failures, not from reading AOSP:
//
//   - writing the same value is a no-op (AMS only re-evaluates when the raw
//     string changes), so repairing a crashed service requires TWO writes:
//     first without the target, then with it;
//   - "bound" cannot be answered from `dumpsys accessibility` (labels only) nor
//     from settings (that is the wish, not the truth): the only reliable check is
//     a ServiceRecord with hasBound=true bound by system/1000 in
//     `dumpsys activity services <pkg>`;
//   - AMS persists the short form (pkg/.Svc), so comparison must normalise or
//     every write adds another duplicate;
//   - a locked device cannot bind anything; that is PENDING, not a fault.
package a11y

import (
	"context"
	"fmt"
	"strings"

	"chopin-core/internal/sysprobe"
)

// Setting is the single settings key this package owns.
const Setting = "enabled_accessibility_services"

// State mirrors the status vocabulary.
type State string

const (
	OK       State = "OK"
	DEGRADED State = "DEGRADED"
	MISSING  State = "MISSING"
	PENDING  State = "PENDING"
	DENIED   State = "DENIED"
)

// Result is the outcome of one reconcile pass.
type Result struct {
	State  State
	Detail string
}

// Normalise expands pkg/.Svc into pkg/pkg.Svc and trims spaces, so the same
// component always compares equal regardless of which form AMS wrote.
func Normalise(component, pkg string) string {
	c := strings.TrimSpace(component)
	if c == "" {
		return ""
	}
	slash := strings.Index(c, "/")
	if slash < 0 {
		return c
	}
	prefix, rest := c[:slash], c[slash+1:]
	if strings.HasPrefix(rest, ".") {
		return prefix + "/" + prefix + rest
	}
	if !strings.Contains(rest, ".") {
		return prefix + "/" + prefix + "." + rest
	}
	return c
}

// Parse splits a settings value into normalised components. Empty entries and
// duplicates are dropped; order is preserved.
func Parse(value, pkg string) []string {
	var out []string
	seen := map[string]bool{}
	for _, part := range strings.Split(value, ":") {
		if strings.TrimSpace(part) == "" {
			continue
		}
		n := Normalise(part, pkg)
		if seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// Contains reports whether component is present in value.
func Contains(value, component, pkg string) bool {
	want := Normalise(component, pkg)
	for _, c := range Parse(value, pkg) {
		if c == want {
			return true
		}
	}
	return false
}

// Join renders components back into a settings value.
func Join(components []string) string { return strings.Join(components, ":") }

// Without returns the value with component removed (empty string when nothing
// would remain, which is what AMS expects for "no services").
func Without(value, component, pkg string) string {
	want := Normalise(component, pkg)
	var keep []string
	for _, c := range Parse(value, pkg) {
		if c != want {
			keep = append(keep, c)
		}
	}
	return Join(keep)
}

// Layer performs the binding work.
type Layer struct {
	Probe     sysprobe.Probe
	AppPkg    string
	Component string
	SettleMax int // seconds to wait for bound after a write

	// Compat is an optional SECOND service that must also be bound.
	//
	// It exists because WeChat decides whether to expose its accessibility tree
	// by looking at which services are actually BOUND, and answers every set it
	// does not like with one empty root node. Measured here with a single
	// variable (TASKS 2026-11-17): primary alone -> 1 node / 0 texts; primary +
	// a bound compat service -> 421 nodes / 82 texts; the compat component
	// written into settings but not resolvable -> back to 1 node. So the check
	// is on real bindings, not on the settings string.
	//
	// The compat service carries no logic: canRetrieveWindowContent=false, no
	// gestures, empty event handler. The name that unlocks the tree is
	// deliberately not the name that reads it.
	Compat string
}

// Raw reads the current settings value.
func (l Layer) Raw(ctx context.Context) (string, error) {
	return l.Probe.SettingsGet(ctx, "secure", Setting)
}

// IsBound reports the real binding state using the service record, which is the
// only reliable source.
//
// The dump writes the component in SHORT form ("pkg/.Cls") while the module
// normalises to "pkg/pkg.Cls", so the record is normalised before comparison —
// comparing raw strings here made every bound service look unbound.
func (l Layer) IsBound(ctx context.Context, component string) (bool, error) {
	want := Normalise(component, l.AppPkg)
	slash := strings.Index(want, "/")
	if slash < 0 {
		return false, fmt.Errorf("component %q is not pkg/class", component)
	}
	pkg := want[:slash]
	out, err := l.Probe.Run.Run(ctx, "dumpsys", "activity", "services", pkg)
	if err != nil {
		return false, err
	}
	inRecord := false
	boundFlag := false
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.Contains(trimmed, "ServiceRecord{") {
			inRecord = recordMatches(trimmed, want, pkg)
			boundFlag = false
			continue
		}
		if !inRecord {
			continue
		}
		if strings.Contains(trimmed, "hasBound=true") {
			boundFlag = true
		}
		// Only a binding held by system_server counts. An app binding its own
		// service is not an accessibility binding.
		if strings.Contains(trimmed, "Client AppBindRecord{") && strings.Contains(trimmed, "system/1000") {
			return boundFlag, nil
		}
	}
	return false, nil
}

// recordMatches extracts the component from a ServiceRecord header line and
// compares it in normalised form.
func recordMatches(line, want, pkg string) bool {
	open := strings.Index(line, "ServiceRecord{")
	if open < 0 {
		return false
	}
	body := line[open+len("ServiceRecord{"):]
	if close := strings.Index(body, "}"); close >= 0 {
		body = body[:close]
	}
	for _, field := range strings.Fields(body) {
		if strings.Contains(field, "/") {
			if Normalise(field, pkg) == want {
				return true
			}
		}
	}
	return false
}

// Restore makes component bound, returning the resulting state.
//
// This is the two-step write: drop the component, then add it back. Writing the
// same value does nothing, and that is the whole reason the old watchdog could
// never repair a crashed service.
func (l Layer) Restore(ctx context.Context, component string) Result {
	res := l.ensure(ctx, component)
	if res.State != OK && res.State != PENDING {
		return res
	}
	if l.Compat == "" || l.Compat == component {
		return res
	}
	// The primary service is the one that does the work, so its state is what we
	// report; a compat failure is only worth saying out loud when it is the
	// reason the primary could not be reached.
	compat := l.ensure(ctx, l.Compat)
	if compat.State == OK {
		return res
	}
	if res.State == OK {
		return Result{compat.State, "compat: " + compat.Detail}
	}
	return res
}

// ensure makes one component bound, returning the resulting state.
func (l Layer) ensure(ctx context.Context, component string) Result {
	if bound, err := l.IsBound(ctx, component); err != nil {
		return Result{DEGRADED, "cannot read service record: " + err.Error()}
	} else if bound {
		return Result{OK, "bound"}
	}

	raw, err := l.Raw(ctx)
	if err != nil {
		return Result{DENIED, "settings get failed: " + err.Error()}
	}
	normalised := Normalise(component, l.AppPkg)
	listed := Contains(raw, component, l.AppPkg)

	// Step 1: remove (always write, even when absent, so the raw string changes).
	without := Without(raw, component, l.AppPkg)
	if without != raw {
		if err := l.Probe.SettingsPut(ctx, "secure", Setting, without); err != nil {
			return Result{DENIED, "settings put (drop) failed: " + err.Error()}
		}
	}
	// Step 2: write back with the component.
	with := normalised
	if without != "" {
		with = without + ":" + normalised
	}
	if err := l.Probe.SettingsPut(ctx, "secure", Setting, with); err != nil {
		return Result{DENIED, "settings put (add) failed: " + err.Error()}
	}
	// accessibility_enabled is derived by services.jar; writing it alone is a
	// placebo, but it must not stay 0 when services are listed.
	if en, err := l.Probe.SettingsGet(ctx, "secure", "accessibility_enabled"); err == nil && en != "1" {
		_ = l.Probe.SettingsPut(ctx, "secure", "accessibility_enabled", "1")
	}

	if l.waitBound(ctx, component) {
		if !listed {
			return Result{OK, "bound (newly enabled)"}
		}
		return Result{OK, "bound (repaired)"}
	}
	if !l.unlocked(ctx) {
		return Result{PENDING, "binds after unlock"}
	}
	return Result{DEGRADED, "written but not bound"}
}

func (l Layer) unlocked(ctx context.Context) bool {
	ok, err := l.Probe.UserUnlocked(ctx)
	return err == nil && ok
}

func (l Layer) waitBound(ctx context.Context, component string) bool {
	max := l.SettleMax
	if max <= 0 {
		max = 6
	}
	for i := 0; i < max; i++ {
		if bound, err := l.IsBound(ctx, component); err == nil && bound {
			return true
		}
		select {
		case <-ctx.Done():
			return false
		case <-sleepCtx(ctx, 1):
		}
	}
	return false
}

// Status reports the current state without writing anything.
func (l Layer) Status(ctx context.Context) Result {
	if bound, err := l.IsBound(ctx, l.Component); err == nil && bound {
		// The compat service is what decides whether WeChat exposes its tree, and
		// a status page that says "OK" while WeChat reads as an empty screen is
		// lying about the thing the user cares about. Everything else keeps
		// working without it, so this is DEGRADED, not MISSING.
		if l.Compat != "" && l.Compat != l.Component {
			if ok, cerr := l.IsBound(ctx, l.Compat); cerr == nil && !ok {
				return Result{DEGRADED, "compat service not bound"}
			}
		}
		return Result{OK, "bound"}
	}
	raw, err := l.Raw(ctx)
	if err != nil {
		return Result{DENIED, "settings get failed: " + err.Error()}
	}
	installed, _ := l.installed(ctx)
	if !installed {
		return Result{MISSING, "package not installed"}
	}
	if Contains(raw, l.Component, l.AppPkg) {
		if !l.unlocked(ctx) {
			return Result{PENDING, "binds after unlock"}
		}
		return Result{DEGRADED, "listed but not bound"}
	}
	return Result{MISSING, "not in settings"}
}

func (l Layer) installed(ctx context.Context) (bool, error) {
	return l.Probe.PackageInstalled(ctx, l.AppPkg)
}

// Remove drops component from the settings value (uninstall path).
func (l Layer) Remove(ctx context.Context, component string) error {
	raw, err := l.Raw(ctx)
	if err != nil {
		return err
	}
	if !Contains(raw, component, l.AppPkg) {
		return nil
	}
	return l.Probe.SettingsPut(ctx, "secure", Setting, Without(raw, component, l.AppPkg))
}
