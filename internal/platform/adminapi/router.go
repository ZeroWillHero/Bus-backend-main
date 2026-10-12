// Package adminapi serves platform-operations endpoints for platform_admin
// under /v1/admin — things about the platform itself rather than any one
// bounded context, starting with the SMS provider balance.
package adminapi

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"sort"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/sl-bus/backend/internal/platform/auth"
	"github.com/sl-bus/backend/internal/platform/httpx"
	"github.com/sl-bus/backend/internal/platform/ratelimit"
	"github.com/sl-bus/backend/internal/platform/sms"
)

// BalanceGetter is satisfied by *sms.BalanceService.
type BalanceGetter interface {
	Get(ctx context.Context, forceRefresh bool) (sms.BalanceSnapshot, bool, error)
}

type Deps struct {
	Verifier   auth.Verifier
	SMSBalance BalanceGetter
	Policy     sms.BalancePolicy
	// Limiter throttles forced refreshes per admin; nil disables refresh.
	Limiter ratelimit.Counter
}

// RefreshInterval is the minimum gap between forced live checks per admin;
// a refresh inside it is served from cache instead of calling SMSlenz.
const RefreshInterval = 15 * time.Second

func NewRouter(deps Deps) chi.Router {
	h := &handlers{deps: deps}
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(auth.Middleware(deps.Verifier))
		r.Use(auth.RequireRole(auth.RolePlatformAdmin))
		r.Get("/sms/balance", h.smsBalance)
	})
	return r
}

type handlers struct {
	deps Deps
}

type senderBalance struct {
	SenderID   string  `json:"senderId"`
	BalanceLKR float64 `json:"balanceLkr"`
}

type smsBalanceResponse struct {
	Provider               string          `json:"provider"`
	Status                 string          `json:"status"`
	MainBalanceLKR         float64         `json:"mainBalanceLkr"`
	EstimatedSMSRemaining  int             `json:"estimatedSmsRemaining"`
	UnitPriceLKR           float64         `json:"unitPriceLkr"`
	LowBalanceThresholdLKR float64         `json:"lowBalanceThresholdLkr"`
	IsLow                  bool            `json:"isLow"`
	SenderBalances         []senderBalance `json:"senderBalances"`
	CheckedAt              time.Time       `json:"checkedAt"`
	Stale                  bool            `json:"stale"`
}

func (h *handlers) smsBalance(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	force := false
	if r.URL.Query().Get("refresh") == "true" && h.deps.Limiter != nil {
		claims, _ := auth.FromContext(ctx)
		n, err := h.deps.Limiter.Incr(ctx, "sms_balance_refresh:"+claims.Subject, RefreshInterval)
		force = err == nil && n == 1
	}

	snap, stale, err := h.deps.SMSBalance.Get(ctx, force)
	if err != nil {
		if errors.Is(err, sms.ErrBalanceUnavailable) {
			slog.Warn("sms balance unavailable", "error", err)
			httpx.WriteError(w, http.StatusServiceUnavailable, httpx.APIError{
				Code:    "PLATFORM_SMS_PROVIDER_UNAVAILABLE",
				Message: "The SMS provider balance can't be checked right now.",
			}, nil)
			return
		}
		slog.Error("sms balance lookup failed", "error", err)
		httpx.WriteError(w, http.StatusInternalServerError, httpx.APIError{Code: "PLATFORM_INTERNAL_ERROR", Message: "Something went wrong. Please try again."}, nil)
		return
	}

	senders := make([]senderBalance, 0, len(snap.SenderBalances))
	for id, bal := range snap.SenderBalances {
		senders = append(senders, senderBalance{SenderID: id, BalanceLKR: bal})
	}
	sort.Slice(senders, func(i, j int) bool { return senders[i].SenderID < senders[j].SenderID })

	p := h.deps.Policy
	httpx.WriteSuccess(w, http.StatusOK, smsBalanceResponse{
		Provider:               "smslenz",
		Status:                 snap.Status,
		MainBalanceLKR:         snap.MainBalanceLKR,
		EstimatedSMSRemaining:  p.EstimatedMessages(snap),
		UnitPriceLKR:           p.UnitPriceLKR,
		LowBalanceThresholdLKR: p.LowThresholdLKR,
		IsLow:                  p.IsLow(snap),
		SenderBalances:         senders,
		CheckedAt:              snap.CheckedAt,
		Stale:                  stale,
	}, nil)
}
