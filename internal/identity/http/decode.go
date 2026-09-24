package http

import (
	"encoding/json"
	"net/http"

	"github.com/go-playground/validator/v10"

	"github.com/sl-bus/backend/internal/platform/httpx"
)

var validate = validator.New()

// decodeAndValidate decodes the request body into dst and runs struct-tag
// validation, writing a VALIDATION_FAILED envelope on either failure
// (ENGINEERING_STANDARDS.md §3.2). Returns false if the caller should
// stop (a response has already been written).
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
				fields = append(fields, httpx.FieldError{
					Field: fe.Field(),
					Issue: fieldIssue(fe),
				})
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

func fieldIssue(fe validator.FieldError) string {
	switch fe.Tag() {
	case "required":
		return "This field is required."
	case "email":
		return "Must be a valid email address."
	case "min":
		return "Value is too short."
	default:
		return "This field is invalid."
	}
}
