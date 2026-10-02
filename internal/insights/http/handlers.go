// Package http adapts internal/insights to chi handlers. Routes are nested
// under /v1/operators/{id} (see Mount); {id} may be the literal "me".
package http

import (
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	"github.com/go-chi/chi/v5"

	"github.com/sl-bus/backend/internal/insights"
	"github.com/sl-bus/backend/internal/platform/auth"
	"github.com/sl-bus/backend/internal/platform/httpx"
)

type Deps struct {
	Insights *insights.Service
	Verifier auth.Verifier
}

// Mount registers the insights routes on the /v1/operators router.
//
// Revenue figures are for operator_admin, finance_admin and platform_admin
// only; operator_staff may see the operational dashboard and activity feed
// (activity hides amounts for them).
func Mount(r chi.Router, deps Deps) {
	h := &handlers{svc: deps.Insights}

	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(deps.Verifier))

		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole(auth.RoleOperatorAdmin, auth.RoleOperatorStaff, auth.RolePlatformAdmin))
			r.Get("/{id}/dashboard", h.dashboard)
			r.Get("/{id}/activity", h.activity)
		})

		r.Group(func(r chi.Router) {
			r.Use(auth.RequireRole(auth.RoleOperatorAdmin, auth.RoleFinanceAdmin, auth.RolePlatformAdmin))
			r.Get("/{id}/analytics/series", h.series)
			r.Get("/{id}/analytics/summary", h.summary)
		})
	})
}

type handlers struct {
	svc *insights.Service
}

func (h *handlers) dashboard(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.FromContext(r.Context())
	out, err := h.svc.Dashboard(r.Context(), claims, chi.URLParam(r, "id"), r.URL.Query().Get("tz"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.WriteSuccess(w, http.StatusOK, out, nil)
}

func (h *handlers) series(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	claims, _ := auth.FromContext(r.Context())
	out, err := h.svc.Series(r.Context(), claims, chi.URLParam(r, "id"), q.Get("from"), q.Get("to"), q.Get("granularity"), q.Get("tz"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.WriteSuccess(w, http.StatusOK, out, nil)
}

func (h *handlers) summary(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	claims, _ := auth.FromContext(r.Context())
	out, err := h.svc.Summary(r.Context(), claims, chi.URLParam(r, "id"), insights.SummaryParams{
		From: q.Get("from"), To: q.Get("to"),
		CompareFrom: q.Get("compareFrom"), CompareTo: q.Get("compareTo"),
		Period: q.Get("period"), TZ: q.Get("tz"),
	})
	if err != nil {
		writeError(w, r, err)
		return
	}
	httpx.WriteSuccess(w, http.StatusOK, out, nil)
}

func (h *handlers) activity(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	limit := 0
	if v := q.Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil {
			httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{Code: "VALIDATION_FAILED", Message: "limit must be a number."}, nil)
			return
		}
		limit = n
	}
	claims, _ := auth.FromContext(r.Context())
	items, next, err := h.svc.Activity(r.Context(), claims, chi.URLParam(r, "id"), limit, q.Get("cursor"))
	if err != nil {
		writeError(w, r, err)
		return
	}
	hasMore := next != ""
	httpx.WriteSuccess(w, http.StatusOK, items, &httpx.Meta{NextCursor: next, HasMore: &hasMore})
}

func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var inputErr *insights.InputError
	switch {
	case errors.Is(err, insights.ErrForbidden):
		httpx.WriteError(w, http.StatusForbidden, httpx.APIError{Code: "AUTH_FORBIDDEN", Message: "You don't have permission to perform this action."}, nil)
	case errors.Is(err, insights.ErrOperatorMissing):
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{Code: "VALIDATION_FAILED", Message: "Platform admins must pass an operator id in the path (\"me\" is not available)."}, nil)
	case errors.Is(err, insights.ErrRangeTooLarge):
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{Code: "ANALYTICS_RANGE_TOO_LARGE", Message: "The date range may span at most " + strconv.Itoa(insights.MaxRangeDays) + " days."}, nil)
	case errors.As(err, &inputErr):
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{Code: "VALIDATION_FAILED", Message: inputErr.Msg}, nil)
	default:
		slog.ErrorContext(r.Context(), "insights request failed", "error", err, "path", r.URL.Path)
		httpx.WriteError(w, http.StatusInternalServerError, httpx.APIError{Code: "PLATFORM_INTERNAL_ERROR", Message: "Something went wrong. Please try again."}, nil)
	}
}
