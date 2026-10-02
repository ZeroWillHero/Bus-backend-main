package http_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	fleethttp "github.com/sl-bus/backend/internal/fleet/http"
	fleetservice "github.com/sl-bus/backend/internal/fleet/service"
	opsservice "github.com/sl-bus/backend/internal/ops/service"
	"github.com/sl-bus/backend/internal/platform/auth"
	"github.com/sl-bus/backend/internal/platform/testutil"
)

type fixture struct {
	router                    http.Handler
	issuer                    *auth.JWTIssuer
	operatorID, otherOperator string
	tripID, routeID, busID    string
}

func setup(t *testing.T) *fixture {
	t.Helper()
	pool := testutil.NewTestPool(t)
	f := &fixture{issuer: auth.NewJWTIssuer("fleet-http-test")}

	// 3 seats: one booked, one held (far-future expiry), one free.
	f.tripID = testutil.SeedTrip(t, pool, testutil.TripSeed{
		AvailableSeats: 1,
		Seats: []testutil.SeatSeed{
			{SeatNumber: "A1", Status: "booked"},
			{SeatNumber: "A2", Status: "held", HoldExpiresAt: farFuture},
			{SeatNumber: "A3", Status: "available"},
		},
	})
	if err := pool.QueryRow(context.Background(),
		`SELECT operator_id::text, route_id::text, bus_id::text FROM fleet.trips WHERE id = $1`, f.tripID).
		Scan(&f.operatorID, &f.routeID, &f.busID); err != nil {
		t.Fatal(err)
	}
	f.otherOperator = testutil.SeedOperator(t, pool)

	approval := opsservice.NewApprovalService(pool)
	f.router = fleethttp.NewRouter(fleethttp.Deps{
		Bus:       fleetservice.NewBusService(pool, approval),
		SeatMap:   fleetservice.NewSeatMapService(pool),
		Route:     fleetservice.NewRouteService(pool, approval),
		Trip:      fleetservice.NewTripService(pool, approval),
		ReadModel: fleetservice.NewReadModelService(pool),
		Verifier:  f.issuer,
	})
	return f
}

func (f *fixture) do(t *testing.T, method, path string, role auth.Role, operatorID string, body string) (int, map[string]any) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	tok, err := f.issuer.Issue("11111111-1111-1111-1111-111111111111", role, operatorID, true)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	rec := httptest.NewRecorder()
	f.router.ServeHTTP(rec, req)
	var out map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &out)
	return rec.Code, out
}

func firstItem(t *testing.T, body map[string]any) map[string]any {
	t.Helper()
	items, _ := body["data"].([]any)
	if len(items) != 1 {
		t.Fatalf("data has %d items, want 1: %v", len(items), body)
	}
	return items[0].(map[string]any)
}

// R6: boarding staff must be able to read fleet data but never change it.
func TestStaffCanReadButNotWriteFleet(t *testing.T) {
	f := setup(t)
	reads := []string{"/buses", "/buses/" + f.busID, "/buses/" + f.busID + "/seat-map",
		"/routes", "/routes/" + f.routeID, "/routes/" + f.routeID + "/buses",
		"/trips", "/trips/" + f.tripID}
	for _, path := range reads {
		if status, body := f.do(t, "GET", path, auth.RoleOperatorStaff, f.operatorID, ""); status != 200 {
			t.Errorf("staff GET %s = %d, want 200 (%v)", path, status, body["error"])
		}
	}
	writes := []struct{ method, path string }{
		{"POST", "/buses"}, {"PATCH", "/buses/" + f.busID}, {"PUT", "/buses/" + f.busID + "/seat-map"},
		{"POST", "/routes"}, {"PATCH", "/routes/" + f.routeID},
		{"POST", "/trips"}, {"POST", "/trips/" + f.tripID + "/cancel"},
		{"PATCH", "/trips/" + f.tripID}, {"PATCH", "/trips/" + f.tripID + "/fares"},
	}
	for _, w := range writes {
		if status, _ := f.do(t, w.method, w.path, auth.RoleOperatorStaff, f.operatorID, "{}"); status != 403 {
			t.Errorf("staff %s %s = %d, want 403", w.method, w.path, status)
		}
	}
	for _, role := range []auth.Role{auth.RolePassenger, auth.RoleFinanceAdmin} {
		if status, _ := f.do(t, "GET", "/trips", role, f.operatorID, ""); status != 403 {
			t.Errorf("%s GET /trips = %d, want 403", role, status)
		}
	}
	// Admin still reads.
	if status, _ := f.do(t, "GET", "/trips", auth.RoleOperatorAdmin, f.operatorID, ""); status != 200 {
		t.Errorf("admin GET /trips = %d", status)
	}
}

// R5: trips carry seat counts and embedded route/bus, buses and routes carry
// their derived counts — so the app needs no per-row follow-up calls.
func TestFleetResponsesCarryDerivedFields(t *testing.T) {
	f := setup(t)

	_, body := f.do(t, "GET", "/trips", auth.RoleOperatorAdmin, f.operatorID, "")
	trip := firstItem(t, body)
	if trip["currency"] != "LKR" || trip["totalSeats"] != 3.0 || trip["bookedSeats"] != 1.0 || trip["heldSeats"] != 1.0 {
		t.Errorf("trip seat fields = %v", trip)
	}
	route, _ := trip["route"].(map[string]any)
	bus, _ := trip["bus"].(map[string]any)
	if route["id"] != f.routeID || route["origin"] != "Colombo" || route["destination"] != "Kandy" {
		t.Errorf("trip.route = %v", route)
	}
	if bus["id"] != f.busID || bus["registrationNo"] == "" || bus["busClass"] != "standard" {
		t.Errorf("trip.bus = %v", bus)
	}
	if _, ok := trip["availableSeats"]; !ok {
		t.Error("availableSeats must stay for existing clients")
	}

	_, one := f.do(t, "GET", "/trips/"+f.tripID, auth.RoleOperatorAdmin, f.operatorID, "")
	if got := one["data"].(map[string]any)["bookedSeats"]; got != 1.0 {
		t.Errorf("single trip bookedSeats = %v", got)
	}

	_, body = f.do(t, "GET", "/buses", auth.RoleOperatorAdmin, f.operatorID, "")
	if b := firstItem(t, body); b["totalSeats"] != 3.0 || b["hasSeatMap"] != true {
		t.Errorf("bus = %v", b)
	}

	_, body = f.do(t, "GET", "/routes", auth.RoleOperatorAdmin, f.operatorID, "")
	if r := firstItem(t, body); r["scheduledTripCount"] != 1.0 || r["nextDepartureAt"] == nil {
		t.Errorf("route = %v", r)
	}

	_, body = f.do(t, "GET", "/routes/"+f.routeID+"/buses", auth.RoleOperatorAdmin, f.operatorID, "")
	rb := firstItem(t, body)
	if rb["upcomingTrips"] != 1.0 || rb["nextDepartureAt"] == nil || rb["bus"].(map[string]any)["id"] != f.busID {
		t.Errorf("route buses = %v", rb)
	}

	if status, _ := f.do(t, "GET", "/routes/00000000-0000-0000-0000-000000000000/buses", auth.RoleOperatorAdmin, f.operatorID, ""); status != 404 {
		t.Errorf("unknown route buses = %d, want 404", status)
	}
	if status, _ := f.do(t, "GET", "/routes/not-a-uuid/buses", auth.RoleOperatorAdmin, f.operatorID, ""); status != 404 {
		t.Errorf("malformed route id = %d, want 404", status)
	}

	// Another operator sees none of it.
	_, body = f.do(t, "GET", "/trips", auth.RoleOperatorStaff, f.otherOperator, "")
	if items, _ := body["data"].([]any); len(items) != 0 {
		t.Errorf("other operator's trips = %v", items)
	}
}
