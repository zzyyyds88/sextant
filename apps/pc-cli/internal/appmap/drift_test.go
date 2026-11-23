package appmap

import (
	"path/filepath"
	"strings"
	"testing"

	"chopin/internal/uiapi"
)

func TestCompareDriftMatches(t *testing.T) {
	screen := &Screen{ID: "s1", Fingerprint: "p_aaaa", AppVersion: "3.1.0"}
	page := uiapi.Page{Fingerprint: "p_aaaa", AppVersion: "3.1.0"}
	d := CompareDrift(screen, page)
	if d.Drift || d.VersionChanged {
		t.Fatalf("drift on identical values: %+v", d)
	}
	if d.Warn("s1") != "" {
		t.Fatalf("warning on a match: %q", d.Warn("s1"))
	}
}

func TestCompareDriftFingerprint(t *testing.T) {
	screen := &Screen{ID: "s1", Fingerprint: "p_aaaa", AppVersion: "3.1.0"}
	page := uiapi.Page{Fingerprint: "p_bbbb", AppVersion: "3.1.0"}
	d := CompareDrift(screen, page)
	if !d.Drift || d.VersionChanged {
		t.Fatalf("drift = %+v", d)
	}
	if warn := d.Warn("s1"); warn == "" || !strings.Contains(warn, "fingerprint changed") {
		t.Fatalf("warning = %q", warn)
	}
}

func TestCompareDriftVersion(t *testing.T) {
	screen := &Screen{ID: "s1", Fingerprint: "p_aaaa", AppVersion: "3.1.0"}
	page := uiapi.Page{Fingerprint: "p_bbbb", AppVersion: "3.2.0"}
	d := CompareDrift(screen, page)
	if !d.Drift || !d.VersionChanged {
		t.Fatalf("drift = %+v", d)
	}
	if warn := d.Warn("s1"); warn == "" || !strings.Contains(warn, "version") {
		t.Fatalf("warning = %q", warn)
	}
}

// An unknown value on either side is not evidence of a mismatch.
func TestCompareDriftUnknownNeverMismatches(t *testing.T) {
	d := CompareDrift(&Screen{}, uiapi.Page{Fingerprint: "p_bbbb", AppVersion: "3.2.0"})
	if d.Drift || d.VersionChanged {
		t.Fatalf("unknown map fields reported as drift: %+v", d)
	}
	d = CompareDrift(&Screen{Fingerprint: "p_aaaa", AppVersion: "3.1.0"}, uiapi.Page{})
	if d.Drift || d.VersionChanged {
		t.Fatalf("unknown page fields reported as drift: %+v", d)
	}
	if d := CompareDrift(nil, uiapi.Page{Fingerprint: "p_bbbb"}); d.Drift {
		t.Fatalf("nil screen reported as drift: %+v", d)
	}
}

func TestYAMLRoundTripKeepsDriftFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "map.yaml")
	m := &Map{
		App: "settings", Package: "com.android.settings", ScreenW: 1080, ScreenH: 2400,
		Screens: []Screen{{
			ID: "screen_0", Package: "com.android.settings",
			Fingerprint: "p_0123456789abcdef", AppVersion: "14.0.1",
			Elements: []Element{{Text: "WLAN", Center: [2]float64{0.5, 0.2}}},
		}},
	}
	if err := m.Save(path); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	got := loaded.Screens[0]
	if got.Fingerprint != "p_0123456789abcdef" || got.AppVersion != "14.0.1" {
		t.Fatalf("round trip lost the drift fields: %+v", got)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
