package demoseed_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/sl-bus/backend/internal/demoseed"
	"github.com/sl-bus/backend/internal/insights"
	"github.com/sl-bus/backend/internal/platform/auth"
	"github.com/sl-bus/backend/internal/platform/testutil"
)

func count(t *testing.T, q string, args ...any) int {
	t.Helper()
	pool := testutil.NewTestPool(t)
	var n int
	if err := pool.QueryRow(context.Background(), q, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// The seeded data must actually light up every stats endpoint, replace
// itself cleanly on a re-run, and never touch another operator's rows.
func TestSeedFeedsTheStatsEndpoints(t *testing.T) {
	ctx := context.Background()
	pool := testutil.NewTestPool(t)

	bystander := testutil.SeedOperator(t, pool) // a "real" operator that must survive
	opts := demoseed.Options{Password: "demo-password-123", Seed: 7}
	sum, err := demoseed.Seed(ctx, pool, opts)
	if err != nil {
		t.Fatal(err)
	}
	if sum.Trips == 0 || sum.Bookings == 0 || sum.Payouts != 3 {
		t.Fatalf("summary = %+v", sum)
	}

	svc := insights.NewService(pool)
	admin := &auth.Claims{Role: auth.RoleOperatorAdmin, OperatorID: sum.OperatorID}

	d, err := svc.Dashboard(ctx, admin, "me", "")
	if err != nil {
		t.Fatal(err)
	}
	if d.Fleet.TotalBuses != 6 || d.Fleet.ActiveBuses != 5 || d.Fleet.Routes != 4 {
		t.Errorf("fleet = %+v", d.Fleet)
	}
	if d.Fleet.UpcomingTrips == 0 || d.Today.Trips == 0 || d.Today.SeatCapacity == 0 {
		t.Errorf("dashboard has no trips today/upcoming: %+v / %+v", d.Fleet, d.Today)
	}
	alerts := map[string]bool{}
	for _, a := range d.Alerts {
		alerts[a.Type] = true
	}
	for _, want := range []string{insights.AlertMissingSeatMap, insights.AlertRouteWithoutTrips, insights.AlertLowOccupancy} {
		if !alerts[want] {
			t.Errorf("no %s alert in demo dashboard: %+v", want, d.Alerts)
		}
	}

	to := time.Now().In(time.FixedZone("LK", 5*3600+1800)).Format("2006-01-02")
	from := time.Now().AddDate(0, 0, -29).Format("2006-01-02")
	s, err := svc.Series(ctx, admin, "me", from, to, "day", "")
	if err != nil {
		t.Fatal(err)
	}
	nonZero := 0
	for _, p := range s.Points {
		if v, _ := strconv.ParseFloat(p.GrossRevenue, 64); v > 0 {
			nonZero++
		}
	}
	if len(s.Points) != 30 || nonZero < 25 {
		t.Errorf("series has %d points, %d with revenue; want a month of busy days", len(s.Points), nonZero)
	}

	m, err := svc.Summary(ctx, admin, "me", insights.SummaryParams{Period: "month_to_date"})
	if err != nil {
		t.Fatal(err)
	}
	if m.Bookings == 0 || m.AverageOccupancy == 0 || len(m.TopRoutes) == 0 {
		t.Errorf("month summary is empty: %+v", m)
	}

	// Walk the whole feed: the newest 100 events are all bookings, the
	// older trip_created / payout events only show up deeper.
	types := map[string]bool{}
	cursor := ""
	for page := 0; page < 200; page++ {
		items, next, err := svc.Activity(ctx, admin, "me", 100, cursor)
		if err != nil {
			t.Fatal(err)
		}
		for _, it := range items {
			types[it.Type] = true
		}
		if next == "" {
			break
		}
		cursor = next
	}
	for _, want := range []string{insights.EventBookingConfirmed, insights.EventCheckIn, insights.EventTripCreated, insights.EventPayoutExecuted} {
		if !types[want] {
			t.Errorf("activity feed has no %s event (types: %v)", want, types)
		}
	}

	// Re-running replaces rather than duplicates.
	before := count(t, `SELECT count(*) FROM fleet.trips WHERE operator_id = $1`, sum.OperatorID)
	sum2, err := demoseed.Seed(ctx, pool, opts)
	if err != nil {
		t.Fatal(err)
	}
	if got := count(t, `SELECT count(*) FROM ops.operators WHERE business_reg_no = $1`, demoseed.MarkerPrefix+"0001"); got != 1 {
		t.Errorf("%d demo operators after re-run, want 1", got)
	}
	if got := count(t, `SELECT count(*) FROM fleet.trips WHERE operator_id = $1`, sum2.OperatorID); got != before {
		t.Errorf("trips after re-run = %d, want %d", got, before)
	}
	if got := count(t, `SELECT count(*) FROM fleet.trips WHERE operator_id = $1`, sum.OperatorID); got != 0 {
		t.Errorf("old demo operator still has %d trips", got)
	}

	// Reset removes it all and leaves bystanders alone.
	if err := demoseed.Reset(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if got := count(t, `SELECT count(*) FROM ops.operators WHERE business_reg_no LIKE $1`, demoseed.MarkerPrefix+"%"); got != 0 {
		t.Errorf("%d demo operators after reset", got)
	}
	if got := count(t, `SELECT count(*) FROM identity.users WHERE email = $1 OR phone LIKE '+9470000%'`, demoseed.AdminEmail); got != 0 {
		t.Errorf("%d demo users after reset", got)
	}
	if got := count(t, `SELECT count(*) FROM ops.operators WHERE id = $1`, bystander); got != 1 {
		t.Error("reset deleted an operator that is not demo data")
	}
}
