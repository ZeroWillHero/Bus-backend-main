// Package http adapts internal/identity/service use cases to chi handlers
// (ARCHITECTURE.md §3.2). Registration (POST /v1/auth/register) is
// deliberately NOT here — it's a cross-context use case that lives in
// internal/ops/service (ops depends on identity, not the reverse per the
// context map) and is wired directly under /v1/auth in cmd/server/main.go.
package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/sl-bus/backend/internal/identity/service"
	"github.com/sl-bus/backend/internal/platform/auth"
)

type Deps struct {
	PassengerRegistration *service.PassengerRegistrationService
	Login                 *service.LoginService
	MFA                   *service.MFAService
	Tokens                *service.TokenService
	Verifier              auth.Verifier
	// RateLimits are optional per-endpoint middlewares (nil = unlimited).
	RateLimits RateLimits
}

// RateLimits holds the per-IP limiters for the unauthenticated endpoints.
type RateLimits struct {
	Login             func(http.Handler) http.Handler
	PassengerRegister func(http.Handler) http.Handler
	MFAVerify         func(http.Handler) http.Handler
	TokenRefresh      func(http.Handler) http.Handler
}

func orPassThrough(m func(http.Handler) http.Handler) func(http.Handler) http.Handler {
	if m == nil {
		return func(next http.Handler) http.Handler { return next }
	}
	return m
}

// NewRouter mounts every identity endpoint except /register (see package
// doc). Caller mounts the result at /v1/auth.
func NewRouter(deps Deps) chi.Router {
	h := &handlers{deps: deps}
	r := chi.NewRouter()

	rl := deps.RateLimits
	r.With(orPassThrough(rl.PassengerRegister)).Post("/passenger/register", h.registerPassenger)
	r.With(orPassThrough(rl.Login)).Post("/login", h.login)
	r.Post("/mfa/enroll", h.enrollMFA)
	r.With(orPassThrough(rl.MFAVerify)).Post("/mfa/verify", h.verifyMFA)
	r.With(orPassThrough(rl.TokenRefresh)).Post("/token/refresh", h.refreshToken)

	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(deps.Verifier))
		r.Post("/logout", h.logout)
		r.Get("/sessions", h.listSessions)
		r.Delete("/sessions/{id}", h.revokeSession)
	})

	return r
}

type handlers struct {
	deps Deps
}
