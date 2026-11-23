package ui

import (
	"context"
	"fmt"
)

// NodeSource produces the node tree for a display.
//
// Two implementations exist because the device has two, and they cannot run at
// the same time: `uiautomator dump` starts its own UiAutomation session, and the
// root helper already holds one for OCR. Starting a second session kills the
// first (the dump dies with "signal: killed"), so the caller must pick the
// source that is actually free rather than firing both.
type NodeSource interface {
	Nodes(ctx context.Context, display int) ([]Node, error)
	Name() string
}

// HelperSource reads the tree from the root helper's UiAutomation session.
type HelperSource struct {
	// Call runs one helper method and decodes its data into out.
	Call func(ctx context.Context, method string, params map[string]any, out any) error
}

// Name implements NodeSource.
func (h HelperSource) Name() string { return "helper" }

// Snapshot is a tree plus what the producer knows about its completeness.
type Snapshot struct {
	Nodes     []Node
	Source    string
	Truncated bool
}

// Snapshot implements the optional snapshotSource interface.
func (h HelperSource) Snapshot(ctx context.Context, display int) (Snapshot, error) {
	var raw map[string]any
	if err := h.Call(ctx, "a11y_tree", map[string]any{"display_id": display}, &raw); err != nil {
		return Snapshot{}, err
	}
	nodes, truncated, err := nodesFromHelper(raw)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Nodes: nodes, Source: h.Name(), Truncated: truncated}, nil
}

// Nodes implements NodeSource.
func (h HelperSource) Nodes(ctx context.Context, display int) ([]Node, error) {
	snap, err := h.Snapshot(ctx, display)
	return snap.Nodes, err
}

// NodesFromHelper converts the helper's nested tree payload into Nodes. It is
// exported for /v1/ui/observe, whose payload has the same shape as a11y_tree.
func NodesFromHelper(raw map[string]any) ([]Node, bool, error) {
	return nodesFromHelper(raw)
}

// nodesFromHelper converts the helper's JSON tree into Nodes.
//
// The helper emits `{"tree":[root...],"nodes":N}` with keys class/text/desc/rid/
// package/clickable/enabled/scrollable/checked/editable/window_id/bounds{left,
// top,right,bottom}/children. Mapping lives here so the two sides cannot drift:
// a rename on the Java side shows up as an empty tree in tests, not as silently
// wrong attributes.
func nodesFromHelper(raw map[string]any) ([]Node, bool, error) {
	tree, ok := raw["tree"].([]any)
	if !ok {
		return nil, false, fmt.Errorf("helper tree has no 'tree' array")
	}
	truncated, _ := raw["truncated"].(bool)
	var out []Node
	var walk func(any, int, int)
	walk = func(v any, parent, depth int) {
		m, ok := v.(map[string]any)
		if !ok {
			return
		}
		n := Node{Attrs: map[string]string{}, Index: len(out), Parent: parent, Depth: depth}
		set := func(attr, key string) {
			if s, ok := m[key].(string); ok && s != "" {
				n.Attrs[attr] = s
			}
		}
		set("text", "text")
		set("class", "class")
		set("package", "package")
		set("resource-id", "rid")
		set("content-desc", "desc")
		// D29: keep both states. "false" is an observation; a missing key is
		// unknown (older helper / virtual node) and must not be read as false.
		for _, key := range []string{
			"clickable", "enabled", "scrollable", "checked", "editable",
			"selected", "visible", "focusable", "long_clickable",
		} {
			if b, ok := m[key].(bool); ok {
				if b {
					n.Attrs[key] = "true"
				} else {
					n.Attrs[key] = "false"
				}
			}
		}
		if b, ok := m["bounds"].(map[string]any); ok {
			n.Bounds[0] = intOf(b["left"])
			n.Bounds[1] = intOf(b["top"])
			n.Bounds[2] = intOf(b["right"])
			n.Bounds[3] = intOf(b["bottom"])
			n.Attrs["bounds"] = fmt.Sprintf("[%d,%d][%d,%d]",
				n.Bounds[0], n.Bounds[1], n.Bounds[2], n.Bounds[3])
		}
		if w, ok := m["window_id"]; ok {
			n.Attrs["window_id"] = fmt.Sprintf("%d", intOf(w))
		}
		out = append(out, n)
		idx := n.Index
		if kids, ok := m["children"].([]any); ok {
			for _, k := range kids {
				walk(k, idx, depth+1)
			}
		}
	}
	for _, item := range tree {
		walk(item, -1, 0)
	}
	return out, truncated, nil
}

func intOf(v any) int {
	switch n := v.(type) {
	case float64:
		return int(n)
	case int:
		return n
	}
	return 0
}

func atoi(s string) int {
	n := 0
	for _, r := range s {
		if r < '0' || r > '9' {
			continue
		}
		n = n*10 + int(r-'0')
	}
	return n
}

// TreeService picks between the two sources.
type TreeService struct {
	Helper     NodeSource
	Dumper     Dumper
	HelperBusy func(ctx context.Context) bool
}

// Nodes returns the tree from the helper when it is running, and from
// `uiautomator dump` only when it is not.
//
// The order matters and is not a preference: while the helper holds UiAutomation,
// `uiautomator dump` is killed mid-run (its own session cannot coexist), so
// falling back to the dump in that state produces "signal: killed" instead of a
// tree. If the helper is up but cannot produce a tree, that is reported as an
// error rather than silently retried through a mechanism known to be blocked.
func (t TreeService) Nodes(ctx context.Context, display int) ([]Node, string, error) {
	snap, err := t.Snapshot(ctx, display)
	return snap.Nodes, snap.Source, err
}

// Snapshot is Nodes plus completeness metadata.
func (t TreeService) Snapshot(ctx context.Context, display int) (Snapshot, error) {
	if t.Helper != nil && t.HelperBusy != nil && t.HelperBusy(ctx) {
		if s, ok := t.Helper.(interface {
			Snapshot(context.Context, int) (Snapshot, error)
		}); ok {
			snap, err := s.Snapshot(ctx, display)
			if err != nil {
				return Snapshot{}, fmt.Errorf("helper tree: %w", err)
			}
			return snap, nil
		}
		nodes, err := t.Helper.Nodes(ctx, display)
		if err != nil {
			return Snapshot{}, fmt.Errorf("helper tree: %w", err)
		}
		return Snapshot{Nodes: nodes, Source: t.Helper.Name()}, nil
	}
	_, nodes, err := t.Dumper.Dump(ctx, "tree")
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{Nodes: nodes, Source: "uiautomator"}, nil
}
