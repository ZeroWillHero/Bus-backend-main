// Package http adapts internal/fleet/service use cases to chi handlers
// (ARCHITECTURE.md §3.2).
package http

import (
	"encoding/json"
	"net/http"

	"github.com/go-playground/validator/v10"

	"github.com/sl-bus/backend/internal/platform/httpx"
)

var validate = validator.New()

// decodeAndValidate mirrors internal/identity/http's helper of the same
// name (ENGINEERING_STANDARDS.md §3.2).
func decodeAndValidate(w http.ResponseWriter, r *http.Request, dst any) bool {
	if err := json.NewDecoder(r.Body).Decode(dst); err != nil {
		httpx.WriteError(w, http.StatusBadRequest, httpx.APIError{
			Code:    "VALIDATION_FAILED",
			Message: "Request body is not valid JSON.",
		}, nil)
		return false
	}
	if err := validate.Struct(dst); err != nil {
		var fields []httpx.FieldError
		if verrs, ok := err.(validator.ValidationErrors); ok {
			for _, fe := range verrs {
				fields = append(fields, httpx.FieldError{Field: fe.Field(), Issue: "This field is invalid."})
			}
		}
		httpx.WriteError(w, http.StatusUnprocessableEntity, httpx.APIError{
			Code:    "VALIDATION_FAILED",
			Message: "Please check the highlighted fields.",
			Fields:  fields,
		}, nil)
		return false
	}
	return true
}
