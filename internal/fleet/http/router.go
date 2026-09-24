package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/sl-bus/backend/internal/fleet/service"
	"github.com/sl-bus/backend/internal/platform/auth"
)

type Deps struct {
	Bus      *service.BusService
	SeatMap  *service.SeatMapService
	Route    *service.RouteService
	Trip     *service.TripService
	Search   *service.SearchService
	Verifier auth.Verifier
}

// NewRouter mounts /v1/fleet/* endpoints — operator_admin/platform_admin
// only, exactly like Phase 1's /v1/operators admin routes.
func NewRouter(deps Deps) chi.Router {
	h := &handlers{deps: deps}
	r := chi.NewRouter()

	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(deps.Verifier))
		r.Use(auth.RequireRole(auth.RoleOperatorAdmin, auth.RolePlatformAdmin))

		r.Post("/buses", h.createBus)
		r.Get("/buses", h.listBuses)
		r.Get("/buses/{id}", h.getBus)
		r.Patch("/buses/{id}", h.updateBus)
		r.Get("/buses/{id}/seat-map", h.getSeatMap)
		r.Put("/buses/{id}/seat-map", h.replaceSeatMap)

		r.Post("/routes", h.createRoute)
		r.Get("/routes", h.listRoutes)
		r.Get("/routes/{id}", h.getRoute)
		r.Patch("/routes/{id}", h.updateRoute)

		r.Post("/trips", h.createTrips)
		r.Get("/trips", h.listTrips)
		r.Get("/trips/{id}", h.getTrip)
		r.Post("/trips/{id}/cancel", h.cancelTrip)
		r.Patch("/trips/{id}", h.rescheduleTrip)
		r.Patch("/trips/{id}/fares", h.updateTripFares)
	})

	return r
}

// NewSearchHandler is mounted directly at GET /v1/trips/search in
// cmd/server/main.go — public, no auth, per AUTHENTICATION_DESIGN.md
// §2.1, mirroring how Phase 1 mounted the registration handler outside
// its home router.
func NewSearchHandler(search *service.SearchService) http.HandlerFunc {
	return (&handlers{deps: Deps{Search: search}}).search
}

type handlers struct {
	deps Deps
}
