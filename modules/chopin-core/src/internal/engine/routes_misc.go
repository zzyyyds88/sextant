package engine

import (
	"net/http"
	"strings"
	"time"

	"chopin-core/internal/httpx"
)

// Notification, SMS and location handlers.
//
// All three go through the root helper: a normal app cannot read notifications,
// send SMS silently or query location without user-visible permissions, and the
// point of running as root is to not depend on those. Every response is the
// helper's structured view, including its errors.

func (s *Server) handleNotifications(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(r, 20*time.Second)
	defer cancel()
	out, err := s.Helper.Notifications(ctx)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable,
			"notification listener unavailable", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (s *Server) handleNotificationDismiss(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Key string `json:"key"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.Key == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "key is required", nil)
		return
	}
	ctx, cancel := contextWithTimeout(r, 20*time.Second)
	defer cancel()
	if err := s.Helper.DismissNotification(ctx, body.Key); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "dismiss failed", err)
		return
	}
	s.Audit.Info("notification: dismissed %s", body.Key)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleSMSSend(w http.ResponseWriter, r *http.Request) {
	var body struct {
		To   string `json:"to"`
		Text string `json:"text"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.To == "" || body.Text == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "to and text are required", nil)
		return
	}
	ctx, cancel := contextWithTimeout(r, 60*time.Second)
	defer cancel()
	out, err := s.Helper.SMSSend(ctx, body.To, body.Text)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "SMS send failed", err)
		return
	}
	// Never log the message body: it is user content and may carry a code.
	s.Audit.Info("sms: send requested to %s (%d chars)", maskNumber(body.To), len([]rune(body.Text)))
	httpx.WriteJSON(w, http.StatusOK, out)
}

func (s *Server) handleLocation(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(r, 30*time.Second)
	defer cancel()
	out, err := s.Helper.Location(ctx)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "location unavailable", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

// settingsHandlers -----------------------------------------------------------

func (s *Server) handleGetSetting(w http.ResponseWriter, r *http.Request) {
	ns := r.PathValue("ns")
	key := r.PathValue("key")
	if !validNamespace(ns) {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"namespace must be system, secure or global", nil)
		return
	}
	v, err := s.Probe.SettingsGet(r.Context(), ns, key)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "settings get failed", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"namespace": ns, "key": key, "value": v})
}

// handlePutSetting writes a setting. It always reads back: a write that did not
// take effect is reported as an error, never as success.
func (s *Server) handlePutSetting(w http.ResponseWriter, r *http.Request) {
	ns := r.PathValue("ns")
	key := r.PathValue("key")
	if !validNamespace(ns) {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"namespace must be system, secure or global", nil)
		return
	}
	var body struct {
		Value string `json:"value"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	// Global-configuration writes (including settings) require exclusivity.
	if sess := s.sessionFromRequest(r); sess == nil {
		if s.Sessions.Count() > 0 {
			httpx.WriteError(w, http.StatusConflict, httpx.CodeExclusiveNeeded,
				"a settings write needs a session and no other active session", nil)
			return
		}
	} else if err := s.Sessions.ExclusiveAllowed(sess.ID); err != nil {
		sessionError(w, err)
		return
	}
	before, _ := s.Probe.SettingsGet(r.Context(), ns, key)
	if err := s.Probe.SettingsPut(r.Context(), ns, key, body.Value); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "settings put failed", err)
		return
	}
	after, err := s.Probe.SettingsGet(r.Context(), ns, key)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "settings read-back failed", err)
		return
	}
	if after != body.Value {
		httpx.WriteError(w, http.StatusConflict, httpx.CodeUnavailable,
			"settings write did not take effect", nil)
		return
	}
	s.Audit.Info("settings: %s/%s %q -> %q", ns, key, before, after)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok": true, "namespace": ns, "key": key, "value": after,
	})
}

func (s *Server) handleGetProp(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	out, err := s.Probe.Run.Run(r.Context(), "getprop", name)
	if err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "getprop failed", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"name": name, "value": strings.TrimSpace(out)})
}

func (s *Server) handleComponents(w http.ResponseWriter, r *http.Request, disable bool) {
	var body struct {
		Component string `json:"component"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if !validComponent(body.Component) {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"component must look like pkg/.Class", nil)
		return
	}
	if sess := s.sessionFromRequest(r); sess == nil && s.Sessions.Count() > 0 {
		httpx.WriteError(w, http.StatusConflict, httpx.CodeExclusiveNeeded,
			"a global configuration write needs a session and no other active session", nil)
		return
	}
	verb := "enable"
	if disable {
		verb = "disable"
	}
	if _, err := s.Probe.Run.Run(r.Context(), "pm", verb, body.Component); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable, "pm "+verb+" failed", err)
		return
	}
	s.Audit.Info("component: %s %s", verb, body.Component)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "component": body.Component, "action": verb})
}

// handleWatchdog reports the daemon's own health so a caller can tell "phone is
// alive but something inside is broken" from "phone is gone".
func (s *Server) handleWatchdog(w http.ResponseWriter, r *http.Request) {
	st := s.Status.Current()
	helperOK := false
	var helperHealth map[string]any
	var helperErr string
	if h, err := s.Helper.Health(r.Context()); err == nil {
		helperOK = true
		helperHealth = h
	} else {
		helperErr = err.Error()
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"daemon": map[string]any{
			"pid": st.Daemon.PID, "state": st.Daemon.State,
			"uptime_sec": st.Daemon.UptimeSec, "version": st.Daemon.Version,
		},
		// The helper's own view is included verbatim: which subsystems it managed
		// to initialise (UiAutomation, notification listener, OCR engine) is the
		// difference between "the feature is broken" and "the phone refuses it".
		"helper":   map[string]any{"available": helperOK, "error": helperErr, "health": helperHealth},
		"sessions": map[string]any{"active": s.Sessions.Count(), "max": s.Cfg.Session.MaxWriteSessions},
		"errors":   st.Errors,
	})
}

// handleOCRDebug reports the ROM OCR engine's internals.
//
// It exists because "OCR returned zero lines" has two very different causes — the
// engine refused to load, or its result shape is not what we parse — and they are
// indistinguishable from the OCR response alone.
func (s *Server) handleOCRDebug(w http.ResponseWriter, r *http.Request) {
	var out map[string]any
	if err := s.Helper.Call(r.Context(), "ocr_debug", nil, &out); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeUnavailable,
			"the root helper could not report the OCR engine", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, out)
}

func validNamespace(ns string) bool {
	switch ns {
	case "system", "secure", "global":
		return true
	}
	return false
}

func validComponent(c string) bool {
	if !strings.Contains(c, "/") {
		return false
	}
	for _, r := range c {
		if r == ' ' || r == ';' || r == '|' || r == '&' || r == '$' || r == '`' {
			return false
		}
	}
	return true
}

// maskNumber keeps the last four digits so a log line can be correlated without
// storing a phone number.
func maskNumber(n string) string {
	if len(n) <= 4 {
		return strings.Repeat("*", len(n))
	}
	return strings.Repeat("*", len(n)-4) + n[len(n)-4:]
}

func parseInt(s string) (int, error) {
	n := 0
	if s == "" {
		return 0, errEmpty
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errNotNumber
		}
		n = n*10 + int(r-'0')
	}
	return n, nil
}

type simpleErr string

func (e simpleErr) Error() string { return string(e) }

const (
	errEmpty     simpleErr = "empty value"
	errNotNumber simpleErr = "not a number"
)
