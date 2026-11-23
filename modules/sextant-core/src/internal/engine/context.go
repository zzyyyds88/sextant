package engine

import (
	"context"
	"net/http"
	"time"
)

// contextWithTimeout bounds a handler's work without losing the request context
// (so a client disconnect still cancels it).
func contextWithTimeout(r *http.Request, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(r.Context(), d)
}
