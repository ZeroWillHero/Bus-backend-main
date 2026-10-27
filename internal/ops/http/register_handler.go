package http

import (
	"encoding/json"
	"net/http"

	"github.com/go-playground/validator/v10"

	"github.com/sl-bus/backend/internal/ops/domain"
	"github.com/sl-bus/backend/internal/ops/service"
	"github.com/sl-bus/backend/internal/platform/httpx"
)

var validate = validator.New()

type registerRequest struct {
	BusinessName       string `json:"businessName" validate:"required"`
	BusinessRegNo      string `json:"businessRegNo" validate:"required"`
	ContactEmail       string `json:"contactEmail" validate:"required,email"`
	ContactPhone       string `json:"contactPhone"`
	RegistrationDocURL string `json:"registrationDocUrl"`
	Password           string `json:"password" validate:"required"`
}

type registerResponse struct {
	OperatorID string `json:"operatorId"`
	UserID     string `json:"userId"`
	Status     string `json:"status"`
}

// NewRegisterHandler is mounted directly at POST /v1/auth/register in
// cmd/server/main.go — a deliberate deviation from mounting it under
// /v1/operators, since it's fundamentally account creation (FR-OPS-1),
// matching AUTHENTICATION_DESIGN.md §3.1's own /auth/register flow.
func NewRegisterHandler(reg *service.RegistrationService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req registerRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			httpx.WriteDecodeError(w, err)
			return
		}
		if err := validate.Struct(&req); err != nil {
			httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{Code: "VALIDATION_FAILED", Message: "Please check the highlighted fields."}, nil)
			return
		}

		operator, user, err := reg.RegisterOperator(r.Context(), service.RegisterOperatorInput{
			BusinessName:       req.BusinessName,
			BusinessRegNo:      req.BusinessRegNo,
			ContactEmail:       req.ContactEmail,
			ContactPhone:       req.ContactPhone,
			RegistrationDocURL: req.RegistrationDocURL,
			Password:           req.Password,
		})
		if err != nil {
			writeDomainError(w, err)
			return
		}

		httpx.WriteSuccess(w, http.StatusCreated, registerResponse{
			OperatorID: operator.ID,
			UserID:     user.ID,
			Status:     string(domain.StatusPending),
		}, nil)
	}
}
