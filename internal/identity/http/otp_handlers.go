//go:build legacy_otp

// Legacy phone-only OTP sign-in, excluded from the build: it issued full
// tokens with no password or second factor. Superseded by SMS as a second
// factor (docs/SMS_VERIFICATION_DESIGN.md). Safe to delete.

package http

import (
	"net/http"

	"github.com/sl-bus/backend/internal/platform/httpx"
)

type requestOTPRequest struct {
	Phone string `json:"phone" validate:"required"`
}

func (h *handlers) requestOTP(w http.ResponseWriter, r *http.Request) {
	var req requestOTPRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if err := h.deps.OTP.RequestOTP(r.Context(), req.Phone, clientIP(r)); err != nil {
		writeDomainError(w, err)
		return
	}
	httpx.WriteSuccess(w, http.StatusAccepted, map[string]any{}, nil)
}

type verifyOTPRequest struct {
	Phone string `json:"phone" validate:"required"`
	Code  string `json:"code" validate:"required"`
}

func (h *handlers) verifyOTP(w http.ResponseWriter, r *http.Request) {
	var req verifyOTPRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	tokens, err := h.deps.OTP.VerifyOTP(r.Context(), req.Phone, req.Code, deviceInfo(r))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeTokenPair(w, http.StatusOK, tokens)
}

func clientIP(r *http.Request) string {
	if fwd := r.Header.Get("X-Forwarded-For"); fwd != "" {
		return fwd
	}
	return r.RemoteAddr
}
