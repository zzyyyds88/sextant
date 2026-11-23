package config

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "core.json")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestMissingFileUsesDefaults(t *testing.T) {
	cfg, existed, err := Load(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil {
		t.Fatal(err)
	}
	if existed {
		t.Fatal("existed should be false")
	}
	if cfg.Listen != "0.0.0.0:8443" {
		t.Fatalf("default listen = %q", cfg.Listen)
	}
}

func TestUnknownFieldIsRejected(t *testing.T) {
	p := write(t, `{"schema":1,"listen":"0.0.0.0:8443","data_dir":"/tmp/x","typo_field":1}`)
	if _, _, err := Load(p); err == nil {
		t.Fatal("unknown field accepted")
	}
}

func TestIMEServiceAndProviderMustDiffer(t *testing.T) {
	p := write(t, `{"schema":1,"listen":"0.0.0.0:8443","data_dir":"/tmp/x",
	  "app":{"package":"com.x","a11y":"com.x/.A","ime_service":"com.x/.I","ime_provider":"com.x/.I"}}`)
	if _, _, err := Load(p); err == nil {
		t.Fatal("identical ime_service and ime_provider accepted")
	}
}

func TestIntervalIsClamped(t *testing.T) {
	p := write(t, `{"schema":1,"listen":"0.0.0.0:8443","data_dir":"/tmp/x",
	  "app":{"package":"com.x","a11y":"com.x/.A","ime_service":"com.x/.I","ime_provider":"com.x.ime"},
	  "keepalive":{"interval_sec":1}}`)
	cfg, _, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Keepalive.IntervalSec < 10 {
		t.Fatalf("interval not clamped: %d", cfg.Keepalive.IntervalSec)
	}
}

func TestSchemaMismatchIsRejected(t *testing.T) {
	p := write(t, `{"schema":99,"listen":"0.0.0.0:8443","data_dir":"/tmp/x",
	  "app":{"package":"com.x","a11y":"com.x/.A","ime_service":"com.x/.I","ime_provider":"com.x.ime"}}`)
	if _, _, err := Load(p); err == nil {
		t.Fatal("schema 99 accepted")
	}
}

func TestPathResolvesRelative(t *testing.T) {
	c := Default()
	if got := c.Path("config/unlock-secret"); got != DefaultDataDir+"/config/unlock-secret" {
		t.Fatalf("Path = %q", got)
	}
	if got := c.Path("/abs/x"); got != "/abs/x" {
		t.Fatalf("absolute Path = %q", got)
	}
}
