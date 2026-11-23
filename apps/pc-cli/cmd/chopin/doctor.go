package main

import (
	"encoding/json"
	"fmt"
	"strings"
)

// doctor answers "why is this not working" in one command. It reads only the
// same published truth as status/watchdog/page, and every line comes from a
// real response; a check that cannot be read is a failure, never a silent pass.

type doctorStatus struct {
	Schema    int    `json:"schema"`
	UpdatedAt string `json:"updated_at"`
	Daemon    struct {
		State   string `json:"state"`
		PID     int    `json:"pid"`
		Version string `json:"version"`
	} `json:"daemon"`
	App struct {
		Installed bool   `json:"installed"`
		Version   string `json:"version_name"`
		A11y      struct {
			State  string `json:"state"`
			Detail string `json:"detail"`
			Compat string `json:"compat"`
		} `json:"a11y"`
		Bg struct {
			OK        bool   `json:"ok"`
			Autostart string `json:"autostart"`
			BgControl string `json:"bgcontrol"`
		} `json:"bg"`
	} `json:"app"`
	Unlock struct {
		ScreenLock bool   `json:"screen_locked"`
		Credential string `json:"credential"`
	} `json:"unlock"`
}

func cmdDoctor(a *args) error {
	c, err := paired()
	if err != nil {
		return fmt.Errorf("pairing: %w", err)
	}
	failures := 0
	check := func(name string, fn func() (string, error)) {
		detail, err := fn()
		if err != nil {
			failures++
			fmt.Printf("FAIL %-12s %v\n", name, err)
			return
		}
		fmt.Printf("ok   %-12s %s\n", name, detail)
	}

	check("health", func() (string, error) {
		raw, err := c.Get("/v1/health")
		if err != nil {
			return "", err
		}
		var h map[string]any
		if err := json.Unmarshal(raw, &h); err != nil {
			return "", err
		}
		return fmt.Sprintf("version=%v", h["version"]), nil
	})

	var st doctorStatus
	raw, statusErr := c.Get("/v1/status")
	if statusErr != nil {
		fmt.Printf("FAIL %-12s %v\n", "status", statusErr)
		failures++
	} else if err := json.Unmarshal(raw, &st); err != nil {
		fmt.Printf("FAIL %-12s %v\n", "status", err)
		failures++
	} else {
		check("status", func() (string, error) {
			if st.Schema != 1 {
				return "", fmt.Errorf("schema %d, want 1", st.Schema)
			}
			return fmt.Sprintf("schema=%d updated_at=%s", st.Schema, st.UpdatedAt), nil
		})
		check("daemon", func() (string, error) {
			if st.Daemon.State == "" {
				return "", fmt.Errorf("daemon state is empty")
			}
			return fmt.Sprintf("state=%s pid=%d version=%s", st.Daemon.State, st.Daemon.PID, st.Daemon.Version), nil
		})
		check("a11y", func() (string, error) {
			if st.App.A11y.State != "OK" {
				return "", fmt.Errorf("state=%s detail=%q compat=%q", st.App.A11y.State, st.App.A11y.Detail, st.App.A11y.Compat)
			}
			return fmt.Sprintf("state=OK compat=%s", st.App.A11y.Compat), nil
		})
		check("background", func() (string, error) {
			if !st.App.Bg.OK {
				return "", fmt.Errorf("autostart=%s bgcontrol=%s", st.App.Bg.Autostart, st.App.Bg.BgControl)
			}
			return fmt.Sprintf("autostart=%s bgcontrol=%s", st.App.Bg.Autostart, st.App.Bg.BgControl), nil
		})
		check("app", func() (string, error) {
			if !st.App.Installed {
				return "", fmt.Errorf("com.chopin.pilot is not installed")
			}
			return "installed version=" + st.App.Version, nil
		})
	}

	check("watchdog", func() (string, error) {
		raw, err := c.Get("/v1/watchdog")
		if err != nil {
			return "", err
		}
		var w struct {
			Helper struct {
				Available bool   `json:"available"`
				Error     string `json:"error"`
			} `json:"helper"`
		}
		if err := json.Unmarshal(raw, &w); err != nil {
			return "", err
		}
		if !w.Helper.Available {
			return "", fmt.Errorf("helper unavailable: %s", w.Helper.Error)
		}
		return "helper available", nil
	})

	check("page", func() (string, error) {
		raw, err := c.Get("/v1/ui/page")
		if err != nil {
			return "", err
		}
		var p struct {
			Package   string `json:"package"`
			Component string `json:"component"`
		}
		if err := json.Unmarshal(raw, &p); err != nil {
			return "", err
		}
		if p.Package == "" {
			return "", fmt.Errorf("no foreground package in the response")
		}
		return fmt.Sprintf("package=%s component=%s", p.Package, p.Component), nil
	})

	if failures > 0 {
		return fmt.Errorf("%d check(s) failed", failures)
	}
	return nil
}

// cmdLog prints the daemon's own log tail. It reads the same file endpoint the
// journey uses, so there is one log path and one access rule.
func cmdLog(a *args) error {
	n, err := a.optInt("lines", 80)
	if err != nil {
		return err
	}
	if n <= 0 {
		return fmt.Errorf("--lines must be positive")
	}
	c, err := paired()
	if err != nil {
		return err
	}
	data, err := c.Get("/v1/files/download?path=" + urlEscape(deviceLogPath))
	if err != nil {
		return err
	}
	lines := strings.Split(strings.TrimRight(string(data), "\n"), "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	fmt.Println(strings.Join(lines, "\n"))
	return nil
}
