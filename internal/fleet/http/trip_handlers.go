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

type tripResponse struct {
	ID             string    `json:"id"`
	RouteID        string    `json:"routeId"`
	BusID          string    `json:"busId"`
	DepartureAt    time.Time `json:"departureAt"`
	Status         string    `json:"status"`
	BaseFare       float64   `json:"baseFare"`
	AvailableSeats int       `json:"availableSeats"`
}

func toTripResponse(t *domain.Trip) tripResponse {
	return tripResponse{
		ID:             t.ID,
		RouteID:        t.RouteID,
		BusID:          t.BusID,
		DepartureAt:    t.DepartureAt,
		Status:         string(t.Status),
		BaseFare:       t.BaseFare,
		AvailableSeats: t.AvailableSeats,
	}
}

// recurrenceRequest's StartDate/EndDate/TimeOfDay are all interpreted as
// UTC (ENGINEERING_STANDARDS.md §7.1: "stored and transmitted in UTC
// always; converted to local time (Asia/Colombo, UTC+5:30) only at the
// UI presentation layer") — the operator app's trip-creation form is
// responsible for converting whatever local time the operator enters
// into UTC before sending it here, exactly as it must for the one-off
// departureAt field.
type recurrenceRequest struct {
	StartDate string `json:"startDate" validate:"required"` // YYYY-MM-DD, UTC
	EndDate   string `json:"endDate" validate:"required"`   // YYYY-MM-DD, UTC
	Weekdays  []int  `json:"weekdays"`                      // 0=Sunday..6=Saturday; empty = every day
	TimeOfDay string `json:"timeOfDay" validate:"required"` // "HH:MM", UTC
}

type createTripsRequest struct {
	RouteID       string             `json:"routeId" validate:"required"`
	BusID         string             `json:"busId" validate:"required"`
	DepartureAt   *string            `json:"departureAt"` // RFC3339, one-off
	Recurrence    *recurrenceRequest `json:"recurrence"`
	BaseFare      float64            `json:"baseFare" validate:"required,gt=0"`
	FareOverrides map[string]float64 `json:"fareOverrides"`
}

func (h *handlers) createTrips(w http.ResponseWriter, r *http.Request) {
	var req createTripsRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}

	input := service.CreateTripsInput{
		RouteID:       req.RouteID,
		BusID:         req.BusID,
		BaseFare:      req.BaseFare,
		FareOverrides: req.FareOverrides,
	}

	if req.DepartureAt != nil {
		t, err := time.Parse(time.RFC3339, *req.DepartureAt)
		if err != nil {
			httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{Code: "VALIDATION_FAILED", Message: "departureAt must be a valid RFC3339 timestamp."}, nil)
			return
		}
		input.DepartureAt = &t
	} else if req.Recurrence != nil {
		rule, err := parseRecurrence(*req.Recurrence)
		if err != nil {
			httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{Code: "VALIDATION_FAILED", Message: err.Error()}, nil)
			return
		}
		input.Recurrence = rule
	} else {
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{Code: "VALIDATION_FAILED", Message: "Provide either departureAt or a recurrence rule."}, nil)
		return
	}

	claims, _ := auth.FromContext(r.Context())
	trips, err := h.deps.Trip.CreateTrips(r.Context(), claims, input)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	out := make([]tripResponse, 0, len(trips))
	for _, t := range trips {
		out = append(out, toTripResponse(t))
	}
	httpx.WriteSuccess(w, http.StatusCreated, out, nil)
}

func parseRecurrence(req recurrenceRequest) (*domain.RecurrenceRule, error) {
	startDate, err := time.Parse("2006-01-02", req.StartDate)
	if err != nil {
		return nil, errInvalidDate("startDate")
	}
	endDate, err := time.Parse("2006-01-02", req.EndDate)
	if err != nil {
		return nil, errInvalidDate("endDate")
	}
	timeOfDay, err := time.Parse("15:04", req.TimeOfDay)
	if err != nil {
		return nil, errInvalidDate("timeOfDay")
	}
	offset := time.Duration(timeOfDay.Hour())*time.Hour + time.Duration(timeOfDay.Minute())*time.Minute

	weekdays := make(map[time.Weekday]bool, len(req.Weekdays))
	for _, wd := range req.Weekdays {
		weekdays[time.Weekday(wd)] = true
	}

	return &domain.RecurrenceRule{
		TimeOfDay: offset,
		Weekdays:  weekdays,
		StartDate: startDate,
		EndDate:   endDate,
	}, nil
}

type validationError string

func (e validationError) Error() string { return string(e) }

func errInvalidDate(field string) error {
	return validationError(field + " is not a valid date/time.")
}

func (h *handlers) getTrip(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.FromContext(r.Context())
	trip, err := h.deps.Trip.Get(r.Context(), claims, chi.URLParam(r, "id"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	httpx.WriteSuccess(w, http.StatusOK, toTripResponse(trip), nil)
}

func (h *handlers) listTrips(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.FromContext(r.Context())
	filter := domain.TripListFilter{
		RouteID: r.URL.Query().Get("routeId"),
		Status:  r.URL.Query().Get("status"),
	}
	cursor := r.URL.Query().Get("cursor")
	trips, nextCursor, err := h.deps.Trip.List(r.Context(), claims, filter, cursor, service.DefaultListLimit)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	out := make([]tripResponse, 0, len(trips))
	for _, t := range trips {
		out = append(out, toTripResponse(t))
	}
	hasMore := nextCursor != ""
	httpx.WriteSuccess(w, http.StatusOK, out, &httpx.Meta{NextCursor: nextCursor, HasMore: &hasMore})
}

func (h *handlers) cancelTrip(w http.ResponseWriter, r *http.Request) {
	claims, _ := auth.FromContext(r.Context())
	trip, err := h.deps.Trip.Cancel(r.Context(), claims, chi.URLParam(r, "id"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	httpx.WriteSuccess(w, http.StatusOK, toTripResponse(trip), nil)
}

type rescheduleTripRequest struct {
	DepartureAt string `json:"departureAt" validate:"required"`
}

func (h *handlers) rescheduleTrip(w http.ResponseWriter, r *http.Request) {
	var req rescheduleTripRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	departureAt, err := time.Parse(time.RFC3339, req.DepartureAt)
	if err != nil {
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{Code: "VALIDATION_FAILED", Message: "departureAt must be a valid RFC3339 timestamp."}, nil)
		return
	}
	claims, _ := auth.FromContext(r.Context())
	trip, err := h.deps.Trip.Reschedule(r.Context(), claims, chi.URLParam(r, "id"), departureAt)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	httpx.WriteSuccess(w, http.StatusOK, toTripResponse(trip), nil)
}

type updateTripFaresRequest struct {
	Fares map[string]float64 `json:"fares" validate:"required,min=1"`
}

func (h *handlers) updateTripFares(w http.ResponseWriter, r *http.Request) {
	var req updateTripFaresRequest
	if !decodeAndValidate(w, r, &req) {
		return
	}
	claims, _ := auth.FromContext(r.Context())
	fares, err := h.deps.Trip.UpdateFares(r.Context(), claims, chi.URLParam(r, "id"), req.Fares)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	out := make(map[string]float64, len(fares))
	for _, f := range fares {
		out[f.SeatType] = f.Fare
	}
	httpx.WriteSuccess(w, http.StatusOK, out, nil)
}
