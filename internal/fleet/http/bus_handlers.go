package http

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sl-bus/backend/internal/fleet/domain"
	"github.com/sl-bus/backend/internal/fleet/service"
	"github.com/sl-bus/backend/internal/platform/auth"
	"github.com/sl-bus/backend/internal/platform/httpx"
)

type busResponse struct {
	ID             string         `json:"id"`
	RegistrationNo string         `json:"registrationNo"`
	BusClass       string         `json:"busClass"`
	Amenities      map[string]any `json:"amenities,omitempty"`
	Active         bool           `json:"active"`
	CreatedAt      time.Time      `json:"createdAt"`
	// TotalSeats is 0 and HasSeatMap false until a seat map is saved.
	TotalSeats int  `json:"totalSeats"`
	HasSeatMap bool `json:"hasSeatMap"`
}

func toBusResponse(b *domain.Bus) busResponse {
	return busResponse{
		ID:             b.ID,
		RegistrationNo: b.RegistrationNo,
		BusClass:       b.BusClass,
		Amenities:      b.Amenities,
		Active:         b.Active,
		CreatedAt:      b.CreatedAt,
	}
}

func (h *handlers) busResponses(r *http.Request, buses []*domain.Bus) ([]busResponse, error) {
	claims, _ := auth.FromContext(r.Context())
	extras, err := h.deps.ReadModel.Buses(r.Context(), claims, service.BusIDs(buses))
	if err != nil {
		return nil, err
	}
	out := make([]busResponse, 0, len(buses))
	for _, b := range buses {
		resp := toBusResponse(b)
		if e, ok := extras[b.ID]; ok {
			resp.TotalSeats, resp.HasSeatMap = e.TotalSeats, e.HasSeatMap
		}
		out = append(out, resp)
	}
	return out, nil
}

func (h *handlers) writeBus(w http.ResponseWriter, r *http.Request, status int, bus *domain.Bus) {
	out, err := h.busResponses(r, []*domain.Bus{bus})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	httpx.WriteSuccess(w, status, out[0], nil)
}

type createBusRequest struct {
	RegistrationNo string         `json:"registrationNo" validate:"required"`
	BusClass       string         `json:"busClass" validate:"required"`
	Amenities      map[string]any `json:"amenities"`
}

func (h *handlers) createBus(w http.ResponseWriter, r *http.Request) {
	var req createBusRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	claims, _ := auth.FromContext(r.Context())
	bus, err := h.deps.Bus.Create(r.Context(), claims, service.CreateBusInput{
		RegistrationNo: req.RegistrationNo,
		BusClass:       req.BusClass,
		Amenities:      req.Amenities,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	h.writeBus(w, r, http.StatusCreated, bus)
}

func (h *handlers) getBus(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.FromContext(r.Context())
	bus, err := h.deps.Bus.Get(r.Context(), claims, chi.URLParam(r, "id"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	h.writeBus(w, r, http.StatusOK, bus)
}

func (h *handlers) listBuses(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.FromContext(r.Context())
	cursor := r.URL.Query().Get("cursor")
	buses, nextCursor, err := h.deps.Bus.List(r.Context(), claims, cursor, service.DefaultListLimit)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	out, err := h.busResponses(r, buses)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	hasMore := nextCursor != ""
	httpx.WriteSuccess(w, http.StatusOK, out, &httpx.Meta{NextCursor: nextCursor, HasMore: &hasMore})
}

type updateBusRequest struct {
	RegistrationNo string         `json:"registrationNo" validate:"required"`
	BusClass       string         `json:"busClass" validate:"required"`
	Amenities      map[string]any `json:"amenities"`
	Active         bool           `json:"active"`
}

func (h *handlers) updateBus(w http.ResponseWriter, r *http.Request) {
	var req updateBusRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	claims, _ := auth.FromContext(r.Context())
	bus, err := h.deps.Bus.Update(r.Context(), claims, chi.URLParam(r, "id"), service.UpdateBusInput{
		RegistrationNo: req.RegistrationNo,
		BusClass:       req.BusClass,
		Amenities:      req.Amenities,
		Active:         req.Active,
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	h.writeBus(w, r, http.StatusOK, bus)
}
