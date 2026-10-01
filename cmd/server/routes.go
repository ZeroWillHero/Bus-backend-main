package main

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	bookinghttp "github.com/sl-bus/backend/internal/booking/http"
	fleethttp "github.com/sl-bus/backend/internal/fleet/http"
	identityhttp "github.com/sl-bus/backend/internal/identity/http"
	opshttp "github.com/sl-bus/backend/internal/ops/http"
	"github.com/sl-bus/backend/internal/platform/apidocs"
)

// routeDeps holds everything the route table needs. Keeping the mounting in
// one function lets routes_test.go build the real route table (with zero
// dependencies) and check it against api/openapi.yaml.
type routeDeps struct {
	Health           http.HandlerFunc
	RegisterOperator http.HandlerFunc
	Identity         identityhttp.Deps
	Ops              opshttp.Deps
	Fleet            fleethttp.Deps
	Search           http.HandlerFunc
	Booking          bookinghttp.Deps
	SeatUpdatesWS    http.HandlerFunc
}

func mountRoutes(r chi.Router, d routeDeps) {
	r.Get("/healthz", d.Health)

	// API documentation (not part of the OpenAPI contract itself).
	r.Get("/docs", apidocs.UI)
	r.Get("/openapi.yaml", apidocs.Spec)

	r.Route("/v1/auth", func(r chi.Router) {
		r.Post("/register", d.RegisterOperator)
		r.Mount("/", identityhttp.NewRouter(d.Identity))
	})
	r.Mount("/v1/operators", opshttp.NewRouter(d.Ops))
	r.Mount("/v1/fleet", fleethttp.NewRouter(d.Fleet))
	r.Get("/v1/trips/search", d.Search)
	r.Mount("/v1", bookinghttp.NewRouter(d.Booking))

	// WebSocket endpoint for live seat status updates.
	r.HandleFunc("GET /v1/ws/trips/{tripID}/seats", d.SeatUpdatesWS)
}
