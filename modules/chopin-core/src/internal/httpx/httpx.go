// Package httpx holds the HTTP plumbing shared by every handler: the error
// envelope, auth middleware, and JSON helpers.
//
// DESIGN §8: one error shape everywhere. A handler never writes a bare string
// and never reports success for work it did not do.
package httpx

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"strings"
)

// Error is the single error envelope.
type Error struct {
	Code    string `json:"code"`
	Message string `json:"message"`
	Detail  string `json:"detail,omitempty"`
}

type errorBody struct {
	Error Error `json:"error"`
}

// WriteJSON writes a value with the schema header.
func WriteJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Chopin-Schema", "1")
	w.WriteHeader(code)
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}

// WriteError writes the standard envelope.
func WriteError(w http.ResponseWriter, statusCode int, code, msg string, err error) {
	detail := ""
	if err != nil {
		detail = err.Error()
	}
	WriteJSON(w, statusCode, errorBody{Error: Error{Code: code, Message: msg, Detail: detail}})
}

// Canonical error codes. Handlers must use these so clients can branch on them.
const (
	CodeBadRequest        = "bad_request"
	CodeUnauthorized      = "unauthorized"
	CodeForbidden         = "forbidden_scope"
	CodeNotFound          = "not_found"
	CodeConflict          = "conflict"
	CodeExclusiveNeeded   = "exclusive_required"
	CodeConfirmation      = "confirmation_required"
	CodeUnavailable       = "unavailable"
	CodeNotReady          = "not_ready"
	CodeUnsupported       = "unsupported"
	CodeInternal          = "internal"
	CodeTimeout           = "timeout"
	CodeCredentialMissing = "credential_missing"
	CodeLoopbackOnly      = "loopback_only"
	CodeTooLarge          = "payload_too_large"
)

// DecodeBody reads and decodes a JSON request body with a hard size limit.
func DecodeBody(w http.ResponseWriter, r *http.Request, maxBytes int64, v any) bool {
	if maxBytes <= 0 {
		maxBytes = 1 << 20
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
	dec := json.NewDecoder(r.Body)
	if err := dec.Decode(v); err != nil {
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			WriteError(w, http.StatusRequestEntityTooLarge, CodeTooLarge,
				fmt.Sprintf("request body exceeds %d bytes", maxBytes), nil)
			return false
		}
		WriteError(w, http.StatusBadRequest, CodeBadRequest, "invalid JSON body", err)
		return false
	}
	return true
}

// IsLoopback reports whether the request came from the device itself.
func IsLoopback(r *http.Request) bool {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return false
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// Bearer extracts the token from the Authorization header.
func Bearer(r *http.Request) string {
	h := r.Header.Get("Authorization")
	const prefix = "Bearer "
	if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
		return strings.TrimSpace(h[len(prefix):])
	}
	return ""
}
