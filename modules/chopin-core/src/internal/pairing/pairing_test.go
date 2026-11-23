package pairing

import (
	"path/filepath"
	"testing"
	"time"
)

func open(t *testing.T) *Manager {
	t.Helper()
	m, err := Open(filepath.Join(t.TempDir(), "pairing.json"))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestCodeIsSingleUse(t *testing.T) {
	m := open(t)
	code, _, err := m.New("pc")
	if err != nil {
		t.Fatal(err)
	}
	agent, err := m.Redeem(code)
	if err != nil {
		t.Fatalf("first redeem failed: %v", err)
	}
	if agent != "pc" {
		t.Fatalf("agent = %q", agent)
	}
	if _, err := m.Redeem(code); err == nil {
		t.Fatal("code accepted twice")
	}
}

func TestWrongCodeDoesNotClearPendingPairing(t *testing.T) {
	m := open(t)
	code, _, err := m.New("pc")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Redeem("deadbeef"); err == nil {
		t.Fatal("wrong code accepted")
	}
	if _, err := m.Redeem(code); err != nil {
		t.Fatalf("correct code rejected after one failure: %v", err)
	}
}

func TestLockoutAfterThreeFailures(t *testing.T) {
	m := open(t)
	code, _, err := m.New("pc")
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < MaxFailures; i++ {
		if _, err := m.Redeem("bad"); err == nil {
			t.Fatal("wrong code accepted")
		}
	}
	if _, err := m.Redeem(code); err == nil {
		t.Fatal("pairing still accepted after lockout")
	}
}

func TestExpiredCodeIsRejected(t *testing.T) {
	m := open(t)
	code, _, err := m.New("pc")
	if err != nil {
		t.Fatal(err)
	}
	// Redeem re-reads the file, so the expiry has to be persisted, not just
	// poked into memory.
	m.mu.Lock()
	m.state.ExpiresAt = time.Now().Add(-time.Second).Unix()
	err = m.saveLocked()
	m.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := m.Redeem(code); err == nil {
		t.Fatal("expired code accepted")
	}
}

func TestNoPendingPairingIsAnError(t *testing.T) {
	m := open(t)
	if _, err := m.Redeem("anything"); err == nil {
		t.Fatal("redeem without a pending pairing succeeded")
	}
}
