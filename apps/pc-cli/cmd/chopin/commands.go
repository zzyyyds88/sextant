package main

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
)

// The command table.
//
// One place decides what the CLI can do, so `chopin help` cannot drift from what
// actually runs and the acceptance suite can walk the same table. Handlers may
// still need bespoke code (pairing, screenshot transfer), but every plain
// API pass-through is declared here.

// command is one CLI verb.
type command struct {
	name    string
	group   string
	usage   string
	summary string
	// method + path make a plain API call; body builds its payload.
	method string
	path   func(a *args) (string, error)
	body   func(a *args) (any, error)
	// run overrides the generic call for commands with local work to do.
	run func(a *args) error
}

func (c command) String() string { return c.name }

// commands is the single source for dispatch and help.
var commands []command

func init() {
	commands = []command{
		// Pairing and orientation.
		{name: "pair", group: "Pairing", usage: "pair [--serial S] [--agent NAME]",
			summary: "mint a code over adb and store the API token", run: func(a *args) error {
				return cmdPair([]string{"--serial", a.optStr("serial", defaultSerial), "--agent", a.optStr("agent", "pc")})
			}},
		{name: "init", group: "Pairing", usage: "init [--serial S] [--sim] [--keep-animation] [--restore]",
			summary: "pair, store credentials and prepare the device in one command", run: func(a *args) error {
				argv := []string{"--serial", a.optStr("serial", defaultSerial), "--agent", a.optStr("agent", "pc")}
				if a.has("sim") {
					argv = append(argv, "--sim")
				}
				if a.has("keep-animation") {
					argv = append(argv, "--keep-animation")
				}
				if a.has("restore") {
					argv = append(argv, "--restore")
				}
				return cmdInit(argv)
			}},
		{name: "health", group: "Read", usage: "health", summary: "liveness and version (no token needed)",
			run: func(a *args) error { return cmdHealth() }},
		{name: "status", group: "Read", usage: "status", summary: "the published status object",
			method: "GET", path: fixed("/v1/status")},
		{name: "device", group: "Read", usage: "device", summary: "device info",
			method: "GET", path: fixed("/v1/device")},
		{name: "lock-state", group: "Read", usage: "lock-state", summary: "keyguard, screen and credential presence",
			method: "GET", path: fixed("/v1/device/lock-state")},
		{name: "watchdog", group: "Read", usage: "watchdog", summary: "daemon, helper and session health",
			method: "GET", path: fixed("/v1/watchdog")},
		{name: "metrics", group: "Read", usage: "metrics", summary: "in-memory request, UI and helper counters",
			method: "GET", path: fixed("/v1/metrics")},
		{name: "doc", group: "Read", usage: "doc [-o file]", summary: "OpenAPI document",
			run: func(a *args) error { return cmdDoc(a) }},
		{name: "doctor", group: "Read", usage: "doctor", summary: "check pairing, daemon, a11y, background, helper and page",
			run: func(a *args) error { return cmdDoctor(a) }},

		// Lock and screen.
		{name: "keep-awake", group: "Act", usage: "keep-awake on|off [--timeout-ms N]",
			summary: "hold or release a partial wake lock", run: func(a *args) error { return cmdKeepAwake(a) }},
		{name: "wake", group: "Act", usage: "wake", summary: "wake the screen",
			method: "POST", path: fixed("/v1/device/wake"), body: emptyBody},
		{name: "lock", group: "Act", usage: "lock", summary: "lock the screen",
			method: "POST", path: fixed("/v1/device/lock"), body: emptyBody},
		{name: "unlock", group: "Act", usage: "unlock", summary: "unlock with the stored pattern",
			method: "POST", path: fixed("/v1/device/unlock"), body: emptyBody},
		{name: "sim-unlock", group: "Act", usage: "sim-unlock", summary: "enter the SIM PIN if the device asks for one",
			method: "POST", path: fixed("/v1/device/sim-unlock"), body: emptyBody},
		{name: "rotation", group: "Act", usage: "rotation [0|90|180|270|auto]",
			summary: "read or set the rotation", run: func(a *args) error { return cmdRotation(a) }},
		{name: "screen", group: "Act", usage: "screen [--brightness N] [--timeout-ms N]",
			summary: "set brightness and screen timeout", run: func(a *args) error { return cmdScreen(a) }},
		{name: "airplane", group: "Act", usage: "airplane on|off", summary: "airplane mode",
			run: func(a *args) error { return cmdRadio(a, "/v1/device/airplane") }},
		{name: "wifi", group: "Act", usage: "wifi on|off", summary: "Wi-Fi on/off",
			run: func(a *args) error { return cmdRadio(a, "/v1/device/wifi") }},

		// Input.
		{name: "tap", group: "Input", usage: "tap X Y [--display N] [--seed S]", summary: "tap at x,y",
			run: func(a *args) error { return cmdTapArgs(a) }},
		{name: "swipe", group: "Input", usage: "swipe X1 Y1 X2 Y2 [duration_ms] [--display N]",
			summary: "swipe between two points", run: func(a *args) error { return cmdSwipeArgs(a) }},
		{name: "longpress", group: "Input", usage: "longpress X Y [duration_ms] [--display N]",
			summary: "long press at x,y", run: func(a *args) error { return cmdLongPressArgs(a) }},
		{name: "key", group: "Input", usage: "key KEYCODE_HOME [--display N]", summary: "send a key event",
			run: func(a *args) error { return cmdKeyArgs(a) }},
		{name: "global", group: "Input", usage: "global back|home|recents|notifications|quick_settings|power_dialog|lock_screen|screenshot",
			summary: "send a global action", run: func(a *args) error { return cmdGlobal(a) }},
		{name: "text", group: "Input", usage: "text TEXT [--ime] [--clear]",
			summary: "type text (accessibility first, IME fallback)", run: func(a *args) error { return cmdText(a) }},

		{name: "audio", group: "Device", usage: "audio [state|mic|route|mode|listen|say|speak]",
			summary: "the device's ears and mouth: mic, speaker, capture level, PCM playback",
			run:     func(a *args) error { return cmdAudio(a) }},

		{name: "settings", group: "Device", usage: "settings [NAME VALUE]",
			summary: "list the daily settings, or set one (goes through the system API and reads back)",
			run:     func(a *args) error { return cmdSettings(a) }},

		// Calls.
		// Perception.
		{name: "tree", group: "Perceive", usage: "tree [--summary] [--fields LIST] [--max-items N] [--viewport]", summary: "accessibility node tree (JSON by default)",
			run: func(a *args) error { return cmdTree(a) }},
		{name: "find", group: "Perceive", usage: "find TEXT [--click] [--timeout-ms N]",
			summary: "locate a node (tree, then OCR)", run: func(a *args) error { return cmdFind(a) }},
		{name: "ocr", group: "Perceive", usage: "ocr [--region L T R B]", summary: "ROM OCR on the screen",
			method: "POST", path: fixed("/v1/ui/ocr"), body: ocrBody},
		{name: "panel", group: "Perceive", usage: "panel", summary: "OCR text, countdown and action button",
			method: "POST", path: fixed("/v1/ui/panel_read"), body: emptyBody},
		{name: "pixels", group: "Perceive", usage: "pixels [--region L T R B]", summary: "brightness and colour statistics",
			method: "POST", path: fixed("/v1/ui/pixel_stats"), body: regionBody},
		{name: "color", group: "Perceive", usage: "color R G B [--tolerance N] [--region L T R B]",
			summary: "how much of the screen matches a colour", run: func(a *args) error { return cmdColor(a) }},
		{name: "shot", group: "Perceive", usage: "shot [-o out.png]", summary: "capture the screen to a local file",
			run: func(a *args) error { return cmdShot(os.Args[2:]) }},
		{name: "frame", group: "Perceive", usage: "frame [-o frame.jpg] [--quality N] [--max-size N] [--cached]",
			summary: "one request, image/jpeg bytes", run: func(a *args) error { return cmdFrame(a) }},
		{name: "observe", group: "Perceive", usage: "observe [--tree] [--ocr] [--pixels] [--frame]",
			summary: "one capture, several observations", run: func(a *args) error { return cmdObserve(a) }},
		{name: "page", group: "Perceive", usage: "page", summary: "foreground, windows, keyboard and page fingerprint",
			method: "GET", path: fixed("/v1/ui/page")},
		{name: "diff", group: "Perceive", usage: "diff --before F [--after F]",
			summary: "structural difference between two observed trees", run: func(a *args) error { return cmdDiff(a) }},
		{name: "wait", group: "Perceive", usage: "wait TEXT|--id RID [--absent] [--timeout-ms N]",
			summary: "wait for a node to appear or disappear", run: func(a *args) error { return cmdWait(a) }},
		{name: "chain", group: "Perceive", usage: "chain '<steps-json>'", summary: "run a short step sequence",
			run: func(a *args) error { return cmdChain(a) }},
		{name: "wait-result", group: "Perceive", usage: "wait-result TEXT|--id RID [--absent] [--screenshot]",
			summary: "wait for a condition and return the frame", run: func(a *args) error { return cmdWaitResult(a) }},
		{name: "act", group: "Perceive", usage: "act --text T|--id RID|--node N [--action click|select|longpress|set_text] [--seed S]",
			summary: "verified action with optional expectation", run: func(a *args) error { return cmdAct(a) }},
		{name: "list", group: "Perceive", usage: "list --text-contains T|--rid R|--class C [--max N] [--scroll N]",
			summary: "extract repeated items from the current page", run: func(a *args) error { return cmdList(a) }},
		{name: "options", group: "Perceive", usage: "options [--group TITLE]",
			summary: "read option groups and selected state", run: func(a *args) error { return cmdOptions(a) }},
		{name: "readback", group: "Perceive", usage: "readback TEXT|--rid RID [--scope tree|ocr]",
			summary: "confirm a result by reading the tree or OCR", run: func(a *args) error { return cmdReadback(a) }},
		{name: "wait-stable", group: "Perceive", usage: "wait-stable [--timeout-ms N] [--stable-ms N] [--scope tree|frame]",
			summary: "wait until the tree or frame stops changing", run: func(a *args) error { return cmdWaitStable(a) }},
		{name: "challenge", group: "Perceive", usage: "challenge",
			summary: "classify the current verification wall", run: func(a *args) error { return cmdChallenge(a) }},
		{name: "challenge-solve", group: "Perceive", usage: "challenge-solve [--dx N] [--seed S]",
			summary: "attempt a slider wall; a code wall is refused", run: func(a *args) error { return cmdChallengeSolve(a) }},
		{name: "risk", group: "Read", usage: "risk [--package P]",
			summary: "packages with live verification-wall records", run: func(a *args) error { return cmdRisk(a) }},
		{name: "handover", group: "Act", usage: "handover --package P [--reason TEXT] | --return [--relaunch] | --status",
			summary: "move an app to the user's screen for manual verification", run: func(a *args) error { return cmdHandover(a) }},
		{name: "map", group: "Perceive", usage: "map build|list|show|ops|where|nav|bench ...",
			summary: "compiled app map: build, inspect, navigate, benchmark", run: func(a *args) error { return cmdMap(a) }},

		// Acceptance.
		{name: "journey", group: "Acceptance", usage: "journey [--out DIR] [--serial S]",
			summary: "user-like acceptance: act, observe independently, confirm in the log",
			run: func(a *args) error {
				if s := a.optStr("serial", ""); s != "" {
					defaultSerial = s
				}
				return cmdJourney(a)
			}},

		// Sessions.
		{name: "sessions", group: "Sessions", usage: "sessions", summary: "list write sessions",
			method: "GET", path: fixed("/v1/sessions")},
		{name: "claim", group: "Sessions", usage: "claim [AGENT] [--display N]", summary: "claim a write session",
			run: func(a *args) error { return cmdClaim(a) }},
		{name: "release", group: "Sessions", usage: "release ID", summary: "release a write session",
			run: func(a *args) error { return cmdRelease(a) }},
		{name: "heartbeat", group: "Sessions", usage: "heartbeat", summary: "renew the local write session",
			run: func(a *args) error { return cmdHeartbeat(a) }},

		// Apps.
		{name: "apps", group: "Apps", usage: "apps", summary: "installed third-party packages",
			method: "GET", path: fixed("/v1/apps")},
		{name: "foreground", group: "Apps", usage: "foreground", summary: "foreground package",
			method: "GET", path: fixed("/v1/apps/foreground")},
		{name: "launch", group: "Apps", usage: "launch PKG|COMPONENT [--clear-task]", summary: "launch an app",
			run: func(a *args) error { return cmdLaunch(a) }},
		{name: "stop", group: "Apps", usage: "stop PACKAGE", summary: "force-stop an app",
			run: func(a *args) error { return cmdPackagePost(a, "/v1/apps/stop") }},
		{name: "open", group: "Apps", usage: "open URI", summary: "open a URI",
			run: func(a *args) error { return cmdOpen(a) }},

		// Files and settings.
		{name: "ls", group: "Files", usage: "ls PATH", summary: "list a directory",
			run: func(a *args) error { return cmdLS(a) }},
		{name: "get", group: "Files", usage: "get PATH [-o file]", summary: "download a file",
			run: func(a *args) error { return cmdGetFile(a) }},
		{name: "put", group: "Files", usage: "put LOCAL [NAME]", summary: "upload a file",
			run: func(a *args) error { return cmdPutFile(a) }},
		{name: "setting", group: "System", usage: "setting NS KEY [VALUE]", summary: "read or write a setting",
			run: func(a *args) error { return cmdSetting(a) }},
		{name: "prop", group: "System", usage: "prop NAME", summary: "read a system property",
			method: "GET", path: func(a *args) (string, error) { return "/v1/props/" + a.str(0, ""), nil }},
		{name: "notifications", group: "System", usage: "notifications", summary: "notification list",
			method: "GET", path: fixed("/v1/notifications")},
		{name: "sms", group: "System", usage: "sms [--limit N]", summary: "recent SMS",
			run: func(a *args) error { return cmdSMS(a) }},
		{name: "location", group: "System", usage: "location", summary: "current location",
			method: "GET", path: fixed("/v1/location")},

		// Displays and streaming.
		{name: "displays", group: "Display", usage: "displays", summary: "list displays",
			method: "GET", path: fixed("/v1/displays")},
		{name: "display-create", group: "Display", usage: "display-create [W H DPI] [--name N]",
			summary: "create a virtual display", run: func(a *args) error { return cmdDisplayCreate(a) }},
		{name: "display-rm", group: "Display", usage: "display-rm ID", summary: "destroy a virtual display",
			run: func(a *args) error { return cmdDisplayRemove(a) }},
		{name: "stream", group: "Display", usage: "stream [--display N] [--seconds N]",
			summary: "verify the WebRTC stream carries frames", run: func(a *args) error { return cmdStream(a) }},

		// Triggers.
		{name: "triggers", group: "Triggers", usage: "triggers", summary: "list triggers",
			method: "GET", path: fixed("/v1/triggers")},
		{name: "runs", group: "Triggers", usage: "runs [--trigger ID] [--limit N]", summary: "trigger runs",
			run: func(a *args) error { return cmdRuns(a) }},
		{name: "trigger-add", group: "Triggers", usage: "trigger-add NAME TYPE [--pattern P] [--package P] [--text T] [--interval N]",
			summary: "create a trigger", run: func(a *args) error { return cmdTriggerAdd(a) }},
		{name: "trigger-rm", group: "Triggers", usage: "trigger-rm ID", summary: "delete a trigger",
			run: func(a *args) error { return cmdDelete(a, "/v1/triggers/") }},

		// Acceptance.
		{name: "acceptance", group: "Acceptance", usage: "acceptance [--out DIR]",
			summary: "run the capability matrix and write a report", run: func(a *args) error { return cmdAcceptance(a) }},

		// Maintenance.
		{name: "token", group: "Maintenance", usage: "token show|forget", summary: "inspect or delete the local pairing",
			run: func(a *args) error { return cmdToken(a.pos) }},
		{name: "skill", group: "Maintenance", usage: "skill [-o FILE] [--zip] [--install DIR]",
			summary: "download or install the agent manual", run: func(a *args) error { return cmdSkill(a) }},
		{name: "run", group: "Maintenance", usage: "run recipe.json [--resume DIR] [--seed S] [--force]",
			summary: "execute a JSON recipe of generic primitives", run: func(a *args) error { return cmdRun(a) }},
		{name: "log", group: "Maintenance", usage: "log [--lines N]",
			summary: "tail the daemon log", run: func(a *args) error { return cmdLog(a) }},
		{name: "version", group: "Maintenance", usage: "version", summary: "print the CLI version",
			run: func(a *args) error { fmt.Println("chopin " + cliVersion); return nil }},
	}
}

// lookupCommand finds a verb.
func lookupCommand(name string) (command, bool) {
	for _, c := range commands {
		if c.name == name {
			return c, true
		}
	}
	return command{}, false
}

// help renders the table grouped by area.
func help() string {
	groups := map[string][]command{}
	var order []string
	for _, c := range commands {
		if _, ok := groups[c.group]; !ok {
			order = append(order, c.group)
		}
		groups[c.group] = append(groups[c.group], c)
	}
	var b strings.Builder
	b.WriteString("chopin " + cliVersion + " — drive a chopin device from a computer\n")
	for _, g := range order {
		b.WriteString("\n" + g + ":\n")
		list := groups[g]
		sort.Slice(list, func(i, j int) bool { return list[i].name < list[j].name })
		for _, c := range list {
			b.WriteString(fmt.Sprintf("  %-46s %s\n", c.usage, c.summary))
		}
	}
	return b.String()
}

// fixed returns a path function that ignores its arguments.
func fixed(p string) func(*args) (string, error) {
	return func(*args) (string, error) { return p, nil }
}

// emptyBody is the body builder for POSTs with no payload.
func emptyBody(*args) (any, error) { return map[string]any{}, nil }

// regionBody reads --region into {left,top,right,bottom}.
func regionBody(a *args) (any, error) {
	region, err := parseRegion(a)
	if err != nil {
		return nil, err
	}
	if region == nil {
		return map[string]any{}, nil
	}
	return map[string]any{"region": region}, nil
}

// ocrBody is regionBody plus an optional display.
func ocrBody(a *args) (any, error) {
	body, err := regionBody(a)
	if err != nil {
		return nil, err
	}
	m := body.(map[string]any)
	if display, err := a.optInt("display", 0); err == nil && display != 0 {
		// The daemon's OCR body field is `display`; `display_id` was silently
		// ignored, so --display never took effect.
		m["display"] = display
	}
	if a.has("fresh") {
		m["fresh"] = true
	}
	return m, nil
}

// parseRegion parses `--region L T R B`.
func parseRegion(a *args) (map[string]int, error) {
	raw, ok := a.opts["region"]
	if !ok || raw == "" {
		return nil, nil
	}
	fields := strings.Fields(raw)
	if len(fields) != 4 {
		return nil, fmt.Errorf("--region needs four numbers: L T R B")
	}
	var v [4]int
	for i, f := range fields {
		if _, err := fmt.Sscanf(f, "%d", &v[i]); err != nil {
			return nil, fmt.Errorf("--region value %q is not a number", f)
		}
	}
	return map[string]int{"left": v[0], "top": v[1], "right": v[2], "bottom": v[3]}, nil
}

// runCommand executes a table entry.
func runCommand(c command, a *args) error {
	if c.run != nil {
		return c.run(a)
	}
	path, err := c.path(a)
	if err != nil {
		return err
	}
	if c.method == "GET" {
		return cmdGet(path)
	}
	var body any = map[string]any{}
	if c.body != nil {
		body, err = c.body(a)
		if err != nil {
			return err
		}
	}
	if a.raw != "" {
		var decoded any
		if err := json.Unmarshal([]byte(a.raw), &decoded); err != nil {
			return fmt.Errorf("--json is not valid JSON: %w", err)
		}
		body = decoded
	}
	return cmdPost(path, body)
}
