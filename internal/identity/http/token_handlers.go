package http

import (
	"net/http"
)

type refreshRequest struct {
	RefreshToken string `json:"refreshToken" validate:"required"`
}

func (h *handlers) refreshToken(w http.ResponseWriter, r *http.Request) {
	var req refreshRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	tokens, err := h.deps.Tokens.RefreshTokens(r.Context(), req.RefreshToken, deviceInfo(r))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeTokenPair(w, http.StatusOK, tokens)
}

type logoutRequest struct {
	RefreshToken string `json:"refreshToken" validate:"required"`
}

func (h *handlers) logout(w http.ResponseWriter, r *http.Request) {
	var req logoutRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	if err := h.deps.Tokens.Logout(r.Context(), req.RefreshToken); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
