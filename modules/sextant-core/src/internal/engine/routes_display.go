package engine

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	sdisplay "sextant-core/internal/display"
	"sextant-core/internal/httpx"
)

// Display control.
//
// Virtual (overlay) displays are created by `cmd display create` on Android 13;
// content is captured by scrcpy-server, because `screencap -d` does not work for
// overlay displays on this ROM (verified: it only serves physical displays).

// handleListDisplays reports the displays the device currently has.
//
// Parsed from the "Display States:" block: this ROM does not print the
// DisplayInfo format the older code looked for, so matching that produced an
// empty list while the phone clearly had a display.
func (s *Server) handleListDisplays(w http.ResponseWriter, r *http.Request) {
	out, err := s.Probe.Run.Run(r.Context(), "dumpsys", "display")
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "dumpsys display failed", err)
		return
	}
	var displays []map[string]any
	var cur map[string]any
	flush := func() {
		if cur != nil {
			displays = append(displays, cur)
			cur = nil
		}
	}
	for _, line := range strings.Split(out, "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "Display Id=") {
			flush()
			cur = map[string]any{"display_id": strings.TrimSpace(strings.TrimPrefix(trimmed, "Display Id="))}
			continue
		}
		if cur == nil {
			continue
		}
		if strings.HasPrefix(trimmed, "Display State=") {
			cur["state"] = strings.TrimSpace(strings.TrimPrefix(trimmed, "Display State="))
			continue
		}
		if strings.HasPrefix(trimmed, "Display Brightness=") {
			cur["brightness"] = strings.TrimSpace(strings.TrimPrefix(trimmed, "Display Brightness="))
			continue
		}
	}
	flush()
	if displays == nil {
		displays = []map[string]any{}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"count": len(displays), "displays": displays})
}

// handleCreateDisplay creates an overlay display by starting scrcpy-server.
//
// `cmd display create` does not exist on this ROM, so the display is owned by a
// scrcpy-server process: cleanup=true makes it vanish when that process exits,
// and Destroy kills it. That is also why the response carries the process's
// socket — a caller that wants frames needs it.
func (s *Server) handleCreateDisplay(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name   string `json:"name"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
		DPI    int    `json:"dpi"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.Width <= 0 || body.Height <= 0 {
		wpx, hpx, err := s.physicalSize(r)
		if err != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable,
				"cannot determine the physical display size for the default", err)
			return
		}
		body.Width, body.Height = wpx, hpx
	}
	if body.DPI <= 0 {
		body.DPI = 320
	}
	ctx, cancel := contextWithTimeout(r, 20*time.Second)
	defer cancel()
	proc, err := s.Displays.Create(ctx, body.Name, body.Width, body.Height, body.DPI)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "display create failed", err)
		return
	}
	s.Audit.Info("display: created id=%d %dx%d dpi=%d socket=%s", proc.ID, proc.Width, proc.Height, proc.DPI, proc.Socket)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok": true, "display_id": proc.ID, "name": proc.Name,
		"width": proc.Width, "height": proc.Height, "dpi": proc.DPI,
		"socket": proc.Socket, "log": proc.LogPath(),
	})
}

// handleDestroyDisplay stops the scrcpy-server that owns the display.
func (s *Server) handleDestroyDisplay(w http.ResponseWriter, r *http.Request) {
	raw := r.PathValue("id")
	id, err := strconv.Atoi(raw)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "id must be a number", nil)
		return
	}
	proc, ok := sdisplay.Get(id)
	if !ok {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound,
			"no overlay display with that id (it may have exited)", nil)
		return
	}
	if err := proc.Destroy(); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "display destroy failed", err)
		return
	}
	s.Audit.Info("display: destroyed id=%d", id)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// physicalSize reads wm size for the default display.
func (s *Server) physicalSize(r *http.Request) (int, int, error) {
	out, err := s.Probe.Run.Run(r.Context(), "wm", "size")
	if err != nil {
		return 0, 0, err
	}
	for _, line := range strings.Split(out, "\n") {
		if !strings.Contains(line, "Physical size:") {
			continue
		}
		spec := strings.TrimSpace(strings.SplitN(line, "Physical size:", 2)[1])
		var wpx, hpx int
		if _, err := fmt.Sscanf(spec, "%dx%d", &wpx, &hpx); err != nil {
			return 0, 0, fmt.Errorf("unparsable wm size %q", spec)
		}
		return wpx, hpx, nil
	}
	return 0, 0, fmt.Errorf("wm size produced no physical size")
}

// handleFrame captures a frame and returns it as image bytes.
//
// D28: this is the agent's fast "look at the screen" call. Display 0 goes through
// the helper's UiAutomation frame as JPEG, optionally served from the recent-frame
// cache. A virtual display cannot be captured by `screencap -d` on this ROM, so it
// is refused explicitly instead of quietly returning the physical screen — which
// is what the old hand-off to handleScreenshot did.
func (s *Server) handleFrame(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	display, _ := strconv.Atoi(q.Get("display_id"))
	quality, _ := strconv.Atoi(q.Get("quality"))
	maxSize, _ := strconv.Atoi(q.Get("max_size"))
	useCache := q.Get("cached") == "1" || q.Get("cached") == "true"
	if display != 0 {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeUnsupported,
			"frame supports display 0; virtual displays stream through WebRTC", nil)
		return
	}
	if quality <= 0 || quality > 100 {
		quality = 80
	}
	if maxSize < 0 {
		maxSize = 0
	}
	restore, err := s.hideOverlayForCapture(r.Context())
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeNotReady,
			"cannot hide the overlay, so the frame would include it", err)
		return
	}
	if restore != nil {
		defer restore()
	}
	start := time.Now()
	data, width, height, captureMs, err := s.frameJPEG(r.Context(), quality, maxSize, useCache)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "frame capture failed", err)
		return
	}
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Sextant-Frame-Ms", strconv.FormatInt(time.Since(start).Milliseconds(), 10))
	w.Header().Set("X-Sextant-Capture-Ms", strconv.FormatInt(captureMs, 10))
	w.Header().Set("X-Sextant-Frame-Width", strconv.Itoa(width))
	w.Header().Set("X-Sextant-Frame-Height", strconv.Itoa(height))
	_, _ = w.Write(data)
}
