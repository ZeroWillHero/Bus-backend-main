package http

import (
	"net/http"

	"github.com/sl-bus/backend/internal/ops/domain"
	"github.com/sl-bus/backend/internal/ops/service"
	"github.com/sl-bus/backend/internal/platform/httpx"
)

func writeDomainError(w http.ResponseWriter, err error) {
	switch err {
	case domain.ErrOperatorNotFound:
		httpx.WriteError(w, http.StatusNotFound, httpx.APIError{Code: "OPS_OPERATOR_NOT_FOUND", Message: "Operator not found."}, nil)
	case domain.ErrInvalidStatusTransition:
		httpx.WriteError(w, http.StatusConflict, httpx.APIError{Code: "OPS_INVALID_STATUS_TRANSITION", Message: "That status change isn't allowed from the operator's current state."}, nil)
	case domain.ErrDuplicateBusinessRegNo:
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{Code: "OPS_DUPLICATE_BUSINESS_REG_NO", Message: "This business registration number is already in use."}, nil)
	case service.ErrPasswordBreached:
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{Code: "AUTH_PASSWORD_BREACHED", Message: "That password has appeared in a data breach. Please choose another."}, nil)
	case service.ErrPasswordTooShort:
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{
			Code:    "VALIDATION_FAILED",
			Message: "Please check the highlighted fields.",
			Fields:  []httpx.FieldError{{Field: "password", Issue: "Must be at least 10 characters."}},
		}, nil)
	case service.ErrRegistrationDocTooLarge:
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{
			Code:    "VALIDATION_FAILED",
			Message: "Please check the highlighted fields.",
			Fields:  []httpx.FieldError{{Field: "registrationDocUrl", Issue: "Document is too large."}},
		}, nil)
	case service.ErrRegistrationDocInvalid:
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{
			Code:    "VALIDATION_FAILED",
			Message: "Please check the highlighted fields.",
			Fields:  []httpx.FieldError{{Field: "registrationDocUrl", Issue: "Must be an https URL or a PNG, JPEG or WebP image."}},
		}, nil)
	default:
		httpx.WriteError(w, http.StatusInternalServerError, httpx.APIError{Code: "PLATFORM_INTERNAL_ERROR", Message: "Something went wrong. Please try again."}, nil)
	}
}
