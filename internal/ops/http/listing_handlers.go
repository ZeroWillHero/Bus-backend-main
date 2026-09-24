package http

import (
	"net/http"
	"strconv"
	"time"

	"github.com/sl-bus/backend/internal/ops/domain"
	"github.com/sl-bus/backend/internal/platform/httpx"
)

type operatorResponse struct {
	ID                 string     `json:"id"`
	Name               string     `json:"name"`
	BusinessRegNo      string     `json:"businessRegNo"`
	Status             string     `json:"status"`
	ContactEmail       string     `json:"contactEmail,omitempty"`
	ContactPhone       string     `json:"contactPhone,omitempty"`
	RegistrationDocURL string     `json:"registrationDocUrl,omitempty"`
	CreatedAt          time.Time  `json:"createdAt"`
	ApprovedAt         *time.Time `json:"approvedAt,omitempty"`
}

func toOperatorResponse(o *domain.Operator) operatorResponse {
	return operatorResponse{
		ID:                 o.ID,
		Name:               o.Name,
		BusinessRegNo:      o.BusinessRegNo,
		Status:             string(o.Status),
		ContactEmail:       o.ContactEmail,
		ContactPhone:       o.ContactPhone,
		RegistrationDocURL: o.RegistrationDocURL,
		CreatedAt:          o.CreatedAt,
		ApprovedAt:         o.ApprovedAt,
	}
}

// listOperators handles GET /v1/operators?status=pending&cursor=&limit= —
// platform_admin only (REQUIREMENTS_COVERAGE.md gap #1's approval
// screen), cursor-paginated per ENGINEERING_STANDARDS.md §4.
func (h *handlers) listOperators(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	if status == "" {
		status = string(domain.StatusPending)
	}
	cursor := r.URL.Query().Get("cursor")
	limit := 0
	if l := r.URL.Query().Get("limit"); l != "" {
		if parsed, err := strconv.Atoi(l); err == nil {
			limit = parsed
		}
	}

	operators, nextCursor, err := h.deps.Listing.ListByStatus(r.Context(), domain.OperatorStatus(status), cursor, limit)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	out := make([]operatorResponse, 0, len(operators))
	for _, o := range operators {
		out = append(out, toOperatorResponse(o))
	}
	hasMore := nextCursor != ""
	httpx.WriteSuccess(w, http.StatusOK, out, &httpx.Meta{NextCursor: nextCursor, HasMore: &hasMore})
}
