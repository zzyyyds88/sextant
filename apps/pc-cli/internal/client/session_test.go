package client

import "testing"

func TestSessionRoundTrip(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	want := Session{Server: "<device-host>:8443", ID: "s1", Token: "tok", Agent: "agent"}
	if err := SaveSession(want); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSession()
	if err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("loaded %+v, want %+v", got, want)
	}
	if err := ClearSession(); err != nil {
		t.Fatal(err)
	}
	got, err = LoadSession()
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "" {
		t.Fatalf("session survived clear: %+v", got)
	}
}
