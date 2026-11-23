// Package ui dumps and parses the accessibility node tree.
//
// `uiautomator dump <path>` writes to a FILE; dumping to a device node like
// /dev/tty fails on this ROM with a MIUI resource exception before any XML is
// produced. So the dump always goes to a real file under the daemon's tmp dir and
// is read back here.
package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"sextant-core/internal/sysprobe"
)

// Node is one accessibility node.
type Node struct {
	Attrs  map[string]string
	Bounds [4]int // x1, y1, x2, y2
	Index  int
	// Parent is the index of the containing node, or -1 for a root. Depth is
	// its distance from that root. Both exist so a caller can prefer a leaf over
	// a container that merely aggregates the same label (D29).
	Parent int
	Depth  int
}

// BoundsString renders the raw bounds attribute.
func (n Node) BoundsString() string { return n.Attrs["bounds"] }

// Center returns the node's midpoint.
func (n Node) Center() (int, int) {
	return (n.Bounds[0] + n.Bounds[2]) / 2, (n.Bounds[1] + n.Bounds[3]) / 2
}

// Dumper produces node trees.
type Dumper struct {
	Probe sysprobe.Probe
	Tmp   string
}

var (
	nodeRE   = regexp.MustCompile(`(?s)<node\b([^>]*?)/?>`)
	attrRE   = regexp.MustCompile(`([A-Za-z0-9_:-]+)="([^"]*)"`)
	boundsRE = regexp.MustCompile(`^\[(-?\d+),(-?\d+)\]\[(-?\d+),(-?\d+)\]$`)
)

// Dump dumps the current tree and returns the raw XML plus parsed nodes.
//
// A non-zero exit AFTER the file was written is normal for uiautomator, so the
// file is the source of truth: the command's error is only reported when there is
// no file to read.
func (d Dumper) Dump(ctx context.Context, name string) (string, []Node, error) {
	dir := d.Tmp
	if dir == "" {
		dir = "/data/adb/sextant-core/tmp"
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", nil, fmt.Errorf("ui dump dir: %w", err)
	}
	path := filepath.Join(dir, name+".xml")
	_ = os.Remove(path)
	cmdErr := func() error {
		_, err := d.Probe.Run.Run(ctx, "uiautomator", "dump", path)
		return err
	}()
	data, readErr := os.ReadFile(path)
	if readErr != nil {
		if cmdErr != nil {
			return "", nil, fmt.Errorf("uiautomator dump: %w", cmdErr)
		}
		return "", nil, fmt.Errorf("read ui dump: %w", readErr)
	}
	xmlText := string(data)
	return xmlText, Parse(xmlText), nil
}

// Parse extracts nodes from a uiautomator XML document.
func Parse(xmlText string) []Node {
	var out []Node
	for i, m := range nodeRE.FindAllStringSubmatch(xmlText, -1) {
		attrs := map[string]string{}
		for _, a := range attrRE.FindAllStringSubmatch(m[1], -1) {
			attrs[a[1]] = a[2]
		}
		var b [4]int
		if bm := boundsRE.FindStringSubmatch(attrs["bounds"]); len(bm) == 5 {
			for j := 0; j < 4; j++ {
				b[j], _ = strconv.Atoi(bm[j+1])
			}
		}
		out = append(out, Node{Attrs: attrs, Bounds: b, Index: i})
	}
	return out
}

// FindByResourceID returns the first node whose resource-id contains sub.
func FindByResourceID(nodes []Node, sub string) (Node, bool) {
	return pick(nodes, func(n Node) bool { return strings.Contains(n.Attrs["resource-id"], sub) })
}

// FindByText returns the best node whose text or content-desc contains sub.
//
// "Best" prefers an EXACT match. Without that, `find serendipity --click` tapped the
// avatar: WeChat's conversation row contains an image whose content-desc is
// 「serendipity头像」, the tree lists it first, and clicking it opens the contact card
// instead of the chat — measured, and it looks like a working click until you read the
// next screen. The row's own title is the exact label, so exactness is the fix.
func FindByText(nodes []Node, sub string) (Node, bool) {
	// Ranking lives in FindCandidates so wait and find see the same node.
	candidates := FindCandidates(nodes, sub)
	if len(candidates) == 0 {
		return Node{}, false
	}
	return candidates[0].Node, true
}

// pick returns the best node matching pred.
//
// "Best" means on screen and tappable. Returning the first match is what made
// `find serendipity --click` tap (‑111, 295): WeChat keeps a floating-window copy of
// the conversation row with negative coordinates, the tree lists it first, and its
// centre is off the left edge of the display. A node whose centre is outside the
// screen cannot be what the caller meant.
func pick(nodes []Node, pred func(Node) bool) (Node, bool) {
	var fallback Node
	haveFallback := false
	for _, n := range nodes {
		if !pred(n) {
			continue
		}
		if tappable(n) {
			return n, true
		}
		if !haveFallback {
			fallback, haveFallback = n, true
		}
	}
	return fallback, haveFallback
}

// Normalized returns the rectangle with left ≤ right and top ≤ bottom.
//
// Accessibility bounds are supposed to be ordered, and WeChat's are not: a
// conversation row came back as [194 2351 430 2249] (bottom above top). Reading it
// literally made the node look like it had negative height, so it failed the
// on-screen test and the search fell back to an off-screen copy at x = -107 —
// a click that lands nowhere and reports success.
func (n Node) Normalized() [4]int {
	b := n.Bounds
	return [4]int{min(b[0], b[2]), min(b[1], b[3]), max(b[0], b[2]), max(b[1], b[3])}
}

// Center returns the node's midpoint.
func (n Node) CenterX() int { return (n.Bounds[0] + n.Bounds[2]) / 2 }
func (n Node) CenterY() int { return (n.Bounds[1] + n.Bounds[3]) / 2 }

// tappable reports whether a node is somewhere a finger can actually land.
//
// It cannot know the display size here, so it uses the weaker but sufficient test:
// a non-empty rectangle whose centre is not negative, measured on the NORMALIZED
// rectangle. Every off-screen copy this device produced had a negative x or y.
func tappable(n Node) bool {
	// D29: an explicitly invisible or disabled node is not a target. Absent
	// attributes mean "unknown" (an older helper or a WebView virtual node), not
	// "false".
	if n.Attrs["visible"] == "false" || n.Attrs["enabled"] == "false" {
		return false
	}
	b := n.Normalized()
	if b[2]-b[0] <= 0 || b[3]-b[1] <= 0 {
		return false
	}
	return (b[0]+b[2])/2 >= 0 && (b[1]+b[3])/2 >= 0
}

// Candidate is one node matching a find query, with the reason it ranked where
// it did.
type Candidate struct {
	Node  Node
	Match string // text_exact | text_contains | desc_exact | desc_contains
}

// FindCandidates returns every node that carries the label, best first.
//
// The ordering is the point: within a match tier, visible and enabled come
// before unknown, a clickable node comes before a container, and a smaller node
// comes before a larger one. A 399px-high container that aggregates its
// children's text must not outrank the 35px text leaf a person would tap.
func FindCandidates(nodes []Node, sub string) []Candidate {
	levels := []struct {
		name string
		pred func(Node) bool
	}{
		{"text_exact", func(n Node) bool { return n.Attrs["text"] == sub }},
		{"text_contains", func(n Node) bool { return strings.Contains(n.Attrs["text"], sub) }},
		{"desc_exact", func(n Node) bool { return n.Attrs["content-desc"] == sub }},
		{"desc_contains", func(n Node) bool { return strings.Contains(n.Attrs["content-desc"], sub) }},
	}
	area := func(n Node) int {
		b := n.Normalized()
		w, h := b[2]-b[0], b[3]-b[1]
		if w <= 0 || h <= 0 {
			return 1 << 30
		}
		return w * h
	}
	rank := func(n Node) (int, int, int, int) {
		vis, en, click := 0, 0, 0
		if n.Attrs["visible"] == "true" {
			vis = 1
		}
		if n.Attrs["enabled"] != "false" {
			en = 1
		}
		if n.Attrs["clickable"] == "true" {
			click = 1
		}
		return -vis, -en, -click, area(n)
	}
	var out []Candidate
	seen := map[int]bool{}
	for _, level := range levels {
		var tier []Candidate
		for _, n := range nodes {
			if seen[n.Index] || !level.pred(n) || !tappable(n) {
				continue
			}
			seen[n.Index] = true
			tier = append(tier, Candidate{Node: n, Match: level.name})
		}
		sort.SliceStable(tier, func(i, j int) bool {
			iv, ie, ic, ia := rank(tier[i].Node)
			jv, je, jc, ja := rank(tier[j].Node)
			if iv != jv {
				return iv < jv
			}
			if ie != je {
				return ie < je
			}
			if ic != jc {
				return ic < jc
			}
			return ia < ja
		})
		out = append(out, tier...)
	}
	return out
}
