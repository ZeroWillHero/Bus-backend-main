package http

import (
	"net/http"

	"github.com/sl-bus/backend/internal/platform/httpx"
)

type enrollMFARequest struct {
	PreAuthToken string `json:"preAuthToken" validate:"required"`
}

type enrollMFAResponse struct {
	Secret          string `json:"secret"`
	ProvisioningURI string `json:"provisioningUri"`
}

func (h *handlers) enrollMFA(w http.ResponseWriter, r *http.Request) {
	var req enrollMFARequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	secret, uri, err := h.deps.MFA.EnrollMFA(r.Context(), req.PreAuthToken)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	httpx.WriteSuccess(w, http.StatusOK, enrollMFAResponse{Secret: secret, ProvisioningURI: uri}, nil)
}

type verifyMFARequest struct {
	PreAuthToken string `json:"preAuthToken" validate:"required"`
	TOTPCode     string `json:"totpCode" validate:"required"`
}

func (h *handlers) verifyMFA(w http.ResponseWriter, r *http.Request) {
	var req verifyMFARequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	tokens, err := h.deps.MFA.VerifyMFA(r.Context(), req.PreAuthToken, req.TOTPCode)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeTokenPair(w, http.StatusOK, tokens)
}
