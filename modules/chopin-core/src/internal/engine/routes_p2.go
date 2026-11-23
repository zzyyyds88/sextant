package engine

import (
	"fmt"
	"net/http"
	"sync"
	"time"

	"chopin-core/internal/httpx"
	"chopin-core/internal/ui"
)

// ---------------------------------------------------------------------------
// metrics

// metricsRecorder keeps the status code without hiding http.Flusher: the SSE
// endpoints would otherwise stop flushing and hang.
type metricsRecorder struct {
	http.ResponseWriter
	status int
}

func (m *metricsRecorder) WriteHeader(code int) {
	m.status = code
	m.ResponseWriter.WriteHeader(code)
}

func (m *metricsRecorder) Flush() {
	if f, ok := m.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (m *metricsRecorder) Unwrap() http.ResponseWriter { return m.ResponseWriter }

// withMetrics records every served request. r.Pattern is the registered route
// pattern (Go 1.22), so labels never come from a request string.
func (s *Server) withMetrics(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rec := &metricsRecorder{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(rec, r)
		s.Metrics.Observe(r.Pattern, rec.status, time.Since(start).Milliseconds())
	})
}

func (s *Server) handleMetrics(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, s.Metrics.Snapshot())
}

// ---------------------------------------------------------------------------
// incremental tree diff

const (
	treeFrameTTL = 60 * time.Second
	treeFrameMax = 8
)

type cachedTree struct {
	nodes []ui.FlatNode
	at    time.Time
}

// treeFrames keeps the last few observed trees so /v1/ui/diff can compare two
// frames without the caller shipping a whole tree back. Capacity and age are
// bounded; an evicted frame is a 404, never a stale comparison.
type treeFrames struct {
	mu    sync.Mutex
	m     map[string]cachedTree
	order []string
}

func (c *treeFrames) put(id string, nodes []ui.FlatNode, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m == nil {
		c.m = map[string]cachedTree{}
	}
	if _, ok := c.m[id]; !ok {
		c.order = append(c.order, id)
	}
	c.m[id] = cachedTree{nodes: nodes, at: now}
	for len(c.order) > treeFrameMax {
		old := c.order[0]
		c.order = c.order[1:]
		delete(c.m, old)
	}
	for key, e := range c.m {
		if now.Sub(e.at) > treeFrameTTL {
			delete(c.m, key)
		}
	}
}

func (c *treeFrames) get(id string, now time.Time) ([]ui.FlatNode, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.m[id]
	if !ok {
		return nil, false
	}
	if now.Sub(e.at) > treeFrameTTL {
		delete(c.m, id)
		return nil, false
	}
	return e.nodes, true
}

// flatNodeJSON is the wire shape shared by observe's tree and diff's lists. It
// matches /v1/ui/tree's primitive nodes plus the structural path diff pairs on.
func flatNodeJSON(f ui.FlatNode) map[string]any {
	var parent any
	if f.ParentNodeID != "" {
		parent = f.ParentNodeID
	}
	return map[string]any{
		"node_id":        f.NodeID,
		"path":           f.Path,
		"text":           f.Attrs["text"],
		"resource_id":    f.Attrs["resource-id"],
		"class":          f.Attrs["class"],
		"content_desc":   f.Attrs["content-desc"],
		"bounds":         f.Bounds,
		"selected":       triState(f.Attrs["selected"]),
		"visible":        triState(f.Attrs["visible"]),
		"enabled":        triState(f.Attrs["enabled"]),
		"clickable":      triState(f.Attrs["clickable"]),
		"depth":          f.Depth,
		"parent_node_id": parent,
	}
}

type diffRequest struct {
	Before string `json:"before_frame_id"`
	After  string `json:"after_frame_id"`
}

func (s *Server) handleDiff(w http.ResponseWriter, r *http.Request) {
	var body diffRequest
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.Before == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "before_frame_id is required", nil)
		return
	}
	now := time.Now()
	before, ok := s.treeFrames.get(body.Before, now)
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, "frame_not_found",
			"the before frame is unknown or expired; capture it with observe(tree=true) again", nil)
		return
	}
	afterID := body.After
	var after []ui.FlatNode
	if afterID == "" {
		ctx, cancel := contextWithTimeout(r, 30*time.Second)
		defer cancel()
		snap, err := s.Tree.Snapshot(ctx, 0)
		if err != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "ui dump failed", err)
			return
		}
		after = ui.Flatten(snap.Nodes)
		afterID = fmt.Sprintf("f_%d", time.Now().UnixNano())
		s.treeFrames.put(afterID, after, time.Now())
	} else {
		after, ok = s.treeFrames.get(afterID, time.Now())
		if !ok {
			httpx.WriteError(w, http.StatusNotFound, "frame_not_found",
				"the after frame is unknown or expired; omit it to capture a fresh one", nil)
			return
		}
	}
	result := ui.Diff(before, after, 500)
	added := make([]map[string]any, 0, len(result.Added))
	for _, n := range result.Added {
		added = append(added, flatNodeJSON(n))
	}
	removed := make([]map[string]any, 0, len(result.Removed))
	for _, n := range result.Removed {
		removed = append(removed, flatNodeJSON(n))
	}
	s.Audit.Info("ui: diff %s -> %s added=%d removed=%d changed=%d truncated=%v",
		body.Before, afterID, result.Counts.Added, result.Counts.Removed, result.Counts.Changed, result.Truncated)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":              true,
		"before_frame_id": body.Before,
		"after_frame_id":  afterID,
		"added":           added,
		"removed":         removed,
		"changed":         result.Changed,
		"counts":          result.Counts,
		"truncated":       result.Truncated,
	})
}
