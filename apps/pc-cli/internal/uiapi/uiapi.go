// Package uiapi is the typed device driver the map layer runs on.
//
// It speaks only the daemon's existing HTTP primitives; it holds no analysis
// state. Keeping it separate from appmap means the map model and its algorithms
// can be tested against captured trees without a device.
package uiapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// Tri is a tri-state attribute: "true", "false", or "unknown" (the producer did
// not say). It preserves the distinction the daemon makes instead of collapsing
// it to a bool.
type Tri string

const (
	TriTrue    Tri = "true"
	TriFalse   Tri = "false"
	TriUnknown Tri = "unknown"
)

// UnmarshalJSON accepts the daemon's bool-or-string encoding.
func (t *Tri) UnmarshalJSON(b []byte) error {
	s := strings.Trim(string(bytes.TrimSpace(b)), `"`)
	switch s {
	case "true":
		*t = TriTrue
	case "false":
		*t = TriFalse
	default:
		*t = TriUnknown
	}
	return nil
}

// Node is one accessibility node as /v1/ui/tree returns it.
type Node struct {
	NodeID      string `json:"node_id"`
	Parent      int    `json:"parent"`
	Depth       int    `json:"depth"`
	Text        string `json:"text"`
	ResourceID  string `json:"resource_id"`
	Class       string `json:"class"`
	Package     string `json:"package"`
	ContentDesc string `json:"content_desc"`
	Clickable   Tri    `json:"clickable"`
	Enabled     Tri    `json:"enabled"`
	Scrollable  Tri    `json:"scrollable"`
	Checked     Tri    `json:"checked"`
	Editable    Tri    `json:"editable"`
	Selected    Tri    `json:"selected"`
	Visible     Tri    `json:"visible"`
	Bounds      [4]int `json:"bounds"`
}

// Label is the readable text of a node (text first, then content-desc).
func (n Node) Label() string {
	if t := strings.TrimSpace(n.Text); t != "" {
		return t
	}
	return strings.TrimSpace(n.ContentDesc)
}

// Center is the midpoint of the node's bounds.
func (n Node) Center() (int, int) {
	return (n.Bounds[0] + n.Bounds[2]) / 2, (n.Bounds[1] + n.Bounds[3]) / 2
}

// Width and Height are the node's box dimensions.
func (n Node) Width() int  { return n.Bounds[2] - n.Bounds[0] }
func (n Node) Height() int { return n.Bounds[3] - n.Bounds[1] }

// Page is the foreground summary from /v1/ui/page.
type Page struct {
	Package     string `json:"package"`
	Component   string `json:"component"`
	Fingerprint string `json:"fingerprint"`
	AppVersion  string `json:"app_version"`
	NodeCount   int    `json:"node_count"`
}

// Doer is the tiny slice of the HTTP client this package needs.
type Doer interface {
	Get(path string) ([]byte, error)
	Post(path string, body any) ([]byte, error)
	Put(path string, body any) ([]byte, error)
}

// Driver wraps a Doer with the primitives the map layer uses.
type Driver struct {
	C Doer
}

// New builds a driver.
func New(c Doer) *Driver { return &Driver{C: c} }

// ScreenSize returns the native screen size from a capture.
//
// The tree cannot answer this: node 0 may be the 35px status bar, and a
// scrollable container can extend past the screen (measured: max bounds gave
// 1934x2280 on a 1080x2400 panel). The capture metadata is the only value that
// is the screen.
func (d *Driver) ScreenSize(ctx context.Context) (int, int, error) {
	raw, err := d.C.Get("/v1/ui/screenshot")
	if err != nil {
		return 0, 0, err
	}
	var out struct {
		Width  int `json:"width"`
		Height int `json:"height"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return 0, 0, err
	}
	if out.Width <= 0 || out.Height <= 0 {
		return 0, 0, fmt.Errorf("capture reported no size")
	}
	return out.Width, out.Height, nil
}

// Page reads the foreground package and page fingerprint.
func (d *Driver) Page(ctx context.Context) (Page, error) {
	raw, err := d.C.Get("/v1/ui/page")
	if err != nil {
		return Page{}, err
	}
	var page Page
	if err := json.Unmarshal(raw, &page); err != nil {
		return Page{}, fmt.Errorf("page: %w", err)
	}
	return page, nil
}

// Tree reads the current accessibility tree. summary=1 keeps the decision
// fields and drops the rest; the map layer never needs window ids or focus
// flags.
func (d *Driver) Tree(ctx context.Context) ([]Node, error) {
	raw, err := d.C.Get("/v1/ui/tree?summary=1")
	if err != nil {
		return nil, err
	}
	var out struct {
		Nodes []Node `json:"nodes"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("tree: %w", err)
	}
	return out.Nodes, nil
}

// WaitStable blocks until the tree stops changing, up to timeoutMS.
func (d *Driver) WaitStable(ctx context.Context, timeoutMS, stableMS int) (map[string]any, error) {
	raw, err := d.C.Post("/v1/ui/wait_stable", map[string]any{
		"timeout_ms": timeoutMS, "stable_ms": stableMS, "scope": "tree",
	})
	if err != nil {
		return nil, err
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out, nil
}

// Act posts a verified action.
func (d *Driver) Act(ctx context.Context, target map[string]any, action, text string, durationMS, verifyMS int, expect map[string]any) (map[string]any, error) {
	body := map[string]any{"target": target, "action": action}
	if text != "" {
		body["text"] = text
	}
	if durationMS > 0 {
		body["duration_ms"] = durationMS
	}
	if verifyMS > 0 {
		body["verify_ms"] = verifyMS
	}
	if len(expect) > 0 {
		body["expect"] = expect
	}
	raw, err := d.C.Post("/v1/ui/act", body)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	_ = json.Unmarshal(raw, &out)
	return out, nil
}

// Tap injects a tap.
func (d *Driver) Tap(ctx context.Context, x, y int) error {
	return d.post("/v1/input/tap", map[string]any{"x": x, "y": y})
}

// Swipe injects a gesture.
func (d *Driver) Swipe(ctx context.Context, x1, y1, x2, y2, durationMS int) error {
	return d.post("/v1/input/swipe", map[string]any{
		"x1": x1, "y1": y1, "x2": x2, "y2": y2, "duration_ms": durationMS,
	})
}

// LongPress injects a long press.
func (d *Driver) LongPress(ctx context.Context, x, y, durationMS int) error {
	return d.post("/v1/input/longpress", map[string]any{"x": x, "y": y, "duration_ms": durationMS})
}

// Text types into the focused field.
func (d *Driver) Text(ctx context.Context, text string) error {
	return d.post("/v1/input/text", map[string]any{"text": text})
}

// Key sends a key event.
func (d *Driver) Key(ctx context.Context, name string) error {
	return d.post("/v1/input/key", map[string]any{"key": name})
}

// Global runs a global action (back/home/recents/...).
func (d *Driver) Global(ctx context.Context, action string) error {
	return d.post("/v1/input/global", map[string]any{"action": action})
}

// Launch opens a package, optionally clearing its task.
func (d *Driver) Launch(ctx context.Context, pkg string, clearTask bool) error {
	return d.post("/v1/apps/launch", map[string]any{"package": pkg, "clear_task": clearTask})
}

// Stop force-stops a package.
func (d *Driver) Stop(ctx context.Context, pkg string) error {
	return d.post("/v1/apps/stop", map[string]any{"package": pkg})
}

// SettingGet reads one settings key.
func (d *Driver) SettingGet(ctx context.Context, ns, key string) (string, error) {
	raw, err := d.C.Get("/v1/settings/" + ns + "/" + key)
	if err != nil {
		return "", err
	}
	var out struct {
		Value string `json:"value"`
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return "", err
	}
	return out.Value, nil
}

// SettingPut writes one settings key and relies on the daemon's read-back.
func (d *Driver) SettingPut(ctx context.Context, ns, key, value string) error {
	raw, err := d.C.Put("/v1/settings/"+ns+"/"+key, map[string]any{"value": value})
	_ = raw
	return err
}

func (d *Driver) post(path string, body any) error {
	_, err := d.C.Post(path, body)
	return err
}
