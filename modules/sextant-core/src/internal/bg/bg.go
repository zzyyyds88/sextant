// Package bg applies and reads back MIUI's background policy.
//
// Three independent switches decide whether MIUI's cleaner leaves a process
// alone; missing any one of them means the app dies again:
//
//  1. AppOps MIUIOP(10008) — the "自启动" toggle
//  2. PowerKeeper userTable.bgControl — the "省电策略" dropdown
//  3. deviceidle whitelist — Android's "电池优化" exemption
//
// Device facts encoded here (all verified on sextant/MIUI 14):
//
//   - bgControl lives in `content://com.miui.powerkeeper.configure/userTable`
//     and the where clause is `pkgName='<pkg>'` — NOT package_name, and the
//     value is a two-token string (`noRestrict` etc.).
//   - The PowerKeeper provider is guarded by
//     miui.permission.powerkeeper.HIDDEN_MODE_PROVIDER: only root may update it.
//   - `cmd appops get <pkg> 10008` prints `MIUIOP(10008): allow`; the plain
//     `appops get` form used elsewhere does not print the mode at all.
//   - `dumpsys deviceidle whitelist` lines are `user,<pkg>,<uid>`; a prefix match
//     ending in a comma never matches, which made this criterion report "off"
//     for every package.
package bg

import (
	"context"
	"fmt"
	"strings"

	"sextant-core/internal/status"
	"sextant-core/internal/sysprobe"
)

// AutoStartOp is MIUI's auto-start operation.
const AutoStartOp = "10008"

// PowerKeeperURI is the MIUI user table.
const PowerKeeperURI = "content://com.miui.powerkeeper.configure/userTable"

// Policy is the desired state.
type Policy struct {
	Autostart string // allow | ignore | ask | default
	BgControl string // miuiAuto | noRestrict | restrictBg | noBg
	Doze      bool
}

// Reading is what the device actually reports now.
type Reading struct {
	Autostart string
	BgControl string
	Doze      bool
}

// Result is Apply's outcome.
type Result struct {
	State   string
	Detail  string
	Reading Reading
}

// Layer applies the policy.
type Layer struct {
	Probe sysprobe.Probe
}

// Read reads all three switches back. Individual failures do not abort the read:
// a locked device legitimately cannot reach PowerKeeper, and reporting
// "unknown" for one switch is more useful than failing all three.
func (l Layer) Read(ctx context.Context, pkg string) (Reading, []error) {
	var r Reading
	var errs []error

	if v, err := l.autostart(ctx, pkg); err != nil {
		errs = append(errs, err)
	} else {
		r.Autostart = v
	}
	if v, err := l.bgControl(ctx, pkg); err != nil {
		errs = append(errs, err)
	} else {
		r.BgControl = v
	}
	if v, err := l.dozeListed(ctx, pkg); err != nil {
		errs = append(errs, err)
	} else {
		r.Doze = v
	}
	return r, errs
}

// AutostartOK reports whether the autostart op is permissive.
func AutostartOK(mode string) bool {
	return mode == "allow" || mode == "default"
}

// BgControlOK reports whether PowerKeeper leaves the process alone.
func BgControlOK(mode string) bool {
	return mode == "noRestrict" || mode == "miuiAuto"
}

// Satisfied is the conjunction used by status and the keep-alive loop.
func Satisfied(r Reading) bool {
	return AutostartOK(r.Autostart) && BgControlOK(r.BgControl) && r.Doze
}

// Apply writes the policy and reads it back.
func (l Layer) Apply(ctx context.Context, pkg string, p Policy) Result {
	var problems []string

	if p.Autostart != "" {
		if err := l.setAutostart(ctx, pkg, p.Autostart); err != nil {
			problems = append(problems, "appops 10008: "+err.Error())
		}
	}
	if p.BgControl != "" {
		if err := l.setBgControl(ctx, pkg, p.BgControl); err != nil {
			problems = append(problems, "PowerKeeper bgControl: "+err.Error())
		}
	}
	if p.Doze {
		if err := l.setDoze(ctx, pkg, true); err != nil && !strings.Contains(err.Error(), "already") {
			problems = append(problems, "deviceidle: "+err.Error())
		}
	}

	reading, errs := l.Read(ctx, pkg)
	var unmet []string
	if p.Autostart != "" && !AutostartOK(reading.Autostart) {
		unmet = append(unmet, "autostart="+orUnknown(reading.Autostart))
	}
	if p.BgControl != "" && !BgControlOK(reading.BgControl) {
		unmet = append(unmet, "bgControl="+orUnknown(reading.BgControl))
	}
	if p.Doze && !reading.Doze {
		unmet = append(unmet, "doze=off")
	}
	if len(unmet) == 0 {
		return Result{State: status.OK, Detail: "policy applied", Reading: reading}
	}
	detail := strings.Join(unmet, " ")
	if len(problems) > 0 {
		detail += "; write errors: " + strings.Join(problems, "; ")
	}
	if len(errs) > 0 {
		msgs := make([]string, 0, len(errs))
		for _, e := range errs {
			msgs = append(msgs, e.Error())
		}
		detail += "; read errors: " + strings.Join(msgs, "; ")
	}
	return Result{State: status.DEGRADED, Detail: detail, Reading: reading}
}

// Revert restores a recorded baseline; used by uninstall.
func (l Layer) Revert(ctx context.Context, pkg string, baseline Policy) error {
	var firstErr error
	if baseline.Autostart != "" {
		if err := l.setAutostart(ctx, pkg, baseline.Autostart); err != nil {
			firstErr = err
		}
	}
	if baseline.BgControl != "" {
		if err := l.setBgControl(ctx, pkg, baseline.BgControl); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	if !baseline.Doze {
		if err := l.setDoze(ctx, pkg, false); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// autostart parses `cmd appops get <pkg> 10008` → "MIUIOP(10008): allow".
func (l Layer) autostart(ctx context.Context, pkg string) (string, error) {
	out, err := l.Probe.Run.Run(ctx, "cmd", "appops", "get", pkg, AutoStartOp)
	if err != nil {
		return "", fmt.Errorf("cmd appops get: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "No operations") {
			continue
		}
		if i := strings.LastIndex(line, ":"); i >= 0 {
			v := strings.TrimSpace(line[i+1:])
			if end := strings.IndexAny(v, "; \t,"); end > 0 {
				v = v[:end]
			}
			if v != "" {
				return v, nil
			}
		}
	}
	return "default", nil
}

func (l Layer) setAutostart(ctx context.Context, pkg, value string) error {
	_, err := l.Probe.Run.Run(ctx, "cmd", "appops", "set", pkg, AutoStartOp, value)
	return err
}

// bgControl reads PowerKeeper's row. A missing row means PowerKeeper has never
// seen the package (or its provider is down before CE) — reported as an error so
// the caller can say PENDING rather than inventing a value.
func (l Layer) bgControl(ctx context.Context, pkg string) (string, error) {
	out, err := l.Probe.Run.Run(ctx, "content", "query",
		"--uri", PowerKeeperURI,
		"--where", "pkgName='"+pkg+"'",
		"--projection", "bgControl")
	if err != nil {
		return "", fmt.Errorf("PowerKeeper query: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		if i := strings.Index(line, "bgControl="); i >= 0 {
			v := strings.TrimSpace(line[i+len("bgControl="):])
			if end := strings.IndexAny(v, ",} \t"); end > 0 {
				v = v[:end]
			}
			if v != "" {
				return v, nil
			}
		}
	}
	return "", fmt.Errorf("PowerKeeper has no bgControl row for %s", pkg)
}

func (l Layer) setBgControl(ctx context.Context, pkg, value string) error {
	_, err := l.Probe.Run.Run(ctx, "content", "update",
		"--uri", PowerKeeperURI,
		"--bind", "bgControl:s:"+value,
		"--where", "pkgName='"+pkg+"'")
	return err
}

func (l Layer) setDoze(ctx context.Context, pkg string, on bool) error {
	arg := "+" + pkg
	if !on {
		arg = "-" + pkg
	}
	_, err := l.Probe.Run.Run(ctx, "cmd", "deviceidle", "whitelist", arg)
	return err
}

// dozeListed parses `dumpsys deviceidle whitelist`, whose lines are
// `user,<pkg>,<uid>`. `system-excidle,<pkg>,<uid>` is not a user exemption and
// must not count.
func (l Layer) dozeListed(ctx context.Context, pkg string) (bool, error) {
	out, err := l.Probe.Run.Run(ctx, "dumpsys", "deviceidle", "whitelist")
	if err != nil {
		return false, fmt.Errorf("dumpsys deviceidle: %w", err)
	}
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Split(strings.TrimSpace(line), ",")
		if len(fields) >= 2 && fields[0] == "user" && fields[1] == pkg {
			return true, nil
		}
	}
	return false, nil
}

func orUnknown(v string) string {
	if v == "" {
		return "unknown"
	}
	return v
}
