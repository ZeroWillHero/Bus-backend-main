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
		r.Get("/", NewListBookingsHandler(deps.CheckoutService))
		r.Post("/", NewCreateBookingHandler(deps.CheckoutService))
		r.Get("/{id}", NewGetBookingHandler(deps.CheckoutService))
		r.Get("/{id}/ticket", NewGetTicketHandler(deps.CheckoutService))
		r.Post("/{id}/confirm", NewConfirmBookingHandler(deps.CheckoutService))
		r.Post("/{id}/cancel", NewCancelBookingHandler(deps.CheckoutService))
	})

	// Staff/operator endpoints — operator_admin may board passengers too.
	r.Route("/tickets", func(r chi.Router) {
		r.Use(auth.Middleware(deps.Verifier), auth.RequireRole(auth.RoleOperatorStaff, auth.RoleOperatorAdmin))
		r.Post("/{qrToken}/check-in", NewCheckInHandler(deps.CheckoutService))
	})

	return r
}
