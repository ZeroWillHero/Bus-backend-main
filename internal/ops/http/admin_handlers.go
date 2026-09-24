package http

import (
	"context"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/sl-bus/backend/internal/ops/domain"
	"github.com/sl-bus/backend/internal/platform/httpx"
)

type transitionFunc func(ctx context.Context, operatorID string) (*domain.Operator, error)

func (h *handlers) respondTransition(w http.ResponseWriter, r *http.Request, fn transitionFunc) {
	id := chi.URLParam(r, "id")
	operator, err := fn(r.Context(), id)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	httpx.WriteSuccess(w, http.StatusOK, toOperatorResponse(operator), nil)
}

func (h *handlers) approve(w http.ResponseWriter, r *http.Request) {
	h.respondTransition(w, r, h.deps.Approval.Approve)
}

func (h *handlers) reject(w http.ResponseWriter, r *http.Request) {
	h.respondTransition(w, r, h.deps.Approval.Reject)
}

func (h *handlers) suspend(w http.ResponseWriter, r *http.Request) {
	h.respondTransition(w, r, h.deps.Approval.Suspend)
}

func (h *handlers) reactivate(w http.ResponseWriter, r *http.Request) {
	h.respondTransition(w, r, h.deps.Approval.Reactivate)
}
