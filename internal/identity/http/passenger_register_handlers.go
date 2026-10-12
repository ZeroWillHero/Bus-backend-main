package http

import (
	"net/http"

	"github.com/sl-bus/backend/internal/platform/httpx"
)

type registerPassengerRequest struct {
	Email    string `json:"email" validate:"required,email"`
	Password string `json:"password" validate:"required"`
}

type registerPassengerResponse struct {
	UserID string `json:"userId"`
}

// registerPassenger creates the account only; the client then signs in
// through /login, which starts TOTP enrollment.
func (h *handlers) registerPassenger(w http.ResponseWriter, r *http.Request) {
	var req registerPassengerRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	user, err := h.deps.PassengerRegistration.RegisterPassenger(r.Context(), req.Email, req.Password)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	httpx.WriteSuccess(w, http.StatusCreated, registerPassengerResponse{UserID: user.ID}, nil)
}
