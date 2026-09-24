package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/sl-bus/backend/internal/fleet/domain"
	"github.com/sl-bus/backend/internal/platform/httpx"
)

type searchResultResponse struct {
	TripID         string         `json:"tripId"`
	OperatorName   string         `json:"operatorName"`
	Origin         string         `json:"origin"`
	Destination    string         `json:"destination"`
	DepartureAt    time.Time      `json:"departureAt"`
	BusClass       string         `json:"busClass"`
	Amenities      map[string]any `json:"amenities,omitempty"`
	BaseFare       float64        `json:"baseFare"`
	AvailableSeats int            `json:"availableSeats"`
}

// search handles GET /v1/trips/search — public, no auth
// (AUTHENTICATION_DESIGN.md §2.1: no login wall before browsing).
func (h *handlers) search(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	origin := q.Get("origin")
	destination := q.Get("destination")
	dateStr := q.Get("date")
	if origin == "" || destination == "" || dateStr == "" {
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{
			Code:    "VALIDATION_FAILED",
			Message: "origin, destination, and date are required.",
		}, nil)
		return
	}
	travelDate, err := time.Parse("2006-01-02", dateStr)
	if err != nil {
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{Code: "VALIDATION_FAILED", Message: "date must be YYYY-MM-DD."}, nil)
		return
	}

	sortBy := domain.SortByDeparture
	if q.Get("sort") == "price" {
		sortBy = domain.SortByPrice
	}

	var minPrice, maxPrice *float64
	if v := q.Get("minPrice"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			minPrice = &f
		}
	}
	if v := q.Get("maxPrice"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			maxPrice = &f
		}
	}

	results, nextCursor, err := h.deps.Search.Search(r.Context(), domain.SearchCriteria{
		Origin:      origin,
		Destination: destination,
		TravelDate:  travelDate,
		SortBy:      sortBy,
		BusClass:    q.Get("busClass"),
		MinPrice:    minPrice,
		MaxPrice:    maxPrice,
		Cursor:      q.Get("cursor"),
	})
	if err != nil {
		writeDomainError(w, err)
		return
	}

	out := make([]searchResultResponse, 0, len(results))
	for _, res := range results {
		out = append(out, searchResultResponse{
			TripID:         res.TripID,
			OperatorName:   res.OperatorName,
			Origin:         res.Origin,
			Destination:    res.Destination,
			DepartureAt:    res.DepartureAt,
			BusClass:       res.BusClass,
			Amenities:      res.Amenities,
			BaseFare:       res.BaseFare,
			AvailableSeats: res.AvailableSeats,
		})
	}
	hasMore := nextCursor != ""
	httpx.WriteSuccess(w, http.StatusOK, out, &httpx.Meta{NextCursor: nextCursor, HasMore: &hasMore})
}
