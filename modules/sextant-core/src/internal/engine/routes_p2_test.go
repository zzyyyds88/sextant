package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"sextant-core/internal/audit"
	"sextant-core/internal/config"
	"sextant-core/internal/sysprobe"
	"sextant-core/internal/ui"
)

// p2NodeSource is a fixed tree source for D33 handler tests. The tree is small
// and hand-built here; real-device trees live in the CLI's appmap testdata.
type p2NodeSource struct{ snapshot ui.Snapshot }

func (f p2NodeSource) Name() string { return "faketree" }
func (f p2NodeSource) Nodes(context.Context, int) ([]ui.Node, error) {
	return f.snapshot.Nodes, nil
}
func (f p2NodeSource) Snapshot(context.Context, int) (ui.Snapshot, error) {
	return f.snapshot, nil
}

func p2Server(nodes []ui.Node) *Server {
	return &Server{
		Cfg:  config.Config{Limits: config.Limits{MaxBodyBytes: 1 << 20, RequestTimeoutSec: 30}},
		Tree: ui.TreeService{Helper: p2NodeSource{snapshot: ui.Snapshot{Nodes: nodes, Source: "faketree"}}, HelperBusy: func(context.Context) bool { return true }},
	}
}

func p2Nodes(selected string) []ui.Node {
	return []ui.Node{
		{Attrs: map[string]string{"class": "android.widget.TextView", "text": "第一项", "resource-id": "app:id/title", "selected": selected}},
		{Attrs: map[string]string{"class": "android.widget.TextView", "text": "第二项", "resource-id": "app:id/title", "selected": "false"}},
	}
}

func TestListReturnsPageFingerprint(t *testing.T) {
	s := p2Server(p2Nodes("false"))
	req := httptest.NewRequest("POST", "/v1/ui/list", strings.NewReader(`{"class":"android.widget.TextView","max_items":5}`))
	rec := httptest.NewRecorder()
	s.handleList(rec, req)
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Count           int    `json:"count"`
		PageFingerprint string `json:"page_fingerprint"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Count != 2 {
		t.Fatalf("count = %d, want 2", out.Count)
	}
	want := pageFingerprint(p2Nodes("false"))
	if out.PageFingerprint != want {
		t.Fatalf("page_fingerprint = %q, want %q", out.PageFingerprint, want)
	}
}

// The fingerprint must follow the selected tab, not just the text.
func TestListPageFingerprintTracksSelected(t *testing.T) {
	s := p2Server(p2Nodes("true"))
	req := httptest.NewRequest("POST", "/v1/ui/list", strings.NewReader(`{"class":"android.widget.TextView","max_items":5}`))
	rec := httptest.NewRecorder()
	s.handleList(rec, req)
	var out struct {
		PageFingerprint string `json:"page_fingerprint"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.PageFingerprint == "" || out.PageFingerprint == pageFingerprint(p2Nodes("false")) {
		t.Fatalf("fingerprint did not follow selected: %q", out.PageFingerprint)
	}
}

type countingRunner struct {
	calls int
	out   string
}

func (r *countingRunner) Run(context.Context, string, ...string) (string, error) {
	r.calls++
	return r.out, nil
}

func TestAppVersionIsCached(t *testing.T) {
	r := &countingRunner{out: "  versionName=14.0.1\n"}
	s := &Server{Probe: sysprobe.Probe{Run: r}}
	if got := s.appVersion(context.Background(), "com.android.settings"); got != "14.0.1" {
		t.Fatalf("version = %q", got)
	}
	if got := s.appVersion(context.Background(), "com.android.settings"); got != "14.0.1" {
		t.Fatalf("second read = %q", got)
	}
	if r.calls != 1 {
		t.Fatalf("dumpsys ran %d times, want 1 (30s cache)", r.calls)
	}
}

func TestAppVersionUnknownStaysEmpty(t *testing.T) {
	s := &Server{Probe: sysprobe.Probe{Run: &countingRunner{out: "no version here\n"}}}
	if got := s.appVersion(context.Background(), "com.android.settings"); got != "" {
		t.Fatalf("version = %q, want empty", got)
	}
	if got := s.appVersion(context.Background(), ""); got != "" {
		t.Fatalf("empty package = %q", got)
	}
}

func TestObserveTreeIsFlatAndDiffable(t *testing.T) {
	// The helper payload shape: nested tree. observe must publish it flat with
	// node_id/parent_node_id and cache it under the returned frame_id.
	raw := map[string]any{"tree": []any{
		map[string]any{"class": "root", "bounds": map[string]any{"left": 0, "top": 0, "right": 100, "bottom": 100},
			"children": []any{
				map[string]any{"class": "TextView", "text": "one", "bounds": map[string]any{"left": 0, "top": 0, "right": 50, "bottom": 50}},
				map[string]any{"class": "TextView", "text": "two", "bounds": map[string]any{"left": 50, "top": 0, "right": 100, "bottom": 50}},
			}},
	}}
	nodes, truncated, err := ui.NodesFromHelper(raw)
	if err != nil {
		t.Fatal(err)
	}
	flat := ui.Flatten(nodes)
	if len(flat) != 3 || truncated {
		t.Fatalf("flat = %d nodes, truncated=%v", len(flat), truncated)
	}
	if flat[1].ParentNodeID != "n_0" || flat[1].Path != "0/0" {
		t.Fatalf("child = %+v", flat[1])
	}
	wire := flatNodeJSON(flat[1])
	if wire["parent_node_id"] != "n_0" || wire["path"] != "0/0" {
		t.Fatalf("wire = %+v", wire)
	}
}

func TestDiffRejectsUnknownFrame(t *testing.T) {
	s := &Server{Cfg: config.Config{Limits: config.Limits{MaxBodyBytes: 1 << 20}},
		Audit: audit.Open(filepath.Join(t.TempDir(), "core.log"), 1<<20, 100)}
	rec := httptest.NewRecorder()
	s.handleDiff(rec, httptest.NewRequest("POST", "/v1/ui/diff", strings.NewReader(`{"before_frame_id":"f_missing"}`)))
	if rec.Code != 404 {
		t.Fatalf("status %d, want 404: %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "frame_not_found") {
		t.Fatalf("body = %s", rec.Body.String())
	}
}

func TestDiffRequiresBeforeFrame(t *testing.T) {
	s := &Server{Cfg: config.Config{Limits: config.Limits{MaxBodyBytes: 1 << 20}},
		Audit: audit.Open(filepath.Join(t.TempDir(), "core.log"), 1<<20, 100)}
	rec := httptest.NewRecorder()
	s.handleDiff(rec, httptest.NewRequest("POST", "/v1/ui/diff", strings.NewReader(`{}`)))
	if rec.Code != 400 {
		t.Fatalf("status %d, want 400", rec.Code)
	}
}

func TestDiffComparesTwoCachedFrames(t *testing.T) {
	s := &Server{Cfg: config.Config{Limits: config.Limits{MaxBodyBytes: 1 << 20}},
		Audit: audit.Open(filepath.Join(t.TempDir(), "core.log"), 1<<20, 100)}
	before := ui.Flatten(p2Nodes("false"))
	after := ui.Flatten(p2Nodes("true"))
	s.treeFrames.put("f_a", before, time.Now())
	s.treeFrames.put("f_b", after, time.Now())
	rec := httptest.NewRecorder()
	s.handleDiff(rec, httptest.NewRequest("POST", "/v1/ui/diff",
		strings.NewReader(`{"before_frame_id":"f_a","after_frame_id":"f_b"}`)))
	if rec.Code != 200 {
		t.Fatalf("status %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Changed []struct {
			NodeID string `json:"node_id"`
			Fields map[string]struct {
				Before string `json:"before"`
				After  string `json:"after"`
			} `json:"fields"`
		} `json:"changed"`
		Counts struct {
			Changed int `json:"changed"`
		} `json:"counts"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatal(err)
	}
	if out.Counts.Changed != 1 || len(out.Changed) != 1 {
		t.Fatalf("changed = %+v", out)
	}
	if got := out.Changed[0].Fields["selected"].After; got != "true" {
		t.Fatalf("selected after = %q", got)
	}
}

func TestTreeFrameCacheEvictsAndExpires(t *testing.T) {
	var c treeFrames
	now := time.Now()
	for i := 0; i < treeFrameMax+3; i++ {
		c.put(fmt.Sprintf("f_%d", i), ui.Flatten(p2Nodes("false")), now)
	}
	if _, ok := c.get("f_0", now); ok {
		t.Fatal("oldest frame was not evicted")
	}
	if _, ok := c.get("f_10", now); !ok {
		t.Fatal("newest frame is missing")
	}
	if _, ok := c.get("f_10", now.Add(treeFrameTTL+time.Second)); ok {
		t.Fatal("expired frame was still returned")
	}
}
