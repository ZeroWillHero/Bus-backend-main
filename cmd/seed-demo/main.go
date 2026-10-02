// Command seed-demo fills a database with demo data (one operator, a month
// of trips and bookings) so the dashboard and analytics have numbers to show.
//
// It is deliberately cautious, because the usual DATABASE_URL in .env points
// at a real hosted database:
//   - it never reads .env — the target must be given explicitly via
//     --database-url or SEED_DATABASE_URL;
//   - it refuses any non-local host unless --allow-remote is also given;
//   - it only creates/deletes rows hanging off operators whose
//     business_reg_no starts with "DEMO-SEED-".
//
// Re-running replaces the previous demo data. Use --reset to remove it.
package main

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"net"
	"os"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/demoseed"
)

func main() {
	dsn := flag.String("database-url", os.Getenv("SEED_DATABASE_URL"), "target database (or SEED_DATABASE_URL); .env is NOT read")
	allowRemote := flag.Bool("allow-remote", false, "permit a non-local database host")
	reset := flag.Bool("reset", false, "only delete the demo data, then exit")
	password := flag.String("password", "", "password for the demo admin/staff logins (default: random, printed once)")
	seed := flag.Int64("seed", 42, "random seed, for reproducible data")
	flag.Parse()

	if err := run(*dsn, *allowRemote, *reset, *password, *seed); err != nil {
		fmt.Fprintln(os.Stderr, "seed-demo:", err)
		os.Exit(1)
	}
}

func run(dsn string, allowRemote, reset bool, password string, seed int64) error {
	if dsn == "" {
		return fmt.Errorf("no target database: pass --database-url or set SEED_DATABASE_URL (.env is deliberately not read)")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return fmt.Errorf("parse database url: %w", err)
	}
	if host := cfg.ConnConfig.Host; !isLocal(host) && !allowRemote {
		return fmt.Errorf("refusing to write demo data to non-local host %q; if that really is the intended database, re-run with --allow-remote", host)
	}
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return err
	}
	defer pool.Close()

	fmt.Printf("target: %s@%s/%s\n", cfg.ConnConfig.User, cfg.ConnConfig.Host, cfg.ConnConfig.Database)
	if reset {
		if err := demoseed.Reset(ctx, pool); err != nil {
			return err
		}
		fmt.Println("demo data removed")
		return nil
	}

	if password == "" {
		b := make([]byte, 12)
		if _, err := rand.Read(b); err != nil {
			return err
		}
		password = base64.RawURLEncoding.EncodeToString(b)
	}
	sum, err := demoseed.Seed(ctx, pool, demoseed.Options{Password: password, Seed: seed})
	if err != nil {
		return err
	}
	fmt.Printf("seeded operator %s\n  %d buses, %d routes, %d trips\n  %d confirmed bookings (%d tickets, %d checked in), %d payouts\n",
		sum.OperatorID, sum.Buses, sum.Routes, sum.Trips, sum.Bookings, sum.Tickets, sum.CheckedIn, sum.Payouts)
	fmt.Printf("\nlogins (same password):\n  admin  %s  (MFA enrolment on first login)\n  staff  %s\n  password: %s\n", demoseed.AdminEmail, demoseed.StaffEmail, password)
	return nil
}

func isLocal(host string) bool {
	if host == "" || strings.HasPrefix(host, "/") || host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
