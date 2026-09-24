// Package http adapts internal/ops/service use cases to chi handlers.
package http

import (
	"github.com/go-chi/chi/v5"

	"github.com/sl-bus/backend/internal/ops/service"
	"github.com/sl-bus/backend/internal/platform/auth"
)

type Deps struct {
	Registration *service.RegistrationService
	Approval     *service.ApprovalService
	Listing      *service.ListingService
	Verifier     auth.Verifier
}

// NewRouter mounts /v1/operators endpoints (admin approval workflow).
// Registration (POST /v1/auth/register) is mounted separately, directly
// under /v1/auth in cmd/server/main.go — see NewRegisterHandler.
func NewRouter(deps Deps) chi.Router {
	h := &handlers{deps: deps}
	r := chi.NewRouter()

	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(deps.Verifier))
		r.Use(auth.RequireRole(auth.RolePlatformAdmin))
		r.Get("/", h.listOperators)
		r.Post("/{id}/approve", h.approve)
		r.Post("/{id}/reject", h.reject)
		r.Post("/{id}/suspend", h.suspend)
		r.Post("/{id}/reactivate", h.reactivate)
	})

	return r
}

type handlers struct {
	deps Deps
}
