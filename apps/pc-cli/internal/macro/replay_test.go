package macro

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"sextant/internal/appmap"
	"sextant/internal/uiapi"
)

// fakeDoer answers the endpoints a replay touches and records the calls, so a
// test can assert which channel was used.
type fakeDoer struct {
	calls  []string
	page   string
	tree   string
	act    string
	failOn string
}

func (f *fakeDoer) Get(path string) ([]byte, error) {
	f.calls = append(f.calls, "GET "+path)
	switch {
	case strings.HasPrefix(path, "/v1/ui/page"):
		return []byte(f.page), nil
	case strings.HasPrefix(path, "/v1/ui/tree"):
		return []byte(f.tree), nil
	}
	return []byte(`{"ok":true}`), nil
}

func (f *fakeDoer) Post(path string, body any) ([]byte, error) {
	f.calls = append(f.calls, "POST "+path)
	if f.failOn != "" && path == f.failOn {
		return nil, fmt.Errorf("boom")
	}
	if path == "/v1/ui/act" {
		return []byte(f.act), nil
	}
	return []byte(`{"ok":true}`), nil
}

func (f *fakeDoer) Put(path string, body any) ([]byte, error) {
	f.calls = append(f.calls, "PUT "+path)
	return []byte(`{"ok":true}`), nil
}

func newFake() *fakeDoer {
	return &fakeDoer{
		page: `{"package":"com.android.settings","component":"com.android.settings/.MainSettings"}`,
		tree: `{"nodes":[{"node_id":"n_0","bounds":[0,0,1080,2400],"text":"设置"}]}`,
		act:  `{"ok":true,"channel":"a11y","verified":"unknown"}`,
	}
}

func (f *fakeDoer) called(prefix string) bool {
	for _, call := range f.calls {
		if strings.HasPrefix(call, prefix) {
			return true
		}
	}
	return false
}

func TestTextTapUsesTheAccessibilityChannel(t *testing.T) {
	f := newFake()
	d := uiapi.New(f)
	res := Run(context.Background(), d, []appmap.Step{
		{Action: "launch", Package: "com.android.settings"},
		{Action: "tap", X: 300, Y: 400, Text: "WLAN"},
	}, Options{Sleep: func(time.Duration) {}})
	if !res.OK {
		t.Fatalf("replay failed: %s", res.Error)
	}
	if !f.called("POST /v1/ui/act") {
		t.Fatalf("text tap should use act: %v", f.calls)
	}
	if f.called("POST /v1/input/tap") {
		t.Fatalf("text tap must not fall back to coordinates when act worked: %v", f.calls)
	}
}

func TestTextTapFallsBackToCoordinates(t *testing.T) {
	f := newFake()
	f.failOn = "/v1/ui/act"
	d := uiapi.New(f)
	res := Run(context.Background(), d, []appmap.Step{
		{Action: "tap", X: 300, Y: 400, Text: "WLAN"},
	}, Options{Sleep: func(time.Duration) {}})
	if !res.OK {
		t.Fatalf("replay failed: %s", res.Error)
	}
	if !f.called("POST /v1/input/tap") {
		t.Fatalf("a failed act must fall back to the coordinate: %v", f.calls)
	}
}

func TestPlainTapUsesCoordinates(t *testing.T) {
	f := newFake()
	d := uiapi.New(f)
	res := Run(context.Background(), d, []appmap.Step{{Action: "tap", X: 10, Y: 20}}, Options{})
	if !res.OK {
		t.Fatal(res.Error)
	}
	if f.called("POST /v1/ui/act") {
		t.Fatalf("a plain tap must not call act: %v", f.calls)
	}
	if !f.called("POST /v1/input/tap") {
		t.Fatalf("a plain tap must inject a coordinate: %v", f.calls)
	}
}

func TestScreenMismatchWarnsByDefault(t *testing.T) {
	f := newFake()
	d := uiapi.New(f)
	var warnings []string
	res := Run(context.Background(), d, []appmap.Step{
		{Action: "tap", X: 10, Y: 20, Screen: "screen_0"},
	}, Options{
		Identify: func(nodes []uiapi.Node, pkg string) (string, float64) { return "screen_9", 0.9 },
		Warn:     func(m string) { warnings = append(warnings, m) },
	})
	if !res.OK {
		t.Fatalf("a mismatch must not abort by default: %s", res.Error)
	}
	if len(res.Warnings) != 1 || !strings.Contains(res.Warnings[0], "screen_9") {
		t.Fatalf("want one warning naming the landed screen, got %v", res.Warnings)
	}
}

func TestScreenMismatchAbortsWhenStrict(t *testing.T) {
	f := newFake()
	d := uiapi.New(f)
	res := Run(context.Background(), d, []appmap.Step{
		{Action: "tap", X: 10, Y: 20, Screen: "screen_0"},
	}, Options{
		StrictScreen: true,
		Identify:     func(nodes []uiapi.Node, pkg string) (string, float64) { return "screen_9", 0.9 },
	})
	if res.OK {
		t.Fatal("strict replay must abort on a mismatch")
	}
}

func TestWaitStepSleeps(t *testing.T) {
	f := newFake()
	d := uiapi.New(f)
	var slept int
	res := Run(context.Background(), d, []appmap.Step{
		{Action: "wait", Duration: 250},
	}, Options{Sleep: func(d time.Duration) { slept += int(d) }})
	if !res.OK {
		t.Fatal(res.Error)
	}
	if slept == 0 {
		t.Fatal("wait step should sleep")
	}
}

func TestUnknownActionIsAnError(t *testing.T) {
	f := newFake()
	d := uiapi.New(f)
	res := Run(context.Background(), d, []appmap.Step{{Action: "teleport"}}, Options{})
	if res.OK {
		t.Fatal("unknown action must fail")
	}
}

// The fixture tree is the daemon's shape; a replay must parse it.
func TestTreeFixtureParses(t *testing.T) {
	var out struct {
		Nodes []uiapi.Node `json:"nodes"`
	}
	if err := json.Unmarshal([]byte(newFake().tree), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Nodes) != 1 || out.Nodes[0].Width() != 1080 {
		t.Fatalf("unexpected node: %+v", out.Nodes)
	}
}
