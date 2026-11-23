package engine

import (
	"context"
	"fmt"
	"hash/fnv"
	"net/http"
	"sort"
	"strings"
	"time"

	"chopin-core/internal/httpx"
	"chopin-core/internal/ui"
)

// The generic UI primitives from D29. They answer questions every task needs —
// what is on this page, what are the options, did my action land, where am I —
// without encoding any particular app's business logic.

// ---------------------------------------------------------------------------
// list

type listRequest struct {
	FrameID      string   `json:"frame_id"`
	TextContains string   `json:"text_contains"`
	ResourceID   string   `json:"resource_id"`
	Class        string   `json:"class"`
	Region       []int    `json:"region"`
	DedupeBy     []string `json:"dedupe_by"`
	MaxItems     int      `json:"max_items"`
	Scroll       *struct {
		MaxScrolls int `json:"max_scrolls"`
		UntilCount int `json:"until_count"`
	} `json:"scroll"`
}

func (s *Server) handleList(w http.ResponseWriter, r *http.Request) {
	var body listRequest
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.TextContains == "" && body.ResourceID == "" && body.Class == "" && len(body.Region) != 4 {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"give at least one of text_contains, resource_id, class, region", nil)
		return
	}
	maxItems := body.MaxItems
	if maxItems <= 0 || maxItems > 500 {
		maxItems = 100
	}
	ctx, cancel := contextWithTimeout(r, 120*time.Second)
	defer cancel()

	seen := map[string]bool{}
	items := make([]map[string]any, 0, maxItems)
	scrolls := 0
	// The fingerprint of the last frame the list was read from. D29 requires it
	// alongside the items so a caller can pin which page produced them.
	pageFP := ""
	for {
		snap, err := s.Tree.Snapshot(ctx, 0)
		if err != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "ui dump failed", err)
			return
		}
		pageFP = pageFingerprint(snap.Nodes)
		for _, n := range snap.Nodes {
			if !matchListItem(n, body) {
				continue
			}
			key := listDedupeKey(n, body.DedupeBy)
			if seen[key] {
				continue
			}
			seen[key] = true
			items = append(items, primitiveNode(n))
			if len(items) >= maxItems {
				break
			}
		}
		if body.Scroll == nil || scrolls >= body.Scroll.MaxScrolls || len(items) >= maxItems {
			break
		}
		if body.Scroll.UntilCount > 0 && len(items) >= body.Scroll.UntilCount {
			break
		}
		if !s.scrollOnce(ctx, snap.Nodes, body.Region) {
			break
		}
		scrolls++
		s.waitTreeStable(ctx, 250, 3000)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok": true, "items": items, "count": len(items),
		"truncated": len(items) >= maxItems, "scrolls": scrolls,
		"page_fingerprint": pageFP,
	})
}

func matchListItem(n ui.Node, body listRequest) bool {
	if n.Attrs["text"] == "" && n.Attrs["resource-id"] == "" {
		return false
	}
	if n.Attrs["visible"] == "false" {
		return false
	}
	if body.TextContains != "" {
		if !strings.Contains(n.Attrs["text"], body.TextContains) &&
			!strings.Contains(n.Attrs["content-desc"], body.TextContains) {
			return false
		}
	}
	if body.ResourceID != "" && !strings.Contains(n.Attrs["resource-id"], body.ResourceID) {
		return false
	}
	if body.Class != "" && !strings.Contains(n.Attrs["class"], body.Class) {
		return false
	}
	if len(body.Region) == 4 && !insideRegion(n, body.Region) {
		return false
	}
	return true
}

func insideRegion(n ui.Node, region []int) bool {
	b := n.Normalized()
	x, y := n.Center()
	return x >= region[0] && x <= region[2] && y >= region[1] && y <= region[3] &&
		b[2] > b[0] && b[3] > b[1]
}

func listDedupeKey(n ui.Node, fields []string) string {
	if len(fields) == 0 {
		fields = []string{"text"}
	}
	parts := make([]string, 0, len(fields))
	for _, f := range fields {
		if f == "bounds" {
			parts = append(parts, fmt.Sprintf("%v", n.Bounds))
			continue
		}
		parts = append(parts, n.Attrs[f])
	}
	return strings.Join(parts, "|")
}

func primitiveNode(n ui.Node) map[string]any {
	var parent any
	if n.Parent >= 0 {
		parent = fmt.Sprintf("n_%d", n.Parent)
	}
	return map[string]any{
		"node_id":        fmt.Sprintf("n_%d", n.Index),
		"text":           n.Attrs["text"],
		"resource_id":    n.Attrs["resource-id"],
		"class":          n.Attrs["class"],
		"content_desc":   n.Attrs["content-desc"],
		"bounds":         n.Bounds,
		"selected":       triState(n.Attrs["selected"]),
		"visible":        triState(n.Attrs["visible"]),
		"enabled":        triState(n.Attrs["enabled"]),
		"clickable":      triState(n.Attrs["clickable"]),
		"depth":          n.Depth,
		"parent_node_id": parent,
	}
}

func (s *Server) scrollOnce(ctx context.Context, nodes []ui.Node, region []int) bool {
	var x, y1, y2 int
	if len(region) == 4 {
		x = (region[0] + region[2]) / 2
		y1 = region[3] - (region[3]-region[1])/5
		y2 = region[1] + (region[3]-region[1])/5
	} else if len(nodes) > 0 {
		b := nodes[0].Normalized()
		x = (b[0] + b[2]) / 2
		y1 = b[3] - (b[3]-b[1])/4
		y2 = b[1] + (b[3]-b[1])/4
	} else {
		return false
	}
	if err := s.Device.Swipe(ctx, x, y1, x, y2, 400, 0); err != nil {
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// options

type optionsRequest struct {
	FrameID string `json:"frame_id"`
	Group   string `json:"group"`
	Region  []int  `json:"region"`
}

func (s *Server) handleOptions(w http.ResponseWriter, r *http.Request) {
	var body optionsRequest
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	snap, err := s.Tree.Snapshot(r.Context(), 0)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "ui dump failed", err)
		return
	}
	byIndex := make(map[int]ui.Node, len(snap.Nodes))
	for _, n := range snap.Nodes {
		byIndex[n.Index] = n
	}
	type group struct {
		label    string
		options  []ui.Node
		inferred bool
	}
	var groups []group

	// Preferred: the caller names the group ("鞋码", "颜色分类", "语言"). Collect
	// the clickable labels below that title until the next title-like element.
	if body.Group != "" {
		if opts := optionsUnderTitle(snap.Nodes, body.Group, body.Region); len(opts) > 0 {
			groups = append(groups, group{label: body.Group, options: opts})
		}
	}

	// Next: siblings under one parent (menus, tab bars, settings radios).
	if len(groups) == 0 {
		grouped := map[int][]ui.Node{}
		for _, n := range snap.Nodes {
			if isOptionNode(n, body.Region) {
				grouped[n.Parent] = append(grouped[n.Parent], n)
			}
		}
		for parent, opts := range grouped {
			if len(opts) < 2 {
				continue
			}
			label := ""
			if p, ok := byIndex[parent]; ok {
				label = p.Attrs["text"]
				if label == "" {
					label = p.Attrs["content-desc"]
				}
			}
			groups = append(groups, group{label: label, options: opts})
		}
	}

	// Last resort: SKU grids often put every option in its own container, so the
	// parent is not shared. Return every clickable label as one inferred group;
	// the caller can still address an option by text. Marked inferred because the
	// grouping is a guess, not a fact from the tree.
	if len(groups) == 0 {
		var opts []ui.Node
		for _, n := range snap.Nodes {
			if isOptionNode(n, body.Region) {
				opts = append(opts, n)
			}
		}
		if len(opts) > 0 {
			groups = append(groups, group{label: "", options: opts, inferred: true})
		}
	}

	for i := range groups {
		sort.SliceStable(groups[i].options, func(a, b int) bool {
			if groups[i].options[a].Bounds[1] != groups[i].options[b].Bounds[1] {
				return groups[i].options[a].Bounds[1] < groups[i].options[b].Bounds[1]
			}
			return groups[i].options[a].Bounds[0] < groups[i].options[b].Bounds[0]
		})
	}
	sort.SliceStable(groups, func(i, j int) bool {
		return groups[i].options[0].Bounds[1] < groups[j].options[0].Bounds[1]
	})
	out := make([]map[string]any, 0, len(groups))
	for _, g := range groups {
		opts := make([]map[string]any, 0, len(g.options))
		for _, n := range g.options {
			opts = append(opts, map[string]any{
				"node_id":  fmt.Sprintf("n_%d", n.Index),
				"text":     n.Attrs["text"],
				"bounds":   n.Bounds,
				"selected": triState(n.Attrs["selected"]),
				"enabled":  triState(n.Attrs["enabled"]),
				"visible":  triState(n.Attrs["visible"]),
			})
		}
		out = append(out, map[string]any{
			"label": g.label, "options": opts, "count": len(opts), "inferred": g.inferred,
		})
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok": true, "groups": out, "count": len(out),
		"source": snap.Source, "truncated": snap.Truncated,
	})
}

// optionsUnderTitle collects clickable labels below the first title containing
// want. A title's own row is excluded; the bottom is the screen edge, so noisy
// trailing buttons may be included and are marked inferred by the caller.
func optionsUnderTitle(nodes []ui.Node, want string, region []int) []ui.Node {
	var title *ui.Node
	for i := range nodes {
		n := nodes[i]
		if !strings.Contains(n.Attrs["text"], want) && !strings.Contains(n.Attrs["content-desc"], want) {
			continue
		}
		if title == nil || n.Bounds[1] < title.Bounds[1] {
			title = &nodes[i]
		}
	}
	if title == nil {
		return nil
	}
	bottom := 0
	if len(nodes) > 0 {
		b := nodes[0].Normalized()
		bottom = b[3]
	}
	var opts []ui.Node
	for _, n := range nodes {
		if n.Index == title.Index || !isOptionNode(n, region) {
			continue
		}
		cy := (n.Bounds[1] + n.Bounds[3]) / 2
		if cy <= title.Bounds[3] || cy > bottom {
			continue
		}
		opts = append(opts, n)
	}
	return opts
}

func isOptionNode(n ui.Node, region []int) bool {
	if n.Attrs["text"] == "" || n.Attrs["clickable"] != "true" {
		return false
	}
	if n.Attrs["visible"] == "false" || n.Attrs["enabled"] == "false" {
		return false
	}
	b := n.Normalized()
	if b[2] <= b[0] || b[3] <= b[1] {
		return false
	}
	if len(region) == 4 && !insideRegion(n, region) {
		return false
	}
	return true
}

// ---------------------------------------------------------------------------
// readback

type readbackRequest struct {
	FrameID      string `json:"frame_id"`
	TextContains string `json:"text_contains"`
	ResourceID   string `json:"resource_id"`
	Scope        string `json:"scope"` // tree | ocr
}

func (s *Server) handleReadback(w http.ResponseWriter, r *http.Request) {
	var body readbackRequest
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.TextContains == "" && body.ResourceID == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"text_contains or resource_id is required", nil)
		return
	}
	scope := body.Scope
	if scope == "" {
		scope = "tree"
	}
	matches := []map[string]any{}
	switch scope {
	case "tree":
		snap, err := s.Tree.Snapshot(r.Context(), 0)
		if err != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "ui dump failed", err)
			return
		}
		for _, n := range snap.Nodes {
			if body.TextContains != "" &&
				!strings.Contains(n.Attrs["text"], body.TextContains) &&
				!strings.Contains(n.Attrs["content-desc"], body.TextContains) {
				continue
			}
			if body.ResourceID != "" && !strings.Contains(n.Attrs["resource-id"], body.ResourceID) {
				continue
			}
			matches = append(matches, primitiveNode(n))
			if len(matches) >= 20 {
				break
			}
		}
	case "ocr":
		restore, err := s.hideOverlayForCapture(r.Context())
		if err != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeNotReady,
				"cannot hide the overlay, so OCR would read the agent's own controls", err)
			return
		}
		if restore != nil {
			defer restore()
		}
		out, ocrErr := s.Helper.OCR(r.Context(), 0, nil)
		if ocrErr != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "OCR unavailable", ocrErr)
			return
		}
		if lines, ok := out["lines"].([]any); ok {
			for _, item := range lines {
				line, ok := item.(map[string]any)
				if !ok {
					continue
				}
				text, _ := line["text"].(string)
				if body.TextContains != "" && !strings.Contains(text, body.TextContains) {
					continue
				}
				matches = append(matches, map[string]any{
					"text": text, "bounds": line["bounds"],
				})
				if len(matches) >= 20 {
					break
				}
			}
		}
	default:
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"scope must be tree or ocr", nil)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok": true, "found": len(matches) > 0, "matches": matches,
		"scope": scope, "count": len(matches),
	})
}

// ---------------------------------------------------------------------------
// page

func (s *Server) handlePage(w http.ResponseWriter, r *http.Request) {
	fg, _ := s.foregroundInfo(r.Context())
	snap, err := s.Tree.Snapshot(r.Context(), 0)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "ui dump failed", err)
		return
	}
	windows := map[string]any{"state": "unavailable"}
	if out, err := s.Helper.Windows(r.Context()); err == nil {
		windows = out
		windows["state"] = "available"
	}
	pkg, _ := fg["package"].(string)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok": true, "package": fg["package"], "component": fg["component"],
		"windows": windows, "keyboard": s.keyboardState(r.Context()),
		"fingerprint": pageFingerprint(snap.Nodes),
		"app_version": s.appVersion(r.Context(), pkg),
		"node_count":  len(snap.Nodes), "truncated": snap.Truncated, "source": snap.Source,
	})
}

func (s *Server) foregroundInfo(ctx context.Context) (map[string]any, error) {
	out, err := s.Probe.Run.Run(ctx, "dumpsys", "window")
	if err != nil {
		return nil, err
	}
	for _, key := range []string{"mCurrentFocus=", "mFocusedApp="} {
		if fg := focusedFrom(out, key); fg != nil {
			return fg, nil
		}
	}
	return map[string]any{"package": "", "component": ""}, nil
}

func (s *Server) keyboardState(ctx context.Context) map[string]any {
	out, err := s.Probe.Run.Run(ctx, "dumpsys", "input_method")
	if err != nil {
		return map[string]any{"shown": "unknown", "detail": err.Error()}
	}
	shown := "unknown"
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, "mInputShown=true") {
			shown = "true"
			break
		}
		if strings.Contains(line, "mInputShown=false") {
			shown = "false"
		}
	}
	return map[string]any{"shown": shown}
}

func pageFingerprint(nodes []ui.Node) string {
	h := fnv.New64a()
	for _, n := range nodes {
		// selected is part of the page identity: a highlighted tab is the only
		// difference between two otherwise identical feeds (PhoneGo's matching
		// uses it the same way).
		fmt.Fprintf(h, "%s|%s|%s|%s|%s\n",
			n.Attrs["class"], n.Attrs["resource-id"], n.Attrs["text"], n.Attrs["content-desc"], n.Attrs["selected"])
	}
	return fmt.Sprintf("p_%016x", h.Sum64())
}

// ---------------------------------------------------------------------------
// wait_stable

type waitStableRequest struct {
	TimeoutMS int    `json:"timeout_ms"`
	StableMS  int    `json:"stable_ms"`
	Scope     string `json:"scope"` // tree | frame
}

func (s *Server) handleWaitStable(w http.ResponseWriter, r *http.Request) {
	var body waitStableRequest
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	timeout := body.TimeoutMS
	if timeout <= 0 {
		timeout = 5000
	}
	stable := body.StableMS
	if stable <= 0 {
		stable = 300
	}
	scope := body.Scope
	if scope == "" {
		scope = "tree"
	}
	if scope != "tree" && scope != "frame" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"scope must be tree or frame", nil)
		return
	}
	ctx, cancel := contextWithTimeout(r, time.Duration(timeout+5000)*time.Millisecond)
	defer cancel()
	start := time.Now()
	deadline := start.Add(time.Duration(timeout) * time.Millisecond)
	last := ""
	since := time.Now()
	for {
		var hash string
		var err error
		if scope == "tree" {
			snap, e := s.Tree.Snapshot(ctx, 0)
			err = e
			if e == nil {
				hash = treeHash(snap.Nodes)
			}
		} else {
			hash, err = s.frameHash(ctx)
		}
		if err == nil {
			if hash != last {
				last = hash
				since = time.Now()
			} else if time.Since(since) >= time.Duration(stable)*time.Millisecond {
				httpx.WriteJSON(w, http.StatusOK, map[string]any{
					"ok": true, "stable": true,
					"waited_ms": time.Since(start).Milliseconds(),
					"stable_ms": stable, "scope": scope,
				})
				return
			}
		}
		if time.Now().After(deadline) {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"ok": true, "stable": false,
				"waited_ms": time.Since(start).Milliseconds(),
				"stable_ms": stable, "scope": scope,
				"reason": "timeout",
			})
			return
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
		}
	}
}

func treeHash(nodes []ui.Node) string {
	h := fnv.New64a()
	for _, n := range nodes {
		// selected belongs to the screen's identity: two tabs of the same app
		// share every other attribute. PhoneGo's screen signature is the raw
		// tree, which includes it.
		fmt.Fprintf(h, "%s|%s|%s|%s|%v\n",
			n.Attrs["class"], n.Attrs["text"], n.Attrs["resource-id"], n.Attrs["selected"], n.Bounds)
	}
	return fmt.Sprintf("t_%016x", h.Sum64())
}

func (s *Server) frameHash(ctx context.Context) (string, error) {
	data, _, _, _, err := s.frameJPEG(ctx, 80, 0, false)
	if err != nil {
		return "", err
	}
	h := fnv.New64a()
	_, _ = h.Write(data)
	return fmt.Sprintf("f_%016x", h.Sum64()), nil
}

// waitTreeStable is the internal form used after a scroll: it does not create an
// HTTP request and reports only success/failure.
func (s *Server) waitTreeStable(ctx context.Context, stableMS, timeoutMS int) bool {
	deadline := time.Now().Add(time.Duration(timeoutMS) * time.Millisecond)
	last := ""
	since := time.Now()
	for {
		snap, err := s.Tree.Snapshot(ctx, 0)
		if err != nil {
			return false
		}
		h := treeHash(snap.Nodes)
		if h != last {
			last = h
			since = time.Now()
		} else if time.Since(since) >= time.Duration(stableMS)*time.Millisecond {
			return true
		}
		if time.Now().After(deadline) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-time.After(100 * time.Millisecond):
		}
	}
}
