package appmap

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"

	"sextant/internal/uiapi"
)

// fixture is a tree captured from the real device (Settings main page,
// 1080x2400). Tests must not invent trees: the labels and bounds here are what
// the daemon actually returned.
func fixture(t *testing.T) []uiapi.Node {
	t.Helper()
	data, err := os.ReadFile("testdata/settings-tree.json")
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		Nodes []uiapi.Node `json:"nodes"`
	}
	if err := json.Unmarshal(data, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Nodes) == 0 {
		t.Fatal("fixture has no nodes")
	}
	return out.Nodes
}

func elementsFrom(nodes []uiapi.Node, w, h int) []Element {
	var out []Element
	seen := map[string]bool{}
	for _, n := range nodes {
		label := n.Label()
		if len(label) < 2 || seen[label] || n.Width() < 20 || n.Height() < 20 {
			continue
		}
		seen[label] = true
		cx, cy := n.Center()
		out = append(out, Element{
			Text:   label,
			Center: [2]float64{float64(cx) / float64(w), float64(cy) / float64(h)},
		})
	}
	return out
}

func fixtureMap(nodes []uiapi.Node) *Map {
	return &Map{
		App:     "Settings",
		Package: "com.android.settings",
		ScreenW: 1080,
		ScreenH: 2400,
		Screens: []Screen{{ID: "screen_0", Elements: elementsFrom(nodes, 1080, 2400)}},
	}
}

func TestIdentifyRealSettingsTree(t *testing.T) {
	nodes := fixture(t)
	m := fixtureMap(nodes)
	id, confidence := m.Identify(nodes, "com.android.settings")
	if id != "screen_0" {
		t.Fatalf("want screen_0, got %q (confidence %.2f)", id, confidence)
	}
	if confidence < MinCoverage {
		t.Fatalf("confidence %.2f below %.2f", confidence, MinCoverage)
	}
}

func TestIdentifyIsStableAcrossRepeats(t *testing.T) {
	nodes := fixture(t)
	m := fixtureMap(nodes)
	first, _ := m.Identify(nodes, "")
	for i := 0; i < 10; i++ {
		if id, _ := m.Identify(nodes, ""); id != first {
			t.Fatalf("identification flipped: %q then %q", first, id)
		}
	}
}

func TestIdentifyRejectsAForeignPackage(t *testing.T) {
	nodes := fixture(t)
	m := fixtureMap(nodes)
	if id, _ := m.Identify(nodes, "com.example.other"); id != "" {
		t.Fatalf("a foreign package must not match, got %q", id)
	}
}

func TestIdentifyNeedsEnoughEvidence(t *testing.T) {
	nodes := fixture(t)
	m := fixtureMap(nodes)
	// One label out of hundreds is not the screen.
	tiny := []uiapi.Node{{Text: "设置", Bounds: [4]int{0, 0, 100, 50}}}
	if id, _ := m.Identify(tiny, ""); id != "" {
		t.Fatalf("one shared label must not identify a screen, got %q", id)
	}
}

func TestSelectedTabDisambiguates(t *testing.T) {
	m := &Map{
		App: "Clock", Package: "com.android.deskclock", ScreenW: 100, ScreenH: 100,
		Screens: []Screen{
			{ID: "screen_alarm", Selected: []string{"闹钟"}, Elements: []Element{
				{Text: "闹钟", Center: [2]float64{0.1, 0.1}},
				{Text: "计时", Center: [2]float64{0.3, 0.1}},
				{Text: "秒表", Center: [2]float64{0.5, 0.1}},
			}},
			{ID: "screen_timer", Selected: []string{"计时"}, Elements: []Element{
				{Text: "闹钟", Center: [2]float64{0.1, 0.1}},
				{Text: "计时", Center: [2]float64{0.3, 0.1}},
				{Text: "秒表", Center: [2]float64{0.5, 0.1}},
			}},
		},
	}
	dump := []uiapi.Node{
		{Text: "闹钟", Selected: uiapi.TriFalse},
		{Text: "计时", Selected: uiapi.TriTrue},
		{Text: "秒表", Selected: uiapi.TriFalse},
	}
	id, _ := m.Identify(dump, "")
	if id != "screen_timer" {
		t.Fatalf("want screen_timer, got %q", id)
	}
}

func TestDeriveOperations(t *testing.T) {
	m := &Map{
		App: "Settings", Package: "com.android.settings", ScreenW: 1000, ScreenH: 2000,
		Screens: []Screen{
			{ID: "screen_0", Elements: []Element{
				{Text: "WLAN", Center: [2]float64{0.3, 0.2}, LeadsTo: "screen_1"},
				{Text: "蓝牙", Center: [2]float64{0.3, 0.3}},
				{Text: "新建", Center: [2]float64{0.3, 0.4}},
				{Text: "x", Center: [2]float64{0.1, 0.1}},
			}},
			{ID: "screen_1", Elements: []Element{
				{Text: "WLAN 设置", Center: [2]float64{0.3, 0.2}},
			}},
		},
		ScreenMacros: map[string][]Step{
			"screen_0": {{Action: "force_stop", Package: "com.android.settings"}, {Action: "launch", Package: "com.android.settings"}},
			"screen_1": {{Action: "force_stop", Package: "com.android.settings"}, {Action: "launch", Package: "com.android.settings"}, {Action: "tap", X: 300, Y: 400, Text: "WLAN"}},
		},
	}
	ops := m.BuildOperations()
	byID := map[string]Operation{}
	for _, op := range ops {
		byID[op.ID] = op
	}
	nav, ok := byID["wlan"]
	if !ok || nav.Type != "NAV" || nav.TargetScreen != "screen_1" {
		t.Fatalf("want NAV wlan → screen_1, got %+v", byID)
	}
	if last := nav.Macro[len(nav.Macro)-1]; last.Action != "tap" || last.Text != "WLAN" {
		t.Fatalf("macro must end with the text-anchored tap, got %+v", last)
	}
	act, ok := byID["蓝牙"]
	if !ok || act.Type != "ACT" {
		t.Fatalf("want ACT 蓝牙, got %+v", act)
	}
	if _, ok := byID["新建"]; !ok {
		// Discovery keeps consequential labels; the crawler, not the model, decides
		// whether to tap them.
		t.Fatal("operation derivation should not drop a label")
	}
	if _, ok := byID["x"]; ok {
		t.Fatal("a single ASCII character is noise")
	}
	child, ok := byID["wlan_wlan_设置"]
	if !ok {
		t.Fatalf("child operation should exist, got %v", keysOf(byID))
	}
	if child.Type != "ACT" {
		t.Fatalf("child without leads_to is ACT, got %s", child.Type)
	}
}

func TestRelativeMacroStripsTheCommonPrefix(t *testing.T) {
	boot := []Step{{Action: "force_stop"}, {Action: "launch"}}
	m := &Map{
		ScreenW: 100, ScreenH: 100,
		ScreenMacros: map[string][]Step{
			"screen_0": boot,
			"screen_1": append(append([]Step(nil), boot...), Step{Action: "tap", Text: "WLAN"}),
			"screen_2": append(append([]Step(nil), boot...), Step{Action: "tap", Text: "WLAN"}, Step{Action: "tap", Text: "高级"}),
		},
	}
	rel := m.RelativeMacro("screen_1", "screen_2")
	if len(rel) != 1 || rel[0].Text != "高级" {
		t.Fatalf("want one step to 高级, got %+v", rel)
	}
	if got := m.RelativeMacro("", "screen_1"); len(got) != 3 {
		t.Fatalf("unknown origin should return the full path, got %+v", got)
	}
	if got := m.RelativeMacro("screen_2", "screen_1"); len(got) != 3 {
		t.Fatalf("an origin off the target path must fall back to the full path, got %+v", got)
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/settings.yaml"
	m := &Map{
		App: "Settings", Package: "com.android.settings", ScreenW: 1080, ScreenH: 2400,
		Screens: []Screen{{ID: "screen_0", Elements: []Element{{Text: "WLAN", Center: [2]float64{0.3, 0.2}}}}},
		ScreenMacros: map[string][]Step{
			"screen_0": {{Action: "tap", X: 300, Y: 400, Text: "WLAN"}},
		},
	}
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Screens, m.Screens) {
		t.Fatalf("screens changed: %+v", got.Screens)
	}
	if !reflect.DeepEqual(got.ScreenMacros["screen_0"], m.ScreenMacros["screen_0"]) {
		t.Fatalf("macros changed: %+v", got.ScreenMacros)
	}
}

func TestLoadPhoneGoShapedYAML(t *testing.T) {
	// The shape PhoneGo writes: block sequences, flow-style center, wait in
	// seconds. If this stops parsing, the map format has drifted.
	const doc = `app: Settings
package: com.android.settings
screen_w: 1440
screen_h: 3120
screens:
- id: screen_0
  elements:
  - text: WLAN
    center: [0.3, 0.2]
    leads_to: screen_1
  - text: 蓝牙
    center: [0.3, 0.3]
- id: screen_1
  elements:
  - text: WLAN 设置
    center: [0.3, 0.2]
screen_macros:
  screen_0:
  - action: launch
    package: com.android.settings
    wait: 3.0
  screen_1:
  - action: launch
    package: com.android.settings
    wait: 3.0
  - action: tap
    x: 432
    y: 624
    wait: 1.0
    text: WLAN
`
	path := t.TempDir() + "/phonego.yaml"
	if err := os.WriteFile(path, []byte(doc), 0o600); err != nil {
		t.Fatal(err)
	}
	m, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Screens) != 2 || m.ScreenW != 1440 {
		t.Fatalf("unexpected map: %+v", m)
	}
	if got := m.Macro("screen_1"); len(got) != 2 || got[1].Wait != 1.0 || got[1].Text != "WLAN" {
		t.Fatalf("macro did not parse: %+v", got)
	}
	ops := m.BuildOperations()
	if len(ops) == 0 || ops[0].Type != "NAV" || ops[0].TargetScreen != "screen_1" {
		t.Fatalf("operations not derived: %+v", ops)
	}
}

func TestLoadRejectsAnEmptyMap(t *testing.T) {
	path := t.TempDir() + "/empty.json"
	if err := os.WriteFile(path, []byte(`{"app":"x","package":"y","screens":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Fatal("a map without screens must not load")
	}
}

func keysOf(m map[string]Operation) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
