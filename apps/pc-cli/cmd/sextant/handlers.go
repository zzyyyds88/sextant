package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	"sextant/internal/client"
)

// Handlers for commands that need more than one API call or local work.

// cmdDoc prints the OpenAPI document, optionally to a file.
func cmdDoc(a *args) error {
	c, err := paired()
	if err != nil {
		return err
	}
	data, err := c.Get("/doc")
	if err != nil {
		return err
	}
	out := a.optStr("o", a.optStr("out", ""))
	if out == "" {
		return printJSON(data)
	}
	if err := os.WriteFile(out, append(data, '\n'), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", out, err)
	}
	fmt.Printf("%s (%d bytes)\n", out, len(data))
	return nil
}

// cmdRotation reads, or sets, the rotation.
func cmdRotation(a *args) error {
	if len(a.pos) == 0 {
		return cmdGet("/v1/device/rotation")
	}
	return cmdPost("/v1/device/rotation", map[string]any{"value": a.pos[0]})
}

// cmdScreen sets brightness and/or the screen timeout.
func cmdScreen(a *args) error {
	body := map[string]any{}
	if a.has("brightness") {
		body["brightness"] = a.opts["brightness"]
	}
	if v := a.optStr("timeout-ms", ""); v != "" {
		body["timeout_ms"] = v
	}
	if len(body) == 0 {
		return fmt.Errorf("usage: sextant screen [--brightness N] [--timeout-ms N]")
	}
	return cmdPost("/v1/device/screen", body)
}

// cmdRadio toggles airplane or wifi.
func cmdRadio(a *args, path string) error {
	if err := a.need(1, "on|off"); err != nil {
		return err
	}
	switch strings.ToLower(a.pos[0]) {
	case "on", "true", "1":
		return cmdPost(path, map[string]any{"on": true})
	case "off", "false", "0":
		return cmdPost(path, map[string]any{"on": false})
	}
	return fmt.Errorf("expected on or off, got %q", a.pos[0])
}

func displayOf(a *args) int {
	n, _ := a.optInt("display", 0)
	return n
}

func cmdTapArgs(a *args) error {
	if err := a.need(2, "tap X Y"); err != nil {
		return err
	}
	x, err := a.num(0, 0)
	if err != nil {
		return err
	}
	y, err := a.num(1, 0)
	if err != nil {
		return err
	}
	body := map[string]any{"x": x, "y": y, "display": displayOf(a)}
	if seed := a.optStr("seed", ""); seed != "" {
		body["seed"] = seed
	}
	return cmdPost("/v1/input/tap", body)
}

func cmdSwipeArgs(a *args) error {
	if err := a.need(4, "swipe X1 Y1 X2 Y2 [duration_ms]"); err != nil {
		return err
	}
	var v [4]int
	for i := 0; i < 4; i++ {
		n, err := a.num(i, 0)
		if err != nil {
			return err
		}
		v[i] = n
	}
	dur, err := a.num(4, 300)
	if err != nil {
		return err
	}
	return cmdPost("/v1/input/swipe", map[string]any{
		"x1": v[0], "y1": v[1], "x2": v[2], "y2": v[3],
		"duration_ms": dur, "display": displayOf(a),
	})
}

func cmdLongPressArgs(a *args) error {
	if err := a.need(2, "longpress X Y [duration_ms]"); err != nil {
		return err
	}
	x, err := a.num(0, 0)
	if err != nil {
		return err
	}
	y, err := a.num(1, 0)
	if err != nil {
		return err
	}
	dur, err := a.num(2, 800)
	if err != nil {
		return err
	}
	return cmdPost("/v1/input/longpress", map[string]any{
		"x": x, "y": y, "duration_ms": dur, "display": displayOf(a),
	})
}

func cmdKeyArgs(a *args) error {
	if err := a.need(1, "key KEYCODE_HOME"); err != nil {
		return err
	}
	return cmdPost("/v1/input/key", map[string]any{"key": a.pos[0], "display": displayOf(a)})
}

func cmdGlobal(a *args) error {
	if err := a.need(1, "global back|home|recents|..."); err != nil {
		return err
	}
	return cmdPost("/v1/input/global", map[string]any{"action": a.pos[0]})
}

// cmdText types text; --ime forces the input method path.
func cmdText(a *args) error {
	text := strings.Join(a.pos, " ")
	// `sextant text "" --clear` is the documented way to empty a field, so empty text
	// is only an error when there is nothing to clear either.
	if text == "" && !a.has("clear") {
		return fmt.Errorf("usage: sextant text TEXT [--ime] [--clear] [--at x,y]")
	}
	if a.has("ime") {
		body := map[string]any{"text": text}
		if a.has("clear") {
			body["clear"] = true
		}
		return cmdPost("/v1/input/ime/text", body)
	}
	// --clear goes through the general path, which now clears with ACTION_SET_TEXT
	// when it can and only falls back to the module IME when it cannot.
	//
	// It used to be routed straight to /v1/input/ime/text, so a clear always cost a
	// keyboard switch (~1.4 s) even on an ordinary EditText that ACTION_SET_TEXT
	// clears instantly — and the flag is common enough that this was most of the
	// typing latency a user would ever see.
	//
	// `--at x,y` names the field: the search box a script just tapped is often not
	// focusable until it is touched, and "no editable node on display 0" then costs a
	// 1.6 s keyboard switch for a field the caller could have pointed at.
	body := map[string]any{"text": text, "clear": a.has("clear")}
	if xy := a.optStr("at", ""); xy != "" {
		parts := strings.SplitN(xy, ",", 2)
		if len(parts) != 2 {
			return fmt.Errorf("--at takes x,y")
		}
		x, err1 := strconv.Atoi(strings.TrimSpace(parts[0]))
		y, err2 := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err1 != nil || err2 != nil {
			return fmt.Errorf("--at takes two integers, got %q", xy)
		}
		body["x"], body["y"] = x, y
	}
	return cmdPost("/v1/input/text", body)
}

// cmdTree prints the node tree, or a compact summary with the default output.
func cmdTree(a *args) error {
	c, err := paired()
	if err != nil {
		return err
	}
	// D29 P1: a large page does not need to be shipped whole. `--fields`,
	// `--max-items` and `--viewport` are passed through; `--summary` both crops
	// the response and prints the human-readable form.
	path := "/v1/ui/tree"
	query := make([]string, 0, 4)
	if a.has("summary") {
		query = append(query, "summary=1")
	}
	if v := a.optStr("fields", ""); v != "" {
		query = append(query, "fields="+v)
	}
	if n, err := a.optInt("max-items", 0); err != nil {
		return err
	} else if n > 0 {
		query = append(query, "max_items="+strconv.Itoa(n))
	}
	if a.has("viewport") {
		query = append(query, "viewport_only=1")
	}
	if len(query) > 0 {
		path += "?" + strings.Join(query, "&")
	}
	data, err := c.Get(path)
	if err != nil {
		return err
	}
	// D29: JSON is the default. The human-readable summary is opt-in, because a
	// script that follows the README must not have to know about --raw.
	if !a.has("summary") {
		return printJSON(data)
	}
	var resp struct {
		Count  int    `json:"count"`
		Source string `json:"source"`
		Nodes  []struct {
			Class       string `json:"class"`
			Text        string `json:"text"`
			ResourceID  string `json:"resource_id"`
			Bounds      [4]int `json:"bounds"`
			ContentDesc string `json:"content_desc"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return printJSON(data)
	}
	fmt.Printf("%d nodes (source: %s)\n", resp.Count, resp.Source)
	for _, n := range resp.Nodes {
		label := n.Text
		if label == "" {
			label = n.ContentDesc
		}
		if label == "" && n.ResourceID == "" {
			continue
		}
		fmt.Printf("  %-34s %-30s %v\n", shortClass(n.Class), truncate(label, 30), n.Bounds)
	}
	return nil
}

// cmdFind locates text with the tree, then OCR, and optionally taps it.
func cmdFind(a *args) error {
	text := strings.Join(a.pos, " ")
	rid := a.optStr("id", "")
	if text == "" && rid == "" {
		return fmt.Errorf("usage: sextant find TEXT | find --id RID [--click] [--timeout-ms N]")
	}
	body := map[string]any{}
	if text != "" {
		body["text"] = text
	}
	if rid != "" {
		body["resource_id"] = rid
	}
	if a.has("click") {
		body["click"] = true
	}
	if t, err := a.optInt("timeout-ms", 5000); err == nil {
		body["timeout_ms"] = t
	}
	return cmdPost("/v1/ui/find", body)
}

// cmdColor reports how much of a region matches a colour.
func cmdColor(a *args) error {
	if err := a.need(3, "color R G B [--tolerance N]"); err != nil {
		return err
	}
	rgb := [3]int{}
	for i := 0; i < 3; i++ {
		n, err := a.num(i, 0)
		if err != nil {
			return err
		}
		rgb[i] = n
	}
	body := map[string]any{"r": rgb[0], "g": rgb[1], "b": rgb[2]}
	if tol, err := a.optInt("tolerance", 24); err == nil {
		body["tolerance"] = tol
	}
	region, err := parseRegion(a)
	if err != nil {
		return err
	}
	if region != nil {
		body["region"] = region
	}
	return cmdPost("/v1/ui/color_probe", body)
}

func cmdClaim(a *args) error {
	c, err := paired()
	if err != nil {
		return err
	}
	body := map[string]any{
		"agent":   a.str(0, "cli"),
		"display": displayOf(a),
	}
	out, err := c.Post("/v1/sessions", body)
	if err != nil {
		return err
	}
	var s struct {
		ID    string `json:"id"`
		Token string `json:"token"`
		Agent string `json:"agent"`
	}
	if json.Unmarshal(out, &s) == nil && s.ID != "" && s.Token != "" {
		if err := client.SaveSession(client.Session{Server: c.Server(), ID: s.ID, Token: s.Token, Agent: s.Agent}); err != nil {
			fmt.Fprintf(os.Stderr, "warning: could not save local session: %v\n", err)
		}
	}
	return printJSON(out)
}

func cmdRelease(a *args) error {
	if err := a.need(1, "release ID"); err != nil {
		return err
	}
	c, err := paired()
	if err != nil {
		return err
	}
	id := a.pos[0]
	out, err := c.Delete("/v1/sessions/" + id)
	if err != nil {
		return err
	}
	if sess, err := client.LoadSession(); err == nil && sess.ID == id {
		_ = client.ClearSession()
	}
	return printJSON(out)
}

func cmdHeartbeat(a *args) error {
	c, err := paired()
	if err != nil {
		return err
	}
	sess, err := client.LoadSession()
	if err != nil {
		return err
	}
	if sess.ID == "" || sess.Token == "" {
		return fmt.Errorf("no local session; run 'sextant claim' first")
	}
	out, err := c.Post("/v1/sessions/"+sess.ID+"/heartbeat", map[string]any{"token": sess.Token})
	if err != nil {
		return err
	}
	return printJSON(out)
}

func cmdLaunch(a *args) error {
	if err := a.need(1, "launch PKG|COMPONENT [--clear-task]"); err != nil {
		return err
	}
	target := a.pos[0]
	body := map[string]any{}
	if strings.Contains(target, "/") {
		body["component"] = target
	} else {
		body["package"] = target
	}
	if a.has("clear-task") {
		body["clear_task"] = true
	}
	return cmdPost("/v1/apps/launch", body)
}

func cmdPackagePost(a *args, path string) error {
	if err := a.need(1, "PACKAGE"); err != nil {
		return err
	}
	return cmdPost(path, map[string]any{"package": a.pos[0]})
}

func cmdOpen(a *args) error {
	if err := a.need(1, "open URI"); err != nil {
		return err
	}
	return cmdPost("/v1/apps/deep-link", map[string]any{"uri": a.pos[0]})
}

// cmdPut writes a setting (PUT) or posts a JSON body depending on the path shape.
func cmdPut(path string, body any) error {
	c, err := paired()
	if err != nil {
		return err
	}
	data, err := c.Put(path, body)
	if err != nil {
		return err
	}
	return printJSON(data)
}

// cmdPostRaw uploads bytes to the daemon.
func cmdPostRaw(c *client.Client, path string, data []byte) error {
	out, err := c.PostRaw(path, data)
	if err != nil {
		return err
	}
	return printJSON(out)
}

func cmdLS(a *args) error {
	if err := a.need(1, "ls PATH"); err != nil {
		return err
	}
	return cmdGet("/v1/files?path=" + urlEscape(a.pos[0]))
}

func cmdGetFile(a *args) error {
	if err := a.need(1, "get PATH -o FILE"); err != nil {
		return err
	}
	out := a.optStr("o", a.optStr("out", ""))
	if out == "" {
		return fmt.Errorf("usage: sextant get PATH -o FILE")
	}
	c, err := paired()
	if err != nil {
		return err
	}
	data, err := c.Get("/v1/files/download?path=" + urlEscape(a.pos[0]))
	if err != nil {
		return err
	}
	if err := os.WriteFile(out, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", out, err)
	}
	fmt.Printf("%s (%d bytes)\n", out, len(data))
	return nil
}

func cmdPutFile(a *args) error {
	if err := a.need(1, "put LOCAL [NAME]"); err != nil {
		return err
	}
	local := a.pos[0]
	name := a.str(1, "")
	if name == "" {
		parts := strings.Split(local, "/")
		name = parts[len(parts)-1]
	}
	c, err := paired()
	if err != nil {
		return err
	}
	data, err := os.ReadFile(local)
	if err != nil {
		return fmt.Errorf("read %s: %w", local, err)
	}
	return cmdPostRaw(c, "/v1/uploads?name="+urlEscape(name), data)
}

func cmdSetting(a *args) error {
	if err := a.need(2, "setting NS KEY [VALUE]"); err != nil {
		return err
	}
	ns, key := a.pos[0], a.pos[1]
	if a.has("value") {
		return cmdPut("/v1/settings/"+ns+"/"+key, map[string]any{"value": a.opts["value"]})
	}
	if len(a.pos) >= 3 {
		return cmdPut("/v1/settings/"+ns+"/"+key, map[string]any{"value": a.pos[2]})
	}
	return cmdGet("/v1/settings/" + ns + "/" + key)
}

func cmdSMS(a *args) error {
	limit, err := a.optInt("limit", 20)
	if err != nil {
		return err
	}
	return cmdGet("/v1/sms/messages?limit=" + strconv.Itoa(limit))
}

func cmdDisplayCreate(a *args) error {
	body := map[string]any{}
	if len(a.pos) >= 3 {
		var v [3]int
		for i := 0; i < 3; i++ {
			n, err := a.num(i, 0)
			if err != nil {
				return err
			}
			v[i] = n
		}
		body["width"], body["height"], body["dpi"] = v[0], v[1], v[2]
	}
	if name := a.optStr("name", ""); name != "" {
		body["name"] = name
	}
	return cmdPost("/v1/displays", body)
}

func cmdDisplayRemove(a *args) error {
	if err := a.need(1, "display-rm ID"); err != nil {
		return err
	}
	return cmdDelete(a, "/v1/displays/")
}

func cmdRuns(a *args) error {
	path := "/v1/triggers/runs"
	if id := a.optStr("trigger", ""); id != "" {
		path = "/v1/triggers/" + id + "/runs"
	}
	if limit, err := a.optInt("limit", 20); err == nil {
		path += "?limit=" + strconv.Itoa(limit)
	}
	return cmdGet(path)
}

func cmdTriggerAdd(a *args) error {
	if err := a.need(2, "trigger-add NAME TYPE"); err != nil {
		return err
	}
	spec := map[string]any{}
	if v := a.optStr("pattern", ""); v != "" {
		spec["pattern"] = v
	}
	if v := a.optStr("package", ""); v != "" {
		spec["package"] = v
	}
	if v := a.optStr("text", ""); v != "" {
		spec["text_contains"] = v
	}
	if a.has("interval") {
		interval, err := a.optInt("interval", 60)
		if err != nil {
			return err
		}
		spec["interval_seconds"] = interval
	}
	return cmdPost("/v1/triggers", map[string]any{
		"name": a.pos[0], "type": a.pos[1], "spec": spec,
	})
}

func cmdDelete(a *args, prefix string) error {
	if err := a.need(1, "ID"); err != nil {
		return err
	}
	c, err := paired()
	if err != nil {
		return err
	}
	data, err := c.Delete(prefix + urlEscape(a.pos[0]))
	if err != nil {
		return err
	}
	return printJSON(data)
}

// helpers -------------------------------------------------------------------

func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "…"
}

func shortClass(class string) string {
	if i := strings.LastIndex(class, "."); i >= 0 {
		return class[i+1:]
	}
	return class
}

func urlEscape(s string) string {
	var b strings.Builder
	for _, r := range []byte(s) {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.', r == '/', r == '~':
			b.WriteByte(r)
		default:
			b.WriteString(fmt.Sprintf("%%%02X", r))
		}
	}
	return b.String()
}

// cmdCall reads and drives phone calls.
// cmdSettings reads and writes the daily settings.
//
// `sextant settings` lists them with their current values; `sextant settings
// airplane_mode on` sets one and prints what the system reports afterwards.
func cmdSettings(a *args) error {
	if len(a.pos) == 0 {
		return cmdGet("/v1/settings/daily")
	}
	if len(a.pos) < 2 {
		return fmt.Errorf("usage: sextant settings [NAME VALUE [--yes REASON]]  (no args lists everything)")
	}
	body := map[string]any{"value": strings.Join(a.pos[1:], " ")}
	// A dangerous setting answers 202 with a confirmation_id; the caller has to
	// decide whether it is really necessary and repeat with --yes. Printing the
	// handed-back id is the point: the second call is a decision, not a retry.
	if id := a.optStr("confirmation-id", ""); id != "" {
		body["confirmation_id"] = id
		body["reason"] = a.optStr("yes", "")
	}
	return cmdPut("/v1/settings/daily/"+urlEscape(a.pos[0]), body)
}

// cmdAudio is the device's ears and mouth.
//
//	sextant audio                     state: mic mute, route, every device
//	sextant audio mic on|off          mute or unmute the microphone
//	sextant audio route speaker|earpiece
//	sextant audio listen [ms]         capture a burst, print its level
//	sextant audio say FILE            play a 16 kHz mono s16le PCM file
//	sextant audio speak TEXT          synthesise TEXT on this computer and play it
func cmdAudio(a *args) error {
	sub := ""
	if len(a.pos) > 0 {
		sub = a.pos[0]
	}
	switch sub {
	case "", "state":
		return cmdGet("/v1/audio")
	case "mic":
		if len(a.pos) < 2 {
			return fmt.Errorf("usage: sextant audio mic on|off")
		}
		on := strings.EqualFold(a.pos[1], "on") || a.pos[1] == "1" || strings.EqualFold(a.pos[1], "unmute")
		return cmdPost("/v1/audio/mic", map[string]any{"muted": !on})
	case "route":
		if len(a.pos) < 2 {
			return fmt.Errorf("usage: sextant audio route speaker|earpiece")
		}
		speaker := strings.EqualFold(a.pos[1], "speaker")
		return cmdPost("/v1/audio/route", map[string]any{"speaker": speaker})
	case "mode":
		if len(a.pos) < 2 {
			return fmt.Errorf("usage: sextant audio mode normal|call|communication|ringtone")
		}
		return cmdPost("/v1/audio/route", map[string]any{"mode": a.pos[1]})
	case "listen":
		ms := 1000
		if len(a.pos) > 1 {
			if n, err := strconv.Atoi(a.pos[1]); err == nil {
				ms = n
			}
		}
		body := map[string]any{"ms": ms}
		// --source names an AudioRecord source; the numbers are the platform's own
		// MediaRecorder.AudioSource constants. Being able to ask for VOICE_CALL
		// explicitly is the difference between "the microphone hears the room" and
		// "this is what the call sounds like", and only the caller knows which.
		if src := a.optStr("source", ""); src != "" {
			if n, err := strconv.Atoi(src); err == nil {
				body["source"] = n
			} else if id, ok := audioSources[strings.ToLower(src)]; ok {
				body["source"] = id
			} else {
				return fmt.Errorf("unknown source %q; known: %s", src, strings.Join(audioSourceNames(), ", "))
			}
		}
		return cmdPost("/v1/audio/listen", body)
	case "say":
		if len(a.pos) < 2 {
			return fmt.Errorf("usage: sextant audio say FILE.pcm   (16 kHz mono s16le)")
		}
		raw, err := os.ReadFile(a.pos[1])
		if err != nil {
			return err
		}
		body := map[string]any{
			"pcm_base64": base64.StdEncoding.EncodeToString(raw),
			"speaker":    !a.has("earpiece"),
		}
		return cmdPost("/v1/audio/speak", body)
	case "speak":
		text := strings.Join(a.pos[1:], " ")
		if text == "" {
			return fmt.Errorf("usage: sextant audio speak TEXT")
		}
		pcm, err := synthesize(text)
		if err != nil {
			return err
		}
		body := map[string]any{
			"pcm_base64": base64.StdEncoding.EncodeToString(pcm),
			"speaker":    !a.has("earpiece"),
		}
		return cmdPost("/v1/audio/speak", body)
	}
	return fmt.Errorf("usage: sextant audio [state|mic|route|mode|listen|say|speak]")
}

// synthesize turns text into 16 kHz mono s16le PCM on THIS computer.
//
// The phone has no TTS engine (`tts_default_synth` is null), so the voice has to
// be produced where one exists. ffmpeg and espeak-ng are tried in that order
// because ffmpeg is far more common and its built-in flite voice needs no extra
// download on most boxes; when neither is present the error says so instead of
// sending silence down and letting the phone look broken.
func synthesize(text string) ([]byte, error) {
	tmp, err := os.CreateTemp("", "sextant-tts-*.wav")
	if err != nil {
		return nil, err
	}
	wav := tmp.Name()
	tmp.Close()
	defer os.Remove(wav)

	type attempt struct {
		name string
		args []string
	}
	attempts := []attempt{
		{"ffmpeg(flite)", []string{"-y", "-f", "lavfi", "-i",
			"flite=text='" + strings.ReplaceAll(text, "'", "") + "':voice=slt",
			"-ar", "16000", "-ac", "1", "-c:a", "pcm_s16le", wav}},
		{"espeak-ng", []string{"-w", wav, "-s", "160", text}},
		{"espeak", []string{"-w", wav, "-s", "160", text}},
	}
	var tried []string
	for _, at := range attempts {
		bin := strings.Fields(at.name)[0]
		if _, err := exec.LookPath(bin); err != nil {
			tried = append(tried, at.name+" (not installed)")
			continue
		}
		cmd := exec.Command(bin, at.args...)
		if out, err := cmd.CombinedOutput(); err != nil {
			tried = append(tried, at.name+" ("+firstLineOf(string(out))+")")
			continue
		}
		raw, err := os.ReadFile(wav)
		if err != nil {
			tried = append(tried, at.name+" (could not read its output)")
			continue
		}
		pcm, err := toDevicePCM(raw)
		if err != nil {
			tried = append(tried, at.name+" ("+err.Error()+")")
			continue
		}
		if len(pcm) == 0 {
			tried = append(tried, at.name+" (produced no samples)")
			continue
		}
		return pcm, nil
	}
	return nil, fmt.Errorf("no speech synthesiser here: tried %s", strings.Join(tried, ", "))
}

func firstLineOf(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		s = s[:i]
	}
	if len(s) > 120 {
		s = s[:120]
	}
	return s
}

// toDevicePCM turns whatever the synthesiser produced into what the device wants:
// bare 16 kHz mono s16le.
//
// Both parts matter. The WAV header is stripped because the daemon writes the
// bytes straight into an AudioTrack, and a RIFF header through a speaker is a click
// and then the clip at the wrong offset. The rate is converted because
// espeak-ng is fixed at 22050 Hz while the device track is opened at 16000 —
// feeding 22050 into a 16 kHz track plays it ~38% fast and shrill, which is exactly
// the kind of "it works" that nobody wants to listen to.
//
// Linear interpolation is enough for speech at these rates; it is not a resampler
// for music and does not pretend to be.
func toDevicePCM(wav []byte) ([]byte, error) {
	if len(wav) < 44 || string(wav[0:4]) != "RIFF" || string(wav[8:12]) != "WAVE" {
		return nil, fmt.Errorf("not a RIFF/WAVE file")
	}
	rate, channels, bits := 0, 0, 0
	// Walk the chunks rather than assuming the canonical 44-byte header: ffmpeg and
	// espeak disagree about whether to insert LIST/fact chunks first.
	for i := 12; i+8 <= len(wav); {
		id := string(wav[i : i+4])
		size := int(wav[i+4]) | int(wav[i+5])<<8 | int(wav[i+6])<<16 | int(wav[i+7])<<24
		body := i + 8
		if body+size > len(wav) {
			size = len(wav) - body
		}
		switch id {
		case "fmt ":
			if size >= 16 {
				channels = int(wav[body+2]) | int(wav[body+3])<<8
				rate = int(wav[body+4]) | int(wav[body+5])<<8 | int(wav[body+6])<<16 | int(wav[body+7])<<24
				bits = int(wav[body+14]) | int(wav[body+15])<<8
			}
		case "data":
			if channels == 0 || rate == 0 || bits != 16 {
				return nil, fmt.Errorf("unsupported WAV: %d ch, %d Hz, %d bit", channels, rate, bits)
			}
			data := wav[body:]
			if size < len(data) {
				data = data[:size]
			}
			return resample16(data, channels, rate), nil
		}
		i = body + size + (size & 1) // chunks are word-aligned
	}
	return nil, fmt.Errorf("WAV has no data chunk")
}

// resample16 converts interleaved 16-bit PCM at `rate` to 16 kHz mono.
func resample16(data []byte, channels, rate int) []byte {
	frames := len(data) / (2 * channels)
	if frames == 0 {
		return nil
	}
	// Mono downmix first, so the rate conversion sees one stream.
	mono := make([]float64, frames)
	for f := 0; f < frames; f++ {
		var sum int32
		for c := 0; c < channels; c++ {
			i := (f*channels + c) * 2
			sum += int32(int16(uint16(data[i]) | uint16(data[i+1])<<8))
		}
		mono[f] = float64(sum) / float64(channels)
	}
	const target = 16000
	if rate == target {
		out := make([]byte, frames*2)
		for f, v := range mono {
			s := int16(v)
			out[f*2] = byte(uint16(s))
			out[f*2+1] = byte(uint16(s) >> 8)
		}
		return out
	}
	outFrames := frames * target / rate
	out := make([]byte, outFrames*2)
	for i := 0; i < outFrames; i++ {
		pos := float64(i) * float64(rate) / float64(target)
		j := int(pos)
		frac := pos - float64(j)
		a := mono[j]
		b := a
		if j+1 < frames {
			b = mono[j+1]
		}
		s := int16(a + (b-a)*frac)
		out[i*2] = byte(uint16(s))
		out[i*2+1] = byte(uint16(s) >> 8)
	}
	return out
}

// audioSources are the MediaRecorder.AudioSource constants an agent may name.
// Named rather than numeric because "3" means nothing to a reader, and a wrong
// number records the wrong thing without complaint.
var audioSources = map[string]int{
	"mic":                 1, // MIC
	"voice_uplink":        2, // what we said
	"voice_downlink":      3, // what they said
	"voice_call":          4, // both, mixed by the platform
	"camcorder":           5,
	"voice_recognition":   6,
	"voice_communication": 7,
	"unprocessed":         9,
}

func audioSourceNames() []string {
	out := make([]string, 0, len(audioSources))
	for k := range audioSources {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
