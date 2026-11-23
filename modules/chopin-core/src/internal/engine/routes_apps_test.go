package engine

import (
	"context"
	"testing"
	"time"

	"chopin-core/internal/sysprobe"
	"chopin-core/internal/ui"
)

// fakeRunner answers every command with one fixed output.
type fakeRunner struct {
	out string
	err error
}

func (f fakeRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	return f.out, f.err
}

func windowWith(pkg string) string {
	return "  mCurrentFocus=Window{2b3c4d u0 " + pkg + "/" + pkg + ".MainActivity}\n"
}

func TestWaitForegroundMatches(t *testing.T) {
	s := &Server{Probe: sysprobe.Probe{Run: fakeRunner{out: windowWith("com.android.settings")}}}
	ok, fg := s.waitForeground(context.Background(), "com.android.settings", 500*time.Millisecond)
	if !ok || fg != "com.android.settings" {
		t.Fatalf("want a match, got ok=%v fg=%q", ok, fg)
	}
}

func TestWaitForegroundReportsWhatIsActuallyThere(t *testing.T) {
	// HyperOS answers some launches by raising another app. The caller needs the
	// real foreground, not a generic timeout.
	s := &Server{Probe: sysprobe.Probe{Run: fakeRunner{out: windowWith("com.android.phone")}}}
	ok, fg := s.waitForeground(context.Background(), "com.android.settings", 100*time.Millisecond)
	if ok {
		t.Fatal("a different foreground must not count as landed")
	}
	if fg != "com.android.phone" {
		t.Fatalf("want the actual foreground, got %q", fg)
	}
}

func TestPageFingerprintIncludesSelected(t *testing.T) {
	base := []ui.Node{{
		Attrs: map[string]string{"class": "android.widget.TextView", "text": "推荐", "selected": "false"},
	}, {
		Attrs: map[string]string{"class": "android.widget.TextView", "text": "消息", "selected": "false"},
	}}
	switched := []ui.Node{base[0], base[1]}
	switched[1].Attrs = map[string]string{"class": "android.widget.TextView", "text": "消息", "selected": "true"}

	if pageFingerprint(base) == pageFingerprint(switched) {
		t.Fatal("two tabs with a different selected entry must not share a page fingerprint")
	}
	if treeHash(base) == treeHash(switched) {
		t.Fatal("two tabs with a different selected entry must not share a tree hash")
	}
	if pageFingerprint(base) != pageFingerprint([]ui.Node{base[0], base[1]}) {
		t.Fatal("the same tree must produce the same fingerprint")
	}
}
