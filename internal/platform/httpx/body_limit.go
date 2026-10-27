package httpx

import (
	"errors"
	"net/http"
)

// DefaultMaxBodyBytes caps every request body. It must stay above the
// largest legitimate payload: operator registration's inline
// registrationDocUrl image (ops/service.MaxRegistrationDocURLLength,
// ~700 KB) plus the rest of that JSON body.
const DefaultMaxBodyBytes int64 = 1 << 20 // 1 MiB

// MaxBodyBytes wraps every request body in http.MaxBytesReader so a client
// can't make a handler buffer an unbounded body. Reading past the limit
// fails with *http.MaxBytesError; decoders report it via WriteDecodeError.
func MaxBodyBytes(limit int64) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.Body != nil && r.Body != http.NoBody {
				r.Body = http.MaxBytesReader(w, r.Body, limit)
			}
			next.ServeHTTP(w, r)
		})
	}
}

// WriteDecodeError writes the response for a failed request-body decode:
// 413 PLATFORM_PAYLOAD_TOO_LARGE when MaxBodyBytes cut the body off,
// otherwise 400 VALIDATION_FAILED (malformed JSON).
func WriteDecodeError(w http.ResponseWriter, err error) {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		WriteError(w, http.StatusRequestEntityTooLarge, APIError{
			Code:    "PLATFORM_PAYLOAD_TOO_LARGE",
			Message: "Request body is too large.",
		}, nil)
		return
	}
	WriteError(w, http.StatusBadRequest, APIError{
		Code:    "VALIDATION_FAILED",
		Message: "Request body is not valid JSON.",
	}, nil)
}
