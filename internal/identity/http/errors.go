package http

import (
	"net/http"

	"github.com/sl-bus/backend/internal/identity/domain"
	"github.com/sl-bus/backend/internal/identity/service"
	"github.com/sl-bus/backend/internal/platform/httpx"
)

// writeDomainError maps a use-case error to the ENGINEERING_STANDARDS.md
// §3 namespaced error taxonomy.
func writeDomainError(w http.ResponseWriter, err error) {
	switch err {
	case domain.ErrInvalidOTP:
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{Code: "AUTH_INVALID_OTP", Message: "That code isn't right. Please try again."}, nil)
	case domain.ErrOTPExpired:
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{Code: "AUTH_OTP_EXPIRED", Message: "That code has expired. Request a new one."}, nil)
	case domain.ErrOTPRateLimited:
		httpx.WriteError(w, http.StatusTooManyRequests, httpx.APIError{Code: "AUTH_OTP_RATE_LIMITED", Message: "Too many requests. Please try again later."}, nil)
	case domain.ErrInvalidCredentials:
		httpx.WriteError(w, http.StatusUnauthorized, httpx.APIError{Code: "AUTH_INVALID_CREDENTIALS", Message: "Incorrect email or password."}, nil)
	case domain.ErrAccountLocked:
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{Code: "AUTH_ACCOUNT_LOCKED", Message: "Too many failed attempts. Please try again later."}, nil)
	case domain.ErrInvalidMFACode:
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{Code: "AUTH_INVALID_MFA_CODE", Message: "That authentication code isn't right."}, nil)
	case domain.ErrTokenInvalid:
		httpx.WriteError(w, http.StatusUnauthorized, httpx.APIError{Code: "AUTH_TOKEN_INVALID", Message: "Please sign in again."}, nil)
	case domain.ErrTokenExpired:
		httpx.WriteError(w, http.StatusUnauthorized, httpx.APIError{Code: "AUTH_TOKEN_EXPIRED", Message: "Your session has expired. Please sign in again."}, nil)
	case domain.ErrUserNotFound:
		httpx.WriteError(w, http.StatusNotFound, httpx.APIError{Code: "AUTH_USER_NOT_FOUND", Message: "Account not found."}, nil)
	case domain.ErrSessionNotOwned:
		httpx.WriteError(w, http.StatusForbidden, httpx.APIError{Code: "AUTH_FORBIDDEN", Message: "You don't have permission to perform this action."}, nil)
	case service.ErrOperatorSuspended:
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{Code: "OPS_OPERATOR_SUSPENDED", Message: "This operator account has been suspended."}, nil)
	default:
		httpx.WriteError(w, http.StatusInternalServerError, httpx.APIError{Code: "PLATFORM_INTERNAL_ERROR", Message: "Something went wrong. Please try again."}, nil)
	}
}
