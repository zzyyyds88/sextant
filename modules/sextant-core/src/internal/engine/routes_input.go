package engine

import (
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"sextant-core/internal/device"
	"sextant-core/internal/httpx"
	"sextant-core/internal/ime"
)

// Input and capture handlers.
//
// Every one of them reports what actually happened: a tap that could not be sent
// is an error, not a quiet success. Text goes through the IME provider on MIUI
// because `input text` only produces a composition string there, which is why it
// is a separate endpoint with its own error codes (DESIGN §9.3).

func (s *Server) handleTap(w http.ResponseWriter, r *http.Request) {
	var body struct {
		X       int    `json:"x"`
		Y       int    `json:"y"`
		Display int    `json:"display"`
		Seed    string `json:"seed"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.X < 0 || body.Y < 0 {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "x and y are required", nil)
		return
	}
	if s.refuseIfPaused(w) || s.refuseIfHandedOver(w) {
		return
	}
	restore, err := s.passThroughForInput(r.Context())
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeNotReady,
			"cannot pass touches through the overlay, so the tap could hit the Stop button", err)
		return
	}
	if restore != nil {
		defer restore()
	}
	seed := s.requestSeed(body.Seed)
	px, py, release, pacedMS, intervalMS, err := s.injectionPoint(r.Context(), body.Display, nil, body.X, body.Y, seed, requestIDFor(r), 0)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "cannot pace the tap", err)
		return
	}
	defer release()
	tapStart := time.Now()
	if err := s.Device.Tap(r.Context(), px, py, body.Display); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "tap failed", err)
		return
	}
	dispatchMS := time.Since(tapStart).Milliseconds()
	s.Audit.Info("input: tap %d,%d (requested %d,%d) display=%d dispatch=%dms paced=%dms seed=%s",
		px, py, body.X, body.Y, body.Display, dispatchMS, pacedMS, seed)
	// The overlay says what is being pressed; a bare 「控制中」 tells the person
	// holding the phone nothing they can act on.
	s.noteAction("%s", stepLabel("", "", px, py))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok": true, "point": []int{px, py}, "seed": seed,
		"dispatch_ms": dispatchMS, "paced_ms": pacedMS, "interval_ms": intervalMS,
	})
}

func (s *Server) handleSwipe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		X1       int    `json:"x1"`
		Y1       int    `json:"y1"`
		X2       int    `json:"x2"`
		Y2       int    `json:"y2"`
		Duration int    `json:"duration_ms"`
		Display  int    `json:"display"`
		Seed     string `json:"seed"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.Duration <= 0 {
		body.Duration = 300
	}
	if s.refuseIfPaused(w) || s.refuseIfHandedOver(w) {
		return
	}
	restore, err := s.passThroughForInput(r.Context())
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeNotReady,
			"cannot pass touches through the overlay, so the swipe could hit the Stop button", err)
		return
	}
	if restore != nil {
		defer restore()
	}
	// A vertical swipe is a scroll, and the list knows how to scroll itself.
	//
	// Doing it by gesture means "move whatever is under this finger", which on a page
	// with a carousel or a banner is not the list. Prefer the node's own scroll
	// action when the point is inside a scrollable node; the gesture stays as the
	// fallback, which app-drawn views (WeChat's chat) need.
	if body.X1 == body.X2 && body.Y1 != body.Y2 {
		direction := "down"
		if body.Y2 > body.Y1 {
			direction = "up"
		}
		if s.scrollNode(r.Context(), direction, body.Display, body.X1, (body.Y1+body.Y2)/2) {
			s.Audit.Info("input: scroll %s via node at %d,%d", direction, body.X1, (body.Y1+body.Y2)/2)
			s.noteAction("滚动 %s", map[string]string{"down": "下", "up": "上"}[direction])
			httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "via": "node"})
			return
		}
	}
	seed := s.requestSeed(body.Seed)
	reqID := requestIDFor(r)
	px1, py1, release, pacedMS, intervalMS, err := s.injectionPoint(r.Context(), body.Display, nil, body.X1, body.Y1, seed, reqID, 0)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "cannot pace the swipe", err)
		return
	}
	defer release()
	px2, py2 := body.X2, body.Y2
	if s.Human != nil {
		px2, py2 = s.Human.Point(nil, body.X2, body.Y2, seed, reqID, body.Display, 1)
	}
	duration := body.Duration
	if s.Human != nil {
		duration = int(s.Human.ScaleWait(time.Duration(body.Duration)*time.Millisecond, seed, reqID, body.Display, 2) / time.Millisecond)
	}
	if err := s.Device.Swipe(r.Context(), px1, py1, px2, py2, duration, body.Display); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "swipe failed", err)
		return
	}
	s.Audit.Info("input: swipe %d,%d -> %d,%d (requested %d,%d -> %d,%d) duration=%dms paced=%dms seed=%s",
		px1, py1, px2, py2, body.X1, body.Y1, body.X2, body.Y2, duration, pacedMS, seed)
	s.noteAction("滑动 %d,%d → %d,%d", px1, py1, px2, py2)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok": true, "from": []int{px1, py1}, "to": []int{px2, py2},
		"seed": seed, "paced_ms": pacedMS, "interval_ms": intervalMS,
	})
}

func (s *Server) handleLongPress(w http.ResponseWriter, r *http.Request) {
	var body struct {
		X        int    `json:"x"`
		Y        int    `json:"y"`
		Duration int    `json:"duration_ms"`
		Display  int    `json:"display"`
		Seed     string `json:"seed"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.Duration <= 0 {
		body.Duration = 800
	}
	if s.refuseIfPaused(w) || s.refuseIfHandedOver(w) {
		return
	}
	restore, err := s.passThroughForInput(r.Context())
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeNotReady,
			"cannot pass touches through the overlay", err)
		return
	}
	if restore != nil {
		defer restore()
	}
	seed := s.requestSeed(body.Seed)
	px, py, release, pacedMS, intervalMS, err := s.injectionPoint(r.Context(), body.Display, nil, body.X, body.Y, seed, requestIDFor(r), 0)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "cannot pace the long press", err)
		return
	}
	defer release()
	if err := s.Device.LongPress(r.Context(), px, py, body.Duration, body.Display); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "long press failed", err)
		return
	}
	s.Audit.Info("input: longpress %d,%d (requested %d,%d) %dms paced=%dms seed=%s",
		px, py, body.X, body.Y, body.Duration, pacedMS, seed)
	s.noteAction("长按 (%d, %d)", px, py)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok": true, "point": []int{px, py}, "seed": seed, "paced_ms": pacedMS, "interval_ms": intervalMS,
	})
}

func (s *Server) handleKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key     string `json:"key"`
		Display int    `json:"display"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if s.refuseIfPaused(w) || s.refuseIfHandedOver(w) {
		return
	}
	seed := s.requestSeed("")
	release, pacedMS, intervalMS, paceErr := s.pace(r.Context(), body.Display, seed, requestIDFor(r), 0)
	if paceErr != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "cannot pace the key event", paceErr)
		return
	}
	defer release()
	if err := s.Device.Key(r.Context(), body.Key, body.Display); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "key injection failed", err)
		return
	}
	s.Audit.Info("input: key %s paced=%dms", body.Key, pacedMS)
	s.noteAction("%s", describeKey(body.Key))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "paced_ms": pacedMS, "interval_ms": intervalMS})
}

// handleScreenshot captures display 0 through the helper and returns metadata.
//
// JSON + path rather than a binary body: the caller is usually an agent that
// wants to know where the image is, and a path keeps the response small enough
// to log. The file lives under tmp/ and is cleaned by the daemon's retention.
//
// D28: the capture goes through UiAutomation and is JPEG-encoded. `screencap -p`
// spent ~1.1s of its 1.3s on PNG compression; the same frame as JPEG costs a
// fraction of that.
func (s *Server) handleScreenshot(w http.ResponseWriter, r *http.Request) {
	// The overlay must not be in the frame (DESIGN 9.4). If it cannot be hidden,
	// the capture is refused instead of returning an image that quietly shows the
	// agent's own controls — and, worse, hides what is underneath them.
	restore, err := s.hideOverlayForCapture(r.Context())
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeNotReady,
			"cannot hide the overlay, so the capture would include it", err)
		return
	}
	if restore != nil {
		defer restore()
	}
	path := s.Paths.Tmp + "/screenshot.jpg"
	_ = os.Remove(path)
	out, err := s.Helper.Screenshot(r.Context(), map[string]any{
		"display_id":  0,
		"format":      "jpeg",
		"quality":     80,
		"max_size":    0, // native resolution: OCR and bounds must stay in screen coordinates
		"output_path": path,
	})
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "helper capture failed", err)
		return
	}
	st, err := os.Stat(path)
	if err != nil || st.Size() == 0 {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable,
			"helper reported a capture but wrote no usable file", err)
		return
	}
	if data, readErr := os.ReadFile(path); readErr == nil {
		s.rememberFrame(data, intOf(out["width"]), intOf(out["height"]))
	}
	s.Audit.Info("ui: screenshot %d bytes (%s, %dx%d, capture %dms, encode %dms)",
		st.Size(), out["format"], intOf(out["width"]), intOf(out["height"]),
		intOf(out["capture_ms"]), intOf(out["encode_ms"]))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok": true, "path": path, "bytes": st.Size(),
		"format": out["format"], "width": intOf(out["width"]), "height": intOf(out["height"]),
		"capture_ms": intOf(out["capture_ms"]), "encode_ms": intOf(out["encode_ms"]),
	})
}

// handleTree returns the accessibility node tree.
//
// Which mechanism provides it depends on who owns UiAutomation right now: the
// root helper holds one session for OCR, and starting a second one via
// `uiautomator dump` kills the first. The Tree service decides and reports which
// source answered.
func (s *Server) handleTree(w http.ResponseWriter, r *http.Request) {
	snap, err := s.Tree.Snapshot(r.Context(), 0)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "ui dump failed", err)
		return
	}
	// D29 P1 context cropping. An agent that only needs labels must be able to
	// ask for labels: the full tree is several hundred KB on a dense page, and
	// most of the attributes are irrelevant to the decision at hand.
	q := r.URL.Query()
	fields := map[string]bool{}
	for _, f := range strings.Split(q.Get("fields"), ",") {
		if f = strings.TrimSpace(f); f != "" {
			fields[f] = true
		}
	}
	if isTrue(q.Get("summary")) {
		// The decision fields for "what can I tap and what does it say".
		for _, f := range []string{"text", "resource_id", "class", "content_desc", "bounds", "clickable", "enabled", "selected", "visible"} {
			fields[f] = true
		}
	}
	maxItems, _ := strconv.Atoi(q.Get("max_items"))
	viewportOnly := isTrue(q.Get("viewport_only"))
	var root [4]int
	if len(snap.Nodes) > 0 {
		root = snap.Nodes[0].Normalized()
	}
	out := make([]map[string]any, 0, len(snap.Nodes))
	for _, n := range snap.Nodes {
		if viewportOnly && len(snap.Nodes) > 0 {
			cx, cy := n.Center()
			if cx < root[0] || cx > root[2] || cy < root[1] || cy > root[3] {
				continue
			}
		}
		if maxItems > 0 && len(out) >= maxItems {
			break
		}
		full := map[string]any{
			"node_id":        fmt.Sprintf("n_%d", n.Index),
			"parent":         n.Parent,
			"depth":          n.Depth,
			"text":           n.Attrs["text"],
			"resource_id":    n.Attrs["resource-id"],
			"class":          n.Attrs["class"],
			"package":        n.Attrs["package"],
			"content_desc":   n.Attrs["content-desc"],
			"clickable":      triState(n.Attrs["clickable"]),
			"enabled":        triState(n.Attrs["enabled"]),
			"scrollable":     triState(n.Attrs["scrollable"]),
			"checked":        triState(n.Attrs["checked"]),
			"editable":       triState(n.Attrs["editable"]),
			"selected":       triState(n.Attrs["selected"]),
			"visible":        triState(n.Attrs["visible"]),
			"focusable":      triState(n.Attrs["focusable"]),
			"long_clickable": triState(n.Attrs["long_clickable"]),
			"window_id":      windowID(n.Attrs["window_id"]),
			"bounds":         n.Bounds,
		}
		if len(fields) == 0 {
			out = append(out, full)
			continue
		}
		// Identity stays in every projection: a cropped node must still be
		// addressable in act/observe without a second full-tree request.
		kept := map[string]any{"node_id": full["node_id"], "parent": full["parent"], "depth": full["depth"]}
		for k, v := range full {
			if fields[k] {
				kept[k] = v
			}
		}
		out = append(out, kept)
	}
	truncated := snap.Truncated || (maxItems > 0 && len(out) >= maxItems)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok": true, "count": len(out), "total": len(snap.Nodes), "source": snap.Source,
		"truncated": truncated, "nodes": out,
	})
}

func isTrue(v string) bool { return v == "1" || v == "true" }

// triState distinguishes "observed false" from "the producer did not say".
func triState(v string) any {
	switch v {
	case "true":
		return true
	case "false":
		return false
	default:
		return "unknown"
	}
}

func windowID(v string) any {
	if v == "" {
		return nil
	}
	if n, err := strconv.Atoi(v); err == nil {
		return n
	}
	return v
}

// handleText injects text into the focused field.
func (s *Server) handleText(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text  string `json:"text"`
		Clear bool   `json:"clear"`
		X     int    `json:"x"`
		Y     int    `json:"y"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.Text == "" && !body.Clear {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "text is required", nil)
		return
	}
	if s.refuseIfPaused(w) {
		return
	}
	x, y := -1, -1
	if body.X > 0 || body.Y > 0 {
		x, y = body.X, body.Y
	}
	if err := s.typeTextAt(r.Context(), body.Text, body.Clear, x, y); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "text injection failed", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "chars": len([]rune(body.Text))})
}

// handleIMEText forces the IME path, for fields that reject accessibility.
func (s *Server) handleIMEText(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text  string `json:"text"`
		Clear bool   `json:"clear"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.Text == "" && !body.Clear {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"text or clear is required", nil)
		return
	}
	if s.refuseIfPaused(w) {
		return
	}
	verdict, err := s.IME.Commit(r.Context(), body.Text, body.Clear)
	if err != nil {
		writeIMEError(w, err)
		return
	}
	s.Audit.Info("input: ime commit %d chars -> %s", len([]rune(body.Text)), verdict)
	s.noteAction("%s", describeText(body.Text))
	if rerr := s.IME.LastRestoreError(); rerr != nil {
		s.Audit.Warn("ime: could not hand the default keyboard back: %v", rerr)
	} else if back := s.IME.LastRestored(); back != "" {
		s.Audit.Info("ime: keyboard handed back to %s", back)
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":     verdict == "VERIFIED" || verdict == "ACCEPTED_UNVERIFIED",
		"result": verdict, "chars": len([]rune(body.Text)),
	})
}

// handleIMEKey sends a named key through the IME.
func (s *Server) handleIMEKey(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	switch strings.ToLower(body.Key) {
	case "enter", "back", "delete", "tab", "escape":
	default:
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"key must be enter, back, delete, tab or escape", nil)
		return
	}
	if s.refuseIfPaused(w) {
		return
	}
	verdict, err := s.IME.Key(r.Context(), strings.ToLower(body.Key))
	if err != nil {
		writeIMEError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "result": verdict})
}

// handleIMEState reports the IME lease and the active method.
func (s *Server) handleIMEState(w http.ResponseWriter, r *http.Request) {
	active, err := s.IME.Active(r.Context())
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "cannot read the active IME", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"active": active, "ours": s.Cfg.App.IMEService,
		"busy": s.IME.Busy(), "authority": s.Cfg.App.IMEProvider,
	})
}

// writeIMEError maps the IME failures onto the documented codes.
func writeIMEError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, ime.ErrBusy):
		httpx.WriteError(w, http.StatusConflict, "text_input_busy",
			"another text commit is in flight (the IME is a single global session)", err)
	case errors.Is(err, ime.ErrDisabled):
		httpx.WriteError(w, http.StatusConflict, httpx.CodeNotReady,
			"the device IME could not be switched to the harness IME", err)
	default:
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "text commit failed", err)
	}
}

// deviceLayer returns the device actions bound to the daemon's runner.
func (s *Server) deviceLayer() device.Layer { return device.Layer{Probe: s.Probe} }
