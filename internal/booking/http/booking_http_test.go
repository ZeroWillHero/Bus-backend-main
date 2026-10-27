package http_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
	return setupAs(t, true)
}

// setupAs builds the router over the slbus_app pool (RLS enforced) or, with
// appRole=false, the superuser pool (RLS bypassed) to prove an authorization
// check does not depend on RLS.
func setupAs(t *testing.T, appRole bool) (http.Handler, *auth.JWTIssuer, string, func(q string, dst interface{}, args ...interface{})) {
	t.Helper()
	pool := testutil.NewTestPool(t) // superuser: seeding and assertions only
	// The service runs as slbus_app, like production, so RLS is enforced. Running
	// as a superuser is what hid the booking path's RLS bugs.
	app := pool
	if appRole {
		app = testutil.NewAppRolePool(t)
	}
	seats := fleetrepo.NewSeatInventoryRepository(app, nil)
	svc := service.NewCheckoutService(
		bookingrepo.NewBookingRepository(app), bookingrepo.NewBookingSeatRepository(app),
		bookingrepo.NewTicketRepository(app), seats, seats, approveAll{},
		bookingrepo.NewOutboxRepository(app),
		bookingrepo.NewConfirmationRepository(app, seats, bookingrepo.NewOutboxRepository(app)), []byte("qr"))
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

type bookingBody struct {
	ID            string  `json:"id"`
	Status        string  `json:"status"`
	TotalAmount   float64 `json:"totalAmount"`
	HoldExpiresAt string  `json:"holdExpiresAt"`
}

// decodeBooking reads the standard {success, data} envelope.
func decodeBooking(t *testing.T, rec *httptest.ResponseRecorder) bookingBody {
	t.Helper()
	var env struct {
		Success bool        `json:"success"`
		Data    bookingBody `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || !env.Success {
		t.Fatalf("not a success envelope (%v): %s", err, rec.Body.String())
	}
	return env.Data
}

func errorCode(t *testing.T, rec *httptest.ResponseRecorder) string {
	t.Helper()
	var env struct {
		Success bool `json:"success"`
		Error   struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil || env.Success {
		t.Fatalf("not an error envelope (%v): %s", err, rec.Body.String())
	}
	return env.Error.Code
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
	var env struct {
		Data struct {
			ID          string  `json:"id"`
			TotalAmount float64 `json:"totalAmount"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &env)
	resp := env.Data
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
	resp := decodeBooking(t, rec)

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

// bookAndConfirm creates and confirms a one-seat booking as a fresh passenger.
func bookAndConfirm(t *testing.T, router http.Handler, issuer *auth.JWTIssuer, scan func(string, interface{}, ...interface{}), trip, seat string) (bookingID, passengerTok string) {
	t.Helper()
	var passenger string
	scan("INSERT INTO identity.users (phone, role) VALUES ('+94' || substr(replace(gen_random_uuid()::text,'-',''),1,12), 'passenger') RETURNING id::text", &passenger)
	passengerTok = tokenFor(t, issuer, passenger, auth.RolePassenger)
	rec := do(t, router, "POST", "/bookings/", passengerTok, map[string]interface{}{
		"tripId": trip, "seats": []map[string]interface{}{{"seatNumber": seat, "passengerName": "P"}}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("create booking: %d %s", rec.Code, rec.Body.String())
	}
	resp := decodeBooking(t, rec)
	if rec := do(t, router, "POST", "/bookings/"+resp.ID+"/confirm", passengerTok, map[string]string{"paymentRef": "pay-" + resp.ID}); rec.Code != http.StatusOK {
		t.Fatalf("confirm booking: %d %s", rec.Code, rec.Body.String())
	}
	return resp.ID, passengerTok
}

// setTrip moves the trip into a given state after booking (bookings require a
// future, scheduled trip; check-in requires departure within -2h..+6h).
func setTrip(t *testing.T, scan func(string, interface{}, ...interface{}), trip, status, departIn string) {
	t.Helper()
	var id string
	scan(`UPDATE fleet.trips SET status = $2, departure_at = now() + $3::interval WHERE id = $1 RETURNING id::text`, &id, trip, status, departIn)
}

func operatorTokenFor(t *testing.T, i *auth.JWTIssuer, userID, operatorID string, role auth.Role) string {
	t.Helper()
	tok, err := i.Issue(userID, role, operatorID, true)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

// operator_admin may board passengers, like operator_staff; nobody else may.
func TestCheckInRoles(t *testing.T) {
	router, issuer, trip, scan := setup(t)
	bookingID, passengerTok := bookAndConfirm(t, router, issuer, scan, trip, "A1")
	setTrip(t, scan, trip, "scheduled", "30 minutes")
	var qr, op string
	scan(`SELECT qr_token FROM booking.tickets WHERE booking_id = $1`, &qr, bookingID)
	scan(`SELECT operator_id::text FROM fleet.trips WHERE id = $1`, &op, trip)
	path := "/tickets/" + qr + "/check-in"

	if rec := do(t, router, "POST", path, passengerTok, nil); rec.Code != http.StatusForbidden {
		t.Errorf("passenger check-in: got %d, want 403", rec.Code)
	}
	admin := operatorTokenFor(t, issuer, "44444444-4444-4444-4444-444444444444", op, auth.RoleOperatorAdmin)
	if rec := do(t, router, "POST", path, admin, nil); rec.Code != http.StatusOK {
		t.Errorf("operator_admin check-in: got %d (%s), want 200", rec.Code, rec.Body.String())
	}
	staff := operatorTokenFor(t, issuer, "55555555-5555-5555-5555-555555555555", op, auth.RoleOperatorStaff)
	if rec := do(t, router, "POST", path, staff, nil); rec.Code != http.StatusConflict {
		t.Errorf("operator_staff re-check-in: got %d, want 409 (already checked in, not 403)", rec.Code)
	}
}

// A ticket can only be checked in by staff of the operator that runs the trip.
// Before: any operator's staff got 200 for any operator's ticket.
func TestCheckInIsScopedToTheTripsOperator(t *testing.T) {
	for name, appRole := range map[string]bool{"rls": true, "no-rls": false} {
		t.Run(name, func(t *testing.T) { checkInScopedToOperator(t, appRole) })
	}
}

func checkInScopedToOperator(t *testing.T, appRole bool) {
	router, issuer, trip, scan := setupAs(t, appRole)
	bookingID, _ := bookAndConfirm(t, router, issuer, scan, trip, "A1")
	setTrip(t, scan, trip, "scheduled", "30 minutes")
	var qr, op, other string
	scan(`SELECT qr_token FROM booking.tickets WHERE booking_id = $1`, &qr, bookingID)
	scan(`SELECT operator_id::text FROM fleet.trips WHERE id = $1`, &op, trip)
	scan(`INSERT INTO ops.operators (name, business_reg_no, status) VALUES ('Other Operator', 'REG-' || gen_random_uuid()::text, 'approved') RETURNING id::text`, &other)
	path := "/tickets/" + qr + "/check-in"

	foreign := operatorTokenFor(t, issuer, "66666666-6666-6666-6666-666666666666", other, auth.RoleOperatorStaff)
	rec := do(t, router, "POST", path, foreign, nil)
	if rec.Code != http.StatusNotFound && rec.Code != http.StatusBadRequest {
		t.Fatalf("another operator's staff: got %d (%s), want 404", rec.Code, rec.Body.String())
	}
	var checkedIn *string
	scan(`SELECT checked_in_at::text FROM booking.tickets WHERE booking_id = $1`, &checkedIn, bookingID)
	if checkedIn != nil {
		t.Fatalf("ticket was checked in by another operator's staff")
	}
	own := operatorTokenFor(t, issuer, "77777777-7777-7777-7777-777777777777", op, auth.RoleOperatorStaff)
	if rec := do(t, router, "POST", path, own, nil); rec.Code != http.StatusOK {
		t.Errorf("own operator's staff: got %d (%s), want 200", rec.Code, rec.Body.String())
	}
}

// The booking lifecycle returns the standard envelope, a UTC hold expiry, the
// passenger's own ticket (with the QR token), a list, and a 409 for cancelling
// a confirmed booking.
func TestBookingLifecycleContract(t *testing.T) {
	router, issuer, trip, scan := setup(t)
	var p string
	scan("INSERT INTO identity.users (phone, role) VALUES ('+94' || substr(replace(gen_random_uuid()::text,'-',''),1,12), 'passenger') RETURNING id::text", &p)
	tok := tokenFor(t, issuer, p, auth.RolePassenger)

	rec := do(t, router, "POST", "/bookings/", tok, map[string]interface{}{"tripId": trip, "seats": []map[string]interface{}{{"seatNumber": "A1", "passengerName": "P"}}})
	if rec.Code != http.StatusCreated {
		t.Fatalf("hold: %d %s", rec.Code, rec.Body.String())
	}
	b := decodeBooking(t, rec)
	exp, err := time.Parse(time.RFC3339, b.HoldExpiresAt)
	if err != nil || !strings.HasSuffix(b.HoldExpiresAt, "Z") {
		t.Fatalf("holdExpiresAt %q must be RFC3339 UTC: %v", b.HoldExpiresAt, err)
	}
	if d := time.Until(exp); d < 8*time.Minute || d > 11*time.Minute {
		t.Errorf("holdExpiresAt is %v from now, want about 10 minutes (timezone bug?)", d)
	}

	if rec := do(t, router, "GET", "/bookings/"+b.ID+"/ticket", tok, nil); rec.Code != http.StatusNotFound || errorCode(t, rec) != "TICKET_NOT_FOUND" {
		t.Errorf("ticket before payment: %d %s", rec.Code, rec.Body.String())
	}
	if rec := do(t, router, "POST", "/bookings/"+b.ID+"/confirm", tok, map[string]string{"paymentRef": "p"}); rec.Code != http.StatusOK || decodeBooking(t, rec).Status != "confirmed" {
		t.Fatalf("confirm: %d %s", rec.Code, rec.Body.String())
	}

	rec = do(t, router, "GET", "/bookings/"+b.ID+"/ticket", tok, nil)
	var tk struct {
		Data struct {
			QRToken   string `json:"qrToken"`
			BookingID string `json:"bookingId"`
			CheckedIn bool   `json:"checkedIn"`
		} `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &tk)
	var want string
	scan(`SELECT qr_token FROM booking.tickets WHERE booking_id = $1`, &want, b.ID)
	if rec.Code != http.StatusOK || tk.Data.QRToken != want || tk.Data.BookingID != b.ID || tk.Data.CheckedIn {
		t.Errorf("ticket: %d %s", rec.Code, rec.Body.String())
	}
	other := tokenFor(t, issuer, "88888888-8888-8888-8888-888888888888", auth.RolePassenger)
	if rec := do(t, router, "GET", "/bookings/"+b.ID+"/ticket", other, nil); rec.Code != http.StatusNotFound {
		t.Errorf("someone else's ticket: got %d, want 404", rec.Code)
	}

	rec = do(t, router, "GET", "/bookings/", tok, nil)
	var list struct {
		Success bool          `json:"success"`
		Data    []bookingBody `json:"data"`
	}
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if rec.Code != http.StatusOK || !list.Success || len(list.Data) != 1 || list.Data[0].ID != b.ID {
		t.Errorf("list: %d %s", rec.Code, rec.Body.String())
	}
	rec = do(t, router, "GET", "/bookings/", other, nil)
	_ = json.Unmarshal(rec.Body.Bytes(), &list)
	if len(list.Data) != 0 {
		t.Errorf("another passenger sees %d bookings, want 0", len(list.Data))
	}

	if rec := do(t, router, "POST", "/bookings/"+b.ID+"/cancel", tok, nil); rec.Code != http.StatusConflict || errorCode(t, rec) != "BOOKING_NOT_CANCELLABLE" {
		t.Errorf("cancel confirmed: %d %s, want 409 BOOKING_NOT_CANCELLABLE", rec.Code, rec.Body.String())
	}
}

// A hold on a trip that has departed (or is inside the booking cutoff) used
// to succeed, since only status='scheduled' was checked. It must be a 409
// BOOKING_TRIP_NOT_BOOKABLE, not a seat-validation error, and hold nothing.
func TestHTTPHoldOnDepartedTripIsNotBookable(t *testing.T) {
	router, issuer, trip, scan := setup(t)
	var passengerID string
	scan("INSERT INTO identity.users (phone, role) VALUES ('+94' || substr(replace(gen_random_uuid()::text,'-',''),1,12), 'passenger') RETURNING id::text", &passengerID)
	passenger := tokenFor(t, issuer, passengerID, auth.RolePassenger)

	for _, departAt := range []string{"-1 hour", "5 minutes"} {
		var ignored string
		scan(`UPDATE fleet.trips SET departure_at = now() + $2::interval WHERE id = $1 RETURNING id::text`, &ignored, trip, departAt)
		rec := do(t, router, "POST", "/bookings/", passenger, map[string]interface{}{
			"tripId": trip,
			"seats":  []map[string]interface{}{{"seatNumber": "A1", "passengerName": "P"}},
		})
		if rec.Code != http.StatusConflict || errorCode(t, rec) != "BOOKING_TRIP_NOT_BOOKABLE" {
			t.Errorf("departure %s: got %d %s, want 409 BOOKING_TRIP_NOT_BOOKABLE", departAt, rec.Code, rec.Body.String())
		}
	}
	var status string
	scan(`SELECT status FROM fleet.trip_seat_inventory WHERE trip_id = $1 AND seat_number = 'A1'`, &status, trip)
	if status != "available" {
		t.Errorf("seat status = %s, want available", status)
	}
}

// Check-in is only valid for a boardable trip near departure; the reason is
// only revealed to the trip's own operator.
func TestCheckInWindowAndTripStatus(t *testing.T) {
	cases := []struct {
		status, departIn string
		wantCode         int
		wantErr          string
	}{
		{"scheduled", "1 day", http.StatusConflict, "TICKET_NOT_VALID_NOW"},
		{"scheduled", "-7 hours", http.StatusConflict, "TICKET_NOT_VALID_NOW"},
		{"cancelled", "30 minutes", http.StatusConflict, "TICKET_TRIP_CANCELLED"},
		{"completed", "-1 hour", http.StatusOK, ""},
		{"completed", "-7 hours", http.StatusConflict, "TICKET_NOT_VALID_NOW"},
		{"scheduled", "90 minutes", http.StatusOK, ""},
	}
	for _, tc := range cases {
		t.Run(tc.status+"_"+tc.departIn, func(t *testing.T) {
			router, issuer, trip, scan := setup(t)
			bookingID, _ := bookAndConfirm(t, router, issuer, scan, trip, "A1")
			setTrip(t, scan, trip, tc.status, tc.departIn)
			var qr, op, other string
			scan(`SELECT qr_token FROM booking.tickets WHERE booking_id = $1`, &qr, bookingID)
			scan(`SELECT operator_id::text FROM fleet.trips WHERE id = $1`, &op, trip)
			scan(`INSERT INTO ops.operators (name, business_reg_no, status) VALUES ('Other', 'REG-' || gen_random_uuid()::text, 'approved') RETURNING id::text`, &other)
			path := "/tickets/" + qr + "/check-in"

			foreign := operatorTokenFor(t, issuer, "66666666-6666-6666-6666-666666666666", other, auth.RoleOperatorStaff)
			// Under RLS the ticket is invisible (400); either way no trip state leaks.
			if rec := do(t, router, "POST", path, foreign, nil); rec.Code != http.StatusNotFound && rec.Code != http.StatusBadRequest {
				t.Errorf("foreign operator: got %d (%s), want 404/400 before any trip state is revealed", rec.Code, rec.Body.String())
			}
			own := operatorTokenFor(t, issuer, "77777777-7777-7777-7777-777777777777", op, auth.RoleOperatorStaff)
			rec := do(t, router, "POST", path, own, nil)
			if rec.Code != tc.wantCode || (tc.wantErr != "" && errorCode(t, rec) != tc.wantErr) {
				t.Errorf("got %d %s, want %d %s", rec.Code, rec.Body.String(), tc.wantCode, tc.wantErr)
			}
		})
	}
}
