package token

import "testing"

// Every scope a route demands must be issuable.
//
// This is the bug this test exists for: /v1/audio/* requires "device", but
// "device" was not in the closed set, so a token minted with the
// default scopes could never call them — the endpoints answered 403 forever, and
// the failure looked like a permissions mistake at the call site rather than a
// scope that could not be granted at all.
func TestEveryRouteScopeCanBeGranted(t *testing.T) {
	want := []string{
		"read", "ui", "input", "apps", "files", "settings", "unlock",
		"sms", "location", "notifications", "webrtc", "triggers", "display",
		"device", "admin", "hud",
	}
	for _, s := range want {
		if !scopes[s] {
			t.Fatalf("scope %q is demanded by a route but cannot be issued", s)
		}
		if _, err := NormaliseScopes([]string{s}); err != nil {
			t.Fatalf("NormaliseScopes(%q): %v", s, err)
		}
	}
}

// A default PC token has to be able to do the things the CLI claims to do. `admin`
// is deliberately excluded — reboot stays a separate, narrower grant.
func TestDefaultScopesCoverTheDeviceScope(t *testing.T) {
	got, err := NormaliseScopes(DefaultScopes())
	if err != nil {
		t.Fatalf("the default scope list is not valid: %v", err)
	}
	have := map[string]bool{}
	for _, s := range got {
		have[s] = true
	}
	if !have["device"] {
		t.Fatal("a default agent token cannot mute the microphone or play audio")
	}
	if have["admin"] {
		t.Fatal("admin (reboot) must not come with the default set")
	}
}
