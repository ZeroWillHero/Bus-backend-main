// Package domain holds the Operator & Multi-Tenant Management context's
// pure business logic (ARCHITECTURE.md §3.2, SRS.md FR-OPS-1..6).
package domain

import (
	"context"
	"time"
)

type OperatorStatus string

const (
	StatusPending   OperatorStatus = "pending"
	StatusApproved  OperatorStatus = "approved"
	StatusRejected  OperatorStatus = "rejected"
	StatusSuspended OperatorStatus = "suspended"
)

// Operator is the tenant-root aggregate (FR-OPS-1..6).
type Operator struct {
	ID                 string
	Name               string
	BusinessRegNo      string
	Status             OperatorStatus
	CommissionRate     *float64
	ContactEmail       string
	ContactPhone       string
	RegistrationDocURL string
	CreatedAt          time.Time
	ApprovedAt         *time.Time
}

// CanTransition encodes the allowed status verb graph (FR-OPS-3: approve,
// reject, suspend, reactivate). Pure and unit-testable without a DB.
func CanTransition(from, to OperatorStatus) bool {
	switch from {
	case StatusPending:
		return to == StatusApproved || to == StatusRejected
	case StatusApproved:
		return to == StatusSuspended
	case StatusSuspended:
		return to == StatusApproved // "reactivate"
	default:
		return false
	}
}

type OperatorRepository interface {
	Create(ctx context.Context, o *Operator) (*Operator, error)
	FindByID(ctx context.Context, id string) (*Operator, error)
	FindByBusinessRegNo(ctx context.Context, regNo string) (*Operator, error)
	ListByStatus(ctx context.Context, status OperatorStatus, cursor string, limit int) (operators []*Operator, nextCursor string, err error)
	UpdateStatus(ctx context.Context, id string, status OperatorStatus) (*Operator, error)
}
