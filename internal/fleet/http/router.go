package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/sl-bus/backend/internal/fleet/service"
	"github.com/sl-bus/backend/internal/platform/auth"
)

type Deps struct {
	Bus     *service.BusService
	SeatMap *service.SeatMapService
	Route   *service.RouteService
	Trip    *service.TripService
	Search  *service.SearchService
	// ReadModel adds derived seat counts and embedded names to responses.
	ReadModel *service.ReadModelService
	Verifier  auth.Verifier
}

// NewRouter mounts /v1/fleet/* endpoints. Reads are open to operator_staff
// (boarding staff need trips, routes, buses and seat maps); every write stays
// operator_admin/platform_admin only.
func NewRouter(deps Deps) chi.Router {
	h := &handlers{deps: deps}
	r := chi.NewRouter()

	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(deps.Verifier))

		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole(auth.RoleOperatorAdmin, auth.RoleOperatorStaff, auth.RolePlatformAdmin))

			r.Get("/buses", h.listBuses)
			r.Get("/buses/{id}", h.getBus)
			r.Get("/buses/{id}/seat-map", h.getSeatMap)

			r.Get("/routes", h.listRoutes)
			r.Get("/routes/{id}", h.getRoute)
			r.Get("/routes/{id}/buses", h.listRouteBuses)

			r.Get("/trips", h.listTrips)
			r.Get("/trips/{id}", h.getTrip)
		})

		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole(auth.RoleOperatorAdmin, auth.RolePlatformAdmin))

			r.Post("/buses", h.createBus)
			r.Patch("/buses/{id}", h.updateBus)
			r.Put("/buses/{id}/seat-map", h.replaceSeatMap)

			r.Post("/routes", h.createRoute)
			r.Patch("/routes/{id}", h.updateRoute)

			r.Post("/trips", h.createTrips)
			r.Post("/trips/{id}/cancel", h.cancelTrip)
			r.Patch("/trips/{id}", h.rescheduleTrip)
			r.Patch("/trips/{id}/fares", h.updateTripFares)
		})
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
