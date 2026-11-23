package session

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T) *Manager {
	t.Helper()
	m, err := Open(filepath.Join(t.TempDir(), "sessions.json"), Config{
		IdleSec: 900, RosterBanSec: 60, MaxWriteSessions: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestOneWriteSessionPerDisplay(t *testing.T) {
	m := open(t)
	if _, err := m.Claim(context.Background(), "a", 0); err != nil {
		t.Fatal(err)
	}
	_, err := m.Claim(context.Background(), "b", 0)
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("second claim on display 0 = %v, want conflict", err)
	}
	// A different display is allowed (main + one virtual).
	if _, err := m.Claim(context.Background(), "b", 1); err != nil {
		t.Fatalf("claim on display 1: %v", err)
	}
	// And the slot limit is enforced beyond that.
	if _, err := m.Claim(context.Background(), "c", 2); !errors.Is(err, ErrConflict) {
		t.Fatalf("third claim = %v, want conflict", err)
	}
}

func TestHeartbeatRequiresTheRightToken(t *testing.T) {
	m := open(t)
	s, err := m.Claim(context.Background(), "a", 0)
	if err != nil {
		t.Fatal(err)
	}
	if err := m.Heartbeat(s.ID, "wrong"); !errors.Is(err, ErrForbidden) {
		t.Fatalf("wrong token = %v, want forbidden", err)
	}
	if err := m.Heartbeat(s.ID, s.Token); err != nil {
		t.Fatalf("right token: %v", err)
	}
}

func TestReapBansTheAgent(t *testing.T) {
	m := open(t)
	s, err := m.Claim(context.Background(), "a", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Simulate an expired lease.
	m.sessions[s.ID].LastSeen -= 100000
	if n := m.Reap(); n != 1 {
		t.Fatalf("reap = %d, want 1", n)
	}
	if _, err := m.Claim(context.Background(), "a", 0); !errors.Is(err, ErrBanned) {
		t.Fatalf("re-claim after reap = %v, want banned", err)
	}
	// A different agent is unaffected.
	if _, err := m.Claim(context.Background(), "b", 0); err != nil {
		t.Fatalf("other agent: %v", err)
	}
}

func TestExclusiveNeedsASingleSession(t *testing.T) {
	m := open(t)
	a, _ := m.Claim(context.Background(), "a", 0)
	if err := m.ExclusiveAllowed(a.ID); err != nil {
		t.Fatalf("sole session: %v", err)
	}
	_, _ = m.Claim(context.Background(), "b", 1)
	if err := m.ExclusiveAllowed(a.ID); !errors.Is(err, ErrExclusive) {
		t.Fatalf("two sessions = %v, want exclusive_required", err)
	}
}

func TestReleaseAll(t *testing.T) {
	m := open(t)
	_, _ = m.Claim(context.Background(), "a", 0)
	_, _ = m.Claim(context.Background(), "b", 1)
	if n := m.ReleaseAll("test"); n != 2 {
		t.Fatalf("release all = %d", n)
	}
	if m.Count() != 0 {
		t.Fatal("sessions survived ReleaseAll")
	}
}

func TestTokenIsNotSerialized(t *testing.T) {
	m := open(t)
	s, _ := m.Claim(context.Background(), "a", 0)
	list := m.List()
	if len(list) != 1 || list[0].Token != "" {
		t.Fatalf("List leaked the token: %+v", list)
	}
	if s.Token == "" {
		t.Fatal("claim must return a token")
	}
}

func TestReapLoopReleasesExpiredSessions(t *testing.T) {
	m, err := Open(filepath.Join(t.TempDir(), "sessions.json"), Config{
		IdleSec: 1, RosterBanSec: 60, MaxWriteSessions: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Claim(context.Background(), "agent", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.sessions[s.ID].LastSeen = time.Now().Add(-2 * time.Second).Unix()
	m.mu.Unlock()

	reaped := make(chan int, 1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.ReapLoop(ctx, 10*time.Millisecond, func(n int) {
		select {
		case reaped <- n:
		default:
		}
	})
	select {
	case n := <-reaped:
		if n != 1 {
			t.Fatalf("reaped = %d, want 1", n)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("the reaper did not fire")
	}
	if got := len(m.List()); got != 0 {
		t.Fatalf("%d session(s) survived", got)
	}
}

func TestReapLoopDisabledAndCancel(t *testing.T) {
	m := open(t)
	done := make(chan struct{})
	go func() {
		m.ReapLoop(context.Background(), 0, func(int) {})
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("reap loop with interval 0 did not return")
	}

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	go func() {
		m.ReapLoop(ctx, 10*time.Millisecond, func(int) {})
		close(stopped)
	}()
	cancel()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("reap loop did not stop after cancel")
	}
}

func TestReapUsesStaleSecAsTheShorterLimit(t *testing.T) {
	m, err := Open(filepath.Join(t.TempDir(), "sessions.json"), Config{
		IdleSec: 900, StaleSec: 1, RosterBanSec: 60, MaxWriteSessions: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Claim(context.Background(), "agent", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.sessions[s.ID].LastSeen = time.Now().Add(-2 * time.Second).Unix()
	m.mu.Unlock()
	if n := m.Reap(); n != 1 {
		t.Fatalf("reaped = %d, want 1 (stale_sec beats idle_sec)", n)
	}
}

func TestReapFallsBackToIdleWhenStaleIsZero(t *testing.T) {
	m, err := Open(filepath.Join(t.TempDir(), "sessions.json"), Config{
		IdleSec: 900, StaleSec: 0, RosterBanSec: 60, MaxWriteSessions: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Claim(context.Background(), "agent", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.sessions[s.ID].LastSeen = time.Now().Add(-2 * time.Second).Unix()
	m.mu.Unlock()
	if n := m.Reap(); n != 0 {
		t.Fatalf("reaped = %d, want 0 (idle_sec is 900)", n)
	}
}

func TestTouchUpdatesActivityAndHeartbeatDoesNot(t *testing.T) {
	m := open(t)
	s, err := m.Claim(context.Background(), "agent", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.sessions[s.ID].LastAction = time.Now().Add(-time.Hour).Unix()
	m.mu.Unlock()

	if err := m.Touch(s.Token, false); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	afterHeartbeat := m.sessions[s.ID].LastAction
	m.mu.Unlock()
	if afterHeartbeat > time.Now().Add(-time.Minute).Unix() {
		t.Fatal("a heartbeat changed LastAction")
	}

	if err := m.Touch(s.Token, true); err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	afterAction := m.sessions[s.ID].LastAction
	m.mu.Unlock()
	if afterAction < time.Now().Add(-time.Minute).Unix() {
		t.Fatal("a business request did not update LastAction")
	}
}

func TestReapZombieReleasesFreshHeartbeatWithNoActivity(t *testing.T) {
	m, err := Open(filepath.Join(t.TempDir(), "sessions.json"), Config{
		IdleSec: 900, StaleSec: 180, RosterBanSec: 60, MaxWriteSessions: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Claim(context.Background(), "agent", 0)
	if err != nil {
		t.Fatal(err)
	}
	m.mu.Lock()
	m.sessions[s.ID].LastSeen = time.Now().Unix()
	m.sessions[s.ID].LastAction = time.Now().Add(-10 * time.Minute).Unix()
	m.mu.Unlock()
	if n := m.ReapZombie(5 * time.Minute); n != 1 {
		t.Fatalf("zombies = %d, want 1", n)
	}
	if len(m.List()) != 0 {
		t.Fatal("the zombie survived")
	}
	if len(m.Roster()) != 0 {
		t.Fatalf("zombie was banned: %v", m.Roster())
	}
}

func TestReapZombieLeavesStaleAndUnknownClocksAlone(t *testing.T) {
	m, err := Open(filepath.Join(t.TempDir(), "sessions.json"), Config{
		IdleSec: 900, StaleSec: 180, RosterBanSec: 60, MaxWriteSessions: 2,
	})
	if err != nil {
		t.Fatal(err)
	}
	s, err := m.Claim(context.Background(), "agent", 0)
	if err != nil {
		t.Fatal(err)
	}
	// Heartbeat is already stale: Reap owns it.
	m.mu.Lock()
	m.sessions[s.ID].LastSeen = time.Now().Add(-time.Hour).Unix()
	m.sessions[s.ID].LastAction = time.Now().Add(-time.Hour).Unix()
	m.mu.Unlock()
	if n := m.ReapZombie(5 * time.Minute); n != 0 {
		t.Fatalf("zombies = %d, want 0 for a stale heartbeat", n)
	}
	// Pre-D37 record with no activity clock.
	m.mu.Lock()
	m.sessions[s.ID].LastSeen = time.Now().Unix()
	m.sessions[s.ID].LastAction = 0
	m.mu.Unlock()
	if n := m.ReapZombie(5 * time.Minute); n != 0 {
		t.Fatalf("zombies = %d, want 0 for an unknown activity clock", n)
	}
	if n := m.ReapZombie(0); n != 0 {
		t.Fatalf("zombies = %d, want 0 when disabled", n)
	}
}
