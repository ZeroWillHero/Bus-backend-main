// Package domain holds the Fleet & Scheduling context's pure business
// logic (ARCHITECTURE.md §3.2, DOMAIN_MODEL.md §2.3) — no HTTP or SQL
// imports, so it's unit-testable without a database.
package domain

import (
	"context"
	"time"
)

// Bus is an entity referenced by Trip (FK), not a sub-aggregate of it —
// it has its own lifecycle, created/edited independently via FR-FLT-1.
type Bus struct {
	ID             string
	OperatorID     string
	RegistrationNo string
	BusClass       string
	Amenities      map[string]any
	Active         bool
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

type BusRepository interface {
	Create(ctx context.Context, b *Bus) (*Bus, error)
	FindByID(ctx context.Context, id string) (*Bus, error)
	ListByOperator(ctx context.Context, operatorID, cursor string, limit int) (buses []*Bus, nextCursor string, err error)
	Update(ctx context.Context, b *Bus) (*Bus, error)
}

// OperatorApprovalChecker is the cross-context port fleet depends on
// (implemented by ops, mirroring identity's OperatorStatusChecker from
// Phase 1) — the context map's "Operator must be Approved" upstream rule:
// no trip (or bus, or route) exists for an unapproved operator.
type OperatorApprovalChecker interface {
	IsOperatorApproved(ctx context.Context, operatorID string) (bool, error)
}
