package insights_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/sl-bus/backend/internal/insights"
	"github.com/sl-bus/backend/internal/platform/auth"
)

func newService(f *fixture) *insights.Service {
	s := insights.NewService(f.pool)
	insights.SetNow(s, func() time.Time { return fixedNow })
	return s
}

func adminOf(operatorID string) *auth.Claims {
	return &auth.Claims{Role: auth.RoleOperatorAdmin, OperatorID: operatorID}
}

func TestDashboard(t *testing.T) {
	f := seedFixture(t)
	d, err := newService(f).Dashboard(context.Background(), adminOf(f.OperatorID), "me", "")
	if err != nil {
		t.Fatal(err)
	}

	if d.Fleet.TotalBuses != 2 || d.Fleet.ActiveBuses != 2 || d.Fleet.BusesWithoutSeatMap != 1 {
		t.Errorf("buses = %+v, want total 2 active 2 withoutSeatMap 1", d.Fleet)
	}
	if d.Fleet.Routes != 2 || d.Fleet.RoutesWithoutScheduledTrip != 1 {
		t.Errorf("routes = %+v, want 2 with 1 lacking trips", d.Fleet)
	}
	if d.Fleet.UpcomingTrips != 2 { // today 10:00Z and tomorrow; the cancelled one doesn't count
		t.Errorf("upcomingTrips = %d, want 2", d.Fleet.UpcomingTrips)
	}

	today := d.Today
	if today.Date != "2026-10-02" {
		t.Errorf("date = %q", today.Date)
	}
	if today.Trips != 1 || today.CancelledTrip != 1 {
		t.Errorf("trips/cancelled = %d/%d, want 1/1", today.Trips, today.CancelledTrip)
	}
	if today.SeatsSold != 1 || today.SeatsHeld != 1 || today.SeatCapacity != 4 {
		t.Errorf("seats sold/held/capacity = %d/%d/%d, want 1/1/4 (cancelled trip excluded, held not sold)",
			today.SeatsSold, today.SeatsHeld, today.SeatCapacity)
	}
	if today.NextDeparture == nil || today.NextDeparture.TripID != f.TripToday || today.NextDeparture.RouteName != "Colombo → Kandy" {
		t.Errorf("nextDeparture = %+v, want today's trip on Colombo → Kandy", today.NextDeparture)
	}

	types := map[string]insights.Alert{}
	for _, a := range d.Alerts {
		types[a.Type] = a
	}
	low, ok := types[insights.AlertLowOccupancy]
	if !ok || low.TripID == nil || *low.TripID != f.TripToday || low.Occupancy == nil || *low.Occupancy != 0.25 {
		t.Errorf("low_occupancy alert = %+v", low)
	}
	if low.Message != "Colombo → Kandy departs 15:30 and is 25% full" {
		t.Errorf("message = %q (departure must render in Colombo time)", low.Message)
	}
	if a := types[insights.AlertMissingSeatMap]; a.BusID == nil || *a.BusID != f.BareBusID {
		t.Errorf("missing_seat_map alert = %+v", a)
	}
	if a := types[insights.AlertRouteWithoutTrips]; a.RouteID == nil || *a.RouteID != f.EmptyRoute {
		t.Errorf("route_without_trips alert = %+v", a)
	}
}

func TestDashboardTenancy(t *testing.T) {
	f := seedFixture(t)
	svc := newService(f)
	ctx := context.Background()

	if _, err := svc.Dashboard(ctx, adminOf(f.OtherOperatorID), f.OperatorID, ""); !errors.Is(err, insights.ErrForbidden) {
		t.Errorf("other operator's admin: err = %v, want ErrForbidden", err)
	}
	// Another operator's own dashboard must not contain this operator's data.
	other, err := svc.Dashboard(ctx, adminOf(f.OtherOperatorID), "me", "")
	if err != nil {
		t.Fatal(err)
	}
	if other.Fleet.Routes != 1 || other.Today.SeatCapacity != 0 {
		t.Errorf("other operator saw foreign data: %+v / %+v", other.Fleet, other.Today)
	}

	platform := &auth.Claims{Role: auth.RolePlatformAdmin}
	if _, err := svc.Dashboard(ctx, platform, "me", ""); !errors.Is(err, insights.ErrOperatorMissing) {
		t.Errorf("platform_admin with me: err = %v, want ErrOperatorMissing", err)
	}
	if _, err := svc.Dashboard(ctx, platform, "not-a-uuid", ""); !errors.Is(err, insights.ErrInvalidInput) {
		t.Errorf("platform_admin bad id: err = %v, want ErrInvalidInput", err)
	}
	d, err := svc.Dashboard(ctx, platform, f.OperatorID, "")
	if err != nil || d.Fleet.Routes != 2 {
		t.Errorf("platform_admin for operator: %v, %+v", err, d)
	}
	if _, err := svc.Dashboard(ctx, adminOf(f.OperatorID), "me", "Mars/Olympus"); !errors.Is(err, insights.ErrInvalidInput) {
		t.Errorf("bad tz: err = %v, want ErrInvalidInput", err)
	}
}

func TestSeriesBucketsByColomboDayAndZeroFills(t *testing.T) {
	f := seedFixture(t)
	s, err := newService(f).Series(context.Background(), adminOf(f.OperatorID), "me", "2026-09-30", "2026-10-02", "day", "")
	if err != nil {
		t.Fatal(err)
	}
	if s.Currency != "LKR" || s.Granularity != "day" || len(s.Points) != 3 {
		t.Fatalf("series = %+v, want 3 zero-filled day points", s)
	}
	want := []insights.SeriesPoint{
		{Date: "2026-09-30", GrossRevenue: "0.00", Refunds: "0.00", NetRevenue: "0.00"},
		{Date: "2026-10-01", GrossRevenue: "500.00", Refunds: "0.00", NetRevenue: "500.00", Bookings: 1, SeatsSold: 1},
		// 3600 + the 20:00Z-on-1-Oct booking (01:30 on the 2nd in Colombo); refund of 500.
		{Date: "2026-10-02", GrossRevenue: "4600.00", Refunds: "500.00", NetRevenue: "4100.00", Bookings: 2, Cancellations: 1, SeatsSold: 3},
	}
	for i, w := range want {
		if s.Points[i] != w {
			t.Errorf("point[%d] = %+v, want %+v", i, s.Points[i], w)
		}
	}
}

func TestSeriesValidation(t *testing.T) {
	f := seedFixture(t)
	svc := newService(f)
	ctx := context.Background()
	admin := adminOf(f.OperatorID)

	if _, err := svc.Series(ctx, admin, "me", "2026-01-01", "2026-12-31", "day", ""); !errors.Is(err, insights.ErrRangeTooLarge) {
		t.Errorf("long range: err = %v, want ErrRangeTooLarge", err)
	}
	if _, err := svc.Series(ctx, admin, "me", "2026-01-01", "2026-04-02", "day", ""); err != nil { // exactly 92 days
		t.Errorf("92 days should be allowed: %v", err)
	}
	for name, args := range map[string][4]string{
		"missing to":  {"2026-10-01", "", "day", ""},
		"bad date":    {"01/10/2026", "2026-10-02", "day", ""},
		"reversed":    {"2026-10-02", "2026-10-01", "day", ""},
		"granularity": {"2026-10-01", "2026-10-02", "week", ""},
	} {
		if _, err := svc.Series(ctx, admin, "me", args[0], args[1], args[2], args[3]); !errors.Is(err, insights.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
}

func TestSummary(t *testing.T) {
	f := seedFixture(t)
	svc := newService(f)
	ctx := context.Background()
	admin := adminOf(f.OperatorID)

	s, err := svc.Summary(ctx, admin, "me", insights.SummaryParams{
		From: "2026-10-01", To: "2026-10-02", CompareFrom: "2026-10-01", CompareTo: "2026-10-01"})
	if err != nil {
		t.Fatal(err)
	}
	if s.GrossRevenue != "5100.00" || s.Refunds != "500.00" || s.NetRevenue != "4600.00" {
		t.Errorf("money = gross %s refunds %s net %s", s.GrossRevenue, s.Refunds, s.NetRevenue)
	}
	if s.Bookings != 3 || s.Cancellations != 1 || s.SeatsSold != 4 {
		t.Errorf("counts = bookings %d cancellations %d seats %d", s.Bookings, s.Cancellations, s.SeatsSold)
	}
	if s.CancellationRate != 0.3333 {
		t.Errorf("cancellationRate = %v, want 0.3333", s.CancellationRate)
	}
	if s.AverageOccupancy != 0.25 { // today's trip 1/4; the cancelled and tomorrow's trips are excluded
		t.Errorf("averageOccupancy = %v, want 0.25", s.AverageOccupancy)
	}
	if len(s.TopRoutes) != 1 || s.TopRoutes[0].Name != "Colombo → Kandy" || s.TopRoutes[0].GrossRevenue != "5100.00" || s.TopRoutes[0].Bookings != 3 {
		t.Errorf("topRoutes = %+v (must not include the other operator's 777000)", s.TopRoutes)
	}
	if s.Comparison == nil || s.Comparison.GrossRevenue != "500.00" || s.Comparison.DeltaPercent == nil || *s.Comparison.DeltaPercent != 920 {
		t.Errorf("comparison = %+v, want 500.00 and +920%%", s.Comparison)
	}

	// A comparison period with zero revenue yields null, never infinity.
	z, err := svc.Summary(ctx, admin, "me", insights.SummaryParams{
		From: "2026-10-01", To: "2026-10-02", CompareFrom: "2026-09-01", CompareTo: "2026-09-02"})
	if err != nil {
		t.Fatal(err)
	}
	if z.Comparison == nil || z.Comparison.GrossRevenue != "0.00" || z.Comparison.DeltaPercent != nil {
		t.Errorf("zero comparison = %+v, want 0.00 and null delta", z.Comparison)
	}

	// No comparison requested -> null.
	n, err := svc.Summary(ctx, admin, "me", insights.SummaryParams{From: "2026-10-01", To: "2026-10-02"})
	if err != nil || n.Comparison != nil {
		t.Errorf("no comparison: %v, %+v", err, n.Comparison)
	}
}

func TestSummaryMonthToDate(t *testing.T) {
	f := seedFixture(t)
	s, err := newService(f).Summary(context.Background(), adminOf(f.OperatorID), "me", insights.SummaryParams{Period: "month_to_date"})
	if err != nil {
		t.Fatal(err)
	}
	// "Now" is 2 Oct in Colombo: this month 1–2 Oct against 1–2 Sep.
	if s.Period.From != "2026-10-01" || s.Period.To != "2026-10-02" {
		t.Errorf("period = %+v", s.Period)
	}
	if s.Comparison == nil || s.Comparison.Period.From != "2026-09-01" || s.Comparison.Period.To != "2026-09-02" {
		t.Errorf("comparison = %+v, want 1–2 Sep", s.Comparison)
	}
	if s.GrossRevenue != "5100.00" {
		t.Errorf("gross = %s", s.GrossRevenue)
	}
}

func TestSummaryValidation(t *testing.T) {
	f := seedFixture(t)
	svc := newService(f)
	admin := adminOf(f.OperatorID)
	for name, p := range map[string]insights.SummaryParams{
		"nothing":           {},
		"period and range":  {Period: "month_to_date", From: "2026-10-01", To: "2026-10-02"},
		"unknown period":    {Period: "year"},
		"half a comparison": {From: "2026-10-01", To: "2026-10-02", CompareFrom: "2026-09-01"},
	} {
		if _, err := svc.Summary(context.Background(), admin, "me", p); !errors.Is(err, insights.ErrInvalidInput) {
			t.Errorf("%s: err = %v, want ErrInvalidInput", name, err)
		}
	}
}

func TestActivity(t *testing.T) {
	f := seedFixture(t)
	svc := newService(f)
	ctx := context.Background()

	items, next, err := svc.Activity(ctx, adminOf(f.OperatorID), "me", 100, "")
	if err != nil {
		t.Fatal(err)
	}
	if next != "" {
		t.Errorf("nextCursor = %q with everything on one page", next)
	}
	counts := map[string]int{}
	for _, it := range items {
		counts[it.Type]++
	}
	want := map[string]int{
		insights.EventBookingConfirmed: 3, // confirmed, night owl, refunded (held never confirmed)
		insights.EventBookingCancelled: 1,
		insights.EventTripCreated:      3,
		insights.EventTripCancelled:    1,
		insights.EventCheckIn:          1,
	}
	for typ, n := range want {
		if counts[typ] != n {
			t.Errorf("%s events = %d, want %d (all: %v)", typ, counts[typ], n, counts)
		}
	}
	for i := 1; i < len(items); i++ {
		if items[i].OccurredAt.After(items[i-1].OccurredAt) {
			t.Fatalf("not newest-first at %d", i)
		}
	}
	for _, it := range items {
		if it.Type == insights.EventBookingConfirmed && it.BookingID != nil && *it.BookingID == f.BookingConfirmed {
			if it.Amount == nil || *it.Amount != "3600.00" || it.Currency == nil || *it.Currency != "LKR" ||
				it.Seats == nil || *it.Seats != 2 || it.RouteName == nil || *it.RouteName != "Colombo → Kandy" {
				t.Errorf("booking_confirmed item = %+v", it)
			}
		}
	}

	// Staff: same events but no money.
	staff := &auth.Claims{Role: auth.RoleOperatorStaff, OperatorID: f.OperatorID}
	staffItems, _, err := svc.Activity(ctx, staff, "me", 100, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range staffItems {
		if it.Amount != nil || it.Currency != nil {
			t.Errorf("staff saw an amount: %+v", it)
		}
	}
}

func TestActivityPagination(t *testing.T) {
	f := seedFixture(t)
	svc := newService(f)
	admin := adminOf(f.OperatorID)

	all, _, err := svc.Activity(context.Background(), admin, "me", 100, "")
	if err != nil {
		t.Fatal(err)
	}
	var paged []insights.ActivityItem
	cursor := ""
	for pages := 0; pages < 50; pages++ {
		items, next, err := svc.Activity(context.Background(), admin, "me", 3, cursor)
		if err != nil {
			t.Fatal(err)
		}
		if len(items) > 3 {
			t.Fatalf("page of %d with limit 3", len(items))
		}
		paged = append(paged, items...)
		if next == "" {
			break
		}
		cursor = next
	}
	if len(paged) != len(all) {
		t.Fatalf("paged %d events, one page had %d", len(paged), len(all))
	}
	for i := range all {
		if paged[i].ID != all[i].ID {
			t.Fatalf("event %d differs across paging: %s vs %s", i, paged[i].ID, all[i].ID)
		}
	}

	if _, _, err := svc.Activity(context.Background(), admin, "me", 101, ""); !errors.Is(err, insights.ErrInvalidInput) {
		t.Errorf("limit 101: err = %v", err)
	}
	if _, _, err := svc.Activity(context.Background(), admin, "me", 10, "garbage"); !errors.Is(err, insights.ErrInvalidInput) {
		t.Errorf("bad cursor: err = %v", err)
	}
}
