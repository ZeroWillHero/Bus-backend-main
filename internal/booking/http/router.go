package http

import (
	"github.com/go-chi/chi/v5"

	"github.com/sl-bus/backend/internal/booking/service"
	"github.com/sl-bus/backend/internal/platform/auth"
)

type Deps struct {
	CheckoutService *service.CheckoutService
	// Verifier validates the bearer token. Required: without auth.Middleware the
	// RequireRole gates below see no claims and reject every request.
	Verifier auth.Verifier
}

func NewRouter(deps Deps) chi.Router {
	r := chi.NewRouter()

	// Passenger-only endpoints
	r.Route("/bookings", func(r chi.Router) {
		r.Use(auth.Middleware(deps.Verifier), auth.RequireRole(auth.RolePassenger))
		r.Post("/", NewCreateBookingHandler(deps.CheckoutService))
		r.Get("/{id}", NewGetBookingHandler(deps.CheckoutService))
		r.Post("/{id}/confirm", NewConfirmBookingHandler(deps.CheckoutService))
		r.Post("/{id}/cancel", NewCancelBookingHandler(deps.CheckoutService))
	})

	// Staff/operator endpoints
	r.Route("/tickets", func(r chi.Router) {
		r.Use(auth.Middleware(deps.Verifier), auth.RequireRole(auth.RoleOperatorStaff))
		r.Post("/{qrToken}/check-in", NewCheckInHandler(deps.CheckoutService))
	})

	return r
}
