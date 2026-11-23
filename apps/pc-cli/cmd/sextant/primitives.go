package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// CLI surface for the D29 generic primitives. Each command is a thin mapping to
// one endpoint; no business logic lives here.

func cmdWait(a *args) error {
	text := strings.Join(a.pos, " ")
	rid := a.optStr("id", "")
	if text == "" && rid == "" {
		return fmt.Errorf("usage: sextant wait TEXT | wait --id RID [--absent] [--timeout-ms N]")
	}
	body := map[string]any{"absent": a.has("absent")}
	if text != "" {
		body["text"] = text
	}
	if rid != "" {
		body["resource_id"] = rid
	}
	if n, err := a.optInt("timeout-ms", 10000); err == nil {
		body["timeout_ms"] = n
	}
	return cmdPost("/v1/ui/wait", body)
}

func cmdChain(a *args) error {
	if err := a.need(1, "chain '<steps-json>'"); err != nil {
		return err
	}
	var steps []any
	if err := json.Unmarshal([]byte(strings.Join(a.pos, " ")), &steps); err != nil {
		return fmt.Errorf("steps must be a JSON array: %w", err)
	}
	return cmdPost("/v1/ui/chain", map[string]any{"steps": steps})
}

func cmdWaitResult(a *args) error {
	text := strings.Join(a.pos, " ")
	rid := a.optStr("id", "")
	if text == "" && rid == "" {
		return fmt.Errorf("usage: sextant wait-result TEXT | wait-result --id RID [--absent] [--screenshot]")
	}
	body := map[string]any{"absent": a.has("absent"), "screenshot": a.has("screenshot")}
	if text != "" {
		body["text"] = text
	}
	if rid != "" {
		body["resource_id"] = rid
	}
	if n, err := a.optInt("timeout-ms", 10000); err == nil {
		body["timeout_ms"] = n
	}
	return cmdPost("/v1/ui/wait_result", body)
}

func cmdAct(a *args) error {
	target := map[string]any{}
	if v := a.optStr("text", ""); v != "" {
		target["text"] = v
	}
	if v := a.optStr("id", ""); v != "" {
		target["resource_id"] = v
	}
	if v := a.optStr("node", ""); v != "" {
		target["node_id"] = v
	}
	if len(target) == 0 {
		return fmt.Errorf("usage: sextant act --text T | --id RID | --node NODE_ID [--action click|select|longpress|set_text]")
	}
	action := a.optStr("action", "click")
	body := map[string]any{"target": target, "action": action}
	if v := a.optStr("value", ""); v != "" {
		body["text"] = v
	}
	if a.has("expect-selected") {
		body["expect"] = map[string]any{"selected": true}
	}
	if a.has("allow-danger") {
		body["allow_danger"] = true
	}
	if n, err := a.optInt("verify-ms", 0); err == nil && n > 0 {
		body["verify_ms"] = n
	}
	// precheck is a recipe-level concept; accept --precheck-selected as the
	// common "only click when this control is in the expected state" case.
	if a.has("precheck-selected") {
		body["precheck"] = map[string]any{"selected": true}
	}
	if n, err := a.optInt("duration-ms", 0); err == nil && n > 0 {
		body["duration_ms"] = n
	}
	if seed := a.optStr("seed", ""); seed != "" {
		body["seed"] = seed
	}
	return cmdPost("/v1/ui/act", body)
}

func cmdList(a *args) error {
	body := map[string]any{}
	if v := a.optStr("text-contains", ""); v != "" {
		body["text_contains"] = v
	}
	if v := a.optStr("rid", ""); v != "" {
		body["resource_id"] = v
	}
	if v := a.optStr("class", ""); v != "" {
		body["class"] = v
	}
	if n, err := a.optInt("max", 100); err == nil {
		body["max_items"] = n
	}
	if v := a.optStr("dedupe", ""); v != "" {
		body["dedupe_by"] = strings.Split(v, ",")
	}
	if n, err := a.optInt("scroll", 0); err == nil && n > 0 {
		scroll := map[string]any{"max_scrolls": n}
		if u, err := a.optInt("until", 0); err == nil && u > 0 {
			scroll["until_count"] = u
		}
		body["scroll"] = scroll
	}
	if len(body) == 0 {
		return fmt.Errorf("usage: sextant list --text-contains T | --rid RID | --class C [--max N] [--scroll N]")
	}
	return cmdPost("/v1/ui/list", body)
}

func cmdOptions(a *args) error {
	body := map[string]any{}
	if v := a.optStr("group", ""); v != "" {
		body["group"] = v
	}
	return cmdPost("/v1/ui/options", body)
}

func cmdReadback(a *args) error {
	body := map[string]any{}
	if text := strings.Join(a.pos, " "); text != "" {
		body["text_contains"] = text
	}
	if v := a.optStr("rid", ""); v != "" {
		body["resource_id"] = v
	}
	if len(body) == 0 {
		return fmt.Errorf("usage: sextant readback TEXT | readback --rid RID [--scope tree|ocr]")
	}
	if v := a.optStr("scope", ""); v != "" {
		body["scope"] = v
	}
	return cmdPost("/v1/ui/readback", body)
}

func cmdKeepAwake(a *args) error {
	if err := a.need(1, "keep-awake on|off [--timeout-ms N]"); err != nil {
		return err
	}
	on := strings.EqualFold(a.pos[0], "on") || a.pos[0] == "1" || strings.EqualFold(a.pos[0], "true")
	body := map[string]any{"enabled": on}
	if n, err := a.optInt("timeout-ms", 0); err == nil && n > 0 {
		body["timeout_ms"] = n
	}
	return cmdPost("/v1/device/keep-awake", body)
}

func cmdWaitStable(a *args) error {
	body := map[string]any{}
	if n, err := a.optInt("timeout-ms", 5000); err == nil {
		body["timeout_ms"] = n
	}
	if n, err := a.optInt("stable-ms", 300); err == nil {
		body["stable_ms"] = n
	}
	if v := a.optStr("scope", ""); v != "" {
		body["scope"] = v
	}
	return cmdPost("/v1/ui/wait_stable", body)
}

// cmdRun executes a JSON recipe (D29). The recipe is data, not a scripting
// language: preconditions, linear steps with per-step expectations, and an
// explicit stop/continue policy. Business logic stays in the recipe; the daemon
// only sees primitives.
func cmdRun(a *args) error {
	if err := a.need(1, "run recipe.json [--resume RUN_DIR]"); err != nil {
		return err
	}
	path := a.pos[0]
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	var rec recipe
	if err := json.Unmarshal(data, &rec); err != nil {
		return fmt.Errorf("recipe is not valid JSON: %w", err)
	}
	if len(rec.Steps) == 0 {
		return fmt.Errorf("recipe has no steps")
	}
	if rec.OnFailure == "" {
		rec.OnFailure = "stop"
	}
	if rec.OnFailure != "stop" && rec.OnFailure != "continue" {
		return fmt.Errorf("on_failure must be stop or continue")
	}

	resume := a.optStr("resume", "")
	var runDir string
	var st runState
	if resume != "" {
		runDir = resume
		st, err = loadRunState(runDir)
		if err != nil {
			return fmt.Errorf("resume %s: %w", runDir, err)
		}
	} else {
		runDir, err = newRunDir(rec.Name)
		if err != nil {
			return err
		}
		st = runState{Name: rec.Name, Recipe: path, StartedAt: time.Now().UTC().Format(time.RFC3339)}
		if err := saveRunState(runDir, st); err != nil {
			return err
		}
	}
	// D30: one seed for the whole run. A seed the caller supplied wins; otherwise
	// the run records the one it generated, so a failure can be replayed exactly.
	seed := a.optStr("seed", "")
	if seed == "" {
		seed = st.Seed
	}
	if seed == "" {
		seed = newRunSeed()
	}
	st.Seed = seed
	if err := saveRunState(runDir, st); err != nil {
		return err
	}
	if err := runRiskPreflight(rec, a.has("force")); err != nil {
		return err
	}
	fmt.Printf("run %s (%d preconditions, %d steps) seed=%s\n", rec.Name, len(rec.Preconditions), len(rec.Steps), seed)
	fmt.Printf("state: %s\n", runDir)

	if rec.KeepAwake {
		if c, err := paired(); err == nil {
			if _, err := c.Post("/v1/device/keep-awake", map[string]any{"enabled": true}); err == nil {
				defer func() {
					_, _ = c.Post("/v1/device/keep-awake", map[string]any{"enabled": false})
				}()
			}
		}
	}
	runStep := func(step recipeStep, phase string) error {
		if runStepDone(st, step.ID) {
			fmt.Printf("skip %-24s (already done)\n", step.ID)
			return nil
		}
		st.Current = step.ID
		_ = saveRunState(runDir, st)
		err := runRecipeStepChecked(step, seed)
		if err != nil {
			st.Failed = append(st.Failed, step.ID)
			st.Current = ""
			st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
			_ = saveRunState(runDir, st)
			fmt.Printf("FAIL %-24s %v\n", step.ID, err)
			fmt.Printf("resume: sextant run %s --resume %s\n", path, runDir)
			return fmt.Errorf("%s %q failed: %w", phase, step.ID, err)
		}
		st.Done = append(st.Done, step.ID)
		st.Failed = removeRunFailed(st.Failed, step.ID)
		st.Current = ""
		st.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		_ = saveRunState(runDir, st)
		return nil
	}
	for i, step := range rec.Preconditions {
		if step.ID == "" {
			step.ID = "precondition-" + strconv.Itoa(i+1)
		}
		if err := runStep(step, "precondition"); err != nil {
			return err
		}
	}
	failed := 0
	for i, step := range rec.Steps {
		if step.ID == "" {
			step.ID = "step-" + strconv.Itoa(i+1)
		}
		if err := runStep(step, "step"); err != nil {
			failed++
			if rec.OnFailure == "stop" {
				return err
			}
		}
	}
	if failed > 0 {
		return fmt.Errorf("%d step(s) failed (on_failure=continue)", failed)
	}
	fmt.Println("recipe complete")
	return nil
}

type runState struct {
	Name      string `json:"name"`
	Recipe    string `json:"recipe"`
	StartedAt string `json:"started_at"`
	UpdatedAt string `json:"updated_at"`
	// Seed is the D30 run seed. Recorded so a run can be replayed exactly and
	// so --resume keeps the same points.
	Seed    string   `json:"seed,omitempty"`
	Current string   `json:"current,omitempty"`
	Done    []string `json:"done"`
	Failed  []string `json:"failed"`
}

func newRunSeed() string {
	var raw [8]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return fmt.Sprintf("t%x", time.Now().UnixNano())
	}
	return hex.EncodeToString(raw[:])
}

// runRiskPreflight refuses to walk into a wall that has stopped earlier runs.
//
// One challenge is a warning: the app may be fine today. Three or more is a
// pattern, and repeating it is what makes an account look worse; the caller has
// to say --force. The endpoint is read-only and exposes no screen content.
func runRiskPreflight(rec recipe, force bool) error {
	c, err := paired()
	if err != nil {
		return err
	}
	path := "/v1/challenge/risk"
	if rec.Package != "" {
		path += "?package=" + url.QueryEscape(rec.Package)
	}
	raw, err := c.Get(path)
	if err != nil {
		return fmt.Errorf("challenge preflight: %w", err)
	}
	var out struct {
		Active   bool   `json:"active"`
		Cooling  bool   `json:"cooling"`
		Count    int    `json:"count"`
		Describe string `json:"describe"`
		Entries  map[string]struct {
			Cooling  bool   `json:"cooling"`
			Count    int    `json:"count"`
			Describe string `json:"describe"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return fmt.Errorf("challenge preflight: %w", err)
	}
	serious := 0
	cooling := 0
	var notes []string
	if rec.Package != "" {
		if out.Cooling {
			cooling++
		}
		if out.Active && out.Count > 0 {
			if out.Describe != "" {
				notes = append(notes, out.Describe)
			}
			if out.Count >= 3 {
				serious++
			}
		}
	} else {
		for _, entry := range out.Entries {
			if entry.Cooling {
				cooling++
			}
			if entry.Count <= 0 {
				continue
			}
			if entry.Describe != "" {
				notes = append(notes, entry.Describe)
			}
			if entry.Count >= 3 {
				serious++
			}
		}
	}
	for _, note := range notes {
		fmt.Fprintf(os.Stderr, "warning: %s\n", note)
	}
	if !force {
		if cooling > 0 {
			return fmt.Errorf("challenge cooldown: %d app(s) answered with a verification wall moments ago; wait for the cooldown or pass --force", cooling)
		}
		if serious > 0 {
			return fmt.Errorf("challenge risk: %d app(s) have three or more verification walls on record; pass --force to run anyway", serious)
		}
	}
	return nil
}

func newRunDir(name string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	slug := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '-', r == '_':
			return r
		default:
			return '-'
		}
	}, name)
	if slug == "" || slug == "-" {
		slug = "run"
	}
	dir := filepath.Join(home, ".sextant", "runs", time.Now().Format("20060102-150405")+"-"+slug)
	return dir, os.MkdirAll(dir, 0o700)
}

func loadRunState(dir string) (runState, error) {
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		return runState{}, err
	}
	var st runState
	if err := json.Unmarshal(data, &st); err != nil {
		return runState{}, err
	}
	return st, nil
}

func saveRunState(dir string, st runState) error {
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	path := filepath.Join(dir, "state.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func runStepDone(st runState, id string) bool {
	for _, done := range st.Done {
		if done == id {
			return true
		}
	}
	return false
}

func removeRunFailed(failed []string, id string) []string {
	out := failed[:0]
	for _, f := range failed {
		if f != id {
			out = append(out, f)
		}
	}
	return out
}

type recipe struct {
	Name string `json:"name"`
	// Package names the app this recipe drives. It is optional, and only used for
	// the D30 challenge preflight: without it the run checks every live record.
	Package       string       `json:"package"`
	Preconditions []recipeStep `json:"preconditions"`
	Steps         []recipeStep `json:"steps"`
	OnFailure     string       `json:"on_failure"`
	// KeepAwake holds the helper's wake lock for the duration of the recipe and
	// releases it on exit, including failures. Long browsing tasks otherwise lose
	// the screen to the display timeout halfway through.
	KeepAwake bool `json:"keep_awake"`
}

// runRecipeStepChecked runs one step and enforces its expectation. A step that
// dispatches but whose expectation is false is a failure, never a pass (D29).
// seed makes every injection in the run reproducible (D30).
func runRecipeStepChecked(step recipeStep, seed string) error {
	start := time.Now()
	resp, err := runRecipeStep(step, seed)
	ms := time.Since(start).Milliseconds()
	if err != nil {
		return err
	}
	if step.Expect != nil {
		if ok, detail := recipeExpect(resp, step.Expect); !ok {
			return fmt.Errorf("expectation not met: %s", detail)
		}
	}
	fmt.Printf("ok   %-24s %5dms\n", step.ID, ms)
	return nil
}

type recipeStep struct {
	ID     string         `json:"id"`
	Action string         `json:"action"` // page|observe|act|list|options|readback|wait|wait_stable
	Params map[string]any `json:"params"`
	Expect map[string]any `json:"expect"`
	Verify int            `json:"verify_ms"`
}

func runRecipeStep(step recipeStep, seed string) (map[string]any, error) {
	// D31: a map navigation runs locally through the same executor as
	// `sextant map nav`; it is not a daemon endpoint.
	if step.Action == "map_nav" {
		return mapNavFromRecipe(step.Params)
	}
	path := ""
	switch step.Action {
	case "page":
		path = "/v1/ui/page"
	case "observe":
		path = "/v1/ui/observe"
	case "act":
		path = "/v1/ui/act"
	case "list":
		path = "/v1/ui/list"
	case "options":
		path = "/v1/ui/options"
	case "readback":
		path = "/v1/ui/readback"
	case "wait":
		path = "/v1/ui/wait"
	case "wait_stable":
		path = "/v1/ui/wait_stable"
	case "challenge":
		path = "/v1/ui/challenge"
	case "challenge_solve":
		path = "/v1/ui/challenge/solve"
	default:
		return nil, fmt.Errorf("unknown action %q", step.Action)
	}
	body := map[string]any{}
	for k, v := range step.Params {
		body[k] = v
	}
	if step.Expect != nil && step.Action == "act" {
		body["expect"] = step.Expect
	}
	if step.Verify > 0 && step.Action == "act" {
		body["verify_ms"] = step.Verify
	}
	// Only the injection primitives take a seed; the recognizing ones ignore it.
	if step.Action == "act" || step.Action == "challenge_solve" {
		body["seed"] = seed
	}
	c, err := paired()
	if err != nil {
		return nil, err
	}
	headers := map[string]string{"X-Sextant-Request-Id": seed + ":" + step.ID}
	var raw []byte
	if step.Action == "page" {
		raw, err = c.Get(path)
	} else if step.Action == "act" || step.Action == "challenge_solve" {
		raw, err = c.DoWithHeaders("POST", path, body, headers)
	} else {
		raw, err = c.Post(path, body)
	}
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, nil
	}
	return out, nil
}

func recipeExpect(resp, expect map[string]any) (bool, string) {
	if want, ok := expect["ok"].(bool); ok {
		got, _ := resp["ok"].(bool)
		return got == want, fmt.Sprintf("ok=%v, want %v", resp["ok"], want)
	}
	if want, ok := expect["verified"].(bool); ok {
		got, _ := resp["verified"].(bool)
		return got == want, fmt.Sprintf("verified=%v, want %v", resp["verified"], want)
	}
	if want, ok := expect["found"].(bool); ok {
		got, _ := resp["found"].(bool)
		return got == want, fmt.Sprintf("found=%v, want %v", resp["found"], want)
	}
	if want, ok := expect["stable"].(bool); ok {
		got, _ := resp["stable"].(bool)
		return got == want, fmt.Sprintf("stable=%v, want %v", resp["stable"], want)
	}
	if want, ok := expect["foreground"].(string); ok {
		got, _ := resp["package"].(string)
		if got == "" {
			got, _ = resp["component"].(string)
		}
		return strings.Contains(got, want), fmt.Sprintf("foreground=%q, want contains %q", got, want)
	}
	return true, ""
}
