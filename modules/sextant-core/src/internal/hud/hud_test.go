package hud

import (
	"strings"
	"testing"
	"time"
)

func TestStopNotifiesOnce(t *testing.T) {
	h := NewHub(time.Second)
	var got []string
	h.SetCallbacks(func(reason string) { got = append(got, reason) }, nil)
	st := h.Stop("")
	if !st.Stopped || st.StopReason != "user_stop" {
		t.Fatalf("stop state = %+v", st)
	}
	if len(got) != 1 || got[0] != "user_stop" {
		t.Fatalf("callback = %v", got)
	}
}

// TestHeartbeatClearsStop used to assert the opposite of this: that a heartbeat
// silently un-stops the overlay. That is the bug, not the contract — a person who
// pressed Stop had it undone 15 seconds later by the overlay's own keep-alive, and
// the same path cleared 暂停. The flag is now cleared by the event that means
// "a new task started", which is Attach (see TestANewClaimEndsThePreviousStop).
func TestHeartbeatKeepsTheStop(t *testing.T) {
	h := NewHub(time.Second)
	h.Stop("panic")
	st := h.Heartbeat()
	if !st.Stopped || !st.Connected {
		t.Fatalf("after heartbeat = %+v", st)
	}
}

func TestSubscribeGetsCurrentStateFirst(t *testing.T) {
	h := NewHub(time.Second)
	h.Attach("agent", "sess", 2)
	ch, cancel := h.Subscribe()
	defer cancel()
	select {
	case st := <-ch:
		if st.Agent != "agent" || st.SessionID != "sess" || st.Display != 2 {
			t.Fatalf("first frame = %+v", st)
		}
	case <-time.After(time.Second):
		t.Fatal("no initial state was delivered")
	}
}

func TestPauseIsReported(t *testing.T) {
	h := NewHub(time.Second)
	var paused *bool
	h.SetCallbacks(nil, func(p bool) { v := p; paused = &v })
	st := h.Pause(true)
	if !st.Paused {
		t.Fatalf("state = %+v", st)
	}
	if paused == nil || !*paused {
		t.Fatal("pause callback did not fire")
	}
}

func TestEncodeIsAnSSEFrame(t *testing.T) {
	frame := string(Encode(State{Connected: true}))
	if !strings.HasPrefix(frame, "data: ") || !strings.HasSuffix(frame, "\n\n") {
		t.Fatalf("frame = %q", frame)
	}
	if !strings.Contains(frame, `"connected":true`) {
		t.Fatalf("frame lost the payload: %q", frame)
	}
}

func TestExpireReleasesOnlyAfterSilence(t *testing.T) {
	h := NewHub(50 * time.Millisecond)
	// Never connected: nothing to expire, even after a long wait.
	if expired, _ := h.Expire(); expired {
		t.Fatal("an overlay that never connected was reported as expired")
	}
	h.Heartbeat()
	if expired, _ := h.Expire(); expired {
		t.Fatal("a freshly heartbeated overlay was reported as expired")
	}
	time.Sleep(80 * time.Millisecond)
	expired, silent := h.Expire()
	if !expired {
		t.Fatal("a silent overlay was not reported as expired")
	}
	if silent < 50*time.Millisecond {
		t.Fatalf("silent = %s, want at least the heartbeat window", silent)
	}
	if h.Snapshot().Connected {
		t.Fatal("the hub still reports the overlay as connected")
	}
}

func TestAttachShowsThePillAndDetachHidesIt(t *testing.T) {
	h := NewHub(time.Minute)
	if h.Snapshot().Visible {
		t.Fatal("the pill is visible before any session exists")
	}
	h.Attach("agent", "sess-1", 0)
	st := h.Snapshot()
	if !st.Visible || st.Agent != "agent" || st.SessionID != "sess-1" {
		t.Fatalf("attach did not make the pill visible: %+v", st)
	}
	h.Detach()
	if h.Snapshot().Visible {
		t.Fatal("detach left the pill on screen")
	}
}

func TestHeartbeatDoesNotResumeAPausedOverlay(t *testing.T) {
	// The overlay heartbeats every 15s. If a heartbeat clears the pause, a person
	// pressing 暂停 gets 15 seconds of quiet and then the agent starts moving the
	// screen again — with the pill still showing 「已暂停」.
	h := NewHub(15 * time.Second)
	h.Attach("agent", "s1", 0)
	h.Pause(true)
	h.Heartbeat()
	if !h.Paused() {
		t.Fatal("a heartbeat resumed a paused overlay")
	}
}

func TestHeartbeatDoesNotUndoAStop(t *testing.T) {
	// Same for Stop: it must hold until a new session is claimed, otherwise
	// "stopped" only means "stopped until the next keep-alive".
	h := NewHub(15 * time.Second)
	h.Attach("agent", "s1", 0)
	h.Stop("human pressed stop")
	h.Heartbeat()
	if !h.Paused() {
		t.Fatal("a heartbeat made a stopped overlay actionable again")
	}
	if !h.Snapshot().Stopped {
		t.Fatal("a heartbeat cleared the stopped flag")
	}
}

func TestANewClaimEndsThePreviousStop(t *testing.T) {
	h := NewHub(15 * time.Second)
	h.Attach("agent", "s1", 0)
	h.Stop("human pressed stop")
	h.Detach()
	h.Attach("agent2", "s2", 0)
	if h.Snapshot().Stopped {
		t.Fatal("a new session inherited the previous stop")
	}
	if h.Paused() {
		t.Fatal("a new session started blocked")
	}
}

func TestAnActionMakesTheOverlayVisibleWithoutAClaim(t *testing.T) {
	// Found in a real session: minutes of taps and typing ran with visible=false
	// because the client never claimed a session, and visibility was only ever set
	// by Attach. The safety overlay cannot depend on the client behaving.
	h := NewHub(15 * time.Second)
	if h.Snapshot().Visible {
		t.Fatal("a fresh hub must not show anything")
	}
	h.NoteAction("点「发送」")
	if !h.Snapshot().Visible {
		t.Fatal("an action must make the overlay visible")
	}
	if got := h.Snapshot().Action; got != "点「发送」" {
		t.Fatalf("action = %q", got)
	}
}

func TestQuietAfterTakesItDownButNotWhileClaimed(t *testing.T) {
	h := NewHub(15 * time.Second)
	h.NoteAction("点「发送」")
	h.QuietAfter(time.Millisecond)
	if h.Snapshot().Visible {
		t.Fatal("the overlay stayed up after the actions stopped")
	}

	// A claimed session owns visibility: the human must keep seeing it even when the
	// agent is thinking rather than acting.
	h.Attach("agent", "s1", 0)
	h.QuietAfter(time.Millisecond)
	if !h.Snapshot().Visible {
		t.Fatal("the idle timer hid an overlay that a claimed session owns")
	}
}
