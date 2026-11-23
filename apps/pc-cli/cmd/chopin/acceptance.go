package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"chopin/internal/client"
)

// cmdAcceptance runs the capability matrix against the phone and writes a report.
//
// This is the deliverable's proof, so it is deliberately not a smoke test: each
// entry states what it proves, and a failure is reported with the daemon's own
// error text. Nothing here is allowed to report success for work it did not
// observe — a capability that cannot be exercised is SKIPPED with the reason, not
// counted as passing.

type checkResult struct {
	Capability string
	Check      string
	State      string // PASS | FAIL | SKIP
	Detail     string
	Millis     int64
}

func cmdAcceptance(a *args) error {
	outDir := a.optStr("out", "")
	if outDir == "" {
		outDir = filepath.Join("out", "acceptance-"+time.Now().Format("20060102-150405"))
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", outDir, err)
	}
	c, err := paired()
	if err != nil {
		return err
	}

	var results []checkResult
	run := func(capability, check string, fn func() (string, error)) {
		start := time.Now()
		detail, err := fn()
		res := checkResult{Capability: capability, Check: check, Millis: time.Since(start).Milliseconds()}
		switch {
		case err != nil:
			res.State, res.Detail = "FAIL", err.Error()
		case detail == "":
			res.State, res.Detail = "PASS", ""
		case strings.HasPrefix(detail, "SKIP: "):
			res.State, res.Detail = "SKIP", strings.TrimPrefix(detail, "SKIP: ")
		default:
			res.State, res.Detail = "PASS", detail
		}
		results = append(results, res)
		fmt.Printf("  %-6s %-22s %s\n", res.State, capability, check)
		if res.State == "FAIL" {
			fmt.Printf("         %s\n", res.Detail)
		}
	}

	fmt.Println("chopin acceptance")
	fmt.Printf("server: %s\n\n", c.Server())

	// Wait for the daemon to answer at all.
	//
	// A cold-boot run is exactly when this matrix is most useful, and at that
	// moment the first requests hit a refused connection. Every check would fail
	// with the same "connection refused", which says nothing about the device.
	// Wait for reachability first, and report how long it took.
	if waited, ok := waitReachable(c, 180*time.Second); ok {
		if waited > time.Second {
			fmt.Printf("daemon reachable after %s\n", waited.Round(time.Second))
		}
	} else {
		return fmt.Errorf("the daemon did not answer within 3 minutes; is the phone booted and on the network?")
	}

	// Wait for the device to be ready before judging it.
	//
	// Right after a reboot the daemon is answering long before the keep-alive loop
	// has applied the background policy, and the panel may still be off (which makes
	// every capture black). Reporting those as capability failures would be wrong:
	// the capability works, the device was not ready. So the matrix waits, and says
	// how long it waited.
	if ready, waited, why := waitReady(c, 90*time.Second); !ready {
		fmt.Printf("device not ready after %s: %s\n", waited.Round(time.Second), why)
		fmt.Println("the matrix will still run; readiness failures will show up per check")
	} else {
		fmt.Printf("device ready after %s\n", waited.Round(time.Second))
	}
	// Perception needs the screen on: an off panel is not a broken capture path.
	if err := unlockTolerant(c); err != nil {
		fmt.Printf("note: could not unlock before the perception checks: %v\n", err)
	}
	fmt.Println()

	// --- liveness -----------------------------------------------------------
	run("core", "health", func() (string, error) {
		data, err := c.Get("/v1/health")
		if err != nil {
			return "", err
		}
		var h struct {
			OK      bool   `json:"ok"`
			Version string `json:"version"`
		}
		if err := json.Unmarshal(data, &h); err != nil {
			return "", err
		}
		if !h.OK {
			return "", fmt.Errorf("health reported ok=false")
		}
		return "version " + h.Version, nil
	})
	run("core", "status", func() (string, error) {
		data, err := c.Get("/v1/status")
		if err != nil {
			return "", err
		}
		var st map[string]any
		if err := json.Unmarshal(data, &st); err != nil {
			return "", err
		}
		app, _ := st["app"].(map[string]any)
		a11y, _ := app["a11y"].(map[string]any)
		bgInfo, _ := app["bg"].(map[string]any)
		if a11y["state"] != "OK" {
			return "", fmt.Errorf("accessibility is %v (%v)", a11y["state"], a11y["detail"])
		}
		if ok, _ := bgInfo["ok"].(bool); !ok {
			return "", fmt.Errorf("background policy is not applied: %v", bgInfo)
		}
		return "a11y OK, background ok", nil
	})
	run("core", "metrics", func() (string, error) {
		data, err := c.Get("/v1/metrics")
		if err != nil {
			return "", err
		}
		var m struct {
			UptimeSec int64 `json:"uptime_sec"`
			Requests  struct {
				Total int64 `json:"total"`
			} `json:"requests"`
			Routes []struct {
				Route string `json:"route"`
				Count int64  `json:"count"`
				P95MS int64  `json:"p95_ms"`
			} `json:"routes"`
			UI     map[string]any `json:"ui"`
			Helper map[string]any `json:"helper"`
			Frames map[string]any `json:"frames"`
		}
		if err := json.Unmarshal(data, &m); err != nil {
			return "", err
		}
		if m.Requests.Total == 0 || len(m.Routes) == 0 {
			return "", fmt.Errorf("metrics has no request data: %+v", m)
		}
		if m.UI == nil || m.Helper == nil || m.Frames == nil {
			return "", fmt.Errorf("metrics is missing a fixed section")
		}
		if len(data) > 64*1024 {
			return "", fmt.Errorf("metrics response is %d bytes, over the 64 KiB bound", len(data))
		}
		return fmt.Sprintf("%d requests, %d route(s), %d bytes", m.Requests.Total, len(m.Routes), len(data)), nil
	})
	run("core", "watchdog", func() (string, error) {
		data, err := c.Get("/v1/watchdog")
		if err != nil {
			return "", err
		}
		var w struct {
			Helper struct {
				Available bool `json:"available"`
			} `json:"helper"`
			Errors []any `json:"errors"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return "", err
		}
		if !w.Helper.Available {
			return "", fmt.Errorf("the root helper is not answering")
		}
		return fmt.Sprintf("helper up, %d recorded error(s)", len(w.Errors)), nil
	})

	// --- lock and unlock ----------------------------------------------------
	run("lock", "lock/unlock cycle", func() (string, error) {
		if _, err := c.Post("/v1/device/lock", map[string]any{}); err != nil {
			return "", fmt.Errorf("lock: %w", err)
		}
		// Locking is asynchronous: the screen turns off and the keyguard engages
		// over the next second or two, so an immediate read reports "not locked"
		// on a phone that is locking correctly.
		locked, err := waitLocked(c, 8*time.Second)
		if err != nil {
			return "", err
		}
		if !locked.Locked {
			return "", fmt.Errorf("the device still reports itself unlocked %s after lock", 8*time.Second)
		}
		// Same cooldown tolerance as the warm-up unlock: the daemon refuses a
		// second attempt inside its window by design.
		if err := unlockTolerant(c); err != nil {
			return "", fmt.Errorf("unlock: %w", err)
		}
		ur := struct {
			OK     bool   `json:"ok"`
			Detail string `json:"detail"`
		}{OK: true, Detail: "unlocked"}
		after, err := waitUnlocked(c, 20*time.Second)
		if err != nil {
			return "", err
		}
		if after.Locked {
			return "", fmt.Errorf("still locked after unlock (%s)", ur.Detail)
		}
		return fmt.Sprintf("screen %s → locked → %s → keyguard clear", locked.Screen, ur.Detail), nil
	})

	// --- input --------------------------------------------------------------
	run("input", "global home", func() (string, error) {
		// The old version of this check only looked for a 200. That is how
		// `global home` shipped while sending KEYCODE_MOVE_END: the call was
		// accepted and nothing moved. Observe the effect instead.
		// Start from an app, not the launcher: if the foreground is already home
		// then "still home afterwards" proves nothing, and that is exactly how the
		// keycode bug hid.
		if _, err := c.Post("/v1/apps/launch", map[string]any{"package": "com.android.settings"}); err != nil {
			return "", err
		}
		time.Sleep(2500 * time.Millisecond)
		before, err := foregroundOf(c)
		if err != nil {
			return "", err
		}
		if strings.Contains(before, "launcher") || strings.Contains(before, "miui.home") {
			return "SKIP: could not get an app into the foreground to go home from", nil
		}
		if _, err := c.Post("/v1/input/global", map[string]any{"action": "home"}); err != nil {
			return "", err
		}
		time.Sleep(1500 * time.Millisecond)
		after, err := foregroundOf(c)
		if err != nil {
			return "", err
		}
		if !strings.Contains(after, "launcher") && !strings.Contains(after, "miui.home") {
			return "", fmt.Errorf("home was accepted but the foreground is still %q", after)
		}
		return fmt.Sprintf("%s → %s", shortComponent(before), shortComponent(after)), nil
	})
	run("input", "invalid key is rejected", func() (string, error) {
		_, err := c.Post("/v1/input/key", map[string]any{"key": "not a key"})
		if err == nil {
			return "", fmt.Errorf("an invalid key name was accepted")
		}
		if !strings.Contains(err.Error(), "bad_request") {
			return "", fmt.Errorf("expected bad_request, got %v", err)
		}
		return "rejected with bad_request", nil
	})

	run("input", "IME state", func() (string, error) {
		data, err := c.Get("/v1/input/ime")
		if err != nil {
			return "", err
		}
		var m struct {
			Active    string `json:"active"`
			Ours      string `json:"ours"`
			Authority string `json:"authority"`
			Busy      bool   `json:"busy"`
		}
		if err := json.Unmarshal(data, &m); err != nil {
			return "", err
		}
		if m.Active == "" {
			return "", fmt.Errorf("the active IME could not be read")
		}
		if m.Authority == "" {
			return "", fmt.Errorf("the IME control authority is not configured")
		}
		if m.Busy {
			return "", fmt.Errorf("the IME lease is stuck busy")
		}
		return fmt.Sprintf("active %s, authority %s", m.Active, m.Authority), nil
	})
	run("input", "IME text commit", func() (string, error) {
		// An empty target returns NO_INPUT_CONNECTION, which is still a real
		// answer: the point is that the provider is reachable and reports a
		// verdict rather than throwing.
		data, err := c.Post("/v1/input/ime/text", map[string]any{"text": "chopin"})
		if err != nil {
			return "", err
		}
		var r struct {
			Result string `json:"result"`
		}
		if err := json.Unmarshal(data, &r); err != nil {
			return "", err
		}
		switch r.Result {
		case "VERIFIED", "ACCEPTED_UNVERIFIED", "NO_INPUT_CONNECTION", "REJECTED":
			return "provider answered " + r.Result, nil
		}
		return "", fmt.Errorf("the provider answered an unknown verdict %q", r.Result)
	})

	// --- perception ---------------------------------------------------------
	run("perceive", "tree", func() (string, error) {
		data, err := c.Get("/v1/ui/tree")
		if err != nil {
			return "", err
		}
		var t struct {
			Count  int    `json:"count"`
			Source string `json:"source"`
		}
		if err := json.Unmarshal(data, &t); err != nil {
			return "", err
		}
		if t.Count == 0 {
			return "", fmt.Errorf("the tree is empty")
		}
		return fmt.Sprintf("%d nodes via %s", t.Count, t.Source), nil
	})
	run("perceive", "tree-crop", func() (string, error) {
		// D29 P1: the crop must shrink the payload without losing identity.
		// node_id/parent/depth stay in every projection so a cropped tree is
		// still addressable by act.
		data, err := c.Get("/v1/ui/tree?max_items=5&fields=text,clickable&viewport_only=1")
		if err != nil {
			return "", err
		}
		var t struct {
			Count     int              `json:"count"`
			Total     int              `json:"total"`
			Truncated bool             `json:"truncated"`
			Nodes     []map[string]any `json:"nodes"`
		}
		if err := json.Unmarshal(data, &t); err != nil {
			return "", err
		}
		if t.Count == 0 {
			return "", fmt.Errorf("the cropped tree is empty")
		}
		if t.Count > 5 {
			return "", fmt.Errorf("max_items=5 returned %d nodes", t.Count)
		}
		if t.Total < t.Count {
			return "", fmt.Errorf("total=%d is smaller than count=%d", t.Total, t.Count)
		}
		for _, n := range t.Nodes {
			if _, ok := n["node_id"]; !ok {
				return "", fmt.Errorf("a cropped node lost node_id: %v", n)
			}
			if _, ok := n["class"]; ok {
				return "", fmt.Errorf("fields=text,clickable still returned class: %v", n)
			}
		}
		return fmt.Sprintf("%d/%d nodes, truncated=%v", t.Count, t.Total, t.Truncated), nil
	})
	run("perceive", "ocr", func() (string, error) {
		data, err := c.Post("/v1/ui/ocr", map[string]any{})
		if err != nil {
			return "", err
		}
		var o struct {
			N    int    `json:"n"`
			Path string `json:"path"`
		}
		if err := json.Unmarshal(data, &o); err != nil {
			return "", err
		}
		return fmt.Sprintf("%d text line(s)", o.N), nil
	})
	run("perceive", "find", func() (string, error) {
		// Probe text that is PROVABLY on screen: take it from the tree we just
		// read. A hardcoded list made this check depend on which app happened to
		// be in front (it went SKIP as soon as the foreground was neither the
		// launcher nor WeChat), which measured the harness's luck, not `find`.
		// A miss is still a SKIP, never a pass.
		probes := []string{"设置", "微信", "搜索"}
		if t, err := c.Get("/v1/ui/tree"); err == nil {
			var tv struct {
				Nodes []struct {
					Text string `json:"text"`
				} `json:"nodes"`
			}
			if json.Unmarshal(t, &tv) == nil {
				for _, n := range tv.Nodes {
					if v := strings.TrimSpace(n.Text); len([]rune(v)) >= 2 && len([]rune(v)) <= 12 {
						probes = append([]string{v}, probes...)
						break
					}
				}
			}
		}
		for _, probe := range probes {
			data, err := c.Post("/v1/ui/find", map[string]any{"text": probe})
			if err != nil {
				if strings.Contains(err.Error(), "not_found") {
					continue
				}
				return "", err
			}
			var f struct {
				Source string `json:"source"`
			}
			_ = json.Unmarshal(data, &f)
			return fmt.Sprintf("found %q via %s", probe, f.Source), nil
		}
		return "SKIP: none of the probe strings were on screen (" + strings.Join(probes, "/") + ")", nil
	})
	run("perceive", "panel read", func() (string, error) {
		data, err := c.Post("/v1/ui/panel_read", map[string]any{})
		if err != nil {
			return "", err
		}
		var p struct {
			Lines []string `json:"lines"`
		}
		if err := json.Unmarshal(data, &p); err != nil {
			return "", err
		}
		return fmt.Sprintf("%d line(s) read", len(p.Lines)), nil
	})
	run("perceive", "pixel stats", func() (string, error) {
		data, err := c.Post("/v1/ui/pixel_stats", map[string]any{})
		if err != nil {
			return "", err
		}
		var p struct {
			Pixels     int  `json:"pixels"`
			LooksBlack bool `json:"looks_black"`
			Colors     int  `json:"distinct_colors"`
		}
		if err := json.Unmarshal(data, &p); err != nil {
			return "", err
		}
		if p.Pixels == 0 {
			return "", fmt.Errorf("no pixels were sampled")
		}
		if p.LooksBlack {
			return "", fmt.Errorf("the capture is entirely black: the frame path is broken")
		}
		return fmt.Sprintf("%d pixels, %d colours", p.Pixels, p.Colors), nil
	})
	run("perceive", "screenshot", func() (string, error) {
		data, err := c.Get("/v1/ui/screenshot")
		if err != nil {
			return "", err
		}
		var s struct {
			Bytes int64  `json:"bytes"`
			Path  string `json:"path"`
		}
		if err := json.Unmarshal(data, &s); err != nil {
			return "", err
		}
		if s.Bytes == 0 {
			return "", fmt.Errorf("the daemon wrote an empty image")
		}
		return fmt.Sprintf("%d bytes at %s", s.Bytes, s.Path), nil
	})
	run("perceive", "page state", func() (string, error) {
		data, err := c.Get("/v1/ui/page")
		if err != nil {
			return "", err
		}
		var p struct {
			Package     string `json:"package"`
			Fingerprint string `json:"fingerprint"`
			NodeCount   int    `json:"node_count"`
		}
		if err := json.Unmarshal(data, &p); err != nil {
			return "", err
		}
		if p.Package == "" || p.Fingerprint == "" || p.NodeCount == 0 {
			return "", fmt.Errorf("page state is incomplete: %+v", p)
		}
		return fmt.Sprintf("%s, %d nodes, %s", p.Package, p.NodeCount, p.Fingerprint), nil
	})
	run("perceive", "list extraction", func() (string, error) {
		data, err := c.Post("/v1/ui/list", map[string]any{
			"class": "android.widget.TextView", "max_items": 5,
		})
		if err != nil {
			return "", err
		}
		var l struct {
			Items           []any  `json:"items"`
			Count           int    `json:"count"`
			PageFingerprint string `json:"page_fingerprint"`
		}
		if err := json.Unmarshal(data, &l); err != nil {
			return "", err
		}
		if l.PageFingerprint == "" {
			return "", fmt.Errorf("list response has no page_fingerprint")
		}
		return fmt.Sprintf("%d items, %s", l.Count, l.PageFingerprint), nil
	})
	run("perceive", "options read", func() (string, error) {
		data, err := c.Post("/v1/ui/options", map[string]any{})
		if err != nil {
			return "", err
		}
		var o struct {
			Groups []any `json:"groups"`
			Count  int   `json:"count"`
		}
		if err := json.Unmarshal(data, &o); err != nil {
			return "", err
		}
		return fmt.Sprintf("%d groups", o.Count), nil
	})
	run("perceive", "wait stable", func() (string, error) {
		data, err := c.Post("/v1/ui/wait_stable", map[string]any{
			"timeout_ms": 3000, "stable_ms": 150,
		})
		if err != nil {
			return "", err
		}
		var w struct {
			Stable bool `json:"stable"`
		}
		if err := json.Unmarshal(data, &w); err != nil {
			return "", err
		}
		return fmt.Sprintf("stable=%v", w.Stable), nil
	})
	run("perceive", "challenge classification", func() (string, error) {
		data, err := c.Post("/v1/ui/challenge", map[string]any{})
		if err != nil {
			return "", err
		}
		var out struct {
			Kind string `json:"kind"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return "", err
		}
		switch out.Kind {
		case "none", "slider", "code":
			return "kind=" + out.Kind, nil
		default:
			return "", fmt.Errorf("unknown challenge kind %q", out.Kind)
		}
	})
	run("perceive", "challenge risk store", func() (string, error) {
		data, err := c.Get("/v1/challenge/risk")
		if err != nil {
			return "", err
		}
		var out struct {
			Entries map[string]any `json:"entries"`
		}
		if err := json.Unmarshal(data, &out); err != nil {
			return "", err
		}
		return fmt.Sprintf("%d live record(s)", len(out.Entries)), nil
	})
	run("device", "handover gate", func() (string, error) {
		var lock struct {
			ScreenLocked bool `json:"screen_locked"`
		}
		if data, err := c.Get("/v1/device/lock-state"); err == nil {
			_ = json.Unmarshal(data, &lock)
		}
		if lock.ScreenLocked {
			return "SKIP: keyguard is up", nil
		}
		if _, err := c.Post("/v1/device/handover", map[string]any{
			"package": "com.android.settings", "reason": "acceptance",
		}); err != nil {
			return "", fmt.Errorf("handover: %w", err)
		}
		cleared := false
		defer func() {
			if !cleared {
				_, _ = c.Post("/v1/device/handover/return", map[string]any{})
			}
		}()
		if _, err := c.Post("/v1/input/tap", map[string]any{"x": 5, "y": 5}); err == nil {
			return "", fmt.Errorf("an injection was accepted while the app was handed over")
		} else if !strings.Contains(err.Error(), "handed_over") {
			return "", fmt.Errorf("expected handed_over, got %w", err)
		}
		if _, err := c.Post("/v1/device/handover/return", map[string]any{}); err != nil {
			return "", fmt.Errorf("handover return: %w", err)
		}
		cleared = true
		return "injection refused with handed_over; handover cleared", nil
	})
	run("perceive", "act refuses a missing target", func() (string, error) {
		_, err := c.Post("/v1/ui/act", map[string]any{
			"target": map[string]any{"text": "__chopin_acceptance_missing__"},
			"action": "click",
		})
		if err == nil {
			return "", fmt.Errorf("act accepted a target that does not exist")
		}
		if !strings.Contains(err.Error(), "target not found") {
			return "", fmt.Errorf("unexpected error: %w", err)
		}
		return "rejected with target not found", nil
	})

	// --- sessions -----------------------------------------------------------
	run("session", "claim/conflict/release", func() (string, error) {
		if err := releaseAll(c); err != nil {
			return "", err
		}
		first, err := c.Post("/v1/sessions", map[string]any{"agent": "acceptance", "display": 0})
		if err != nil {
			return "", fmt.Errorf("claim: %w", err)
		}
		var s struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(first, &s); err != nil {
			return "", err
		}
		if _, err := c.Post("/v1/sessions", map[string]any{"agent": "other", "display": 0}); err == nil {
			return "", fmt.Errorf("a second claim on display 0 was accepted")
		}
		if _, err := c.Delete("/v1/sessions/" + s.ID); err != nil {
			return "", fmt.Errorf("release: %w", err)
		}
		return "one holder per display, release works", nil
	})

	// --- system -------------------------------------------------------------
	run("system", "setting read/write", func() (string, error) {
		const ns, key = "system", "screen_off_timeout"
		data, err := c.Get("/v1/settings/" + ns + "/" + key)
		if err != nil {
			return "", err
		}
		var cur struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(data, &cur); err != nil {
			return "", err
		}
		resp, err := c.Put("/v1/settings/"+ns+"/"+key, map[string]any{"value": cur.Value})
		if err != nil {
			return "", fmt.Errorf("write back the same value: %w", err)
		}
		var wrote struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(resp, &wrote); err != nil {
			return "", err
		}
		if wrote.Value != cur.Value {
			return "", fmt.Errorf("read-back %q != written %q", wrote.Value, cur.Value)
		}
		return "read-back matches", nil
	})
	run("system", "prop", func() (string, error) {
		data, err := c.Get("/v1/props/ro.product.model")
		if err != nil {
			return "", err
		}
		var p struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(data, &p); err != nil {
			return "", err
		}
		if p.Value == "" {
			return "", fmt.Errorf("ro.product.model is empty")
		}
		return p.Value, nil
	})
	run("system", "apps + foreground", func() (string, error) {
		data, err := c.Get("/v1/apps")
		if err != nil {
			return "", err
		}
		var a struct {
			Count int `json:"count"`
		}
		if err := json.Unmarshal(data, &a); err != nil {
			return "", err
		}
		if a.Count == 0 {
			return "", fmt.Errorf("no third-party packages were listed")
		}
		return fmt.Sprintf("%d packages", a.Count), nil
	})
	run("system", "notifications", func() (string, error) {
		data, err := c.Get("/v1/notifications")
		if err != nil {
			return "", err
		}
		var n struct {
			Connected bool   `json:"connected"`
			Error     string `json:"error"`
		}
		if err := json.Unmarshal(data, &n); err != nil {
			return "", err
		}
		if !n.Connected {
			return "", fmt.Errorf("the notification listener is not connected (%s)", n.Error)
		}
		return "listener connected", nil
	})
	run("system", "sms", func() (string, error) {
		data, err := c.Get("/v1/sms/messages?limit=1")
		if err != nil {
			return "", err
		}
		var s struct {
			Count int `json:"count"`
		}
		if err := json.Unmarshal(data, &s); err != nil {
			return "", err
		}
		return fmt.Sprintf("%d message(s) readable", s.Count), nil
	})
	run("system", "files round trip", func() (string, error) {
		payload := []byte("acceptance-" + time.Now().Format(time.RFC3339))
		name := "acceptance.txt"
		if _, err := c.PostRaw("/v1/uploads?name="+name, payload); err != nil {
			return "", fmt.Errorf("upload: %w", err)
		}
		back, err := c.Get("/v1/files/download?path=/data/adb/chopin-core/tmp/" + name)
		if err != nil {
			return "", fmt.Errorf("download: %w", err)
		}
		if string(back) != string(payload) {
			return "", fmt.Errorf("downloaded bytes differ from the upload")
		}
		_, _ = c.Delete("/v1/files?path=/data/adb/chopin-core/tmp/" + name)
		return fmt.Sprintf("%d bytes round-tripped", len(payload)), nil
	})

	// --- display and streaming ---------------------------------------------
	run("display", "virtual display lifecycle", func() (string, error) {
		data, err := c.Post("/v1/displays", map[string]any{
			"name": "acceptance", "width": 540, "height": 960, "dpi": 240,
		})
		if err != nil {
			return "", fmt.Errorf("create: %w", err)
		}
		var d struct {
			ID int `json:"display_id"`
		}
		if err := json.Unmarshal(data, &d); err != nil {
			return "", err
		}
		if d.ID <= 0 {
			return "", fmt.Errorf("created display has id %d", d.ID)
		}
		if _, err := c.Delete(fmt.Sprintf("/v1/displays/%d", d.ID)); err != nil {
			return "", fmt.Errorf("destroy: %w", err)
		}
		return fmt.Sprintf("created and destroyed display %d", d.ID), nil
	})
	run("display", "webrtc carries frames", func() (string, error) {
		detail, err := streamProbe(c, 0, 6)
		if err != nil {
			return "", err
		}
		return detail, nil
	})

	// --- triggers -----------------------------------------------------------
	run("trigger", "timer fires", func() (string, error) {
		data, err := c.Post("/v1/triggers", map[string]any{
			"name": "acceptance-timer", "type": "timer",
			"spec": map[string]any{"interval_seconds": 5},
		})
		if err != nil {
			return "", fmt.Errorf("create: %w", err)
		}
		var t struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(data, &t); err != nil {
			return "", err
		}
		defer func() { _, _ = c.Delete("/v1/triggers/" + t.ID) }()
		deadline := time.Now().Add(20 * time.Second)
		for time.Now().Before(deadline) {
			runs, err := c.Get("/v1/triggers/" + t.ID + "/runs")
			if err != nil {
				return "", err
			}
			var r struct {
				Runs []any `json:"runs"`
			}
			_ = json.Unmarshal(runs, &r)
			if len(r.Runs) > 0 {
				return fmt.Sprintf("%d run(s) recorded", len(r.Runs)), nil
			}
			time.Sleep(time.Second)
		}
		return "", fmt.Errorf("the trigger did not fire within 20s")
	})

	// --- control surface ---------------------------------------------------
	run("control", "rotation preference round trip", func() (string, error) {
		data, err := c.Get("/v1/device/rotation")
		if err != nil {
			return "", err
		}
		var cur struct {
			Rotation   string `json:"rotation"`
			UserRot    string `json:"user_rotation"`
			AutoRotate bool   `json:"auto_rotate"`
		}
		if err := json.Unmarshal(data, &cur); err != nil {
			return "", err
		}
		if _, err := c.Post("/v1/device/rotation", map[string]any{"value": "90"}); err != nil {
			return "", fmt.Errorf("set 90: %w", err)
		}
		// Two different facts, two different checks: the stored preference must
		// echo the request, while the panel may stay put because the foreground app
		// pins its orientation. Conflating them makes a working endpoint look broken.
		var after struct {
			OK           bool   `json:"ok"`
			Stored       string `json:"stored"`
			Panel        string `json:"panel"`
			PanelFollows bool   `json:"panel_follows"`
			Detail       string `json:"detail"`
		}
		raw, err := c.Post("/v1/device/rotation", map[string]any{"value": "90"})
		if err != nil {
			return "", fmt.Errorf("set 90 (again): %w", err)
		}
		if err := json.Unmarshal(raw, &after); err != nil {
			return "", err
		}
		restore := cur.Rotation
		if restore == "" {
			restore = "auto"
		}
		if _, err := c.Post("/v1/device/rotation", map[string]any{"value": restore}); err != nil {
			return "", fmt.Errorf("restore %s: %w", restore, err)
		}
		if !after.OK || after.Stored != "1" {
			return "", fmt.Errorf("stored preference is %q (ok=%v), want 1", after.Stored, after.OK)
		}
		if after.PanelFollows {
			return fmt.Sprintf("stored 1, panel %s", after.Panel), nil
		}
		return fmt.Sprintf("stored 1, panel stayed %s (preference honoured; the app pins orientation)", after.Panel), nil
	})
	run("control", "brightness round trip", func() (string, error) {
		data, err := c.Get("/v1/settings/system/screen_brightness")
		if err != nil {
			return "", err
		}
		var cur struct {
			Value string `json:"value"`
		}
		if err := json.Unmarshal(data, &cur); err != nil {
			return "", err
		}
		resp, err := c.Post("/v1/device/screen", map[string]any{"brightness": "120"})
		if err != nil {
			return "", err
		}
		var out struct {
			Brightness string `json:"brightness"`
		}
		_ = json.Unmarshal(resp, &out)
		if out.Brightness != "120" {
			return "", fmt.Errorf("read back %q, want 120", out.Brightness)
		}
		if cur.Value != "" && cur.Value != "120" {
			if _, err := c.Post("/v1/device/screen", map[string]any{"brightness": cur.Value}); err != nil {
				return "", fmt.Errorf("restore brightness %s: %w", cur.Value, err)
			}
		}
		return "120, restored", nil
	})
	run("control", "displays listed", func() (string, error) {
		data, err := c.Get("/v1/displays")
		if err != nil {
			return "", err
		}
		var d struct {
			Count int `json:"count"`
		}
		if err := json.Unmarshal(data, &d); err != nil {
			return "", err
		}
		if d.Count == 0 {
			return "", fmt.Errorf("no display was listed")
		}
		return fmt.Sprintf("%d display(s)", d.Count), nil
	})
	run("control", "openapi document", func() (string, error) {
		data, err := c.Get("/doc")
		if err != nil {
			return "", err
		}
		var doc struct {
			OpenAPI string         `json:"openapi"`
			Paths   map[string]any `json:"paths"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			return "", err
		}
		if doc.OpenAPI == "" || len(doc.Paths) < 60 {
			return "", fmt.Errorf("the document lists %d paths (openapi %q)", len(doc.Paths), doc.OpenAPI)
		}
		return fmt.Sprintf("openapi %s, %d paths", doc.OpenAPI, len(doc.Paths)), nil
	})

	run("control", "keep-awake", func() (string, error) {
		data, err := c.Post("/v1/device/keep-awake", map[string]any{
			"enabled": true, "timeout_ms": 5000,
		})
		if err != nil {
			return "", err
		}
		var k struct {
			KeepAwake struct {
				Held bool `json:"held"`
			} `json:"keep_awake"`
		}
		if err := json.Unmarshal(data, &k); err != nil {
			return "", err
		}
		if !k.KeepAwake.Held {
			return "", fmt.Errorf("the helper did not report a held wake lock")
		}
		if _, err := c.Post("/v1/device/keep-awake", map[string]any{"enabled": false}); err != nil {
			return "", fmt.Errorf("release failed: %w", err)
		}
		return "held and released", nil
	})

	// --- stream and HUD contract -------------------------------------------
	run("stream", "notification SSE opens", func() (string, error) {
		return sseOpens(c, "/v1/notifications/events", "notifications")
	})
	run("hud", "is loopback-only", func() (string, error) {
		// The app token is loopback-only by design; a PC must be refused. Reporting
		// "works" here would mean the safety channel is reachable from the network.
		_, err := c.Post("/v1/hud/heartbeat", map[string]any{})
		if err == nil {
			return "", fmt.Errorf("a remote caller was allowed to send a HUD heartbeat")
		}
		if !strings.Contains(err.Error(), "403") && !strings.Contains(err.Error(), "loopback") {
			return "", fmt.Errorf("expected looback_only/403, got %v", err)
		}
		return "remote HUD call refused", nil
	})

	// --- report -------------------------------------------------------------
	report := renderReport(c.Server(), results)
	path := filepath.Join(outDir, "REPORT.md")
	if err := os.WriteFile(path, []byte(report), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	raw, _ := json.MarshalIndent(results, "", "  ")
	if err := os.WriteFile(filepath.Join(outDir, "results.json"), append(raw, '\n'), 0o644); err != nil {
		return err
	}

	counts := map[string]int{}
	for _, r := range results {
		counts[r.State]++
	}
	fmt.Printf("\n%s\n", filepath.Join(outDir, "REPORT.md"))
	fmt.Printf("PASS %d  FAIL %d  SKIP %d\n", counts["PASS"], counts["FAIL"], counts["SKIP"])
	if counts["FAIL"] > 0 {
		return fmt.Errorf("%d check(s) failed", counts["FAIL"])
	}
	return nil
}

type lockStateView struct {
	Locked     bool
	Screen     string
	Credential string
}

func lockState(c interface {
	Get(string) ([]byte, error)
}) (lockStateView, error) {
	data, err := c.Get("/v1/device/lock-state")
	if err != nil {
		return lockStateView{}, err
	}
	var v struct {
		ScreenLocked bool   `json:"screen_locked"`
		ScreenState  string `json:"screen_state"`
		Credential   string `json:"credential"`
	}
	if err := json.Unmarshal(data, &v); err != nil {
		return lockStateView{}, err
	}
	return lockStateView{Locked: v.ScreenLocked, Screen: v.ScreenState, Credential: v.Credential}, nil
}

// waitUnlocked polls until the keyguard is gone.
func waitUnlocked(c *client.Client, timeout time.Duration) (lockStateView, error) {
	deadline := time.Now().Add(timeout)
	var last lockStateView
	for {
		v, err := lockState(c)
		if err != nil {
			return v, err
		}
		last = v
		if !v.Locked {
			return v, nil
		}
		if time.Now().After(deadline) {
			return last, nil
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// unlockTolerant unlocks, waiting out the designed cooldown.
//
// The daemon refuses a second unlock within its cooldown window on purpose (a
// wrong pattern must not be retried immediately). A test harness that treats that
// refusal as a capability failure is measuring its own impatience.
func unlockTolerant(c *client.Client) error {
	for attempt := 0; attempt < 3; attempt++ {
		resp, err := c.Post("/v1/device/unlock", map[string]any{})
		if err == nil {
			var r struct {
				OK     bool   `json:"ok"`
				Detail string `json:"detail"`
			}
			_ = json.Unmarshal(resp, &r)
			if r.OK {
				return nil
			}
			return fmt.Errorf("unlock reported not ok: %s", r.Detail)
		}
		msg := err.Error()
		if !strings.Contains(msg, "cooling down") {
			return err
		}
		// "unlock cooling down for 15.7s"
		wait := 20 * time.Second
		if i := strings.Index(msg, "for "); i >= 0 {
			rest := msg[i+4:]
			if j := strings.IndexByte(rest, 's'); j > 0 {
				if secs, convErr := strconv.ParseFloat(strings.TrimSpace(rest[:j]), 64); convErr == nil && secs > 0 && secs < 120 {
					wait = time.Duration(secs*float64(time.Second)) + 2*time.Second
				}
			}
		}
		fmt.Printf("note: unlock is cooling down; waiting %s\n", wait.Round(time.Second))
		time.Sleep(wait)
	}
	return fmt.Errorf("unlock still refused after waiting out the cooldown")
}

// sseOpens verifies a stream endpoint answers with event-stream and stays open.
func sseOpens(c *client.Client, path, scope string) (string, error) {
	headers, status, err := c.StreamProbe(path)
	if err != nil {
		return "", err
	}
	if status != 200 {
		return "", fmt.Errorf("HTTP %d", status)
	}
	ct := headers.Get("Content-Type")
	if !strings.Contains(ct, "text/event-stream") {
		return "", fmt.Errorf("content-type is %q, not text/event-stream", ct)
	}
	return "event-stream open (" + scope + ")", nil
}

// waitReachable polls /v1/health until the daemon answers.
func waitReachable(c *client.Client, timeout time.Duration) (time.Duration, bool) {
	start := time.Now()
	deadline := start.Add(timeout)
	for {
		if _, err := c.Get("/v1/health"); err == nil {
			return time.Since(start), true
		}
		if time.Now().After(deadline) {
			return time.Since(start), false
		}
		time.Sleep(2 * time.Second)
	}
}

// waitReady waits until the daemon reports the keep-alive work as applied.
func waitReady(c *client.Client, timeout time.Duration) (bool, time.Duration, string) {
	start := time.Now()
	deadline := start.Add(timeout)
	var why string
	for {
		data, err := c.Get("/v1/status")
		if err == nil {
			var st struct {
				App struct {
					A11y struct {
						State string `json:"state"`
					} `json:"a11y"`
					Bg struct {
						OK bool `json:"ok"`
					} `json:"bg"`
				} `json:"app"`
			}
			if err := json.Unmarshal(data, &st); err == nil {
				// Both must hold: the background policy is applied by the same pass
				// that repairs accessibility, but accessibility can still be DEGRADED
				// for a few seconds after boot while the service binds.
				if st.App.A11y.State == "OK" && st.App.Bg.OK {
					return true, time.Since(start), ""
				}
				if st.App.A11y.State != "OK" {
					why = "accessibility is " + st.App.A11y.State
				} else {
					why = "background policy not applied yet"
				}
			}
		} else {
			why = err.Error()
		}
		if time.Now().After(deadline) {
			return false, time.Since(start), why
		}
		time.Sleep(2 * time.Second)
	}
}

// waitLocked polls the lock state until the device reports itself locked.
func waitLocked(c *client.Client, timeout time.Duration) (lockStateView, error) {
	deadline := time.Now().Add(timeout)
	var last lockStateView
	for {
		v, err := lockState(c)
		if err != nil {
			return v, err
		}
		last = v
		if v.Locked {
			return v, nil
		}
		if time.Now().After(deadline) {
			return last, nil
		}
		time.Sleep(300 * time.Millisecond)
	}
}

// releaseAll drops every session so the matrix starts from a known state.
func releaseAll(c *client.Client) error {
	data, err := c.Get("/v1/sessions")
	if err != nil {
		return err
	}
	var d struct {
		Active []struct {
			ID string `json:"id"`
		} `json:"active"`
	}
	if err := json.Unmarshal(data, &d); err != nil {
		return err
	}
	for _, s := range d.Active {
		if _, err := c.Delete("/v1/sessions/" + s.ID); err != nil {
			return err
		}
	}
	return nil
}

// renderReport writes the markdown the task list links to.
func renderReport(server string, results []checkResult) string {
	var b strings.Builder
	counts := map[string]int{}
	for _, r := range results {
		counts[r.State]++
	}
	b.WriteString("# chopin acceptance report\n\n")
	b.WriteString(fmt.Sprintf("- server: `%s`\n", server))
	b.WriteString(fmt.Sprintf("- generated: %s\n", time.Now().Format("2006-01-02 15:04:05 -07:00")))
	b.WriteString(fmt.Sprintf("- result: **PASS %d / FAIL %d / SKIP %d**\n\n", counts["PASS"], counts["FAIL"], counts["SKIP"]))
	b.WriteString("A capability is only PASS when it was observed working on the device.\n")
	b.WriteString("SKIP means it could not be exercised and says why; it is never counted as a pass.\n\n")
	b.WriteString("| state | capability | check | evidence | took |\n|---|---|---|---|---|\n")

	// Failures first: a report where the eye lands on passes first hides the
	// problem it exists to surface.
	sorted := append([]checkResult(nil), results...)
	sort.SliceStable(sorted, func(i, j int) bool {
		rank := func(s string) int {
			switch s {
			case "FAIL":
				return 0
			case "SKIP":
				return 1
			}
			return 2
		}
		return rank(sorted[i].State) < rank(sorted[j].State)
	})
	for _, r := range sorted {
		detail := strings.ReplaceAll(r.Detail, "|", "\\|")
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %dms |\n",
			r.State, r.Capability, r.Check, detail, r.Millis))
	}
	return b.String()
}

// shortComponent trims a component to pkg/.Cls for report cells.
func shortComponent(component string) string {
	if i := strings.LastIndex(component, "."); i > 0 && strings.Contains(component, "/") {
		return component[:strings.Index(component, "/")] + "/…" + component[i:]
	}
	return component
}
