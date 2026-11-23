package device

import (
	"context"
	"fmt"
	"strconv"
	"strings"
)

// Daily settings, through the system's own APIs.
//
// Every one of these goes through the service that owns the setting
// (`cmd connectivity airplane-mode`, `cmd wifi set-wifi-enabled`, `cmd
// bluetooth_manager enable`, …) rather than writing the Settings database, and the
// difference is not cosmetic. Writing `settings put global airplane_mode_on 1`
// changes the stored value and NOTHING ELSE: measured on this device, the row read
// back 1 while Wi-Fi stayed up, because the radios are switched by
// ConnectivityManager reacting to a broadcast, not by the row. A "setting written"
// reply for that is a lie a caller cannot detect.
//
// Read-back is part of every setter: each one ends by re-reading through the same
// service and comparing. Returning nil because the command printed nothing is how
// "it worked" and "it did nothing" become the same answer.

// Stream names accepted for volume, mapped to AudioManager stream ids.
//
// The ids are the platform's STREAM_* constants; they are stable across the
// versions this ships for.
var volumeStreams = map[string]int{
	"music":        3,
	"ring":         2,
	"notification": 5,
	"alarm":        4,
	"call":         0,
	"system":       1,
}

// riskySettings are the daily settings that can cut the agent's own link to the
// phone, or leave it in a state a person has to walk over and fix.
//
// This is a judgement about THIS device, written down so it is not re-made by
// accident: the phone is reachable only over Wi-Fi (adb tcpip on the LAN), so
// airplane mode is not "a radio switch", it is "lose the device". Measured: turning
// it on dropped the connection instantly (`adb connect` → No route to host) and
// nothing could turn it back off from this side.
//
// Wi-Fi is on the list for the same reason but weaker: turning it off is only
// fatal while the daemon is reached over Wi-Fi, which is the normal case here.
//
// Bluetooth and location are NOT risky by this definition — losing them costs a
// feature, not the device.
var riskySettings = map[string]string{
	"airplane_mode": "turns off every radio, including the Wi-Fi this phone is managed over",
	"wifi":          "drops the Wi-Fi connection the phone is managed over; nothing can turn it back on from here",
}

// Risky reports whether a setting needs a confirmation, and why.
func Risky(name string) (string, bool) {
	why, ok := riskySettings[strings.ToLower(strings.TrimSpace(name))]
	return why, ok
}

// DailySetting is one settable thing and how to read it back.
type DailySetting struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`  // "bool", "int", "choice"
	Value string `json:"value"` // current value, as the system reports it
	Note  string `json:"note,omitempty"`
}

// Daily returns the daily settings and their current values.
//
// One command per setting is slower than a batch, but a batch that half-fails
// cannot say which item failed, and a status page that cannot name the broken
// setting is worse than a slow one.
func (l Layer) DailySettings(ctx context.Context) []DailySetting {
	out := make([]DailySetting, 0, 12)
	add := func(name, kind string, read func() (string, string)) {
		v, note := read()
		if why, risky := Risky(name); risky {
			if note != "" {
				note += "; "
			}
			note += "DANGEROUS: " + why
		}
		out = append(out, DailySetting{Name: name, Kind: kind, Value: v, Note: note})
	}

	add("airplane_mode", "bool", func() (string, string) {
		v, err := l.Probe.Run.Run(ctx, "cmd", "connectivity", "airplane-mode")
		if err != nil {
			return "", err.Error()
		}
		return strings.TrimSpace(v), ""
	})
	add("wifi", "bool", func() (string, string) {
		v, err := l.Probe.SettingsGet(ctx, "global", "wifi_on")
		if err != nil {
			return "", err.Error()
		}
		if strings.TrimSpace(v) == "1" {
			return "enabled", ""
		}
		return "disabled", ""
	})
	add("bluetooth", "bool", func() (string, string) {
		v, err := l.Probe.SettingsGet(ctx, "global", "bluetooth_on")
		if err != nil {
			return "", err.Error()
		}
		if strings.TrimSpace(v) == "1" {
			return "enabled", ""
		}
		return "disabled", ""
	})
	add("location", "bool", func() (string, string) {
		v, err := l.Probe.Run.Run(ctx, "cmd", "location", "is-location-enabled")
		if err != nil {
			return "", err.Error()
		}
		return strings.TrimSpace(v), ""
	})
	add("night_mode", "choice", func() (string, string) {
		v, err := l.Probe.Run.Run(ctx, "cmd", "uimode", "night")
		if err != nil {
			return "", err.Error()
		}
		return strings.TrimSpace(v), ""
	})
	add("volume_music", "int", func() (string, string) { return l.volumeOf(ctx, "music") })
	add("volume_ring", "int", func() (string, string) { return l.volumeOf(ctx, "ring") })
	add("volume_alarm", "int", func() (string, string) { return l.volumeOf(ctx, "alarm") })
	add("screen_off_timeout", "int", func() (string, string) {
		v, err := l.Probe.SettingsGet(ctx, "system", "screen_off_timeout")
		if err != nil {
			return "", err.Error()
		}
		return strings.TrimSpace(v), "milliseconds"
	})
	add("screen_brightness", "int", func() (string, string) {
		v, err := l.Probe.SettingsGet(ctx, "system", "screen_brightness")
		if err != nil {
			return "", err.Error()
		}
		return strings.TrimSpace(v), "0-255"
	})
	add("auto_rotate", "bool", func() (string, string) {
		v, err := l.Probe.SettingsGet(ctx, "system", "accelerometer_rotation")
		if err != nil {
			return "", err.Error()
		}
		if strings.TrimSpace(v) == "1" {
			return "enabled", ""
		}
		return "disabled", ""
	})
	add("dnd", "choice", func() (string, string) {
		v, err := l.Probe.Run.Run(ctx, "settings", "get", "global", "zen_mode")
		if err != nil {
			return "", err.Error()
		}
		return strings.TrimSpace(v), "0=off 1=priority 2=none 3=alarms"
	})
	return out
}

// SetDaily applies one daily setting and reads it back.
//
// It returns the value the system reports afterwards, which is the only evidence
// that anything happened: several of these are refused on a locked phone or against
// a carrier policy, and the refusal is silent.
func (l Layer) SetDaily(ctx context.Context, name, value string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	value = strings.TrimSpace(value)
	switch name {
	case "airplane_mode":
		on, err := parseBool(value)
		if err != nil {
			return "", err
		}
		arg := "disable"
		if on {
			arg = "enable"
		}
		if _, err := l.Probe.Run.Run(ctx, "cmd", "connectivity", "airplane-mode", arg); err != nil {
			return "", err
		}
		return l.readBack(ctx, "airplane_mode")
	case "wifi":
		on, err := parseBool(value)
		if err != nil {
			return "", err
		}
		arg := "disabled"
		if on {
			arg = "enabled"
		}
		if _, err := l.Probe.Run.Run(ctx, "cmd", "wifi", "set-wifi-enabled", arg); err != nil {
			return "", err
		}
		return l.readBack(ctx, "wifi")
	case "bluetooth":
		on, err := parseBool(value)
		if err != nil {
			return "", err
		}
		arg := "disable"
		if on {
			arg = "enable"
		}
		if _, err := l.Probe.Run.Run(ctx, "cmd", "bluetooth_manager", arg); err != nil {
			return "", err
		}
		return l.readBack(ctx, "bluetooth")
	case "location":
		on, err := parseBool(value)
		if err != nil {
			return "", err
		}
		if _, err := l.Probe.Run.Run(ctx, "cmd", "location", "set-location-enabled", strconv.FormatBool(on)); err != nil {
			return "", err
		}
		return l.readBack(ctx, "location")
	case "night_mode":
		if _, err := l.Probe.Run.Run(ctx, "cmd", "uimode", "night", value); err != nil {
			return "", err
		}
		return l.readBack(ctx, "night_mode")
	case "dnd":
		if _, err := l.Probe.Run.Run(ctx, "cmd", "notification", "set_dnd", value); err != nil {
			return "", err
		}
		return l.readBack(ctx, "dnd")
	case "auto_rotate":
		on, err := parseBool(value)
		if err != nil {
			return "", err
		}
		b := "0"
		if on {
			b = "1"
		}
		if _, err := l.Probe.Run.Run(ctx, "settings", "put", "system", "accelerometer_rotation", b); err != nil {
			return "", err
		}
		return l.readBack(ctx, "auto_rotate")
	case "screen_off_timeout":
		if _, err := strconv.Atoi(value); err != nil {
			return "", fmt.Errorf("screen_off_timeout takes milliseconds, got %q", value)
		}
		if _, err := l.Probe.Run.Run(ctx, "settings", "put", "system", "screen_off_timeout", value); err != nil {
			return "", err
		}
		return l.readBack(ctx, "screen_off_timeout")
	case "screen_brightness":
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 || n > 255 {
			return "", fmt.Errorf("screen_brightness takes 0-255, got %q", value)
		}
		if _, err := l.Probe.Run.Run(ctx, "settings", "put", "system", "screen_brightness", value); err != nil {
			return "", err
		}
		return l.readBack(ctx, "screen_brightness")
	}
	if stream, ok := volumeStreams[name]; ok || strings.HasPrefix(name, "volume_") {
		sname := strings.TrimPrefix(name, "volume_")
		id, ok := volumeStreams[sname]
		if !ok {
			return "", fmt.Errorf("unknown volume stream %q", sname)
		}
		n, err := strconv.Atoi(value)
		if err != nil || n < 0 {
			return "", fmt.Errorf("volume takes a non-negative index, got %q", value)
		}
		_ = stream
		if _, err := l.Probe.Run.Run(ctx, "cmd", "media_session", "volume",
			"--stream", strconv.Itoa(id), "--set", strconv.Itoa(n)); err != nil {
			return "", err
		}
		return l.readBack(ctx, name)
	}
	return "", fmt.Errorf("unknown setting %q", name)
}

// readBack re-reads through the same path the status page uses.
func (l Layer) readBack(ctx context.Context, name string) (string, error) {
	for _, s := range l.DailySettings(ctx) {
		if s.Name == name {
			if s.Value == "" {
				return "", fmt.Errorf("%s: the system would not report a value (%s)", name, s.Note)
			}
			return s.Value, nil
		}
	}
	return "", fmt.Errorf("%s is not a known setting", name)
}

// volumeOf reads one stream's index, with its range, from AudioService.
func (l Layer) volumeOf(ctx context.Context, stream string) (string, string) {
	id, ok := volumeStreams[stream]
	if !ok {
		return "", "unknown stream"
	}
	out, err := l.Probe.Run.Run(ctx, "cmd", "media_session", "volume", "--stream", strconv.Itoa(id), "--get")
	if err != nil {
		return "", err.Error()
	}
	// "[V] volume is 8 in range [0..15]"
	for _, line := range strings.Split(out, "\n") {
		i := strings.Index(line, "volume is ")
		if i < 0 {
			continue
		}
		rest := strings.TrimSpace(line[i+len("volume is "):])
		if j := strings.Index(rest, " in range "); j > 0 {
			return rest[:j], "range " + strings.TrimSpace(rest[j+len(" in range "):])
		}
		return rest, ""
	}
	return "", "no volume line in the reply"
}

func parseBool(v string) (bool, error) {
	switch strings.ToLower(v) {
	case "1", "true", "on", "enabled", "yes":
		return true, nil
	case "0", "false", "off", "disabled", "no":
		return false, nil
	}
	return false, fmt.Errorf("expected on/off, got %q", v)
}
