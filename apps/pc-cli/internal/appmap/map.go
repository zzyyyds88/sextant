// Package appmap is the App Navigation Map: a compiled description of the
// screens of one app and the paths between them.
//
// Portions of the model and the matching rules are derived from PhoneGo
// (https://github.com/HKUDS/PhoneGo), MIT License,
// Copyright (c) 2024 xuyifan-0731 and 2025 yangqin-jiang.
//
// The layer is deliberately dumb: it identifies the current screen, derives a
// catalogue of known operations, and replays a known path. It never calls a
// model and never decides what the user wants; the agent on the computer does
// that from the catalogue.
package appmap

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"unicode"

	"chopin/internal/uiapi"
	"chopin/third_party/yaml"
)

// MinCoverage is the smallest share of a screen's anchors a dump must contain
// before it can be called that screen. Below it, "no match" is the honest answer.
const MinCoverage = 0.5

// MaxDepth bounds operation derivation; PhoneGo uses the same value.
const MaxDepth = 6

// Element is one labelled control on a screen.
type Element struct {
	Text string `yaml:"text"`
	// Center is normalised to [0,1] so a map survives a resolution change.
	Center        [2]float64 `yaml:"center"`
	LeadsTo       string     `yaml:"leads_to,omitempty"`
	FoundAtScroll int        `yaml:"found_at_scroll,omitempty"`
	Fixed         bool       `yaml:"fixed,omitempty"`
	Aliases       []string   `yaml:"aliases,omitempty"`
	SemanticType  string     `yaml:"semantic_type,omitempty"`
	// Dynamic marks content rather than chrome. It may be explored but must not
	// be the evidence a screen match rests on.
	Dynamic  bool `yaml:"dynamic,omitempty"`
	Redacted bool `yaml:"redacted,omitempty"`
}

// Screen is one captured page.
type Screen struct {
	ID       string    `yaml:"id"`
	Elements []Element `yaml:"elements"`
	// Selected is the tab labels the app marked selected when captured. Two tabs
	// of a feed are otherwise identical.
	Selected []string `yaml:"selected,omitempty"`
	// Fingerprint is the pageFingerprint of the frame this screen was captured
	// from (D33). It is evidence for a drift warning, not a replay precondition:
	// dynamic text changes it without changing what the screen is.
	Fingerprint string `yaml:"fingerprint,omitempty"`
	// AppVersion is the target package's versionName at capture time. A version
	// change is the coarser signal that a map may be stale.
	AppVersion      string `yaml:"app_version,omitempty"`
	Description     string `yaml:"description,omitempty"`
	Package         string `yaml:"package,omitempty"`
	Scrollable      bool   `yaml:"scrollable,omitempty"`
	ScrollDirection string `yaml:"scroll_direction,omitempty"`
}

// Step is one replayable action. Coordinates are in screen pixels.
type Step struct {
	Action    string `yaml:"action"`
	Package   string `yaml:"package,omitempty"`
	Component string `yaml:"component,omitempty"`
	X         int    `yaml:"x"`
	Y         int    `yaml:"y"`
	X1        int    `yaml:"x1"`
	Y1        int    `yaml:"y1"`
	X2        int    `yaml:"x2"`
	Y2        int    `yaml:"y2"`
	// Duration is milliseconds (PhoneGo's `duration`).
	Duration int `yaml:"duration,omitempty"`
	// Wait is seconds (PhoneGo's `wait`), because the map format is PhoneGo's.
	Wait float64 `yaml:"wait,omitempty"`
	Text string  `yaml:"text,omitempty"`
	Key  string  `yaml:"key,omitempty"`
	// Screen is the screen a tap was recorded on. It is evidence for a warning,
	// never a reason to abort: the identifier is not stable enough for that.
	Screen string `yaml:"screen,omitempty"`
}

// Operation is one entry of the derived catalogue.
type Operation struct {
	ID           string
	Description  string
	Macro        []Step
	Type         string // NAV or ACT
	TargetScreen string
}

// Bench records how this map scored when its own operations were replayed.
type Bench struct {
	Ops     int    `yaml:"ops"`
	Exact   int    `yaml:"exact"`
	SameApp int    `yaml:"same_app"`
	At      string `yaml:"at,omitempty"`
}

// Coverage describes what a crawl reached and what it left behind.
type Coverage struct {
	Screens    int    `yaml:"screens"`
	Queued     int    `yaml:"queued"`
	DepthLimit int    `yaml:"depth_limit"`
	Stopped    string `yaml:"stopped,omitempty"`
	Complete   bool   `yaml:"complete"`
}

// Map is one app's compiled map.
type Map struct {
	App              string            `yaml:"app"`
	Package          string            `yaml:"package"`
	ScreenW          int               `yaml:"screen_w"`
	ScreenH          int               `yaml:"screen_h"`
	Display          *int              `yaml:"display,omitempty"`
	LaunchIntent     string            `yaml:"launch_intent,omitempty"`
	Screens          []Screen          `yaml:"screens"`
	ScreenMacros     map[string][]Step `yaml:"screen_macros"`
	CrawledAt        string            `yaml:"crawled_at,omitempty"`
	Bench            Bench             `yaml:"bench,omitempty"`
	Coverage         Coverage          `yaml:"coverage,omitempty"`
	KnownLimitations []string          `yaml:"known_limitations,omitempty"`

	rarity map[string]int
}

// Load reads a map file.
func Load(path string) (*Map, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var m Map
	if err := yaml.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("map %s: %w", path, err)
	}
	if len(m.Screens) == 0 {
		return nil, fmt.Errorf("map %s has no screens", path)
	}
	if m.ScreenW <= 0 || m.ScreenH <= 0 {
		return nil, fmt.Errorf("map %s has no screen size", path)
	}
	return &m, nil
}

// DefaultDir is where maps live on the computer.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".chopin", "maps"), nil
}

// Save writes the map atomically (tmp + rename) with 0600.
func (m *Map) Save(path string) error {
	if path == "" {
		dir, err := DefaultDir()
		if err != nil {
			return err
		}
		path = filepath.Join(dir, m.Package+".yaml")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	data, err := yaml.Marshal(m)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// Screen returns one screen by id.
func (m *Map) Screen(id string) *Screen {
	for i := range m.Screens {
		if m.Screens[i].ID == id {
			return &m.Screens[i]
		}
	}
	return nil
}

// Macro returns the cold-start path to a screen.
func (m *Map) Macro(id string) []Step {
	return append([]Step(nil), m.ScreenMacros[id]...)
}

// Identify matches a captured tree against the map.
//
// Rules, all of them measured in PhoneGo:
//   - anchors are labels visible without scrolling, because a replay dump shows
//     page 0 while the map also stores elements found after swipes;
//   - dynamic content and rows found only after scrolling are not evidence;
//   - a different selected tab is a different screen, whatever else the two
//     share;
//   - rarity only breaks ties: a label on every screen says nothing.
func (m *Map) Identify(nodes []uiapi.Node, pkg string) (string, float64) {
	screen, score := m.IdentifyScreen(nodes, pkg)
	if screen == nil {
		return "", score
	}
	return screen.ID, score
}

// IdentifyScreen is Identify plus the matched Screen, so a caller can compare
// the fingerprint and app version it recorded at capture time (D33).
func (m *Map) IdentifyScreen(nodes []uiapi.Node, pkg string) (*Screen, float64) {
	if len(nodes) == 0 {
		return nil, 0
	}
	texts := map[string]bool{}
	tabs := map[string]bool{}
	for _, n := range nodes {
		if label := n.Label(); len(label) > 1 {
			texts[label] = true
		}
		if n.Selected == uiapi.TriTrue {
			if label := n.Label(); label != "" {
				tabs[label] = true
				texts[label] = true
			}
		}
	}
	if len(texts) == 0 {
		// A dump with no labels can still be matched if the map has none either;
		// in practice it means the tree is not readable and saying "unknown" is
		// the only honest answer.
		return nil, 0
	}

	m.buildRarity()
	best := (*Screen)(nil)
	bestOverlap, bestCoverage, bestRarity := 0, 0.0, 0.0
	for i := range m.Screens {
		screen := &m.Screens[i]
		owner := screen.Package
		if owner == "" {
			owner = m.Package
		}
		if pkg != "" && owner != pkg {
			continue
		}
		if len(tabs) > 0 && len(screen.Selected) > 0 && !intersects(tabs, screen.Selected) {
			continue
		}
		anchors := screenTexts(screen)
		if len(anchors) == 0 {
			continue
		}
		overlap := 0
		rarity := 0.0
		for text := range texts {
			if anchors[text] {
				overlap++
				rarity += 1.0 / float64(maxInt(1, m.rarity[text]))
			}
		}
		coverage := float64(overlap) / float64(len(anchors))
		rankBetter := overlap > bestOverlap ||
			(overlap == bestOverlap && round6(coverage) > round6(bestCoverage)) ||
			(overlap == bestOverlap && round6(coverage) == round6(bestCoverage) && rarity > bestRarity)
		if coverage >= MinCoverage && rankBetter {
			best, bestOverlap, bestCoverage, bestRarity = screen, overlap, coverage, rarity
		}
	}
	if best == nil {
		return nil, bestCoverage
	}
	return best, bestCoverage
}

// Drift reports how a matched screen differs from the frame being identified
// (D33). It is evidence, not a veto: map matching rests on text overlap, and a
// fingerprint changes whenever dynamic content changes. An unknown value on
// either side is never reported as a mismatch.
type Drift struct {
	Drift          bool   `json:"drift"`
	VersionChanged bool   `json:"version_changed"`
	Want           string `json:"want_fingerprint,omitempty"`
	Got            string `json:"got_fingerprint,omitempty"`
	WantVersion    string `json:"want_app_version,omitempty"`
	GotVersion     string `json:"got_app_version,omitempty"`
}

// CompareDrift builds the drift report for a screen matched against a page.
func CompareDrift(screen *Screen, page uiapi.Page) Drift {
	d := Drift{Got: page.Fingerprint, GotVersion: page.AppVersion}
	if screen == nil {
		return d
	}
	d.Want, d.WantVersion = screen.Fingerprint, screen.AppVersion
	if screen.Fingerprint != "" && page.Fingerprint != "" && screen.Fingerprint != page.Fingerprint {
		d.Drift = true
	}
	if screen.AppVersion != "" && page.AppVersion != "" && screen.AppVersion != page.AppVersion {
		d.VersionChanged = true
	}
	return d
}

// Warn renders a one-line warning, or "" when the map still matches.
func (d Drift) Warn(screenID string) string {
	if d.VersionChanged {
		return fmt.Sprintf("map drift: screen %s was captured on version %s, the device has %s", screenID, d.WantVersion, d.GotVersion)
	}
	if d.Drift {
		return fmt.Sprintf("map drift: screen %s fingerprint changed (%s -> %s)", screenID, d.Want, d.Got)
	}
	return ""
}

func (m *Map) buildRarity() {
	if m.rarity != nil {
		return
	}
	m.rarity = map[string]int{}
	for i := range m.Screens {
		for text := range screenTexts(&m.Screens[i]) {
			m.rarity[text]++
		}
	}
}

// screenTexts is the anchor set of a screen with the same fallbacks PhoneGo
// uses: a screen made only of content still has to be identifiable.
func screenTexts(screen *Screen) map[string]bool {
	add := func(include func(Element) bool) map[string]bool {
		out := map[string]bool{}
		for _, e := range screen.Elements {
			if e.Text != "" && include(e) {
				out[e.Text] = true
			}
		}
		return out
	}
	if out := add(func(e Element) bool { return e.FoundAtScroll == 0 && !e.Redacted && !e.Dynamic }); len(out) > 0 {
		for _, tab := range screen.Selected {
			out[tab] = true
		}
		return out
	}
	if out := add(func(e Element) bool { return !e.Redacted && !e.Dynamic }); len(out) > 0 {
		return out
	}
	if out := add(func(e Element) bool { return !e.Redacted }); len(out) > 0 {
		return out
	}
	return add(func(Element) bool { return true })
}

// BuildOperations derives the catalogue by walking the screen graph from
// screen_0. Deterministic: the same map yields the same order and ids.
func (m *Map) BuildOperations() []Operation {
	var ops []Operation
	visitedScreens := map[string]bool{}
	visitedOps := map[string]bool{}

	var explore func(screenID string, prefix []string, depth int)
	explore = func(screenID string, prefix []string, depth int) {
		if depth > MaxDepth || visitedScreens[screenID] {
			return
		}
		visitedScreens[screenID] = true
		screen := m.Screen(screenID)
		if screen == nil {
			return
		}
		base := m.Macro(screenID)
		midX := m.ScreenW / 2
		fromY := int(float64(m.ScreenH) * 0.7)
		toY := int(float64(m.ScreenH) * 0.2)
		for _, element := range screen.Elements {
			if element.Redacted || element.Text == "" || skip(element.Text) {
				continue
			}
			x := int(math.Round(element.Center[0] * float64(m.ScreenW)))
			y := int(math.Round(element.Center[1] * float64(m.ScreenH)))
			macro := append([]Step(nil), base...)
			for i := 0; i < element.FoundAtScroll; i++ {
				macro = append(macro, Step{Action: "swipe", X1: midX, Y1: fromY, X2: midX, Y2: toY, Duration: 400, Wait: 0.5})
			}
			macro = append(macro, Step{Action: "tap", X: x, Y: y, Wait: 1.0, Text: element.Text, Screen: screenID})
			texts := append(append([]string(nil), prefix...), element.Text)
			id := opID(texts)
			if id == "" || visitedOps[id] {
				continue
			}
			visitedOps[id] = true
			op := Operation{ID: id, Description: strings.Join(texts, " → "), Macro: macro, Type: "ACT"}
			if element.LeadsTo != "" {
				op.Type = "NAV"
				op.TargetScreen = element.LeadsTo
			}
			ops = append(ops, op)
			if element.LeadsTo != "" {
				explore(element.LeadsTo, texts, depth+1)
			}
		}
	}
	explore("screen_0", nil, 0)
	return ops
}

// RelativeMacro is the path from one screen to another.
//
// The relative form is only valid when the current screen lies ON the target's
// cold-start path: then the shared prefix can be skipped and the remainder
// replayed from here. If it does not (the caller navigated elsewhere), stripping
// the prefix would replay the target's tail from the wrong screen — measured:
// asking for 声音与触感 while on the WLAN page replayed its tap against the WLAN
// list and stayed there. In that case the full path is returned, which starts
// with force_stop/launch and is correct from anywhere.
func (m *Map) RelativeMacro(fromID, toID string) []Step {
	to := m.ScreenMacros[toID]
	if len(to) == 0 {
		return nil
	}
	from := m.ScreenMacros[fromID]
	if len(from) > 0 && len(from) <= len(to) {
		prefix := true
		for i := range from {
			if from[i] != to[i] {
				prefix = false
				break
			}
		}
		if prefix {
			return append([]Step(nil), to[len(from):]...)
		}
	}
	return append([]Step(nil), to...)
}

// Catalog renders the operation list for a caller that has no model in this
// process. navOnly keeps just the navigation entries.
func (m *Map) Catalog(navOnly bool) string {
	ops := m.BuildOperations()
	var b strings.Builder
	n := 0
	for _, op := range ops {
		if navOnly && op.Type != "NAV" {
			continue
		}
		n++
		fmt.Fprintf(&b, "[%s] %s — %s\n", op.Type, op.ID, op.Description)
	}
	if n == 0 {
		return ""
	}
	return fmt.Sprintf("%s (%s) — %d operations\n%s", m.App, m.Package, n, b.String())
}

func opID(texts []string) string {
	collapsed := make([]string, 0, len(texts))
	for _, text := range texts {
		if len(collapsed) == 0 || collapsed[len(collapsed)-1] != text {
			collapsed = append(collapsed, text)
		}
	}
	joined := strings.ToLower(strings.Join(collapsed, "_"))
	var b strings.Builder
	for _, r := range joined {
		switch {
		case unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := strings.Trim(b.String(), "_")
	if len(out) > 60 {
		out = out[:60]
	}
	return out
}

var noise = []string{
	"profile picture", "navigate up", "more options", "dismiss", "clear",
	"learn more", "back", "cancel", "no unused", "no recent", "no nearby",
}

func skip(text string) bool {
	trimmed := strings.TrimSpace(text)
	if len(trimmed) <= 1 && isASCII(trimmed) {
		return true
	}
	lower := strings.ToLower(trimmed)
	for _, word := range noise {
		if strings.Contains(lower, word) {
			return true
		}
	}
	return false
}

func isASCII(s string) bool {
	for _, r := range s {
		if r > 127 {
			return false
		}
	}
	return true
}

func intersects(tabs map[string]bool, list []string) bool {
	for _, text := range list {
		if tabs[text] {
			return true
		}
	}
	return false
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func round6(v float64) float64 { return math.Round(v*1e6) / 1e6 }

// SortedOps returns operations ordered by id, for stable output in tests.
func SortedOps(ops []Operation) []Operation {
	out := append([]Operation(nil), ops...)
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}
