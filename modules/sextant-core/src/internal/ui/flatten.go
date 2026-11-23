package ui

import (
	"fmt"
	"strconv"
)

// FlatNode is a node plus a structural path. The path is what Diff pairs on: it
// is stable while the tree structure is unchanged, and shifts for the nodes
// after an insertion, which Diff reports honestly as changes rather than trying
// to guess a minimal edit.
type FlatNode struct {
	Path   string
	NodeID string
	// Parent is the parent's structural path; ParentNodeID is its node_id.
	Parent       string
	ParentNodeID string
	Attrs        map[string]string
	Bounds       [4]int
	Depth        int
}

// Flatten assigns structural paths in document order. Nodes must be in
// pre-order with Parent pointing at an earlier node (the shape the helper
// emits).
func Flatten(nodes []Node) []FlatNode {
	out := make([]FlatNode, len(nodes))
	nextChild := map[int]int{}
	for i, n := range nodes {
		parentPath := ""
		parentID := ""
		ordinal := 0
		if n.Parent >= 0 && n.Parent < len(nodes) && n.Parent < i {
			parentPath = out[n.Parent].Path
			parentID = out[n.Parent].NodeID
			ordinal = nextChild[n.Parent]
			nextChild[n.Parent]++
		} else {
			ordinal = nextChild[-1]
			nextChild[-1]++
		}
		path := strconv.Itoa(ordinal)
		if parentPath != "" {
			path = parentPath + "/" + strconv.Itoa(ordinal)
		}
		out[i] = FlatNode{
			Path: path, NodeID: fmt.Sprintf("n_%d", n.Index), Parent: parentPath,
			ParentNodeID: parentID, Attrs: n.Attrs, Bounds: n.Bounds, Depth: n.Depth,
		}
	}
	return out
}

// DiffField is one changed attribute.
type DiffField struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

// DiffChange is one node whose attributes or bounds changed.
type DiffChange struct {
	NodeID string               `json:"node_id"`
	Path   string               `json:"path"`
	Fields map[string]DiffField `json:"fields"`
}

// DiffCounts reports totals even when the change list is truncated.
type DiffCounts struct {
	Before  int `json:"before"`
	After   int `json:"after"`
	Added   int `json:"added"`
	Removed int `json:"removed"`
	Changed int `json:"changed"`
}

// DiffResult is the D33 structural difference between two frames.
type DiffResult struct {
	Added     []FlatNode   `json:"added"`
	Removed   []FlatNode   `json:"removed"`
	Changed   []DiffChange `json:"changed"`
	Counts    DiffCounts   `json:"counts"`
	Truncated bool         `json:"truncated"`
}

func indexFlat(nodes []FlatNode) map[string]FlatNode {
	m := make(map[string]FlatNode, len(nodes))
	for _, n := range nodes {
		m[n.Path] = n
	}
	return m
}

// Diff compares two flattened frames by structural path. maxChanges bounds each
// list; counts still describe the whole difference.
func Diff(before, after []FlatNode, maxChanges int) DiffResult {
	if maxChanges <= 0 {
		maxChanges = 500
	}
	bm := indexFlat(before)
	am := indexFlat(after)
	res := DiffResult{
		Added:   []FlatNode{},
		Removed: []FlatNode{},
		Changed: []DiffChange{},
		Counts:  DiffCounts{Before: len(before), After: len(after)},
	}
	budget := maxChanges
	take := func() bool {
		if budget <= 0 {
			res.Truncated = true
			return false
		}
		budget--
		return true
	}
	for _, n := range before {
		if _, ok := am[n.Path]; !ok {
			res.Counts.Removed++
			if take() {
				res.Removed = append(res.Removed, n)
			}
		}
	}
	for _, n := range after {
		if _, ok := bm[n.Path]; !ok {
			res.Counts.Added++
			if take() {
				res.Added = append(res.Added, n)
			}
		}
	}
	for _, n := range after {
		b, ok := bm[n.Path]
		if !ok {
			continue
		}
		fields := diffFields(b, n)
		if len(fields) == 0 {
			continue
		}
		res.Counts.Changed++
		if take() {
			res.Changed = append(res.Changed, DiffChange{NodeID: n.NodeID, Path: n.Path, Fields: fields})
		}
	}
	return res
}

func diffFields(before, after FlatNode) map[string]DiffField {
	fields := map[string]DiffField{}
	for key, bv := range before.Attrs {
		av := after.Attrs[key]
		if bv != av {
			fields[key] = DiffField{Before: bv, After: av}
		}
	}
	for key, av := range after.Attrs {
		if _, ok := before.Attrs[key]; ok {
			continue
		}
		fields[key] = DiffField{Before: "", After: av}
	}
	if before.Bounds != after.Bounds {
		fields["bounds"] = DiffField{
			Before: fmt.Sprintf("%v", before.Bounds),
			After:  fmt.Sprintf("%v", after.Bounds),
		}
	}
	return fields
}
