package http

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sl-bus/backend/internal/platform/auth"
	"github.com/sl-bus/backend/internal/platform/httpx"
)

type sessionResponse struct {
	ID         string    `json:"id"`
	DeviceInfo string    `json:"deviceInfo,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
	ExpiresAt  time.Time `json:"expiresAt"`
}

func (h *handlers) listSessions(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, httpx.APIError{Code: "AUTH_TOKEN_INVALID", Message: "Please sign in again."}, nil)
		return
	}
	sessions, err := h.deps.Tokens.ListSessions(r.Context(), claims.Subject)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	out := make([]sessionResponse, 0, len(sessions))
	for _, s := range sessions {
		out = append(out, sessionResponse{ID: s.ID, DeviceInfo: s.DeviceInfo, CreatedAt: s.CreatedAt, ExpiresAt: s.ExpiresAt})
	}
	httpx.WriteSuccess(w, http.StatusOK, out, nil)
}

func (h *handlers) revokeSession(w http.ResponseWriter, r *http.Request) {
	claims, ok := auth.FromContext(r.Context())
	if !ok {
		httpx.WriteError(w, http.StatusUnauthorized, httpx.APIError{Code: "AUTH_TOKEN_INVALID", Message: "Please sign in again."}, nil)
		return
	}
	sessionID := chi.URLParam(r, "id")
	if err := h.deps.Tokens.RevokeSession(r.Context(), claims.Subject, sessionID); err != nil {
		writeDomainError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
