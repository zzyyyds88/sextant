package engine

import (
	"net/http"

	"chopin-core/internal/device"
	"chopin-core/internal/httpx"
)

// Daily settings: one place to read and set the switches a person actually uses.
//
// Separated from /v1/settings/{ns}/{key} on purpose. That endpoint writes the
// Settings database and nothing else, which is fine for the rows that ARE the
// setting (timeouts, brightness) and wrong for the rows that are only a mirror of a
// radio's state (airplane mode, Wi-Fi, Bluetooth): measured on this device, writing
// `airplane_mode_on=1` left Wi-Fi running. This endpoint goes through the owning
// service and reads the result back, so "it worked" and "it did nothing" stop being
// the same reply.

// handleDailySettings lists the daily settings and their current values.
func (s *Server) handleDailySettings(w http.ResponseWriter, r *http.Request) {
	items := s.Device.DailySettings(r.Context())
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "count": len(items), "settings": items})
}

// handleSetDailySetting applies one setting and returns what the system reports.
//
// A DANGEROUS setting needs the confirmation handshake first (DESIGN D13). Which
// ones are dangerous is a judgement about this device, recorded in
// device.Risky: airplane mode and Wi-Fi are on the list because this phone is
// managed over Wi-Fi, so turning either off does not "switch a radio", it makes the
// phone unreachable — measured, and nothing on this side can undo it.
//
// The confirmation is what the user asked for: "危险项需要 agent 二次确认，让 AI
// 思考是不是必须更改". The reply names the risk in words, so the second request is
// made with the reason in hand rather than blind.
func (s *Server) handleSetDailySetting(w http.ResponseWriter, r *http.Request) {
	if s.refuseIfPaused(w) {
		return
	}
	var body struct {
		Value          string `json:"value"`
		ConfirmationID string `json:"confirmation_id"`
		Reason         string `json:"reason"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	name := r.PathValue("name")
	if why, risky := device.Risky(name); risky && body.ConfirmationID == "" {
		id := newConfirmation("settings/daily/"+name, map[string]string{"value": body.Value})
		s.Audit.Warn("settings: %s=%s is dangerous (%s); asked for confirmation", name, body.Value, why)
		httpx.WriteJSON(w, http.StatusAccepted, map[string]any{
			"error": map[string]any{
				"code":    httpx.CodeConfirmation,
				"message": name + " is dangerous: " + why,
				"detail":  "decide whether this is really necessary, then repeat with confirmation_id and say why in reason",
			},
			"confirmation_id": id,
			"expires_in_sec":  int(confirmTTL.Seconds()),
			"risk":            why,
		})
		return
	}
	if body.ConfirmationID != "" {
		if err := takeConfirmation(body.ConfirmationID, "settings/daily/"+name,
			map[string]string{"value": body.Value}); err != nil {
			httpx.WriteError(w, http.StatusBadRequest, httpx.CodeConfirmation, "invalid confirmation", err)
			return
		}
		if body.Reason != "" {
			s.Audit.Warn("settings: %s=%s confirmed: %s", name, body.Value, body.Reason)
		}
	}
	before, _ := s.Device.SetDaily(r.Context(), name, body.Value)
	// The audit line records what happened, not what was requested: on a locked
	// phone several of these are refused, and the refusal leaves no trace anywhere
	// else.
	s.Audit.Info("settings: daily %s -> %s (system reports %s)", name, body.Value, before)
	s.noteAction("改设置 %s", name)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"ok": true, "setting": name, "requested": body.Value, "value": before,
	})
}
