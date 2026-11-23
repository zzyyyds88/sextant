package main

import (
	"strings"
	"testing"
)

// TestPairRequiresSerial pins the public-build contract: no device address is
// compiled in, so `pair` without --serial or SEXTANT_SERIAL must fail before it
// tries to reach a host.
func TestPairRequiresSerial(t *testing.T) {
	old := defaultSerial
	defaultSerial = ""
	defer func() { defaultSerial = old }()
	err := cmdPair(nil)
	if err == nil || !strings.Contains(err.Error(), "missing device serial") {
		t.Fatalf("pair without a serial = %v, want a missing-device-serial error", err)
	}
}

// TestValuelessFlagDoesNotSwallowTheText pins the bug that made the documented
// `sextant text --ime "你好"` unusable: the flag took the next token as its value,
// so the text became "true" and the command answered with a usage error about a
// missing argument. Measured on the device: `text --ime "这是…"` printed
// "usage: sextant text TEXT [--ime] [--clear] [--at x,y]".
func TestValuelessFlagDoesNotSwallowTheText(t *testing.T) {
	a, err := parseArgs([]string{"--ime", "你好"})
	if err != nil {
		t.Fatal(err)
	}
	if !a.has("ime") {
		t.Fatal("--ime was not recorded")
	}
	if got := a.str(0, ""); got != "你好" {
		t.Fatalf("text = %q, want 你好", got)
	}
}

// TestShortOutputFlag covers the other half of the same parser: the help text
// prints `get PATH -o FILE` and every call was rejected with the usage string.
func TestShortOutputFlag(t *testing.T) {
	a, err := parseArgs([]string{"/data/adb/x.log", "-o", "/tmp/x.log"})
	if err != nil {
		t.Fatal(err)
	}
	if got := a.optStr("o", ""); got != "/tmp/x.log" {
		t.Fatalf("-o = %q", got)
	}
	if got := a.str(0, ""); got != "/data/adb/x.log" {
		t.Fatalf("path = %q", got)
	}
}

// TestNegativeNumbersStayPositional keeps `pixels --region` / `tap` usable: a
// region may start at a negative coordinate on a secondary display.
func TestNegativeNumbersStayPositional(t *testing.T) {
	a, err := parseArgs([]string{"-3", "5"})
	if err != nil {
		t.Fatal(err)
	}
	if len(a.pos) != 2 || a.pos[0] != "-3" {
		t.Fatalf("positionals = %v", a.pos)
	}
	if len(a.opts) != 0 {
		t.Fatalf("options = %v", a.opts)
	}
}

// TestValuedFlagKeepsNegativeValue checks the boundary of the same rule: after a
// flag that takes a value, a negative number must still be that value.
func TestValuedFlagKeepsNegativeValue(t *testing.T) {
	a, err := parseArgs([]string{"--region", "-5", "--timeout-ms", "300"})
	if err != nil {
		t.Fatal(err)
	}
	if got := a.optStr("region", ""); got != "-5" {
		t.Fatalf("region = %q", got)
	}
}

// TestUnquotedRegionKeepsItsFourNumbers covers the spelling the help text prints:
// `ocr --region L T R B` used to keep only `L`, and the command then failed with
// "--region needs four numbers" on its own documented form.
func TestUnquotedRegionKeepsItsFourNumbers(t *testing.T) {
	a, err := parseArgs([]string{"--region", "0", "0", "1080", "400"})
	if err != nil {
		t.Fatal(err)
	}
	if got := a.optStr("region", ""); got != "0 0 1080 400" {
		t.Fatalf("region = %q", got)
	}
	if len(a.pos) != 0 {
		t.Fatalf("leftover positionals = %v", a.pos)
	}
}
