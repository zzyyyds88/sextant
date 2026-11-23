package engine

import (
	"archive/zip"
	"bytes"
	_ "embed"
	"net/http"

	"chopin-core/internal/httpx"
)

// skillMarkdown is the agent-facing operating manual served by /doc/skill.
//
// It lives next to the daemon rather than in a separate docs site because the
// version that answers the API is the only one that can describe it: a manual
// shipped separately drifts the moment the daemon changes.
//
//go:embed skill/SKILL.md
var skillMarkdown []byte

// handleSkill serves the manual as Markdown. Public read, alongside /doc.
func (s *Server) handleSkill(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(skillMarkdown)
}

// handleSkillZip serves the same manual as an installable skill package
// (SKILL.md with its frontmatter), for agents that expect a directory.
func (s *Server) handleSkillZip(w http.ResponseWriter, r *http.Request) {
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	f, err := zw.Create("SKILL.md")
	if err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "build skill package", err)
		return
	}
	if _, err := f.Write(skillMarkdown); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "build skill package", err)
		return
	}
	if err := zw.Close(); err != nil {
		httpx.WriteError(w, http.StatusInternalServerError, httpx.CodeInternal, "build skill package", err)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="chopin-skill.zip"`)
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf.Bytes())
}
