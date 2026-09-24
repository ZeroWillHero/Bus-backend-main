package repository_test

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/sl-bus/backend/internal/fleet/domain"
	"github.com/sl-bus/backend/internal/fleet/repository"
)

// TestFleetRLSBlocksCrossTenantReads extends Phase 1's RLS contract-test
// pattern (internal/ops/repository/operator_repo_rls_test.go) to
// fleet.buses — the same "root table with a direct operator_id column"
// shape as ops.operators. It proves both directions of the property
// Phase 2 depends on: operator A cannot see operator B's bus, and the
// synthetic platform_admin context search deliberately runs under
// (fleet/service/search_service.go) *can* see across both — the one
// place Phase 2 wants the cross-tenant read to work.
func TestFleetRLSBlocksCrossTenantReads(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping testcontainers-based RLS test in -short mode")
	}
	ctx := context.Background()

	pgContainer, err := postgres.Run(ctx, "postgres:18",
		postgres.WithDatabase("slbus_test"),
		postgres.WithUsername("slbus"),
		postgres.WithPassword("slbus"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = pgContainer.Terminate(ctx) })

	dsn, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	defer pool.Close()

	fleetApplyMigrations(t, ctx, pool)

	var operatorAID, operatorBID string
	if err := pool.QueryRow(ctx, `INSERT INTO ops.operators (name, business_reg_no, status) VALUES ('Operator A', 'REG-FLEET-A', 'approved') RETURNING id`).Scan(&operatorAID); err != nil {
		t.Fatalf("insert operator A: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO ops.operators (name, business_reg_no, status) VALUES ('Operator B', 'REG-FLEET-B', 'approved') RETURNING id`).Scan(&operatorBID); err != nil {
		t.Fatalf("insert operator B: %v", err)
	}

	appDSN, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	appDSN = fleetWithAppRoleCredentials(appDSN)
	appPool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("connect as app role: %v", err)
	}
	defer appPool.Close()

	// Create a bus for operator A, scoped to A's own tenant context. This
	// one must actually commit — the read-only checks below need the row
	// to persist across their own separate transactions.
	var busAID string
	withCommittedTx(t, ctx, appPool, func(tx pgx.Tx) {
		setTenantContext(t, ctx, tx, operatorAID, "operator_admin")
		bus, err := repository.NewBusRepository(tx).Create(ctx, &domain.Bus{
			OperatorID:     operatorAID,
			RegistrationNo: "BUS-RLS-TEST",
			BusClass:       "luxury",
			Active:         true,
		})
		if err != nil {
			t.Fatalf("create bus for operator A: %v", err)
		}
		busAID = bus.ID
	})

	// Operator B's own tenant context must not see operator A's bus.
	withTx(t, ctx, appPool, func(tx pgx.Tx) {
		setTenantContext(t, ctx, tx, operatorBID, "operator_admin")
		if _, err := repository.NewBusRepository(tx).FindByID(ctx, busAID); err == nil {
			t.Errorf("expected RLS to block operator B from reading operator A's bus, but the query succeeded")
		}
	})

	// The synthetic platform_admin context (search_service.go's exact
	// pattern) must be able to see it — the deliberate cross-tenant read.
	withTx(t, ctx, appPool, func(tx pgx.Tx) {
		setTenantContext(t, ctx, tx, "00000000-0000-0000-0000-000000000000", "platform_admin")
		if _, err := repository.NewBusRepository(tx).FindByID(ctx, busAID); err != nil {
			t.Errorf("expected the platform_admin context to see operator A's bus, got error: %v", err)
		}
	})
}

// withTx runs fn inside a transaction that is always rolled back — for
// the read-only checks, where we don't want any side effect to persist.
func withTx(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx)) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	fn(tx)
}

// withCommittedTx runs fn inside a transaction that commits on success —
// for the one write (bus creation) later reads in this test depend on.
func withCommittedTx(t *testing.T, ctx context.Context, pool *pgxpool.Pool, fn func(tx pgx.Tx)) {
	t.Helper()
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	fn(tx)
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit tx: %v", err)
	}
}

func setTenantContext(t *testing.T, ctx context.Context, tx pgx.Tx, operatorID, role string) {
	t.Helper()
	if _, err := tx.Exec(ctx, `SET LOCAL "app.current_operator_id" = '`+operatorID+`'`); err != nil {
		t.Fatalf("set current_operator_id: %v", err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL "app.current_role" = '`+role+`'`); err != nil {
		t.Fatalf("set current_role: %v", err)
	}
}

// fleetApplyMigrations/fleetWithAppRoleCredentials mirror
// internal/ops/repository/operator_repo_rls_test.go's helpers of the same
// shape — duplicated (not shared) because each directory is its own Go
// package, even sharing the external test package name.
func fleetApplyMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	migrationsDir := fleetFindMigrationsDir(t)
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" && filepath.Ext(fleetTrimSuffix(e.Name(), ".sql")) == ".up" {
			files = append(files, e.Name())
		}
	}
	fleetSortStrings(files)

	for _, name := range files {
		sql, err := os.ReadFile(filepath.Join(migrationsDir, name))
		if err != nil {
			t.Fatalf("read migration %s: %v", name, err)
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			t.Fatalf("apply migration %s: %v", name, err)
		}
	}
}

func fleetFindMigrationsDir(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	for {
		candidate := filepath.Join(dir, "db", "migrations")
		if info, err := os.Stat(candidate); err == nil && info.IsDir() {
			return candidate
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("could not locate db/migrations above %s", dir)
		}
		dir = parent
	}
}

func fleetTrimSuffix(s, suffix string) string {
	if len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix {
		return s[:len(s)-len(suffix)]
	}
	return s
}

func fleetSortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

func fleetWithAppRoleCredentials(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	u.User = url.UserPassword("slbus_app", "slbus_app")
	return u.String()
}
