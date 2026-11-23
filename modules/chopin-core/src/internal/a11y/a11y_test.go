package a11y

import "testing"

func TestNormaliseExpandsShortForm(t *testing.T) {
	cases := map[string]string{
		"com.x/.Svc":      "com.x/com.x.Svc",
		"com.x/com.x.Svc": "com.x/com.x.Svc",
		"com.x/.a.B":      "com.x/com.x.a.B",
		// Android semantics: a class that does not start with "." is already
		// fully qualified, so it must be preserved verbatim.
		"com.x/a.B": "com.x/a.B",
	}
	for in, want := range cases {
		if got := Normalise(in, "com.x"); got != want {
			t.Errorf("Normalise(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseDropsDuplicatesAfterNormalisation(t *testing.T) {
	// This is the bug that made the list grow on every write: AMS persists the
	// short form, the module wrote the long form, and string comparison saw two
	// different entries.
	raw := "com.a/.Svc:com.a/com.a.Svc:com.b/.Svc"
	got := Parse(raw, "com.a")
	if len(got) != 2 {
		t.Fatalf("Parse = %v, want 2 entries", got)
	}
	if got[0] != "com.a/com.a.Svc" || got[1] != "com.b/com.b.Svc" {
		t.Fatalf("Parse = %v", got)
	}
}

func TestContainsHandlesBothForms(t *testing.T) {
	if !Contains("com.a/.Svc", "com.a/com.a.Svc", "com.a") {
		t.Fatal("long form must match a short-form setting")
	}
	if Contains("com.b/.Svc", "com.a/.Svc", "com.a") {
		t.Fatal("different package must not match")
	}
}

func TestWithoutRemovesOnlyTarget(t *testing.T) {
	raw := "com.a/.Svc:com.b/.Svc"
	if got := Without(raw, "com.a/.Svc", "com.a"); got != "com.b/com.b.Svc" {
		t.Fatalf("Without = %q", got)
	}
	if got := Without("com.a/.Svc", "com.a/.Svc", "com.a"); got != "" {
		t.Fatalf("Without (only entry) = %q, want empty", got)
	}
}

func TestJoinRoundTrip(t *testing.T) {
	in := []string{"com.a/com.a.Svc", "com.b/com.b.Svc"}
	if got := Parse(Join(in), "com.a"); len(got) != 2 || got[0] != in[0] || got[1] != in[1] {
		t.Fatalf("round trip = %v", got)
	}
}

func TestRestoreSkipsEnsureForMissingCompat(t *testing.T) {
	// A Layer with no Compat must behave exactly as before: nothing extra is
	// touched. This guards the zero-config path.
	l := Layer{Component: "com.a/.Svc"}
	if l.Compat != "" {
		t.Fatalf("zero Layer must have no Compat, got %q", l.Compat)
	}
}
