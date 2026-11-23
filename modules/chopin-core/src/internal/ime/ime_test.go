package ime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type fakeRunner struct {
	out string
	// active, when set, is what `dumpsys input_method` reports as the current
	// default. It lets a test describe "the device is now on X" independently of
	// the canned output.
	active string
	err    error
	cmds   []string
}

func (f *fakeRunner) Run(ctx context.Context, name string, args ...string) (string, error) {
	f.cmds = append(f.cmds, name+" "+strings.Join(args, " "))
	if f.err != nil {
		return "", f.err
	}
	if f.active != "" && name == "dumpsys" && len(args) > 0 && args[0] == "input_method" {
		return "  mCurMethodId=" + f.active + "\n", nil
	}
	return f.out, nil
}

func TestActiveParsesCurMethodID(t *testing.T) {
	r := &fakeRunner{out: "  mCurMethodId=com.example.ime/.Svc mCurId=com.example.ime/.Svc\n"}
	m := New(r, Target{Service: "com.example.ime/.Svc"})
	got, err := m.Active(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got != "com.example.ime/.Svc" {
		t.Fatalf("active = %q", got)
	}
}

func TestEnsureActiveEnablesAndSets(t *testing.T) {
	r := &fakeRunner{out: "  mCurMethodId=com.other/.Ime\n"}
	m := New(r, Target{Service: "com.chopin.pilot/.ime.RemoteImeService"})
	m.Settle = time.Millisecond
	// After the switch the fake keeps answering with the old IME, so this must
	// report a failure rather than pretending the switch worked.
	err := m.EnsureActive(context.Background())
	if err == nil {
		t.Fatal("a switch that did not take effect was reported as success")
	}
	joined := strings.Join(r.cmds, "\n")
	if !strings.Contains(joined, "ime enable com.chopin.pilot/.ime.RemoteImeService") {
		t.Fatalf("ime enable was not called: %v", r.cmds)
	}
	if !strings.Contains(joined, "ime set com.chopin.pilot/.ime.RemoteImeService") {
		t.Fatalf("ime set was not called: %v", r.cmds)
	}
}

func TestEnsureActiveIsANoOpWhenAlreadyActive(t *testing.T) {
	r := &fakeRunner{out: "  mCurMethodId=com.chopin.pilot/.ime.RemoteImeService\n"}
	m := New(r, Target{Service: "com.chopin.pilot/.ime.RemoteImeService"})
	if err := m.EnsureActive(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(r.cmds) != 1 {
		t.Fatalf("expected only the dumpsys probe, got %v", r.cmds)
	}
}

func TestParseResult(t *testing.T) {
	for _, want := range []string{"VERIFIED", "ACCEPTED_UNVERIFIED", "REJECTED", "NO_INPUT_CONNECTION"} {
		got, err := parseResult("Bundle[{result=" + want + "}]")
		if err != nil || got != want {
			t.Fatalf("parseResult(%q) = %q, %v", want, got, err)
		}
	}
	if _, err := parseResult("Bundle[{something=else}]"); err == nil {
		t.Fatal("an unknown verdict was accepted")
	}
}

func TestLeaseBlocksConcurrentCommits(t *testing.T) {
	m := New(&fakeRunner{out: "  mCurMethodId=com.chopin.pilot/.ime.RemoteImeService\n"},
		Target{Service: "com.chopin.pilot/.ime.RemoteImeService"})
	if err := m.Acquire(); err != nil {
		t.Fatal(err)
	}
	if err := m.Acquire(); !errors.Is(err, ErrBusy) {
		t.Fatalf("second acquire = %v, want text_input_busy", err)
	}
	m.Release()
	if err := m.Acquire(); err != nil {
		t.Fatalf("after release: %v", err)
	}
}

func TestRestoreUsesThePreviousIME(t *testing.T) {
	r := &fakeRunner{out: "  mCurMethodId=com.other/.Ime\n"}
	m := New(r, Target{Service: "com.chopin.pilot/.ime.RemoteImeService"})
	ctx := context.Background()
	m.mu.Lock()
	m.previous, m.switched = "com.other/.Ime", true
	m.mu.Unlock()
	if err := m.Restore(ctx); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(r.cmds, "\n"), "ime set com.other/.Ime") {
		t.Fatalf("restore did not set the previous IME: %v", r.cmds)
	}
}

func TestCommitSendsBase64(t *testing.T) {
	r := &fakeRunner{out: "  mCurMethodId=com.chopin.pilot/.ime.RemoteImeService\nBundle[{result=VERIFIED}]"}
	m := New(r, Target{Service: "com.chopin.pilot/.ime.RemoteImeService", Authority: "com.chopin.pilot.ime"})
	verdict, err := m.Commit(context.Background(), "蓝牙", false)
	if err != nil {
		t.Fatal(err)
	}
	if verdict != "VERIFIED" {
		t.Fatalf("verdict = %q", verdict)
	}
	joined := strings.Join(r.cmds, "\n")
	// "蓝牙" in UTF-8 is 6 bytes -> "6JOd54mZ" in standard Base64. Sending the raw
	// text makes the provider decode garbage and still answer VERIFIED.
	if !strings.Contains(joined, "--arg 6JOd54mZ") {
		t.Fatalf("the provider was not given Base64 text: %v", r.cmds)
	}
}

func TestKeyMapsNamesToKeyCodes(t *testing.T) {
	r := &fakeRunner{out: "  mCurMethodId=com.chopin.pilot/.ime.RemoteImeService\nBundle[{result=VERIFIED}]"}
	m := New(r, Target{Service: "com.chopin.pilot/.ime.RemoteImeService", Authority: "com.chopin.pilot.ime"})
	if _, err := m.Key(context.Background(), "enter"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(r.cmds, "\n"), "--arg 66") {
		t.Fatalf("enter was not mapped to key code 66: %v", r.cmds)
	}
	if _, err := m.Key(context.Background(), "nope"); err == nil {
		t.Fatal("an unknown key name was accepted")
	}
}

func TestSameComponentHandlesShortForm(t *testing.T) {
	if !sameComponent("pkg/.Svc", "pkg/pkg.Svc") {
		t.Fatal("short and long forms were treated as different")
	}
	if sameComponent("pkg/.Svc", "pkg/.Other") {
		t.Fatal("different components compared equal")
	}
}

func TestRestoreReadsPreviousFromFileAfterRestart(t *testing.T) {
	// A daemon restart must not strand the user on the harness IME: the previous
	// value lives in a file, not only in the process that switched it.
	dir := t.TempDir()
	path := filepath.Join(dir, "ime.previous")
	if err := os.WriteFile(path, []byte("com.example.kb/.Kb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r := &fakeRunner{active: "com.chopin.pilot/.ime.RemoteImeService"}
	m := New(r, Target{Service: "com.chopin.pilot/.ime.RemoteImeService", Authority: "a.b"})
	m.PreviousFile = path
	if err := m.Restore(context.Background()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if !strings.Contains(strings.Join(r.cmds, "\n"), "ime set com.example.kb/.Kb") {
		t.Fatalf("restore did not run: %v", r.cmds)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("previous-ime file should be cleared after a successful restore")
	}
}

func TestRestoreLeavesAUserChosenImeAlone(t *testing.T) {
	// If the user picked a different keyboard after we took over, restoring would
	// silently undo their choice.
	dir := t.TempDir()
	path := filepath.Join(dir, "ime.previous")
	_ = os.WriteFile(path, []byte("com.example.kb/.Kb\n"), 0o600)
	r := &fakeRunner{active: "com.user.choice/.New"}
	m := New(r, Target{Service: "com.chopin.pilot/.ime.RemoteImeService", Authority: "a.b"})
	m.PreviousFile = path
	if err := m.Restore(context.Background()); err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if strings.Contains(strings.Join(r.cmds, "\n"), "ime set") {
		t.Fatalf("restore must not override a user-chosen IME: %v", r.cmds)
	}
}

func TestCommitHandsTheKeyboardBackImmediately(t *testing.T) {
	// The harness IME is borrowed for one commit, not for the session: after the
	// text is in, the device must be back on the keyboard the user had. Keeping it
	// until a session release means the user's own next keystroke goes into our
	// invisible 48dp view.
	dir := t.TempDir()
	prev := filepath.Join(dir, "ime.previous")
	r := &fakeRunner{active: "com.user.kb/.Kb",
		out: "Bundle[{result=VERIFIED}]"}
	m := New(r, Target{Service: "com.chopin.pilot/.ime.RemoteImeService", Authority: "a.b"})
	m.PreviousFile = prev
	m.Settle = time.Millisecond

	if _, err := m.Commit(context.Background(), "hello", false); err != nil {
		// EnsureActive polls until the fake reports our IME; this fake never does,
		// so a failure here is expected and the restore must still have happened.
		_ = err
	}
	joined := strings.Join(r.cmds, "\n")
	if !strings.Contains(joined, "ime set com.user.kb/.Kb") {
		t.Fatalf("commit did not hand the keyboard back: %v", r.cmds)
	}
}

func TestRestoreAfterUseReportsFailureInsteadOfSwallowingIt(t *testing.T) {
	// If the hand-back fails the user is stuck on our keyboard; the API must be
	// able to say so rather than answering ok:true.
	dir := t.TempDir()
	path := filepath.Join(dir, "ime.previous")
	_ = os.WriteFile(path, []byte("com.user.kb/.Kb\n"), 0o600)
	r := &fakeRunner{err: errors.New("ime set failed")}
	m := New(r, Target{Service: "com.chopin.pilot/.ime.RemoteImeService", Authority: "a.b"})
	m.PreviousFile = path
	m.restoreAfterUse()
	if m.LastRestoreError() == nil {
		t.Fatal("a failed hand-back must be reported, not swallowed")
	}
}
