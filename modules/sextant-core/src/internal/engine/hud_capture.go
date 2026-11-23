package engine

import (
	"context"
	"time"
)

// hideOverlayForCapture asks the overlay to hide itself and returns the function
// that brings it back.
//
// The return contract matters more than the mechanism: a nil restore with a nil
// error means there was no overlay to hide (the service is disabled), which is a
// clean capture. An error means the caller must NOT capture, because it cannot
// prove the frame is free of the agent's own controls. "Probably fine" is not one
// of the outcomes: a screenshot that silently contains the Stop button is a
// screenshot the agent will reason about as if it were the app.
func (s *Server) hideOverlayForCapture(ctx context.Context) (func(), error) {
	// The instruction goes over the SSE channel the overlay already consumes.
	// The local socket was tried first and abandoned: MIUI rebinds the
	// accessibility service while the app is alive, two instances race for the
	// abstract name, and the loser holds EADDRINUSE for the rest of the process —
	// which would make every capture fail on a device whose overlay works fine.
	if restore := s.HUD.CaptureWindow(s.overlaySettle()); restore != nil {
		return restore, nil
	}
	return nil, nil
}

// passThroughForInput makes the overlay untouchable while a stroke is injected, so
// the tap is never swallowed by the Stop control. It rides the same channel and
// for the same reason as the capture instruction; D28 makes the wait ACK-driven,
// with the settle duration acting only as the timeout ceiling.
func (s *Server) passThroughForInput(ctx context.Context) (func(), error) {
	if !s.HUD.Snapshot().Connected {
		return nil, nil
	}
	start := time.Now()
	restore := s.HUD.PassThroughWindow(s.overlaySettle(), true)
	s.Audit.Info("input: pass_through ready in %dms", time.Since(start).Milliseconds())
	return restore, nil
}

// overlaySettle is how long the overlay gets to apply an instruction it received
// over SSE. It is deliberately short: long enough for one main-loop turn plus the
// window transaction, short enough not to dominate a capture.
func (s *Server) overlaySettle() time.Duration {
	if s.Cfg.Hud.CaptureSettleMs > 0 {
		return time.Duration(s.Cfg.Hud.CaptureSettleMs) * time.Millisecond
	}
	// D28: the ACK normally resolves the wait in ~100ms. This is the ceiling for
	// an overlay that does not acknowledge (older APK), not the expected cost.
	return 120 * time.Millisecond
}
