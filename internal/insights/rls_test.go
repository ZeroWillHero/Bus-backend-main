package insights_test

import (
	"context"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/insights"
	"github.com/sl-bus/backend/internal/platform/auth"
)

// appPool connects to the same database but as slbus_app — the non-superuser
// role the API really runs as — so Row-Level Security applies. The default
// test pool is a superuser and would hide a query RLS silently empties.
func appPool(t *testing.T, f *fixture) *pgxpool.Pool {
	t.Helper()
	cfg := f.pool.Config().Copy()
	cfg.AfterConnect = func(ctx context.Context, c *pgx.Conn) error {
		_, err := c.Exec(ctx, "SET ROLE slbus_app")
		return err
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// TestEndpointsUnderRLS runs every read model as slbus_app and checks the
// same numbers come back for each role that may read them.
func TestEndpointsUnderRLS(t *testing.T) {
	f := seedFixture(t)
	svc := insights.NewService(appPool(t, f))
	insights.SetNow(svc, func() time.Time { return fixedNow })
	ctx := context.Background()

	roles := map[string]*auth.Claims{
		"operator_admin": {Role: auth.RoleOperatorAdmin, OperatorID: f.OperatorID},
		"operator_staff": {Role: auth.RoleOperatorStaff, OperatorID: f.OperatorID},
		"finance_admin":  {Role: auth.RoleFinanceAdmin, OperatorID: f.OperatorID},
		"platform_admin": {Role: auth.RolePlatformAdmin},
	}
	for name, claims := range roles {
		t.Run(name, func(t *testing.T) {
			id := "me"
			if claims.Role == auth.RolePlatformAdmin {
				id = f.OperatorID
			}
			if claims.Role != auth.RoleFinanceAdmin {
				d, err := svc.Dashboard(ctx, claims, id, "")
				if err != nil {
					t.Fatalf("dashboard: %v", err)
				}
				if d.Fleet.Routes != 2 || d.Today.SeatCapacity != 4 || d.Today.SeatsSold != 1 {
					t.Errorf("dashboard under RLS = %+v / %+v", d.Fleet, d.Today)
				}
				items, _, err := svc.Activity(ctx, claims, id, 100, "")
				if err != nil {
					t.Fatalf("activity: %v", err)
				}
				if len(items) < 9 {
					t.Errorf("activity under RLS returned %d items, want the full feed", len(items))
				}
			}
			if claims.Role != auth.RoleOperatorStaff {
				s, err := svc.Summary(ctx, claims, id, insights.SummaryParams{From: "2026-10-01", To: "2026-10-02"})
				if err != nil {
					t.Fatalf("summary: %v", err)
				}
				if s.GrossRevenue != "5100.00" || s.Bookings != 3 || len(s.TopRoutes) != 1 {
					t.Errorf("summary under RLS = %+v", s)
				}
				series, err := svc.Series(ctx, claims, id, "2026-10-01", "2026-10-02", "day", "")
				if err != nil {
					t.Fatalf("series: %v", err)
				}
				if series.Points[1].GrossRevenue != "4600.00" {
					t.Errorf("series under RLS = %+v", series.Points)
				}
			}
		})
	}
}
