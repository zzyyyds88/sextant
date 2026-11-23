// Package macro replays an App Map's recorded steps against a device.
//
// Portions are derived from PhoneGo (https://github.com/HKUDS/PhoneGo),
// MIT License, Copyright (c) 2024 xuyifan-0731 and 2025 yangqin-jiang.
//
// A step is data, never a shell string. The executor only calls the daemon's
// primitives, so the same map works on any transport that implements uiapi.
package macro

import (
	"context"
	"fmt"
	"time"

	"sextant/internal/appmap"
	"sextant/internal/uiapi"
)

// Options changes how strict a replay is.
type Options struct {
	// StrictScreen aborts when the screen a tap was recorded on is not the one
	// up now. Default is false: PhoneGo measured the identifier as unstable
	// enough that aborting threw away 9 of 10 otherwise valid macros.
	StrictScreen bool
	// SourceW/Height are the map's compile-time screen size, used to scale
	// coordinates onto the current screen.
	SourceW, SourceH int
	// CurrentW/Height skip the capture the scaler would otherwise take. A crawl
	// already knows the size.
	CurrentW, CurrentH int
	// Identify is called before a tap that carries a screen id; nil disables
	// the check.
	Identify func(nodes []uiapi.Node, pkg string) (string, float64)
	// Warn receives non-fatal findings (a screen mismatch that did not abort).
	Warn func(string)
	// Sleep is injectable for tests.
	Sleep func(time.Duration)
}

// StepResult is one step's outcome.
type StepResult struct {
	Index  int
	Action string
	OK     bool
	Detail string
}

// Result is a replay outcome.
type Result struct {
	OK       bool
	Executed int
	Steps    []StepResult
	Warnings []string
	Error    string
}

// Summary is a one-line outcome for logs and CLI output.
func (r Result) Summary() string {
	if r.OK {
		return fmt.Sprintf("macro ok (%d steps)", r.Executed)
	}
	return "macro failed: " + r.Error
}

// Run executes the steps in order and stops at the first failure.
func Run(ctx context.Context, d *uiapi.Driver, steps []appmap.Step, opts Options) Result {
	if opts.Sleep == nil {
		opts.Sleep = time.Sleep
	}
	res := Result{OK: true}
	var currentW, currentH int
	currentW, currentH = opts.CurrentW, opts.CurrentH
	if currentW <= 0 || currentH <= 0 {
		if w, h, err := d.ScreenSize(ctx); err == nil {
			currentW, currentH = w, h
		}
	}
	scale := func(x, y int) (int, int) {
		if opts.SourceW <= 0 || opts.SourceH <= 0 || currentW <= 0 || currentH <= 0 {
			return x, y
		}
		if currentW == opts.SourceW && currentH == opts.SourceH {
			return x, y
		}
		return x * currentW / opts.SourceW, y * currentH / opts.SourceH
	}

	warn := func(message string) {
		res.Warnings = append(res.Warnings, message)
		if opts.Warn != nil {
			opts.Warn(message)
		}
	}
	for i, step := range steps {
		detail, err := apply(ctx, d, step, opts, scale, currentW, currentH, warn)
		if err != nil {
			res.Steps = append(res.Steps, StepResult{Index: i, Action: step.Action, OK: false, Detail: err.Error()})
			res.OK = false
			res.Error = fmt.Sprintf("step %d (%s): %v", i, step.Action, err)
			return res
		}
		res.Steps = append(res.Steps, StepResult{Index: i, Action: step.Action, OK: true, Detail: detail})
		res.Executed++
	}
	return res
}

func apply(ctx context.Context, d *uiapi.Driver, step appmap.Step, opts Options, scale func(int, int) (int, int), screenW, screenH int, warn func(string)) (string, error) {
	switch step.Action {
	case "force_stop":
		if step.Package == "" {
			return "", fmt.Errorf("force_stop without package")
		}
		return "stopped " + step.Package, d.Stop(ctx, step.Package)

	case "launch":
		if step.Package == "" {
			return "", fmt.Errorf("launch without package")
		}
		if err := d.Launch(ctx, step.Package, false); err != nil {
			return "", err
		}
		if err := waitForPackage(ctx, d, step.Package, 15*time.Second); err != nil {
			return "", err
		}
		// The window changes before the accessibility tree does. Tapping in that
		// gap searches the previous screen, then falls back to a coordinate that
		// belongs to a layout which is no longer up.
		_, _ = d.WaitStable(ctx, 3000, 150)
		return "launched " + step.Package, nil

	case "tap":
		if step.Screen != "" && opts.Identify != nil {
			if nodes, err := d.Tree(ctx); err == nil {
				page, _ := d.Page(ctx)
				landed, _ := opts.Identify(nodes, page.Package)
				if landed != "" && landed != step.Screen {
					if opts.StrictScreen {
						return "", fmt.Errorf("screen changed: expected %s, on %s", step.Screen, landed)
					}
					warn(fmt.Sprintf("step tap recorded on %s but the screen is %s", step.Screen, landed))
				}
			}
		}
		if step.Text != "" {
			if clicked, channel := clickText(ctx, d, step.Text); clicked {
				if err := settle(ctx, d, step.Wait); err != nil {
					return "", err
				}
				return "click_text " + step.Text + " via " + channel, nil
			}
			// The recorded page offset is a hint, not a guarantee: the scroll
			// distance a list applies to one ACTION_SCROLL_DOWN varies by list.
			// Search a bounded number of pages for the label before trusting a
			// coordinate that was measured at a different offset.
			if screenW > 0 && screenH > 0 {
				for page := 0; page < 3; page++ {
					if err := d.Swipe(ctx, screenW/2, int(float64(screenH)*0.7), screenW/2, int(float64(screenH)*0.3), 400); err != nil {
						break
					}
					_, _ = d.WaitStable(ctx, 2000, 150)
					if clicked, channel := clickText(ctx, d, step.Text); clicked {
						if err := settle(ctx, d, step.Wait); err != nil {
							return "", err
						}
						return "click_text " + step.Text + " via " + channel + " after scrolling", nil
					}
				}
			}
		}
		x, y := scale(step.X, step.Y)
		if err := d.Tap(ctx, x, y); err != nil {
			return "", err
		}
		if err := settle(ctx, d, step.Wait); err != nil {
			return "", err
		}
		return fmt.Sprintf("tap %d,%d", x, y), nil

	case "longpress":
		duration := step.Duration
		if duration <= 0 {
			duration = 800
		}
		if step.Text != "" {
			if _, err := d.Act(ctx, map[string]any{"text": step.Text}, "longpress", "", duration, 0, nil); err == nil {
				return "longpress_text " + step.Text, nil
			}
		}
		x, y := scale(step.X, step.Y)
		return fmt.Sprintf("longpress %d,%d", x, y), d.LongPress(ctx, x, y, duration)

	case "swipe":
		x1, y1 := scale(step.X1, step.Y1)
		x2, y2 := scale(step.X2, step.Y2)
		duration := step.Duration
		if duration <= 0 {
			duration = 400
		}
		if err := d.Swipe(ctx, x1, y1, x2, y2, duration); err != nil {
			return "", err
		}
		if err := settle(ctx, d, step.Wait); err != nil {
			return "", err
		}
		return fmt.Sprintf("swipe %d,%d→%d,%d", x1, y1, x2, y2), nil

	case "text":
		if step.Text == "" {
			return "", fmt.Errorf("text step without text")
		}
		return "typed " + fmt.Sprintf("%d chars", len([]rune(step.Text))), d.Text(ctx, step.Text)

	case "back":
		return "back", d.Global(ctx, "back")
	case "home":
		return "home", d.Global(ctx, "home")
	case "key":
		return "key " + step.Key, d.Key(ctx, step.Key)
	case "wait", "sleep":
		ms := step.Duration
		if ms <= 0 {
			ms = int(step.Wait * 1000)
		}
		if ms > 0 {
			opts.Sleep(time.Duration(ms) * time.Millisecond)
		}
		return "waited", nil
	}
	return "", fmt.Errorf("unknown action %q", step.Action)
}

// clickText asks the daemon to click a label through accessibility. It reports
// whether the node itself accepted the click; a coordinate tap always "succeeds"
// even when nothing is there, which is why the checker needs the channel.
func clickText(ctx context.Context, d *uiapi.Driver, text string) (bool, string) {
	out, err := d.Act(ctx, map[string]any{"text": text}, "click", "", 0, 0, nil)
	if err != nil {
		return false, ""
	}
	channel, _ := out["channel"].(string)
	if channel == "" || channel == "none" {
		return false, ""
	}
	return true, channel
}

// settle waits for the screen to stop changing when the step asked for a wait.
func settle(ctx context.Context, d *uiapi.Driver, waitSeconds float64) error {
	if waitSeconds <= 0 {
		return nil
	}
	timeout := int(waitSeconds * 1000)
	if timeout < 2000 {
		timeout = 2000
	}
	_, err := d.WaitStable(ctx, timeout, 150)
	return err
}

func waitForPackage(ctx context.Context, d *uiapi.Driver, pkg string, budget time.Duration) error {
	deadline := time.Now().Add(budget)
	for {
		page, err := d.Page(ctx)
		if err == nil && page.Package == pkg {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("did not land in %s (foreground is %q)", pkg, page.Package)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(300 * time.Millisecond):
		}
	}
}
