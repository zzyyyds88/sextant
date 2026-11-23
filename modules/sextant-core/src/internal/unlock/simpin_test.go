package unlock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRemainingSIMAttempts(t *testing.T) {
	cases := map[string]int{
		"<node text=\"还可以尝试 2 次\" />":       2,
		"<node text=\"剩余 3 次\" />":          3,
		"<node text=\"3 attempts left\" />": 3,
		"<node text=\"输入 SIM 卡 PIN\" />":    0,
	}
	for xmlText, want := range cases {
		got, ok := remainingSIMAttempts(xmlText)
		if want == 0 {
			if ok {
				t.Fatalf("%q parsed as %d attempts, want none", xmlText, got)
			}
			continue
		}
		if !ok || got != want {
			t.Fatalf("%q = %d (%v), want %d", xmlText, got, ok, want)
		}
	}
}

func TestSIMPinValidation(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sim-pin")
	m := &Manager{SIMSecret: path}

	if _, err := m.readSIMPin(); err == nil {
		t.Fatal("a missing SIM PIN file was accepted")
	}
	if err := os.WriteFile(path, []byte("12"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.readSIMPin(); err == nil {
		t.Fatal("a two-digit PIN was accepted")
	}
	if err := os.WriteFile(path, []byte("1234"), 0o600); err != nil {
		t.Fatal(err)
	}
	pin, err := m.readSIMPin()
	if err != nil || pin != "1234" {
		t.Fatalf("readSIMPin = %q, %v", pin, err)
	}
}

func TestSIMCredentialPresent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sim-pin")
	m := Manager{SIMSecret: path}
	if m.SIMCredentialPresent() {
		t.Fatal("an absent file was reported as present")
	}
	if err := os.WriteFile(path, []byte("1234"), 0o600); err != nil {
		t.Fatal(err)
	}
	if !m.SIMCredentialPresent() {
		t.Fatal("a configured PIN was reported as absent")
	}
	if (Manager{}).SIMCredentialPresent() {
		t.Fatal("an empty path must disable the SIM path")
	}
}

func TestFindSimField(t *testing.T) {
	xmlText := `<hierarchy><node class="android.widget.EditText" resource-id="com.android.phone:id/simPinEntry" bounds="[10,20][110,220]" /></hierarchy>`
	nodes := parseTestNodes(xmlText)
	field, ok := findSimField(nodes)
	if !ok {
		t.Fatal("the SIM PIN field was not found")
	}
	x, y := field.Center()
	if x != 60 || y != 120 {
		t.Fatalf("centre = %d,%d, want 60,120", x, y)
	}
}
