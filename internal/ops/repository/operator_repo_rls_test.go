package repository_test

import (
	"context"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/sl-bus/backend/internal/ops/repository"
)

// TestRLSBlocksCrossTenantReads proves the actual security property
// DATABASE_DESIGN.md §5 rests on: with app.current_operator_id set to
// operator A's session variable, a query against ops.operators must
// return only A's row, never B's — even though both rows exist in the
// same table under the same DB role (RLS is FORCEd, see migration
// 000004's comment). A syntax-only check on the migration SQL wouldn't
// catch a missing FORCE, an inverted USING clause, or the "table owner
// bypasses RLS by default" footgun this exact schema hit during
// development.
func TestRLSBlocksCrossTenantReads(t *testing.T) {
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

	applyMigrations(t, ctx, pool)

	var operatorAID, operatorBID string
	if err := pool.QueryRow(ctx, `INSERT INTO ops.operators (name, business_reg_no) VALUES ('Operator A', 'REG-A') RETURNING id`).Scan(&operatorAID); err != nil {
		t.Fatalf("insert operator A: %v", err)
	}
	if err := pool.QueryRow(ctx, `INSERT INTO ops.operators (name, business_reg_no) VALUES ('Operator B', 'REG-B') RETURNING id`).Scan(&operatorBID); err != nil {
		t.Fatalf("insert operator B: %v", err)
	}

	// The query under test MUST run as the non-superuser app role
	// (migration 000005) — Postgres superusers bypass RLS unconditionally,
	// so running this as the same superuser that owns the table would
	// pass even with a completely broken policy.
	appDSN, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}
	appDSN = withAppRoleCredentials(appDSN)
	appPool, err := pgxpool.New(ctx, appDSN)
	if err != nil {
		t.Fatalf("connect as app role: %v", err)
	}
	defer appPool.Close()

	tx, err := appPool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin tx: %v", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `SET LOCAL "app.current_operator_id" = '`+operatorAID+`'`); err != nil {
		t.Fatalf("set current_operator_id: %v", err)
	}
	if _, err := tx.Exec(ctx, `SET LOCAL "app.current_role" = 'operator_admin'`); err != nil {
		t.Fatalf("set current_role: %v", err)
	}

	opRepo := repository.NewOperatorRepository(tx)

	if _, err := opRepo.FindByID(ctx, operatorAID); err != nil {
		t.Errorf("expected operator A to see its own row, got error: %v", err)
	}

	if _, err := opRepo.FindByID(ctx, operatorBID); err == nil {
		t.Errorf("expected RLS to block operator A from reading operator B's row, but the query succeeded")
	}
}

// applyMigrations runs every db/migrations/*.up.sql file in order against
// pool, mirroring what golang-migrate applies in dev/CI.
func applyMigrations(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	migrationsDir := findMigrationsDir(t)
	entries, err := os.ReadDir(migrationsDir)
	if err != nil {
		t.Fatalf("read migrations dir: %v", err)
	}

	var files []string
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".sql" && filepath.Ext(trimSuffix(e.Name(), ".sql")) == ".up" {
			files = append(files, e.Name())
		}
	}
	sortStrings(files)

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

func findMigrationsDir(t *testing.T) string {
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

func trimSuffix(s, suffix string) string {
	if len(s) >= len(suffix) && s[len(s)-len(suffix):] == suffix {
		return s[:len(s)-len(suffix)]
	}
	return s
}

// withAppRoleCredentials swaps the superuser credentials testcontainers
// hands back for the non-superuser slbus_app role's (migration 000005),
// leaving host/port/db/params untouched.
func withAppRoleCredentials(dsn string) string {
	u, err := url.Parse(dsn)
	if err != nil {
		return dsn
	}
	u.User = url.UserPassword("slbus_app", "slbus_app")
	return u.String()
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}
