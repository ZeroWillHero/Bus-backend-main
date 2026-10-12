package http

import (
	"net/http"

	"github.com/sl-bus/backend/internal/fleet/domain"
	"github.com/sl-bus/backend/internal/platform/httpx"
)

func writeDomainError(w http.ResponseWriter, err error) {
	switch err {
	case domain.ErrBusNotFound:
		httpx.WriteError(w, http.StatusNotFound, httpx.APIError{Code: "FLEET_BUS_NOT_FOUND", Message: "Bus not found."}, nil)
	case domain.ErrRouteNotFound:
		httpx.WriteError(w, http.StatusNotFound, httpx.APIError{Code: "FLEET_ROUTE_NOT_FOUND", Message: "Route not found."}, nil)
	case domain.ErrTripNotFound:
		httpx.WriteError(w, http.StatusNotFound, httpx.APIError{Code: "TRIP_NOT_FOUND", Message: "Trip not found."}, nil)
	case domain.ErrDuplicateRegistrationNo:
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{Code: "FLEET_DUPLICATE_REGISTRATION_NO", Message: "This bus registration number is already in use."}, nil)
	case domain.ErrOperatorNotApproved:
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{Code: "OPS_OPERATOR_NOT_APPROVED", Message: "Your operator account is still pending approval."}, nil)
	case domain.ErrOperatorMismatch:
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{Code: "FLEET_OPERATOR_MISMATCH", Message: "The selected bus and route must belong to your operator."}, nil)
	case domain.ErrInvalidTripStatusTransition:
		httpx.WriteError(w, http.StatusConflict, httpx.APIError{Code: "FLEET_INVALID_TRIP_STATUS_TRANSITION", Message: "That status change isn't allowed from the trip's current state."}, nil)
	case domain.ErrBusHasNoSeatMap:
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{Code: "FLEET_BUS_HAS_NO_SEAT_MAP", Message: "Draw this bus's seat map before scheduling trips on it."}, nil)
	case domain.ErrEmptySeatMap, domain.ErrDuplicateSeatNumber, domain.ErrInvalidSeatMap:
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{Code: "FLEET_INVALID_SEAT_MAP", Message: "That seat map isn't valid."}, nil)
	case domain.ErrNotFound:
		httpx.WriteError(w, http.StatusNotFound, httpx.APIError{Code: "FLEET_NOT_FOUND", Message: "Not found."}, nil)
	default:
		httpx.WriteError(w, http.StatusInternalServerError, httpx.APIError{Code: "PLATFORM_INTERNAL_ERROR", Message: "Something went wrong. Please try again."}, nil)
	}
}
