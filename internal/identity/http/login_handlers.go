package http

import (
	"net/http"

	"github.com/sl-bus/backend/internal/platform/httpx"
)

type loginRequest struct {
	Email             string `json:"email" validate:"required,email"`
	Password          string `json:"password" validate:"required"`
	DeviceFingerprint string `json:"deviceFingerprint"`
}

type loginResponse struct {
	MFAChallengeRequired bool   `json:"mfaChallengeRequired,omitempty"`
	MFAEnrollNeeded      bool   `json:"mfaEnrollNeeded,omitempty"`
	PreAuthToken         string `json:"preAuthToken,omitempty"`
}

func (h *handlers) login(w http.ResponseWriter, r *http.Request) {
	var req loginRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	result, err := h.deps.Login.Login(r.Context(), req.Email, req.Password, req.DeviceFingerprint, deviceInfo(r))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	if result.MFAChallenge {
		httpx.WriteSuccess(w, http.StatusOK, loginResponse{
			MFAChallengeRequired: true,
			MFAEnrollNeeded:      result.MFAEnrollNeeded,
			PreAuthToken:         result.PreAuthToken,
		}, nil)
		return
	}
	writeTokenPair(w, http.StatusOK, result.Tokens)
}
