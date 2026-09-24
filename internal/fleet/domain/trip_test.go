package domain

import "testing"

func TestCanTransitionStatus(t *testing.T) {
	cases := []struct {
		from, to TripStatus
		want     bool
	}{
		{TripStatusScheduled, TripStatusCancelled, true},
		{TripStatusScheduled, TripStatusCompleted, true},
		{TripStatusScheduled, TripStatusScheduled, false},
		{TripStatusCancelled, TripStatusScheduled, false},
		{TripStatusCancelled, TripStatusCompleted, false},
		{TripStatusCompleted, TripStatusCancelled, false},
	}
	for _, c := range cases {
		if got := CanTransitionStatus(c.from, c.to); got != c.want {
			t.Errorf("CanTransitionStatus(%s, %s) = %v, want %v", c.from, c.to, got, c.want)
		}
	}
}

func TestSameOperator(t *testing.T) {
	bus := &Bus{OperatorID: "op-1"}
	route := &Route{OperatorID: "op-1"}
	if !SameOperator("op-1", bus, route) {
		t.Errorf("expected matching operator IDs to satisfy SameOperator")
	}
	if SameOperator("op-1", bus, &Route{OperatorID: "op-2"}) {
		t.Errorf("expected mismatched route operator to fail SameOperator")
	}
	if SameOperator("op-1", &Bus{OperatorID: "op-2"}, route) {
		t.Errorf("expected mismatched bus operator to fail SameOperator")
	}
}
