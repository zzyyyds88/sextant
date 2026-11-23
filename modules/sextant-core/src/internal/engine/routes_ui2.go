package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"sextant-core/internal/httpx"
	"sextant-core/internal/ui"
)

// OCR runs the ROM OCR engine over the current screen (or a region) and returns
// text blocks with bounds.
func (s *Server) handleOCR(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Region  map[string]int `json:"region"`
		Display int            `json:"display"`
		// Fresh forces a new capture. Without it a recent frame is reused: the
		// agent usually takes a frame and then asks what it says (D28).
		Fresh bool `json:"fresh"`
	}
	if r.ContentLength > 0 {
		if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
			return
		}
	}
	// The overlay must not be in the frame, for the same reason the screenshot
	// refuses to include it: OCR answers "what does this screen say", and the
	// agent's own pill says 「滑动 540,1900 → 540,500」. Measured on the device:
	// OCR of WeChat's chat page returned that exact line inside total_text, and
	// OCR of a note returned 「点「完成编辑」」 — both are the HUD, not the app.
	if restore, err := s.hideOverlayForCapture(r.Context()); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeNotReady,
			"cannot hide the overlay, so OCR would read the agent's own controls", err)
		return
	} else if restore != nil {
		defer restore()
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	if body.Display == 0 && !body.Fresh {
		if data, _, _, ok := s.recentFrame(frameCacheTTL); ok {
			if out, cacheErr := s.Helper.OCRPNG(ctx, 0, data, body.Region); cacheErr == nil {
				s.Audit.Info("ui: ocr display=0 cached_frame")
				httpx.WriteJSON(w, http.StatusOK, out)
				return
			}
		}
	}
	framePath := s.Paths.Tmp + "/ocr.jpg"
	_ = os.Remove(framePath)
	params := map[string]any{
		"display_id": body.Display,
		"format":     "jpeg",
		"quality":    80,
		"max_size":   0, // native size keeps OCR bounds in screen coordinates
	}
	if body.Display == 0 {
		params["return_frame"] = true
		params["output_path"] = framePath
	}
	if body.Region != nil {
		params["region"] = body.Region
	}
	var out map[string]any
	if err := s.Helper.Call(ctx, "ocr", params, &out); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable,
			"OCR unavailable: the root helper could not run the ROM engine", err)
		return
	}
	if f, ok := out["_frame"].(map[string]any); ok {
		delete(out, "_frame")
		if data, readErr := os.ReadFile(framePath); readErr == nil {
			s.rememberFrame(data, intOf(f["width"]), intOf(f["height"]))
		}
	}
	s.Audit.Info("ui: ocr display=%d", body.Display)
	httpx.WriteJSON(w, http.StatusOK, out)
}

// findRequest is shared by find and wait.
type findRequest struct {
	Text       string `json:"text"`
	ResourceID string `json:"resource_id"`
	Click      bool   `json:"click"`
	Timeout    int    `json:"timeout_ms"`
}

// handleFind locates a node by text or resource id and optionally taps it.
func (s *Server) handleFind(w http.ResponseWriter, r *http.Request) {
	if s.refuseIfPaused(w) {
		return
	}
	var body findRequest
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.Text == "" && body.ResourceID == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"text or resource_id is required", nil)
		return
	}
	timeout := time.Duration(body.Timeout) * time.Millisecond
	if timeout <= 0 {
		timeout = 5 * time.Second
	}
	// A find that may click covers both the tree and the OCR fallback; installing
	// the guard once here is what keeps the fallback from tapping through the
	// overlay's Stop control.
	if body.Click {
		if s.refuseIfPaused(w) || s.refuseIfHandedOver(w) {
			return
		}
		restore, guardErr := s.passThroughForInput(r.Context())
		if guardErr != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeNotReady,
				"cannot pass touches through the overlay", guardErr)
			return
		}
		if restore != nil {
			defer restore()
		}
	}
	deadline := time.Now().Add(timeout)
	var found ui.Node
	var ok bool
	var candidates []ui.Candidate
	for {
		nodes, _, err := s.Tree.Nodes(r.Context(), 0)
		if err != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "ui dump failed", err)
			return
		}
		if body.ResourceID != "" {
			found, ok = ui.FindByResourceID(nodes, body.ResourceID)
		} else {
			candidates = ui.FindCandidates(nodes, body.Text)
			if len(candidates) > 0 {
				found, ok = candidates[0].Node, true
			}
		}
		if ok || time.Now().After(deadline) {
			break
		}
		select {
		case <-r.Context().Done():
			return
		case <-time.After(300 * time.Millisecond):
		}
	}
	if !ok && body.Text != "" {
		// The accessibility tree does not contain app-drawn text (games, canvases,
		// WeChat's custom widgets). Fall back to OCR so a visible label is findable,
		// and say which mechanism answered. The overlay is hidden first for the
		// same reason handleOCR hides it: the agent's own pill says
		// 「点「...」」 and must not become a match.
		restore, hideErr := s.hideOverlayForCapture(r.Context())
		if hideErr != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeNotReady,
				"cannot hide the overlay, so OCR would read the agent's own controls", hideErr)
			return
		}
		if restore != nil {
			defer restore()
		}
		ocrCtx, cancelOCR := contextWithTimeout(r, 60*time.Second)
		out, ocrErr := s.Helper.OCR(ocrCtx, 0, nil)
		cancelOCR()
		if ocrErr == nil {
			if line, hit := findOCRLine(out, body.Text); hit {
				x, y := lineCenter(line)
				resp := map[string]any{
					"ok": true, "source": "ocr", "text": strOf(line["text"]),
					"x": x, "y": y, "bounds": line["bounds"],
				}
				if body.Click {
					seed := s.requestSeed("")
					px, py, release, _, _, paceErr := s.injectionPoint(r.Context(), 0, nil, x, y, seed, requestIDFor(r), 0)
					if paceErr != nil {
						httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "cannot pace the tap", paceErr)
						return
					}
					tapErr := s.Device.Tap(r.Context(), px, py, 0)
					release()
					if tapErr != nil {
						httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "tap failed", tapErr)
						return
					}
					resp["clicked"] = true
					resp["x"], resp["y"] = px, py
					s.Audit.Info("ui: ocr find+click %s at %d,%d seed=%s", describeTarget(body.Text, body.ResourceID), px, py, seed)
					s.noteAction("%s", stepLabel(body.Text, body.ResourceID, px, py))
				}
				httpx.WriteJSON(w, http.StatusOK, resp)
				return
			}
		}
	}
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "node not found", nil)
		return
	}
	resp := map[string]any{
		"ok": true, "source": "tree",
		"node_id":     fmt.Sprintf("n_%d", found.Index),
		"text":        found.Attrs["text"],
		"resource_id": found.Attrs["resource-id"],
		"class":       found.Attrs["class"],
		"bounds":      found.Bounds,
		"selected":    triState(found.Attrs["selected"]),
		"visible":     triState(found.Attrs["visible"]),
		"enabled":     triState(found.Attrs["enabled"]),
	}
	if len(candidates) > 0 {
		resp["candidates"] = candidateList(candidates, 10)
	}
	if body.Click {
		x, y := found.Center()
		// Refuse to click a point that is not on the screen.
		//
		// WeChat keeps a floating-window copy of a conversation row at negative
		// coordinates and the tree lists it first. Tapping (-107, 683) does nothing,
		// and the old code answered `{"ok": true, "clicked": true}` — the worst kind of
		// reply, because it is indistinguishable from success. A click that cannot land
		// has to be an error, so the caller retries instead of moving on.
		if x < 0 || y < 0 {
			httpx.WriteError(w, http.StatusConflict, httpx.CodeConflict,
				"the matched node is off screen, so a tap there would hit nothing",
				fmt.Errorf("node bounds %v, centre (%d,%d)", found.Bounds, x, y))
			return
		}
		// Prefer the node's own click over a blind coordinate tap.
		//
		// A tap at the centre of a node is a guess about what is on top at that point.
		// On screens with a floating window (WeChat's 浮窗 copy of a row) or a scrim,
		// the guess is wrong and nothing in the reply says so: the caller gets
		// `ok` and the screen did not move. The helper can click the node itself, or
		// the nearest clickable ancestor that contains it — which is what a finger
		// does — and the coordinate tap stays as the fallback.
		if via := s.clickNode(r.Context(), body.Text, body.ResourceID, false, 0, x, y); via != "" && via != "none" {
			resp["clicked"] = true
			resp["via"] = via
			s.Audit.Info("ui: find+click %s at %d,%d via %s", describeTarget(body.Text, body.ResourceID), x, y, via)
			s.noteAction("%s", stepLabel(body.Text, body.ResourceID, x, y))
			httpx.WriteJSON(w, http.StatusOK, resp)
			return
		}
		seed := s.requestSeed("")
		bounds := found.Bounds
		px, py, release, _, _, paceErr := s.injectionPoint(r.Context(), 0, &bounds, x, y, seed, requestIDFor(r), 0)
		if paceErr != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "cannot pace the tap", paceErr)
			return
		}
		tapErr := s.Device.Tap(r.Context(), px, py, 0)
		release()
		if tapErr != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "tap failed", tapErr)
			return
		}
		resp["clicked"] = true
		resp["x"], resp["y"] = px, py
		resp["seed"] = seed
		s.Audit.Info("ui: find+click %s at %d,%d seed=%s", describeTarget(body.Text, body.ResourceID), px, py, seed)
		s.noteAction("%s", stepLabel(found.Attrs["text"], body.ResourceID, px, py))
	}
	httpx.WriteJSON(w, http.StatusOK, resp)
}

// candidateList renders the ranked matches a find query saw, so a caller can
// tell "there was exactly one" from "the first of eleven".
func candidateList(cs []ui.Candidate, max int) []map[string]any {
	if len(cs) > max {
		cs = cs[:max]
	}
	out := make([]map[string]any, 0, len(cs))
	for _, c := range cs {
		out = append(out, map[string]any{
			"node_id":     fmt.Sprintf("n_%d", c.Node.Index),
			"text":        c.Node.Attrs["text"],
			"resource_id": c.Node.Attrs["resource-id"],
			"class":       c.Node.Attrs["class"],
			"bounds":      c.Node.Bounds,
			"match":       c.Match,
			"selected":    triState(c.Node.Attrs["selected"]),
			"visible":     triState(c.Node.Attrs["visible"]),
			"enabled":     triState(c.Node.Attrs["enabled"]),
			"clickable":   triState(c.Node.Attrs["clickable"]),
			"depth":       c.Node.Depth,
		})
	}
	return out
}

// handleWait blocks until a condition holds.
func (s *Server) handleWait(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text       string `json:"text"`
		ResourceID string `json:"resource_id"`
		Absent     bool   `json:"absent"`
		Timeout    int    `json:"timeout_ms"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.Text == "" && body.ResourceID == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"text or resource_id is required", nil)
		return
	}
	timeout := time.Duration(body.Timeout) * time.Millisecond
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	// D28: wake on accessibility events instead of polling. The stream may already
	// be running for /v1/ui/events; starting it here is idempotent at the helper and
	// guarded by eventsOnce. If it cannot start, the timeout below is the fallback.
	events, cancelEvents := s.Helper.SubscribeEvents()
	defer cancelEvents()
	if err := s.Helper.RetainEvents(r.Context()); err != nil {
		s.Audit.Info("ui: wait falls back to polling: %v", err)
	} else {
		defer s.Helper.ReleaseEvents()
	}
	deadline := time.Now().Add(timeout)
	for {
		nodes, _, err := s.Tree.Nodes(r.Context(), 0)
		if err != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "ui dump failed", err)
			return
		}
		var present bool
		if body.ResourceID != "" {
			_, present = ui.FindByResourceID(nodes, body.ResourceID)
		} else {
			_, present = ui.FindByText(nodes, body.Text)
		}
		if present != body.Absent {
			httpx.WriteJSON(w, http.StatusOK, map[string]any{
				"ok": true, "present": present, "waited_ms": timeout.Milliseconds() - time.Until(deadline).Milliseconds(),
			})
			return
		}
		if time.Now().After(deadline) {
			httpx.WriteError(w, http.StatusGatewayTimeout, httpx.CodeTimeout,
				"condition not met before the timeout", nil)
			return
		}
		select {
		case <-r.Context().Done():
			return
		case <-events:
			// A window changed. Give the frame one turn to settle before re-reading;
			// a burst of animation events must not spin the tree reader.
			select {
			case <-r.Context().Done():
				return
			case <-time.After(30 * time.Millisecond):
			}
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// handleChain runs a sequence of simple steps: tap, key, text, wait_ms.
//
// It exists so a multi-step action is one round trip instead of five, which
// matters when the phone is on a mobile link. It is deliberately not a scripting
// language: no branching, no variables.
func (s *Server) handleChain(w http.ResponseWriter, r *http.Request) {
	if s.refuseIfPaused(w) {
		return
	}
	var body struct {
		Steps []struct {
			Op   string `json:"op"`
			X    int    `json:"x"`
			Y    int    `json:"y"`
			Key  string `json:"key"`
			Text string `json:"text"`
			MS   int    `json:"ms"`
		} `json:"steps"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if len(body.Steps) == 0 || len(body.Steps) > 50 {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"steps must contain 1..50 entries", nil)
		return
	}
	// A chain contains touch/key/text steps; one guard covers the whole sequence
	// instead of each branch remembering to install its own.
	if s.refuseIfPaused(w) || s.refuseIfHandedOver(w) {
		return
	}
	restore, guardErr := s.passThroughForInput(r.Context())
	if guardErr != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeNotReady,
			"cannot pass touches through the overlay", guardErr)
		return
	}
	if restore != nil {
		defer restore()
	}
	done := 0
	chainSeed := s.requestSeed("")
	for i, st := range body.Steps {
		var err error
		switch st.Op {
		case "tap":
			px, py, release, _, _, paceErr := s.injectionPoint(r.Context(), 0, nil, st.X, st.Y, chainSeed, requestIDFor(r), i)
			if paceErr != nil {
				httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "cannot pace the tap", paceErr)
				return
			}
			err = s.Device.Tap(r.Context(), px, py, 0)
			release()
			if err == nil {
				s.noteAction("%s", stepLabel("", "", px, py))
			}
		case "key":
			err = s.Device.Key(r.Context(), st.Key, 0)
		case "text":
			err = s.typeText(r.Context(), st.Text)
		case "wait_ms":
			ms := st.MS
			if ms <= 0 || ms > 30000 {
				ms = 500
			}
			select {
			case <-r.Context().Done():
				return
			case <-time.After(time.Duration(ms) * time.Millisecond):
			}
		default:
			httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
				"unknown step op: "+st.Op+" (tap|key|text|wait_ms)", nil)
			return
		}
		if err != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable,
				"chain failed at step "+itoa(done), err)
			return
		}
		done++
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "steps_done": done})
}

// handleNotificationEvents streams notifications as SSE.
//
// The helper already pushes NOTIFICATION frames; this exposes them over the same
// protocol as the other streams instead of making a client poll. A poll-based
// client cannot tell "no new notification" from "the listener died", which is
// exactly what a notification-driven workflow needs to know.
func (s *Server) handleNotificationEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "streaming unsupported", nil)
		return
	}
	ctx := r.Context()
	ch, cancel := s.Helper.SubscribeEvents()
	defer cancel()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	// Flush the headers before waiting for anything: net/http buffers them, and a
	// client that waits for headers would time out on a perfectly healthy stream.
	flusher.Flush()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-ch:
			if !ok {
				return
			}
			if ev.Event != "NOTIFICATION" {
				continue
			}
			frame, err := jsonFrame(ev)
			if err != nil {
				continue
			}
			if _, err := w.Write(frame); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

// handleEvents streams accessibility events as SSE.
//
// The stream is bounded: it ends when the client disconnects, and the daemon
// re-arms the helper's event pump on demand rather than leaving it running.
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "streaming unsupported", nil)
		return
	}
	ctx := r.Context()
	if err := s.Helper.RetainEvents(ctx); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable,
			"accessibility events unavailable", err)
		return
	}
	defer s.Helper.ReleaseEvents()
	events, cancelEvents := s.Helper.SubscribeEvents()
	defer cancelEvents()

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	// Flush the headers before waiting for anything: net/http buffers them, and a
	// client that waits for headers would time out on a perfectly healthy stream.
	flusher.Flush()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-events:
			if !ok {
				return
			}
			payload, err := json.Marshal(ev)
			if err != nil {
				continue
			}
			if _, err := fmt.Fprintf(w, "event: %s\ndata: %s\n\n", ev.Event, payload); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

var _ = strings.TrimSpace

// describeTarget names what a find was looking for.
//
// Printing only the text made the audit line read `find+click ""` whenever the
// caller searched by resource id, which is the common case for a node whose text
// is ambiguous (WeChat's contact row is also the chat title and the draft row).
func describeTarget(text, resourceID string) string {
	switch {
	case text != "" && resourceID != "":
		return fmt.Sprintf("%q (id %s)", text, resourceID)
	case text != "":
		return fmt.Sprintf("%q", text)
	case resourceID != "":
		return "id " + resourceID
	}
	return "(empty)"
}
