package engine

import (
	"errors"
	"net/http"
)

// asMaxBytes reports whether err is the http.MaxBytesReader sentinel.
func asMaxBytes(err error, target **http.MaxBytesError) bool {
	var maxErr *http.MaxBytesError
	if errors.As(err, &maxErr) {
		if target != nil {
			*target = maxErr
		}
		return true
	}
	return false
}
