package engine

import (
	"context"
	"encoding/base64"
	"net/http"
	"strconv"
	"time"

	"chopin-core/internal/httpx"
)

// The device's ears and mouth.
//
// Everything here goes through the helper, which runs as root with a system
// Context and therefore reaches the same AudioManager the Settings app uses. The
// ordinary route — grant the app CAPTURE_AUDIO_OUTPUT and record — does not work:
// that permission is signature|privileged, `pm grant` cannot grant it, and a
// missing audio-capture permission does not raise, it returns SILENCE.
//
// That failure mode shapes every reply below. "Started" is never an answer; each
// capture reports an RMS and a `silent` flag, and each route change reports what
// the platform said afterwards rather than what was requested.

// handleAudioState reports the microphone, the route, and every device present.
func (s *Server) handleAudioState(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var out map[string]any
	if err := s.Helper.Call(ctx, "audio_state", nil, &out); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeNotReady,
			"the audio bridge is not reachable", err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "audio": out})
}

// handleAudioMicMute sets the microphone mute and reports whether it moved.
func (s *Server) handleAudioMicMute(w http.ResponseWriter, r *http.Request) {
	if s.refuseIfPaused(w) {
		return
	}
	var body struct {
		Muted *bool `json:"muted"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.Muted == nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"muted is required (true or false)", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var out map[string]any
	if err := s.Helper.Call(ctx, "audio_mic_mute", map[string]any{"muted": *body.Muted}, &out); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeNotReady, "mic mute failed", err)
		return
	}
	s.Audit.Info("audio: mic mute -> %v (%v)", *body.Muted, out["after"])
	s.noteAction("麦克风%s", map[bool]string{true: "静音", false: "开"}[*body.Muted])
	// The platform accepts setMicrophoneMute outside a call and ignores it. Saying
	// "ok" there would leave the agent believing it muted a live call it did not.
	if applied, _ := out["applied"].(bool); !applied {
		httpx.WriteJSON(w, http.StatusConflict, map[string]any{
			"error": map[string]any{
				"code":    httpx.CodeConflict,
				"message": "the platform accepted the call but the mute state did not change",
				"detail":  out["reason"],
			},
			"audio": out,
		})
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "audio": out})
}

// handleAudioRoute switches between speaker and earpiece.
func (s *Server) handleAudioRoute(w http.ResponseWriter, r *http.Request) {
	if s.refuseIfPaused(w) {
		return
	}
	var body struct {
		Speaker *bool  `json:"speaker"`
		Mode    string `json:"mode"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	var out map[string]any
	if body.Mode != "" {
		if err := s.Helper.Call(ctx, "audio_mode", map[string]any{"mode": body.Mode}, &out); err != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeNotReady, "audio mode failed", err)
			return
		}
		s.Audit.Info("audio: mode -> %s (%v)", body.Mode, out["after"])
	}
	if body.Speaker != nil {
		if err := s.Helper.Call(ctx, "audio_speakerphone", map[string]any{"on": *body.Speaker}, &out); err != nil {
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeNotReady, "route change failed", err)
			return
		}
		s.Audit.Info("audio: speakerphone -> %v (%v)", *body.Speaker, out["after"])
		s.noteAction("切到%s", map[bool]string{true: "扬声器", false: "听筒"}[*body.Speaker])
	}
	if body.Speaker == nil && body.Mode == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"give speaker, mode, or both", nil)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "audio": out})
}

// handleAudioListen records a short burst and returns its level.
//
// This is the "can the device hear the room" probe. It returns a number, not a
// file: the caller is deciding whether to proceed, and a recording it cannot play
// would not help it decide.
func (s *Server) handleAudioListen(w http.ResponseWriter, r *http.Request) {
	var body struct {
		MS     int `json:"ms"`
		Source int `json:"source"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.MS <= 0 {
		body.MS = 1000
	}
	if body.MS > 15000 {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"ms is capped at 15000; ask for a shorter window or poll twice", nil)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(body.MS+8000)*time.Millisecond)
	defer cancel()
	var out map[string]any
	if err := s.Helper.Call(ctx, "audio_listen", map[string]any{"ms": body.MS, "source": body.Source}, &out); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeNotReady, "capture failed", err)
		return
	}
	s.noteAction("听 %dms", body.MS)
	// Silence is a result, not an error: 200 with silent:true and a reason. A 5xx
	// would make a working-but-quiet microphone look like a broken bridge.
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "capture": out})
}

// handleAudioSpeak plays raw PCM supplied by the caller.
//
// The device has no TTS engine installed (`tts_default_synth` is null), so speech
// is synthesised on the PC and sent down as 16 kHz mono s16le. That is also why
// this endpoint takes bytes rather than text: "say this" belongs to whoever can
// speak the language, and right now that is not the phone.
func (s *Server) handleAudioSpeak(w http.ResponseWriter, r *http.Request) {
	if s.refuseIfPaused(w) {
		return
	}
	var body struct {
		PCMBase64 string `json:"pcm_base64"`
		Speaker   *bool  `json:"speaker"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if body.PCMBase64 == "" {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"pcm_base64 is required (16 kHz mono s16le)", nil)
		return
	}
	if _, err := base64.StdEncoding.DecodeString(body.PCMBase64); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest,
			"pcm_base64 is not valid base64", err)
		return
	}
	speaker := true
	if body.Speaker != nil {
		speaker = *body.Speaker
	}
	// The helper blocks for the length of the clip, so the deadline follows it:
	// 16 kHz mono is 32000 bytes per second, and a caller that sends 30 s of audio
	// must not be cut off at the default timeout.
	secs := len(body.PCMBase64) * 3 / 4 / 32000
	ctx, cancel := context.WithTimeout(r.Context(), time.Duration(secs+20)*time.Second)
	defer cancel()
	var out map[string]any
	if err := s.Helper.Call(ctx, "audio_speak", map[string]any{
		"pcm_base64": body.PCMBase64, "speaker": speaker,
	}, &out); err != nil {
		httpx.WriteError(w, http.StatusServiceUnavailable, httpx.CodeNotReady, "playback failed", err)
		return
	}
	s.Audit.Info("audio: played %d bytes via speaker=%v", intOf(out["bytes"]), speaker)
	s.noteAction("说话 %s秒", strconv.Itoa(secs))
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true, "playback": out})
}
