package adminapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/sl-bus/backend/internal/platform/auth"
	"github.com/sl-bus/backend/internal/platform/ratelimit"
	"github.com/sl-bus/backend/internal/platform/sms"
)

type fakeBalance struct {
	forced []bool
	snap   sms.BalanceSnapshot
	stale  bool
	err    error
}

func (f *fakeBalance) Get(_ context.Context, force bool) (sms.BalanceSnapshot, bool, error) {
	f.forced = append(f.forced, force)
	return f.snap, f.stale, f.err
}

var jwtIssuer = auth.NewJWTIssuer("test-signing-key")

func get(t *testing.T, h http.Handler, role auth.Role, url string) (int, map[string]any, string) {
	t.Helper()
	token, _ := jwtIssuer.Issue("11111111-1111-1111-1111-111111111111", role, "", true)
	req := httptest.NewRequest(http.MethodGet, url, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	var env map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	data, _ := env["data"].(map[string]any)
	return rec.Code, data, rec.Body.String()
}

func newRouter(b *fakeBalance) http.Handler {
	return NewRouter(Deps{
		Verifier:   jwtIssuer,
		SMSBalance: b,
		Policy:     sms.BalancePolicy{UnitPriceLKR: 0.59, LowThresholdLKR: 500},
		Limiter:    ratelimit.NewMemoryCounter(),
	})
}

func TestSMSBalanceForPlatformAdmin(t *testing.T) {
	b := &fakeBalance{snap: sms.BalanceSnapshot{
		Status: "active", MainBalanceLKR: 924.45,
		SenderBalances: map[string]float64{"SL Bus": 300, "A": 1},
		CheckedAt:      time.Date(2026, 10, 12, 9, 0, 0, 0, time.UTC),
	}}
	status, data, body := get(t, newRouter(b), auth.RolePlatformAdmin, "/sms/balance")
	if status != http.StatusOK {
		t.Fatalf("status = %d %s", status, body)
	}
	if data["mainBalanceLkr"] != 924.45 || data["estimatedSmsRemaining"] != float64(1566) || data["isLow"] != false || data["stale"] != false || data["provider"] != "smslenz" {
		t.Fatalf("data = %v", data)
	}
	senders := data["senderBalances"].([]any)
	if len(senders) != 2 || senders[0].(map[string]any)["senderId"] != "A" {
		t.Fatalf("senderBalances = %v, want sorted by id", senders)
	}
	for _, secret := range []string{"api_key", "apiKey", "user_id"} {
		if strings.Contains(body, secret) {
			t.Fatalf("response mentions %q: %s", secret, body)
		}
	}
}

func TestSMSBalanceIsPlatformAdminOnly(t *testing.T) {
	for _, role := range []auth.Role{auth.RoleOperatorAdmin, auth.RoleFinanceAdmin, auth.RoleSupport, auth.RoleOperatorStaff, auth.RolePassenger} {
		if status, _, _ := get(t, newRouter(&fakeBalance{}), role, "/sms/balance"); status != http.StatusForbidden {
			t.Errorf("%s: status = %d, want 403", role, status)
		}
	}
	rec := httptest.NewRecorder()
	newRouter(&fakeBalance{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/sms/balance", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", rec.Code)
	}
}

func TestSMSBalanceRefreshIsThrottled(t *testing.T) {
	b := &fakeBalance{snap: sms.BalanceSnapshot{Status: "active"}}
	h := newRouter(b)
	get(t, h, auth.RolePlatformAdmin, "/sms/balance?refresh=true")
	get(t, h, auth.RolePlatformAdmin, "/sms/balance?refresh=true")
	get(t, h, auth.RolePlatformAdmin, "/sms/balance")
	if want := []bool{true, false, false}; len(b.forced) != 3 || b.forced[0] != want[0] || b.forced[1] != want[1] || b.forced[2] != want[2] {
		t.Fatalf("forced = %v, want %v", b.forced, want)
	}
}

func TestSMSBalanceUnavailable(t *testing.T) {
	b := &fakeBalance{err: sms.ErrBalanceUnavailable}
	status, _, body := get(t, newRouter(b), auth.RolePlatformAdmin, "/sms/balance")
	if status != http.StatusServiceUnavailable || !strings.Contains(body, "PLATFORM_SMS_PROVIDER_UNAVAILABLE") {
		t.Fatalf("status = %d %s", status, body)
	}
}

func TestSMSBalanceStaleAndLow(t *testing.T) {
	b := &fakeBalance{stale: true, snap: sms.BalanceSnapshot{Status: "active", MainBalanceLKR: 120}}
	_, data, _ := get(t, newRouter(b), auth.RolePlatformAdmin, "/sms/balance")
	if data["stale"] != true || data["isLow"] != true {
		t.Fatalf("data = %v, want stale and low", data)
	}
}
