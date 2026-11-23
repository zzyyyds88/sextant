package engine

import (
	"net/http"
	"os"
	"time"

	"sextant-core/internal/httpx"
	"sextant-core/internal/status"
)

// routes wires the v1 surface. Every handler that mutates state requires a
// scope; the two public endpoints are health and the OpenAPI document.
func (s *Server) routes() *http.ServeMux {
	mux := http.NewServeMux()
	auth := s.Auth()

	mux.HandleFunc("GET /v1/health", s.handleHealth)
	mux.HandleFunc("GET /doc", s.handleDoc)
	mux.HandleFunc("GET /doc/openapi.json", s.handleDocJSON)
	mux.HandleFunc("GET /openapi.json", s.handleDocJSON)
	mux.HandleFunc("GET /doc/skill", s.handleSkill)
	mux.HandleFunc("GET /doc/skill.zip", s.handleSkillZip)
	// Browsers request /favicon.ico on their own; answering it keeps the console
	// free of a 404 that would mask real errors.
	mux.HandleFunc("GET /favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/x-icon")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(faviconICO)
	})
	mux.Handle("GET /ui/", http.StripPrefix("/ui/", s.webUIServer()))
	mux.HandleFunc("GET /ui", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/ui/", http.StatusFound)
	})

	mux.HandleFunc("GET /v1/status", auth.Require([]string{"read"}, s.handleStatus))
	mux.HandleFunc("POST /v1/pair", s.handlePair)

	mux.HandleFunc("GET /v1/device", auth.Require([]string{"read"}, s.handleDevice))
	mux.HandleFunc("GET /v1/device/state", auth.Require([]string{"read"}, s.handleDeviceState))
	mux.HandleFunc("GET /v1/device/lock-state", auth.Require([]string{"read"}, s.handleLockState))
	mux.HandleFunc("POST /v1/device/wake", auth.Require([]string{"ui"}, s.handleWake))
	mux.HandleFunc("POST /v1/device/unlock", auth.Require([]string{"unlock"}, s.handleUnlock))
	mux.HandleFunc("POST /v1/device/unlock-local", auth.Require([]string{"unlock"}, s.handleUnlockLocal))
	mux.HandleFunc("POST /v1/device/sim-unlock", auth.Require([]string{"unlock"}, s.handleSIMUnlock))
	mux.HandleFunc("POST /v1/device/lock", auth.Require([]string{"ui"}, s.handleLock))
	mux.HandleFunc("POST /v1/device/screen", auth.Require([]string{"settings"}, s.handleScreen))
	mux.HandleFunc("GET /v1/device/rotation", auth.Require([]string{"read"}, s.handleRotation))
	mux.HandleFunc("POST /v1/device/rotation", auth.Require([]string{"settings"}, s.handleRotation))
	mux.HandleFunc("POST /v1/device/keep-awake", auth.Require([]string{"device"}, s.handleKeepAwake))
	mux.HandleFunc("POST /v1/device/airplane", auth.Require([]string{"settings"}, s.handleAirplane))
	mux.HandleFunc("POST /v1/device/wifi", auth.Require([]string{"settings"}, s.handleWifi))
	mux.HandleFunc("POST /v1/input/global", auth.Require([]string{"input"}, s.handleGlobal))

	mux.HandleFunc("POST /v1/input/tap", auth.Require([]string{"input"}, s.handleTap))
	mux.HandleFunc("POST /v1/input/swipe", auth.Require([]string{"input"}, s.handleSwipe))
	mux.HandleFunc("POST /v1/input/longpress", auth.Require([]string{"input"}, s.handleLongPress))
	mux.HandleFunc("POST /v1/input/key", auth.Require([]string{"input"}, s.handleKey))
	mux.HandleFunc("POST /v1/input/text", auth.Require([]string{"input"}, s.handleText))
	mux.HandleFunc("POST /v1/input/ime/text", auth.Require([]string{"input"}, s.handleIMEText))
	mux.HandleFunc("POST /v1/input/ime/key", auth.Require([]string{"input"}, s.handleIMEKey))
	mux.HandleFunc("GET /v1/input/ime", auth.Require([]string{"read"}, s.handleIMEState))

	mux.HandleFunc("GET /v1/ui/screenshot", auth.Require([]string{"ui"}, s.handleScreenshot))
	mux.HandleFunc("GET /v1/ui/tree", auth.Require([]string{"ui"}, s.handleTree))
	mux.HandleFunc("POST /v1/ui/ocr", auth.Require([]string{"ui"}, s.handleOCR))
	mux.HandleFunc("GET /v1/ui/ocr/debug", auth.Require([]string{"read"}, s.handleOCRDebug))
	mux.HandleFunc("POST /v1/ui/find", auth.Require([]string{"ui"}, s.handleFind))
	mux.HandleFunc("POST /v1/ui/wait", auth.Require([]string{"ui"}, s.handleWait))
	mux.HandleFunc("POST /v1/ui/chain", auth.Require([]string{"ui"}, s.handleChain))
	mux.HandleFunc("GET /v1/ui/events", auth.Require([]string{"ui"}, s.handleEvents))
	mux.HandleFunc("POST /v1/ui/color_probe", auth.Require([]string{"ui"}, s.handleColorProbe))
	mux.HandleFunc("POST /v1/ui/pixel_stats", auth.Require([]string{"ui"}, s.handlePixelStats))
	mux.HandleFunc("POST /v1/ui/panel_read", auth.Require([]string{"ui"}, s.handlePanelRead))
	mux.HandleFunc("POST /v1/ui/wait_result", auth.Require([]string{"ui"}, s.handleWaitResult))

	mux.HandleFunc("GET /v1/sessions", auth.Require([]string{"read"}, s.handleListSessions))
	mux.HandleFunc("POST /v1/sessions", auth.Require([]string{"ui"}, s.handleClaimCompat))
	mux.HandleFunc("POST /v1/sessions/{id}/heartbeat", auth.Require([]string{"ui"}, s.handleHeartbeat))
	mux.HandleFunc("DELETE /v1/sessions/{id}", auth.Require([]string{"ui"}, s.handleRelease))
	mux.HandleFunc("GET /v1/roster", auth.Require([]string{"read"}, s.handleListSessions))
	mux.HandleFunc("POST /v1/guard/reap", auth.Require([]string{"ui"}, s.handleReap))

	// Call takeover. `read` for state (it is just a reading), `device` for the
	// destructive half — hanging up on somebody is not a read.

	mux.HandleFunc("GET /v1/notifications", auth.Require([]string{"notifications"}, s.handleNotifications))
	mux.HandleFunc("POST /v1/notifications/dismiss", auth.Require([]string{"notifications"}, s.handleNotificationDismiss))
	mux.HandleFunc("GET /v1/notifications/events", auth.Require([]string{"notifications"}, s.handleNotificationEvents))
	mux.HandleFunc("GET /v1/sms/messages", auth.Require([]string{"sms"}, s.handleSMSListDB))
	mux.HandleFunc("POST /v1/sms/send", auth.Require([]string{"sms"}, s.handleSMSSend))
	mux.HandleFunc("GET /v1/sms/status/{id}", auth.Require([]string{"sms"}, s.handleSMSStatusDB))
	mux.HandleFunc("GET /v1/location", auth.Require([]string{"location"}, s.handleLocation))
	// Ears and mouth. Through the helper (root + system Context), because a plain
	// app cannot hold CAPTURE_AUDIO_OUTPUT and a missing capture permission returns
	// silence rather than an error.
	mux.HandleFunc("GET /v1/audio", auth.Require([]string{"read"}, s.handleAudioState))
	mux.HandleFunc("POST /v1/audio/mic", auth.Require([]string{"device"}, s.handleAudioMicMute))
	mux.HandleFunc("POST /v1/audio/route", auth.Require([]string{"device"}, s.handleAudioRoute))
	mux.HandleFunc("POST /v1/audio/listen", auth.Require([]string{"read"}, s.handleAudioListen))
	mux.HandleFunc("POST /v1/audio/speak", auth.Require([]string{"device"}, s.handleAudioSpeak))

	// Daily settings: the switches a person uses, through the service that owns
	// them (and read back). Separate from /v1/settings/{ns}/{key}, which only writes
	// the database — correct for a timeout, wrong for airplane mode.
	mux.HandleFunc("GET /v1/settings/daily", auth.Require([]string{"settings"}, s.handleDailySettings))
	mux.HandleFunc("PUT /v1/settings/daily/{name}", auth.Require([]string{"settings"}, s.handleSetDailySetting))

	mux.HandleFunc("GET /v1/settings/{ns}/{key}", auth.Require([]string{"settings"}, s.handleGetSetting))
	mux.HandleFunc("PUT /v1/settings/{ns}/{key}", auth.Require([]string{"settings"}, s.handlePutSetting))
	mux.HandleFunc("GET /v1/props/{name}", auth.Require([]string{"read"}, s.handleGetProp))
	mux.HandleFunc("POST /v1/components/disable", auth.Require([]string{"settings"}, func(w http.ResponseWriter, r *http.Request) { s.handleComponents(w, r, true) }))
	mux.HandleFunc("POST /v1/components/enable", auth.Require([]string{"settings"}, func(w http.ResponseWriter, r *http.Request) { s.handleComponents(w, r, false) }))
	mux.HandleFunc("GET /v1/watchdog", auth.Require([]string{"read"}, s.handleWatchdog))
	mux.HandleFunc("GET /v1/metrics", auth.Require([]string{"read"}, s.handleMetrics))

	mux.HandleFunc("GET /v1/apps", auth.Require([]string{"apps"}, s.handleApps))
	mux.HandleFunc("GET /v1/apps/foreground", auth.Require([]string{"read"}, s.handleForeground))
	mux.HandleFunc("POST /v1/apps/launch", auth.Require([]string{"apps"}, s.handleLaunch))
	mux.HandleFunc("POST /v1/apps/deep-link", auth.Require([]string{"apps"}, s.handleDeepLink))
	mux.HandleFunc("POST /v1/apps/stop", auth.Require([]string{"apps"}, s.handleStop))
	mux.HandleFunc("POST /v1/apps/install", auth.Require([]string{"apps"}, s.handleInstall))
	mux.HandleFunc("POST /v1/apps/uninstall", auth.Require([]string{"apps"}, s.handleUninstall))
	mux.HandleFunc("POST /v1/apps/clearData", auth.Require([]string{"apps"}, s.handleClearData))
	mux.HandleFunc("POST /v1/apps/permissions", auth.Require([]string{"apps"}, s.handleAppPermissions))
	mux.HandleFunc("GET /v1/files", auth.Require([]string{"files"}, s.handleFiles))
	mux.HandleFunc("GET /v1/files/download", auth.Require([]string{"files"}, s.handleFileDownload))
	mux.HandleFunc("POST /v1/files/delete", auth.Require([]string{"files"}, s.handleFileDelete))
	mux.HandleFunc("POST /v1/files/move", auth.Require([]string{"files"}, s.handleFileMove))
	mux.HandleFunc("POST /v1/uploads", auth.Require([]string{"files"}, s.handleUpload))
	mux.HandleFunc("POST /v1/device/reboot", auth.Require([]string{"admin"}, s.handleReboot))
	mux.HandleFunc("GET /v1/ui/frame", auth.Require([]string{"ui"}, s.handleFrame))
	mux.HandleFunc("POST /v1/ui/observe", auth.Require([]string{"ui"}, s.handleObserve))
	mux.HandleFunc("POST /v1/ui/act", auth.Require([]string{"ui"}, s.handleAct))
	mux.HandleFunc("POST /v1/ui/list", auth.Require([]string{"ui"}, s.handleList))
	mux.HandleFunc("POST /v1/ui/options", auth.Require([]string{"ui"}, s.handleOptions))
	mux.HandleFunc("POST /v1/ui/readback", auth.Require([]string{"ui"}, s.handleReadback))
	mux.HandleFunc("POST /v1/ui/wait_stable", auth.Require([]string{"ui"}, s.handleWaitStable))
	mux.HandleFunc("GET /v1/ui/page", auth.Require([]string{"read"}, s.handlePage))
	mux.HandleFunc("POST /v1/ui/diff", auth.Require([]string{"ui"}, s.handleDiff))
	mux.HandleFunc("POST /v1/ui/challenge", auth.Require([]string{"ui"}, s.handleChallenge))
	mux.HandleFunc("POST /v1/ui/challenge/solve", auth.Require([]string{"ui"}, s.handleChallengeSolve))
	mux.HandleFunc("GET /v1/challenge/risk", auth.Require([]string{"read"}, s.handleChallengeRisk))

	mux.HandleFunc("POST /v1/device/handover", auth.Require([]string{"device"}, s.handleHandover))
	mux.HandleFunc("POST /v1/device/handover/return", auth.Require([]string{"device"}, s.handleHandoverReturn))
	mux.HandleFunc("GET /v1/device/handover", auth.Require([]string{"read"}, s.handleHandoverStatus))

	mux.HandleFunc("GET /v1/displays", auth.Require([]string{"display"}, s.handleListDisplays))
	mux.HandleFunc("POST /v1/displays", auth.Require([]string{"display"}, s.handleCreateDisplay))
	mux.HandleFunc("DELETE /v1/displays/{id}", auth.Require([]string{"display"}, s.handleDestroyDisplay))

	mux.HandleFunc("POST /v1/stream/webrtc/offer", auth.Require([]string{"webrtc"}, s.handleWebRTCOffer))
	mux.HandleFunc("POST /v1/stream/webrtc/ice", auth.Require([]string{"webrtc"}, s.handleWebRTCICE))
	mux.HandleFunc("GET /v1/stream/stats", auth.Require([]string{"webrtc"}, s.handleStreamStats))
	mux.HandleFunc("DELETE /v1/stream/viewers/{id}", auth.Require([]string{"webrtc"}, s.handleStreamClose))

	// The HUD *state* is device state: anyone allowed to read the device can see
	// what the overlay is showing, and the overlay's own hud token reads the same
	// state it receives over SSE. Only the overlay's own write channel (heartbeat,
	// pause, stop) needs the narrower `hud` scope. Demanding `hud` here made the
	// WebUI's activity line show "没有动作" forever for every ordinary PC token —
	// a read that failed silently and looked like an idle device. Requiring only
	// `read` broke the HUD's own reader (and journey) with a silent 403, hence
	// RequireAny.
	mux.HandleFunc("GET /v1/hud", auth.RequireAny([]string{"read", "hud"}, s.handleHUDState))
	mux.HandleFunc("POST /v1/hud/heartbeat", auth.Require([]string{"hud"}, s.handleHUDHeartbeat))
	mux.HandleFunc("POST /v1/hud/pause", auth.Require([]string{"hud"}, s.handleHUDPause))
	mux.HandleFunc("POST /v1/hud/stop", auth.Require([]string{"hud"}, s.handleHUDStop))
	mux.HandleFunc("POST /v1/hud/ack", auth.Require([]string{"hud"}, s.handleHUDAck))
	mux.HandleFunc("GET /v1/hud/events", auth.Require([]string{"hud"}, s.handleHUDEvents))

	mux.HandleFunc("GET /v1/triggers", auth.Require([]string{"triggers"}, s.handleTriggerList))
	mux.HandleFunc("POST /v1/triggers", auth.Require([]string{"triggers"}, s.handleTriggerCreate))
	mux.HandleFunc("PATCH /v1/triggers/{id}", auth.Require([]string{"triggers"}, s.handleTriggerUpdate))
	mux.HandleFunc("DELETE /v1/triggers/{id}", auth.Require([]string{"triggers"}, s.handleTriggerDelete))
	mux.HandleFunc("GET /v1/triggers/{id}/runs", auth.Require([]string{"triggers"}, s.handleTriggerRuns))
	mux.HandleFunc("GET /v1/triggers/runs", auth.Require([]string{"triggers"}, s.handleTriggerRuns))
	mux.HandleFunc("GET /v1/triggers/events", auth.Require([]string{"triggers"}, s.handleTriggerEvents))
	return mux
}

// handleHealth is public: it reports liveness, version and schema only.
func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok": true, "version": Version, "schema": 1,
		"uptime_sec": int64(time.Since(s.started).Seconds()),
	})
}

// handleStatus serves the same object that state/status.json holds.
func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, s.Status.Current())
}

// handlePair trades a one-time adb-minted code for a long-lived API token.
func (s *Server) handlePair(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Code  string `json:"code"`
		Agent string `json:"agent"`
	}
	if !httpx.DecodeBody(w, r, 4096, &body) {
		return
	}
	agent, err := s.Pairing.Redeem(body.Code)
	if err != nil {
		s.Audit.Warn("pair: %v", err)
		httpx.WriteError(w, http.StatusUnauthorized, httpx.CodeUnauthorized, "pairing failed", err)
		return
	}
	if body.Agent != "" && body.Agent != agent {
		s.Audit.Warn("pair: agent mismatch (%s != %s)", body.Agent, agent)
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"agent does not match the pairing code", nil)
		return
	}
	tok, rec, err := s.Tokens.Create(agent, tokenDefaultScopes(), false)
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "could not issue token", err)
		return
	}
	fingerprint, _ := s.certFingerprint()
	s.Audit.Info("pair: issued token for %s", agent)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"agent": agent, "token": tok, "scopes": rec.Scopes,
		"server_url": s.Cfg.Listen, "server_cert_sha256": fingerprint,
	})
}

func (s *Server) handleDevice(w http.ResponseWriter, r *http.Request) {
	out := map[string]any{
		"packages": map[string]any{},
	}
	for _, key := range []string{"ro.product.model", "ro.product.device", "ro.build.version.release", "ro.build.version.sdk"} {
		if v, err := s.Probe.Run.Run(r.Context(), "getprop", key); err == nil {
			out["packages"].(map[string]any)[key] = trimNewline(v)
		}
	}
	if uid, err := s.Probe.PackageUID(r.Context(), s.Cfg.App.Package); err == nil {
		out["app_uid"] = uid
	}
	out["version"] = Version
	out["schema"] = 1
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (s *Server) handleDeviceState(w http.ResponseWriter, r *http.Request) {
	screen, _ := s.Probe.DisplayState(r.Context())
	unlocked, uerr := s.Probe.UserUnlocked(r.Context())
	keyguard, kerr := s.Probe.KeyguardShowing(r.Context())
	display, _ := s.A11y.Raw(r.Context())
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"screen_state":     screen,
		"user_unlocked":    unlocked,
		"keyguard_showing": keyguard,
		"errors":           errorList(uerr, kerr),
		"a11y_setting":     display,
	})
}

// handleLockState reports the lock state from the signal that actually decides
// whether an unlock is needed.
//
// `dumpsys user` says RUNNING_UNLOCKED as soon as the user has ever unlocked once
// since boot, while the keyguard can still be up (screen off, locked again). The
// keyguard is what has to be dismissed, so it is the primary signal here and the
// user state is reported alongside it rather than replacing it.
func (s *Server) handleLockState(w http.ResponseWriter, r *http.Request) {
	unlocked, uerr := s.Probe.UserUnlocked(r.Context())
	keyguard, kerr := s.Probe.KeyguardShowing(r.Context())
	if uerr != nil && kerr != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable,
			"cannot read lock state", uerr)
		return
	}
	screen, _ := s.Probe.DisplayState(r.Context())
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"screen_locked":    keyguard,
		"keyguard_showing": keyguard,
		"user_unlocked":    unlocked,
		"screen_state":     screen,
		"credential":       credentialPresence(s.Unlock.CredentialPresent()),
		"errors":           errorList(uerr, kerr),
	})
}

func (s *Server) handleWake(w http.ResponseWriter, r *http.Request) {
	if _, err := s.Probe.Run.Run(r.Context(), "input", "keyevent", "KEYCODE_WAKEUP"); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "wake failed", err)
		return
	}
	state, _ := s.Probe.DisplayState(r.Context())
	s.Audit.Info("device: wake requested")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"screen_state": state})
}

func (s *Server) handleLock(w http.ResponseWriter, r *http.Request) {
	if _, err := s.Probe.Run.Run(r.Context(), "input", "keyevent", "KEYCODE_SLEEP"); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "lock failed", err)
		return
	}
	s.Audit.Info("device: lock requested")
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleUnlock(w http.ResponseWriter, r *http.Request) {
	s.doUnlock(w, r, false)
}

// handleUnlockLocal is the device-local trigger: loopback + app token, no
// session required, because the keyguard must be dismissed before a session can
// be claimed (DESIGN D14).
func (s *Server) handleUnlockLocal(w http.ResponseWriter, r *http.Request) {
	if !httpx.IsLoopback(r) {
		httpx.WriteError(w, http.StatusForbidden, httpx.CodeLoopbackOnly, "unlock-local is loopback only", nil)
		return
	}
	s.doUnlock(w, r, true)
}

func (s *Server) doUnlock(w http.ResponseWriter, r *http.Request, local bool) {
	res, err := s.Unlock.EnsureUnlocked(r.Context())
	if err != nil {
		s.Status.Update(func(st *status.Status) { st.Unlock.LastAttempt = err.Error() })
		s.Audit.Warn("unlock: %v", err)
		code := httpx.CodeUnavailable
		msg := "unlock failed"
		switch {
		case isErr(err, "credential_missing"):
			code, msg = httpx.CodeCredentialMissing, "no unlock credential on the device"
		case isErr(err, "unsupported_lock_type"):
			code, msg = httpx.CodeUnsupported, "this lock type cannot be automated"
		}
		httpx.WriteError(w, http.StatusConflict, code, msg, err)
		return
	}
	s.Status.Update(func(st *status.Status) { st.Unlock.LastAttempt = res.Detail })
	s.Audit.Info("unlock: %s (local=%v)", res.Detail, local)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "detail": res.Detail})
}

func credentialPresence(present bool) string {
	if present {
		return "present"
	}
	return "absent"
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}

func errorString(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

func errorList(errs ...error) []string {
	var out []string
	for _, e := range errs {
		if e != nil {
			out = append(out, e.Error())
		}
	}
	return out
}

var _ = os.Getpid
