package service

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/identity/domain"
	"github.com/sl-bus/backend/internal/identity/repository"
	"github.com/sl-bus/backend/internal/platform/auth"
	"github.com/sl-bus/backend/internal/platform/testutil"
)

type fixedOperatorStatus struct{ suspended bool }

func (f fixedOperatorStatus) IsOperatorSuspended(context.Context, string) (bool, error) {
	return f.suspended, nil
}

type refreshFixture struct {
	pool    *pgxpool.Pool
	users   *repository.UserRepository
	refresh *repository.RefreshTokenRepository
	tokens  *TokenIssuer
}

func newRefreshFixture(t *testing.T) *refreshFixture {
	pool := testutil.NewTestPool(t)
	refresh := repository.NewRefreshTokenRepository(pool)
	return &refreshFixture{
		pool:    pool,
		users:   repository.NewUserRepository(pool),
		refresh: refresh,
		tokens:  NewTokenIssuer(auth.NewJWTIssuer("test-signing-key"), refresh),
	}
}

func (f *refreshFixture) service(status OperatorStatusChecker) *TokenService {
	return NewTokenService(f.refresh, f.users, status, auth.NewJWTIssuer("test-signing-key"), f.tokens)
}

func (f *refreshFixture) login(t *testing.T, userID, device string) string {
	t.Helper()
	user, err := f.users.FindByID(context.Background(), userID)
	if err != nil {
		t.Fatalf("FindByID: %v", err)
	}
	pair, err := f.tokens.IssuePair(context.Background(), user, device)
	if err != nil {
		t.Fatalf("IssuePair: %v", err)
	}
	return pair.RefreshToken
}

// barrierRefreshRepo holds every FindByHash until n callers have read
// the token, so all of them see it unrevoked and race on the revoke —
// the window a plain "read, check, revoke" lets through.
type barrierRefreshRepo struct {
	*repository.RefreshTokenRepository
	barrier *sync.WaitGroup
}

func (r barrierRefreshRepo) FindByHash(ctx context.Context, hash string) (*domain.RefreshToken, error) {
	rec, err := r.RefreshTokenRepository.FindByHash(ctx, hash)
	r.barrier.Done()
	r.barrier.Wait()
	return rec, err
}

func TestRefreshConcurrentRotationHasExactlyOneWinner(t *testing.T) {
	f := newRefreshFixture(t)
	userID := testutil.SeedPassenger(t, f.pool)
	token := f.login(t, userID, "phone")

	const n = 10
	var barrier sync.WaitGroup
	barrier.Add(n)
	svc := NewTokenService(barrierRefreshRepo{f.refresh, &barrier}, f.users, nil, auth.NewJWTIssuer("test-signing-key"), f.tokens)
	var wg sync.WaitGroup
	errs := make([]error, n)
	start := make(chan struct{})
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			_, errs[i] = svc.RefreshTokens(context.Background(), token, "phone")
		}(i)
	}
	close(start)
	wg.Wait()

	wins := 0
	for _, err := range errs {
		switch err {
		case nil:
			wins++
		case domain.ErrTokenInvalid:
		default:
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if wins != 1 {
		t.Fatalf("%d concurrent refreshes succeeded, want exactly 1", wins)
	}
}

func TestRefreshReuseOfRotatedTokenRevokesAllSessions(t *testing.T) {
	ctx := context.Background()
	f := newRefreshFixture(t)
	svc := f.service(nil)
	userID := testutil.SeedPassenger(t, f.pool)
	phone := f.login(t, userID, "phone")
	laptop := f.login(t, userID, "laptop")

	rotated, err := svc.RefreshTokens(ctx, phone, "phone")
	if err != nil {
		t.Fatalf("first refresh: %v", err)
	}
	if _, err := svc.RefreshTokens(ctx, phone, "attacker"); err != domain.ErrTokenInvalid {
		t.Fatalf("reused token error = %v, want ErrTokenInvalid", err)
	}

	active, err := f.refresh.ListActiveForUser(ctx, userID)
	if err != nil {
		t.Fatalf("ListActiveForUser: %v", err)
	}
	if len(active) != 0 {
		t.Fatalf("%d sessions still active after reuse, want 0", len(active))
	}
	for name, tok := range map[string]string{"rotated": rotated.RefreshToken, "laptop": laptop} {
		if _, err := svc.RefreshTokens(ctx, tok, "x"); err != domain.ErrTokenInvalid {
			t.Errorf("%s token after reuse: error = %v, want ErrTokenInvalid", name, err)
		}
	}
}

func TestRefreshRechecksTheAccount(t *testing.T) {
	ctx := context.Background()
	f := newRefreshFixture(t)

	t.Run("suspended operator", func(t *testing.T) {
		operatorID := testutil.SeedOperator(t, f.pool)
		var userID string
		if err := f.pool.QueryRow(ctx, `
			INSERT INTO identity.users (email, role, operator_id)
			VALUES ('staff-' || gen_random_uuid() || '@example.com', 'operator_staff', $1)
			RETURNING id`, operatorID).Scan(&userID); err != nil {
			t.Fatalf("seed staff: %v", err)
		}
		token := f.login(t, userID, "tablet")

		if _, err := f.service(fixedOperatorStatus{suspended: true}).RefreshTokens(ctx, token, "tablet"); err != ErrOperatorSuspended {
			t.Fatalf("error = %v, want ErrOperatorSuspended", err)
		}
		// Refused without consuming the token: it still works once reinstated.
		if _, err := f.service(fixedOperatorStatus{}).RefreshTokens(ctx, token, "tablet"); err != nil {
			t.Fatalf("refresh after reinstatement: %v", err)
		}
	})

	t.Run("locked account", func(t *testing.T) {
		userID := testutil.SeedPassenger(t, f.pool)
		token := f.login(t, userID, "phone")
		if err := f.users.SetLockedUntil(ctx, userID, time.Now().Add(time.Hour)); err != nil {
			t.Fatalf("SetLockedUntil: %v", err)
		}
		if _, err := f.service(nil).RefreshTokens(ctx, token, "phone"); err != domain.ErrAccountLocked {
			t.Fatalf("error = %v, want ErrAccountLocked", err)
		}
	})
}

func TestClaimMFAStepIsMonotonic(t *testing.T) {
	ctx := context.Background()
	f := newRefreshFixture(t)
	userID := testutil.SeedPassenger(t, f.pool)

	for _, c := range []struct {
		step int64
		want bool
	}{{100, true}, {100, false}, {99, false}, {101, true}} {
		got, err := f.users.ClaimMFAStep(ctx, userID, c.step)
		if err != nil {
			t.Fatalf("ClaimMFAStep(%d): %v", c.step, err)
		}
		if got != c.want {
			t.Errorf("ClaimMFAStep(%d) = %v, want %v", c.step, got, c.want)
		}
	}
}
