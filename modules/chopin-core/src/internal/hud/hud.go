// Package hud is the device-side safety layer.
//
// The HUD is an overlay drawn by the APK inside the accessibility service. It is
// the only thing on the phone that can tell a human "an agent is driving right
// now, tap Stop". Its state therefore lives here, in the daemon, and not in the
// app: if the app is killed the daemon still knows a session is unguarded and can
// refuse to continue rather than silently acting on an unwatched screen.
//
// Everything in this package is reachable only from loopback with a hud-scoped
// token (the app's own token), never from the PC.
package hud

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// State is what the overlay renders.
type State struct {
	Connected bool `json:"connected"`
	// Visible is what the overlay keys its pill on.
	//
	// It is NOT the same as Connected: Connected answers "is the overlay alive",
	// Visible answers "should a human see a pill right now". The APK renders the
	// pill only for visible=true, so a daemon that never sets it produces a fully
	// provisioned overlay that draws nothing — which is exactly what happened
	// before this field existed (hud.json was missing too, but fixing only that
	// would have produced a connected overlay with no pixels).
	Visible bool `json:"visible"`
	// CaptureHidden tells the overlay to hide itself while the daemon takes a
	// screenshot or injects a stroke.
	//
	// This is an INSTRUCTION over the SSE channel the overlay already reads, not a
	// request over the local socket. The socket was the obvious design and it does
	// not survive MIUI's accessibility lifecycle: the service is rebound, two
	// instances race for the abstract name, and the loser gets EADDRINUSE — after
	// which every capture would have to fail even though the overlay is healthy.
	// The SSE channel is the one that demonstrably works (it is how the pill gets
	// its text), so the instruction rides it.
	CaptureHidden bool `json:"capture_hidden"`
	// PassThrough makes the overlay ignore touches while a stroke is injected.
	PassThrough bool `json:"pass_through"`
	// RequestID identifies the instruction currently waiting for the overlay's
	// acknowledgement (D28). Empty when nothing is pending. The overlay applies
	// capture_hidden/pass_through and then POSTs /v1/hud/ack with this id; the
	// daemon stops waiting immediately instead of always paying the full settle.
	RequestID    string   `json:"request_id,omitempty"`
	Paused       bool     `json:"paused"`
	Stopped      bool     `json:"stopped"`
	StopReason   string   `json:"stop_reason,omitempty"`
	Agent        string   `json:"agent,omitempty"`
	SessionID    string   `json:"session_id,omitempty"`
	Display      int      `json:"display"`
	Action       string   `json:"action,omitempty"`
	LastActionAt int64    `json:"last_action_at,omitempty"`
	UpdatedAt    int64    `json:"updated_at"`
	Active       []string `json:"active,omitempty"`
}

// Hub fans state out to SSE subscribers and remembers the last state.
type Hub struct {
	mu        sync.Mutex
	state     State
	subs      map[chan State]struct{}
	heartbeat time.Duration
	// OnStop is called when a human presses Stop; the daemon releases sessions.
	onStop func(reason string)
	// OnPause is called when the overlay is paused or resumed.
	onPause func(paused bool)
	// lastSeen is when the overlay last checked in.
	lastSeen time.Time

	// ackMu guards pending. Each instruction that needs acknowledgement registers
	// a one-shot channel before it is published, so an ACK that races the SSE frame
	// cannot be lost.
	ackMu   sync.Mutex
	pending map[string]chan struct{}
	seq     atomic.Uint64
}

// NewHub builds a hub. heartbeat is how long the overlay may stay silent before
// it is considered gone.
func NewHub(heartbeat time.Duration) *Hub {
	if heartbeat <= 0 {
		heartbeat = 15 * time.Second
	}
	return &Hub{
		subs:      map[chan State]struct{}{},
		heartbeat: heartbeat,
		state:     State{Connected: false, Display: 0, UpdatedAt: time.Now().Unix()},
		pending:   map[string]chan struct{}{},
	}
}

// SetCallbacks wires the two actions a human can take from the overlay.
func (h *Hub) SetCallbacks(onStop func(string), onPause func(bool)) {
	h.mu.Lock()
	h.onStop, h.onPause = onStop, onPause
	h.mu.Unlock()
}

// Snapshot returns the current state.
func (h *Hub) Snapshot() State {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state
}

// Heartbeat marks the overlay alive.
func (h *Hub) Heartbeat() State {
	h.mu.Lock()
	h.lastSeen = time.Now()
	h.mu.Unlock()
	// A heartbeat means "the overlay is alive", nothing else.
	//
	// It used to clear Paused and Stopped as well, which quietly undid the two
	// things a human just asked for: press 暂停, and 15 seconds later the overlay
	// resumes itself; press Stop, and actions become possible again before the
	// person has even put the phone down. Measured on the device — paused=true
	// read back as false after one heartbeat window.
	//
	// Both flags are cleared by the events that should clear them: Paused by the
	// human resuming, Stopped by a new session being claimed.
	return h.update(func(s *State) {
		s.Connected = true
	})
}

// Expire reports whether the overlay was connected and has since gone silent for
// longer than the heartbeat window, marking it disconnected.
//
// This is the half of the safety contract that a heartbeat alone cannot provide:
// knowing when the overlay STOPPED talking. Without it a dead overlay looks
// exactly like a healthy one, and sessions keep being handed out on a phone where
// nobody can press Stop any more.
func (h *Hub) Expire() (bool, time.Duration) {
	h.mu.Lock()
	last, connected := h.lastSeen, h.state.Connected
	window := h.heartbeat
	h.mu.Unlock()
	if !connected || last.IsZero() {
		return false, 0
	}
	silent := time.Since(last)
	if silent <= window {
		return false, silent
	}
	h.update(func(s *State) { s.Connected = false })
	return true, silent
}

// Paused reports whether a human asked the screen to hold still.
//
// This is read by the input layer, not just by the claim path: a pause that only
// refuses NEW sessions still lets an agent that already holds one keep tapping,
// which is the opposite of what the person who pressed 暂停 asked for.
func (h *Hub) Paused() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.state.Paused || h.state.Stopped
}

// NoteAction records what the agent just did, so the overlay can show it.
func (h *Hub) NoteAction(action string) {
	h.update(func(s *State) {
		s.Action = action
		s.LastActionAt = time.Now().Unix()
		// Any action means somebody is driving, and the person holding the phone
		// must be able to see that and stop it. Waiting for a claim to make the
		// overlay visible meant a whole real session ran invisibly: taps and typing
		// for minutes with visible=false, because the client never claimed.
		//
		// It goes away on its own: NoActionFor below.
		s.Visible = true
	})
}

// QuietAfter hides the overlay when no action has been recorded for d.
//
// Visible is set by an action, not only by a claim, so it also has to be cleared by
// the absence of actions — otherwise a single tap leaves the pill on screen forever.
func (h *Hub) QuietAfter(d time.Duration) {
	if d <= 0 {
		return
	}
	h.update(func(s *State) {
		if s.SessionID != "" || s.Action == "" || s.LastActionAt == 0 {
			return // a claimed session owns visibility, not the idle timer
		}
		if time.Since(time.Unix(s.LastActionAt, 0)) < d {
			return
		}
		s.Visible = false
		s.Action = ""
	})
}

// SetCaptureHidden asks the overlay to hide (or come back) for a capture.
func (h *Hub) SetCaptureHidden(hidden bool) State {
	return h.update(func(s *State) {
		s.CaptureHidden = hidden
		s.RequestID = ""
	})
}

// CaptureWindow hides the overlay, waits for the overlay to apply it, and returns
// the function that brings it back.
//
// D28: the wait is driven by the overlay's ACK, not by a fixed sleep. The daemon
// publishes the instruction with a request_id; the overlay applies it and POSTs
// /v1/hud/ack. The settle duration remains as a timeout ceiling for overlays that
// do not acknowledge (for example an older APK still installed).
func (h *Hub) CaptureWindow(settle time.Duration) func() {
	if !h.Snapshot().Connected {
		return nil
	}
	id := h.beginRequest(func(s *State) { s.CaptureHidden = true })
	h.waitAck(id, settle)
	return func() {
		h.endRequest(id, func(s *State) { s.CaptureHidden = false })
	}
}

// SetPassThrough makes the overlay ignore touches while an agent injects.
func (h *Hub) SetPassThrough(on bool) State {
	return h.update(func(s *State) {
		s.PassThrough = on
		s.RequestID = ""
	})
}

// PassThroughWindow applies a pass-through instruction and waits for the
// overlay's acknowledgement, with the same timeout ceiling as CaptureWindow.
func (h *Hub) PassThroughWindow(settle time.Duration, on bool) func() {
	if !h.Snapshot().Connected {
		return nil
	}
	id := h.beginRequest(func(s *State) { s.PassThrough = on })
	h.waitAck(id, settle)
	return func() {
		h.endRequest(id, func(s *State) { s.PassThrough = false })
	}
}

// Ack resolves a pending instruction. It returns false for an unknown or already
// resolved request, which is normal when a heartbeat repeats a state frame.
func (h *Hub) Ack(requestID string) bool {
	if requestID == "" {
		return false
	}
	h.ackMu.Lock()
	ch, ok := h.pending[requestID]
	h.ackMu.Unlock()
	if !ok {
		return false
	}
	select {
	case ch <- struct{}{}:
	default:
	}
	return true
}

// beginRequest registers the acknowledgement channel before publishing the
// instruction, so an ACK cannot arrive before the daemon is listening for it.
func (h *Hub) beginRequest(apply func(*State)) string {
	id := fmt.Sprintf("hud-%d", h.seq.Add(1))
	ch := make(chan struct{}, 1)
	h.ackMu.Lock()
	h.pending[id] = ch
	h.ackMu.Unlock()
	h.update(func(s *State) {
		apply(s)
		s.RequestID = id
	})
	return id
}

// waitAck blocks until the overlay acknowledges id or timeout elapses. The entry
// is removed either way; a late ACK is a no-op.
func (h *Hub) waitAck(id string, timeout time.Duration) {
	h.ackMu.Lock()
	ch := h.pending[id]
	h.ackMu.Unlock()
	if ch == nil {
		return
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-ch:
	case <-timer.C:
	}
	h.ackMu.Lock()
	delete(h.pending, id)
	h.ackMu.Unlock()
}

func (h *Hub) endRequest(id string, apply func(*State)) {
	h.update(func(s *State) {
		apply(s)
		if s.RequestID == id {
			s.RequestID = ""
		}
	})
}

// Attach records which agent/session the overlay is guarding and makes it
// visible: a session is being driven, so a human must be able to see and stop it.
func (h *Hub) Attach(agent, sessionID string, display int) {
	h.update(func(s *State) {
		s.Agent, s.SessionID, s.Display = agent, sessionID, display
		s.Visible = true
		// A new claim is what ends the previous stop: the human stopped that task,
		// not every future one. Clearing it here rather than on a heartbeat keeps
		// "stopped" honest for as long as it matters.
		s.Stopped = false
		s.StopReason = ""
	})
}

// Detach clears the guard and hides the pill.
func (h *Hub) Detach() {
	h.update(func(s *State) {
		s.Agent, s.SessionID = "", ""
		s.Visible = false
	})
}

// Pause sets the paused flag and notifies.
func (h *Hub) Pause(paused bool) State {
	st := h.update(func(s *State) { s.Paused = paused })
	h.mu.Lock()
	cb := h.onPause
	h.mu.Unlock()
	if cb != nil {
		cb(paused)
	}
	return st
}

// Stop is the human escape hatch: it marks the hub stopped and notifies.
func (h *Hub) Stop(reason string) State {
	if reason == "" {
		reason = "user_stop"
	}
	st := h.update(func(s *State) {
		s.Stopped = true
		s.StopReason = reason
		s.Paused = false
	})
	h.mu.Lock()
	cb := h.onStop
	h.mu.Unlock()
	if cb != nil {
		cb(reason)
	}
	return st
}

// Disconnected marks the overlay gone.
func (h *Hub) Disconnected() {
	h.update(func(s *State) { s.Connected = false })
}

// Subscribe returns a state channel and its cancel function. The current state is
// delivered immediately so a new overlay renders without waiting for a change.
func (h *Hub) Subscribe() (<-chan State, func()) {
	// The overlay acknowledges instructions from its SSE consumer. Four frames was
	// enough to drop a request_id whenever the consumer was momentarily busy
	// (measured: one tap in ten timed out), so the buffer is sized for a burst of
	// actions plus heartbeats.
	ch := make(chan State, 16)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	cur := h.state
	h.mu.Unlock()
	ch <- cur
	return ch, func() {
		h.mu.Lock()
		if _, ok := h.subs[ch]; ok {
			delete(h.subs, ch)
			close(ch)
		}
		h.mu.Unlock()
	}
}

// Encode renders a state as one SSE data frame.
func Encode(st State) []byte {
	data, err := json.Marshal(st)
	if err != nil {
		return nil
	}
	out := make([]byte, 0, len(data)+8)
	out = append(out, "data: "...)
	out = append(out, data...)
	out = append(out, '\n', '\n')
	return out
}

func (h *Hub) update(fn func(*State)) State {
	h.mu.Lock()
	fn(&h.state)
	h.state.UpdatedAt = time.Now().Unix()
	cur := h.state
	subs := make([]chan State, 0, len(h.subs))
	for ch := range h.subs {
		subs = append(subs, ch)
	}
	h.mu.Unlock()
	for _, ch := range subs {
		select {
		case ch <- cur:
		default: // a slow overlay must not stall the daemon
		}
	}
	return cur
}

// HeartbeatInterval is how often the overlay must check in.
func (h *Hub) HeartbeatInterval() time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.heartbeat
}
