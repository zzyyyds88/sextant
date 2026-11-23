package engine

import (
	"net/http"
	"time"

	"sextant-core/internal/httpx"
)

// HUD endpoints. They are the app's control channel and are therefore
// loopback-only by contract (the app token has LocalOnly set): a remote caller
// cannot press the device's own Stop button.

// handleHUDHeartbeat reports the overlay is alive and returns what to render.
func (s *Server) handleHUDHeartbeat(w http.ResponseWriter, r *http.Request) {
	if !httpx.IsLoopback(r) {
		httpx.WriteError(w, http.StatusForbidden, httpx.CodeLoopbackOnly,
			"the HUD channel is loopback-only", nil)
		return
	}
	st := s.HUD.Heartbeat()
	active := s.Sessions.List()
	out := st
	out.Active = make([]string, 0, len(active))
	for _, sess := range active {
		out.Active = append(out.Active, sess.Agent)
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// handleHUDPause toggles pause. While paused the daemon refuses new write
// sessions: a human is looking at the screen and does not want it to move.
func (s *Server) handleHUDPause(w http.ResponseWriter, r *http.Request) {
	if !httpx.IsLoopback(r) {
		httpx.WriteError(w, http.StatusForbidden, httpx.CodeLoopbackOnly,
			"the HUD channel is loopback-only", nil)
		return
	}
	var body struct {
		Paused bool `json:"paused"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	st := s.HUD.Pause(body.Paused)
	s.Audit.Info("hud: paused=%v", body.Paused)
	httpx.WriteJSON(w, http.StatusOK, st)
}

// handleHUDStop is the human escape hatch: it releases every session at once.
func (s *Server) handleHUDStop(w http.ResponseWriter, r *http.Request) {
	if !httpx.IsLoopback(r) {
		httpx.WriteError(w, http.StatusForbidden, httpx.CodeLoopbackOnly,
			"the HUD channel is loopback-only", nil)
		return
	}
	var body struct {
		Reason string `json:"reason"`
	}
	if r.ContentLength > 0 {
		if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
			return
		}
	}
	st := s.HUD.Stop(body.Reason)
	s.publishSessions()
	s.Audit.Warn("hud: stop pressed (%s); sessions released", st.StopReason)
	httpx.WriteJSON(w, http.StatusOK, st)
}

// handleHUDAck resolves a capture/pass-through instruction.
//
// D28: the overlay applies the instruction and acknowledges it here, so the
// daemon can continue immediately instead of always paying the settle delay.
// An unknown or already-resolved request is not an error: heartbeat state frames
// repeat the same request_id, and duplicate ACKs are harmless.
func (s *Server) handleHUDAck(w http.ResponseWriter, r *http.Request) {
	if !httpx.IsLoopback(r) {
		httpx.WriteError(w, http.StatusForbidden, httpx.CodeLoopbackOnly,
			"the HUD channel is loopback-only", nil)
		return
	}
	var body struct {
		RequestID string `json:"request_id"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.RequestID == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"request_id is required", nil)
		return
	}
	accepted := s.HUD.Ack(body.RequestID)
	s.Audit.Info("hud: ack %s accepted=%v", body.RequestID, accepted)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok": true, "accepted": accepted,
	})
}

// handleHUDEvents streams HUD state as SSE.
func (s *Server) handleHUDEvents(w http.ResponseWriter, r *http.Request) {
	if !httpx.IsLoopback(r) {
		httpx.WriteError(w, http.StatusForbidden, httpx.CodeLoopbackOnly,
			"the HUD channel is loopback-only", nil)
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "streaming unsupported", nil)
		return
	}
	ch, cancel := s.HUD.Subscribe()
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
		case <-r.Context().Done():
			s.HUD.Disconnected()
			return
		case st, ok := <-ch:
			if !ok {
				return
			}
			if _, err := w.Write(hudEncode(st)); err != nil {
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

// handleHUDState reports the overlay state plus a live probe of the local socket.
//
// The probe is part of the answer on purpose: "connected" only says heartbeats
// arrive, while the socket says whether the daemon can still make the overlay hide
// for a capture. Those are different failures, and a caller debugging a screenshot
// with a floating pill in it needs to know which one it has.
func (s *Server) handleHUDState(w http.ResponseWriter, r *http.Request) {
	if !httpx.IsLoopback(r) {
		httpx.WriteError(w, http.StatusForbidden, httpx.CodeLoopbackOnly,
			"the HUD channel is loopback-only", nil)
		return
	}
	st := s.HUD.Snapshot()
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		// The state fields are inlined as well as nested: the app reads a flat
		// object from the SSE stream, and one shape for both keeps the client simple.
		"connected": st.Connected, "visible": st.Visible, "paused": st.Paused,
		"stopped": st.Stopped, "agent": st.Agent, "session_id": st.SessionID,
		"display": st.Display, "updated_at": st.UpdatedAt,
		"capture_hidden": st.CaptureHidden, "pass_through": st.PassThrough,
		"state": st,
	})
}
