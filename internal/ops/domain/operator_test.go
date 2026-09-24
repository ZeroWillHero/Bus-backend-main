package domain

import "testing"

func TestCanTransition(t *testing.T) {
	cases := []struct {
		from, to OperatorStatus
		want     bool
	}{
		{StatusPending, StatusApproved, true},
		{StatusPending, StatusRejected, true},
		{StatusPending, StatusSuspended, false},
		{StatusApproved, StatusSuspended, true},
		{StatusApproved, StatusPending, false},
		{StatusApproved, StatusRejected, false},
		{StatusSuspended, StatusApproved, true}, // "reactivate"
		{StatusSuspended, StatusPending, false},
		{StatusSuspended, StatusRejected, false},
		{StatusRejected, StatusApproved, false},
		{StatusRejected, StatusPending, false},
		{StatusRejected, StatusSuspended, false},
	}
	for _, c := range cases {
		got := CanTransition(c.from, c.to)
		if got != c.want {
			t.Errorf("CanTransition(%s, %s) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}
