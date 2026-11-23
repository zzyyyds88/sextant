package engine

import (
	"embed"
	"io/fs"
	"net/http"
)

// webuiFS holds the single-page UI. It is embedded because it must be served by
// the daemon itself: no separate web server, no Node toolchain, no CDN (DESIGN
// D16).
//
//go:embed webui/*
var webuiFS embed.FS

// webUIServer serves /ui from the embedded tree with the security headers DESIGN
// D16 requires. It consumes /v1 like any other client; it does not own state.
func (s *Server) webUIServer() http.Handler {
	sub, err := fs.Sub(webuiFS, "webui")
	if err != nil {
		return http.NotFoundHandler()
	}
	fileServer := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// script-src/style-src are named explicitly: leaving them to the
		// default-src fallback makes Chrome log a warning that then hides real
		// console errors during verification.
		w.Header().Set("Content-Security-Policy",
			"default-src 'self'; script-src 'self'; style-src 'self'; "+
				"img-src 'self' blob: data:; media-src 'self' blob:; connect-src 'self'; "+
				"object-src 'none'; base-uri 'none'; form-action 'none'")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		fileServer.ServeHTTP(w, r)
	})
}
