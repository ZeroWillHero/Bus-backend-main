package domain

import "errors"

var (
	ErrBusNotFound                 = errors.New("fleet: bus not found")
	ErrRouteNotFound               = errors.New("fleet: route not found")
	ErrTripNotFound                = errors.New("fleet: trip not found")
	ErrDuplicateRegistrationNo     = errors.New("fleet: bus registration number already in use")
	ErrOperatorNotApproved         = errors.New("fleet: operator is not approved")
	ErrOperatorMismatch            = errors.New("fleet: bus and route must belong to the same operator")
	ErrInvalidTripStatusTransition = errors.New("fleet: invalid trip status transition")

	// ErrNotFound is the generic "no row" signal repositories return;
	// callers translate it into a context-specific error, same pattern as
	// internal/identity/repository and internal/ops/domain from Phase 1.
	ErrNotFound = errors.New("fleet: not found")
)
