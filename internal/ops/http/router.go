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
	// MountOperatorScoped lets other bounded contexts add read-only routes
	// under /v1/operators/{id}/... (e.g. insights' dashboard) without this
	// package depending on them. chi cannot mount two routers at the same
	// prefix, so they attach to this one.
	MountOperatorScoped func(r chi.Router)
}

// NewRouter mounts /v1/operators endpoints (admin approval workflow).
// Registration (POST /v1/auth/register) is mounted separately, directly
// under /v1/auth in cmd/server/main.go — see NewRegisterHandler.
func NewRouter(deps Deps) chi.Router {
	h := &handlers{deps: deps}
	r := chi.NewRouter()

	// Any authenticated operator-side user may read their own operator record.
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(deps.Verifier))
		r.Get("/me", h.getOwnOperator)
	})

	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(deps.Verifier))
		r.Use(auth.RequireRole(auth.RolePlatformAdmin))
		r.Get("/", h.listOperators)
		r.Post("/{id}/approve", h.approve)
		r.Post("/{id}/reject", h.reject)
		r.Post("/{id}/suspend", h.suspend)
		r.Post("/{id}/reactivate", h.reactivate)
	})

	if deps.MountOperatorScoped != nil {
		deps.MountOperatorScoped(r)
	}

	return r
}

type handlers struct {
	deps Deps
}
