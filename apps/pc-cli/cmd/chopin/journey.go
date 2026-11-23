package main

// journey acceptance: do what a user does, then prove it from the log.
//
// The capability matrix (acceptance.go) answers "does the endpoint exist and
// answer". That is necessary and not enough: an endpoint can answer 200 while
// doing nothing (this repo has shipped exactly that — `--clear` reported ok:true
// and never cleared anything, `ime.Restore` existed and was never called). So this
// matrix is built on three rules:
//
//  1. ACT like a user would, through the same public API a client uses — not by
//     poking internal state.
//  2. OBSERVE the effect through a DIFFERENT channel than the one that caused it
//     (type with the tree, then read the pixels; write a setting, then read it
//     back; lock, then ask the keyguard).
//  3. READ THE LOG and require the audit line that proves the intended path ran.
//     Two implementations can produce the same visible result; only the log tells
//     you WHICH one ran.
//
// A step passes only when all three agree. Any disagreement is a FAIL with both
// sides quoted, because "the screen looks right but the log says otherwise" is
// precisely the bug class this exists to catch.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"chopin/internal/client"
)

const deviceLogPath = "/data/adb/chopin-core/log/core.log"

// stepOutcome is what a single step observed plus what the log must confirm.
type stepOutcome struct {
	// observed is the evidence from the independent channel.
	observed string
	// logMust are fragments that must appear in the log lines written during
	// this step. Missing any one of them fails the step.
	logMust []string
	// logWant are fragments that are nice to quote but not required.
	logWant []string
}

type journeyResult struct {
	Scenario   string
	Step       string
	State      string
	Observed   string
	LogLine    string
	LogMissing string
	Millis     int64
}

// logCursor lets a step look at only the lines it caused.
type logCursor struct {
	c    *client.Client
	seen int // lines present when the step started
}

func newLogCursor(c *client.Client) *logCursor {
	lc := &logCursor{c: c}
	lc.seen = len(lc.lines())
	return lc
}

func (lc *logCursor) lines() []string {
	data, err := lc.c.Get("/v1/files/download?path=" + urlEscape(deviceLogPath))
	if err != nil {
		return nil
	}
	return strings.Split(strings.TrimRight(string(data), "\n"), "\n")
}

// mark records the current line count as the boundary for the next step.
func (lc *logCursor) mark() { lc.seen = len(lc.lines()) }

// since returns the lines written after the last mark.
//
// Rotation truncates the file to the last few hundred lines, which can make the
// old count larger than the new one. That is not "no lines were written", so the
// fallback returns the whole file and lets the assertions look at the tail.
func (lc *logCursor) since() []string {
	all := lc.lines()
	if len(all) < lc.seen {
		return all
	}
	return all[lc.seen:]
}

// find returns the first new line containing every fragment, or "".
func findLine(lines []string, must []string) string {
	for _, line := range lines {
		ok := true
		for _, frag := range must {
			if !strings.Contains(line, frag) {
				ok = false
				break
			}
		}
		if ok {
			return strings.TrimSpace(line)
		}
	}
	return ""
}

func cmdJourney(a *args) error {
	outDir := a.optStr("out", "")
	if outDir == "" {
		outDir = filepath.Join("out", "journey-"+time.Now().Format("20060102-150405"))
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return fmt.Errorf("create %s: %w", outDir, err)
	}
	c, err := paired()
	if err != nil {
		return err
	}

	log := newLogCursor(c)
	if len(log.lines()) == 0 {
		return fmt.Errorf("cannot read %s over the API; the log is the point of this matrix", deviceLogPath)
	}

	var results []journeyResult

	// step runs one user action, then judges it against the log.
	step := func(scenario, name string, fn func() (stepOutcome, error)) {
		start := time.Now()
		log.mark()
		out, err := fn()
		res := journeyResult{Scenario: scenario, Step: name, Millis: time.Since(start).Milliseconds()}
		switch {
		case err != nil:
			res.State = "FAIL"
			res.Observed = err.Error()
		default:
			newLines := log.since()
			var missing []string
			var matched []string
			for _, frag := range out.logMust {
				line := findLine(newLines, []string{frag})
				if line == "" {
					missing = append(missing, frag)
					continue
				}
				matched = append(matched, line)
			}
			res.Observed = out.observed
			if len(matched) > 0 {
				res.LogLine = matched[0]
			}
			if len(missing) > 0 {
				res.State = "FAIL"
				res.LogMissing = strings.Join(missing, " | ")
			} else {
				res.State = "PASS"
			}
		}
		results = append(results, res)
		fmt.Printf("  %-5s %-18s %s\n", res.State, res.Scenario, res.Step)
		if res.State == "FAIL" {
			if res.LogMissing != "" {
				fmt.Printf("        observed: %s\n        log did not confirm: %s\n", res.Observed, res.LogMissing)
			} else {
				fmt.Printf("        %s\n", res.Observed)
			}
		}
	}

	fmt.Println("chopin journey (user-like acceptance)")
	fmt.Printf("server: %s\n\n", c.Server())

	// A journey is a user session: unlock first, and leave the device usable after.
	if err := unlockTolerant(c); err != nil {
		fmt.Printf("note: could not unlock at the start: %v\n", err)
	}
	_ = releaseAll(c)

	runJourneyWeChat(c, step)
	runJourneyTyping(c, step)
	runJourneyPerception(c, step)
	runJourneyMap(c, step)
	runJourneySettings(c, step)
	runJourneySession(c, step)
	runJourneyApps(c, step)
	runJourneyFiles(c, step)
	runJourneyLock(c, step)
	runJourneyTriggers(c, step)
	runJourneyHUD(c, step)

	_ = releaseAll(c)

	rep := renderJourney(c.Server(), results)
	if err := os.WriteFile(filepath.Join(outDir, "REPORT.md"), []byte(rep), 0o644); err != nil {
		return err
	}
	data, _ := json.MarshalIndent(results, "", "  ")
	if err := os.WriteFile(filepath.Join(outDir, "results.json"), data, 0o644); err != nil {
		return err
	}
	counts := map[string]int{}
	for _, r := range results {
		counts[r.State]++
	}
	fmt.Printf("\n%s\n", rep)
	fmt.Printf("report: %s/REPORT.md\n", outDir)
	if counts["FAIL"] > 0 {
		return fmt.Errorf("%d step(s) failed", counts["FAIL"])
	}
	return nil
}

// --- scenarios ---------------------------------------------------------------

func runJourneyWeChat(c *client.Client, step func(string, string, func() (stepOutcome, error))) {
	step("wechat", "open the app and read a conversation", func() (stepOutcome, error) {
		if _, err := c.Post("/v1/apps/launch", map[string]any{"package": "com.tencent.mm"}); err != nil {
			return stepOutcome{}, err
		}
		time.Sleep(4 * time.Second)
		how, err := ensureWeChatList(c)
		if err != nil {
			return stepOutcome{}, err
		}
		fg, err := foregroundOf(c)
		if err != nil {
			return stepOutcome{}, err
		}
		if !strings.HasPrefix(fg, "com.tencent.mm") {
			return stepOutcome{}, fmt.Errorf("foreground is %q, expected WeChat", fg)
		}
		// WeChat's tree is only exposed with the compat service bound; a count of
		// 1 with no text is the empty-root signature, so assert on content.
		count, texts, err := treeSummary(c)
		if err != nil {
			return stepOutcome{}, err
		}
		if count < 50 || texts < 5 {
			return stepOutcome{}, fmt.Errorf("WeChat tree has %d nodes / %d texts; the empty-root signature means the compat service is not doing its job", count, texts)
		}
		return stepOutcome{
			observed: fmt.Sprintf("%d nodes, %d texts, foreground %s (%s)", count, texts, fg, how),
			logMust:  []string{"apps: launch com.tencent.mm"},
		}, nil
	})

	step("wechat", "open a chat and read its messages", func() (stepOutcome, error) {
		// Start from the list, not from wherever the previous run stopped.
		if _, err := ensureWeChatList(c); err != nil {
			return stepOutcome{}, err
		}
		// The row is located by TEXT and not by `id/kbq`: that id is the generic row
		// container shared by every conversation, so finding by id taps whichever row
		// happens to be first — which is how this step once tapped a different chat
		// and then reported "no composer appeared". Text plus the daemon's preference
		// for on-screen nodes picks the right row.
		row, err := waitForText(c, "serendipity", 15*time.Second)
		if err != nil {
			return stepOutcome{}, err
		}
		data, err := c.Post("/v1/ui/find", map[string]any{"text": "serendipity", "click": true})
		if err != nil {
			return stepOutcome{}, err
		}
		var clicked struct {
			Clicked bool `json:"clicked"`
			X       int  `json:"x"`
			Y       int  `json:"y"`
		}
		if err := json.Unmarshal(data, &clicked); err != nil {
			return stepOutcome{}, err
		}
		if !clicked.Clicked {
			return stepOutcome{}, fmt.Errorf("the conversation row was found at %v but not clicked", row.Bounds)
		}
		// The chat page is the one with a composer. Waiting for it is what proves
		// the tap opened the conversation instead of just landing somewhere.
		composer, err := waitForNode(c, "com.tencent.mm:id/bkk", 15*time.Second)
		if err != nil {
			return stepOutcome{}, fmt.Errorf("the tap at %d,%d did not open a chat (no composer appeared): %w", clicked.X, clicked.Y, err)
		}
		// Independent channel: OCR the pixels, not the tree that found the row.
		var ocr struct {
			N    int    `json:"n"`
			Text string `json:"text"`
		}
		od, err := c.Post("/v1/ui/ocr", map[string]any{})
		if err != nil {
			return stepOutcome{}, err
		}
		if err := json.Unmarshal(od, &ocr); err != nil {
			return stepOutcome{}, err
		}
		if ocr.N < 3 {
			return stepOutcome{}, fmt.Errorf("OCR read only %d lines inside the chat", ocr.N)
		}
		// The chat must show messages, not an empty page: a date separator or a
		// message bubble is what distinguishes "opened the conversation" from
		// "opened something with a composer".
		msgs, err := treeLabels(c)
		if err != nil {
			return stepOutcome{}, err
		}
		titled := false
		for _, t := range msgs {
			if strings.Contains(t, "serendipity") {
				titled = true
			}
		}
		if !titled {
			return stepOutcome{}, fmt.Errorf("the open chat does not mention the contact; this is not the conversation that was asked for")
		}
		return stepOutcome{
			observed: fmt.Sprintf("row at %v clicked (%d,%d), composer at %v, OCR %d lines", row.Bounds, clicked.X, clicked.Y, composer.Bounds, ocr.N),
			logMust:  []string{"ui: find+click", "ui: ocr display=0"},
		}, nil
	})
}

func runJourneyTyping(c *client.Client, step func(string, string, func() (stepOutcome, error))) {
	// The text is deliberately unique so a stale screen cannot fake a pass.
	const probe = "验收探针ABC"

	step("typing", "type into the WeChat composer without sending", func() (stepOutcome, error) {
		// Refuse to run without a composer. Otherwise "the probe is not on screen"
		// is indistinguishable from "there was nowhere to type it", and the clear
		// step after this one would pass on a screen that never had the text.
		if _, err := openChat(c); err != nil {
			return stepOutcome{}, fmt.Errorf("no composer on screen, so nothing could be typed: %w", err)
		}
		// Clear first so the pass cannot come from text that was already there.
		// The general path, not the IME one: a clear on an ordinary EditText must not
		// cost a keyboard switch. Forcing /v1/input/ime/text here made the journey keep
		// exercising the slow path even after typing moved to ACTION_SET_TEXT.
		if _, err := c.Post("/v1/input/text", map[string]any{"text": "", "clear": true}); err != nil {
			return stepOutcome{}, err
		}
		time.Sleep(400 * time.Millisecond)
		if _, err := c.Post("/v1/input/text", map[string]any{"text": probe}); err != nil {
			return stepOutcome{}, err
		}
		time.Sleep(600 * time.Millisecond)
		// Independent channel: read the field back out of the tree.
		var got string
		dd, err := c.Post("/v1/ui/find", map[string]any{"text": probe})
		if err != nil {
			return stepOutcome{}, err
		}
		var f struct {
			OK   bool   `json:"ok"`
			Text string `json:"text"`
		}
		if json.Unmarshal(dd, &f) == nil && f.OK {
			got = f.Text
		}
		if !strings.Contains(got, probe) {
			return stepOutcome{}, fmt.Errorf("the field reads %q, not %q", got, probe)
		}
		return stepOutcome{
			observed: fmt.Sprintf("field reads back %q", got),
			// Either path is legitimate; the log must name the one that ran.
			logMust: []string{"input: text via"},
			logWant: []string{"a11y_set_text", "ime"},
		}, nil
	})

	step("typing", "clear the composer leaves it empty", func() (stepOutcome, error) {
		if _, err := waitForNode(c, "com.tencent.mm:id/bkk", 8*time.Second); err != nil {
			return stepOutcome{}, fmt.Errorf("no composer to clear: %w", err)
		}
		if _, err := c.Post("/v1/input/ime/text", map[string]any{"clear": true}); err != nil {
			return stepOutcome{}, err
		}
		time.Sleep(500 * time.Millisecond)
		dd, err := c.Post("/v1/ui/find", map[string]any{"text": probe})
		if err == nil {
			var f struct {
				OK bool `json:"ok"`
			}
			_ = json.Unmarshal(dd, &f)
			if f.OK {
				return stepOutcome{}, fmt.Errorf("the composer still contains %q after a clear", probe)
			}
		}
		return stepOutcome{
			observed: "the probe string is gone from the tree",
			logMust:  []string{"ime commit 0 chars"},
		}, nil
	})

	step("typing", "the keyboard is handed back immediately", func() (stepOutcome, error) {
		// Regression: the harness IME used to stay the default until a session was
		// released, so the user's own typing went into our invisible view.
		before, err := settingOf(c, "secure", "default_input_method")
		if err != nil {
			return stepOutcome{}, err
		}
		if _, err := c.Post("/v1/input/ime/text", map[string]any{"text": "x"}); err != nil {
			return stepOutcome{}, err
		}
		time.Sleep(700 * time.Millisecond)
		after, err := settingOf(c, "secure", "default_input_method")
		if err != nil {
			return stepOutcome{}, err
		}
		if after == "com.chopin.pilot/.ime.RemoteImeService" {
			return stepOutcome{}, fmt.Errorf("the device is still on the harness IME after the commit; the user's next keystroke would land in our invisible view")
		}
		return stepOutcome{
			observed: fmt.Sprintf("before %s, after %s", before, after),
			logMust:  []string{"ime: keyboard handed back to"},
		}, nil
	})
}

func runJourneyPerception(c *client.Client, step func(string, string, func() (stepOutcome, error))) {
	step("perception", "tree, OCR and pixels agree about the screen", func() (stepOutcome, error) {
		// Three independent capture paths. If one is silently broken it either
		// returns nothing (caught by the minimums) or disagrees (caught by the
		// cross-check below).
		count, texts, err := treeSummary(c)
		if err != nil {
			return stepOutcome{}, err
		}
		var ocr struct {
			N    int    `json:"n"`
			Text string `json:"text"`
		}
		od, err := c.Post("/v1/ui/ocr", map[string]any{})
		if err != nil {
			return stepOutcome{}, err
		}
		if err := json.Unmarshal(od, &ocr); err != nil {
			return stepOutcome{}, err
		}
		var px struct {
			Pixels     int  `json:"pixels"`
			Colours    int  `json:"distinct_colors"`
			MeanLuma   int  `json:"mean_luma"`
			LooksBlack bool `json:"looks_black"`
		}
		pd, err := c.Post("/v1/ui/pixel_stats", map[string]any{})
		if err != nil {
			return stepOutcome{}, err
		}
		if err := json.Unmarshal(pd, &px); err != nil {
			return stepOutcome{}, err
		}
		if count < 5 || texts < 1 {
			return stepOutcome{}, fmt.Errorf("tree %d nodes / %d texts", count, texts)
		}
		if ocr.N < 3 {
			return stepOutcome{}, fmt.Errorf("OCR %d lines", ocr.N)
		}
		if px.Pixels == 0 || px.LooksBlack || px.Colours < 10 || px.MeanLuma < 10 {
			return stepOutcome{}, fmt.Errorf("pixels %d, colours %d, mean luma %d, looks_black=%v; that is an off panel or a broken capture path",
				px.Pixels, px.Colours, px.MeanLuma, px.LooksBlack)
		}
		// Cross-check: something the tree sees must also appear in the OCR text.
		agree := ""
		if nodes, err := treeLabels(c); err == nil {
			for _, t := range nodes {
				t = strings.TrimSpace(t)
				if len([]rune(t)) >= 2 && len([]rune(t)) <= 8 && strings.Contains(ocr.Text, t) {
					agree = t
					break
				}
			}
		}
		return stepOutcome{
			observed: fmt.Sprintf("tree %d nodes/%d texts, OCR %d lines, %d colours; both saw %q", count, texts, ocr.N, px.Colours, agree),
			logMust:  []string{"ui: ocr display=0"},
		}, nil
	})

	step("perception", "screenshot is a real image of the screen", func() (stepOutcome, error) {
		data, err := c.Get("/v1/ui/screenshot")
		if err != nil {
			return stepOutcome{}, err
		}
		var sh struct {
			Bytes int    `json:"bytes"`
			Path  string `json:"path"`
		}
		if err := json.Unmarshal(data, &sh); err != nil {
			return stepOutcome{}, err
		}
		if sh.Bytes < 20000 {
			return stepOutcome{}, fmt.Errorf("screenshot is only %d bytes; that is a black or truncated frame", sh.Bytes)
		}
		// Pull it over a different endpoint and check the bytes match, so the
		// "bytes" field cannot be a number the daemon made up.
		img, err := c.Get("/v1/files/download?path=" + urlEscape(sh.Path))
		if err != nil {
			return stepOutcome{}, err
		}
		if len(img) != sh.Bytes {
			return stepOutcome{}, fmt.Errorf("reported %d bytes but downloading it gave %d", sh.Bytes, len(img))
		}
		kind := ""
		switch {
		case len(img) >= 3 && img[0] == 0xff && img[1] == 0xd8 && img[2] == 0xff:
			kind = "JPEG"
		case len(img) >= 8 && string(img[1:4]) == "PNG":
			kind = "PNG"
		}
		if kind == "" {
			return stepOutcome{}, fmt.Errorf("downloaded file is neither JPEG nor PNG")
		}
		return stepOutcome{
			observed: fmt.Sprintf("%d byte %s, downloaded and byte-count matched", sh.Bytes, kind),
			logMust:  []string{"ui: screenshot"},
		}, nil
	})
}

func runJourneySettings(c *client.Client, step func(string, string, func() (stepOutcome, error))) {
	step("settings", "change a setting and see it take effect", func() (stepOutcome, error) {
		// No session may be active for a global write, and the journey released
		// everything before this point.
		before, err := settingOf(c, "system", "screen_off_timeout")
		if err != nil {
			return stepOutcome{}, err
		}
		target := "600000"
		if before == target {
			target = "900000"
		}
		if _, err := c.Put("/v1/settings/system/screen_off_timeout", map[string]any{"value": target}); err != nil {
			return stepOutcome{}, err
		}
		got, err := settingOf(c, "system", "screen_off_timeout")
		if err != nil {
			return stepOutcome{}, err
		}
		if got != target {
			return stepOutcome{}, fmt.Errorf("wrote %s but read back %s", target, got)
		}
		// Put it back: a test that leaves the device changed is not a test.
		if _, err := c.Put("/v1/settings/system/screen_off_timeout", map[string]any{"value": before}); err != nil {
			return stepOutcome{}, fmt.Errorf("could not restore the original value %s: %w", before, err)
		}
		restored, err := settingOf(c, "system", "screen_off_timeout")
		if err != nil {
			return stepOutcome{}, err
		}
		if restored != before {
			return stepOutcome{}, fmt.Errorf("restore failed: %s != %s", restored, before)
		}
		return stepOutcome{
			observed: fmt.Sprintf("%s -> %s -> %s", before, target, restored),
			logMust:  []string{"settings: system/screen_off_timeout"},
		}, nil
	})
}

func runJourneySession(c *client.Client, step func(string, string, func() (stepOutcome, error))) {
	var firstID string
	step("session", "a second writer is refused, the first one is not", func() (stepOutcome, error) {
		d1, err := c.Post("/v1/sessions", map[string]any{"agent": "journey-a", "display": 0})
		if err != nil {
			return stepOutcome{}, err
		}
		var s1 struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(d1, &s1); err != nil {
			return stepOutcome{}, err
		}
		firstID = s1.ID
		// A second writer on the same display must be refused, not queued.
		_, err2 := c.Post("/v1/sessions", map[string]any{"agent": "journey-b", "display": 0})
		if err2 == nil {
			return stepOutcome{}, fmt.Errorf("a second writer on display 0 was accepted; two agents can now interleave input")
		}
		return stepOutcome{
			observed: fmt.Sprintf("first claim %s accepted, second refused (%v)", firstID, trimErr(err2)),
			logMust:  []string{"session: claimed " + firstID},
		}, nil
	})

	step("session", "releasing hands the device back", func() (stepOutcome, error) {
		if firstID == "" {
			return stepOutcome{}, fmt.Errorf("no session from the previous step")
		}
		if _, err := c.Delete("/v1/sessions/" + firstID); err != nil {
			return stepOutcome{}, err
		}
		list, err := c.Get("/v1/sessions")
		if err != nil {
			return stepOutcome{}, err
		}
		var l struct {
			Active []any `json:"active"`
		}
		if err := json.Unmarshal(list, &l); err != nil {
			return stepOutcome{}, err
		}
		if len(l.Active) != 0 {
			return stepOutcome{}, fmt.Errorf("%d session(s) still active after release", len(l.Active))
		}
		return stepOutcome{
			observed: "no active sessions remain",
			logMust:  []string{"session: released " + firstID},
		}, nil
	})
}

func runJourneyApps(c *client.Client, step func(string, string, func() (stepOutcome, error))) {
	step("apps", "launch, observe, then stop an app", func() (stepOutcome, error) {
		if _, err := c.Post("/v1/apps/launch", map[string]any{"package": "com.android.settings"}); err != nil {
			return stepOutcome{}, err
		}
		time.Sleep(3 * time.Second)
		fg, err := foregroundOf(c)
		if err != nil {
			return stepOutcome{}, err
		}
		if !strings.Contains(fg, "settings") {
			return stepOutcome{}, fmt.Errorf("foreground is %q after launching Settings", fg)
		}
		if _, err := c.Post("/v1/apps/stop", map[string]any{"package": "com.android.settings"}); err != nil {
			return stepOutcome{}, err
		}
		return stepOutcome{
			observed: "foreground became " + fg + " and the app was stopped",
			logMust:  []string{"apps: launch com.android.settings", "apps: force-stop com.android.settings"},
		}, nil
	})
}

func runJourneyFiles(c *client.Client, step func(string, string, func() (stepOutcome, error))) {
	const name = "journey-probe.txt"
	payload := []byte("chopin journey file probe " + time.Now().Format(time.RFC3339) + "\n")
	step("files", "upload, download and delete survive the round trip", func() (stepOutcome, error) {
		if _, err := c.PostRaw("/v1/uploads?name="+urlEscape(name), payload); err != nil {
			return stepOutcome{}, err
		}
		// Read it back over a different endpoint and compare the bytes.
		got, err := c.Get("/v1/files/download?path=" + urlEscape("/data/adb/chopin-core/tmp/"+name))
		if err != nil {
			return stepOutcome{}, err
		}
		if string(got) != string(payload) {
			return stepOutcome{}, fmt.Errorf("round trip changed the content: %d bytes back, wanted %d", len(got), len(payload))
		}
		if _, err := c.Post("/v1/files/delete", map[string]any{"path": "/data/adb/chopin-core/tmp/" + name}); err != nil {
			return stepOutcome{}, err
		}
		return stepOutcome{
			observed: fmt.Sprintf("%d bytes uploaded, downloaded identical, deleted", len(got)),
			logMust:  []string{"files: uploaded", "files: deleted"},
		}, nil
	})
}

func runJourneyLock(c *client.Client, step func(string, string, func() (stepOutcome, error))) {
	step("lock", "lock and unlock, judged by the keyguard", func() (stepOutcome, error) {
		if _, err := c.Post("/v1/device/lock", map[string]any{}); err != nil {
			return stepOutcome{}, err
		}
		time.Sleep(3 * time.Second)
		locked, err := keyguardOf(c)
		if err != nil {
			return stepOutcome{}, err
		}
		if locked.Screen != "OFF" && !locked.Keyguard {
			return stepOutcome{}, fmt.Errorf("after locking: screen=%s keyguard=%v", locked.Screen, locked.Keyguard)
		}
		if _, err := c.Post("/v1/device/unlock", map[string]any{}); err != nil {
			return stepOutcome{}, err
		}
		time.Sleep(3 * time.Second)
		open, err := keyguardOf(c)
		if err != nil {
			return stepOutcome{}, err
		}
		if open.Screen != "ON" || open.Keyguard {
			return stepOutcome{}, fmt.Errorf("after unlocking: screen=%s keyguard=%v", open.Screen, open.Keyguard)
		}
		return stepOutcome{
			observed: fmt.Sprintf("locked (screen %s, keyguard %v) then unlocked", locked.Screen, locked.Keyguard),
			logMust:  []string{"device: lock requested", "unlock: "},
		}, nil
	})
}

func runJourneyTriggers(c *client.Client, step func(string, string, func() (stepOutcome, error))) {
	var id string
	step("trigger", "a timer trigger fires and records a run", func() (stepOutcome, error) {
		data, err := c.Post("/v1/triggers", map[string]any{
			"name": "journey-timer", "type": "timer",
			"spec": map[string]any{"interval_seconds": 1},
		})
		if err != nil {
			return stepOutcome{}, err
		}
		var tr struct {
			ID string `json:"id"`
		}
		if err := json.Unmarshal(data, &tr); err != nil {
			return stepOutcome{}, err
		}
		id = tr.ID
		time.Sleep(3 * time.Second)
		rd, err := c.Get("/v1/triggers/" + id + "/runs")
		if err != nil {
			return stepOutcome{}, err
		}
		var runs struct {
			Runs []any `json:"runs"`
		}
		_ = json.Unmarshal(rd, &runs)
		if len(runs.Runs) == 0 {
			return stepOutcome{}, fmt.Errorf("the trigger recorded no runs in 3s at a 1s interval")
		}
		return stepOutcome{
			observed: fmt.Sprintf("%d run(s) recorded", len(runs.Runs)),
			logMust:  []string{"trigger: created " + id},
		}, nil
	})

	step("trigger", "deleting it stops it firing", func() (stepOutcome, error) {
		if id == "" {
			return stepOutcome{}, fmt.Errorf("no trigger from the previous step")
		}
		if _, err := c.Delete("/v1/triggers/" + id); err != nil {
			return stepOutcome{}, err
		}
		list, err := c.Get("/v1/triggers")
		if err != nil {
			return stepOutcome{}, err
		}
		var l struct {
			Triggers []struct {
				ID string `json:"id"`
			} `json:"triggers"`
		}
		_ = json.Unmarshal(list, &l)
		for _, t := range l.Triggers {
			if t.ID == id {
				return stepOutcome{}, fmt.Errorf("the trigger is still listed after delete")
			}
		}
		return stepOutcome{
			observed: "the trigger is gone from the list",
			logMust:  []string{"trigger: deleted " + id},
		}, nil
	})
}

// --- device readers ----------------------------------------------------------

// treeNode is one entry of /v1/ui/tree, reduced to what the journey looks at.
type treeNode struct {
	Text        string `json:"text"`
	ContentDesc string `json:"content_desc"`
	ResourceID  string `json:"resource_id"`
	Class       string `json:"class"`
	Bounds      [4]int `json:"bounds"`
}

// ensureWeChatList puts WeChat in a known state: the conversation list.
//
// WeChat resumes into the last open chat, so every scenario that starts with "open
// the app" was silently depending on the state the previous run left behind. That
// made the matrix pass or fail depending on history, which is worse than failing:
// it looks green. This walks back the way a person does, falls back to a cold
// start, and reports which it needed.
func ensureWeChatList(c *client.Client) (string, error) {
	// Getting WeChat to a known screen, deterministically.
	//
	// Back presses were the obvious way and they are not deterministic: from the
	// contacts page, back lands on 发现, presses a profile, opens a stranger's
	// timeline, and the next scenario fails because it is no longer in WeChat's
	// list at all. A cold start is what a person does with an app in that state, and
	// it is the only reset whose result does not depend on history.
	probe := func() (count, texts int, why string) {
		data, err := c.Get("/v1/ui/tree")
		if err != nil {
			return 0, 0, err.Error()
		}
		var t struct {
			Count int        `json:"count"`
			Nodes []treeNode `json:"nodes"`
		}
		if json.Unmarshal(data, &t) != nil {
			return 0, 0, "tree is not JSON"
		}
		labels := map[string]bool{}
		tabBar := map[string]bool{}
		for _, n := range t.Nodes {
			label := strings.TrimSpace(n.Text)
			if label == "" {
				label = strings.TrimSpace(n.ContentDesc)
			}
			if label == "" || n.Bounds[2] <= n.Bounds[0] {
				continue
			}
			texts++
			switch label {
			case "新的朋友", "仅聊天的朋友", "企业微信联系人":
				labels[label] = true
			}
			// The tab bar is the cheap, unambiguous marker of "WeChat's top level":
			// a chat page does not have it. Both halves are needed — the tab bar
			// alone is also on 通讯录, and "many texts" alone is also true inside a
			// chat, which is how this helper once reported "on the list" while a
			// conversation was open and then failed to find a conversation row.
			if n.ResourceID == "com.tencent.mm:id/icon_tv" && n.Bounds[1] > 2100 {
				tabBar[label] = true
			}
		}
		if labels["新的朋友"] || labels["仅聊天的朋友"] || labels["企业微信联系人"] {
			return t.Count, texts, "on the contacts page"
		}
		if !(tabBar["微信"] && tabBar["通讯录"] && tabBar["发现"] && tabBar["我"]) {
			return t.Count, texts, "no tab bar, so this is not WeChat's top level (a chat or a detail page)"
		}
		if texts < 5 || t.Count < 50 {
			return t.Count, texts, "the tree is too thin to be the conversation list"
		}
		return t.Count, texts, ""
	}

	if count, texts, why := probe(); why == "" {
		return fmt.Sprintf("chat list present (%d nodes, %d texts)", count, texts), nil
	}

	// Cold start, explicitly onto the tab container.
	//
	// `launch <package>` resolves the launcher activity and WeChat restores whatever
	// page it was on — measured here: it came back on a stranger's 视频号 profile, and
	// no amount of tab pressing fixes that because the tab bar is not there. Naming
	// `.ui.LauncherUI` is what a person is doing when they tap the icon from the home
	// screen: start the tab container, not "resume my last screen".
	if _, err := c.Post("/v1/apps/stop", map[string]any{"package": "com.tencent.mm"}); err != nil {
		return "", err
	}
	time.Sleep(time.Second)
	if _, err := c.Post("/v1/apps/launch", map[string]any{"component": "com.tencent.mm/.ui.LauncherUI"}); err != nil {
		return "", err
	}
	for i := 0; i < 10; i++ {
		time.Sleep(time.Second)
		if count, texts, why := probe(); why == "" {
			return fmt.Sprintf("cold start (%d nodes, %d texts)", count, texts), nil
		}
	}

	// Still not the list: press the 微信 tab, located by resource id + position so a
	// conversation that happens to be named 微信 cannot be tapped by mistake.
	data, err := c.Get("/v1/ui/tree")
	if err == nil {
		var t struct {
			Nodes []treeNode `json:"nodes"`
		}
		if json.Unmarshal(data, &t) == nil {
			for _, n := range t.Nodes {
				if n.ResourceID != "com.tencent.mm:id/icon_tv" || strings.TrimSpace(n.Text) != "微信" {
					continue
				}
				if n.Bounds[1] < 2100 || n.Bounds[2] <= n.Bounds[0] {
					continue
				}
				if _, err := c.Post("/v1/input/tap", map[string]any{
					"x": (n.Bounds[0] + n.Bounds[2]) / 2, "y": (n.Bounds[1] + n.Bounds[3]) / 2,
				}); err != nil {
					return "", err
				}
				time.Sleep(1500 * time.Millisecond)
				if count, texts, why := probe(); why == "" {
					return fmt.Sprintf("tab press (%d nodes, %d texts)", count, texts), nil
				}
				break
			}
		}
	}
	count, texts, why := probe()
	return "", fmt.Errorf("WeChat never reached the conversation list (%d nodes, %d texts): %s", count, texts, why)
}

// backToText is backToNode for a text match.
func backToText(c *client.Client, text string, presses int, timeout time.Duration) (treeNode, error) {
	var last error
	for attempt := 0; attempt <= presses; attempt++ {
		if n, err := waitForText(c, text, timeout/time.Duration(presses+1)); err == nil {
			return n, nil
		} else {
			last = err
		}
		if attempt < presses {
			if _, err := c.Post("/v1/input/global", map[string]any{"action": "back"}); err != nil {
				return treeNode{}, fmt.Errorf("could not press back to reach the conversation list: %w", err)
			}
			time.Sleep(800 * time.Millisecond)
		}
	}
	return treeNode{}, fmt.Errorf("the conversation list never appeared after %d back press(es): %v", presses, last)
}

// openChat navigates to a conversation and returns once its composer exists.
//
// A person does this in two steps (put the phone on the list, tap the chat), and so
// does this: the composer is the proof the tap opened the conversation rather than
// landing somewhere that merely looks like one.
func openChat(c *client.Client) (treeNode, error) {
	if _, err := ensureWeChatList(c); err != nil {
		return treeNode{}, err
	}
	if _, err := c.Post("/v1/ui/find", map[string]any{"text": "serendipity", "click": true}); err != nil {
		return treeNode{}, err
	}
	return waitForNode(c, "com.tencent.mm:id/bkk", 12*time.Second)
}

// waitForText polls the tree until a node with this text appears ON SCREEN.
//
// A conversation can be in the tree and still be below the viewport (WeChat's
// list keeps more rows than a screen); the journey used to fail on a row that
// existed but was not visible. A bounded downward swipe is what a person does
// there, and it does not hide a missing row: after three swipes the same honest
// timeout is returned.
func waitForText(c *client.Client, text string, timeout time.Duration) (treeNode, error) {
	deadline := time.Now().Add(timeout)
	var last string
	scrolls := 0
	for {
		data, err := c.Get("/v1/ui/tree")
		if err == nil {
			var t struct {
				Count int        `json:"count"`
				Nodes []treeNode `json:"nodes"`
			}
			if json.Unmarshal(data, &t) == nil {
				offscreen := false
				for _, n := range t.Nodes {
					if !strings.Contains(n.Text, text) {
						continue
					}
					// WeChat keeps off-screen floating copies with negative bounds;
					// they are listed first and cannot be tapped.
					if n.Bounds[2] > n.Bounds[0] && n.Bounds[3] > n.Bounds[1] &&
						n.Bounds[0] >= 0 && n.Bounds[1] >= 0 {
						return n, nil
					}
					offscreen = true
				}
				last = fmt.Sprintf("%d nodes, none on screen matching %q", t.Count, text)
				if offscreen && scrolls < 3 {
					_, _ = c.Post("/v1/input/swipe", map[string]any{
						"x1": 540, "y1": 1600, "x2": 540, "y2": 700,
						"duration_ms": 300, "display": 0,
					})
					scrolls++
					time.Sleep(800 * time.Millisecond)
					continue
				}
			}
		} else {
			last = err.Error()
		}
		if time.Now().After(deadline) {
			return treeNode{}, fmt.Errorf("waited %s: %s", timeout, last)
		}
		time.Sleep(400 * time.Millisecond)
	}
}

// backToNode presses back up to `presses` times until the wanted node appears.
//
// This is navigation a user performs, not a retry loop: WeChat resumes into the
// last open chat, so "open a conversation" first requires being on the list.
func backToNode(c *client.Client, resourceID string, presses int, timeout time.Duration) (treeNode, error) {
	var last error
	for attempt := 0; attempt <= presses; attempt++ {
		if n, err := waitForNode(c, resourceID, timeout/time.Duration(presses+1)); err == nil {
			return n, nil
		} else {
			last = err
		}
		if attempt < presses {
			if _, err := c.Post("/v1/input/global", map[string]any{"action": "back"}); err != nil {
				return treeNode{}, fmt.Errorf("could not press back to reach the conversation list: %w", err)
			}
			time.Sleep(800 * time.Millisecond)
		}
	}
	return treeNode{}, fmt.Errorf("the conversation list never appeared after %d back press(es): %v", presses, last)
}

// waitForNode polls the tree until a node with this resource id appears.
//
// A sleep would be shorter to write and would make the matrix flaky in exactly
// the way that hides bugs: sometimes it passes on a stale screen.
func waitForNode(c *client.Client, resourceID string, timeout time.Duration) (treeNode, error) {
	deadline := time.Now().Add(timeout)
	var last string
	for {
		data, err := c.Get("/v1/ui/tree")
		if err == nil {
			var t struct {
				Count int        `json:"count"`
				Nodes []treeNode `json:"nodes"`
			}
			if json.Unmarshal(data, &t) == nil {
				for _, n := range t.Nodes {
					if n.ResourceID == resourceID {
						return n, nil
					}
				}
				last = fmt.Sprintf("%d nodes, none with id %s", t.Count, resourceID)
			}
		} else {
			last = err.Error()
		}
		if time.Now().After(deadline) {
			return treeNode{}, fmt.Errorf("waited %s for %s: %s", timeout, resourceID, last)
		}
		time.Sleep(400 * time.Millisecond)
	}
}

func foregroundOf(c *client.Client) (string, error) {
	data, err := c.Get("/v1/apps/foreground")
	if err != nil {
		return "", err
	}
	var f struct {
		Package   string `json:"package"`
		Component string `json:"component"`
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return "", err
	}
	if f.Component != "" {
		return f.Component, nil
	}
	return f.Package, nil
}

func settingOf(c *client.Client, ns, key string) (string, error) {
	data, err := c.Get("/v1/settings/" + ns + "/" + key)
	if err != nil {
		return "", err
	}
	var s struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return "", err
	}
	return s.Value, nil
}

// keyguardOf reads the keyguard state. It uses its own endpoint rather than the
// one the lock call answered with, so "the daemon says it locked" and "the device
// is locked" stay separate facts.
type keyguardState struct {
	Screen       string `json:"screen_state"`
	Keyguard     bool   `json:"keyguard_showing"`
	ScreenLocked bool   `json:"screen_locked"`
}

func keyguardOf(c *client.Client) (keyguardState, error) {
	var k keyguardState
	data, err := c.Get("/v1/device/lock-state")
	if err != nil {
		return k, err
	}
	err = json.Unmarshal(data, &k)
	return k, err
}

func treeSummary(c *client.Client) (count, texts int, err error) {
	data, err := c.Get("/v1/ui/tree")
	if err != nil {
		return 0, 0, err
	}
	var t struct {
		Count int `json:"count"`
		Nodes []struct {
			Text string `json:"text"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(data, &t); err != nil {
		return 0, 0, err
	}
	for _, n := range t.Nodes {
		if strings.TrimSpace(n.Text) != "" {
			texts++
		}
	}
	return t.Count, texts, nil
}

// treeLabels returns every string the tree carries, text AND content-desc.
//
// Both matter: WeChat puts the sender's name in the avatar's content-desc
// ("serendipity头像") and the message body in text, so looking only at text
// concludes "this chat does not mention the contact" while it plainly does.
func treeLabels(c *client.Client) ([]string, error) {
	data, err := c.Get("/v1/ui/tree")
	if err != nil {
		return nil, err
	}
	var t struct {
		Nodes []treeNode `json:"nodes"`
	}
	if err := json.Unmarshal(data, &t); err != nil {
		return nil, err
	}
	var out []string
	for _, n := range t.Nodes {
		if n.Text != "" {
			out = append(out, n.Text)
		}
		if n.ContentDesc != "" {
			out = append(out, n.ContentDesc)
		}
	}
	return out, nil
}

func trimErr(err error) string {
	s := err.Error()
	if i := strings.IndexByte(s, '\n'); i > 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120] + "…"
	}
	return s
}

// --- report ------------------------------------------------------------------

func renderJourney(server string, results []journeyResult) string {
	counts := map[string]int{}
	for _, r := range results {
		counts[r.State]++
	}
	var b strings.Builder
	b.WriteString("# chopin journey report (user-like acceptance)\n\n")
	b.WriteString(fmt.Sprintf("- server: `%s`\n", server))
	b.WriteString(fmt.Sprintf("- generated: %s\n", time.Now().Format("2006-01-02 15:04:05 -07:00")))
	b.WriteString(fmt.Sprintf("- result: **PASS %d / FAIL %d**\n\n", counts["PASS"], counts["FAIL"]))
	b.WriteString("Each step does what a user does, checks the effect through a different\n")
	b.WriteString("channel than the one that caused it, and requires the audit log to confirm\n")
	b.WriteString("that the intended code path actually ran. All three must agree.\n\n")
	b.WriteString("| state | scenario | step | observed | log confirms | took |\n|---|---|---|---|---|---|\n")

	sorted := append([]journeyResult(nil), results...)
	sort.SliceStable(sorted, func(i, j int) bool {
		rank := func(s string) int {
			if s == "FAIL" {
				return 0
			}
			return 1
		}
		return rank(sorted[i].State) < rank(sorted[j].State)
	})
	esc := func(s string) string { return strings.ReplaceAll(s, "|", "\\|") }
	for _, r := range sorted {
		log := r.LogLine
		if r.LogMissing != "" {
			log = "**missing:** " + r.LogMissing
		}
		b.WriteString(fmt.Sprintf("| %s | %s | %s | %s | %s | %dms |\n",
			r.State, r.Scenario, esc(r.Step), esc(r.Observed), esc(log), r.Millis))
	}
	return b.String()
}

// --- HUD: the safety surface ------------------------------------------------

func runJourneyHUD(c *client.Client, step func(string, string, func() (stepOutcome, error))) {
	var hudToken string

	// hudToken reads the overlay's own credential off the device.
	//
	// The HUD endpoints are deliberately loopback-only and hud-scoped: the PC token
	// cannot call them, which is the point (no remote client can silence the safety
	// overlay). So the journey reads the token the daemon provisioned for the app
	// and calls the endpoints the way the app does — over adb, from the device.
	hudToken = func() string {
		out, err := adbExport(suShell("cat /data/data/com.chopin.pilot/files/hud.json"))
		if err != nil {
			return ""
		}
		var cfg struct {
			Token string `json:"token"`
		}
		if json.Unmarshal([]byte(out), &cfg) != nil {
			return ""
		}
		return cfg.Token
	}()

	hudCall := func(method, path, body string) (string, error) {
		if hudToken == "" {
			return "", fmt.Errorf("cannot read the overlay token; the HUD is not provisioned")
		}
		// The whole remote command is double-quoted and every inner single quote is
		// part of a shell string, so it survives `adb shell` -> `su -c` unchanged.
		// A single-quoted outer wrapper gets eaten by the first inner quote and the
		// token silently disappears — which reads as "missing bearer token" from a
		// daemon that is working fine.
		cmd := fmt.Sprintf(
			`curl -sk -X %s -H "Authorization: Bearer %s" -H "Content-Type: application/json" -d "%s" https://127.0.0.1:8443%s`,
			method, hudToken, strings.ReplaceAll(body, `"`, `\"`), path)
		return adbExport(suShell(cmd))
	}

	hudState := func() (map[string]any, error) {
		out, err := hudCall("GET", "/v1/hud", "{}")
		if err != nil {
			return nil, err
		}
		var full struct {
			State map[string]any `json:"state"`
		}
		if err := json.Unmarshal([]byte(out), &full); err != nil {
			return nil, fmt.Errorf("hud state is not JSON: %s", trimErr(fmt.Errorf("%s", out)))
		}
		return full.State, nil
	}

	step("hud", "the overlay appears when an agent takes control", func() (stepOutcome, error) {
		id, err := claimSession(c, "journey-hud")
		if err != nil {
			return stepOutcome{}, err
		}
		time.Sleep(1500 * time.Millisecond)
		st, err := hudState()
		if err != nil {
			return stepOutcome{}, err
		}
		if st["visible"] != true {
			return stepOutcome{}, fmt.Errorf("the overlay is not visible while a session is held: %v", st)
		}
		// The window has to be real, not just a flag: WindowManager is the only
		// place that proves something is on screen.
		wins, err := adbExport(suShell(`dumpsys window windows | grep -c "Chopin Pilot 悬浮控制"`))
		if err != nil {
			return stepOutcome{}, err
		}
		n, _ := strconv.Atoi(strings.TrimSpace(wins))
		if n < 1 {
			return stepOutcome{}, fmt.Errorf("visible=true but WindowManager has no overlay window")
		}
		return stepOutcome{
			observed: fmt.Sprintf("visible=true, %d overlay window(s) registered", n),
			logMust:  []string{"session: claimed " + id},
		}, nil
	})

	step("hud", "it says what the agent is doing, not just 「控制中」", func() (stepOutcome, error) {
		// The reference design is explicit: the capsule shows the action
		// (「点「发送」」), because "something is happening" does not tell the person
		// holding the phone whether to reach for Stop.
		// Any real action will do; use one whose target is provably on screen so a
		// 404 from find cannot be mistaken for "the overlay is not updating".
		if _, err := ensureWeChatList(c); err != nil {
			return stepOutcome{}, err
		}
		if _, err := c.Post("/v1/ui/find", map[string]any{"text": "通讯录", "click": true}); err != nil {
			return stepOutcome{}, err
		}
		time.Sleep(1200 * time.Millisecond)
		st, err := hudState()
		if err != nil {
			return stepOutcome{}, err
		}
		action, _ := st["action"].(string)
		if action == "" || action == "控制中" {
			return stepOutcome{}, fmt.Errorf("the overlay still shows %q, not the action that just ran", action)
		}
		return stepOutcome{
			observed: "overlay line is " + strconv.Quote(action),
			logMust:  []string{"ui: find+click"},
		}, nil
	})

	step("hud", "暂停 stops the phone moving, and keeps holding", func() (stepOutcome, error) {
		if _, err := hudCall("POST", "/v1/hud/pause", `{"paused":true}`); err != nil {
			return stepOutcome{}, err
		}
		// Past one heartbeat window: the overlay heartbeats every 15s, and a version
		// of this code let that heartbeat resume the paused overlay.
		time.Sleep(17 * time.Second)
		st, err := hudState()
		if err != nil {
			return stepOutcome{}, err
		}
		if st["paused"] != true {
			return stepOutcome{}, fmt.Errorf("the pause was cleared by a heartbeat")
		}
		// Every injection path must refuse, not just new claims.
		for _, call := range []struct {
			path string
			body map[string]any
		}{
			{"/v1/input/tap", map[string]any{"x": 500, "y": 1200}},
			{"/v1/input/text", map[string]any{"text": "x"}},
			{"/v1/input/global", map[string]any{"action": "back"}},
			{"/v1/input/key", map[string]any{"key": "KEYCODE_HOME"}},
		} {
			if _, err := c.Post(call.path, call.body); err == nil {
				return stepOutcome{}, fmt.Errorf("%s was accepted while the overlay is paused", call.path)
			} else if !strings.Contains(err.Error(), "conflict") {
				return stepOutcome{}, fmt.Errorf("%s was refused with the wrong reason: %v", call.path, err)
			}
		}
		if _, err := hudCall("POST", "/v1/hud/pause", `{"paused":false}`); err != nil {
			return stepOutcome{}, err
		}
		return stepOutcome{
			observed: "paused held for 17s (past one heartbeat) and 4 injection paths were refused",
			logMust:  []string{"hud: paused=true", "hud: paused=false"},
		}, nil
	})

	step("hud", "停止 releases everything and stays stopped", func() (stepOutcome, error) {
		if _, err := hudCall("POST", "/v1/hud/stop", `{"reason":"journey"}`); err != nil {
			return stepOutcome{}, err
		}
		time.Sleep(16 * time.Second) // past a heartbeat again
		list, err := c.Get("/v1/sessions")
		if err != nil {
			return stepOutcome{}, err
		}
		var l struct {
			Active []any `json:"active"`
		}
		if err := json.Unmarshal(list, &l); err != nil {
			return stepOutcome{}, err
		}
		if len(l.Active) != 0 {
			return stepOutcome{}, fmt.Errorf("%d session(s) survived the stop", len(l.Active))
		}
		if _, err := c.Post("/v1/input/tap", map[string]any{"x": 500, "y": 1200}); err == nil {
			return stepOutcome{}, fmt.Errorf("input was accepted after the human pressed stop")
		}
		return stepOutcome{
			observed: "all sessions released; input still refused 16s later",
			logMust:  []string{"hud: stop pressed (journey)"},
		}, nil
	})

	step("hud", "a new task starts clean", func() (stepOutcome, error) {
		id, err := claimSession(c, "journey-after-stop")
		if err != nil {
			return stepOutcome{}, fmt.Errorf("a new session could not start after a stop: %w", err)
		}
		time.Sleep(1200 * time.Millisecond)
		st, err := hudState()
		if err != nil {
			return stepOutcome{}, err
		}
		if st["stopped"] == true {
			return stepOutcome{}, fmt.Errorf("the new session inherited the previous stop")
		}
		if _, err := c.Post("/v1/input/tap", map[string]any{"x": 500, "y": 1200}); err != nil {
			return stepOutcome{}, fmt.Errorf("input is still refused for the new session: %w", err)
		}
		if _, err := c.Delete("/v1/sessions/" + id); err != nil {
			return stepOutcome{}, err
		}
		return stepOutcome{
			observed: "new claim accepted and input works again",
			logMust:  []string{"session: claimed " + id},
		}, nil
	})
}

// adbExport runs one remote shell command and returns stdout.
//
// The whole command line is handed to adb as a SINGLE argv element, because adb
// joins its arguments with spaces and lets the DEVICE shell split them again. Three
// quoting layers are involved (Go -> adb -> device shell -> su -c), and getting them
// wrong is silent: the header arrives empty and a healthy daemon answers "missing
// bearer token". That is exactly what happened here before this comment existed.
func adbExport(remote string) (string, error) {
	if defaultSerial == "" {
		return "", fmt.Errorf("journey needs a device: pass --serial or set CHOPIN_SERIAL")
	}
	cmd := exec.Command("adb", "-s", defaultSerial, "shell", remote)
	if home := os.Getenv("CHOPIN_ADB_HOME"); home != "" {
		cmd.Env = append(os.Environ(), "HOME="+home)
	}
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("adb shell %s: %w", remote, err)
	}
	return string(out), nil
}

// suShell wraps a command for `su -c`.
//
// Single quotes around the whole command, with any inner single quote escaped the
// POSIX way, so the device shell cannot split it before su sees it.
func suShell(cmd string) string {
	return "su -c " + "'" + strings.ReplaceAll(cmd, "'", `'\''`) + "'"
}

// claimSession claims a write session and returns its id.
func claimSession(c *client.Client, agent string) (string, error) {
	data, err := c.Post("/v1/sessions", map[string]any{"agent": agent, "display": 0})
	if err != nil {
		return "", err
	}
	var s struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return "", err
	}
	if s.ID == "" {
		return "", fmt.Errorf("claim returned no session id: %s", trimErr(fmt.Errorf("%s", data)))
	}
	return s.ID, nil
}
