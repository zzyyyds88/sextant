package device

import (
	"strings"
	"testing"
)

// Risky is the gate that stopped being theoretical: turning airplane mode on over
// the API dropped the only route to this phone (`adb connect` → No route to host)
// and nothing on this side could turn it back off. The list is a judgement about
// the device, so the test pins the judgement rather than the mechanism.
func TestRiskyCoversTheTwoRadiosThatCutTheLink(t *testing.T) {
	for _, name := range []string{"airplane_mode", "wifi"} {
		why, ok := Risky(name)
		if !ok {
			t.Fatalf("%s must be risky: it can strand the device", name)
		}
		if !strings.Contains(why, "Wi-Fi") && !strings.Contains(why, "radio") {
			t.Fatalf("%s risk text does not say what is lost: %q", name, why)
		}
	}
}

// Losing Bluetooth costs a headset, not the device. Treating it as dangerous would
// teach the agent to click through confirmations, which is how a real one gets
// waved past.
func TestRiskyDoesNotIncludeFeatureLosses(t *testing.T) {
	for _, name := range []string{"bluetooth", "location", "night_mode", "volume_music"} {
		if _, ok := Risky(name); ok {
			t.Fatalf("%s is not dangerous; putting it behind a confirmation dilutes the real ones", name)
		}
	}
}

func TestRiskyIsCaseAndSpaceInsensitive(t *testing.T) {
	if _, ok := Risky("  Airplane_Mode  "); !ok {
		t.Fatal("a setting name with different case or spacing was not recognised as risky")
	}
}

func TestParseBoolAcceptsWhatCallersActuallySend(t *testing.T) {
	yes := []string{"1", "true", "on", "enabled", "yes", "TRUE", "On"}
	for _, v := range yes {
		if b, err := parseBool(v); err != nil || !b {
			t.Fatalf("parseBool(%q) = %v, %v; want true", v, b, err)
		}
	}
	no := []string{"0", "false", "off", "disabled", "no"}
	for _, v := range no {
		if b, err := parseBool(v); err != nil || b {
			t.Fatalf("parseBool(%q) = %v, %v; want false", v, b, err)
		}
	}
	if _, err := parseBool("maybe"); err == nil {
		t.Fatal("parseBool accepted a value that is not a boolean")
	}
}
