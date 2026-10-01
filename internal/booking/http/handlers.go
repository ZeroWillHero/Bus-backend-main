package http

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/sl-bus/backend/internal/booking/domain"
	"github.com/sl-bus/backend/internal/booking/service"
	"github.com/sl-bus/backend/internal/platform/auth"
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
	Status        string  `json:"status"`
	TotalAmount   float64 `json:"totalAmount"`
	HoldExpiresAt *string `json:"holdExpiresAt,omitempty"`
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

func NewCreateBookingHandler(svc *service.CheckoutService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req CreateBookingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
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
			http.Error(w, `{"code":"AUTH_UNAUTHORIZED"}`, http.StatusUnauthorized)
			return
		}

		input := service.CreateHoldInput{
			TripID:      req.TripID,
			Seats:       seats,
			PassengerID: passengerID,
		}

		booking, err := svc.CreateHold(r.Context(), input)
		if err != nil {
			slog.Error("create_hold failed", "error", err)
			switch err {
			case domain.ErrSeatUnavailable:
				http.Error(w, `{"code":"BOOKING_SEAT_UNAVAILABLE","message":"Seat unavailable"}`, http.StatusConflict)
			case domain.ErrUnknownSeat, domain.ErrDuplicateSeat, domain.ErrNoSeatsSelected:
				http.Error(w, `{"code":"VALIDATION_FAILED","message":"Invalid seat selection"}`, http.StatusUnprocessableEntity)
			default:
				http.Error(w, `{"code":"INTERNAL_ERROR","message":"Failed to create booking"}`, http.StatusInternalServerError)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(bookingToResponse(booking))
	}
}

func NewGetBookingHandler(svc *service.CheckoutService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bookingID := chi.URLParam(r, "id")

		actor, ok := actorID(r)
		if !ok {
			http.Error(w, `{"code":"AUTH_UNAUTHORIZED"}`, http.StatusUnauthorized)
			return
		}
		booking, err := svc.GetBooking(r.Context(), actor, bookingID)
		if err != nil {
			http.Error(w, `{"code":"BOOKING_NOT_FOUND"}`, http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(bookingToResponse(booking))
	}
}

type ConfirmBookingRequest struct {
	PaymentRef string `json:"paymentRef"`
}

func NewConfirmBookingHandler(svc *service.CheckoutService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bookingID := chi.URLParam(r, "id")

		var req ConfirmBookingRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, `{"error":"invalid_request"}`, http.StatusBadRequest)
			return
		}

		actor, ok := actorID(r)
		if !ok {
			http.Error(w, `{"code":"AUTH_UNAUTHORIZED"}`, http.StatusUnauthorized)
			return
		}
		booking, err := svc.ConfirmBooking(r.Context(), actor, bookingID, req.PaymentRef)
		if err != nil {
			slog.Error("confirm_booking failed", "error", err)
			switch err {
			case domain.ErrHoldExpired:
				http.Error(w, `{"code":"BOOKING_HOLD_EXPIRED","message":"Hold has expired"}`, http.StatusGone)
			case domain.ErrBookingNotFound:
				http.Error(w, `{"code":"BOOKING_NOT_FOUND"}`, http.StatusNotFound)
			default:
				http.Error(w, `{"code":"PAYMENT_DECLINED","message":"Payment failed"}`, http.StatusPaymentRequired)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(bookingToResponse(booking))
	}
}

func NewCancelBookingHandler(svc *service.CheckoutService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bookingID := chi.URLParam(r, "id")

		actor, ok := actorID(r)
		if !ok {
			http.Error(w, `{"code":"AUTH_UNAUTHORIZED"}`, http.StatusUnauthorized)
			return
		}
		booking, err := svc.CancelBooking(r.Context(), actor, bookingID)
		if err != nil {
			slog.Error("cancel_booking failed", "error", err)
			http.Error(w, `{"code":"BOOKING_NOT_FOUND"}`, http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(bookingToResponse(booking))
	}
}

func NewCheckInHandler(svc *service.CheckoutService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		qrToken := chi.URLParam(r, "qrToken")

		ticket, err := svc.CheckIn(r.Context(), qrToken)
		if err != nil {
			slog.Error("check_in failed", "error", err)
			switch err {
			case domain.ErrInvalidQRToken:
				http.Error(w, `{"code":"INVALID_QR_TOKEN"}`, http.StatusBadRequest)
			case domain.ErrAlreadyCheckedIn:
				http.Error(w, `{"code":"TICKET_ALREADY_CHECKED_IN"}`, http.StatusConflict)
			default:
				http.Error(w, `{"code":"TICKET_NOT_FOUND"}`, http.StatusNotFound)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"id":        ticket.ID,
			"bookingId": ticket.BookingID,
			"checkedIn": ticket.CheckedInAt != nil,
		})
	}
}

func bookingToResponse(b *domain.Booking) BookingResponse {
	var holdExpiresAt *string
	if b.HoldExpiresAt != nil {
		s := b.HoldExpiresAt.Format("2006-01-02T15:04:05Z")
		holdExpiresAt = &s
	}

	return BookingResponse{
		ID:            b.ID,
		TripID:        b.TripID,
		Status:        string(b.Status),
		TotalAmount:   b.TotalAmount,
		HoldExpiresAt: holdExpiresAt,
	}
}
