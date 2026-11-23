package engine

import (
	"net/http"

	"sextant-core/internal/httpx"
)

// apiPaths is the single source for the OpenAPI document. Adding a route means
// adding it here too; check-contracts asserts the two agree.
var apiPaths = map[string]map[string]string{
	"/doc":                        {"get": "this document (public)"},
	"/doc/skill":                  {"get": "agent operation manual (public)"},
	"/doc/skill.zip":              {"get": "agent operation manual as a skill package (public)"},
	"/v1/health":                  {"get": "liveness, version, schema"},
	"/v1/status":                  {"get": "published status object"},
	"/v1/pair":                    {"post": "trade a pairing code for an API token"},
	"/v1/device":                  {"get": "device info"},
	"/v1/device/state":            {"get": "screen/user/keyguard state"},
	"/v1/device/lock-state":       {"get": "lock state and credential presence"},
	"/v1/device/wake":             {"post": "wake the screen"},
	"/v1/device/unlock":           {"post": "unlock with the stored pattern"},
	"/v1/device/unlock-local":     {"post": "unlock from the device (loopback app token)"},
	"/v1/device/sim-unlock":       {"post": "enter the SIM PIN when prompted"},
	"/v1/device/lock":             {"post": "lock the screen"},
	"/v1/device/screen":           {"post": "set brightness and screen timeout"},
	"/v1/device/rotation":         {"get": "read rotation", "post": "set rotation"},
	"/v1/device/keep-awake":       {"post": "hold or release a partial wake lock"},
	"/v1/device/airplane":         {"post": "airplane mode"},
	"/v1/device/wifi":             {"post": "Wi-Fi on/off"},
	"/v1/input/tap":               {"post": "tap at x,y"},
	"/v1/input/swipe":             {"post": "swipe between two points"},
	"/v1/input/longpress":         {"post": "long press at x,y"},
	"/v1/input/key":               {"post": "send a key event"},
	"/v1/input/text":              {"post": "type text into the focused field"},
	"/v1/input/global":            {"post": "send a global action (back/home/...)"},
	"/v1/input/ime":               {"get": "IME lease and active method"},
	"/v1/input/ime/text":          {"post": "commit text through the IME"},
	"/v1/input/ime/key":           {"post": "send a key through the IME"},
	"/v1/ui/screenshot":           {"get": "capture the screen to a PNG"},
	"/v1/ui/tree":                 {"get": "accessibility node tree (fields/max_items/summary/viewport_only query)"},
	"/v1/ui/frame":                {"get": "latest frame as image/jpeg bytes"},
	"/v1/ui/observe":              {"post": "one capture: tree, OCR, pixels and frame"},
	"/v1/ui/act":                  {"post": "verified action: click/select/longpress/set_text"},
	"/v1/ui/list":                 {"post": "extract repeated items from the current page"},
	"/v1/ui/options":              {"post": "read option groups and their selected state"},
	"/v1/ui/readback":             {"post": "confirm a result by reading the tree or OCR"},
	"/v1/ui/wait_stable":          {"post": "wait until the tree or frame stops changing"},
	"/v1/ui/page":                 {"get": "foreground, windows, keyboard and page fingerprint"},
	"/v1/ui/diff":                 {"post": "structural difference between two observed trees (frame ids expire after 60s)"},
	"/v1/ui/challenge":            {"post": "classify a verification wall: slider, code, unknown or none"},
	"/v1/ui/challenge/solve":      {"post": "attempt a slider wall; a code wall is refused for handover"},
	"/v1/challenge/risk":          {"get": "package-level challenge records (decaying)"},
	"/v1/ui/ocr":                  {"post": "ROM OCR on the current screen"},
	"/v1/ui/ocr/debug":            {"get": "ROM OCR engine diagnostics"},
	"/v1/ui/find":                 {"post": "find a node by text or resource id, optionally tap"},
	"/v1/ui/wait":                 {"post": "wait until a node appears or disappears"},
	"/v1/ui/chain":                {"post": "run a short step sequence"},
	"/v1/ui/events":               {"get": "accessibility events (SSE)"},
	"/v1/ui/color_probe":          {"post": "measure how much of a region matches a colour"},
	"/v1/ui/pixel_stats":          {"post": "brightness and colour statistics for a region"},
	"/v1/ui/panel_read":           {"post": "OCR text plus countdown and action button"},
	"/v1/ui/wait_result":          {"post": "wait for a condition and return the frame"},
	"/v1/sessions":                {"get": "list write sessions", "post": "claim a write session"},
	"/v1/sessions/{id}/heartbeat": {"post": "renew a session"},
	"/v1/sessions/{id}":           {"delete": "release a session"},
	"/v1/roster":                  {"get": "agent roster and bans"},
	"/v1/guard/reap":              {"post": "release expired sessions"},
	"/v1/notifications":           {"get": "notification list"},
	"/v1/notifications/dismiss":   {"post": "dismiss a notification"},
	"/v1/notifications/events":    {"get": "notification stream (SSE)"},
	"/v1/sms/messages":            {"get": "SMS messages"},
	"/v1/sms/send":                {"post": "send an SMS"},
	"/v1/sms/status/{id}":         {"get": "SMS send status"},
	"/v1/location":                {"get": "current location"},
	"/v1/audio":                   {"get": "microphone state, route, and every audio device"},
	"/v1/audio/mic":               {"post": "set the microphone mute and report whether it moved"},
	"/v1/audio/route":             {"post": "speaker or earpiece, plus the audio mode"},
	"/v1/audio/listen":            {"post": "capture a short burst and report its level"},
	"/v1/audio/speak":             {"post": "play raw PCM (16 kHz mono s16le) through the chosen route"},
	"/v1/settings/daily":          {"get": "the daily settings and their current values"},
	"/v1/settings/daily/{name}":   {"put": "set one daily setting through its owning service, then read it back"},
	"/v1/settings/{ns}/{key}":     {"get": "read a setting", "put": "write a setting"},
	"/v1/props/{name}":            {"get": "read a system property"},
	"/v1/components/disable":      {"post": "disable a component"},
	"/v1/components/enable":       {"post": "enable a component"},
	"/v1/watchdog":                {"get": "daemon and helper health"},
	"/v1/metrics":                 {"get": "in-memory request, UI and helper counters (resets on restart)"},
	"/v1/apps":                    {"get": "installed third-party packages"},
	"/v1/apps/foreground":         {"get": "foreground package"},
	"/v1/apps/launch":             {"post": "launch an app"},
	"/v1/apps/deep-link":          {"post": "open a URI"},
	"/v1/apps/stop":               {"post": "force-stop an app"},
	"/v1/apps/install":            {"post": "install an uploaded APK"},
	"/v1/apps/uninstall":          {"post": "uninstall (confirmation)"},
	"/v1/apps/clearData":          {"post": "clear app data (confirmation)"},
	"/v1/apps/permissions":        {"post": "grant or revoke a permission"},
	"/v1/files":                   {"get": "list or stat a path"},
	"/v1/files/download":          {"get": "download a file"},
	"/v1/files/delete":            {"post": "delete a path"},
	"/v1/files/move":              {"post": "move a path"},
	"/v1/uploads":                 {"post": "stream a file into the daemon tmp dir"},
	"/v1/device/reboot":           {"post": "reboot (confirmation)"},
	"/v1/device/handover":         {"get": "handover state", "post": "move an app to the user's display for manual verification"},
	"/v1/device/handover/return":  {"post": "end handover and optionally relaunch on the agent display"},
	"/v1/displays":                {"get": "list displays", "post": "create an overlay display"},
	"/v1/displays/{id}":           {"delete": "destroy an overlay display"},
	"/v1/stream/webrtc/offer":     {"post": "answer an SDP offer with an H.264 track"},
	"/v1/stream/webrtc/ice":       {"post": "add a trickled ICE candidate"},
	"/v1/stream/stats":            {"get": "viewer and display stream statistics"},
	"/v1/stream/viewers/{id}":     {"delete": "detach a viewer"},
	"/v1/hud":                     {"get": "overlay state (loopback only)"},
	"/v1/hud/heartbeat":           {"post": "overlay heartbeat (loopback only)"},
	"/v1/hud/pause":               {"post": "pause or resume (loopback only)"},
	"/v1/hud/stop":                {"post": "stop everything (loopback only)"},
	"/v1/hud/ack":                 {"post": "acknowledge a capture/pass-through instruction (loopback only)"},
	"/v1/hud/events":              {"get": "overlay state stream (SSE, loopback only)"},
	"/v1/triggers":                {"get": "list triggers", "post": "create a trigger"},
	"/v1/triggers/{id}":           {"patch": "update a trigger", "delete": "delete a trigger"},
	"/v1/triggers/{id}/runs":      {"get": "runs for one trigger"},
	"/v1/triggers/runs":           {"get": "all runs"},
	"/v1/triggers/events":         {"get": "trigger runs (SSE)"},
}

// publicOperations are the endpoints that answer without a bearer token. The
// document's global security requirement is bearer, so each of these opts out
// with an empty requirement; otherwise the document claims a token is needed
// where it is not. Keys are "<method> <path>", matching apiPaths.
var publicOperations = map[string]bool{
	"get /doc":       true,
	"get /v1/health": true,
	"post /v1/pair":  true,
}

// openAPIDocument builds the document for a caller that reached us at host.
// The listener address (0.0.0.0:8443) is not a usable base URL because every
// client would be told to connect to 0.0.0.0; r.Host is what the caller used.
func guidance() map[string]any {
	// The agent's first request is /doc. Pointing at the skill only through
	// externalDocs made it a second request and left "what do I do first"
	// unanswered, so the ordered steps live here too. Commands are exactly the
	// ones the manual documents; credentials are always stdin, never argv.
	return map[string]any{
		"summary":   "Read these steps in order: install once, then drive the phone through the /v1 primitives. /doc/skill has the same guidance in Markdown.",
		"skill":     "/doc/skill",
		"skill_zip": "/doc/skill.zip",
		"steps": []map[string]any{
			{
				"id": "install-module", "phase": "install",
				"title":   "Flash the sextant-core KernelSU module",
				"command": "adb push modules/sextant-core.zip /data/local/tmp/sextant-core.zip && adb shell su -c 'ksud module install /data/local/tmp/sextant-core.zip'",
				"expect":  "ksud reports Done; the zip is a build artefact (tools/scripts/build-module.sh) and is not in Git",
			},
			{
				"id": "reboot", "phase": "install",
				"title":   "Reboot once so the module runs",
				"command": "adb reboot && adb wait-for-device",
				"expect":  "the device reconnects",
			},
			{
				"id": "health", "phase": "install",
				"title":   "Confirm the daemon is up",
				"command": "sextant health",
				"expect":  "a JSON body with \"ok\": true",
			},
			{
				"id": "init", "phase": "install",
				"title":   "Pair and store the unlock credential in one command",
				"command": "printf '%s\\n' '<unlock pattern>' | sextant init --serial <adb-serial>",
				"expect":  "health passes and /v1/status answers; the pattern goes through stdin only",
			},
			{
				"id": "doctor", "phase": "install",
				"title":   "Check the environment",
				"command": "sextant doctor",
				"expect":  "pairing, daemon, accessibility, background policy and helper all ok",
			},
			{
				"id": "status", "phase": "use",
				"title":   "Read the single status object",
				"command": "sextant status",
				"expect":  "daemon/app/unlock/sessions fields match /v1/status",
			},
			{
				"id": "observe", "phase": "use",
				"title":   "Look before acting",
				"command": "sextant observe --tree --frame",
				"expect":  "tree, OCR/pixels and the JPEG come from the same frame_id",
			},
			{
				"id": "act", "phase": "use",
				"title":   "Act and verify",
				"command": "sextant act --text '<control label>' --action click --verify-ms 1500",
				"expect":  "dispatched=true plus verified=true/false/unknown and a failure.code when false",
			},
			{
				"id": "map", "phase": "use",
				"title":   "Use a compiled app map when one exists",
				"command": "sextant map where ~/.sextant/maps/<package>.yaml && sextant map nav ~/.sextant/maps/<package>.yaml <op>",
				"expect":  "the current screen is identified and the NAV operation lands on its target screen",
			},
			{
				"id": "run", "phase": "use",
				"title":   "Run a recipe for a repeatable task",
				"command": "sextant run recipe.json --seed <seed>",
				"expect":  "steps report ok/failed; --resume continues from the failed step with the same seed",
			},
		},
	}
}

func openAPIDocument(host string) map[string]any {
	paths := map[string]any{}
	for p, methods := range apiPaths {
		m := map[string]any{}
		for verb, summary := range methods {
			op := map[string]any{
				"summary":   summary,
				"responses": map[string]any{"200": map[string]any{"description": "ok"}},
			}
			// In OpenAPI 3.1 an absent operation security inherits the document
			// requirement; an explicit empty array overrides it.
			if publicOperations[verb+" "+p] {
				op["security"] = []any{}
			}
			m[verb] = op
		}
		paths[p] = m
	}
	return map[string]any{
		"openapi": "3.1.0",
		"info":    map[string]any{"title": "sextant-core", "version": Version},
		"externalDocs": map[string]any{
			"description": "agent operation manual",
			"url":         "/doc/skill",
		},
		"x-sextant-guidance": guidance(),
		"servers":            []map[string]any{{"url": "https://" + host}},
		"components": map[string]any{
			"securitySchemes": map[string]any{
				"bearer": map[string]any{"type": "http", "scheme": "bearer"},
			},
		},
		"security": []map[string]any{{"bearer": []string{}}},
		"paths":    paths,
	}
}

func (s *Server) handleDoc(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	if host == "" {
		// HTTP/1.0 without a Host header is the only way here; fall back to the
		// configured listener rather than emitting an empty base URL.
		host = s.Cfg.Listen
	}
	httpx.WriteJSON(w, http.StatusOK, openAPIDocument(host))
}
