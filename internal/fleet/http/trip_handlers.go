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
	ID          string    `json:"id"`
	RouteID     string    `json:"routeId"`
	BusID       string    `json:"busId"`
	DepartureAt time.Time `json:"departureAt"`
	Status      string    `json:"status"`
	BaseFare    float64   `json:"baseFare"`
	// AvailableSeats is the denormalised counter kept at hold/confirm time;
	// total/booked/held below come from the seat inventory.
	AvailableSeats int       `json:"availableSeats"`
	Currency       string    `json:"currency"`
	TotalSeats     int       `json:"totalSeats"`
	BookedSeats    int       `json:"bookedSeats"`
	HeldSeats      int       `json:"heldSeats"`
	Route          *routeRef `json:"route,omitempty"`
	Bus            *busRef   `json:"bus,omitempty"`
}

type routeRef struct {
	ID          string `json:"id"`
	Origin      string `json:"origin"`
	Destination string `json:"destination"`
}

type busRef struct {
	ID             string `json:"id"`
	RegistrationNo string `json:"registrationNo"`
	BusClass       string `json:"busClass"`
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
		Currency:       service.Currency,
	}
}

// tripResponses converts trips and fills the derived fields (seat counts,
// embedded route/bus) with a single query for the whole page.
func (h *handlers) tripResponses(r *http.Request, trips []*domain.Trip) ([]tripResponse, error) {
	claims, _ := auth.FromContext(r.Context())
	extras, err := h.deps.ReadModel.Trips(r.Context(), claims, service.TripIDs(trips))
	if err != nil {
		return nil, err
	}
	out := make([]tripResponse, 0, len(trips))
	for _, t := range trips {
		resp := toTripResponse(t)
		if e, ok := extras[t.ID]; ok {
			resp.TotalSeats, resp.BookedSeats, resp.HeldSeats = e.TotalSeats, e.BookedSeats, e.HeldSeats
			resp.Route = &routeRef{ID: e.Route.ID, Origin: e.Route.Origin, Destination: e.Route.Destination}
			resp.Bus = &busRef{ID: e.Bus.ID, RegistrationNo: e.Bus.RegistrationNo, BusClass: e.Bus.BusClass}
		}
		out = append(out, resp)
	}
	return out, nil
}

func (h *handlers) writeTrip(w http.ResponseWriter, r *http.Request, status int, trip *domain.Trip) {
	out, err := h.tripResponses(r, []*domain.Trip{trip})
	if err != nil {
		writeDomainError(w, err)
		return
	}
	httpx.WriteSuccess(w, status, out[0], nil)
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
	out, err := h.tripResponses(r, trips)
	if err != nil {
		writeDomainError(w, err)
		return
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
	h.writeTrip(w, r, http.StatusOK, trip)
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
	out, err := h.tripResponses(r, trips)
	if err != nil {
		writeDomainError(w, err)
		return
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
	h.writeTrip(w, r, http.StatusOK, trip)
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
	h.writeTrip(w, r, http.StatusOK, trip)
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
