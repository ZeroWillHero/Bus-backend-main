// Package httpx implements the standard API response envelope defined in
// ENGINEERING_STANDARDS.md §1 — every handler in every bounded context
// responds through these two functions so the shape never drifts per
// endpoint.
package httpx

import (
	"encoding/json"
	"net/http"
)

// Meta carries cross-cutting response metadata (request tracing,
// pagination) — never business data (ENGINEERING_STANDARDS.md §1.1).
type Meta struct {
	RequestID  string `json:"requestId,omitempty"`
	NextCursor string `json:"nextCursor,omitempty"`
	HasMore    *bool  `json:"hasMore,omitempty"`
}

type successEnvelope struct {
	Success bool  `json:"success"`
	Data    any   `json:"data"`
	Meta    *Meta `json:"meta,omitempty"`
}

// FieldError is one entry in a VALIDATION_FAILED error's `fields` array
// (ENGINEERING_STANDARDS.md §3.2).
type FieldError struct {
	Field string `json:"field"`
	Issue string `json:"issue"`
}

// APIError is the `error` object in the failure envelope
// (ENGINEERING_STANDARDS.md §1.2). Code must come from the namespaced
// taxonomy in ENGINEERING_STANDARDS.md §3 (AUTH_*, BOOKING_*, ...).
type APIError struct {
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Fields  []FieldError `json:"fields,omitempty"`
}

type errorEnvelope struct {
	Success bool     `json:"success"`
	Error   APIError `json:"error"`
	Meta    *Meta    `json:"meta,omitempty"`
}

// WriteSuccess writes a 2xx envelope with the given payload in `data`.
func WriteSuccess(w http.ResponseWriter, status int, data any, meta *Meta) {
	writeJSON(w, status, successEnvelope{Success: true, Data: data, Meta: meta})
}

// WriteError writes a failure envelope. status must match the HTTP status
// code conventions in ENGINEERING_STANDARDS.md §2 for the given error code.
func WriteError(w http.ResponseWriter, status int, apiErr APIError, meta *Meta) {
	writeJSON(w, status, errorEnvelope{Success: false, Error: apiErr, Meta: meta})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}
