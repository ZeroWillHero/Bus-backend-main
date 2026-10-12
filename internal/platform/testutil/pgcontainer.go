package testutil

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/testcontainers/testcontainers-go/wait"

	"github.com/sl-bus/backend/internal/platform/db"
)

var (
	sharedOnce sync.Once
	sharedDSN  string
	sharedErr  error
)

// NewTestPool returns a pool against a fully migrated Postgres, as the
// superuser (so tests can seed freely; RLS is bypassed, use SET ROLE slbus_app
// to exercise it).
//
//   - TEST_DATABASE_URL set: use that database. It MUST be a throwaway database
//     that is already migrated; tests insert data and never clean it up. Never
//     point this at staging or production.
//   - otherwise: start ONE postgres:18 container per test binary, apply every
//     db/migrations/*.up.sql, and share it across tests (each test gets its own
//     pool and creates its own uniquely-keyed rows). Needs Docker.
func NewTestPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping database test in -short mode")
	}

	dsn := os.Getenv("TEST_DATABASE_URL")
	if dsn != "" && os.Getenv("ALLOW_DESTRUCTIVE_TESTS") != "1" {
		t.Fatalf("TEST_DATABASE_URL is set, but these tests insert data and DELETE FROM platform.outbox_events. " +
			"Point it ONLY at a throwaway, already-migrated database (e.g. a separate Supabase project, never your real one) " +
			"and set ALLOW_DESTRUCTIVE_TESTS=1 to confirm. Unset TEST_DATABASE_URL to use a disposable Docker Postgres instead.")
	}
	if dsn == "" {
		sharedOnce.Do(func() { sharedDSN, sharedErr = startSharedContainer() })
		if sharedErr != nil {
			t.Fatalf("start shared test postgres (is Docker running?): %v", sharedErr)
		}
		dsn = sharedDSN
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := db.NewPool(ctx, dsn)
	if err != nil {
		t.Fatalf("failed to create test pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

// NewAppRolePool returns a pool connected as slbus_app, the non-superuser role
// the API runs as in production, so Row-Level Security is enforced. Use it for
// the code under test and NewTestPool (superuser) to seed data.
func NewAppRolePool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	super := NewTestPool(t)
	cfg := super.Config().Copy()
	cfg.ConnConfig.User = "slbus_app"
	cfg.ConnConfig.Password = "slbus_app"
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Fatalf("open slbus_app pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("connect as slbus_app: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func startSharedContainer() (string, error) {
	ctx := context.Background()
	c, err := postgres.Run(ctx, "postgres:18",
		postgres.WithDatabase("slbus_test"),
		postgres.WithUsername("slbus"),
		postgres.WithPassword("slbus"),
		testcontainers.WithWaitStrategy(
			wait.ForLog("database system is ready to accept connections").WithOccurrence(2),
		),
	)
	if err != nil {
		return "", err
	}
	// The container is reaped by testcontainers' Ryuk when the test process exits.
	dsn, err := c.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return "", err
	}

	pool, err := db.NewPool(ctx, dsn)
	if err != nil {
		return "", err
	}
	defer pool.Close()

	_, thisFile, _, _ := runtime.Caller(0)
	dir := filepath.Join(filepath.Dir(thisFile), "..", "..", "..", "db", "migrations")
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", err
	}
	var files []string
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".up.sql") {
			files = append(files, e.Name())
		}
	}
	sort.Strings(files)
	for _, f := range files {
		sql, err := os.ReadFile(filepath.Join(dir, f))
		if err != nil {
			return "", err
		}
		if _, err := pool.Exec(ctx, string(sql)); err != nil {
			return "", &migrationError{file: f, err: err}
		}
	}
	return dsn, nil
}

type migrationError struct {
	file string
	err  error
}

func (e *migrationError) Error() string { return "apply migration " + e.file + ": " + e.err.Error() }
