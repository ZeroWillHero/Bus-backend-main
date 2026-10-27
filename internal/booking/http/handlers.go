package http

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sl-bus/backend/internal/booking/domain"
	"github.com/sl-bus/backend/internal/booking/service"
	"github.com/sl-bus/backend/internal/platform/auth"
	"github.com/sl-bus/backend/internal/platform/httpx"
)

// CreateBookingRequest holds the seat selection for a new booking
type CreateBookingRequest struct {
	TripID string `json:"tripId"`
	Seats  []struct {
		SeatNumber    string  `json:"seatNumber"`
		PassengerName string  `json:"passengerName"`
		PassengerNic  string  `json:"passengerNic,omitempty"`
		FareAmount    float64 `json:"fareAmount"`
	} `json:"seats"`
}

type BookingResponse struct {
	ID            string  `json:"id"`
	TripID        string  `json:"tripId"`
	PassengerID   string  `json:"passengerId"`
	Status        string  `json:"status"`
	TotalAmount   float64 `json:"totalAmount"`
	Currency      string  `json:"currency"`
	HoldExpiresAt *string `json:"holdExpiresAt,omitempty"`
	CreatedAt     string  `json:"createdAt"`
	UpdatedAt     string  `json:"updatedAt"`
	ConfirmedAt   *string `json:"confirmedAt,omitempty"`
	CancelledAt   *string `json:"cancelledAt,omitempty"`
}

type TicketResponse struct {
	ID        string `json:"id"`
	BookingID string `json:"bookingId"`
	QRToken   string `json:"qrToken"`
	IssuedAt  string `json:"issuedAt"`
	CheckedIn bool   `json:"checkedIn"`
}

// actorID returns the authenticated user's id (JWT subject). It is empty/false
// only if the auth middleware did not run, in which case callers must refuse.
func actorID(r *http.Request) (string, bool) {
	claims, ok := auth.FromContext(r.Context())
	if !ok || claims.Subject == "" {
		return "", false
	}
	return claims.Subject, true
}

func writeErr(w http.ResponseWriter, status int, code, message string) {
	httpx.WriteError(w, status, httpx.APIError{Code: code, Message: message}, nil)
}

func unauthorized(w http.ResponseWriter) {
	writeErr(w, http.StatusUnauthorized, "AUTH_TOKEN_INVALID", "Please sign in again.")
}

func NewCreateBookingHandler(svc *service.CheckoutService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req CreateBookingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteDecodeError(w, err)
			return
		}

		// Convert to service input
		seats := make([]service.SeatSelection, len(req.Seats))
		for i, s := range req.Seats {
			seats[i] = service.SeatSelection{
				SeatNumber:    s.SeatNumber,
				PassengerName: s.PassengerName,
				PassengerNic:  s.PassengerNic,
				FareAmount:    s.FareAmount,
			}
		}

		// Identity comes from the verified JWT, never from the request body.
		passengerID, ok := actorID(r)
		if !ok {
			unauthorized(w)
			return
		}

		booking, err := svc.CreateHold(r.Context(), service.CreateHoldInput{
			TripID:      req.TripID,
			Seats:       seats,
			PassengerID: passengerID,
		})
		if err != nil {
			slog.Error("create_hold failed", "error", err)
			switch {
			case errors.Is(err, domain.ErrSeatUnavailable):
				writeErr(w, http.StatusConflict, "BOOKING_SEAT_UNAVAILABLE", "One or more of those seats were just taken.")
			case errors.Is(err, domain.ErrTripNotBookable):
				writeErr(w, http.StatusConflict, "BOOKING_TRIP_NOT_BOOKABLE", "This trip has departed or is no longer open for booking.")
			case errors.Is(err, domain.ErrTooManySeats):
				writeErr(w, http.StatusUnprocessableEntity, "BOOKING_TOO_MANY_SEATS", fmt.Sprintf("At most %d seats per booking.", domain.MaxSeatsPerBooking))
			case errors.Is(err, domain.ErrTooManyActiveHolds):
				writeErr(w, http.StatusTooManyRequests, "BOOKING_TOO_MANY_HOLDS", "You already have too many seats on hold. Pay for or cancel one first.")
			case errors.Is(err, domain.ErrUnknownSeat), errors.Is(err, domain.ErrDuplicateSeat), errors.Is(err, domain.ErrNoSeatsSelected):
				writeErr(w, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "Invalid seat selection.")
			default:
				writeErr(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to create booking.")
			}
			return
		}
		httpx.WriteSuccess(w, http.StatusCreated, bookingToResponse(booking), nil)
	}
}

func NewGetBookingHandler(svc *service.CheckoutService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actorID(r)
		if !ok {
			unauthorized(w)
			return
		}
		booking, err := svc.GetBooking(r.Context(), actor, chi.URLParam(r, "id"))
		if err != nil {
			writeErr(w, http.StatusNotFound, "BOOKING_NOT_FOUND", "Booking not found.")
			return
		}
		httpx.WriteSuccess(w, http.StatusOK, bookingToResponse(booking), nil)
	}
}

// NewListBookingsHandler lists the signed-in passenger's own bookings.
func NewListBookingsHandler(svc *service.CheckoutService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actorID(r)
		if !ok {
			unauthorized(w)
			return
		}
		limit := 20
		if v := r.URL.Query().Get("limit"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 100 {
				writeErr(w, http.StatusUnprocessableEntity, "VALIDATION_FAILED", "limit must be between 1 and 100.")
				return
			}
			limit = n
		}
		bookings, next, err := svc.ListBookings(r.Context(), actor, r.URL.Query().Get("cursor"), limit)
		if err != nil {
			slog.Error("list_bookings failed", "error", err)
			writeErr(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to load bookings.")
			return
		}
		out := make([]BookingResponse, len(bookings))
		for i, b := range bookings {
			out[i] = bookingToResponse(b)
		}
		hasMore := next != ""
		httpx.WriteSuccess(w, http.StatusOK, out, &httpx.Meta{NextCursor: next, HasMore: &hasMore})
	}
}

// NewGetTicketHandler returns the passenger's own ticket, including the QR
// token they show at boarding.
func NewGetTicketHandler(svc *service.CheckoutService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actorID(r)
		if !ok {
			unauthorized(w)
			return
		}
		ticket, err := svc.GetTicket(r.Context(), actor, chi.URLParam(r, "id"))
		if err != nil {
			code := "TICKET_NOT_FOUND"
			if errors.Is(err, domain.ErrBookingNotFound) {
				code = "BOOKING_NOT_FOUND"
			}
			writeErr(w, http.StatusNotFound, code, "Ticket not found.")
			return
		}
		httpx.WriteSuccess(w, http.StatusOK, ticketToResponse(ticket), nil)
	}
}

type ConfirmBookingRequest struct {
	PaymentRef string `json:"paymentRef"`
}

func NewConfirmBookingHandler(svc *service.CheckoutService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req ConfirmBookingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteDecodeError(w, err)
			return
		}
		actor, ok := actorID(r)
		if !ok {
			unauthorized(w)
			return
		}
		booking, err := svc.ConfirmBooking(r.Context(), actor, chi.URLParam(r, "id"), req.PaymentRef)
		if err != nil {
			slog.Error("confirm_booking failed", "error", err)
			switch {
			case errors.Is(err, domain.ErrHoldExpired):
				writeErr(w, http.StatusGone, "BOOKING_HOLD_EXPIRED", "Your seat hold has expired.")
			case errors.Is(err, domain.ErrBookingNotFound):
				writeErr(w, http.StatusNotFound, "BOOKING_NOT_FOUND", "Booking not found.")
			case errors.Is(err, domain.ErrInvalidStatusTransition):
				writeErr(w, http.StatusConflict, "BOOKING_INVALID_STATE", "This booking can no longer be paid.")
			case errors.Is(err, domain.ErrConcurrentUpdate):
				writeErr(w, http.StatusConflict, "BOOKING_CONFLICT", "This booking is already being paid for.")
			case errors.Is(err, domain.ErrSeatUnavailable):
				writeErr(w, http.StatusConflict, "BOOKING_SEAT_UNAVAILABLE", "One or more of your seats are no longer held.")
			case errors.Is(err, domain.ErrPaymentDeclined):
				writeErr(w, http.StatusPaymentRequired, "PAYMENT_DECLINED", "Payment failed.")
			default:
				writeErr(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to confirm booking.")
			}
			return
		}
		httpx.WriteSuccess(w, http.StatusOK, bookingToResponse(booking), nil)
	}
}

func NewCancelBookingHandler(svc *service.CheckoutService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		actor, ok := actorID(r)
		if !ok {
			unauthorized(w)
			return
		}
		booking, err := svc.CancelBooking(r.Context(), actor, chi.URLParam(r, "id"))
		if err != nil {
			slog.Error("cancel_booking failed", "error", err)
			switch {
			case errors.Is(err, domain.ErrBookingNotFound):
				writeErr(w, http.StatusNotFound, "BOOKING_NOT_FOUND", "Booking not found.")
			case errors.Is(err, domain.ErrInvalidStatusTransition):
				writeErr(w, http.StatusConflict, "BOOKING_NOT_CANCELLABLE", "This booking can no longer be cancelled.")
			case errors.Is(err, domain.ErrConcurrentUpdate):
				writeErr(w, http.StatusConflict, "BOOKING_CONFLICT", "This booking was changed by another request.")
			default:
				writeErr(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to cancel booking.")
			}
			return
		}
		httpx.WriteSuccess(w, http.StatusOK, bookingToResponse(booking), nil)
	}
}

func NewCheckInHandler(svc *service.CheckoutService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		claims, ok := auth.FromContext(r.Context())
		if !ok {
			unauthorized(w)
			return
		}
		ticket, err := svc.CheckIn(r.Context(), claims.OperatorID, chi.URLParam(r, "qrToken"))
		if err != nil {
			slog.Error("check_in failed", "error", err)
			switch {
			case errors.Is(err, domain.ErrInvalidQRToken):
				writeErr(w, http.StatusBadRequest, "INVALID_QR_TOKEN", "This QR code is not valid.")
			case errors.Is(err, domain.ErrAlreadyCheckedIn):
				writeErr(w, http.StatusConflict, "TICKET_ALREADY_CHECKED_IN", "This ticket was already used.")
			case errors.Is(err, domain.ErrTicketTripCancelled):
				writeErr(w, http.StatusConflict, "TICKET_TRIP_CANCELLED", "This ticket's trip was cancelled.")
			case errors.Is(err, domain.ErrTicketNotValidNow):
				writeErr(w, http.StatusConflict, "TICKET_NOT_VALID_NOW", "This ticket can't be checked in at this time.")
			case errors.Is(err, domain.ErrTicketNotFound):
				writeErr(w, http.StatusNotFound, "TICKET_NOT_FOUND", "Ticket not found.")
			default:
				writeErr(w, http.StatusInternalServerError, "INTERNAL_ERROR", "Failed to check in ticket.")
			}
			return
		}
		httpx.WriteSuccess(w, http.StatusOK, map[string]any{
			"id":        ticket.ID,
			"bookingId": ticket.BookingID,
			"checkedIn": ticket.CheckedInAt != nil,
		}, nil)
	}
}

// utc renders an instant as RFC 3339 in UTC. The "Z" suffix is a claim that the
// instant is in UTC, and the server's local zone is not.
func utc(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func utcPtr(t *time.Time) *string {
	if t == nil {
		return nil
	}
	s := utc(*t)
	return &s
}

func bookingToResponse(b *domain.Booking) BookingResponse {
	return BookingResponse{
		ID:            b.ID,
		TripID:        b.TripID,
		PassengerID:   b.PassengerID,
		Status:        string(b.Status),
		TotalAmount:   b.TotalAmount,
		Currency:      b.Currency,
		HoldExpiresAt: utcPtr(b.HoldExpiresAt),
		CreatedAt:     utc(b.CreatedAt),
		UpdatedAt:     utc(b.UpdatedAt),
		ConfirmedAt:   utcPtr(b.ConfirmedAt),
		CancelledAt:   utcPtr(b.CancelledAt),
	}
}

func ticketToResponse(t *domain.Ticket) TicketResponse {
	return TicketResponse{
		ID:        t.ID,
		BookingID: t.BookingID,
		QRToken:   t.QRToken,
		IssuedAt:  utc(t.IssuedAt),
		CheckedIn: t.CheckedInAt != nil,
	}
}
