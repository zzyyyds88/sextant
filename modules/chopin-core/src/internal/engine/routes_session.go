package engine

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"chopin-core/internal/httpx"
	"chopin-core/internal/session"
	"chopin-core/internal/status"
)

// sessionError maps session errors to the documented HTTP contract.
func sessionError(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, session.ErrConflict):
		httpx.WriteError(w, http.StatusConflict, httpx.CodeConflict, "display is already claimed", err)
	case errors.Is(err, session.ErrExclusive):
		httpx.WriteError(w, http.StatusConflict, httpx.CodeExclusiveNeeded,
			"a global configuration write needs exclusive access", err)
	case errors.Is(err, session.ErrBanned):
		httpx.WriteError(w, http.StatusTooManyRequests, httpx.CodeForbidden,
			"this agent is temporarily banned after a lost session", err)
	case errors.Is(err, session.ErrForbidden):
		httpx.WriteError(w, http.StatusForbidden, httpx.CodeForbidden, "session token mismatch", err)
	case errors.Is(err, session.ErrNotFound):
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "no such session", err)
	default:
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "session operation failed", err)
	}
}

// claimSession is the shared claim path, including the paused-overlay and
// unlock preconditions.
func (s *Server) claimSession(w http.ResponseWriter, r *http.Request, agent string, display int) {
	// finishTask locks the screen when the last session is gone. Holding this
	// across claim+unlock makes "claim" win over a concurrent finish.
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	// A paused overlay means a human asked the screen to hold still. Handing out a
	// new write session would move it under them, so claims are refused until the
	// overlay is resumed.
	if hudState := s.HUD.Snapshot(); hudState.Paused {
		httpx.WriteError(w, http.StatusConflict, httpx.CodeConflict,
			"the on-device overlay is paused; resume it before claiming a session", nil)
		return
	}
	// A claim needs the keyguard gone: the HUD lives in the app and cannot be
	// bound before the user is unlocked, so claiming first would produce a
	// session with no safety overlay.
	if !s.Unlock.IsUnlocked(r.Context()) {
		res, err := s.Unlock.EnsureUnlocked(r.Context())
		if err != nil {
			httpx.WriteError(w, http.StatusConflict, httpx.CodeNotReady,
				"cannot claim a session on a locked device without a working unlock", err)
			return
		}
		s.Audit.Info("session: unlock before claim: %s", res.Detail)
	}
	sess, err := s.Sessions.Claim(r.Context(), agent, display)
	if err != nil {
		sessionError(w, err)
		return
	}
	s.HUD.Attach(sess.Agent, sess.ID, sess.Display)
	s.publishSessions()
	s.Audit.Info("session: claimed %s display=%d by %s", sess.ID, sess.Display, sess.Agent)
	// Both spellings are returned: the canonical id/token and the session_id/
	// session_token aliases older clients read. One claim, one contract.
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"id": sess.ID, "token": sess.Token, "agent": sess.Agent,
		"display": sess.Display, "idle_sec": sess.IdleSec,
		"session_id": sess.ID, "session_token": sess.Token, "display_id": sess.Display,
	})
}

// handleHeartbeat renews a session.
func (s *Server) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Token string `json:"token"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	if err := s.Sessions.Heartbeat(id, body.Token); err != nil {
		sessionError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleRelease drops a session.
func (s *Server) handleRelease(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	// Releasing requires only a valid API token: a session left behind by an
	// agent that lost its session token must still be releasable, otherwise the
	// display stays claimed until the idle reaper fires.
	if err := s.Sessions.Release(id, ""); err != nil {
		sessionError(w, err)
		return
	}
	s.HUD.Detach()
	s.publishSessions()
	s.Audit.Info("session: released %s", id)
	// Releasing the last writer is the end of the task: restore the user's IME,
	// drop keep-awake and lock the screen (D36).
	if len(s.Sessions.List()) == 0 {
		s.finishTask("release")
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// handleListSessions lists the active claims and the roster.
func (s *Server) handleListSessions(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"active": s.Sessions.List(),
		// `sessions` is the same list under the name the WebUI reads; both stay so
		// neither client has to guess.
		"sessions": s.Sessions.List(),
		"roster":   s.Sessions.Roster(),
		"max":      s.Cfg.Session.MaxWriteSessions,
	})
}

// handleReap releases expired sessions.
func (s *Server) handleReap(w http.ResponseWriter, r *http.Request) {
	n := s.Sessions.Reap()
	if n > 0 {
		s.publishSessions()
		s.Audit.Info("session: reaped %d expired session(s)", n)
		if len(s.Sessions.List()) == 0 {
			s.finishTask("reap")
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"reaped": n})
}

// publishSessions mirrors the session counters into the published status.
func (s *Server) publishSessions() {
	list := s.Sessions.List()
	roster := s.Sessions.Roster()
	s.Status.Update(func(st *status.Status) {
		st.Sessions.Active = len(list)
		st.Sessions.Max = s.Cfg.Session.MaxWriteSessions
		st.Sessions.Roster = roster
	})
}

// sessionFromRequest returns the session named by X-Chopin-Session, if any.
func (s *Server) sessionFromRequest(r *http.Request) *session.Session {
	tok := strings.TrimSpace(r.Header.Get("X-Chopin-Session"))
	if tok == "" {
		return nil
	}
	sess, err := s.Sessions.Require(tok)
	if err != nil {
		return nil
	}
	return sess
}

var _ = strconv.Itoa

// finishTask is the single end-of-task cleanup (D36). It runs only when the last
// write session is gone: a human handover leaves the screen to the person, and
// an active session means the task is still running.
//
// The caller's request context is deliberately not used: a release reply must
// not cancel the lock that follows it.
func (s *Server) finishTask(reason string) {
	s.taskMu.Lock()
	defer s.taskMu.Unlock()
	if len(s.Sessions.List()) > 0 {
		return
	}
	if active, _, _, _, _ := s.handover.snapshot(); active {
		s.Audit.Info("session: task finished (%s); handover active, screen left on", reason)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := s.Helper.KeepAwake(ctx, false, 0); err != nil {
		s.Audit.Warn("session: keep-awake release failed: %v", err)
	}
	if err := s.IME.RestoreIfIdle(ctx); err != nil {
		s.Audit.Warn("ime: restore after %s failed: %v", reason, err)
	}
	if !s.Unlock.IsUnlocked(ctx) {
		s.Audit.Info("session: task finished (%s); screen already locked", reason)
		return
	}
	if _, err := s.Probe.Run.Run(ctx, "input", "keyevent", "KEYCODE_SLEEP"); err != nil {
		s.Audit.Warn("session: lock after %s failed: %v", reason, err)
		return
	}
	s.Audit.Info("session: task finished (%s); screen locked", reason)
}

// touchSession records activity for the session named by X-Chopin-Session. It
// runs after bearer/scope validation (httpx.Authenticator.OnAuthorized), so an
// unauthenticated request cannot keep a session alive. A heartbeat keeps the
// session alive but is not business activity (D37).
func (s *Server) touchSession(r *http.Request) {
	tok := strings.TrimSpace(r.Header.Get("X-Chopin-Session"))
	if tok == "" {
		return
	}
	action := !strings.HasSuffix(r.URL.Path, "/heartbeat")
	_ = s.Sessions.Touch(tok, action)
}
