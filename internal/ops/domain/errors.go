package domain

import "errors"

var (
	ErrOperatorNotFound        = errors.New("ops: operator not found")
	ErrInvalidStatusTransition = errors.New("ops: invalid status transition")
	ErrDuplicateBusinessRegNo  = errors.New("ops: business registration number already in use")
	ErrNotFound                = errors.New("ops: not found")
)
