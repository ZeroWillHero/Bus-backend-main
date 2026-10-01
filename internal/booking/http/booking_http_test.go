package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/sl-bus/backend/internal/booking/domain"
	bookinghttp "github.com/sl-bus/backend/internal/booking/http"
	bookingrepo "github.com/sl-bus/backend/internal/booking/repository"
	"github.com/sl-bus/backend/internal/booking/service"
	fleetrepo "github.com/sl-bus/backend/internal/fleet/repository"
	"github.com/sl-bus/backend/internal/platform/auth"
	"github.com/sl-bus/backend/internal/platform/testutil"
)

type approveAll struct{}

func (approveAll) Charge(_ context.Context, _ float64, _, ref string, _ map[string]interface{}) (*domain.PaymentResult, error) {
	return &domain.PaymentResult{Gateway: "test", TransactionID: ref, Approved: true}, nil
}
func (approveAll) Refund(context.Context, string, float64, string) error { return nil }

func setup(t *testing.T) (http.Handler, *auth.JWTIssuer, string, func(q string, dst interface{}, args ...interface{})) {
	t.Helper()
	pool := testutil.NewTestPool(t)
	seats := fleetrepo.NewSeatInventoryRepository(pool, nil)
	svc := service.NewCheckoutService(
		bookingrepo.NewBookingRepository(pool), bookingrepo.NewBookingSeatRepository(pool),
		bookingrepo.NewTicketRepository(pool), seats, seats, approveAll{},
		bookingrepo.NewOutboxRepository(pool), []byte("qr"))
	issuer := auth.NewJWTIssuer("http-test-secret")
	router := bookinghttp.NewRouter(bookinghttp.Deps{CheckoutService: svc, Verifier: issuer})

	trip := testutil.SeedTrip(t, pool, testutil.TripSeed{
		AvailableSeats: 2,
		Seats:          []testutil.SeatSeed{{SeatNumber: "A1"}, {SeatNumber: "A2"}},
		Fares:          map[string]float64{"standard": 1000},
	})
	scan := func(q string, dst interface{}, args ...interface{}) {
		t.Helper()
		if err := pool.QueryRow(context.Background(), q, args...).Scan(dst); err != nil {
			t.Fatalf("%s: %v", q, err)
		}
	}
	return router, issuer, trip, scan
}

func do(t *testing.T, h http.Handler, method, path, token string, body interface{}) *httptest.ResponseRecorder {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		_ = json.NewEncoder(&buf).Encode(body)
	}
	req := httptest.NewRequest(method, path, &buf)
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func tokenFor(t *testing.T, i *auth.JWTIssuer, userID string, role auth.Role) string {
	t.Helper()
	tok, err := i.Issue(userID, role, "", true)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// TestBookingEndpointsRequireAuthAndRole: the routes were mounted without
// auth.Middleware, so RequireRole saw no claims and rejected EVERYONE. Now:
// no token -> 401, wrong role -> 403, passenger -> works.
func TestBookingEndpointsRequireAuthAndRole(t *testing.T) {
	router, issuer, trip, _ := setup(t)
	body := map[string]interface{}{"tripId": trip, "seats": []map[string]interface{}{{"seatNumber": "A1", "passengerName": "P"}}}

	if rec := do(t, router, "POST", "/bookings/", "", body); rec.Code != http.StatusUnauthorized {
		t.Errorf("no token: got %d, want 401", rec.Code)
	}
	if rec := do(t, router, "POST", "/bookings/", "garbage", body); rec.Code != http.StatusUnauthorized {
		t.Errorf("bad token: got %d, want 401", rec.Code)
	}
	staff := tokenFor(t, issuer, "11111111-1111-1111-1111-111111111111", auth.RoleOperatorStaff)
	if rec := do(t, router, "POST", "/bookings/", staff, body); rec.Code != http.StatusForbidden {
		t.Errorf("operator staff booking: got %d, want 403", rec.Code)
	}
	passenger := tokenFor(t, issuer, "22222222-2222-2222-2222-222222222222", auth.RolePassenger)
	if rec := do(t, router, "POST", "/bookings/", passenger, body); rec.Code != http.StatusCreated {
		t.Errorf("passenger booking: got %d (%s), want 201", rec.Code, rec.Body.String())
	}
}

// TestHTTPFareTamperingAndIdentitySpoofing: over the wire, a client-supplied
// fareAmount and any attempt to name another passenger are ignored.
func TestHTTPFareTamperingAndIdentitySpoofing(t *testing.T) {
	router, issuer, trip, scan := setup(t)
	var passengerID string
	scan("INSERT INTO identity.users (phone, role) VALUES ('+94' || substr(replace(gen_random_uuid()::text,'-',''),1,12), 'passenger') RETURNING id::text", &passengerID)
	passenger := tokenFor(t, issuer, passengerID, auth.RolePassenger)

	body := map[string]interface{}{
		"tripId":      trip,
		"passengerId": "99999999-9999-9999-9999-999999999999", // spoof attempt: not a field, must be ignored
		"seats": []map[string]interface{}{
			{"seatNumber": "A1", "passengerName": "P", "fareAmount": 0.01},
			{"seatNumber": "A2", "passengerName": "P", "fareAmount": -50},
		},
	}
	rec := do(t, router, "POST", "/bookings/", passenger, body)
	if rec.Code != http.StatusCreated {
		t.Fatalf("got %d: %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ID          string  `json:"id"`
		TotalAmount float64 `json:"totalAmount"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)
	if resp.TotalAmount != 2000 {
		t.Errorf("totalAmount = %v, want 2000", resp.TotalAmount)
	}
	var owner string
	scan(`SELECT passenger_id::text FROM booking.bookings WHERE id=$1`, &owner, resp.ID)
	if owner != passengerID {
		t.Errorf("booking owner = %s, want the JWT subject %s", owner, passengerID)
	}
}

// TestHTTPBookingOwnership: passenger B gets 404 for A's booking on every verb.
func TestHTTPBookingOwnership(t *testing.T) {
	router, issuer, trip, scan := setup(t)
	var a string
	scan("INSERT INTO identity.users (phone, role) VALUES ('+94' || substr(replace(gen_random_uuid()::text,'-',''),1,12), 'passenger') RETURNING id::text", &a)
	tokA := tokenFor(t, issuer, a, auth.RolePassenger)
	tokB := tokenFor(t, issuer, "33333333-3333-3333-3333-333333333333", auth.RolePassenger)

	rec := do(t, router, "POST", "/bookings/", tokA, map[string]interface{}{
		"tripId": trip, "seats": []map[string]interface{}{{"seatNumber": "A1", "passengerName": "P"}}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("setup: %d %s", rec.Code, rec.Body.String())
	}
	var resp struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &resp)

	for _, c := range []struct{ method, path string }{
		{"GET", "/bookings/" + resp.ID},
		{"POST", "/bookings/" + resp.ID + "/confirm"},
		{"POST", "/bookings/" + resp.ID + "/cancel"},
	} {
		got := do(t, router, c.method, c.path, tokB, map[string]string{"paymentRef": "x"})
		if got.Code != http.StatusNotFound && got.Code != http.StatusPaymentRequired {
			t.Errorf("%s %s as another passenger: got %d, want 404", c.method, c.path, got.Code)
		}
		if got.Code == http.StatusPaymentRequired {
			t.Errorf("%s %s leaked the booking's existence/state to another passenger (got 402)", c.method, c.path)
		}
	}
	var status string
	scan(`SELECT status FROM booking.bookings WHERE id=$1`, &status, resp.ID)
	if status != "held" {
		t.Errorf("booking status = %q, want held", status)
	}
	_ = fmt.Sprint
}
