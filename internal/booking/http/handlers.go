package http

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"github.com/sl-bus/backend/internal/booking/domain"
	"github.com/sl-bus/backend/internal/booking/service"
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

		// TODO: Extract passenger ID from JWT claims
		passengerID := "temp-passenger-id"

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
			default:
				http.Error(w, `{"code":"INTERNAL_ERROR","message":"Failed to create booking"}`, http.StatusInternalServerError)
			}
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(bookingToResponse(booking))
	}
}

func NewGetBookingHandler(svc *service.CheckoutService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bookingID := chi.URLParam(r, "id")

		booking, err := svc.GetBooking(r.Context(), bookingID)
		if err != nil {
			http.Error(w, `{"code":"BOOKING_NOT_FOUND"}`, http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(bookingToResponse(booking))
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

		booking, err := svc.ConfirmBooking(r.Context(), bookingID, req.PaymentRef)
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
		json.NewEncoder(w).Encode(bookingToResponse(booking))
	}
}

func NewCancelBookingHandler(svc *service.CheckoutService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		bookingID := chi.URLParam(r, "id")

		booking, err := svc.CancelBooking(r.Context(), bookingID)
		if err != nil {
			slog.Error("cancel_booking failed", "error", err)
			http.Error(w, `{"code":"BOOKING_NOT_FOUND"}`, http.StatusNotFound)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(bookingToResponse(booking))
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
		json.NewEncoder(w).Encode(map[string]interface{}{
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
