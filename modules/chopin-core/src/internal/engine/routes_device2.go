package engine

import (
	"errors"
	"net/http"
	"strings"

	"chopin-core/internal/httpx"
	"chopin-core/internal/unlock"
)

// Device control beyond lock/wake: screen, rotation, radios, global actions.

// handleKeepAwake holds or releases a partial wake lock in the helper.
//
// Long tasks lose time to the display timeout; the lock is explicit and has no
// implicit lifetime. It dies with the helper process, so a crashed caller cannot
// leave it held across a daemon restart.
func (s *Server) handleKeepAwake(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Enabled   *bool `json:"enabled"`
		TimeoutMS int   `json:"timeout_ms"`
	}
	if r.ContentLength > 0 {
		if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
			return
		}
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	out, err := s.Helper.KeepAwake(r.Context(), enabled, body.TimeoutMS)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "keep-awake failed", err)
		return
	}
	s.Audit.Info("device: keep-awake enabled=%v held=%v", enabled, out["held"])
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "keep_awake": out})
}

// handleScreen sets brightness and the screen-off timeout.
//
// Both values are written through `settings put system`, and both are read back:
// MIUI silently clamps an out-of-range brightness, and reporting the requested
// value instead of the stored one would hide that.
func (s *Server) handleScreen(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Brightness string `json:"brightness"`
		TimeoutMS  string `json:"timeout_ms"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.Brightness == "" && body.TimeoutMS == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"brightness or timeout_ms is required", nil)
		return
	}
	if err := s.Device.SetScreen(r.Context(), body.Brightness, body.TimeoutMS); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "screen write failed", err)
		return
	}
	out := map[string]any{"ok": true}
	if body.Brightness != "" {
		if v, err := s.Probe.SettingsGet(r.Context(), "system", "screen_brightness"); err == nil {
			out["brightness"] = v
		}
	}
	if body.TimeoutMS != "" {
		if v, err := s.Probe.SettingsGet(r.Context(), "system", "screen_off_timeout"); err == nil {
			out["timeout_ms"] = v
		}
	}
	s.Audit.Info("device: screen brightness=%q timeout=%q", body.Brightness, body.TimeoutMS)
	httpx.WriteJSON(w, http.StatusOK, out)
}

// handleRotation reads or writes the rotation.
func (s *Server) handleRotation(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodGet {
		value, err := s.Device.Rotation(r.Context())
		if err != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "rotation read failed", err)
			return
		}
		pref, _ := s.Probe.SettingsGet(r.Context(), "system", "user_rotation")
		auto, _ := s.Probe.SettingsGet(r.Context(), "system", "accelerometer_rotation")
		httpx.WriteJSON(w, http.StatusOK, map[string]any{
			"rotation":      value,
			"user_rotation": pref,
			"auto_rotate":   auto == "1",
		})
		return
	}
	var body struct {
		Value string `json:"value"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.Value == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "value is required", nil)
		return
	}
	if !validRotation(body.Value) {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"value must be 0, 90, 180, 270 or auto", nil)
		return
	}
	if err := s.Device.SetRotation(r.Context(), body.Value); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "rotation write failed", err)
		return
	}
	// Read back two different things, because they are different facts:
	//
	//   * the stored preference (user_rotation + auto-rotate) is what this endpoint
	//     owns — it must echo what was asked for;
	//   * the panel rotation is decided by the foreground app, which may pin its
	//     orientation. A launcher that does not rotate is not a failure of this
	//     endpoint, and reporting it as one sends the caller chasing the wrong bug.
	stored, err := s.Probe.SettingsGet(r.Context(), "system", "user_rotation")
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "rotation read-back failed", err)
		return
	}
	panel, err := s.Device.Rotation(r.Context())
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "rotation read-back failed", err)
		return
	}
	wantIndex, _ := rotationIndexOf(body.Value)
	follows := body.Value == "auto" || panel == body.Value
	ok := body.Value == "auto" || stored == wantIndex
	s.Audit.Info("device: rotation requested=%s stored=%s panel=%s follows=%v",
		body.Value, stored, panel, follows)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok": ok, "requested": body.Value, "stored": stored,
		"panel": panel, "panel_follows": follows,
		"detail": panelDetail(body.Value, panel, follows),
	})
}

// handleSIMUnlock enters the SIM PIN when the device asks for one.
//
// It is separate from /v1/device/unlock because the two locks are independent:
// an agent may need to clear a SIM prompt on a device whose screen pattern is
// already handled, and reporting one as the other hides which lock was in the way.
func (s *Server) handleSIMUnlock(w http.ResponseWriter, r *http.Request) {
	prompted, err := s.Unlock.EnsureSIMUnlocked(r.Context())
	if err != nil {
		switch {
		case errors.Is(err, unlock.ErrSIMLowAttempts):
			httpx.WriteError(w, http.StatusConflict, "sim_pin_low_attempts",
				"one attempt left; refusing to spend it automatically", err)
		case errors.Is(err, unlock.ErrCredentialMissing):
			httpx.WriteError(w, http.StatusConflict, httpx.CodeNotReady,
				"no SIM PIN is configured", err)
		case errors.Is(err, unlock.ErrSIMFailed):
			httpx.WriteError(w, http.StatusForbidden, "sim_pin_failed", "the SIM rejected the PIN", err)
		default:
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "SIM unlock failed", err)
		}
		return
	}
	if !prompted {
		httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "prompted": false,
			"detail": "no SIM PIN prompt is showing"})
		return
	}
	s.Audit.Info("unlock: SIM PIN entered")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "prompted": true})
}

// validRotation accepts the three right angles plus "auto".
func validRotation(v string) bool {
	switch v {
	case "0", "90", "180", "270", "auto":
		return true
	}
	return false
}

// handleAirplane toggles airplane mode.
func (s *Server) handleAirplane(w http.ResponseWriter, r *http.Request) {
	var body struct {
		On *bool `json:"on"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.On == nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "on is required", nil)
		return
	}
	if err := s.Device.SetAirplane(r.Context(), *body.On); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "airplane toggle failed", err)
		return
	}
	s.Audit.Info("device: airplane on=%v", *body.On)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "on": *body.On})
}

// handleWifi toggles Wi-Fi.
func (s *Server) handleWifi(w http.ResponseWriter, r *http.Request) {
	var body struct {
		On *bool `json:"on"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.On == nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "on is required", nil)
		return
	}
	if err := s.Device.SetWifi(r.Context(), *body.On); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "wifi toggle failed", err)
		return
	}
	s.Audit.Info("device: wifi on=%v", *body.On)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "on": *body.On})
}

// handleGlobal sends a global action (back/home/recents/notifications/...).
func (s *Server) handleGlobal(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Action string `json:"action"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	action := strings.ToLower(strings.TrimSpace(body.Action))
	if _, ok := globalActions[action]; !ok {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"unknown action; use one of: "+globalActionNames(), nil)
		return
	}
	if s.refuseIfPaused(w) {
		return
	}
	if err := s.Device.Global(r.Context(), action); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "global action failed", err)
		return
	}
	s.Audit.Info("device: global %s", action)
	s.noteAction("%s", describeGlobal(action))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "action": action})
}

// globalActions is the closed set of accepted names. A closed set: an unknown
// name is a 400, not a silently ignored key event.
//
// The names follow AccessibilityService.GLOBAL_ACTION_*, but the values are not
// reused: those constants are not key codes, and mapping them onto `input
// keyevent` made "back" send KEYCODE_MENU and "recents" send KEYCODE_HOME (both
// answering 200). Device.Global owns the command per action.
var globalActions = map[string]bool{
	"back": true, "home": true, "recents": true, "notifications": true,
	"quick_settings": true, "power_dialog": true, "lock_screen": true, "screenshot": true,
}

func globalActionNames() string {
	return "back, home, recents, notifications, quick_settings, power_dialog, lock_screen, screenshot"
}

// rotationIndexOf maps an API rotation value to the stored user_rotation index.
func rotationIndexOf(value string) (string, bool) {
	switch value {
	case "0":
		return "0", true
	case "90":
		return "1", true
	case "180":
		return "2", true
	case "270":
		return "3", true
	}
	return "", false
}

// panelDetail explains why the panel may not have followed the preference.
func panelDetail(requested, panel string, follows bool) string {
	if requested == "auto" {
		return "auto-rotate enabled; the panel follows the sensor"
	}
	if follows {
		return "the panel rotated to " + panel
	}
	return "preference stored, but the foreground app pins its orientation so the panel stayed at " + panel
}

// describeGlobal names a global action the way a person would say it.
func describeGlobal(action string) string {
	switch action {
	case "back":
		return "返回"
	case "home":
		return "回桌面"
	case "recents":
		return "最近任务"
	case "notifications":
		return "拉下通知栏"
	case "quick_settings":
		return "打开快捷设置"
	case "power_dialog":
		return "呼出电源菜单"
	case "lock_screen":
		return "锁屏"
	case "screenshot":
		return "截图"
	}
	return action
}
