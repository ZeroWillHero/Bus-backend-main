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

type routeStopRequest struct {
	StopName           string `json:"stopName" validate:"required"`
	Sequence           int    `json:"sequence"`
	ArrivalOffsetMin   *int   `json:"arrivalOffsetMin"`
	DepartureOffsetMin *int   `json:"departureOffsetMin"`
}

type routeStopResponse struct {
	StopName           string `json:"stopName"`
	Sequence           int    `json:"sequence"`
	ArrivalOffsetMin   *int   `json:"arrivalOffsetMin,omitempty"`
	DepartureOffsetMin *int   `json:"departureOffsetMin,omitempty"`
}

type routeResponse struct {
	ID          string              `json:"id"`
	Origin      string              `json:"origin"`
	Destination string              `json:"destination"`
	DistanceKm  *float64            `json:"distanceKm,omitempty"`
	Stops       []routeStopResponse `json:"stops,omitempty"`
	CreatedAt   time.Time           `json:"createdAt"`
}

func toRouteResponse(r *domain.Route) routeResponse {
	out := routeResponse{
		ID:          r.ID,
		Origin:      r.Origin,
		Destination: r.Destination,
		DistanceKm:  r.DistanceKm,
		CreatedAt:   r.CreatedAt,
	}
	for _, s := range r.Stops {
		out.Stops = append(out.Stops, routeStopResponse{
			StopName:           s.StopName,
			Sequence:           s.Sequence,
			ArrivalOffsetMin:   s.ArrivalOffsetMin,
			DepartureOffsetMin: s.DepartureOffsetMin,
		})
	}
	return out
}

func toDomainStops(reqs []routeStopRequest) []domain.RouteStop {
	out := make([]domain.RouteStop, 0, len(reqs))
	for _, s := range reqs {
		out = append(out, domain.RouteStop{
			StopName:           s.StopName,
			Sequence:           s.Sequence,
			ArrivalOffsetMin:   s.ArrivalOffsetMin,
			DepartureOffsetMin: s.DepartureOffsetMin,
		})
	}
	return out
}

type createRouteRequest struct {
	Origin      string             `json:"origin" validate:"required"`
	Destination string             `json:"destination" validate:"required"`
	DistanceKm  *float64           `json:"distanceKm"`
	Stops       []routeStopRequest `json:"stops"`
}

func (h *handlers) createRoute(w http.ResponseWriter, r *http.Request) {
	var req createRouteRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	claims, _ := auth.FromContext(r.Context())
	route, err := h.deps.Route.Create(r.Context(), claims, service.CreateRouteInput{
		Origin:      req.Origin,
		Destination: req.Destination,
		DistanceKm:  req.DistanceKm,
		Stops:       toDomainStops(req.Stops),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	httpx.WriteSuccess(w, http.StatusCreated, toRouteResponse(route), nil)
}

func (h *handlers) getRoute(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.FromContext(r.Context())
	route, err := h.deps.Route.Get(r.Context(), claims, chi.URLParam(r, "id"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	httpx.WriteSuccess(w, http.StatusOK, toRouteResponse(route), nil)
}

func (h *handlers) listRoutes(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.FromContext(r.Context())
	cursor := r.URL.Query().Get("cursor")
	routes, nextCursor, err := h.deps.Route.List(r.Context(), claims, cursor, service.DefaultListLimit)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	out := make([]routeResponse, 0, len(routes))
	for _, rt := range routes {
		out = append(out, toRouteResponse(rt))
	}
	hasMore := nextCursor != ""
	httpx.WriteSuccess(w, http.StatusOK, out, &httpx.Meta{NextCursor: nextCursor, HasMore: &hasMore})
}

type updateRouteRequest struct {
	Origin      string             `json:"origin" validate:"required"`
	Destination string             `json:"destination" validate:"required"`
	DistanceKm  *float64           `json:"distanceKm"`
	Stops       []routeStopRequest `json:"stops"`
}

func (h *handlers) updateRoute(w http.ResponseWriter, r *http.Request) {
	var req updateRouteRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	claims, _ := auth.FromContext(r.Context())
	route, err := h.deps.Route.Update(r.Context(), claims, chi.URLParam(r, "id"), service.UpdateRouteInput{
		Origin:      req.Origin,
		Destination: req.Destination,
		DistanceKm:  req.DistanceKm,
		Stops:       toDomainStops(req.Stops),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	httpx.WriteSuccess(w, http.StatusOK, toRouteResponse(route), nil)
}
