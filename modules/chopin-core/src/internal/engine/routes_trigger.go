package engine

import (
	"errors"
	"net/http"
	"strconv"
	"time"

	"chopin-core/internal/httpx"
	"chopin-core/internal/trigger"
)

// Trigger endpoints. A trigger records what happened; it does not press anything.
// That boundary is deliberate: an unattended watcher that acts on its own is one
// config mistake away from a loop nobody can stop.

func (s *Server) handleTriggerList(w http.ResponseWriter, r *http.Request) {
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"triggers": s.Triggers.List()})
}

func (s *Server) handleTriggerCreate(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name    string       `json:"name"`
		Type    string       `json:"type"`
		Enabled *bool        `json:"enabled"`
		Spec    trigger.Spec `json:"spec"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	enabled := true
	if body.Enabled != nil {
		enabled = *body.Enabled
	}
	tr, err := s.Triggers.Create(body.Name, body.Type, body.Spec, enabled)
	if err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid trigger", err)
		return
	}
	s.Audit.Info("trigger: created %s (%s)", tr.ID, tr.Type)
	httpx.WriteJSON(w, http.StatusOK, tr)
}

func (s *Server) handleTriggerUpdate(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var body struct {
		Name    *string       `json:"name"`
		Enabled *bool         `json:"enabled"`
		Spec    *trigger.Spec `json:"spec"`
	}
	if !httpx.DecodeBody(w, r, s.Cfg.Limits.MaxBodyBytes, &body) {
		return
	}
	tr, err := s.Triggers.Update(id, func(t *trigger.Trigger) error {
		if body.Name != nil {
			t.Name = *body.Name
		}
		if body.Enabled != nil {
			t.Enabled = *body.Enabled
		}
		if body.Spec != nil {
			t.Spec = *body.Spec
		}
		return nil
	})
	if err != nil {
		s.triggerError(w, err)
		return
	}
	httpx.WriteJSON(w, http.StatusOK, tr)
}

func (s *Server) handleTriggerDelete(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if err := s.Triggers.Delete(id); err != nil {
		s.triggerError(w, err)
		return
	}
	// Audit the removal: a watcher that keeps firing after the user deleted it is
	// the failure mode, and without this line the log cannot show the delete ever
	// happened.
	s.Audit.Info("trigger: deleted %s", id)
	httpx.WriteJSON(w, http.StatusOK, map[string]any{"ok": true})
}

func (s *Server) handleTriggerRuns(w http.ResponseWriter, r *http.Request) {
	limit := 100
	if v := r.URL.Query().Get("limit"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			limit = n
		}
	}
	httpx.WriteJSON(w, http.StatusOK, map[string]any{
		"runs": s.Triggers.Runs(r.PathValue("id"), limit),
	})
}

// handleTriggerEvents streams runs as SSE.
func (s *Server) handleTriggerEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "streaming unsupported", nil)
		return
	}
	ch, cancel := s.Triggers.Subscribe()
	defer cancel()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	// Flush the headers before waiting for anything: net/http buffers them, and a
	// client that waits for headers would time out on a perfectly healthy stream.
	flusher.Flush()
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-r.Context().Done():
			return
		case run, ok := <-ch:
			if !ok {
				return
			}
			frame, err := jsonFrame(run)
			if err != nil {
				continue
			}
			if _, err := w.Write(frame); err != nil {
				return
			}
			flusher.Flush()
		case <-ticker.C:
			if _, err := w.Write([]byte(": keepalive\n\n")); err != nil {
				return
			}
			flusher.Flush()
		}
	}
}

func (s *Server) triggerError(w http.ResponseWriter, err error) {
	if errors.Is(err, trigger.ErrNotFound) {
		httpx.WriteError(w, http.StatusNotFound, httpx.CodeNotFound, "no such trigger", err)
		return
	}
	httpx.WriteError(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid trigger", err)
}
