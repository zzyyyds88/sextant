package engine

import (
	"fmt"
	"net/http"
	"os"
	"time"

	"chopin-core/internal/httpx"
	"chopin-core/internal/ui"
)

// handleObserve answers one perception question in one capture.
//
// D28: the agent's loop was paying for a capture per observation — a screenshot,
// another for OCR, another for pixels. One capture now feeds all of them, and
// the response says how long each part took so a slow stage is visible rather
// than guessed at.
func (s *Server) handleObserve(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Display      int            `json:"display"`
		Tree         bool           `json:"tree"`
		OCR          bool           `json:"ocr"`
		Pixels       bool           `json:"pixels"`
		Frame        bool           `json:"frame"`
		Quality      int            `json:"quality"`
		MaxSize      int            `json:"max_size"`
		SampleStride int            `json:"sample_stride"`
		Region       map[string]int `json:"region"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if !body.Tree && !body.OCR && !body.Pixels && !body.Frame {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"choose at least one of tree, ocr, pixels, frame", nil)
		return
	}
	if body.Display != 0 {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeUnsupported,
			"observe supports display 0 only; use the display endpoints for virtual displays", nil)
		return
	}
	if body.Quality <= 0 || body.Quality > 100 {
		body.Quality = 80
	}
	if body.MaxSize < 0 {
		body.MaxSize = 0
	}
	if body.SampleStride <= 0 {
		body.SampleStride = 4
	}
	frameID := fmt.Sprintf("f_%d", time.Now().UnixNano())
	capturedAt := time.Now().UTC().Format(time.RFC3339Nano)
	ctx, cancel := contextWithTimeout(r, 60*time.Second)
	defer cancel()

	// The overlay must not be in the frame (DESIGN 9.4).
	restore, err := s.hideOverlayForCapture(ctx)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeNotReady,
			"cannot hide the overlay, so the observation would include it", err)
		return
	}
	if restore != nil {
		defer restore()
	}

	params := map[string]any{
		"display_id":    0,
		"tree":          body.Tree,
		"ocr":           body.OCR,
		"pixels":        body.Pixels,
		"frame":         body.Frame,
		"quality":       body.Quality,
		"max_size":      body.MaxSize,
		"sample_stride": body.SampleStride,
	}
	if body.Region != nil {
		params["region"] = body.Region
	}
	framePath := ""
	if body.Frame {
		framePath = s.Paths.Tmp + "/observe.jpg"
		_ = os.Remove(framePath)
		params["output_path"] = framePath
	}
	out, err := s.Helper.Observe(ctx, params)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "observe failed", err)
		return
	}
	// D33: the observed tree is published flat with node_id/parent_node_id and
	// cached under frame_id, so /v1/ui/diff can compare it with a later frame.
	if body.Tree {
		if raw, ok := out["tree"].(map[string]any); ok {
			nodes, truncated, decodeErr := ui.NodesFromHelper(raw)
			if decodeErr != nil {
				httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "observe tree decode failed", decodeErr)
				return
			}
			flat := ui.Flatten(nodes)
			s.treeFrames.put(frameID, flat, time.Now())
			wire := make([]map[string]any, 0, len(flat))
			for _, n := range flat {
				wire = append(wire, flatNodeJSON(n))
			}
			out["tree"] = map[string]any{"count": len(flat), "nodes": wire, "truncated": truncated}
		}
	}
	if body.Frame && framePath != "" {
		if data, readErr := os.ReadFile(framePath); readErr == nil {
			if f, ok := out["frame"].(map[string]any); ok {
				s.rememberFrame(data, intOf(f["width"]), intOf(f["height"]))
				s.Metrics.Frame(len(data))
			}
		}
	}
	s.Metrics.ObserveCall()
	s.Audit.Info("ui: observe %s tree=%v ocr=%v pixels=%v frame=%v total=%dms",
		frameID, body.Tree, body.OCR, body.Pixels, body.Frame, intOf(out["total_ms"]))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok":               true,
		"frame_id":         frameID,
		"captured_at":      capturedAt,
		"coordinate_space": "screen_px",
		"observe":          out,
	})
}
