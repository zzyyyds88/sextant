// Package config loads and validates the daemon configuration.
//
// The file is a single declarative JSON document (DESIGN §7). Unknown fields are
// a hard error: a typo in a config key silently doing nothing is exactly the
// class of bug that made the old shell layer unmaintainable.
package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
)

// DefaultDataDir is fixed by the module, not configurable: every runtime path
// (lock, pid, tokens, baseline, status) derives from it, and letting the user
// move it turns "edit a config" into "move the state directory".
const DefaultDataDir = "/data/adb/chopin-core"

// SchemaVersion is the config schema this binary understands.
const SchemaVersion = 1

type App struct {
	Package string `json:"package"`
	A11y    string `json:"a11y"`
	// A11yCompat is a second, zero-function accessibility service that must also
	// be bound. WeChat only exposes its tree when the bound set of services
	// contains a component name it likes; the name that unlocks it is kept
	// separate from the service that reads it (DESIGN §9.3).
	A11yCompat  string `json:"a11y_compat"`
	IMEService  string `json:"ime_service"`
	IMEProvider string `json:"ime_provider"`
	HUDPackage  string `json:"hud_package"`
}

type A11y struct {
	Enabled                    bool `json:"enabled"`
	SettleMaxSec               int  `json:"settle_max_sec"`
	SkipWhenForegroundSettings bool `json:"skip_when_foreground_settings"`
}

type Bg struct {
	Enabled   bool   `json:"enabled"`
	Autostart string `json:"autostart"`
	BgControl string `json:"bgcontrol"`
	Doze      bool   `json:"doze"`
}

type Keepalive struct {
	IntervalSec int      `json:"interval_sec"`
	A11y        A11y     `json:"a11y"`
	Bg          Bg       `json:"bg"`
	Perms       []string `json:"perms"`
}

type Unlock struct {
	CredentialFile string `json:"credential_file"`
	// SIMPinFile is optional; an empty value disables the SIM PIN path entirely
	// (a device whose SIM has no PIN must not be treated as broken).
	SIMPinFile  string `json:"sim_pin_file,omitempty"`
	Attempts    int    `json:"attempts"`
	CooldownSec int    `json:"cooldown_sec"`
	AutoOnBoot  bool   `json:"auto_on_boot"`
}

// Hud holds the overlay channel's timing.
type Hud struct {
	// HeartbeatSec is how long the overlay may stay silent before it is treated
	// as gone and stops being considered a safety net.
	HeartbeatSec int `json:"heartbeat_sec"`
	// CaptureSettleMs is how long the overlay gets to hide before a capture.
	CaptureSettleMs int `json:"capture_settle_ms"`
	// IdleHideSec is how long after the last action a session-less overlay
	// disappears again. The overlay is shown by ACTIONS, not only by a claimed
	// session, so it also has to be taken down by the absence of them.
	IdleHideSec int `json:"idle_hide_sec"`
}

type Session struct {
	IdleSec int `json:"idle_sec"`
	// ReapSec is how often the daemon checks for sessions past IdleSec.
	// 0 disables the background reaper; POST /v1/guard/reap still works.
	ReapSec int `json:"reap_sec"`
	// StaleSec is the heartbeat-loss threshold used by Reap (default 180 =
	// 3 x heartbeat_sec). 0 falls back to idle_sec alone.
	StaleSec int `json:"stale_sec"`
	// ZombieSec releases a session whose heartbeat is still fresh but which has
	// had no business activity for this long (default 300). 0 disables it.
	ZombieSec        int `json:"zombie_sec"`
	LingerSec        int `json:"linger_sec"`
	HeartbeatSec     int `json:"heartbeat_sec"`
	GuardStaleSec    int `json:"guard_stale_sec"`
	RosterBanSec     int `json:"roster_ban_sec"`
	MaxWriteSessions int `json:"max_write_sessions"`
}

type Helper struct {
	Socket string `json:"socket"`
	Dex    string `json:"dex"`
}

type Scrcpy struct {
	Jar              string `json:"jar"`
	MaxSize          int    `json:"max_size"`
	BitRate          int    `json:"bit_rate"`
	MaxFPS           int    `json:"max_fps"`
	StreamMaxViewers int    `json:"stream_max_viewers"`
}

type Audit struct {
	MaxBytes int `json:"max_bytes"`
	KeepDays int `json:"keep_days"`
}

type Limits struct {
	MaxBodyBytes      int64 `json:"max_body_bytes"`
	RequestTimeoutSec int   `json:"request_timeout_sec"`
}

// Human is the D30 injection profile: a random point inside the target, a paced
// gap between injections, and scaled waits. Disabled means the daemon injects
// the exact coordinate it was given, which is the control arm for A/B runs.
type Human struct {
	Enabled    bool `json:"enabled"`
	GapMinMS   int  `json:"gap_min_ms"`
	GapMaxMS   int  `json:"gap_max_ms"`
	TapInset   int  `json:"tap_inset"`
	JitterPx   int  `json:"jitter_px"`
	WaitMinPct int  `json:"wait_min_pct"`
	WaitMaxPct int  `json:"wait_max_pct"`
}

// Challenge is the D30 verification-wall policy.
type Challenge struct {
	Enabled        bool `json:"enabled"`
	SliderAttempts int  `json:"slider_attempts"`
	CooldownSec    int  `json:"cooldown_sec"`
	RiskDays       int  `json:"risk_days"`
}

// Config is the whole on-device configuration.
type Config struct {
	Schema    int       `json:"schema"`
	Listen    string    `json:"listen"`
	DataDir   string    `json:"data_dir"`
	App       App       `json:"app"`
	Keepalive Keepalive `json:"keepalive"`
	Unlock    Unlock    `json:"unlock"`
	Session   Session   `json:"session"`
	Hud       Hud       `json:"hud"`
	Helper    Helper    `json:"helper"`
	Scrcpy    Scrcpy    `json:"scrcpy"`
	Audit     Audit     `json:"audit"`
	Limits    Limits    `json:"limits"`
	Human     Human     `json:"human"`
	Challenge Challenge `json:"challenge"`
}

// Default returns the built-in configuration (documented in DESIGN §7).
func Default() Config {
	return Config{
		Schema:  SchemaVersion,
		Listen:  "0.0.0.0:8443",
		DataDir: DefaultDataDir,
		App: App{
			Package:     "com.chopin.pilot",
			A11y:        "com.chopin.pilot/.hud.RemoteA11yService",
			A11yCompat:  "com.chopin.pilot/com.google.android.accessibility.selecttospeak.SelectToSpeakService",
			IMEService:  "com.chopin.pilot/.ime.RemoteImeService",
			IMEProvider: "com.chopin.pilot.ime",
			HUDPackage:  "com.chopin.pilot",
		},
		Keepalive: Keepalive{
			IntervalSec: 15,
			A11y:        A11y{Enabled: true, SettleMaxSec: 6, SkipWhenForegroundSettings: true},
			Bg:          Bg{Enabled: true, Autostart: "allow", BgControl: "noRestrict", Doze: true},
			Perms:       []string{"idle", "runtime"},
		},
		Unlock: Unlock{CredentialFile: "config/unlock-secret", Attempts: 1, CooldownSec: 30, AutoOnBoot: true},
		Session: Session{
			IdleSec: 900, ReapSec: 60, StaleSec: 180, ZombieSec: 300, LingerSec: 90, HeartbeatSec: 60,
			GuardStaleSec: 600, RosterBanSec: 900, MaxWriteSessions: 2,
		},
		Hud:    Hud{HeartbeatSec: 15, IdleHideSec: 25},
		Helper: Helper{Socket: "@chopin-core-helper", Dex: "lib/helper.jar"},
		Scrcpy: Scrcpy{Jar: "lib/scrcpy-server.jar", MaxSize: 1280, BitRate: 4000000, MaxFPS: 30, StreamMaxViewers: 3},
		Audit:  Audit{MaxBytes: 1 << 20, KeepDays: 30},
		Limits: Limits{MaxBodyBytes: 1 << 20, RequestTimeoutSec: 1800},
		Human: Human{
			Enabled: true, GapMinMS: 700, GapMaxMS: 1600,
			TapInset: 6, JitterPx: 6, WaitMinPct: 80, WaitMaxPct: 135,
		},
		Challenge: Challenge{Enabled: true, SliderAttempts: 2, CooldownSec: 600, RiskDays: 7},
	}
}

// Load reads path. A missing file is not an error: the caller decides whether
// that means "first run" (use defaults) or "misconfigured install".
func Load(path string) (Config, bool, error) {
	cfg := Default()
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, false, nil
		}
		return cfg, false, fmt.Errorf("read config %s: %w", path, err)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&cfg); err != nil {
		return cfg, true, fmt.Errorf("parse config %s: %w", path, err)
	}
	if err := cfg.Validate(); err != nil {
		return cfg, true, fmt.Errorf("config %s: %w", path, err)
	}
	return cfg, true, nil
}

// Validate rejects values that would produce a confusing runtime failure. It
// clamps documented minimums rather than accepting them silently.
func (c *Config) Validate() error {
	if c.Schema != SchemaVersion {
		return fmt.Errorf("unsupported schema %d (want %d)", c.Schema, SchemaVersion)
	}
	if c.Listen == "" {
		return fmt.Errorf("listen is empty")
	}
	if c.DataDir == "" {
		return fmt.Errorf("data_dir is empty")
	}
	if c.App.Package == "" {
		return fmt.Errorf("app.package is empty")
	}
	if c.App.A11y == "" {
		return fmt.Errorf("app.a11y is empty")
	}
	if c.App.A11yCompat == c.App.A11y {
		return fmt.Errorf("app.a11y_compat must differ from app.a11y: one service does the work, the other only unlocks the tree")
	}
	if c.App.IMEService == "" || c.App.IMEProvider == "" {
		return fmt.Errorf("app.ime_service and app.ime_provider are both required")
	}
	if c.App.IMEService == c.App.IMEProvider {
		return fmt.Errorf("app.ime_service must not equal app.ime_provider: one is a component, the other a provider authority")
	}
	if c.Keepalive.IntervalSec < 10 {
		c.Keepalive.IntervalSec = 10
	}
	if c.Session.MaxWriteSessions < 1 {
		c.Session.MaxWriteSessions = 1
	}
	if c.Session.ReapSec < 0 {
		return fmt.Errorf("session.reap_sec must be >= 0 (0 disables the background reaper)")
	}
	if c.Session.StaleSec < 0 {
		return fmt.Errorf("session.stale_sec must be >= 0 (0 falls back to idle_sec)")
	}
	if c.Session.ZombieSec < 0 {
		return fmt.Errorf("session.zombie_sec must be >= 0 (0 disables zombie reaping)")
	}
	if c.Limits.MaxBodyBytes <= 0 {
		c.Limits.MaxBodyBytes = 1 << 20
	}
	if c.Audit.MaxBytes <= 0 {
		c.Audit.MaxBytes = 1 << 20
	}
	if c.Scrcpy.Jar == "" {
		c.Scrcpy.Jar = "lib/scrcpy-server.jar"
	}
	// D30 human profile. Clamp rather than reject: a slightly wrong number
	// should not stop the daemon, but a nonsensical one must not reach the
	// injection layer unexamined.
	if c.Human.TapInset < 1 {
		c.Human.TapInset = 6
	}
	if c.Human.JitterPx < 0 {
		c.Human.JitterPx = 0
	}
	if c.Human.GapMinMS < 0 {
		c.Human.GapMinMS = 0
	}
	if c.Human.GapMaxMS < c.Human.GapMinMS {
		c.Human.GapMaxMS = c.Human.GapMinMS
	}
	if c.Human.WaitMinPct < 50 || c.Human.WaitMinPct > 200 {
		c.Human.WaitMinPct = 80
	}
	if c.Human.WaitMaxPct < c.Human.WaitMinPct || c.Human.WaitMaxPct > 200 {
		c.Human.WaitMaxPct = c.Human.WaitMinPct
	}
	if c.Challenge.SliderAttempts < 1 {
		c.Challenge.SliderAttempts = 2
	}
	if c.Challenge.CooldownSec < 0 {
		c.Challenge.CooldownSec = 0
	}
	if c.Challenge.RiskDays < 1 {
		c.Challenge.RiskDays = 7
	}
	return nil
}

// Path resolves a config-relative path against DataDir.
func (c Config) Path(rel string) string {
	if rel == "" {
		return ""
	}
	if rel[0] == '/' {
		return rel
	}
	return c.DataDir + "/" + rel
}
