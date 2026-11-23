package main

import (
	"fmt"
	"net/url"
	"strings"
)

// D30 CLI surface: verification walls and handover.
//
// These are thin mappings to the daemon. The classification and the decision to
// refuse are the daemon's; the CLI only reports them.

func cmdChallenge(a *args) error {
	body := map[string]any{}
	if frame := a.optStr("frame", ""); frame != "" {
		body["frame_id"] = frame
	}
	return cmdPost("/v1/ui/challenge", body)
}

func cmdChallengeSolve(a *args) error {
	body := map[string]any{}
	if dx, err := a.optInt("dx", 0); err == nil && dx > 0 {
		body["dx"] = dx
	}
	if seed := a.optStr("seed", ""); seed != "" {
		body["seed"] = seed
	}
	headers := map[string]string{}
	if seed, _ := body["seed"].(string); seed != "" {
		headers["X-Sextant-Request-Id"] = seed + ":challenge_solve"
	}
	c, err := paired()
	if err != nil {
		return err
	}
	raw, err := c.DoWithHeaders("POST", "/v1/ui/challenge/solve", body, headers)
	if err != nil {
		return err
	}
	return printJSON(raw)
}

func cmdRisk(a *args) error {
	path := "/v1/challenge/risk"
	if pkg := a.optStr("package", ""); pkg != "" {
		path += "?package=" + url.QueryEscape(pkg)
	}
	return cmdGet(path)
}

func cmdHandover(a *args) error {
	c, err := paired()
	if err != nil {
		return err
	}
	if a.has("status") {
		return cmdGet("/v1/device/handover")
	}
	if a.has("return") {
		body := map[string]any{"launch_on_agent_display": a.has("relaunch")}
		raw, err := c.Post("/v1/device/handover/return", body)
		if err != nil {
			return err
		}
		return printJSON(raw)
	}
	pkg := a.optStr("package", "")
	component := a.optStr("component", "")
	if pkg == "" && component == "" {
		return fmt.Errorf("usage: sextant handover --package P [--reason TEXT] | --return [--relaunch] | --status")
	}
	reason := strings.TrimSpace(strings.Join(a.pos, " "))
	if reason == "" {
		reason = a.optStr("reason", "")
	}
	body := map[string]any{
		"package": pkg, "component": component, "reason": reason,
		"launch_on_agent_display": a.has("relaunch"),
	}
	if n, err := a.optInt("agent-display", 0); err == nil && n > 0 {
		body["agent_display"] = n
	}
	raw, err := c.Post("/v1/device/handover", body)
	if err != nil {
		return err
	}
	return printJSON(raw)
}
