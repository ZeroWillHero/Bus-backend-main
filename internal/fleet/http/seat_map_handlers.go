package http

import (
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/sl-bus/backend/internal/fleet/domain"
	"github.com/sl-bus/backend/internal/platform/auth"
	"github.com/sl-bus/backend/internal/platform/httpx"
)

type seatResponse struct {
	Number string `json:"number"`
	Type   string `json:"type"`
	Row    int    `json:"row"`
	Col    int    `json:"col"`
}

type seatMapResponse struct {
	TotalSeats int            `json:"totalSeats"`
	Seats      []seatResponse `json:"seats"`
}

func toSeatMapResponse(sm *domain.SeatMap) seatMapResponse {
	out := seatMapResponse{TotalSeats: sm.TotalSeats}
	for _, s := range sm.Seats {
		out.Seats = append(out.Seats, seatResponse{Number: s.Number, Type: s.Type, Row: s.Row, Col: s.Col})
	}
	return out
}

type replaceSeatMapRequest struct {
	Seats []struct {
		Number string `json:"number" validate:"required"`
		Type   string `json:"type" validate:"required"`
		Row    int    `json:"row"`
		Col    int    `json:"col"`
	} `json:"seats" validate:"required,min=1,dive"`
}

func (h *handlers) replaceSeatMap(w http.ResponseWriter, r *http.Request) {
	var req replaceSeatMapRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	claims, _ := auth.FromContext(r.Context())
	seats := make([]domain.Seat, 0, len(req.Seats))
	for _, s := range req.Seats {
		seats = append(seats, domain.Seat{Number: s.Number, Type: s.Type, Row: s.Row, Col: s.Col})
	}
	seatMap, err := h.deps.SeatMap.Replace(r.Context(), claims, chi.URLParam(r, "id"), seats)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	httpx.WriteSuccess(w, http.StatusOK, toSeatMapResponse(seatMap), nil)
}

func (h *handlers) getSeatMap(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.FromContext(r.Context())
	seatMap, err := h.deps.SeatMap.Get(r.Context(), claims, chi.URLParam(r, "id"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	httpx.WriteSuccess(w, http.StatusOK, toSeatMapResponse(seatMap), nil)
}
