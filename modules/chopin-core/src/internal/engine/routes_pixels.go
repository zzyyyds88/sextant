package engine

import (
	"bytes"
	"context"
	"fmt"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"net/http"
	"os"
	"strings"
	"time"

	"chopin-core/internal/httpx"
)

// Pixel-level UI primitives.
//
// They exist because "the screen looks right" is not checkable from a UI tree: a
// rendering bug, a dimmed panel or a colour-coded status can only be answered by
// reading pixels. Each one captures a fresh frame through the same path as
// /v1/ui/screenshot, so what they report is what a human would see.

// captureImage grabs the screen into a decoded image.
//
// The overlay is hidden first (DESIGN 9.4): every pixel primitive answers "what
// would a human see", and a human does not see the agent's own controls floating
// over the app. Counting the pill's pixels as app content has produced wrong
// colour and coverage answers before, which is a silent failure.
//
// D28: the capture goes through the helper (JPEG) and reuses the recent frame
// when one is fresh. The image is still decoded here because the colour
// primitives need pixel access; the win is skipping the capture and PNG encode.
func (s *Server) captureImage(ctx context.Context) (image.Image, string, error) {
	if restore, err := s.hideOverlayForCapture(ctx); err != nil {
		return nil, "", err
	} else if restore != nil {
		defer restore()
	}
	if data, _, _, ok := s.recentFrame(frameCacheTTL); ok {
		if img, _, err := image.Decode(bytes.NewReader(data)); err == nil {
			return img, "frame-cache", nil
		}
	}
	path := s.Paths.Tmp + "/probe.jpg"
	_ = os.Remove(path)
	if _, err := s.Helper.Screenshot(ctx, map[string]any{
		"display_id": 0, "format": "jpeg", "quality": 85, "max_size": 0, "output_path": path,
	}); err != nil {
		return nil, "", fmt.Errorf("helper capture: %w", err)
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, "", fmt.Errorf("open capture: %w", err)
	}
	defer f.Close()
	img, _, err := image.Decode(f)
	if err != nil {
		return nil, "", fmt.Errorf("decode capture: %w", err)
	}
	return img, path, nil
}

// cropImage narrows an image to a region; an empty region means the whole frame.
func cropImage(img image.Image, region map[string]int) (image.Image, error) {
	if len(region) == 0 {
		return img, nil
	}
	b := img.Bounds()
	left := maxInt(region["left"], 0)
	top := maxInt(region["top"], 0)
	right := region["right"]
	bottom := region["bottom"]
	if right <= 0 || right > b.Max.X {
		right = b.Max.X
	}
	if bottom <= 0 || bottom > b.Max.Y {
		bottom = b.Max.Y
	}
	if right <= left || bottom <= top {
		return nil, fmt.Errorf("region is empty: [%d,%d][%d,%d]", left, top, right, bottom)
	}
	if c, ok := img.(interface {
		SubImage(r image.Rectangle) image.Image
	}); ok {
		return c.SubImage(image.Rect(left, top, right, bottom)), nil
	}
	return img, nil
}

// handleColorProbe reports how much of a region matches a colour.
func (s *Server) handleColorProbe(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Region    map[string]int `json:"region"`
		R         int            `json:"r"`
		G         int            `json:"g"`
		B         int            `json:"b"`
		Tolerance int            `json:"tolerance"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.Tolerance <= 0 {
		body.Tolerance = 24
	}
	ctx, cancel := contextWithTimeout(r, 30*time.Second)
	defer cancel()
	img, path, err := s.captureImage(ctx)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "capture failed", err)
		return
	}
	img, err = cropImage(img, body.Region)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid region", err)
		return
	}
	ratio, mean, pixels := colorRatio(img, body.R, body.G, body.B, body.Tolerance)
	s.Audit.Info("ui: color_probe ratio=%.3f pixels=%d", ratio, pixels)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"hit": ratio > 0.5, "ratio": ratio, "pixels": pixels, "mean": mean,
		"target":    map[string]int{"r": body.R, "g": body.G, "b": body.B},
		"tolerance": body.Tolerance, "path": path,
	})
}

// handlePixelStats summarises the colour distribution of a region.
func (s *Server) handlePixelStats(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Region map[string]int `json:"region"`
		Sample int            `json:"sample_stride"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	stride := body.Sample
	if stride <= 0 {
		stride = 2
	}
	ctx, cancel := contextWithTimeout(r, 30*time.Second)
	defer cancel()
	img, path, err := s.captureImage(ctx)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "capture failed", err)
		return
	}
	img, err = cropImage(img, body.Region)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid region", err)
		return
	}
	stats := pixelStats(img, stride)
	stats["path"] = path
	httpx.WriteJSON(w, http.StatusOK, stats)
}

// handlePanelRead extracts the screen's text with a countdown and the action
// button that follows it.
//
// This is the "what does this screen say, and what do I press next" primitive:
// OCR text plus the countdown value plus the button to press, all from one
// capture, so the three cannot disagree.
func (s *Server) handlePanelRead(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Display int `json:"display_id"`
	}
	if r.ContentLength > 0 {
		if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
			return
		}
	}
	// Same frame rule as the screenshot: the overlay is not app content, and this
	// response tells the caller what to press next — the HUD's own 「点「…」」 line
	// would be read back as part of the panel.
	if restore, err := s.hideOverlayForCapture(r.Context()); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeNotReady,
			"cannot hide the overlay, so the panel read would include the agent's own controls", err)
		return
	} else if restore != nil {
		defer restore()
	}
	ctx, cancel := contextWithTimeout(r, 60*time.Second)
	defer cancel()
	out, err := s.Helper.OCR(ctx, body.Display, nil)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "OCR unavailable", err)
		return
	}
	texts, countdown := extractTextAndCountdown(out)
	res := map[string]any{
		"ok": true, "lines": texts, "text": strings.Join(texts, ""),
		"countdown_s": countdown,
	}
	if btn, ok := findActionButton(out); ok {
		res["button"] = btn
	}
	httpx.WriteJSON(w, http.StatusOK, res)
}

// handleWaitResult waits for a condition on the screen and returns the frame that
// satisfied it.
//
// The difference from /v1/ui/wait: this returns what the screen said once the
// condition held, so a caller does not have to capture again (and cannot observe
// a different state than the one it waited for).
func (s *Server) handleWaitResult(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Text       string `json:"text"`
		ResourceID string `json:"resource_id"`
		Absent     bool   `json:"absent"`
		Timeout    int    `json:"timeout_ms"`
		Screenshot bool   `json:"screenshot"`
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
		timeout = 15 * time.Second
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
			_, present = findNodeByResourceID(nodes, body.ResourceID)
		} else {
			_, present = findNodeByText(nodes, body.Text)
		}
		if present != body.Absent {
			res := map[string]any{"ok": true, "present": present}
			if body.Screenshot {
				path := s.Paths.Tmp + "/wait-result.png"
				_ = os.Remove(path)
				if _, err := s.Probe.Run.Run(r.Context(), "screencap", "-p", path); err == nil {
					res["path"] = path
				}
			}
			httpx.WriteJSON(w, http.StatusOK, res)
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
		case <-time.After(300 * time.Millisecond):
		}
	}
}

// colorRatio returns the fraction of sampled pixels within tolerance, the mean
// colour, and the number of pixels examined.
func colorRatio(img image.Image, tr, tg, tb, tol int) (float64, map[string]int, int) {
	b := img.Bounds()
	total, hit := 0, 0
	var sr, sg, sb int64
	for y := b.Min.Y; y < b.Max.Y; y += 2 {
		for x := b.Min.X; x < b.Max.X; x += 2 {
			cr, cg, cb, _ := img.At(x, y).RGBA()
			r8, g8, b8 := int(cr>>8), int(cg>>8), int(cb>>8)
			sr += int64(r8)
			sg += int64(g8)
			sb += int64(b8)
			total++
			if absInt(r8-tr) <= tol && absInt(g8-tg) <= tol && absInt(b8-tb) <= tol {
				hit++
			}
		}
	}
	mean := map[string]int{}
	ratio := 0.0
	if total > 0 {
		ratio = float64(hit) / float64(total)
		mean = map[string]int{"r": int(sr / int64(total)), "g": int(sg / int64(total)), "b": int(sb / int64(total))}
	}
	return ratio, mean, total
}

// pixelStats summarises brightness, dark-pixel share and colour variety.
func pixelStats(img image.Image, stride int) map[string]any {
	b := img.Bounds()
	total := 0
	nearBlack := 0
	var sumLuma int64
	colors := map[uint32]struct{}{}
	var minL, maxL = 255, 0
	for y := b.Min.Y; y < b.Max.Y; y += stride {
		for x := b.Min.X; x < b.Max.X; x += stride {
			cr, cg, cb, _ := img.At(x, y).RGBA()
			r8, g8, b8 := int(cr>>8), int(cg>>8), int(cb>>8)
			luma := (299*r8 + 587*g8 + 114*b8) / 1000
			sumLuma += int64(luma)
			if luma < 8 {
				nearBlack++
			}
			if luma < minL {
				minL = luma
			}
			if luma > maxL {
				maxL = luma
			}
			colors[uint32(r8)<<16|uint32(g8)<<8|uint32(b8)] = struct{}{}
			total++
		}
	}
	out := map[string]any{"pixels": total, "distinct_colors": len(colors), "bounds": map[string]int{
		"width": b.Max.X - b.Min.X, "height": b.Max.Y - b.Min.Y,
	}}
	if total > 0 {
		out["mean_luma"] = int(sumLuma / int64(total))
		out["near_black"] = nearBlack
		out["dark_ratio"] = float64(nearBlack) / float64(total)
		out["min_luma"] = minL
		out["max_luma"] = maxL
		// A fully black capture is the signature of a broken capture path, not of a
		// dark screen: say so instead of letting the caller guess.
		out["looks_black"] = nearBlack == total
	}
	return out
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
