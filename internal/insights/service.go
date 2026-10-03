// Package insights serves the operator-facing read models behind the
// dashboard, analytics and activity endpoints (operator app requests
// R1–R4). It is read-only: every query runs inside a tenant-scoped
// transaction (platform/db.WithTenantContext) *and* filters by operator_id
// explicitly, so a platform_admin — whom RLS lets see every tenant — still
// gets exactly one operator's numbers per request.
package insights

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/sl-bus/backend/internal/platform/auth"
	platformdb "github.com/sl-bus/backend/internal/platform/db"
)

// Currency is the platform's single settlement currency. bookings.currency
// exists per row, but nothing in the system creates anything but LKR.
const Currency = "LKR"

// DefaultTZ is the calendar the operators live in; "today" and daily buckets
// are cut on this zone's midnight, not UTC's.
const DefaultTZ = "Asia/Colombo"

// MaxRangeDays bounds the analytics date range (R2).
const MaxRangeDays = 92

var (
	ErrForbidden       = errors.New("insights: caller may not read this operator")
	ErrOperatorMissing = errors.New("insights: operator id is required")
	ErrInvalidInput    = errors.New("insights: invalid input")
	ErrRangeTooLarge   = errors.New("insights: date range too large")
)

// InputError carries a user-facing validation message.
type InputError struct{ Msg string }

func (e *InputError) Error() string   { return e.Msg }
func (e *InputError) Is(t error) bool { return t == ErrInvalidInput }

func invalid(format string, args ...any) error {
	return &InputError{Msg: fmt.Sprintf(format, args...)}
}

type Service struct {
	pool *pgxpool.Pool
	now  func() time.Time
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, now: time.Now}
}

// resolveOperator maps the {id} path value ("me" or a UUID) to the operator
// whose data is read, enforcing tenancy: operator-side roles may only read
// their own operator; platform_admin may read any but must name it.
func resolveOperator(claims *auth.Claims, id string) (string, error) {
	if claims.Role == auth.RolePlatformAdmin {
		if id == "" || id == "me" {
			return "", ErrOperatorMissing
		}
		if _, err := uuid.Parse(id); err != nil {
			return "", invalid("operator id must be a UUID")
		}
		return id, nil
	}
	if claims.OperatorID == "" {
		return "", ErrForbidden
	}
	if id == "me" || id == claims.OperatorID {
		return claims.OperatorID, nil
	}
	return "", ErrForbidden
}

// withTx runs fn in a tenant-scoped transaction for the caller.
func (s *Service) withTx(ctx context.Context, claims *auth.Claims, fn func(ctx context.Context, tx pgx.Tx) error) error {
	return platformdb.WithTenantContext(ctx, s.pool, claims, fn)
}

func loadZone(name string) (*time.Location, error) {
	if name == "" {
		name = DefaultTZ
	}
	loc, err := time.LoadLocation(name)
	if err != nil {
		return nil, invalid("tz must be a valid IANA time zone name")
	}
	return loc, nil
}

// dayStart returns the instant local midnight of d begins in loc.
func dayStart(d time.Time, loc *time.Location) time.Time {
	return time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, loc)
}

const dateLayout = "2006-01-02"

func parseDate(field, v string) (time.Time, error) {
	t, err := time.Parse(dateLayout, v)
	if err != nil {
		return time.Time{}, invalid("%s must be a date in YYYY-MM-DD format", field)
	}
	return t, nil
}

// dateRange is an inclusive local-date range. Start/End are the UTC instants
// bounding it ([Start, End)) for timestamp comparisons.
type dateRange struct {
	From, To   time.Time // dates (UTC midnight, only Y-M-D meaningful)
	Start, End time.Time // instants in loc → compared against timestamptz
}

func newDateRange(from, to time.Time, loc *time.Location) dateRange {
	return dateRange{
		From:  from,
		To:    to,
		Start: dayStart(from, loc),
		End:   dayStart(to.AddDate(0, 0, 1), loc),
	}
}

func (r dateRange) days() int { return int(r.To.Sub(r.From).Hours()/24) + 1 }
