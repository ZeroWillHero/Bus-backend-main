package service

import (
	"context"
	"testing"
	"time"

	"github.com/sl-bus/backend/internal/platform/testutil"
)

// Suspending an operator ends its staff's sessions (refresh tokens) and
// leaves other users' sessions alone. Runs the service as slbus_app.
func TestSuspendRevokesOperatorStaffRefreshTokens(t *testing.T) {
	ctx := context.Background()
	super := testutil.NewTestPool(t)
	svc := NewApprovalService(testutil.NewAppRolePool(t))

	operatorID := testutil.SeedOperator(t, super)
	var staffID string
	if err := super.QueryRow(ctx, `
		INSERT INTO identity.users (email, role, operator_id)
		VALUES ('staff-' || gen_random_uuid() || '@example.com', 'operator_staff', $1)
		RETURNING id`, operatorID).Scan(&staffID); err != nil {
		t.Fatalf("seed staff: %v", err)
	}
	passengerID := testutil.SeedPassenger(t, super)
	for _, uid := range []string{staffID, staffID, passengerID} {
		if _, err := super.Exec(ctx, `
			INSERT INTO identity.refresh_tokens (user_id, token_hash, expires_at)
			VALUES ($1, gen_random_uuid()::text, $2)`, uid, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("seed refresh token: %v", err)
		}
	}

	if _, err := svc.Suspend(ctx, operatorID); err != nil {
		t.Fatalf("Suspend: %v", err)
	}

	active := func(uid string) int {
		var n int
		if err := super.QueryRow(ctx, `SELECT count(*) FROM identity.refresh_tokens WHERE user_id = $1 AND revoked_at IS NULL`, uid).Scan(&n); err != nil {
			t.Fatalf("count: %v", err)
		}
		return n
	}
	if n := active(staffID); n != 0 {
		t.Errorf("staff has %d active refresh tokens after suspension, want 0", n)
	}
	if n := active(passengerID); n != 1 {
		t.Errorf("unrelated passenger has %d active refresh tokens, want 1", n)
	}
}
